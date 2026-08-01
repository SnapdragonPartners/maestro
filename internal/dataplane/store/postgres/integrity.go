package postgres

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"orchestrator/internal/dataplane/canonical"
	"orchestrator/internal/dataplane/gen"
	"orchestrator/internal/dataplane/objects"
	"orchestrator/internal/dataplane/store"
)

// Problem kinds, named so a report can be filtered without matching prose.
const (
	problemPayloadDigest = "payload-digest"
	problemReviewDigest  = "review-digest"
	problemAttachment    = "attachment"
)

// skippedOutcome marks an attachment a concurrent truncation removed
// between the listing and the read.
const skippedOutcome = "skipped"

// Verify recomputes every stored digest and reads every attachment.
//
// This is what a restore is validated by. A whole-root copy's characteristic
// failure is a TORN PAIR — a Postgres cluster and an object store captured
// at moments that disagree — and neither store can detect that alone: the
// database is internally consistent, the object store is internally
// consistent, and they disagree with each other. Recomputing digests across
// both is the only thing that observes it.
//
// It walks organizations rather than reaching across them, so every
// statement except the organization list stays tenant-scoped exactly like
// the rest of the query surface.
func (s *Store) Verify(ctx context.Context) (store.VerifyReport, error) {
	organizations, err := s.queries.ListOrganizations(ctx)
	if err != nil {
		return store.VerifyReport{}, fmt.Errorf("list organizations: %w", err)
	}

	report := store.VerifyReport{Organizations: len(organizations)}
	for i := range organizations {
		organizationID := fromUUID(organizations[i].OrganizationID)
		if orgErr := s.verifyOrganization(ctx, organizationID, &report); orgErr != nil {
			return store.VerifyReport{}, orgErr
		}
	}
	return report, nil
}

// organizationRows is one organization's records as of a single moment.
type organizationRows struct {
	management  []gen.ManagementArtifact
	audit       []gen.AuditArtifact
	attachments []gen.BinaryAttachment
}

func (s *Store) verifyOrganization(ctx context.Context, organizationID uuid.UUID, report *store.VerifyReport) error {
	rows, err := s.snapshotOrganization(ctx, organizationID)
	if err != nil {
		return err
	}

	report.ManagementArtifacts += len(rows.management)
	report.AuditArtifacts += len(rows.audit)
	report.Attachments += len(rows.attachments)

	for i := range rows.management {
		verifyManagementDigests(&rows.management[i], organizationID, report)
	}
	for i := range rows.audit {
		artifact := auditFromRow(&rows.audit[i])
		checkPayloadDigest(artifact.Payload, artifact.PayloadDigest, organizationID, artifact.ArtifactID, report)
	}
	for i := range rows.attachments {
		if err := s.verifyAttachmentBlob(ctx, organizationID, &rows.attachments[i], report); err != nil {
			return err
		}
	}
	return nil
}

// snapshotOrganization materialises the row set under one REPEATABLE READ
// transaction and COMMITS before anything is read.
//
// The commit is the load-bearing part. Holding the listing transaction open
// across the attachment reads would make the later existence recheck run
// against the LISTING's snapshot, where the row still exists — so the
// recheck could never observe the deletion it exists to detect, and the
// skipped outcome would be unreachable. A recheck that cannot fail is
// decoration.
func (s *Store) snapshotOrganization(ctx context.Context, organizationID uuid.UUID) (organizationRows, error) {
	pgxTx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return organizationRows{}, fmt.Errorf("begin verification snapshot: %w", err)
	}
	defer func() { _ = pgxTx.Rollback(ctx) }()

	queries := s.queries.WithTx(pgxTx)
	scoped := toUUID(organizationID)

	var rows organizationRows
	if rows.management, err = queries.ListManagementArtifactsForVerify(ctx, scoped); err != nil {
		return organizationRows{}, fmt.Errorf("list management artifacts: %w", err)
	}
	if rows.audit, err = queries.ListAuditArtifactsForVerify(ctx, scoped); err != nil {
		return organizationRows{}, fmt.Errorf("list audit artifacts: %w", err)
	}
	if rows.attachments, err = queries.ListBinaryAttachmentsForVerify(ctx, scoped); err != nil {
		return organizationRows{}, fmt.Errorf("list attachments: %w", err)
	}

	if err := pgxTx.Commit(ctx); err != nil {
		return organizationRows{}, fmt.Errorf("close verification snapshot: %w", err)
	}
	return rows, nil
}

