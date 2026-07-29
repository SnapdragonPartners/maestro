package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"orchestrator/internal/dataplane/gen"
	"orchestrator/internal/dataplane/objects"
	"orchestrator/internal/dataplane/store"
)

// Key layout (design D3). Organization-first, which costs deduplication
// across tenants and buys a deletion rule that is not a cross-tenant
// reference count -- the query most likely to be wrong and least likely to
// be noticed. The two-level fan-out is for filesystem-backed stores, where
// one directory holding every object is a known pathology.
const (
	stagingPrefix = "staging/"
	fanOutWidth   = 2
)

// Lease timings.
//
// The term is short and RENEWED rather than long and hoped-for: a term
// sized to the longest imaginable upload is a guess about someone else's
// hardware, and the design already rejects reasoning from a constant. What
// the term actually bounds is how long an abandoned staging object waits
// before cleanup may consider it -- so a writer that dies leaves a mess for
// two minutes rather than an hour.
const (
	leaseTerm = 2 * time.Minute
	// Renewals happen at a third of the term, so two consecutive failures
	// still leave time for a third attempt before the lease lapses.
	leaseRenewInterval = leaseTerm / 3
)

// objectKey is the digest's address: <organization>/<aa>/<bb>/<digest>.
func objectKey(organizationID uuid.UUID, digest string) string {
	return fmt.Sprintf("%s/%s/%s/%s", organizationID, digest[:fanOutWidth],
		digest[fanOutWidth:fanOutWidth*2], digest)
}

// stagingKeyFor is `staging/<organization>/<uuid>`, the exact shape the
// lease table's own CHECK enforces -- unique per upload, so a cleanup that
// enumerates it can never reach another writer's object.
func stagingKeyFor(organizationID, uploadID uuid.UUID) string {
	return stagingPrefix + organizationID.String() + "/" + uploadID.String()
}

// digestLockKey derives the advisory-lock key for one (organization,
// digest) from the first eight bytes of their hash.
//
// Sixty-four bits of a hash into a space this small will collide
// eventually, and a collision serialises two unrelated digests: it costs
// concurrency and nothing else, which is the direction a lock is allowed to
// fail in. The alternative -- a lock table with real rows -- would need its
// own retention and its own cleanup.
func digestLockKey(organizationID uuid.UUID, digest string) int64 {
	sum := sha256.Sum256([]byte(organizationID.String() + "/" + digest))
	return int64(binary.BigEndian.Uint64(sum[:8])) //nolint:gosec // the sign carries no meaning; the key is opaque
}

