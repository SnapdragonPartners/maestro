package objects

import (
	"context"
	"io"
)

// Store is the provider-neutral object surface the persistence layer needs.
//
// It exists so a second provider can be composed in without the store above
// it changing. What it is NOT is a new application-facing abstraction:
// `store.ObjectStore` remains the stable one, and the acceptance claim for
// cloud portability stays there — application persistence must see the same
// `store.Store` and `store.ObjectStore` behaviour in both modes. This
// interface is the seam underneath that makes it possible, not the promise
// itself.
//
// It is deliberately narrower than *Blob. Two things were left off.
//
// **Bucket creation is not here.** `EnsureBucket` is called only by the
// lifecycle and test-harness code that provisions a plane, never by the
// store, and putting it on the runtime surface would force every adapter to
// hold bucket-creation authority in order to read an object. In a managed
// cloud that is a real privilege, granted for a one-time provisioning step
// and not for the process that serves reads.
//
// **The S3 multipart vocabulary is not here verbatim.** `ListUploadsForKey`,
// `ListUploadsUnder` and `AbortUpload` name a mechanism rather than an
// obligation. The obligation is that a write interrupted before it completed
// must not accumulate forever, and providers discharge it differently — see
// IncompleteWrites.
type Store interface {
	// PutStaged writes bytes to a staging key and returns the version
	// that holds them. The content is not yet reachable by digest.
	PutStaged(ctx context.Context, key string, size int64, body io.Reader) (string, error)

	// Promote copies an exact staged version to its digest key and returns
	// the version created there.
	//
	// Exact-version is the whole point: promoting "whatever is at that key
	// now" would copy a concurrent overwrite. Both providers can express
	// this — S3 by version ID, GCS by generation — which is why it survives
	// into the neutral surface while the multipart vocabulary does not.
	Promote(ctx context.Context, stagingKey, stagingVersion, digestKey string) (string, error)

	// Get streams an object's bytes. Verification is the caller's, and
	// `store.ObjectStore` performs it; this surface moves bytes.
	Get(ctx context.Context, key string) (io.ReadCloser, error)

	// Exists reports whether any live version of the key is present.
	Exists(ctx context.Context, key string) (bool, error)

	// ListVersions enumerates every version under a prefix, delete markers
	// included, because a delete marker is a state the sweep must see.
	ListVersions(ctx context.Context, prefix string) ([]Version, error)

	// DeleteVersion removes one exact version.
	DeleteVersion(ctx context.Context, key, versionID string) error

	// IncompleteWrites reports how this provider handles writes that were
	// started and never completed, and it is the reason this interface is
	// not a mechanical lift of *Blob.
	//
	// An S3-compatible provider keeps incomplete multipart uploads until
	// something aborts them: they are invisible to version listing,
	// unreachable by version deletion, and they bill. They must be
	// enumerated and reclaimed, which is what the sweep's deletion claims
	// do.
	//
	// GCS resumable uploads have no listing API and expire on their own.
	// An adapter for it CANNOT enumerate them, and returning an empty slice
	// would be a fabricated answer — the sweep would record that it found
	// nothing to reclaim, which is a claim about the bucket, when the truth
	// is a statement about the adapter's blindness. Reporting the capability
	// keeps "none present" and "not observable here" distinguishable, which
	// is the same unavailable-versus-zero discipline ADR 0025 applies to
	// benchmark records.
	IncompleteWrites() IncompleteWriteSupport
}

// IncompleteWriteReclaimer is the half of the surface only an enumerating
// provider implements.
//
// It is a separate interface rather than three methods on Store returning
// "unsupported", so a provider that cannot enumerate incomplete writes
// cannot accidentally answer as though it had looked. A caller reaches these
// only after IncompleteWrites reports Enumerable, and a type assertion is
// what enforces it.
type IncompleteWriteReclaimer interface {
	// ListUploadsForKey enumerates incomplete writes on one key.
	ListUploadsForKey(ctx context.Context, key string) ([]Upload, error)

	// ListUploadsUnder enumerates incomplete writes beneath a prefix.
	ListUploadsUnder(ctx context.Context, prefix string) ([]Upload, error)

	// AbortUpload reclaims one incomplete write.
	AbortUpload(ctx context.Context, key, uploadID string) error
}

// IncompleteWriteSupport says how a provider handles interrupted writes.
type IncompleteWriteSupport string

const (
	// IncompleteWritesEnumerable means the provider exposes interrupted
	// writes and does not reclaim them itself, so the sweep must. The
	// adapter also implements IncompleteWriteReclaimer.
	IncompleteWritesEnumerable IncompleteWriteSupport = "enumerable"

	// IncompleteWritesProviderReclaimed means interrupted writes are not
	// enumerable and the provider expires them on its own schedule. The
	// sweep records the obligation as the provider's rather than reporting
	// a count it did not measure.
	IncompleteWritesProviderReclaimed IncompleteWriteSupport = "provider-reclaimed"
)

// Compile-time proof that the MinIO adapter satisfies both halves.
//
// It proves that and nothing more: it says *Blob implements both interfaces,
// not that any provider declaring Enumerable implements the reclaimer. That
// relationship is between a runtime value and a runtime capability, so no
// assertion here can enforce it for an arbitrary provider — the postgres
// store validates it at construction instead.
var (
	_ Store                    = (*Blob)(nil)
	_ IncompleteWriteReclaimer = (*Blob)(nil)
)

// IncompleteWrites reports that S3-compatible storage keeps interrupted
// multipart uploads until they are aborted.
func (b *Blob) IncompleteWrites() IncompleteWriteSupport {
	return IncompleteWritesEnumerable
}
