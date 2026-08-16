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
	if s.blob.IncompleteWrites() != objects.IncompleteWritesEnumerable {
		return nil, false, nil
	}
	r, ok := s.blob.(objects.IncompleteWriteReclaimer)
	if !ok {
		return nil, false, fmt.Errorf(
			"object provider declares incomplete writes %q but does not implement the reclaimer: %w",
			objects.IncompleteWritesEnumerable, store.ErrInvariant)
	}
	return r, true, nil
}

// listUploadsUnder enumerates incomplete writes beneath a prefix, returning
// none where the provider reclaims them itself.
//
// It does NOT yet report which of those two cases produced an empty result,
// and that omission is deliberate rather than an oversight. Nothing consumes
// the distinction today, because MinIO is the only provider and it always
// enumerates; a third return value would be surface built ahead of its
// consumer, which is the habit ADR 0032's scope correction exists to break.
// It returns when the GCS adapter and the sweep report that reads it land
// together (#286), and the capability is already declared on the interface
// so the answer is available the moment something asks.
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
