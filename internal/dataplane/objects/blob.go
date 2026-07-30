// Package objects is the data plane's object storage.
//
// This file is Layer 1 of the item 6 design: the blob adapter. It knows
// bytes, keys and versions, and nothing about artifacts, attachments or
// pins — those are the seam's, and mixing them was the first thing the
// design got wrong.
//
// The vocabulary is deliberately version-aware. The bucket has versioning
// enabled, because the sweep fences a late delete by naming the version it
// removes, and under a versioned bucket the convenient primitives change
// meaning: a key-level delete writes a DELETE MARKER and leaves the bytes,
// and a prefix listing does not enumerate versions or markers at all. So
// there is no key-level delete and no plain prefix list here; every
// destructive operation names exactly what it removes.
package objects

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Config locates the object store and the credentials to reach it.
//
//nolint:govet // fieldalignment: grouped by what it configures, read once at startup
type Config struct {
	Endpoint  string
	Bucket    string
	AccessKey string
	SecretKey string
	// UseTLS is false for the local stack, which listens on loopback.
	UseTLS bool

	// Transport replaces the HTTP transport, for the fault injection design
	// D7 requires: "the adapter is fronted by a fault-injecting decorator,
	// and each step is failed in turn". Several of those steps cannot be
	// failed any other way -- a promote that dies halfway, a delete that
	// fails while the write it follows succeeded -- because the store
	// itself has no way to be asked for them.
	//
	// Nil in production, which is every caller outside a test.
	Transport http.RoundTripper
}

// Blob is the S3-compatible adapter.
//
// It holds both client faces the design needs: the high-level client for
// objects and versions, and the lower-level Core for multipart uploads,
// which is the only face that is upload-id-specific. The high-level
// RemoveIncompleteUpload aborts every upload on a key, which is safe for a
// staging key that is unique per upload and unsafe for a digest key that is
// reused — so it is not used here at all.
type Blob struct {
	core   *minio.Core
	bucket string
	// copyLimit is the size above which a promote must go multipart. It is
	// a field only because a test cannot upload five gibibytes to reach
	// the branch, and an untested multipart-copy path is the one that runs
	// for the largest, most expensive evidence media.
	copyLimit int64
}

// Version is one stored version of a key, including delete markers.
//
// LastModified is the store's own record of when this version was written,
// and it is here for the sweep's grace period: an unreferenced object has no
// row anywhere, so its age is knowable only from the object store.
type Version struct {
	LastModified   time.Time
	Key            string
	VersionID      string
	Size           int64
	IsDeleteMarker bool
}

// Upload is one multipart upload that was started and never completed.
// These are a third storage state: invisible to version listing, and
// unreachable by version deletion.
//
// Initiated dates the upload for the same reason Version carries a
// timestamp: a digest key whose only residue is an incomplete upload is a
// sweep candidate, and the grace period has to be able to judge it too.
type Upload struct {
	Initiated time.Time
	Key       string
	UploadID  string
}

// ErrNoSuchObject reports a key or version that is not present.
var ErrNoSuchObject = errors.New("object not found")

// New builds an adapter. It does not contact the server.
//
//nolint:gocritic // hugeParam: by value, so a caller cannot mutate it afterwards
func New(cfg Config) (*Blob, error) {
	return newBlob(cfg, cfg.Transport)
}

// newBlob builds an adapter over a caller-supplied transport.
//
// The transport seam exists because two of this module's guards cannot be
// proven through the client at all: the upload checksum is transport
// integrity, so demonstrating that the server enforces it means corrupting
// the body AFTER the client has computed the header, and the versioning
// check refuses a state a cooperating server will not report. Both are
// claims the design requires measuring rather than citing, and a guard that
// can only be described is a guard nobody has seen fire.
//
//nolint:gocritic // hugeParam: by value, matching New
func newBlob(cfg Config, transport http.RoundTripper) (*Blob, error) {
	if cfg.Endpoint == "" || cfg.Bucket == "" {
		return nil, errors.New("object store endpoint and bucket are required")
	}
	// The endpoint is stored as a URL in the bootstrap pointer and taken as
	// a host:port here, so the scheme is stripped rather than passed
	// through to a client that would treat it as part of the host.
	endpoint := strings.TrimPrefix(strings.TrimPrefix(cfg.Endpoint, "http://"), "https://")

	core, err := minio.NewCore(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseTLS,
		// Required for the upload checksum below, which the pinned client
		// sends as a trailing header: without this it refuses the request
		// outright with "Checksum requires Client with TrailingHeaders
		// enabled", so every upload fails rather than falling back to an
		// unverified one. Measured against v7.2.1, not assumed.
		TrailingHeaders: true,
		Transport:       transport,
	})
	if err != nil {
		return nil, fmt.Errorf("build object store client: %w", err)
	}
	return &Blob{core: core, bucket: cfg.Bucket, copyLimit: singleCopyLimit}, nil
}

