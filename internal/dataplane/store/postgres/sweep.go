package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/gen"
	"orchestrator/internal/dataplane/store"
)

// objectSweepLimit bounds the digests one pass condemns.
//
// A bound rather than "everything unreferenced", for the reason staging
// cleanup is bounded: each digest costs a lock, two transactions and several
// remote calls, and an unbounded pass over a backlog would hold that pattern
// for as long as the backlog takes. Candidates are discovered by their own
// residue rather than by a record that can be lost, so a deferred remainder
// is the next pass's work rather than storage nothing will find again.
const objectSweepLimit = 100

// sweepGracePeriod is how long storage is left alone after it appears.
//
// It is defence in depth and NOT the mechanism. Round 1 of the design made
// an hour the whole protection and that was correctly rejected: a paused
// writer or a slow upload can exceed any constant, so age cannot prove
// abandonment. The advisory lock and the reference recheck under it are what
// make the decision sound; this only keeps very fresh residue out of a
// sweep's reach while a writer that is about to commit does so.
//
// So it is not sized to any writer's duration -- that reasoning is what was
// rejected. It is a floor, it cannot be zero, and it is not configurable:
// design D6 requires the guard to be aged past rather than switched off.
const sweepGracePeriod = 15 * time.Minute

// DeleteUnpinned reclaims the storage of digests nothing references.
//
// The name comes from ADR 0022. What it actually removes is storage that is
// UNREFERENCED: pins hold attachment rows, attachment rows are the reachable
// set, and this pass never reads a pin. Attachment truncation is what turns
// an unpinned row into an absent one (design D6a), and only then is its
// object reclaimable here.
//
// Three steps per digest, deliberately NOT one transaction:
//
//  1. under the digest lock, recheck unreferenced and record a deletion
//     claim naming the exact version and upload ids observed;
//  2. issue the version-specific deletes and upload aborts;
//  3. under the digest lock, clear the claim.
//
// The split exists because step 2 is remote. An advisory lock lives in a
// Postgres connection, and if that connection dies with a delete in flight
// the lock is released and the delete is not cancelled -- so a writer could
// take the lock, promote, commit a row, and have the delayed delete remove
// an object that row references. No ordering inside Postgres fixes that,
// because the operation being ordered is outside Postgres. A committed claim
// naming specific ids does: it survives the connection, and a late arrival
// removes something already condemned and nothing else.
func (s *Store) DeleteUnpinned(ctx context.Context, organizationID uuid.UUID) (store.ObjectSweep, error) {
	var result store.ObjectSweep

	candidates, err := s.sweepCandidates(ctx, organizationID)
	if err != nil {
		return result, err
	}

	// Bounded here, AFTER referenced digests have been filtered out, so the
	// budget is spent only on candidates. Spending it on digests the pass
	// will decline to touch would let a busy organization starve its own
	// reclamation -- and starve it repeatedly, since the same digests sort
	// first every pass.
	if len(candidates) > objectSweepLimit {
		result.DeferredForNextPass = len(candidates) - objectSweepLimit
		candidates = candidates[:objectSweepLimit]
		slog.Default().InfoContext(ctx, "more unreferenced digests than one sweep pass condemns",
			"organization_id", organizationID, "deferred", result.DeferredForNextPass)
	}

	for _, digest := range candidates {
		claim, outcome, condemnErr := s.condemn(ctx, organizationID, digest)
		if condemnErr != nil {
			return result, condemnErr
		}
		switch outcome {
		case condemnedNow:
		case foundReferenced:
			result.DeferredReferenced++
			continue
		case foundYoung:
			result.DeferredYoung++
			continue
		case foundClaimed:
			result.DeferredClaimed++
			continue
		case foundNothing:
			continue
		}

		versions, uploads, execErr := s.executeClaim(ctx, &claim)
		result.VersionsDeleted += versions
		result.UploadsAborted += uploads
		if execErr != nil {
			// The claim survives, which is the point of having written it:
			// the reconciler re-issues these same ids at `dataplane-up`. What
			// must not happen is going on to the next digest as though this
			// one were finished.
			return result, execErr
		}
		result.DigestsReclaimed++
	}
	return result, nil
}