// AttachmentExists answers without transferring the object.
//
// It checks BOTH halves, because the question acceptance asks is whether
// the evidence exists -- and a row whose object is gone is precisely the
// state that would let missing evidence pass as present (design D5). The
// two failures are reported differently on purpose: no row is an ordinary
// false, while a row without its object is the store contradicting itself,
// which ADR 0021 requires to fail loudly rather than weaken a proof.
func (s *Store) AttachmentExists(ctx context.Context, organizationID, attachmentID uuid.UUID) (bool, error) {
	row, err := s.queries.GetBinaryAttachment(ctx, gen.GetBinaryAttachmentParams{
		AttachmentID:   toUUID(attachmentID),
		OrganizationID: toUUID(organizationID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("check attachment %s: %w", attachmentID, err)
	}

	stored, err := s.blob.Exists(ctx, objectKey(organizationID, row.ObjectDigest))
	if err != nil {
		return false, fmt.Errorf("check object for attachment %s: %w", attachmentID, err)
	}
	if !stored {
		return false, fmt.Errorf("%w: attachment %s references object %s, which is not stored",
			store.ErrInvariant, attachmentID, row.ObjectDigest)
	}
	return true, nil
}

// GetAttachment streams an attachment's bytes, verifying them.
func (s *Store) GetAttachment(
	ctx context.Context, organizationID, attachmentID uuid.UUID,
) (io.ReadCloser, *store.Attachment, error) {
	row, err := s.queries.GetBinaryAttachment(ctx, gen.GetBinaryAttachmentParams{
		AttachmentID:   toUUID(attachmentID),
		OrganizationID: toUUID(organizationID),
	})
	if err != nil {
		return nil, nil, notFound(err, "attachment", attachmentID)
	}
	attachment := attachmentFromRow(&row)

	body, err := s.blob.Get(ctx, objectKey(organizationID, attachment.Digest))
	if err != nil {
		if errors.Is(err, objects.ErrNoSuchObject) {
			// The row exists and the object does not. ADR 0021 requires a
			// dangling reference to fail verification rather than quietly
			// weaken the proof, and this is the store contradicting its own
			// records -- not a caller naming something that never existed.
			return nil, nil, fmt.Errorf("%w: attachment %s references object %s, which is not stored",
				store.ErrInvariant, attachmentID, attachment.Digest)
		}
		return nil, nil, fmt.Errorf("open attachment %s: %w", attachmentID, err)
	}
	return newVerifyingReader(body, attachment.Digest), &attachment, nil
}

// PutAttachment stores bytes at their digest and records the row that makes
// them reachable (design D2 and D6).
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (s *Store) PutAttachment(ctx context.Context, input store.PutAttachmentInput) (*store.Attachment, error) {
	if err := requireDigest(input.Digest); err != nil {
		return nil, err
	}
	if err := requireMediaType(input.MediaType); err != nil {
		return nil, err
	}
	if input.SizeBytes < 0 {
		return nil, fmt.Errorf("size_bytes is %d: a negative size cannot describe any object",
			input.SizeBytes)
	}
	if input.Body == nil {
		return nil, errors.New("body is nil: there is nothing to store")
	}
	attachmentID, err := newIdentifier(input.AttachmentID)
	if err != nil {
		return nil, err
	}

	// The shortcut first, because on a content-addressed store the object
	// usually already exists. It VERIFIES rather than trusting: the digest
	// key is exactly where a previously corrupted or half-promoted object
	// would sit, and returning success would bless it into a new row.
	existing, applied, err := s.putOverExisting(ctx, &input, attachmentID)
	if err != nil {
		return nil, err
	}
	if applied {
		return existing, nil
	}
	return s.putNewObject(ctx, &input, attachmentID)
}

// putOverExisting takes the digest lock and, if the object is already
// stored and verifies, records the row without transferring anything.
//
// The lock is held through the INSERT, not merely through the check. Round
// 2 left this path outside the protocol entirely, so a sweep could delete
// the object between its verification and its attachment row -- producing
// exactly the dangling reference the protocol exists to prevent, on the
// path most likely to be taken.
func (s *Store) putOverExisting(
	ctx context.Context, input *store.PutAttachmentInput, attachmentID uuid.UUID,
) (*store.Attachment, bool, error) {
	type outcome struct {
		attachment *store.Attachment
		applied    bool
	}
	result, err := inTx(ctx, s, func(t *tx) (outcome, error) {
		if lockErr := t.queries.TakeDigestLock(ctx, digestLockKey(input.OrganizationID, input.Digest)); lockErr != nil {
			return outcome{}, fmt.Errorf("take digest lock: %w", lockErr)
		}

		// A live claim forbids the shortcut. The current version may vanish
		// under an in-flight delete this lock cannot cancel, and PutAttachment
		// always has the source bytes, so the full path is always available.
		claimed, claimErr := t.queries.LiveDeletionClaimExists(ctx, gen.LiveDeletionClaimExistsParams{
			OrganizationID: toUUID(input.OrganizationID),
			ObjectDigest:   input.Digest,
		})
		if claimErr != nil {
			return outcome{}, fmt.Errorf("check deletion claim: %w", claimErr)
		}
		if claimed {
			return outcome{}, nil
		}

		switch err := s.verifyStored(ctx, input.OrganizationID, input.Digest); {
		case errors.Is(err, objects.ErrNoSuchObject):
			// Nothing there: the full path uploads it. The source has not
			// been touched, which is what keeps that path available.
			return outcome{}, nil
		case err != nil:
			return outcome{}, err
		}

		// The stored object being correct says nothing about the CALLER's
		// bytes. Skipping the upload must not also skip the contract: an
		// unread source could be shorter, longer, or unrelated, and the row
		// would record a size nobody measured. So it is read and proven
		// here, exactly as the upload path proves it -- the difference is
		// only that these bytes are discarded rather than sent.
		//
		// After the stored verification, not before: a source consumed on a
		// path that then discovers it must upload after all is a source
		// nothing can rewind.
		if err := drainAndCheckSource(input); err != nil {
			return outcome{}, err
		}

		attachment, insertErr := t.insertAttachment(ctx, input, attachmentID)
		if insertErr != nil {
			return outcome{}, insertErr
		}
		return outcome{attachment: attachment, applied: true}, nil
	})
	if err != nil {
		return nil, false, err
	}
	return result.attachment, result.applied, nil
}

// putNewObject uploads to staging under a lease, then promotes under the
// digest lock and the lease's row lock together.
func (s *Store) putNewObject(
	ctx context.Context, input *store.PutAttachmentInput, attachmentID uuid.UUID,
) (*store.Attachment, error) {
	uploadID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("allocate staging id: %w", err)
	}
	stagingKey := stagingKeyFor(input.OrganizationID, uploadID)
	token, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("allocate lease token: %w", err)
	}

	leaseID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("allocate lease id: %w", err)
	}
	if _, err = s.queries.CreateStagingLease(ctx, gen.CreateStagingLeaseParams{
		StagingLeaseID: toUUID(leaseID),
		OrganizationID: toUUID(input.OrganizationID),
		StagingKey:     stagingKey,
		OwnerToken:     toUUID(token),
		TermSeconds:    leaseTerm.Seconds(),
	}); err != nil {
		return nil, fmt.Errorf("take staging lease: %w", err)
	}
	// Released whatever happens: an abandoned lease is storage nobody
	// reclaims until it expires, and on the failure paths below there is
	// nothing left to protect.
	defer s.releaseStaging(ctx, input.OrganizationID, stagingKey, token)

	stagedVersion, err := s.uploadStaged(ctx, input, stagingKey, token)
	if err != nil {
		return nil, err
	}

	return inTx(ctx, s, func(t *tx) (*store.Attachment, error) {
		if lockErr := t.queries.TakeDigestLock(ctx, digestLockKey(input.OrganizationID, input.Digest)); lockErr != nil {
			return nil, fmt.Errorf("take digest lock: %w", lockErr)
		}
		// The lease row is LOCKED for the rest of this transaction, which
		// spans the promote, the read-back and the insert. Checking
		// ownership and then promoting would leave the window open: those
		// are remote calls, the lease can lapse while they run, and cleanup
		// would then be free to delete the staging object out from under an
		// authorised promotion.
		locked, lockErr := t.queries.LockStagingLease(ctx, gen.LockStagingLeaseParams{
			OrganizationID: toUUID(input.OrganizationID),
			StagingKey:     stagingKey,
		})
		if lockErr != nil {
			if errors.Is(lockErr, pgx.ErrNoRows) {
				return nil, fmt.Errorf("%w: lease on %s is gone", store.ErrLeaseLost, stagingKey)
			}
			return nil, fmt.Errorf("lock staging lease: %w", lockErr)
		}
		if err := leaseHeld(&locked, token); err != nil {
			return nil, err
		}

		promoted, promoteErr := s.blob.Promote(ctx, stagingKey, stagedVersion,
			objectKey(input.OrganizationID, input.Digest))
		if promoteErr != nil {
			return nil, fmt.Errorf("promote staged object: %w", promoteErr)
		}
		_ = promoted

		// A copy landing intact is a claim like any other, and the server
		// checksum cannot answer it: a multipart copy's value is a
		// composite checksum-of-checksums, not this object's digest.
		if err := s.verifyStored(ctx, input.OrganizationID, input.Digest); err != nil {
			return nil, err
		}
		return t.insertAttachment(ctx, input, attachmentID)
	})
}

