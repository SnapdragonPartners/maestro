//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"orchestrator/internal/dataplane/gen"
)

// What happens when attachment truncation races a pin — measured, and then
// pinned down (item 6 design, D6a).
//
// The foreign key guarantees the thing that matters: no interleaving leaves
// a pin pointing at a deleted attachment. It does not say which party sees
// which error, and that decides the contract. Neither ordering raises
// 40001, so item 5's serialization retry does not cover this at all.
//
// An earlier version of this test only LOGGED what it observed, which made
// it exactly the vacuous guard it claimed to be: it would have passed if
// 23001 became something else, if the constraint name changed, or if the
// commit stopped failing — the three regressions it exists to catch. Every
// observation is now asserted.
//
// Both orderings run under a controlled barrier, because whichever error
// arises depends on lock acquisition and commit order, and a test that
// merely starts two transactions proves nothing about either.

// The measured contract. These constants ARE the contract the seam's retry
// predicate and error mapping are written against; changing one here means
// changing the handler that reads it.
const (
	// foreignKeyViolation is what the PIN receives when the attachment was
	// already deleted.
	foreignKeyViolation = "23503"
	// restrictViolation is what the TRUNCATION receives when a pin was
	// created after its snapshot. It is NOT 23503, and a handler matching
	// only foreign-key violations misses it.
	restrictViolation = "23001"
	// pinAttachmentConstraint is the constraint both errors name. The retry
	// predicate matches on it as well as on the code, so an unrelated
	// RESTRICT violation is never retried into a misleading exhaustion.
	pinAttachmentConstraint = "retention_pins_attachment_fkey"
)

// pinAuditConstraint is the constraint an audit-artifact pin names. Pins
// have two possible targets and both truncations use the same NOT EXISTS
// plus ON DELETE RESTRICT shape, so both races exist and each has to be
// measured rather than assumed to mirror the other.
const pinAuditConstraint = "retention_pins_audit_target_fkey"

// truncateAttachments and truncateAuditArtifacts run the SHIPPED queries
// inside a caller-controlled transaction.
//
// They used to be handwritten copies of the pass's SQL, because item 6's
// generated queries did not exist when the measurement was first taken.
// A copy measures nothing once it can drift: a change to the shipped
// statement would leave this regression green while the race it describes
// changed underneath it.
func truncateAttachments(ctx context.Context, handle gen.DBTX, org uuid.UUID, before time.Time) error {
	_, err := gen.New(handle).TruncateAttachments(ctx, gen.TruncateAttachmentsParams{
		OrganizationID: toPgUUID(org),
		Before:         pgtype.Timestamptz{Time: before, Valid: true},
	})
	return err
}

func truncateAuditArtifacts(ctx context.Context, handle gen.DBTX, org uuid.UUID, before time.Time) error {
	_, err := gen.New(handle).TruncateAuditArtifacts(ctx, gen.TruncateAuditArtifactsParams{
		OrganizationID: toPgUUID(org),
		Before:         pgtype.Timestamptz{Time: before, Valid: true},
	})
	return err
}

func seedAttachment(t *testing.T, f *fixture, at time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := f.pool.Exec(context.Background(), `
		INSERT INTO binary_attachments (attachment_id, organization_id, object_digest, media_type,
			size_bytes, created_at)
		VALUES ($1, $2, repeat('c', 64), 'application/octet-stream', 7, $3)`,
		id, f.organizationID, at); err != nil {
		t.Fatalf("seed attachment: %v", err)
	}
	return id
}

func pinStatement(holder, attachment, org uuid.UUID) (string, []any) {
	return `INSERT INTO retention_pins (retention_pin_id, organization_id, pinned_by_artifact_id,
			pinned_attachment_id, pinned_digest)
		VALUES (gen_random_uuid(), $1, $2, $3, repeat('c', 64))`,
		[]any{org, holder, attachment}
}