// sweepOutcome is what the locked recheck decided about one digest.
type sweepOutcome int

const (
	condemnedNow sweepOutcome = iota
	// foundReferenced means a reference existed once the lock was granted --
	// either it was always there and the pre-filter raced it, or a writer
	// held the lock and committed while this pass waited for it.
	foundReferenced
	// foundYoung means the residue is inside the grace period.
	foundYoung
	// foundClaimed means a claim from an earlier pass is still live on this
	// digest. Ordinary post-crash state, not a failure -- and not this
	// pass's to finish.
	foundClaimed
	// foundNothing means the key was empty by the time the lock was granted.
	// Another sweep, or the writer's own release, got there first.
	foundNothing
)

// sweepCandidates finds every digest under an organization's prefix that no
// attachment row references.
//
// The candidate set is the UNION of both storage vocabularies, and the union
// is not redundant: a digest key can carry incomplete multipart uploads and
// no version at all -- the residue of a promote that died before completing
// -- which version enumeration can never discover, and which the reachability
// check cannot see either because there is no object to be unreferenced.
func (s *Store) sweepCandidates(ctx context.Context, organizationID uuid.UUID) ([]string, error) {
	prefix := organizationID.String() + "/"

	versions, err := s.blob.ListVersions(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("list object versions under %s: %w", prefix, err)
	}
	uploads, err := s.blob.ListUploadsUnder(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("list object uploads under %s: %w", prefix, err)
	}

	digests := make(map[string]struct{}, len(versions)+len(uploads))
	for i := range versions {
		s.noteCandidate(ctx, digests, organizationID, versions[i].Key)
	}
	for i := range uploads {
		s.noteCandidate(ctx, digests, organizationID, uploads[i].Key)
	}
	if len(digests) == 0 {
		return nil, nil
	}

	// One query for the whole candidate set. This is a pre-filter and not
	// the decision -- the decision is made again under the lock -- but on a
	// content-addressed store nearly every object is referenced, and taking
	// a lock and two transactions per digest to discover that would make the
	// normal case the expensive one.
	candidates := slices.Sorted(maps.Keys(digests))
	referencedRows, err := s.queries.ListReferencedDigests(ctx, gen.ListReferencedDigestsParams{
		OrganizationID: toUUID(organizationID),
		ObjectDigests:  candidates,
	})
	if err != nil {
		return nil, fmt.Errorf("read references for %d digests: %w", len(candidates), err)
	}
	referenced := make(map[string]struct{}, len(referencedRows))
	for _, digest := range referencedRows {
		referenced[digest] = struct{}{}
	}

	unreferenced := make([]string, 0, len(candidates))
	for _, digest := range candidates {
		if _, isReferenced := referenced[digest]; !isReferenced {
			unreferenced = append(unreferenced, digest)
		}
	}
	return unreferenced, nil
}

// noteCandidate records the digest a key addresses, or refuses to guess.
//
// A key is a candidate only if this module would have written it: the digest
// is taken from the last segment and the whole key is then REBUILT from it
// and compared. Anything that does not match exactly -- a different fan-out,
// an extra segment, a digest that is not 64 hex characters, a key some other
// tool left in the bucket -- is left alone and reported.
//
// Parsing leniently would be the dangerous direction. The sweep's next act
// is to delete every version of what it identified, so a key it cannot
// account for is precisely the key it must not touch.
func (s *Store) noteCandidate(
	ctx context.Context, digests map[string]struct{}, organizationID uuid.UUID, key string,
) {
	// requireDigest is checked BEFORE objectKey rebuilds the key, and the
	// order is load-bearing: objectKey slices the digest to build the fan-out
	// and would panic on anything shorter than that.
	digest := key[strings.LastIndex(key, "/")+1:]
	if requireDigest(digest) != nil || objectKey(organizationID, digest) != key {
		slog.Default().WarnContext(ctx, "object key does not match this module's layout; leaving it alone",
			"organization_id", organizationID, "key", key)
		return
	}
	digests[digest] = struct{}{}
}