// uploadStaged streams the body to its staging key, proving length and
// content locally on the way past.
//
// The upload happens WITHOUT the digest lock -- it is the long part, and
// holding a database lock across it would serialise every writer in the
// organization behind the slowest one.
func (s *Store) uploadStaged(
	ctx context.Context, input *store.PutAttachmentInput, stagingKey string, token uuid.UUID,
) (string, error) {
	uploadCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	stopRenewals := s.renewLeaseUntilDone(uploadCtx, cancel, input.OrganizationID, stagingKey, token)

	source := newCountingHasher(input.Body, input.SizeBytes)
	version, err := s.blob.PutStaged(uploadCtx, stagingKey, input.SizeBytes, source)
	stopRenewals()
	if err != nil {
		// A lost lease cancelled the upload, so the transport error is a
		// symptom; report the cause the writer must act on.
		if cause := context.Cause(uploadCtx); cause != nil && !errors.Is(cause, context.Canceled) {
			return "", fmt.Errorf("upload abandoned: %w", cause)
		}
		// A source that ended early fails the upload too, since the
		// uploader is promised more bytes than it can read. Which of the
		// two happened is worth saying: one is the caller's input, the
		// other is the network.
		if source.sourceEndedEarly() {
			return "", fmt.Errorf("%w: source ended after %d bytes, stated %d",
				store.ErrSizeMismatch, source.read, input.SizeBytes)
		}
		return "", fmt.Errorf("upload to staging: %w", err)
	}

	if err := checkSource(source, input); err != nil {
		return "", err
	}
	return version, nil
}

