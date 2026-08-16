package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"

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
// It reports both halves of what it did, because they mean different
// things: a released lease is an abandoned writer collected, while a
// collected orphan is residue that outlived its own discovery record and
// says something went wrong earlier.
func (s *Store) CleanUpStaging(ctx context.Context, organizationID uuid.UUID) (store.StagingCleanup, error) {
	var result store.StagingCleanup

	expired, err := s.queries.ListExpiredStagingLeases(ctx, gen.ListExpiredStagingLeasesParams{
		OrganizationID: toUUID(organizationID),
		RowLimit:       stagingSweepLimit,
	})
	if err != nil {
		return result, fmt.Errorf("list expired staging leases: %w", err)
	}

	for i := range expired {
		// One transaction per lease, not one for the pass. Each holds a row
		// lock across remote calls, and batching them would hold every lock
		// for the duration of the slowest.
		done, releaseErr := s.releaseOneLease(ctx, organizationID, expired[i].StagingKey)
		if releaseErr != nil {
			return result, releaseErr
		}
		if done {
			result.LeasesReleased++
		}
	}

	orphans, err := s.collectStagingOrphans(ctx, organizationID)
	if err != nil {
		return result, err
	}
	result.OrphansCollected = orphans
	return result, nil
}

// collectStagingOrphans empties staging keys that no lease owns.
//
// This is the "next pass" the idempotence claim depends on, and without it
// there was no such pass. Cleanup deletes the lease row when it finishes
// with a key, and that row is the only record by which the key can be
// found -- so anything appearing afterwards was undiscoverable: the final
// object sweep never considers the staging prefix, and nothing else looks
// there. Two ways it happens:
//
//   - a writer paused past its term resumes and starts an upload. The token
//     stops it PROMOTING; nothing stops it writing to its own staging key;
//   - an upload completes between cleanup's abort step and its version
//     enumeration, appearing as a version the pass has already looked past.
//
// The absence of a lease is what licenses deletion, and it is a strong
// licence rather than a guess: a writer inserts its lease before the first
// byte, and a lost lease can never be resurrected -- there is no re-insert.
// So a staging object with no lease belongs to a writer that provably
// cannot promote, and removing it is not removing work that might still
// complete. ADR 0027 forbids destroying another actor's in-progress work;
// it does not require preserving work that has been made impossible.
//
// Any key that still has a lease is left alone, whatever its state: that
// key belongs to the lease-driven path above, which locks it properly.
func (s *Store) collectStagingOrphans(ctx context.Context, organizationID uuid.UUID) (int, error) {
	prefix := stagingPrefix + organizationID.String() + "/"

	// Both storage states, because either can be the residue. A version
	// listing cannot see incomplete uploads and an upload listing cannot
	// see versions.
	versions, err := s.blob.ListVersions(ctx, prefix)
	if err != nil {
		return 0, fmt.Errorf("list staging versions under %s: %w", prefix, err)
	}
	// An empty set is two different facts. Where the provider enumerates
	// incomplete writes it means none are present; where it reclaims them
	// itself it means this store cannot see them. Both leave nothing for
	// the caller to do here, which is why the distinction is not acted on —
	// it is the sweep's REPORT that must not claim a count it never took.
	uploads, err := s.listUploadsUnder(ctx, prefix)
	if err != nil {
		return 0, fmt.Errorf("list staging uploads under %s: %w", prefix, err)
	}

	keys := make(map[string]struct{}, len(versions)+len(uploads))
	for i := range versions {
		keys[versions[i].Key] = struct{}{}
	}
	for i := range uploads {
		keys[uploads[i].Key] = struct{}{}
	}

	// One query for the whole candidate set. Asking per key made the round
	// trips a function of how much legitimate work was in flight: an
	// organization with many live writers and no residue at all did the
	// most work and collected nothing.
	candidates := slices.Sorted(maps.Keys(keys))
	ownedRows, err := s.queries.ListOwnedStagingKeys(ctx, gen.ListOwnedStagingKeysParams{
		OrganizationID: toUUID(organizationID),
		StagingKeys:    candidates,
	})
	if err != nil {
		return 0, fmt.Errorf("read leases for %d staging keys: %w", len(candidates), err)
	}
	owned := make(map[string]struct{}, len(ownedRows))
	for _, key := range ownedRows {
		owned[key] = struct{}{}
	}

	// Owned keys are filtered out BEFORE the budget is applied, so they
	// never consume it. Spending the budget on keys it must not touch would
	// let a busy organization starve its own orphan collection -- and
	// starve it repeatedly, since the same keys sort first every pass.
	unowned := make([]string, 0, len(candidates))
	for _, key := range candidates {
		if _, isOwned := owned[key]; !isOwned {
			unowned = append(unowned, key)
		}
	}

	// The destructive work is bounded; discovery is not. A staging prefix
	// is normally near-empty -- writers release their own keys -- so
	// enumerating it is cheap, and an orphan is discovered by its own
	// residue rather than by a record that can be lost, which is what makes
	// deferring the remainder safe rather than a silent cap.
	collected := len(unowned)
	if collected > stagingSweepLimit {
		collected = stagingSweepLimit
		slog.Default().WarnContext(ctx, "more orphaned staging keys than one pass collects",
			"organization_id", organizationID, "found", len(unowned), "collecting", collected)
	}
	for _, key := range unowned[:collected] {
		if err := s.emptyStagingKey(ctx, key); err != nil {
			return 0, err
		}
	}
	return collected, nil
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
	// No uploads to abort where the provider reclaims its own; the version
	// deletion below is then the whole of the work.
	uploads, err := s.listUploadsForKey(ctx, stagingKey)
	if err != nil {
		return fmt.Errorf("list incomplete uploads on %s: %w", stagingKey, err)
	}
	for i := range uploads {
		if abortErr := s.abortUpload(ctx, stagingKey, uploads[i].UploadID); abortErr != nil {
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