// condemn is step 1: under the digest lock, decide and record.
//
// The recheck under the lock is what makes the decision sound. "Unreferenced"
// is established in mutual exclusion with the commit that would make it
// referenced, and a writer that has not yet taken the lock has not yet
// promoted -- so there is nothing at the digest key to delete.
//
// The ids are enumerated HERE, under the lock, rather than being carried
// over from discovery. The claim's whole purpose is to name what was
// observed at the instant the decision was made; ids read before the lock
// was granted are ids that may have changed while this pass waited.
func (s *Store) condemn(
	ctx context.Context, organizationID uuid.UUID, digest string,
) (gen.DeletionClaim, sweepOutcome, error) {
	type decision struct {
		claim   gen.DeletionClaim
		outcome sweepOutcome
	}
	made, err := inTx(ctx, s, func(t *tx) (decision, error) {
		if lockErr := t.queries.TakeDigestLock(ctx, digestLockKey(organizationID, digest)); lockErr != nil {
			return decision{}, fmt.Errorf("take digest lock: %w", lockErr)
		}

		referenced, refErr := t.queries.DigestIsReferenced(ctx, gen.DigestIsReferencedParams{
			OrganizationID: toUUID(organizationID),
			ObjectDigest:   digest,
		})
		if refErr != nil {
			return decision{}, fmt.Errorf("recheck references for %s: %w", digest, refErr)
		}
		if referenced {
			return decision{outcome: foundReferenced}, nil
		}

		// A claim already live on this digest is left alone, and this pass is
		// not entitled to finish it. Intent is not a fence: the earlier
		// delete may still be in flight, so re-issuing its ids under a second
		// claim would condemn the same storage twice without fencing either
		// attempt. The owner or the reconciler at `dataplane-up` completes it.
		//
		// Checked rather than discovered: without this, ordinary post-crash
		// residue reaches CreateDeletionClaim, trips the one-claim-per-digest
		// unique constraint, and kills the whole pass. The constraint stays as
		// the backstop for two sweeps that somehow got here at once.
		claimed, claimErr := t.queries.LiveDeletionClaimExists(ctx, gen.LiveDeletionClaimExistsParams{
			OrganizationID: toUUID(organizationID),
			ObjectDigest:   digest,
		})
		if claimErr != nil {
			return decision{}, fmt.Errorf("check deletion claim for %s: %w", digest, claimErr)
		}
		if claimed {
			return decision{outcome: foundClaimed}, nil
		}

		observed, obsErr := s.observeResidue(ctx, organizationID, digest)
		if obsErr != nil {
			return decision{}, obsErr
		}
		switch {
		case observed.young:
			return decision{outcome: foundYoung}, nil
		case len(observed.versionIDs) == 0 && len(observed.uploadIDs) == 0:
			return decision{outcome: foundNothing}, nil
		}

		claimID, idErr := uuid.NewV7()
		if idErr != nil {
			return decision{}, fmt.Errorf("allocate deletion claim id: %w", idErr)
		}
		claim, insertErr := t.queries.CreateDeletionClaim(ctx, gen.CreateDeletionClaimParams{
			DeletionClaimID: toUUID(claimID),
			OrganizationID:  toUUID(organizationID),
			ObjectDigest:    digest,
			VersionIds:      observed.versionIDs,
			UploadIds:       observed.uploadIDs,
		})
		if insertErr != nil {
			return decision{}, fmt.Errorf("record deletion claim for %s: %w", digest, insertErr)
		}
		return decision{claim: claim, outcome: condemnedNow}, nil
	})
	if err != nil {
		return gen.DeletionClaim{}, condemnedNow, err
	}
	return made.claim, made.outcome, nil
}

// residue is what one digest key holds, as observed under the lock.
type residue struct {
	versionIDs []string
	uploadIDs  []string
	// young reports that something here is inside the grace period. It is a
	// property of the KEY rather than of each id: a claim naming some of a
	// key's storage and not the rest would reclaim it in pieces, and the
	// conservative direction for a backstop is to leave the whole key.
	young bool
}

