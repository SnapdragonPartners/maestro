package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"orchestrator/internal/dataplane/gen"
	"orchestrator/internal/dataplane/store"
)

// stagingSweepLimit bounds one cleanup pass.
//
// A bound rather than "everything expired", because each lease costs remote
// calls and the pass holds a row lock for each one: an unbounded pass on a
// backlog would hold locks for as long as the backlog takes. Cleanup is
// re-runnable by construction, so the remainder is the next pass's.
const stagingSweepLimit = 100

// CleanUpStaging releases staging objects whose writers are gone.
//
// The three mechanisms answer three different questions (design D6), and
// this is where the third one earns its place:
//
//   - EXPIRY decides which leases cleanup may consider. It cannot decide
//     whether a writer is alive: a process paused past its term resumes
//     believing it still holds the lease.
//   - The OWNER TOKEN decides whether a writer may still promote. Cleanup
//     holds no token, which is why it cannot simply delete what it finds.
//   - The ROW LOCK decides who acts first when a lease is expired and its
//     writer is still running. Cleanup takes the same lock the promotion
//     holds, and rechecks expiry under it -- so it either waits for the
//     promotion or finds the lease alive. Round 3 had only expiry; round 4
//     added the token and left this as a race.
//
// It returns the number of leases released, which is what a caller reports.
func (s *Store) CleanUpStaging(ctx context.Context, organizationID uuid.UUID) (int, error) {
	expired, err := s.queries.ListExpiredStagingLeases(ctx, gen.ListExpiredStagingLeasesParams{
		OrganizationID: toUUID(organizationID),
		RowLimit:       stagingSweepLimit,
	})
	if err != nil {
		return 0, fmt.Errorf("list expired staging leases: %w", err)
	}

	released := 0
	for i := range expired {
		// One transaction per lease, not one for the pass. Each holds a row
		// lock across remote calls, and batching them would hold every lock
		// for the duration of the slowest.
		done, err := s.releaseOneLease(ctx, organizationID, expired[i].StagingKey)
		if err != nil {
			return released, err
		}
		if done {
			released++
		}
	}
	return released, nil
}

// releaseOneLease cleans one staging key, or declines to.
//
// Reports whether it released the lease: a lease that turned out to be
// alive, or that another actor had already collected, is not a failure.
func (s *Store) releaseOneLease(ctx context.Context, organizationID uuid.UUID, stagingKey string) (bool, error) {
	return inTx(ctx, s, func(t *tx) (bool, error) {
		locked, err := t.queries.LockStagingLease(ctx, gen.LockStagingLeaseParams{
			OrganizationID: toUUID(organizationID),
			StagingKey:     stagingKey,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// Collected by someone else between the listing and this
				// lock. Nothing to do, and nothing wrong.
				return false, nil
			}
			return false, fmt.Errorf("lock staging lease %s: %w", stagingKey, err)
		}

		// Rechecked HERE, under the lock, against the server's clock. The
		// listing that found this lease is a hint: a writer may have
		// renewed it in the meantime, and acting on the stale answer is
		// exactly the destructive recovery ADR 0027 forbids.
		if fromTimestamptz(locked.StagingLease.ExpiresAt).After(fromTimestamptz(locked.LockedAt)) {
			return false, nil
		}

		// Emptied BEFORE the lease row goes, and that order is deliberate.
		// Releasing the record first and then failing to empty the key
		// would leave residue nothing owns and nothing lists -- the final
		// sweep never considers the staging prefix, so an orphan there is
		// collected by no one.
		//
		// It follows that the recheck above is the guard, not the fence in
		// the delete below: by the time that fence can refuse, these
		// objects are already gone. The fence reports the mistake; the
		// recheck is what prevents it.
		if emptyErr := s.emptyStagingKey(ctx, stagingKey); emptyErr != nil {
			return false, emptyErr
		}

		removed, err := t.queries.DeleteExpiredStagingLease(ctx, gen.DeleteExpiredStagingLeaseParams{
			OrganizationID: toUUID(organizationID),
			StagingKey:     stagingKey,
		})
		if err != nil {
			return false, fmt.Errorf("delete staging lease %s: %w", stagingKey, err)
		}
		if removed == 0 {
			// The row was locked and expired a moment ago, so the delete's
			// own expiry fence should have matched. It did not, which means
			// the lease became live between the recheck and here -- and
			// this key has ALREADY been emptied, so the report has to say
			// so rather than read as a harmless no-op.
			return false, fmt.Errorf("%w: lease %s was live by the time its own deletion ran, and its "+
				"staging objects had already been removed; the expiry recheck under the row lock did "+
				"not hold", store.ErrInvariant, stagingKey)
		}
		return true, nil
	})
}

// emptyStagingKey removes both kinds of residue a dead writer leaves.
//
// The two crash windows leave DIFFERENT residue, and neither vocabulary can
// see the other's:
//
//	during the multipart upload  -> uploaded PARTS, and no version at all
//	after it completed, before   -> a VERSION the lease never named
//	the version id was recorded
//
// So the order is: abort the enumerated upload ids, THEN enumerate versions,
// then delete each. That way round, an upload that completes between the two
// steps appears as a version the enumeration finds. The other order would
// leave it behind.
//
// It never assumes the lease carries a version id. The staging key is unique
// per upload, so enumerating it is both safe and complete -- and a writer
// that died before recording anything is the common case, not the edge one.
//
// Idempotent by construction, so a version that appears after one pass is
// removed by the next.
func (s *Store) emptyStagingKey(ctx context.Context, stagingKey string) error {
	uploads, err := s.blob.ListUploadsForKey(ctx, stagingKey)
	if err != nil {
		return fmt.Errorf("list incomplete uploads on %s: %w", stagingKey, err)
	}
	for i := range uploads {
		if abortErr := s.blob.AbortUpload(ctx, stagingKey, uploads[i].UploadID); abortErr != nil {
			return fmt.Errorf("abort upload %s on %s: %w", uploads[i].UploadID, stagingKey, abortErr)
		}
	}

	versions, err := s.blob.ListVersions(ctx, stagingKey)
	if err != nil {
		return fmt.Errorf("list versions of %s: %w", stagingKey, err)
	}
	for i := range versions {
		// Version-specific, like every deletion in this module. A key-level
		// delete would write a delete marker and reclaim nothing.
		if delErr := s.blob.DeleteVersion(ctx, versions[i].Key, versions[i].VersionID); delErr != nil {
			return fmt.Errorf("delete %s version %s: %w", versions[i].Key, versions[i].VersionID, delErr)
		}
	}
	return nil
}