// checkSource proves the caller's bytes are the length and the content
// they were claimed to be.
//
// Both paths use it, so the contract cannot drift between the write that
// uploads and the write that recognises an object it already holds.
func checkSource(source *countingHasher, input *store.PutAttachmentInput) error {
	if source.read != input.SizeBytes {
		return fmt.Errorf("%w: read %d bytes, stated %d",
			store.ErrSizeMismatch, source.read, input.SizeBytes)
	}
	// "We stopped at size" is not the same claim as "the source ended".
	// A longer source would otherwise be stored as a truncation whose
	// digest matched nothing the caller has.
	exhausted, err := source.exhausted()
	if err != nil {
		return err
	}
	if !exhausted {
		return fmt.Errorf("%w: source is longer than the stated %d bytes",
			store.ErrSizeMismatch, input.SizeBytes)
	}
	if got := source.digest(); got != input.Digest {
		return fmt.Errorf("%w: source hashes to %s, claimed %s",
			store.ErrContentMismatch, got, input.Digest)
	}
	return nil
}

// drainAndCheckSource reads the caller's source without storing it, and
// holds it to the same contract as an uploaded one.
func drainAndCheckSource(input *store.PutAttachmentInput) error {
	source := newCountingHasher(input.Body, input.SizeBytes)
	if _, err := io.Copy(io.Discard, source); err != nil {
		return fmt.Errorf("read source: %w", err)
	}
	return checkSource(source, input)
}

// renewLeaseUntilDone keeps the lease alive while a long upload runs, and
// cancels the upload the moment it cannot.
//
// Cancelling is the point. A writer whose lease has lapsed must not go on
// uploading to a staging key that cleanup is now entitled to empty -- and
// it must not discover this at promotion time, having paid for the whole
// transfer.
func (s *Store) renewLeaseUntilDone(
	ctx context.Context, cancel context.CancelCauseFunc,
	organizationID uuid.UUID, stagingKey string, token uuid.UUID,
) func() {
	stop := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		ticker := time.NewTicker(leaseRenewInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, err := s.queries.RenewStagingLease(ctx, gen.RenewStagingLeaseParams{
					OrganizationID: toUUID(organizationID),
					StagingKey:     stagingKey,
					OwnerToken:     toUUID(token),
					TermSeconds:    leaseTerm.Seconds(),
				})
				switch {
				case errors.Is(err, pgx.ErrNoRows):
					// Zero rows means this token no longer holds the lease,
					// or it has already lapsed. There is no re-insert: a
					// lease cannot be resurrected by the actor that lost it.
					cancel(fmt.Errorf("%w: renewal for %s matched no live lease",
						store.ErrLeaseLost, stagingKey))
					return
				case err != nil:
					if ctx.Err() != nil {
						return
					}
					cancel(fmt.Errorf("renew staging lease: %w", err))
					return
				}
			}
		}
	}()

	return func() {
		close(stop)
		// Waited for, not merely signalled: the promotion that follows
		// locks this lease row, and a renewal still in flight would be a
		// second writer to it.
		<-done
	}
}