// observeResidue enumerates one digest key in both vocabularies and judges
// its age.
func (s *Store) observeResidue(
	ctx context.Context, organizationID uuid.UUID, digest string,
) (residue, error) {
	key := objectKey(organizationID, digest)
	horizon := s.now().Add(-sweepGracePeriod)

	// Empty and non-nil, both of them. pgx sends a nil slice as SQL NULL
	// rather than as an empty array, and both columns are NOT NULL -- so a
	// digest whose residue is entirely of one kind, which is the common case,
	// would fail its own claim insert.
	observed := residue{versionIDs: []string{}, uploadIDs: []string{}}

	versions, err := s.blob.ListVersions(ctx, key)
	if err != nil {
		return observed, fmt.Errorf("list versions of %s: %w", key, err)
	}
	for i := range versions {
		// ListVersions takes a PREFIX, and this one is a whole key. No valid
		// key can extend another -- they all end in a fixed-length digest --
		// but the filter costs one comparison and the alternative is a delete
		// aimed at whatever the server chose to include.
		if versions[i].Key != key {
			continue
		}
		if s.tooFresh(ctx, key, versions[i].LastModified, horizon) {
			observed.young = true
		}
		// Delete markers are included deliberately: a marker IS a version,
		// and one left behind keeps the key alive with nothing readable at
		// it. Version ids that cannot fence a delete are dropped instead --
		// the claim's own CHECK refuses a blank id, and one blank id would
		// abort the insert and with it the condemnation of every usable id
		// beside it.
		if versions[i].VersionID == "" {
			slog.Default().WarnContext(ctx, "stored version carries no id and cannot be deleted by name",
				"key", key)
			continue
		}
		observed.versionIDs = append(observed.versionIDs, versions[i].VersionID)
	}

	uploads, err := s.blob.ListUploadsForKey(ctx, key)
	if err != nil {
		return observed, fmt.Errorf("list incomplete uploads on %s: %w", key, err)
	}
	for i := range uploads {
		if s.tooFresh(ctx, key, uploads[i].Initiated, horizon) {
			observed.young = true
		}
		observed.uploadIDs = append(observed.uploadIDs, uploads[i].UploadID)
	}
	return observed, nil
}

// tooFresh reports whether a timestamp falls inside the grace period.
//
// The comparison crosses two clocks: the object store dated the storage and
// this process reads the horizon. That is worth stating rather than hiding,
// and it is tolerable for exactly one reason -- the grace period is a
// backstop behind the lock, so skew widens or narrows a margin rather than
// deciding correctness. Skew in the safe direction defers a reclamation; in
// the other it removes a backstop the lock still stands behind.
//
// A timestamp in the future is skew large enough to say so. Left silent, a
// store running fast would defer every candidate forever and the sweep would
// look like it had nothing to do.
func (s *Store) tooFresh(ctx context.Context, key string, stamp, horizon time.Time) bool {
	if stamp.After(s.now()) {
		slog.Default().WarnContext(ctx, "object store dated storage in the future; check clock skew",
			"key", key, "stamp", stamp)
	}
	return stamp.After(horizon)
}

// executeClaim is steps 2 and 3: issue exactly what the claim records, then
// clear it.
//
// The ids come from the claim ROW -- what the database returned -- and never
// from an enumeration. That is the fence. An id that failed to commit is an
// id no crash recovery could re-issue, so deleting it would be deleting
// storage nothing durable condemned.
//
// Shared with the reconciler, which is the same two steps performed by
// someone else. Sharing them is what makes "repeating a version-specific
// delete is harmless" a single claim about a single code path.
func (s *Store) executeClaim(ctx context.Context, claim *gen.DeletionClaim) (int, int, error) {
	organizationID := fromUUID(claim.OrganizationID)
	key := objectKey(organizationID, claim.ObjectDigest)

	var aborted int
	for _, uploadID := range claim.UploadIds {
		if err := s.blob.AbortUpload(ctx, key, uploadID); err != nil {
			return 0, aborted, fmt.Errorf("abort upload %s on %s: %w", uploadID, key, err)
		}
		aborted++
	}

	var deleted int
	for _, versionID := range claim.VersionIds {
		if err := s.blob.DeleteVersion(ctx, key, versionID); err != nil {
			return deleted, aborted, fmt.Errorf("delete %s version %s: %w", key, versionID, err)
		}
		deleted++
	}

	if err := s.clearClaim(ctx, claim); err != nil {
		return deleted, aborted, err
	}
	return deleted, aborted, nil
}