// requirePgError asserts the SQLSTATE and constraint name of a failure.
//
// Both halves matter. The code alone would accept a RESTRICT violation from
// any other foreign key, which the retry predicate must NOT treat as this
// race; the constraint alone would accept a different failure mode against
// the same constraint.
func requirePgError(t *testing.T, err error, wantCode, wantConstraint, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s succeeded; want %s on %s", what, wantCode, wantConstraint)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("%s failed with a non-Postgres error %v; want %s on %s", what, err, wantCode, wantConstraint)
	}
	if pgErr.Code != wantCode {
		t.Errorf("%s returned SQLSTATE %s, want %s: %v", what, pgErr.Code, wantCode, err)
	}
	if pgErr.ConstraintName != wantConstraint {
		t.Errorf("%s named constraint %q, want %q: %v", what, pgErr.ConstraintName, wantConstraint, err)
	}
}

// TestPinRacingAttachmentTruncation fixes the contract for both orderings.
func TestPinRacingAttachmentTruncation(t *testing.T) {
	t.Run("truncate first, pin second", func(t *testing.T) {
		f := newFixture(t)
		ctx := context.Background()
		holder := acceptedOriginal(t, f, `{"title":"holder"}`)
		attachment := seedAttachment(t, f, time.Now().Add(-2*time.Hour))

		truncator, err := f.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
		if err != nil {
			t.Fatalf("begin truncator: %v", err)
		}
		defer func() { _ = truncator.Rollback(ctx) }()
		if err := truncateAttachments(ctx, truncator, f.organizationID, time.Now()); err != nil {
			t.Fatalf("truncate: %v", err)
		}

		// The pin now blocks on the uncommitted delete. Run it in a
		// goroutine and let the truncator commit underneath it.
		pinErr := make(chan error, 1)
		go func() {
			sql, args := pinStatement(holder.ArtifactID, attachment, f.organizationID)
			_, execErr := f.pool.Exec(context.Background(), sql, args...)
			pinErr <- execErr
		}()
		waitForLockWait(t, f)
		if err := truncator.Commit(ctx); err != nil {
			t.Fatalf("commit truncator: %v", err)
		}

		select {
		case err := <-pinErr:
			requirePgError(t, err, foreignKeyViolation, pinAttachmentConstraint, "the pin")
		case <-time.After(30 * time.Second):
			t.Fatal("the pin never returned")
		}

		// And the attachment really is gone: a pin that failed because the
		// row still existed would be a different result with the same error.
		if rowExists(t, f, "binary_attachments", "attachment_id", attachment) {
			t.Error("the attachment survived the truncation, so the pin failed for another reason")
		}
	})

	t.Run("pin first, truncate second", func(t *testing.T) {
		f := newFixture(t)
		ctx := context.Background()
		holder := acceptedOriginal(t, f, `{"title":"holder"}`)
		attachment := seedAttachment(t, f, time.Now().Add(-2*time.Hour))

		truncator, err := f.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
		if err != nil {
			t.Fatalf("begin truncator: %v", err)
		}
		defer func() { _ = truncator.Rollback(ctx) }()

		// Establish the snapshot BEFORE the pin exists, so the pass's
		// NOT EXISTS is evaluated against a state in which the row is
		// unpinned. Without this the test proves only that a committed pin
		// is visible to a later snapshot, which is not the race.
		var seen int
		if err := truncator.QueryRow(ctx,
			`SELECT count(*) FROM binary_attachments WHERE organization_id = $1`,
			f.organizationID).Scan(&seen); err != nil {
			t.Fatalf("establish snapshot: %v", err)
		}

		sql, args := pinStatement(holder.ArtifactID, attachment, f.organizationID)
		if _, err := f.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("pin: %v", err)
		}

		truncErr := truncateAttachments(ctx, truncator, f.organizationID, time.Now())
		requirePgError(t, truncErr, restrictViolation, pinAttachmentConstraint, "the truncation")

		// The whole pass is lost, not just the statement: the surrounding
		// commit fails as a rollback. That is why the retry has to be
		// whole-operation rather than per-statement.
		if commitErr := truncator.Commit(ctx); commitErr == nil {
			t.Error("the transaction committed after its DELETE was aborted")
		}

		if !rowExists(t, f, "binary_attachments", "attachment_id", attachment) {
			t.Error("the pinned attachment was deleted; a pin points at nothing")
		}
	})
}