// EnsureBucket creates the bucket if it is missing, enables versioning, and
// VERIFIES that versioning is on.
//
// Nothing had ever created this bucket: the stack named it and published it
// in the bootstrap pointer, and no code issued a create — so a clean machine
// reported a ready plane that could not store an object.
//
// Verification is not the same as enabling. Versioning can be turned off
// after the fact by an operator, a restored backup or a stray `mc` command,
// and every fence in the sweep depends on it — a version-specific delete is
// what stops a delayed delete from removing a newer writer's object. If it
// cannot be confirmed, the plane refuses to start rather than running
// unprotected.
func (b *Blob) EnsureBucket(ctx context.Context) error {
	exists, err := b.core.BucketExists(ctx, b.bucket)
	if err != nil {
		return fmt.Errorf("check bucket %s: %w", b.bucket, err)
	}
	if !exists {
		if makeErr := b.core.MakeBucket(ctx, b.bucket, minio.MakeBucketOptions{}); makeErr != nil {
			// A concurrent creator is not a failure: two processes running
			// `up` at once is ordinary, and the bucket existing is the
			// outcome both wanted.
			present, checkErr := b.core.BucketExists(ctx, b.bucket)
			if checkErr != nil || !present {
				return fmt.Errorf("create bucket %s: %w", b.bucket, makeErr)
			}
		}
	}
	if versionErr := b.core.EnableVersioning(ctx, b.bucket); versionErr != nil {
		return fmt.Errorf("enable versioning on %s: %w", b.bucket, versionErr)
	}

	config, err := b.core.GetBucketVersioning(ctx, b.bucket)
	if err != nil {
		return fmt.Errorf("read versioning state of %s: %w", b.bucket, err)
	}
	if !config.Enabled() {
		return fmt.Errorf("versioning on bucket %s reports status %q after being enabled; "+
			"every fence in the object sweep is version-specific, so the plane will not run without it",
			b.bucket, config.Status)
	}
	return nil
}

// PutStaged uploads to a staging key and returns the stored version.
//
// The caller streams through its own hashing reader: this layer does not
// know what the bytes are supposed to be, and the design keeps content
// verification at the seam where the claimed digest lives.
func (b *Blob) PutStaged(ctx context.Context, key string, size int64, body io.Reader) (string, error) {
	info, err := b.core.Client.PutObject(ctx, b.bucket, key, body, size, minio.PutObjectOptions{
		// SHA-256 here is TRANSPORT integrity, and the wire has two
		// mechanisms: the client stream-signs each chunk, and this header
		// is verified on an unsigned payload. Neither is proof of the
		// object's address — both are computed by the client from the same
		// buffer, and a multipart upload's value is a composite
		// checksum-of-checksums rather than the full-object digest.
		Checksum: minio.ChecksumSHA256,
	})
	if err != nil {
		return "", fmt.Errorf("upload staging object %s: %w", key, err)
	}
	return fencedVersion(key, info.VersionID)
}

// Promote server-side copies one staged VERSION onto its digest key and
// returns the new version.
//
// The staged version is named rather than implied. A copy that takes
// whatever is current at the staging key is not the object this writer
// staged and holds a lease on: staging keys are unique per upload, so this
// should not happen — but "should not happen" is what the lease, the token
// and the row lock all exist to stop being load-bearing, and naming the
// version costs one field.
func (b *Blob) Promote(ctx context.Context, stagingKey, stagingVersion, digestKey string) (string, error) {
	if stagingVersion == "" {
		return "", fmt.Errorf("refusing to promote %s without a version id: the copy would take "+
			"whatever version is current rather than the one that was staged and verified", stagingKey)
	}
	source := minio.CopySrcOptions{Bucket: b.bucket, Object: stagingKey, VersionID: stagingVersion}
	dest := minio.CopyDestOptions{Bucket: b.bucket, Object: digestKey}

	// The size decides which copy the protocol permits, so it has to be
	// known first. Statting the exact version also fails here — before
	// anything is written to the digest key — if the staged version is gone.
	staged, err := b.core.Client.StatObject(ctx, b.bucket, stagingKey,
		minio.StatObjectOptions{VersionID: stagingVersion})
	if err != nil {
		if isNoSuchKey(err) {
			return "", fmt.Errorf("%w: %s version %s", ErrNoSuchObject, stagingKey, stagingVersion)
		}
		return "", fmt.Errorf("stat staged %s version %s: %w", stagingKey, stagingVersion, err)
	}

	var info minio.UploadInfo
	if staged.Size <= b.copyLimit {
		info, err = b.core.Client.CopyObject(ctx, dest, source)
	} else {
		info, err = b.core.Client.ComposeObject(ctx, dest, source)
	}
	if err != nil {
		return "", fmt.Errorf("promote %s version %s to %s (%d bytes): %w",
			stagingKey, stagingVersion, digestKey, staged.Size, err)
	}
	return fencedVersion(digestKey, info.VersionID)
}

