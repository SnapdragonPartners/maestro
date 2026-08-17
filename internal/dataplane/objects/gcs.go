// This file is the Google Cloud Storage adapter, the second implementation of
// the provider-neutral Store seam and the reason that seam exists (#286).
//
// It is deliberately NOT a port of blob.go. Everything below that diverges
// from the S3-compatible adapter does so because the provider genuinely
// differs, and each divergence was MEASURED against a real bucket rather than
// recalled — the same discipline blob.go's comments record for MinIO. Where a
// claim here is about GCS behaviour, it was observed; where it is about the
// pinned client library, it was read in the source of
// cloud.google.com/go/storage v1.56.0.
//
// The three that shape the code most:
//
//   - There are NO DELETE MARKERS. Deleting a live object under versioning
//     adds no listing entry at all; the formerly-live generation simply gains
//     a deletion timestamp and goes noncurrent, still holding its bytes. So
//     Version.IsDeleteMarker is always false here, which is a statement about
//     the provider and not a shortcut in the adapter.
//   - Interrupted writes CANNOT BE ENUMERATED, which is why the Store seam
//     declares a capability instead of a method set. See IncompleteWrites.
//   - Storage is not reclaimed by DeleteVersion unless the bucket was
//     provisioned with soft delete disabled. See the note on DeleteVersion.

// (Detached from the package clause deliberately: blob.go carries the package
// comment, and a second one adjacent to `package objects` would compete with
// it.)

package objects

import (
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"strconv"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
)

// GCSConfig locates the bucket. There are no credentials in it.
//
// Unlike Config, which carries a static access key and secret for an
// S3-compatible endpoint, this adapter authenticates with Application Default
// Credentials — the ambient identity of whatever is running it, which is a
// developer's gcloud login locally and the attached service account in a
// deployed environment. Putting a credential field here would invite exactly
// the long-lived key that ADC exists to avoid.
//
// There is no fault-injection transport seam either, deliberately. blob.go has
// one because two of its guards can only be demonstrated by corrupting a
// request after the client has built it, and it has a present consumer in its
// own tests. Nothing here needs one yet, and surface built ahead of its
// consumer is the habit ADR 0032's scope correction exists to break.
type GCSConfig struct {
	// Bucket must already exist, be versioned, and have soft delete disabled.
	// Provisioning and verifying all three is the lifecycle path's job, not
	// this adapter's — the same split that keeps EnsureBucket off the Store
	// interface.
	Bucket string
}

// GCS is the Google Cloud Storage adapter.
type GCS struct {
	client *storage.Client
	bucket string
}

// NewGCS builds an adapter.
//
// It CONTACTS THE NETWORK, which New does not: storage.NewClient resolves
// Application Default Credentials, and that can mean reading a well-known file,
// calling the metadata server, or minting a token. A caller that treats
// construction as free — in an init, or inside a lock — is making a blocking
// remote call it did not plan for, so it takes a context and says so here.
func NewGCS(ctx context.Context, cfg GCSConfig) (*GCS, error) {
	if cfg.Bucket == "" {
		return nil, errors.New("object store bucket is required")
	}
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("build GCS client: %w", err)
	}
	return &GCS{client: client, bucket: cfg.Bucket}, nil
}

// Close releases the client's connections.
//
// It is not on the Store interface. Only one of the two adapters needs it —
// the MinIO client holds no pooled state a caller must return — and adding a
// method to the seam that one implementation would leave empty puts a
// lifecycle obligation on every future provider to satisfy this one. The
// composition that owns the client's lifetime calls this directly.
func (g *GCS) Close() error {
	if err := g.client.Close(); err != nil {
		return fmt.Errorf("close GCS client: %w", err)
	}
	return nil
}

// object returns a handle for the live generation of a key.
func (g *GCS) object(key string) *storage.ObjectHandle {
	return g.client.Bucket(g.bucket).Object(key)
}

