package postgres

import (
	"context"
	"fmt"

	"orchestrator/internal/dataplane/objects"
	"orchestrator/internal/dataplane/store"
)

// reclaimer resolves the object provider's incomplete-write reclaimer, or
// reports that this provider has none.
//
// The second return says which case it is, and it exists so callers cannot
// treat "this provider expires interrupted writes itself" as "there were
// none". Those are different facts and only one of them is a measurement:
// an S3-compatible provider keeps incomplete multipart uploads until
// something aborts them, while GCS resumable uploads are not enumerable at
// all and expire on their own schedule. An adapter for the second kind that
// returned an empty slice would put a count the store never took into a
// sweep record, which is the unavailable-versus-zero mistake ADR 0025 names
// for benchmark records and this repository has now paid for twice.
//
// A provider declaring Enumerable and not implementing the reclaimer is a
// programming error rather than a runtime condition, so it surfaces as an
// invariant violation instead of degrading to "nothing to do" — degrading
// would silently stop reclaiming storage that keeps billing.
func (s *Store) reclaimer() (objects.IncompleteWriteReclaimer, bool, error) {
	switch s.blob.IncompleteWrites() {
	case objects.IncompleteWritesProviderReclaimed:
		return nil, false, nil
	case objects.IncompleteWritesEnumerable:
		r, ok := s.blob.(objects.IncompleteWriteReclaimer)
		if !ok {
			return nil, false, fmt.Errorf(
				"object provider declares incomplete writes %q but does not implement the reclaimer: %w",
				objects.IncompleteWritesEnumerable, store.ErrInvariant)
		}
		return r, true, nil
	default:
		// Unreachable through New, which rejects this at construction. It
		// is an error rather than a default-to-provider-reclaimed because
		// failing open here is what would silently stop reclaiming.
		return nil, false, unknownIncompleteWrites(s.blob.IncompleteWrites())
	}
}

// validateIncompleteWrites rejects an adapter whose capability is unknown, or
// which declares enumeration and cannot enumerate.
//
// Both are wiring errors, so they belong at construction: the alternative is
// a store that comes up fine and stops reclaiming storage the first time a
// sweep runs, which is the failure mode hardest to notice because it costs
// money quietly rather than breaking anything.
func validateIncompleteWrites(blob objects.Store) error {
	switch blob.IncompleteWrites() {
	case objects.IncompleteWritesProviderReclaimed:
		return nil
	case objects.IncompleteWritesEnumerable:
		if _, ok := blob.(objects.IncompleteWriteReclaimer); !ok {
			return fmt.Errorf(
				"postgres store: object adapter declares incomplete writes %q but does not implement "+
					"the reclaimer: %w", objects.IncompleteWritesEnumerable, store.ErrInvariant)
		}
		return nil
	default:
		return unknownIncompleteWrites(blob.IncompleteWrites())
	}
}

func unknownIncompleteWrites(got objects.IncompleteWriteSupport) error {
	return fmt.Errorf(
		"postgres store: object adapter reports unknown incomplete-write support %q; expected %q or %q: %w",
		got, objects.IncompleteWritesEnumerable, objects.IncompleteWritesProviderReclaimed,
		store.ErrInvariant)
}

// listUploadsUnder enumerates incomplete writes beneath a prefix, returning
// none where the provider reclaims them itself.
//
// It does NOT report which of those two cases produced an empty result, and
// that is a live gap rather than a resolved one. A non-enumerating provider
// now exists — the GCS adapter (#286) — so the earlier note here, that MinIO
// was the only provider and the distinction would land alongside GCS, is no
// longer true on either half.
//
// What keeps the present behaviour honest is narrower than reporting the
// distinction: the sweep's fields count ACTIONS TAKEN, not residue found. A
// zero abort count says nothing was aborted, which is true of a
// provider-reclaimed backend and of an enumerating one with nothing to
// reclaim. It does not assert that anything was enumerated, so no record
// currently claims a measurement it did not take.
//
// That holds only while the consumers stay action-shaped. The moment a sweep
// report says how much residue was FOUND, or an operator reads a zero as "the
// bucket is clean", this must carry the capability through — the answer is
// already on the interface, and it is the callers that are not asking.
func (s *Store) listUploadsUnder(ctx context.Context, prefix string) ([]objects.Upload, error) {
	r, ok, err := s.reclaimer()
	if err != nil || !ok {
		return nil, err
	}
	uploads, err := r.ListUploadsUnder(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("enumerate incomplete writes under %s: %w", prefix, err)
	}
	return uploads, nil
}

// listUploadsForKey enumerates incomplete writes on one key, with the same
// caveat as listUploadsUnder about the case an empty result hides.
func (s *Store) listUploadsForKey(ctx context.Context, key string) ([]objects.Upload, error) {
	r, ok, err := s.reclaimer()
	if err != nil || !ok {
		return nil, err
	}
	uploads, err := r.ListUploadsForKey(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("enumerate incomplete writes on %s: %w", key, err)
	}
	return uploads, nil
}

// abortUpload reclaims one incomplete write.
//
// Reaching this without an enumerating provider means a caller acted on an
// upload ID it could not have observed, so it is an invariant violation
// rather than a no-op.
func (s *Store) abortUpload(ctx context.Context, key, uploadID string) error {
	r, ok, err := s.reclaimer()
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf(
			"abort upload %s on %s: provider does not enumerate incomplete writes: %w",
			uploadID, key, store.ErrInvariant)
	}
	if err := r.AbortUpload(ctx, key, uploadID); err != nil {
		return fmt.Errorf("reclaim incomplete write %s on %s: %w", uploadID, key, err)
	}
	return nil
}