// singleCopyLimit is the largest object the protocol will copy in one
// request. Above it the copy must be multipart, which is what
// ComposeObject does — evidence media are exactly the objects that get
// there, and the design's own cleanup assumes a promote can be multipart
// and die halfway.
//
// The branch is explicit because ComposeObject does NOT fall back for a
// small object at the pinned version, whatever its shape suggests: the
// single-request path requires a source `Start` of -1, and the option
// validator rejects any negative `Start`, so the fallback is unreachable
// through that entry point. MEASURED — composing a twelve-byte object
// yields a multipart ETag. Sending every promote through a three-step
// multipart copy would also mean every promote could leave an incomplete
// upload on a digest key.
const singleCopyLimit = 5 * 1024 * 1024 * 1024

// nullVersion is the id a store reports for an object it holds without a
// version — what a write lands as while versioning is off or suspended.
const nullVersion = "null"

// fencedVersion rejects a write whose version id cannot fence a delete.
//
// Every deletion in this module names a version, because a delete issued
// under a lock can arrive after that lock is gone, and naming one immutable
// generation is what stops it removing a newer writer's object. The two
// unusable answers fail that for different reasons, and neither is "the
// object cannot be reclaimed" — the sweep reclaims a null version by name,
// which ListVersions and DeleteVersion both support deliberately:
//
//   - an EMPTY id gives a later delete nothing to name at all;
//   - the NULL id is deletable, but it is a SLOT rather than a generation.
//     Every unversioned write to that key lands as `null` again, so a
//     delayed delete condemning this object removes whatever occupies the
//     slot when it arrives, which is the unfenced delete versioning exists
//     to close.
//
// MEASURED: a write to an unversioned or suspended bucket returns an EMPTY
// id on the pinned server, and the literal "null" on a store that reports
// S3's null version.
func fencedVersion(key, version string) (string, error) {
	switch version {
	case "":
		return "", fmt.Errorf("%s was stored without a version id: the bucket is not versioned, so "+
			"no later delete can name what was written here", key)
	case nullVersion:
		return "", fmt.Errorf("%s was stored as the %q version: the bucket is not versioned. That id "+
			"can be deleted, but it names a slot every unversioned write reuses rather than this "+
			"object, so a delayed delete would remove whatever occupies it on arrival", key, nullVersion)
	}
	return version, nil
}

// Get streams an object back. The caller verifies the bytes; a reader that
// is not hashed on the way past is a reader whose corruption is invisible.
func (b *Blob) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	object, err := b.core.Client.GetObject(ctx, b.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", key, err)
	}
	// GetObject is lazy: it reports a missing key on the first read, not
	// here. Stat now so a caller learns immediately rather than at EOF,
	// where this module's own verification failure also surfaces and the
	// two would be indistinguishable.
	if _, err := object.Stat(); err != nil {
		_ = object.Close()
		if isNoSuchKey(err) {
			return nil, fmt.Errorf("%w: %s", ErrNoSuchObject, key)
		}
		return nil, fmt.Errorf("stat %s: %w", key, err)
	}
	return object, nil
}

// Exists reports whether a key has a current version.
func (b *Blob) Exists(ctx context.Context, key string) (bool, error) {
	_, err := b.core.Client.StatObject(ctx, b.bucket, key, minio.StatObjectOptions{})
	if err == nil {
		return true, nil
	}
	if isNoSuchKey(err) {
		return false, nil
	}
	return false, fmt.Errorf("stat %s: %w", key, err)
}

