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

// ListIncompleteUploads enumerates multipart uploads started and never
// completed, with their upload ids.
//
// This is the third storage state, and the other two cannot see it: parts
// are not an object version, so ListVersions does not report them and
// DeleteVersion cannot remove them. A process that dies mid-upload leaves
// them behind forever unless something enumerates them by name.
func (b *Blob) ListIncompleteUploads(ctx context.Context, prefix string) ([]Upload, error) {
	var (
		uploads        []Upload
		keyMarker      string
		uploadIDMarker string
	)
	for {
		result, err := b.core.ListMultipartUploads(ctx, b.bucket, prefix,
			keyMarker, uploadIDMarker, "", listPageSize)
		if err != nil {
			return nil, fmt.Errorf("list incomplete uploads under %s: %w", prefix, err)
		}
		for i := range result.Uploads {
			// Indexed rather than ranged by value: ObjectMultipartInfo is
			// 160 bytes and only two fields are wanted.
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
// protocol's own default maximum; paging is implemented either way, so it
// only decides how many round trips a large sweep costs.
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
