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
)

// What happens when attachment truncation races a pin, measured rather than
// asserted (item 6 design, D6a).
//
// The foreign key guarantees the thing that matters — no interleaving
// leaves a pin pointing at a deleted attachment — but it does not say which
// party sees which error, and that decides the contract: a serialization
// failure joins item 5's existing retry, while a foreign-key violation has
// to be mapped at the seam into a diagnostic naming the attachment.
//
// Both orderings run under a controlled barrier, because whichever error
// arises depends on lock acquisition and commit order, and a test that
// merely starts two transactions proves nothing about either.

// attachmentTruncation is item 5's pass restricted to the one table item 6
// adds: organization-scoped, horizon-bounded, pinned rows excluded in the
// WHERE rather than discovered at commit.
const attachmentTruncation = `
DELETE FROM binary_attachments a
WHERE a.organization_id = $1
  AND a.created_at      < $2
  AND NOT EXISTS (
      SELECT 1 FROM retention_pins p
      WHERE p.pinned_attachment_id = a.attachment_id
        AND p.organization_id      = a.organization_id)`

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

func pinStatement(holder, attachment uuid.UUID, org uuid.UUID) (string, []any) {
	return `INSERT INTO retention_pins (retention_pin_id, organization_id, pinned_by_artifact_id,
			pinned_attachment_id, pinned_digest)
		VALUES (gen_random_uuid(), $1, $2, $3, repeat('c', 64))`,
		[]any{org, holder, attachment}
}

// sqlstate reports the SQLSTATE of a Postgres error, or "" for anything else.
func sqlstate(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

// TestPinRacingAttachmentTruncation records the contract for both orderings.
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
		if _, err := truncator.Exec(ctx, attachmentTruncation, f.organizationID, time.Now()); err != nil {
			t.Fatalf("truncate: %v", err)
		}

		// The pin now blocks on the uncommitted delete. Run it in a
		// goroutine and let the truncator commit underneath it.
		pinErr := make(chan error, 1)
		go func() {
			sql, args := pinStatement(holder.ArtifactID, attachment, f.organizationID)
			_, err := f.pool.Exec(context.Background(), sql, args...)
			pinErr <- err
		}()
		waitForLockWait(t, f)
		if err := truncator.Commit(ctx); err != nil {
			t.Fatalf("commit truncator: %v", err)
		}

		select {
		case err := <-pinErr:
			t.Logf("OBSERVED pin-after-truncate: sqlstate=%q err=%v", sqlstate(err), err)
			if err == nil {
				t.Fatal("the pin succeeded against a deleted attachment; the foreign key did not hold")
			}
		case <-time.After(30 * time.Second):
			t.Fatal("the pin never returned")
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

		_, truncErr := truncator.Exec(ctx, attachmentTruncation, f.organizationID, time.Now())
		commitErr := truncator.Commit(ctx)
		t.Logf("OBSERVED truncate-after-pin: delete sqlstate=%q err=%v", sqlstate(truncErr), truncErr)
		t.Logf("OBSERVED truncate-after-pin: commit sqlstate=%q err=%v", sqlstate(commitErr), commitErr)

		var survives bool
		if err := f.pool.QueryRow(context.Background(),
			`SELECT EXISTS (SELECT 1 FROM binary_attachments WHERE attachment_id = $1)`,
			attachment).Scan(&survives); err != nil {
			t.Fatalf("check survival: %v", err)
		}
		if !survives {
			t.Error("the pinned attachment was deleted; a pin points at nothing")
		}
	})
}