// TestPinRacingAuditArtifactTruncation is the same race on the OTHER pin
// target, and it is measured rather than assumed to mirror the first.
//
// Pins point at an Audit artifact or an attachment, and audit_artifacts has
// been in the truncation pass since item 5 with the same NOT EXISTS plus
// ON DELETE RESTRICT shape. So this race has existed all along; what was
// missing was any handler for it. Both orderings are fixed here for the
// same reason the attachment pair is: the codes decide the retry predicate,
// and a predicate written from the attachment case alone would let one
// concurrent audit pin kill an entire pass.
func TestPinRacingAuditArtifactTruncation(t *testing.T) {
	t.Run("truncate first, pin second", func(t *testing.T) {
		f := newFixture(t)
		ctx := context.Background()
		holder := acceptedOriginal(t, f, `{"title":"holder"}`)
		audit := seedAgedAuditArtifact(t, f)

		truncator, err := f.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
		if err != nil {
			t.Fatalf("begin truncator: %v", err)
		}
		defer func() { _ = truncator.Rollback(ctx) }()
		if err := truncateAuditArtifacts(ctx, truncator, f.organizationID, time.Now()); err != nil {
			t.Fatalf("truncate: %v", err)
		}

		pinErr := make(chan error, 1)
		go func() {
			sql, args := auditPinStatement(holder.ArtifactID, audit, f.organizationID)
			_, execErr := f.pool.Exec(context.Background(), sql, args...)
			pinErr <- execErr
		}()
		waitForLockWait(t, f)
		if err := truncator.Commit(ctx); err != nil {
			t.Fatalf("commit truncator: %v", err)
		}

		select {
		case err := <-pinErr:
			requirePgError(t, err, foreignKeyViolation, pinAuditConstraint, "the pin")
		case <-time.After(30 * time.Second):
			t.Fatal("the pin never returned")
		}
		if rowExists(t, f, "audit_artifacts", "artifact_id", audit) {
			t.Error("the audit artifact survived the truncation, so the pin failed for another reason")
		}
	})

	t.Run("pin first, truncate second", func(t *testing.T) {
		f := newFixture(t)
		ctx := context.Background()
		holder := acceptedOriginal(t, f, `{"title":"holder"}`)
		audit := seedAgedAuditArtifact(t, f)

		truncator, err := f.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
		if err != nil {
			t.Fatalf("begin truncator: %v", err)
		}
		defer func() { _ = truncator.Rollback(ctx) }()

		// The snapshot is established BEFORE the pin exists, so the pass's
		// NOT EXISTS is evaluated against a state in which the row is
		// unpinned. Without this the test proves only that a committed pin
		// is visible to a later snapshot, which is not the race.
		var seen int
		if err := truncator.QueryRow(ctx,
			`SELECT count(*) FROM audit_artifacts WHERE organization_id = $1`,
			f.organizationID).Scan(&seen); err != nil {
			t.Fatalf("establish snapshot: %v", err)
		}

		sql, args := auditPinStatement(holder.ArtifactID, audit, f.organizationID)
		if _, err := f.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("pin: %v", err)
		}

		truncErr := truncateAuditArtifacts(ctx, truncator, f.organizationID, time.Now())
		requirePgError(t, truncErr, restrictViolation, pinAuditConstraint, "the truncation")

		if commitErr := truncator.Commit(ctx); commitErr == nil {
			t.Error("the transaction committed after its DELETE was aborted")
		}
		if !rowExists(t, f, "audit_artifacts", "artifact_id", audit) {
			t.Error("the pinned audit artifact was deleted; a pin points at nothing")
		}
	})
}

// seedAgedAuditArtifact reuses the truncation suite's seeder, which already
// writes a row past the horizon, and reports the digest a pin on it must
// bind.
func seedAgedAuditArtifact(t *testing.T, f *fixture) uuid.UUID {
	t.Helper()
	return seedAuditArtifact(t, f, f.organizationID, nil)
}

// auditPinDigest is the digest that seeder writes. A pin binding anything
// else would be refused for a reason this test is not about.
const auditPinDigest = "repeat('a', 64)"

func auditPinStatement(holder, audit, org uuid.UUID) (string, []any) {
	return `INSERT INTO retention_pins (retention_pin_id, organization_id, pinned_by_artifact_id,
			pinned_audit_artifact_id, pinned_digest)
		VALUES (gen_random_uuid(), $1, $2, $3, ` + auditPinDigest + `)`,
		[]any{org, holder, audit}
}