// ListVersions enumerates every version under a prefix, INCLUDING delete
// markers and the `null` version left by anything written before versioning
// was enabled. A sweep that skipped either would leave storage it believed
// it had reclaimed.
func (b *Blob) ListVersions(ctx context.Context, prefix string) ([]Version, error) {
	// Not pre-allocated: the page size is unknown before the channel drains,
	// and a guess would allocate for an empty prefix on every sweep.
	var versions []Version //nolint:prealloc // length unknown until the listing channel closes
	for info := range b.core.Client.ListObjects(ctx, b.bucket, minio.ListObjectsOptions{
		Prefix:       prefix,
		Recursive:    true,
		WithVersions: true,
	}) {
		if info.Err != nil {
			return nil, fmt.Errorf("list versions under %s: %w", prefix, info.Err)
		}
		versions = append(versions, Version{
			LastModified:   info.LastModified,
			Key:            info.Key,
			VersionID:      info.VersionID,
			Size:           info.Size,
			IsDeleteMarker: info.IsDeleteMarker,
		})
	}
	return versions, nil
}

// DeleteVersion removes exactly one version and nothing else.
//
// There is no key-level delete in this adapter. On a versioned bucket that
// call writes a delete marker — reclaiming nothing — and it cannot be
// fenced against a later writer, which is the whole reason the sweep names
// versions.
func (b *Blob) DeleteVersion(ctx context.Context, key, versionID string) error {
	if versionID == "" {
		return fmt.Errorf("refusing to delete %s without a version id: a key-level delete writes a "+
			"delete marker and leaves the bytes", key)
	}
	if err := b.core.Client.RemoveObject(ctx, b.bucket, key, minio.RemoveObjectOptions{
		VersionID: versionID,
	}); err != nil {
		// A version that is already gone is the outcome this asked for, and
		// tolerating it is what makes the deletion claim's central promise
		// true. The claim exists for a crash AFTER the remote delete and
		// BEFORE the row clears, so the very next reconciliation re-issues a
		// delete for a version that is no longer there. Reporting that as a
		// failure would strand the claim permanently -- on the one path whose
		// whole purpose is finishing work an earlier actor could not.
		//
		// "Repeating a version-specific delete is harmless by construction"
		// has to be a property of this adapter rather than of one server's
		// leniency, which is the difference between a claim that clears and a
		// claim retried forever.
		//
		// MEASURED: the pinned server returns no error at all for a repeated
		// delete, for an unknown version id on a live key, or for a key that
		// never existed -- so the integration suite cannot tell whether this
		// tolerance is here. A store that answers NoSuchVersion or NoSuchKey
		// can, and it is unit-tested against a canned response instead, as
		// AbortUpload's equivalent tolerance is.
		if !isNoSuchKey(err) {
			return fmt.Errorf("delete %s version %s: %w", key, versionID, err)
		}
	}
	return nil
}

// Incomplete multipart uploads are the third storage state, and the other
// two cannot see it: parts are not an object version, so ListVersions does
// not report them and DeleteVersion cannot remove them. A process that dies
// mid-upload leaves them behind forever unless something enumerates them.
//
// There are two operations rather than one, because the server offers two
// modes and no third. MEASURED against the pinned MinIO image
// (RELEASE.2025-09-07T16-13-09Z), `ListMultipartUploads` treats its prefix
// parameter as an EXACT object key:
//
//	prefix ""                    -> every upload in the bucket
//	prefix "staging/org/upload"  -> that key's uploads
//	prefix "staging/"            -> NOTHING, with no error
//	prefix "staging/org/uploa"   -> NOTHING, with no error
//
// This is deliberate upstream and long-standing, not a defect in this
// deployment (minio/minio#11686, closed as intended; #20989, open), and it
// diverges from S3. A single prefix-taking operation would therefore be a
// silent lie: a sweep asking for one organization's residue would be told
// there is none and would reclaim nothing, which is the same
// correct-on-the-easy-case failure the composite checksum had.
//
// So prefix matching happens HERE, over the only listing the server will
// actually answer, and each caller names which question it is asking. Both
// operations filter client-side, which also makes them correct against a
// store with true prefix semantics: real S3 would answer the exact-key form
// with `key` plus every key it prefixes, and the abort fence depends on not
// getting those.

// ListUploadsForKey enumerates the incomplete uploads on exactly one key.
func (b *Blob) ListUploadsForKey(ctx context.Context, key string) ([]Upload, error) {
	if key == "" {
		return nil, errors.New("refusing to list uploads for an empty key: on this server that " +
			"enumerates the whole bucket")
	}
	return b.listUploads(ctx, key, func(candidate string) bool { return candidate == key })
}