// clearClaim is step 3: the claim goes under the digest lock, once its
// storage is gone.
//
// Under the lock because "the claim is gone" is what a writer reads to mean
// "the condemned storage is already removed". Clearing it outside the lock
// would let a writer take the existing-object shortcut over an object whose
// delete had not yet been issued.
func (s *Store) clearClaim(ctx context.Context, claim *gen.DeletionClaim) error {
	organizationID := fromUUID(claim.OrganizationID)
	_, err := inTx(ctx, s, func(t *tx) (struct{}, error) {
		if lockErr := t.queries.TakeDigestLock(ctx,
			digestLockKey(organizationID, claim.ObjectDigest)); lockErr != nil {
			return struct{}{}, fmt.Errorf("take digest lock: %w", lockErr)
		}
		cleared, clearErr := t.queries.ClearDeletionClaim(ctx, gen.ClearDeletionClaimParams{
			OrganizationID:  claim.OrganizationID,
			DeletionClaimID: claim.DeletionClaimID,
		})
		if clearErr != nil {
			return struct{}{}, fmt.Errorf("clear deletion claim %s: %w",
				fromUUID(claim.DeletionClaimID), clearErr)
		}
		if cleared == 0 {
			// Not an error, and not an invariant failure. The reconciler is
			// entitled to finish any claim it finds, so an owner whose
			// deletes were slow can arrive to find its own claim already
			// completed by a `dataplane-up` that ran in between. The storage
			// is gone either way; both actors re-issued the same ids, which
			// is exactly the idempotence the claim is built on.
			slog.Default().InfoContext(ctx, "deletion claim was already cleared by another finisher",
				"organization_id", organizationID, "digest", claim.ObjectDigest)
		}
		return struct{}{}, nil
	})
	return err
}

// ReconcileDeletionClaims finishes deletes an earlier actor could not.
//
// A claim outlives the connection that made it, so a crash between recording
// the intent and clearing it leaves a row -- the only record that storage was
// condemned but may still be there. This re-issues exactly the recorded ids
// and clears the row, which is safe at any time because a version-specific
// delete repeated is a no-op by construction.
//
// It runs at `dataplane-up` rather than on a timer, because a surviving
// claim is not only unreclaimed storage: it also forbids the existing-object
// shortcut for its digest until it clears, so every writer of those bytes
// pays for a full upload in the meantime.
//
// One claim's failure does not abandon the rest. A claim exists precisely
// because something already went wrong, and the pass that gives up on the
// first one is the pass that leaves a whole plane's residue in place.
func (s *Store) ReconcileDeletionClaims(ctx context.Context) (store.ClaimReconciliation, error) {
	var result store.ClaimReconciliation

	claims, err := s.queries.ListDeletionClaims(ctx)
	if err != nil {
		return result, fmt.Errorf("list deletion claims: %w", err)
	}

	var failures []error
	for i := range claims {
		versions, uploads, execErr := s.executeClaim(ctx, &claims[i])
		result.VersionsDeleted += versions
		result.UploadsAborted += uploads
		if execErr != nil {
			failures = append(failures, execErr)
			continue
		}
		result.ClaimsCleared++
	}
	if len(failures) > 0 {
		return result, fmt.Errorf("%d of %d deletion claims could not be completed: %w",
			len(failures), len(claims), errors.Join(failures...))
	}
	return result, nil
}