// verifyManagementDigests recomputes BOTH digests a Management artifact
// carries.
//
// Both, not just the payload: review_digest binds the structure a reviewer
// actually approved, and an accepted artifact's authority rests on it.
// Checking only the payload would leave the binding — the thing that makes
// acceptance mean anything — unverified.
func verifyManagementDigests(row *gen.ManagementArtifact, organizationID uuid.UUID, report *store.VerifyReport) {
	artifact := managementFromRow(row)
	checkPayloadDigest(artifact.Payload, artifact.PayloadDigest, organizationID, artifact.ArtifactID, report)

	recomputed, err := canonical.Digest(reviewableProjectionOf(&artifact))
	switch {
	case err != nil:
		addProblem(report, problemReviewDigest, organizationID, artifact.ArtifactID,
			fmt.Sprintf("review digest could not be recomputed: %v", err))
	case recomputed != artifact.ReviewDigest:
		addProblem(report, problemReviewDigest, organizationID, artifact.ArtifactID,
			fmt.Sprintf("stored %s, recomputed %s", artifact.ReviewDigest, recomputed))
	}
}

func checkPayloadDigest(payload []byte, stored string, organizationID, id uuid.UUID, report *store.VerifyReport) {
	recomputed, err := canonical.DigestJSON(payload)
	switch {
	case err != nil:
		addProblem(report, problemPayloadDigest, organizationID, id,
			fmt.Sprintf("payload digest could not be recomputed: %v", err))
	case recomputed != stored:
		addProblem(report, problemPayloadDigest, organizationID, id,
			fmt.Sprintf("stored %s, recomputed %s", stored, recomputed))
	}
}

func addProblem(report *store.VerifyReport, kind string, organizationID, id uuid.UUID, detail string) {
	report.Problems = append(report.Problems, store.VerifyProblem{
		Kind: kind, OrganizationID: organizationID, ID: id, Detail: detail,
	})
}

// verifyAttachmentBlob reads one attachment's bytes through the object
// module, which verifies the content digest as it streams.
//
// The read happens INSIDE a transaction holding the per-(organization,
// digest) advisory lock — the same lock writers and the sweep serialise on.
// The sweep establishes "unreferenced" under that lock, so while it is held
// the object cannot be concluded prunable and deleted mid-read.
//
// Under that lock, and in the CURRENT snapshot rather than the listing's,
// the row is rechecked. A row a concurrent truncation legitimately removed
// since the listing is reported as SKIPPED rather than as damage: a
// verifier that cried wolf about a healthy plane would teach its operator
// to stop believing it.
func (s *Store) verifyAttachmentBlob(
	ctx context.Context, organizationID uuid.UUID, row *gen.BinaryAttachment, report *store.VerifyReport,
) error {
	attachment := attachmentFromRow(row)

	outcome, err := inTx(ctx, s, func(t *tx) (string, error) {
		if lockErr := t.queries.TakeDigestLock(ctx, digestLockKey(organizationID, attachment.Digest)); lockErr != nil {
			return "", fmt.Errorf("take digest lock: %w", lockErr)
		}
		// Rechecked in the CURRENT snapshot, under the lock. A row that has
		// gone since the listing was taken is a legitimate truncation, not
		// damage.
		_, existsErr := t.queries.GetBinaryAttachment(ctx, gen.GetBinaryAttachmentParams{
			AttachmentID:   toUUID(attachment.AttachmentID),
			OrganizationID: toUUID(organizationID),
		})
		if errors.Is(existsErr, pgx.ErrNoRows) {
			return skippedOutcome, nil
		}
		if existsErr != nil {
			return "", fmt.Errorf("recheck attachment %s: %w", attachment.AttachmentID, existsErr)
		}
		return "", s.drainBlob(ctx, organizationID, attachment.Digest)
	})

	switch {
	case outcome == skippedOutcome:
		report.Skipped++
		report.Attachments--
		return nil
	case err != nil && isBlobProblem(err):
		addProblem(report, problemAttachment, organizationID, attachment.AttachmentID, err.Error())
		return nil
	case err != nil:
		// Anything that is not a statement about the blob is a failure of
		// the verification itself, and must not be reported as a finding
		// about the plane.
		return err
	}
	return nil
}

// drainBlob reads an object to EOF through the verifying reader.
//
// To EOF deliberately: the digest is computed as the stream is consumed, so
// a reader nobody finishes has verified nothing. Stopping early would make
// every attachment pass.
func (s *Store) drainBlob(ctx context.Context, organizationID uuid.UUID, digest string) error {
	body, err := s.blob.Get(ctx, objectKey(organizationID, digest))
	if err != nil {
		if errors.Is(err, objects.ErrNoSuchObject) {
			return fmt.Errorf("%w: object %s is not stored", store.ErrInvariant, digest)
		}
		return fmt.Errorf("open object %s: %w", digest, err)
	}
	defer func() { _ = body.Close() }()

	if _, err := io.Copy(io.Discard, newVerifyingReader(body, digest)); err != nil {
		return fmt.Errorf("%w: object %s did not read back: %w", store.ErrInvariant, digest, err)
	}
	return nil
}

// isBlobProblem distinguishes a finding about the plane from a failure of
// the verifier itself.
func isBlobProblem(err error) bool {
	return errors.Is(err, store.ErrInvariant)
}