// leaseHeld checks ownership and expiry against the server's own clock,
// read under the row lock.
func leaseHeld(locked *gen.LockStagingLeaseRow, token uuid.UUID) error {
	if fromUUID(locked.StagingLease.OwnerToken) != token {
		return fmt.Errorf("%w: lease on %s is held by another writer",
			store.ErrLeaseLost, locked.StagingLease.StagingKey)
	}
	if !fromTimestamptz(locked.StagingLease.ExpiresAt).After(fromTimestamptz(locked.LockedAt)) {
		return fmt.Errorf("%w: lease on %s expired at %s",
			store.ErrLeaseLost, locked.StagingLease.StagingKey,
			fromTimestamptz(locked.StagingLease.ExpiresAt))
	}
	return nil
}

// releaseStaging drops the lease row and the staging object.
//
// Failure is LOGGED rather than returned: by the time this runs the
// attachment row is committed and the write has succeeded, and reporting a
// cleanup failure as a write failure would tell a caller to retry an
// operation that already happened. What is left behind -- a lease row and
// one staging object -- is exactly what staging cleanup exists to collect.
func (s *Store) releaseStaging(ctx context.Context, organizationID uuid.UUID, stagingKey string, token uuid.UUID) {
	// A fresh context: the caller's may already be cancelled by the error
	// that brought us here, and the release would then never be attempted.
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()

	logger := slog.Default().With("staging_key", stagingKey, "organization_id", organizationID)

	versions, err := s.blob.ListVersions(releaseCtx, stagingKey)
	if err != nil {
		logger.WarnContext(releaseCtx, "could not list staging versions to release", "error", err)
	}
	for i := range versions {
		if delErr := s.blob.DeleteVersion(releaseCtx, versions[i].Key, versions[i].VersionID); delErr != nil {
			logger.WarnContext(releaseCtx, "could not delete staging object",
				"version_id", versions[i].VersionID, "error", delErr)
		}
	}

	if _, err := s.queries.DeleteStagingLease(releaseCtx, gen.DeleteStagingLeaseParams{
		OrganizationID: toUUID(organizationID),
		StagingKey:     stagingKey,
		OwnerToken:     toUUID(token),
	}); err != nil {
		logger.WarnContext(releaseCtx, "could not release staging lease", "error", err)
	}
}

// releaseTimeout bounds the best-effort cleanup after a completed write.
const releaseTimeout = 30 * time.Second

// verifyStored reads an object back from its digest key and hashes it.
//
// This is the only proof that what is at the address belongs there. It runs
// after a promote, and again on the idempotent shortcut, because an object
// sitting at a digest key is not evidence of its own correctness.
func (s *Store) verifyStored(ctx context.Context, organizationID uuid.UUID, digest string) error {
	body, err := s.blob.Get(ctx, objectKey(organizationID, digest))
	if err != nil {
		// Wrapped, not replaced: the caller distinguishes "nothing is
		// stored there" from "the read failed", and %w keeps that possible.
		return fmt.Errorf("read stored object %s: %w", digest, err)
	}
	defer func() { _ = body.Close() }()

	got, err := hashStream(body)
	if err != nil {
		return fmt.Errorf("read stored object %s: %w", digest, err)
	}
	if got != digest {
		return fmt.Errorf("%w: object at %s hashes to %s", store.ErrCorruptObject, digest, got)
	}
	return nil
}

// insertAttachment writes the row that makes the object reachable.
func (t *tx) insertAttachment(
	ctx context.Context, input *store.PutAttachmentInput, attachmentID uuid.UUID,
) (*store.Attachment, error) {
	row, err := t.queries.CreateBinaryAttachment(ctx, gen.CreateBinaryAttachmentParams{
		AttachmentID:   toUUID(attachmentID),
		OrganizationID: toUUID(input.OrganizationID),
		ObjectDigest:   input.Digest,
		MediaType:      input.MediaType,
		SizeBytes:      input.SizeBytes,
	})
	if err != nil {
		return nil, fmt.Errorf("record attachment %s: %w", attachmentID, err)
	}
	attachment := attachmentFromRow(&row)
	return &attachment, nil
}

func attachmentFromRow(row *gen.BinaryAttachment) store.Attachment {
	return store.Attachment{
		CreatedAt:      fromTimestamptz(row.CreatedAt),
		Digest:         row.ObjectDigest,
		MediaType:      row.MediaType,
		SizeBytes:      row.SizeBytes,
		AttachmentID:   fromUUID(row.AttachmentID),
		OrganizationID: fromUUID(row.OrganizationID),
	}
}