// PutStaged uploads to a staging key and returns the stored generation.
//
// It VERIFIES TRANSPORT INTEGRITY AFTER THE FACT, which is the one place this
// adapter does more work than blob.go rather than less, and the reason is that
// GCS offers no equivalent of the checksum header blob.go sends.
//
// The pinned client will only transmit a CRC32C if Writer.SendCRC32C is set
// with Writer.CRC32C already populated, and the source is explicit that both
// must be set BEFORE the first Write. A streaming adapter cannot know the
// checksum of bytes it has not read yet, and buffering to find out is not an
// option for the evidence media this store exists to hold.
//
// Doing nothing was the tempting answer and is wrong. The seam above hashes
// the stream as it SENDS it, so `store.ObjectStore`'s digest describes the
// bytes that left this process; if they are corrupted on the way, that check
// still passes and the stored object is silently not the object anyone
// verified. blob.go closes the same gap on the request side and calls it
// transport integrity.
//
// So the check moves to the far side of the write. The bytes are hashed as
// they stream past, and the result is compared against the CRC32C the SERVICE
// computed over what it actually stored — which is a stronger statement than
// any client-side checksum, because it is the server's own account of the
// object rather than a restatement of what we believed we sent.
//
// A mismatch DELETES the generation before returning. Leaving it would put an
// object nobody verified at a key the caller is about to be told does not
// exist, and the sweep has no way to tell it apart from a live one.
func (g *GCS) PutStaged(ctx context.Context, key string, size int64, body io.Reader) (string, error) {
	// The writer gets a cancellable context of its own because cancelling it
	// is how an upload is abandoned: the pinned client deprecates
	// CloseWithError in favour of exactly this. Without it a failed copy
	// would leave the resumable session open, and this is the one provider
	// whose abandoned sessions nothing can enumerate afterwards.
	writeCtx, abandonWrite := context.WithCancel(ctx)
	defer abandonWrite()

	writer := g.object(key).NewWriter(writeCtx)
	// Declared so the service can reject a size mismatch of its own accord;
	// the client does not enforce it.
	writer.Size = size

	sent := crc32.New(crc32cTable)
	if _, err := io.Copy(writer, io.TeeReader(body, sent)); err != nil {
		abandonWrite()
		return "", fmt.Errorf("upload staging object %s: %w", key, err)
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("finalize staging object %s: %w", key, err)
	}

	attrs := writer.Attrs()
	if stored, computed := attrs.CRC32C, sent.Sum32(); stored != computed {
		return "", discardCorrupt(ctx, g.DeleteVersion, key, attrs.Generation, stored, computed)
	}
	return fencedGeneration(key, attrs.Generation)
}

// removeVersion is the deletion discardCorrupt performs, taken as a parameter
// so the outcome of the removal can be chosen by a test.
//
// It exists because the branch that matters most here — a corrupt object
// successfully deleted — is the one a unit test cannot otherwise reach, and
// it is where the damaging mutation lives: returning nil there reports the
// upload as SUCCEEDED, with an empty version id, for an object that was just
// deleted. That was demonstrated rather than imagined; the first version of
// this code had that branch uncovered and a mutation to it went undetected.
// It matches DeleteVersion's signature so the real call site passes the
// method directly.
type removeVersion func(ctx context.Context, key, versionID string) error

// discardCorrupt removes an object whose stored bytes do not match what was
// sent, and reports the original corruption whether or not the removal worked.
//
// The removal failing does not make the upload succeed, so the corruption is
// ALWAYS the returned error; a failed cleanup is additional context on it
// rather than a replacement for it, and a successful cleanup does not make it
// go away either. There is no path through here that returns nil.
func discardCorrupt(
	ctx context.Context, remove removeVersion, key string, generation int64, stored, computed uint32,
) error {
	corruption := fmt.Errorf("stored object %s does not match what was sent: service computed CRC32C "+
		"%08x over generation %d, this process computed %08x over the bytes it streamed",
		key, stored, generation, computed)

	if generation <= 0 {
		// Nothing nameable to remove. Reported rather than ignored, because
		// it means a corrupt object is now live at a key whose write is about
		// to be reported as failed.
		return fmt.Errorf("%w; it could not be removed because the write returned no usable generation",
			corruption)
	}
	if err := remove(ctx, key, strconv.FormatInt(generation, 10)); err != nil {
		return fmt.Errorf("%w; removing it also failed: %w", corruption, err)
	}
	return corruption
}

// Promote server-side copies one staged GENERATION onto its digest key.
//
// There is ONE path here where blob.go has two. Its promote branches on size
// because the S3 protocol will not copy above five gibibytes in a single
// request, so a large object must go through a multipart copy. The pinned
// client's Copier.Run loops on the rewrite operation internally until the
// service reports it done, so object size does not change the call — and,
// usefully, a promote interrupted partway leaves no incomplete upload to
// reclaim, only work to redo.
//
// The source names an exact generation for the reason blob.go names an exact
// version: a copy of whatever is current at the staging key is not the object
// this writer staged and holds a lease on. The DESTINATION deliberately does
// not carry one — the pinned client rejects that outright, and it is the right
// rejection, since the destination generation is the thing being created.
func (g *GCS) Promote(ctx context.Context, stagingKey, stagingVersion, digestKey string) (string, error) {
	generation, err := parseGeneration(stagingKey, stagingVersion)
	if err != nil {
		return "", err
	}
	source := g.object(stagingKey).Generation(generation)
	attrs, err := g.object(digestKey).CopierFrom(source).Run(ctx)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return "", fmt.Errorf("%w: %s generation %d", ErrNoSuchObject, stagingKey, generation)
		}
		return "", fmt.Errorf("promote %s generation %d to %s: %w",
			stagingKey, generation, digestKey, err)
	}
	return fencedGeneration(digestKey, attrs.Generation)
}

