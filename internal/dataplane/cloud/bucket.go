package cloud

import (
	"context"
	"errors"
	"fmt"
	"time"

	"cloud.google.com/go/storage"
)

// ErrBucketUnsafe reports a bucket whose configuration would make the object
// sweep report storage it did not reclaim.
var ErrBucketUnsafe = errors.New("object bucket is not configured safely")

// EnsureBucket configures an existing bucket so the store's deletion contract
// actually holds, and REFUSES rather than proceeding when it cannot confirm it.
//
// It does not create the bucket. Creation is a separate privilege from
// configuration and both are separate from reading an object, which is why the
// object seam has none of them.
//
// Two properties have to hold, and only one of them is obvious.
//
// VERSIONING, because every fence in the object adapter names a generation and
// an unversioned bucket discards the previous generation on overwrite. This is
// the same check blob.go performs for the local stack.
//
// SOFT DELETE DISABLED, which is the one that costs money silently. GCS applies
// a seven-day soft-delete retention by default (absent a tag-based override),
// and under it a generation-specific delete SUCCEEDS, leaves the versioned
// listing, and retains the bytes — billed — until their hard-delete time. The
// object sweep would issue its deletes, record the storage as reclaimed, and
// the bill would not move for a week. Nothing in the data path can detect this:
// `DeleteVersion` returns 204 either way.
//
// # Why disabling is not enough, and why order matters
//
// Disabling soft delete governs FUTURE deletions only. Objects already
// soft-deleted keep their original retention, and — worse — become
// unobservable once the policy is off: a read of a known soft-deleted
// generation then answers `400 Soft delete policy is required...`, a refusal to
// answer rather than a 404. So a bucket that has already accumulated residue
// cannot be audited afterwards.
//
// The consequence is that this must run BEFORE the first object write. It is
// called at provisioning time for that reason, and it reports what it changed
// so a caller that ran it too late can see so.
func EnsureBucket(ctx context.Context, cfg Config) (Report, error) {
	if cfg.Bucket == "" {
		return Report{}, errors.New("configure an object bucket: none was supplied")
	}
	client, err := storage.NewClient(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("build a cloud storage client: %w", err)
	}
	defer func() {
		// A close failure here cannot invalidate what was already observed,
		// and there is no error channel left to report it on that would not
		// mask the result.
		_ = client.Close()
	}()

	bucket := client.Bucket(cfg.Bucket)
	attrs, err := bucket.Attrs(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("read the configuration of bucket %s: %w", cfg.Bucket, err)
	}

	report := Report{
		Bucket:            cfg.Bucket,
		VersioningEnabled: attrs.VersioningEnabled,
		SoftDeleteRetention: func() time.Duration {
			if attrs.SoftDeletePolicy == nil {
				return 0
			}
			return attrs.SoftDeletePolicy.RetentionDuration
		}(),
	}

	if !attrs.VersioningEnabled {
		return report, fmt.Errorf("%w: bucket %s is not versioned, so an overwrite discards the "+
			"previous generation and no delete can be fenced against a later writer",
			ErrBucketUnsafe, cfg.Bucket)
	}

	if report.SoftDeleteRetention > 0 {
		if _, updateErr := bucket.Update(ctx, storage.BucketAttrsToUpdate{
			SoftDeletePolicy: &storage.SoftDeletePolicy{RetentionDuration: 0},
		}); updateErr != nil {
			return report, fmt.Errorf("disable soft delete on bucket %s (it retains deleted objects "+
				"for %s, during which DeleteVersion reclaims nothing): %w",
				cfg.Bucket, report.SoftDeleteRetention, updateErr)
		}
		report.DisabledSoftDelete = true
		// Re-read rather than assuming the update took: the whole point of
		// this function is that configuration is confirmed rather than
		// requested.
		attrs, err = bucket.Attrs(ctx)
		if err != nil {
			return report, fmt.Errorf("re-read bucket %s after disabling soft delete: %w",
				cfg.Bucket, err)
		}
		if attrs.SoftDeletePolicy != nil {
			report.SoftDeleteRetention = attrs.SoftDeletePolicy.RetentionDuration
		} else {
			report.SoftDeleteRetention = 0
		}
		if report.SoftDeleteRetention > 0 {
			return report, fmt.Errorf("%w: bucket %s still reports a soft-delete retention of %s "+
				"after it was disabled", ErrBucketUnsafe, cfg.Bucket, report.SoftDeleteRetention)
		}
	}

	// CONFIGURED is not EFFECTIVE. The read above proves what the bucket says,
	// not what its delete path does, and Google bounds neither how long the
	// change takes to apply nor how to observe that it has. So a bucket that
	// was modified moments ago is refused rather than trusted — including one
	// this call just modified, which is why `DisabledSoftDelete` is reported:
	// the caller is being told to wait, not that something went wrong.
	report.Settled = time.Since(attrs.Updated)
	if report.Settled < SoftDeleteStabilization {
		return report, fmt.Errorf("%w: bucket %s was modified %s ago. Its configuration already "+
			"reads safely, but a soft-delete change is not effective when it becomes visible and "+
			"Google does not bound the delay, so this cannot distinguish a settled bucket from one "+
			"still retaining deletions. Wait %s and retry",
			ErrBucketUnsafe, cfg.Bucket, report.Settled.Round(time.Second),
			(SoftDeleteStabilization - report.Settled).Round(time.Second))
	}
	return report, nil
}

// Report says what EnsureBucket observed and changed.
//
// It exists so a caller can distinguish the two ways this refuses: a bucket
// that is wrong and cannot be fixed, from one that was just fixed and needs
// time to settle. Those look identical from an error alone and call for
// different responses — the first is an operator problem, the second is a wait.
//
//nolint:govet // fieldalignment: grouped for reading; constructed once per call
type Report struct {
	// Bucket names what was inspected.
	Bucket string

	// VersioningEnabled is the state as found.
	VersioningEnabled bool

	// SoftDeleteRetention is the retention AFTER any change this call made.
	// Zero means disabled.
	SoftDeleteRetention time.Duration

	// DisabledSoftDelete reports that this call turned it off, which means any
	// objects already soft-deleted are now unreclaimable residue that can no
	// longer even be enumerated.
	DisabledSoftDelete bool

	// Settled is how long since the bucket was last modified, as observed.
	Settled time.Duration
}
