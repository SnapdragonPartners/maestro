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

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Config locates the object store and the credentials to reach it.
type Config struct {
	Endpoint  string
	Bucket    string
	AccessKey string
	SecretKey string
	// UseTLS is false for the local stack, which listens on loopback.
	UseTLS bool
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
}

// Version is one stored version of a key, including delete markers.
type Version struct {
	Key            string
	VersionID      string
	Size           int64
	IsDeleteMarker bool
}

// Upload is one multipart upload that was started and never completed.
// These are a third storage state: invisible to version listing, and
// unreachable by version deletion.
type Upload struct {
	Key      string
	UploadID string
}

// ErrNoSuchObject reports a key or version that is not present.
var ErrNoSuchObject = errors.New("object not found")

// New builds an adapter. It does not contact the server.
func New(cfg Config) (*Blob, error) {
	return newBlob(cfg, nil)
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
	return &Blob{core: core, bucket: cfg.Bucket}, nil
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
		// SHA-256 here is TRANSPORT integrity: the server verifies what it
		// received against what the client computed. It is not proof of the
		// object's address — a multipart upload's value is a composite
		// checksum-of-checksums, not the full-object digest.
		Checksum: minio.ChecksumSHA256,
	})
	if err != nil {
		return "", fmt.Errorf("upload staging object %s: %w", key, err)
	}
	return info.VersionID, nil
}

// Promote server-side copies a staging object onto its digest key and
// returns the new version.
func (b *Blob) Promote(ctx context.Context, stagingKey, digestKey string) (string, error) {
	info, err := b.core.Client.CopyObject(ctx,
		minio.CopyDestOptions{Bucket: b.bucket, Object: digestKey},
		minio.CopySrcOptions{Bucket: b.bucket, Object: stagingKey})
	if err != nil {
		return "", fmt.Errorf("promote %s to %s: %w", stagingKey, digestKey, err)
	}
	return info.VersionID, nil
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
		return fmt.Errorf("delete %s version %s: %w", key, versionID, err)
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
				Key:      result.Uploads[i].Key,
				UploadID: result.Uploads[i].UploadID,
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
// MEASURED: the pinned MinIO image IGNORES this parameter and answers with
// every upload it has, `IsTruncated` false, whatever is asked for — one
// upload requested returns four. The paging above is therefore dead against
// this server and cannot be exercised by it at any bucket size, which is
// why the marker arithmetic is tested against canned responses instead.
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
		return fmt.Errorf("abort upload %s on %s: %w", uploadID, key, err)
	}
	return nil
}

// isNoSuchKey recognises the store's not-found response.
func isNoSuchKey(err error) bool {
	return minio.ToErrorResponse(err).Code == "NoSuchKey"
}