// Get streams an object back. The caller verifies the bytes.
//
// Unlike the S3 client's lazy reader, NewReader issues its request here, so a
// missing key surfaces now rather than at the first read. That is the
// behaviour blob.go has to add a Stat call to obtain.
func (g *GCS) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	reader, err := g.object(key).NewReader(ctx)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNoSuchObject, key)
		}
		return nil, fmt.Errorf("open %s: %w", key, err)
	}
	return reader, nil
}

// Exists reports whether a key has a live generation.
func (g *GCS) Exists(ctx context.Context, key string) (bool, error) {
	if _, err := g.object(key).Attrs(ctx); err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("stat %s: %w", key, err)
	}
	return true, nil
}

// ListVersions enumerates every generation under a prefix, live and noncurrent.
//
// IsDeleteMarker IS ALWAYS FALSE, and that is a fact about GCS rather than an
// omission here. MEASURED: deleting the live object under a versioned bucket
// adds NO entry to the listing — the count does not change. The generation
// that was live simply gains a deletion timestamp and becomes noncurrent,
// still holding its bytes at full size. S3 writes a zero-byte tombstone in
// that situation and blob.go must report it; there is nothing equivalent to
// report here.
//
// The consequence for the sweep is that it sees fewer states, not more: every
// entry this returns is real stored bytes that DeleteVersion can name. The
// S3 adapter's listing mixes those with tombstones that occupy no space.
//
// LastModified carries the generation's creation time, which is the analogue
// of the S3 version's last-modified stamp: the moment these bytes were
// written. It is what the sweep's grace period judges an unreferenced object
// by, since such an object has no row anywhere to date it.
func (g *GCS) ListVersions(ctx context.Context, prefix string) ([]Version, error) {
	//nolint:prealloc // length unknown until the iterator is drained
	var versions []Version
	it := g.client.Bucket(g.bucket).Objects(ctx, &storage.Query{Prefix: prefix, Versions: true})
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return versions, nil
		}
		if err != nil {
			return nil, fmt.Errorf("list versions under %s: %w", prefix, err)
		}
		versions = append(versions, Version{
			LastModified: attrs.Created,
			Key:          attrs.Name,
			VersionID:    strconv.FormatInt(attrs.Generation, 10),
			Size:         attrs.Size,
			// See the doc comment: GCS has no delete-marker entry to report.
			IsDeleteMarker: false,
		})
	}
}

// DeleteVersion removes exactly one generation and nothing else.
//
// ⚠️ WHETHER THIS RECLAIMS STORAGE IS A PROPERTY OF THE BUCKET, NOT OF THIS
// CODE. GCS creates every bucket with a seven-day soft-delete policy unless
// told otherwise. Under it this call returns success and the generation leaves
// the versioned listing, but the bytes are retained and billed until their
// hard-delete time — invisible to ListVersions, and unreachable by a second
// call to this method. A sweep would report storage reclaimed and the bill
// would not move. MEASURED, and recorded on #286.
//
// The obligation therefore sits with whatever provisions the bucket: soft
// delete must be disabled BEFORE the first write and positively verified,
// exactly as blob.go's EnsureBucket verifies versioning rather than trusting
// that it enabled it. Ordering matters because disabling later does not
// retroactively release what is already retained — and worse, it makes that
// residue unobservable, since the API then refuses to report soft-deleted
// objects at all.
//
// Nothing here can check that: a bucket-policy read is provisioning authority,
// which is precisely what the Store seam withholds from adapters so that
// reading an object does not require the rights to reconfigure the bucket.
func (g *GCS) DeleteVersion(ctx context.Context, key, versionID string) error {
	generation, err := parseGeneration(key, versionID)
	if err != nil {
		return err
	}
	if err := g.object(key).Generation(generation).Delete(ctx); err != nil {
		// A generation that is already gone is the outcome this asked for,
		// and tolerating it is what lets a deletion claim clear. The claim
		// exists for a crash AFTER the remote delete and BEFORE the row
		// clears, so the next reconciliation re-issues a delete for something
		// no longer there; reporting that as a failure would strand the claim
		// permanently, on the one path whose purpose is finishing work an
		// earlier actor could not.
		if !errors.Is(err, storage.ErrObjectNotExist) {
			return fmt.Errorf("delete %s generation %d: %w", key, generation, err)
		}
	}
	return nil
}