// ListUploadsUnder enumerates every incomplete upload whose key carries the
// given prefix. An empty prefix means the whole bucket, which is what the
// sweep's candidate discovery and teardown both want.
func (b *Blob) ListUploadsUnder(ctx context.Context, prefix string) ([]Upload, error) {
	return b.listUploads(ctx, "", func(candidate string) bool {
		return strings.HasPrefix(candidate, prefix)
	})
}

// listUploads pages the multipart listing, keeping what the caller wants.
func (b *Blob) listUploads(ctx context.Context, serverPrefix string, keep func(key string) bool) ([]Upload, error) {
	var (
		uploads        []Upload
		keyMarker      string
		uploadIDMarker string
	)
	for {
		result, err := b.core.ListMultipartUploads(ctx, b.bucket, serverPrefix,
			keyMarker, uploadIDMarker, "", listPageSize)
		if err != nil {
			return nil, fmt.Errorf("list incomplete uploads: %w", err)
		}
		for i := range result.Uploads {
			// Indexed rather than ranged by value: ObjectMultipartInfo is
			// 160 bytes and only two fields are wanted.
			if !keep(result.Uploads[i].Key) {
				continue
			}
			uploads = append(uploads, Upload{
				Initiated: result.Uploads[i].Initiated,
				Key:       result.Uploads[i].Key,
				UploadID:  result.Uploads[i].UploadID,
			})
		}
		if !result.IsTruncated {
			return uploads, nil
		}
		// Both markers advance together: paging on the key alone repeats
		// every upload after the first for a key with several in flight.
		keyMarker, uploadIDMarker = result.NextKeyMarker, result.NextUploadIDMarker
	}
}

// listPageSize bounds one page of multipart uploads. The value is the
// protocol's own default maximum.
//
// MEASURED: the pinned MinIO image IGNORES this parameter — asked for one
// upload with four present it returns all four, `IsTruncated` false. That
// is the only lever a test has, so nothing here establishes what it would
// do at a scale where it might truncate of its own accord; what is
// established is that paging above cannot be reached by asking. The marker
// arithmetic is tested against canned responses instead.
//
// It stays because a store that honours the protocol will truncate at a
// thousand and answer the rest only to a correct pair of markers, and
// ADR 0022 names other backends as a later choice.
const listPageSize = 1000

// AbortUpload aborts exactly one upload id on one key.
//
// Upload-id-specific on purpose. The high-level client's
// RemoveIncompleteUpload aborts EVERY upload in progress on a key, which is
// safe for a staging key that is unique per upload and actively unsafe for
// a digest key, which is reused: a delayed abort would kill a newer
// writer's promotion. Naming the id is the fence.
func (b *Blob) AbortUpload(ctx context.Context, key, uploadID string) error {
	if uploadID == "" {
		return fmt.Errorf("refusing to abort uploads on %s without an upload id: a key-scoped abort "+
			"would cancel a concurrent writer's upload", key)
	}
	if err := b.core.AbortMultipartUpload(ctx, b.bucket, key, uploadID); err != nil {
		// An upload that is already gone is the outcome this asked for.
		// Cleanup is re-run by construction — a crash between the server
		// aborting and the lease or claim recording it leaves the
		// reconciler to retry the same id — so an error here would strand
		// that claim permanently, on the one path whose whole purpose is
		// to finish work an earlier actor could not.
		//
		// MEASURED: the pinned server returns no error at all for a repeat
		// abort, or for an id that never existed. S3 answers NoSuchUpload,
		// so this tolerance is for a store that follows the protocol and
		// cannot be exercised here; it is unit-tested against a canned
		// response instead.
		if minio.ToErrorResponse(err).Code != noSuchUpload {
			return fmt.Errorf("abort upload %s on %s: %w", uploadID, key, err)
		}
	}
	return nil
}

// noSuchUpload is the response code for an upload id the server does not
// hold — already aborted, already completed, or never started.
const noSuchUpload = "NoSuchUpload"

// isNoSuchKey recognises the store's not-found responses.
//
// There are two, and they are not interchangeable: a key that does not
// exist answers NoSuchKey, and a key whose NAMED VERSION is gone answers
// NoSuchVersion — which is what a promote sees when cleanup removed the
// staged version out from under it. Measured, because matching only the
// first turned a routine lost-lease outcome into an unrecognised error.
//
// A malformed version id is deliberately NOT here: it answers
// InvalidArgument with status 400, and it means the caller passed
// something that was never a version id rather than one that has gone.
func isNoSuchKey(err error) bool {
	switch minio.ToErrorResponse(err).Code {
	case "NoSuchKey", "NoSuchVersion":
		return true
	default:
		return false
	}
}