// IncompleteWrites reports that GCS reclaims interrupted writes itself.
//
// This is the declaration the capability on Store was introduced for, and it
// is a measured claim rather than a convenience. A resumable upload session
// that has accepted data and never been finalized is invisible to EVERY
// listing surface the API offers: MEASURED by opening a session, writing a
// 256 KiB chunk, and querying the live, versioned and soft-deleted listings —
// all three reported nothing. There is no operation that enumerates sessions
// and none that aborts one by name; they expire on the service's own schedule.
//
// So this adapter does not implement IncompleteWriteReclaimer, and the
// omission is the point. If it declared enumeration and returned an empty
// slice, the sweep would record that it looked and found nothing to reclaim —
// a claim about the bucket, when the truth is a statement about this adapter's
// blindness. Keeping the two apart is the same unavailable-versus-zero
// discipline ADR 0025 applies to benchmark records.
func (g *GCS) IncompleteWrites() IncompleteWriteSupport {
	return IncompleteWritesProviderReclaimed
}

// Compile-time proof that the adapter satisfies the neutral surface.
//
// There is no assertion that it does NOT implement IncompleteWriteReclaimer,
// because a negative like that cannot be written this way: the language offers
// no "does not satisfy" constraint, and a var declaration that fails to
// compile is not a test anyone can run. It is guarded at runtime instead, in
// the test that asserts the type assertion fails — the same relationship the
// postgres store validates at construction.
var _ Store = (*GCS)(nil)

// parseGeneration converts a seam version id into a GCS generation.
//
// The seam speaks version ids as strings because that is what an S3 version
// is. A GCS generation is an int64, so every entry point that accepts one has
// to convert, and this is the single place that decides what counts as usable.
func parseGeneration(key, versionID string) (int64, error) {
	if versionID == "" {
		return 0, fmt.Errorf("refusing to act on %s without a generation: an unqualified request "+
			"addresses whatever version is live rather than the one that was named", key)
	}
	generation, err := strconv.ParseInt(versionID, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("generation %q on %s is not an integer: %w", versionID, key, err)
	}
	if generation <= 0 {
		return 0, fmt.Errorf("generation %d on %s is not a valid generation: %w",
			generation, key, errUnusableGeneration)
	}
	return generation, nil
}

// fencedGeneration rejects a write whose generation cannot fence a delete.
//
// It is the GCS counterpart of fencedVersion, and it has one case where that
// has two. S3 has an empty version id AND a literal "null" version, the second
// being a reusable slot rather than an immutable generation, which is why
// blob.go must reject a value the server will happily accept for deletion.
// GCS has no such slot: a generation is always a distinct immutable number,
// and the pinned client uses -1 rather than 0 to mean "unspecified", so
// anything non-positive is an absent answer rather than a usable one.
func fencedGeneration(key string, generation int64) (string, error) {
	if generation <= 0 {
		return "", fmt.Errorf("%s was stored without a usable generation (%d): no later delete can "+
			"name what was written here: %w", key, generation, errUnusableGeneration)
	}
	return strconv.FormatInt(generation, 10), nil
}

// errUnusableGeneration marks a generation that cannot fence a later delete,
// so a caller can distinguish it from a transport failure without matching on
// message text.
var errUnusableGeneration = errors.New("unusable generation")

// crc32cTable is the Castagnoli table GCS computes its CRC32C with, named
// because the wrong polynomial here would not fail loudly: IEEE is the
// package default, produces a perfectly valid checksum, and would simply
// never agree with the service's — turning every upload into a reported
// corruption. The polynomial is pinned by test rather than by a type
// assertion, since every table produces the same type.
//
//nolint:gochecknoglobals // Immutable lookup table, built once at init.
var crc32cTable = crc32.MakeTable(crc32.Castagnoli)

// Compile-time proof that DeleteVersion satisfies the remover discardCorrupt
// takes. If that signature drifts, the call site would need a wrapper — and a
// wrapper is somewhere a removal can quietly become something other than a
// removal, which is the one thing that path must not do.
var _ removeVersion = (*GCS)(nil).DeleteVersion
