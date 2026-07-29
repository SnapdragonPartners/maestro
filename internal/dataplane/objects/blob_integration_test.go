//go:build integration

// These tests run against the PINNED MinIO image the local stack composes,
// never a mock. Everything this adapter claims is a claim about what an
// S3-compatible server actually does — that a key-level delete leaves the
// bytes behind, that parts of an unfinished upload are invisible to version
// listing, that an upload checksum is enforced — and a mock would only
// replay the belief being tested.
//
// The suite is an INTERNAL test package because the states under test are
// exactly the ones the adapter refuses to create: there is no key-level
// delete here, so a delete marker has to be made with the raw client, and
// an incomplete multipart upload has to be started and abandoned.

package objects

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/minio/minio-go/v7"

	"orchestrator/internal/dataplane/paths"
	"orchestrator/internal/dataplane/secret"
)

// testConfig locates the running local data plane's object store.
//
// It assembles the endpoint here rather than calling stack.Config.Bootstrap
// because internal/dataplane/stack imports THIS package to provision the
// bucket, and an internal test importing stack back would be an import
// cycle in the test binary. The migrations suite duplicates the DSN for the
// same reason; the coupling to the stack package's defaults is deliberate
// and fails loudly by not connecting rather than testing the wrong store.
func testConfig(t *testing.T) Config {
	t.Helper()

	roots, err := paths.Resolve()
	if err != nil {
		t.Skipf("cannot resolve storage roots: %v", err)
	}
	rootKey, err := paths.EnsureKey(roots.Config)
	if err != nil {
		t.Skipf("cannot read the root-of-trust key: %v", err)
	}
	accessKey, err := secret.Derive(rootKey, secret.ContextObjectAccessKey)
	if err != nil {
		t.Fatalf("derive object access key: %v", err)
	}
	secretKey, err := secret.Derive(rootKey, secret.ContextObjectSecretKey)
	if err != nil {
		t.Fatalf("derive object secret key: %v", err)
	}

	port := 59000 // stack.DefaultMinIOPort
	if raw := os.Getenv("MAESTRO_MINIO_PORT"); raw != "" {
		parsed, convErr := strconv.Atoi(raw)
		if convErr != nil {
			t.Fatalf("MAESTRO_MINIO_PORT=%q: %v", raw, convErr)
		}
		port = parsed
	}

	return Config{
		Endpoint:  net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		Bucket:    disposableBucketName(t),
		AccessKey: accessKey,
		SecretKey: secretKey,
	}
}

// disposableBucketName mirrors the migrations suite's disposable database:
// tests write objects and must never do so in the developer's working
// bucket, where a sweep test's cleanup would remove real evidence.
func disposableBucketName(t *testing.T) string {
	t.Helper()
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("generate bucket suffix: %v", err)
	}
	return "maestro-it-" + hex.EncodeToString(suffix)
}

// testBlob returns an adapter over a fresh disposable bucket that has been
// through EnsureBucket, so versioning is on — the state every other
// operation here assumes.
func testBlob(t *testing.T) *Blob {
	t.Helper()
	blob := rawTestBlob(t, testConfig(t))
	if err := blob.EnsureBucket(t.Context()); err != nil {
		t.Fatalf("EnsureBucket: %v", err)
	}
	return blob
}

// rawTestBlob builds an adapter over a named bucket WITHOUT creating it,
// and registers the teardown.
func rawTestBlob(t *testing.T, cfg Config) *Blob {
	t.Helper()
	blob, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { dropBucket(t, cfg) })
	return blob
}

// dropBucket empties and removes a disposable bucket.
//
// A versioned bucket refuses removal while anything at all remains, and
// "anything" includes the two states that are easy to forget: delete
// markers, which are versions, and incomplete multipart uploads, which are
// not. Emptying it through this module's own primitives is also a small
// standing check that they can express every state the tests produce.
//
// It builds its own client so a test that deliberately broke its adapter —
// by corrupting the transport, say — still cleans up after itself.
func dropBucket(t *testing.T, cfg Config) {
	t.Helper()
	blob, err := New(cfg)
	if err != nil {
		t.Errorf("cleanup: build client for %s: %v", cfg.Bucket, err)
		return
	}
	ctx := context.WithoutCancel(t.Context())

	exists, err := blob.core.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		t.Errorf("cleanup: check bucket %s: %v", cfg.Bucket, err)
		return
	}
	if !exists {
		return
	}

	uploads, err := blob.ListUploadsUnder(ctx, "")
	if err != nil {
		t.Errorf("cleanup: list uploads in %s: %v", cfg.Bucket, err)
		return
	}
	for _, upload := range uploads {
		if abortErr := blob.AbortUpload(ctx, upload.Key, upload.UploadID); abortErr != nil {
			t.Errorf("cleanup: abort %s: %v", upload.UploadID, abortErr)
		}
	}

	versions, err := blob.ListVersions(ctx, "")
	if err != nil {
		t.Errorf("cleanup: list versions in %s: %v", cfg.Bucket, err)
		return
	}
	for _, version := range versions {
		if delErr := blob.DeleteVersion(ctx, version.Key, version.VersionID); delErr != nil {
			t.Errorf("cleanup: delete %s@%s: %v", version.Key, version.VersionID, delErr)
		}
	}

	if err := blob.core.RemoveBucket(ctx, cfg.Bucket); err != nil {
		t.Errorf("cleanup: remove bucket %s: %v", cfg.Bucket, err)
	}
}

// put uploads bytes through the adapter and returns the version id.
func put(t *testing.T, blob *Blob, key string, body []byte) string {
	t.Helper()
	version, err := blob.PutStaged(t.Context(), key, int64(len(body)), bytes.NewReader(body))
	if err != nil {
		t.Fatalf("PutStaged(%s): %v", key, err)
	}
	if version == "" {
		t.Fatalf("PutStaged(%s) returned no version id on a versioned bucket", key)
	}
	return version
}

// readAll streams a key back through the adapter.
func readAll(t *testing.T, blob *Blob, key string) []byte {
	t.Helper()
	reader, err := blob.Get(t.Context(), key)
	if err != nil {
		t.Fatalf("Get(%s): %v", key, err)
	}
	defer func() { _ = reader.Close() }()
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read %s: %v", key, err)
	}
	return body
}

// startAbandonedUpload begins a multipart upload, writes one part, and
// never completes it — the residue a writer that dies mid-upload leaves.
//
// The part is far below S3's five-mebibyte minimum, which is legal here
// precisely because the upload is never completed: the size floor is
// enforced at completion, and this upload has no completion.
func startAbandonedUpload(t *testing.T, blob *Blob, key string) string {
	t.Helper()
	ctx := t.Context()
	uploadID, err := blob.core.NewMultipartUpload(ctx, blob.bucket, key, minio.PutObjectOptions{})
	if err != nil {
		t.Fatalf("start upload on %s: %v", key, err)
	}
	part := []byte("abandoned part")
	if _, err := blob.core.PutObjectPart(ctx, blob.bucket, key, uploadID, 1,
		bytes.NewReader(part), int64(len(part)), minio.PutObjectPartOptions{}); err != nil {
		t.Fatalf("upload part on %s: %v", key, err)
	}
	return uploadID
}

func uploadIDs(uploads []Upload) []string {
	ids := make([]string, 0, len(uploads))
	for _, upload := range uploads {
		ids = append(ids, upload.UploadID)
	}
	return ids
}

// --- EnsureBucket -----------------------------------------------------

// TestEnsureBucketCreatesEnablesAndIsIdempotent covers the gap that made
// this item necessary: nothing had ever created the bucket, so a clean
// machine reported a ready plane that could not store an object.
func TestEnsureBucketCreatesEnablesAndIsIdempotent(t *testing.T) {
	cfg := testConfig(t)
	blob := rawTestBlob(t, cfg)
	ctx := t.Context()

	exists, err := blob.core.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		t.Fatalf("check bucket before: %v", err)
	}
	if exists {
		t.Fatal("the disposable bucket already exists; the test is not starting clean")
	}

	if err = blob.EnsureBucket(ctx); err != nil {
		t.Fatalf("EnsureBucket: %v", err)
	}
	config, err := blob.core.GetBucketVersioning(ctx, cfg.Bucket)
	if err != nil {
		t.Fatalf("read versioning: %v", err)
	}
	if !config.Enabled() {
		t.Fatalf("versioning reports %q after EnsureBucket", config.Status)
	}

	// The second `up` is the normal case, and it must not fail on the
	// bucket it created a moment ago.
	if err := blob.EnsureBucket(ctx); err != nil {
		t.Fatalf("second EnsureBucket: %v", err)
	}
}

// TestEnsureBucketReEnablesSuspendedVersioning is the operator-suspended
// case: `mc version suspend`, a restored backup, a console click. Every
// fence in the sweep is version-specific, so the plane must put it back.
func TestEnsureBucketReEnablesSuspendedVersioning(t *testing.T) {
	cfg := testConfig(t)
	blob := rawTestBlob(t, cfg)
	ctx := t.Context()

	if err := blob.EnsureBucket(ctx); err != nil {
		t.Fatalf("EnsureBucket: %v", err)
	}
	if err := blob.core.SuspendVersioning(ctx, cfg.Bucket); err != nil {
		t.Fatalf("suspend versioning: %v", err)
	}
	suspended, err := blob.core.GetBucketVersioning(ctx, cfg.Bucket)
	if err != nil {
		t.Fatalf("read suspended versioning: %v", err)
	}
	if suspended.Enabled() {
		t.Fatal("suspending versioning did not take effect; the test proves nothing")
	}

	if err = blob.EnsureBucket(ctx); err != nil {
		t.Fatalf("EnsureBucket over a suspended bucket: %v", err)
	}
	restored, err := blob.core.GetBucketVersioning(ctx, cfg.Bucket)
	if err != nil {
		t.Fatalf("read restored versioning: %v", err)
	}
	if !restored.Enabled() {
		t.Fatalf("versioning still reports %q after EnsureBucket", restored.Status)
	}
}

// TestEnsureBucketRefusesWhenVersioningCannotBeConfirmed fires the guard
// itself, which no cooperating server will trigger: it re-enables
// versioning on request and then reports it enabled. The verification step
// exists for the case where that is not true, so the response is rewritten
// in transit to say Suspended.
//
// Without this the guard is unfalsifiable — the suite would be green
// whether the check ran or not.
func TestEnsureBucketRefusesWhenVersioningCannotBeConfirmed(t *testing.T) {
	cfg := testConfig(t)
	// The bucket is created and torn down through an honest client; only
	// the verification read is lied to.
	honest := rawTestBlob(t, cfg)
	if err := honest.EnsureBucket(t.Context()); err != nil {
		t.Fatalf("EnsureBucket: %v", err)
	}

	lying, err := newBlob(cfg, suspendedVersioningTransport{})
	if err != nil {
		t.Fatalf("newBlob: %v", err)
	}
	err = lying.EnsureBucket(t.Context())
	if err == nil {
		t.Fatal("EnsureBucket accepted a bucket that reports versioning suspended")
	}
	if !strings.Contains(err.Error(), "version-specific") {
		t.Fatalf("refusal does not name the fence it protects: %v", err)
	}
}

// suspendedVersioningTransport answers the versioning read — and only that
// read — with a suspended configuration, passing everything else through.
type suspendedVersioningTransport struct{}

func (suspendedVersioningTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet || !req.URL.Query().Has("versioning") {
		return http.DefaultTransport.RoundTrip(req)
	}
	body := `<VersioningConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/">` +
		`<Status>Suspended</Status></VersioningConfiguration>`
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/xml"}},
		Request:    req,
	}, nil
}

// --- Read, write, promote ---------------------------------------------

func TestPutStagedPromoteAndRead(t *testing.T) {
	blob := testBlob(t)
	ctx := t.Context()
	body := []byte("evidence bytes")
	stagingKey := "staging/org/upload-1"
	digestKey := "org/aa/bb/aabb"

	stagedVersion := put(t, blob, stagingKey, body)

	if got := readAll(t, blob, stagingKey); !bytes.Equal(got, body) {
		t.Fatalf("staged read returned %q, want %q", got, body)
	}
	exists, err := blob.Exists(ctx, stagingKey)
	if err != nil {
		t.Fatalf("Exists(staging): %v", err)
	}
	if !exists {
		t.Fatal("Exists reports the staged object missing")
	}

	promotedVersion, err := blob.Promote(ctx, stagingKey, stagedVersion, digestKey)
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if promotedVersion == stagedVersion {
		t.Fatal("the promoted copy carries the staging object's version id")
	}
	// Read back THROUGH S3: MinIO inlines small objects into xl.meta, so a
	// host-side read of the bind mount is not the object body.
	if got := readAll(t, blob, digestKey); !bytes.Equal(got, body) {
		t.Fatalf("promoted read returned %q, want %q", got, body)
	}
}

// TestPromoteCopiesTheNamedVersion is the fence on the copy itself: a
// promote must move the version the writer staged and verified, not
// whatever is current at the key when the copy is issued.
func TestPromoteCopiesTheNamedVersion(t *testing.T) {
	blob := testBlob(t)
	ctx := t.Context()
	stagingKey := "staging/org/upload-1"
	digestKey := "org/aa/bb/aabb"

	staged := put(t, blob, stagingKey, []byte("the verified bytes"))
	// Something writes the staging key again. It should not be possible on
	// a key that is unique per upload, which is exactly why relying on that
	// rather than naming the version is the kind of assumption this module
	// keeps refusing to make.
	put(t, blob, stagingKey, []byte("a different object entirely"))

	if _, err := blob.Promote(ctx, stagingKey, staged, digestKey); err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if got := readAll(t, blob, digestKey); !bytes.Equal(got, []byte("the verified bytes")) {
		t.Fatalf("promote landed %q, want the version it was told to copy", got)
	}
}

func TestPromoteRefusesAnUnnamedVersion(t *testing.T) {
	blob := testBlob(t)
	stagingKey := "staging/org/upload-1"
	put(t, blob, stagingKey, []byte("evidence bytes"))

	_, err := blob.Promote(t.Context(), stagingKey, "", "org/aa/bb/aabb")
	if err == nil {
		t.Fatal("Promote accepted a staging object with no version named")
	}
	exists, err := blob.Exists(t.Context(), "org/aa/bb/aabb")
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if exists {
		t.Fatal("the refused promote wrote to the digest key anyway")
	}
}

// TestPromoteReportsAMissingStagedVersion covers the window the lease
// exists to close: cleanup deleted the staged version before this writer
// promoted it. It must fail before anything reaches the digest key, not
// leave a partial object there.
func TestPromoteReportsAMissingStagedVersion(t *testing.T) {
	blob := testBlob(t)
	ctx := t.Context()
	stagingKey := "staging/org/upload-1"
	digestKey := "org/aa/bb/aabb"

	staged := put(t, blob, stagingKey, []byte("evidence bytes"))
	if err := blob.DeleteVersion(ctx, stagingKey, staged); err != nil {
		t.Fatalf("DeleteVersion: %v", err)
	}

	_, err := blob.Promote(ctx, stagingKey, staged, digestKey)
	if !errors.Is(err, ErrNoSuchObject) {
		t.Fatalf("Promote of a deleted staged version returned %v, want ErrNoSuchObject", err)
	}
	exists, err := blob.Exists(ctx, digestKey)
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if exists {
		t.Fatal("a failed promote left an object at the digest key")
	}
}

// TestPromoteGoesMultipartAboveTheCopyLimit exercises the branch that runs
// for the largest evidence media, which is the one no test can reach
// honestly: a single-request copy is capped at five gibibytes, and
// uploading that much to prove it would make the suite unusable. The limit
// is lowered instead, so the multipart path runs over a small object.
//
// A multipart copy is identifiable: its ETag carries a `-<parts>` suffix,
// which a single-request copy does not. That is what makes this test able
// to fail — the bytes come back correct either way.
func TestPromoteGoesMultipartAboveTheCopyLimit(t *testing.T) {
	blob := testBlob(t)
	ctx := t.Context()
	body := []byte("small enough to test, large enough to branch")
	blob.copyLimit = int64(len(body)) - 1

	stagingKey := "staging/org/upload-1"
	digestKey := "org/aa/bb/aabb"
	staged := put(t, blob, stagingKey, body)

	if _, err := blob.Promote(ctx, stagingKey, staged, digestKey); err != nil {
		t.Fatalf("Promote above the copy limit: %v", err)
	}
	if got := readAll(t, blob, digestKey); !bytes.Equal(got, body) {
		t.Fatalf("multipart promote landed %q, want %q", got, body)
	}

	info, err := blob.core.Client.StatObject(ctx, blob.bucket, digestKey, minio.StatObjectOptions{})
	if err != nil {
		t.Fatalf("stat promoted object: %v", err)
	}
	if !strings.Contains(info.ETag, "-") {
		t.Fatalf("promoted object has ETag %q, which is a single-request copy; "+
			"an object over the limit must be copied multipart", info.ETag)
	}
	// A completed multipart copy leaves nothing behind. The sweep's
	// upload enumeration would otherwise find residue from every promote.
	uploads, err := blob.ListUploadsUnder(ctx, "")
	if err != nil {
		t.Fatalf("ListUploadsUnder: %v", err)
	}
	if len(uploads) != 0 {
		t.Fatalf("a completed multipart promote left %d incomplete uploads", len(uploads))
	}
}

// TestWritesRefuseAnUnversionedBucket is the invariant behind every fence
// in the sweep. A write that returns no version id cannot be deleted by
// name later, and the layers above would record an empty string and never
// notice — so the write fails here instead.
func TestWritesRefuseAnUnversionedBucket(t *testing.T) {
	cfg := testConfig(t)
	blob := rawTestBlob(t, cfg)
	ctx := t.Context()

	// Deliberately NOT through EnsureBucket, which is what turns
	// versioning on and verifies it.
	if err := blob.core.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{}); err != nil {
		t.Fatalf("create unversioned bucket: %v", err)
	}

	_, err := blob.PutStaged(ctx, "staging/org/x", 4, strings.NewReader("body"))
	if err == nil {
		t.Fatal("PutStaged accepted a write that produced no version id")
	}
	if !strings.Contains(err.Error(), "not versioned") {
		t.Fatalf("the refusal does not name the cause: %v", err)
	}
	// The object is still there — this is an invariant failure reported
	// after the fact, not a rollback, and saying so matters: the caller's
	// staging cleanup is what removes it.
	exists, err := blob.Exists(ctx, "staging/org/x")
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if !exists {
		t.Fatal("the unversioned write left nothing; this test no longer describes the failure")
	}
}

// TestAbortUploadIsIdempotent covers the reconciler's retry: a crash
// between the server aborting and the claim recording it leaves the same
// upload id to be aborted again.
//
// The pinned server returns no error for a repeat abort or an unknown id,
// so this passes whether or not the tolerance below it exists. It is here
// because the reconciler's retry is a real path and this is what it does;
// the tolerance itself is unit-tested against a canned NoSuchUpload.
func TestAbortUploadIsIdempotent(t *testing.T) {
	blob := testBlob(t)
	ctx := t.Context()
	key := "staging/org/died-mid-upload"
	uploadID := startAbandonedUpload(t, blob, key)

	for attempt := range 2 {
		if err := blob.AbortUpload(ctx, key, uploadID); err != nil {
			t.Fatalf("abort attempt %d: %v", attempt+1, err)
		}
	}
	if err := blob.AbortUpload(ctx, key, "an-id-that-never-existed"); err != nil {
		t.Fatalf("abort of an unknown id: %v", err)
	}
}

func TestGetReportsAMissingKey(t *testing.T) {
	blob := testBlob(t)
	_, err := blob.Get(t.Context(), "org/aa/bb/absent")
	if !errors.Is(err, ErrNoSuchObject) {
		t.Fatalf("Get of a missing key returned %v, want ErrNoSuchObject", err)
	}
}

// TestGetFailsBeforeTheFirstRead pins the reason Get stats eagerly: the
// client's GetObject is lazy and would otherwise surface a missing key as a
// read error, indistinguishable from this module's own digest verification
// failing at EOF.
func TestGetFailsBeforeTheFirstRead(t *testing.T) {
	blob := testBlob(t)
	reader, err := blob.Get(t.Context(), "org/aa/bb/absent")
	if err == nil {
		_ = reader.Close()
		t.Fatal("Get returned a reader for a missing key")
	}
	if reader != nil {
		t.Fatal("Get returned both an error and a reader")
	}
}

func TestExistsIsFalseWithoutAnError(t *testing.T) {
	blob := testBlob(t)
	exists, err := blob.Exists(t.Context(), "org/aa/bb/absent")
	if err != nil {
		t.Fatalf("Exists of a missing key errored: %v", err)
	}
	if exists {
		t.Fatal("Exists reports a key that was never written")
	}
}

// --- Versions ---------------------------------------------------------

// TestListVersionsSeesEveryVersionAndDeleteMarkers is the measurement
// behind the adapter's refusal to offer a key-level delete: on a versioned
// bucket that call reclaims nothing and leaves a marker on top of the bytes.
func TestListVersionsSeesEveryVersionAndDeleteMarkers(t *testing.T) {
	blob := testBlob(t)
	ctx := t.Context()
	key := "org/aa/bb/twice"

	first := put(t, blob, key, []byte("first"))
	second := put(t, blob, key, []byte("second"))
	if first == second {
		t.Fatal("two writes to one key produced one version id")
	}

	// The raw client on purpose: the adapter has no key-level delete, and
	// this is the state it refuses to create.
	if err := blob.core.RemoveObject(ctx, blob.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		t.Fatalf("write a delete marker: %v", err)
	}

	versions, err := blob.ListVersions(ctx, "org/")
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	var markers int
	seen := map[string]bool{}
	for _, version := range versions {
		seen[version.VersionID] = true
		if version.IsDeleteMarker {
			markers++
		}
	}
	if !seen[first] || !seen[second] {
		t.Fatalf("ListVersions returned %v, missing one of the two stored versions", versions)
	}
	if markers != 1 {
		t.Fatalf("ListVersions reported %d delete markers, want 1: %v", markers, versions)
	}

	// The bytes are still there — which is the whole point. A sweep that
	// used a key-level delete would have reclaimed nothing.
	if len(versions) != 3 {
		t.Fatalf("ListVersions returned %d entries, want 2 versions and 1 marker: %v", len(versions), versions)
	}
}

// TestListVersionsSeesTheNullVersion covers objects written before
// versioning was enabled. They carry the literal version id "null", and a
// sweep that skipped them would leave storage it believed it had reclaimed.
func TestListVersionsSeesTheNullVersion(t *testing.T) {
	cfg := testConfig(t)
	blob := rawTestBlob(t, cfg)
	ctx := t.Context()

	// Created WITHOUT versioning, which is what leaves a null version.
	if err := blob.core.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{}); err != nil {
		t.Fatalf("create unversioned bucket: %v", err)
	}
	key := "org/aa/bb/predates-versioning"
	// Written with the raw client. PutStaged refuses a write that produces
	// no version id, so staging this fixture through the adapter would be
	// asserting the opposite of what the module guarantees — and the state
	// under test is one that PREDATES the adapter, which the sweep has to
	// reclaim whoever wrote it.
	if _, err := blob.core.Client.PutObject(ctx, cfg.Bucket, key,
		strings.NewReader("before"), 6, minio.PutObjectOptions{}); err != nil {
		t.Fatalf("write before versioning: %v", err)
	}
	if err := blob.EnsureBucket(ctx); err != nil {
		t.Fatalf("EnsureBucket: %v", err)
	}

	versions, err := blob.ListVersions(ctx, "org/")
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("ListVersions returned %d entries, want the one pre-versioning object: %v", len(versions), versions)
	}
	if versions[0].VersionID != "null" {
		t.Fatalf("pre-versioning object carries version id %q, want \"null\"", versions[0].VersionID)
	}
	// And it must be removable by name, or the sweep cannot reclaim it.
	if err = blob.DeleteVersion(ctx, versions[0].Key, versions[0].VersionID); err != nil {
		t.Fatalf("DeleteVersion of the null version: %v", err)
	}
	remaining, err := blob.ListVersions(ctx, "org/")
	if err != nil {
		t.Fatalf("ListVersions after delete: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("the null version survived deletion: %v", remaining)
	}
}

// TestDeleteVersionRemovesOnlyTheNamedVersion is the sweep's fence: a
// delete condemning version n must not touch the version a writer created
// afterwards, because that delete may arrive after the lock is gone.
func TestDeleteVersionRemovesOnlyTheNamedVersion(t *testing.T) {
	blob := testBlob(t)
	ctx := t.Context()
	key := "org/aa/bb/reused"

	condemned := put(t, blob, key, []byte("condemned"))
	survivor := put(t, blob, key, []byte("survivor"))

	if err := blob.DeleteVersion(ctx, key, condemned); err != nil {
		t.Fatalf("DeleteVersion: %v", err)
	}

	versions, err := blob.ListVersions(ctx, "org/")
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions) != 1 || versions[0].VersionID != survivor {
		t.Fatalf("after deleting %s the bucket holds %v, want only %s", condemned, versions, survivor)
	}
	if got := readAll(t, blob, key); !bytes.Equal(got, []byte("survivor")) {
		t.Fatalf("current version reads %q, want \"survivor\"", got)
	}
}

// --- Incomplete multipart uploads -------------------------------------

// TestIncompleteUploadsAreInvisibleToVersions establishes the third
// storage state. Neither of the other two vocabularies can see it: the
// parts are not a version, so nothing that enumerates versions would ever
// discover the storage they occupy.
func TestIncompleteUploadsAreInvisibleToVersions(t *testing.T) {
	blob := testBlob(t)
	ctx := t.Context()
	key := "staging/org/died-mid-upload"

	uploadID := startAbandonedUpload(t, blob, key)

	versions, err := blob.ListVersions(ctx, "staging/")
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions) != 0 {
		t.Fatalf("version listing reported an incomplete upload: %v", versions)
	}
	exists, err := blob.Exists(ctx, key)
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if exists {
		t.Fatal("Exists reports an object for a key that only has parts")
	}

	uploads, err := blob.ListUploadsUnder(ctx, "staging/")
	if err != nil {
		t.Fatalf("ListUploadsUnder: %v", err)
	}
	if len(uploads) != 1 || uploads[0].UploadID != uploadID || uploads[0].Key != key {
		t.Fatalf("ListUploadsUnder returned %v, want %s on %s", uploads, uploadID, key)
	}

	if err = blob.AbortUpload(ctx, key, uploadID); err != nil {
		t.Fatalf("AbortUpload: %v", err)
	}
	after, err := blob.ListUploadsUnder(ctx, "staging/")
	if err != nil {
		t.Fatalf("ListUploadsUnder after abort: %v", err)
	}
	if len(after) != 0 {
		t.Fatalf("the aborted upload survived: %v", after)
	}
}

// TestAbortUploadSparesOtherUploadsOnTheSameKey is the fence that made the
// adapter reject the client's key-scoped abort. Digest keys are reused, so
// an abort issued by a sweep whose lock has since been released must not
// kill a newer writer's promotion of the same digest.
func TestAbortUploadSparesOtherUploadsOnTheSameKey(t *testing.T) {
	blob := testBlob(t)
	ctx := t.Context()
	key := "org/aa/bb/contended"

	condemned := startAbandonedUpload(t, blob, key)
	newer := startAbandonedUpload(t, blob, key)
	if condemned == newer {
		t.Fatal("two uploads on one key share an id; the fence would be meaningless")
	}

	if err := blob.AbortUpload(ctx, key, condemned); err != nil {
		t.Fatalf("AbortUpload: %v", err)
	}

	uploads, err := blob.ListUploadsForKey(ctx, key)
	if err != nil {
		t.Fatalf("ListUploadsForKey: %v", err)
	}
	if len(uploads) != 1 || uploads[0].UploadID != newer {
		t.Fatalf("after aborting %s the key holds %v, want only %s", condemned, uploadIDs(uploads), newer)
	}
}

// TestListUploadsUnderFindsWhatTheServerPrefixCannot is the regression test
// for the measurement recorded beside the implementation: this server's
// multipart listing takes an EXACT key, so asking it for a prefix returns
// nothing at all and reports no error. Prefix matching therefore happens in
// the adapter, over the bucket-wide listing.
//
// Without the client-side filter a sweep would be told an organization has
// no incomplete uploads and would reclaim none of them, silently.
func TestListUploadsUnderFindsWhatTheServerPrefixCannot(t *testing.T) {
	blob := testBlob(t)
	ctx := t.Context()

	wanted := map[string]bool{}
	for _, key := range []string{"staging/org/one", "staging/org/two"} {
		wanted[startAbandonedUpload(t, blob, key)] = true
	}
	// A different prefix, which must not be returned.
	elsewhere := startAbandonedUpload(t, blob, "other-org/aa/bb/digest")

	// The server behaviour this works around, asserted rather than
	// described. If a pin bump makes this pass a prefix through, revisit
	// the note in blob.go: the client-side filter may become belt and
	// braces rather than the only thing that works.
	raw, err := blob.core.ListMultipartUploads(ctx, blob.bucket, "staging/", "", "", "", 1000)
	if err != nil {
		t.Fatalf("raw ListMultipartUploads: %v", err)
	}
	if len(raw.Uploads) != 0 {
		t.Fatalf("the pinned server now answers a prefixed multipart listing with %d uploads; "+
			"the measurement recorded in blob.go is stale", len(raw.Uploads))
	}

	uploads, err := blob.ListUploadsUnder(ctx, "staging/")
	if err != nil {
		t.Fatalf("ListUploadsUnder: %v", err)
	}
	if len(uploads) != len(wanted) {
		t.Fatalf("ListUploadsUnder(staging/) returned %v, want the %d staging uploads",
			uploadIDs(uploads), len(wanted))
	}
	for _, upload := range uploads {
		if !wanted[upload.UploadID] {
			t.Fatalf("ListUploadsUnder(staging/) returned %s on %s, outside the prefix",
				upload.UploadID, upload.Key)
		}
	}

	all, err := blob.ListUploadsUnder(ctx, "")
	if err != nil {
		t.Fatalf("ListUploadsUnder(\"\"): %v", err)
	}
	if len(all) != len(wanted)+1 {
		t.Fatalf("the empty prefix returned %d uploads, want every one in the bucket", len(all))
	}
	if !slices.ContainsFunc(all, func(u Upload) bool { return u.UploadID == elsewhere }) {
		t.Fatalf("the empty prefix omitted %s", elsewhere)
	}
}

// TestTheServerNeverTruncatesTheUploadListing pins the second measurement
// recorded beside the implementation: this server ignores `max-uploads` and
// answers with everything it has, never setting IsTruncated.
//
// That makes the adapter's paging unreachable HERE, which is why the marker
// arithmetic is tested against canned responses instead. Asserting the
// server's behaviour is what keeps those unit tests honest: if a pin bump
// starts truncating, this fails and says the real path is now exercisable.
func TestTheServerNeverTruncatesTheUploadListing(t *testing.T) {
	blob := testBlob(t)
	const key = "org/aa/bb/many"
	for range 3 {
		startAbandonedUpload(t, blob, key)
	}

	result, err := blob.core.ListMultipartUploads(t.Context(), blob.bucket, "", "", "", "", 1)
	if err != nil {
		t.Fatalf("raw ListMultipartUploads: %v", err)
	}
	if len(result.Uploads) != 3 || result.IsTruncated {
		t.Fatalf("asking for 1 upload returned %d with truncated=%v; the pinned server now honours "+
			"max-uploads and the measurement in blob.go is stale",
			len(result.Uploads), result.IsTruncated)
	}
}

// --- Transport integrity ----------------------------------------------

// TestPutStagedRejectsCorruptionInTransit proves the transport claim on the
// path this module actually ships, and identifies which mechanism enforces
// it — which is not the one the design originally named.
//
// Bytes are corrupted at the TRANSPORT, after the client has read, hashed
// and signed them. Corrupting the source instead would prove nothing: the
// client would hash the corruption and the server would accept it happily,
// which is exactly why content correctness rests on the local hash and this
// test measures only the increment the wire mechanisms add.
//
// Measured against the pinned image, with a single byte of the payload
// flipped and every length left intact:
//
//	signed chunks + checksum (SHIPPED) -> refused, SignatureDoesNotMatch
//	unsigned chunks + checksum         -> refused, XAmzContentChecksumMismatch
//	unsigned chunks, no checksum       -> ACCEPTED, corruption stored
//
// So on the shipped configuration the per-chunk signature refuses the body
// before the checksum is ever consulted, and the checksum is the mechanism
// that would catch it if the payload were ever unsigned. Both are computed
// by the client from the same buffer, so neither says anything about
// content — only the local hash does.
//
// The third row is the control. Without it the first two would be
// unfalsifiable: a server that ignored both would fail no assertion here,
// because nothing else in the suite ever sends bytes it did not mean.
func TestPutStagedRejectsCorruptionInTransit(t *testing.T) {
	cfg := testConfig(t)
	honest := rawTestBlob(t, cfg)
	if err := honest.EnsureBucket(t.Context()); err != nil {
		t.Fatalf("EnsureBucket: %v", err)
	}
	body := []byte("the bytes the client hashed")

	// PutStaged's own options, restated so the shipped row is the shipped
	// options and the other two are one deliberate change away from it.
	shipped := minio.PutObjectOptions{Checksum: minio.ChecksumSHA256}

	for _, testCase := range []struct {
		name    string
		options minio.PutObjectOptions
		refusal string
	}{
		{"shipped", shipped, "SignatureDoesNotMatch"},
		{"unsigned payload", minio.PutObjectOptions{
			Checksum:             minio.ChecksumSHA256,
			DisableContentSha256: true,
		}, "XAmzContentChecksumMismatch"},
		{"neither mechanism", minio.PutObjectOptions{DisableContentSha256: true}, ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			corrupting, err := newBlob(cfg, &corruptingTransport{needle: body})
			if err != nil {
				t.Fatalf("newBlob: %v", err)
			}
			key := "staging/org/corrupted-" + testCase.name

			_, err = corrupting.core.Client.PutObject(t.Context(), cfg.Bucket, key,
				bytes.NewReader(body), int64(len(body)), testCase.options)

			if testCase.refusal == "" {
				// The control: with nothing guarding the wire the server
				// stores what it received, corruption and all.
				if err != nil {
					t.Fatalf("expected the unguarded upload to be accepted, got %v", err)
				}
				stored := readAll(t, honest, key)
				if bytes.Equal(stored, body) {
					t.Fatal("the corrupting transport did not corrupt anything; " +
						"the other cases prove nothing")
				}
				return
			}

			if err == nil {
				t.Fatalf("the server accepted %d bytes that were altered on the wire", len(body))
			}
			// Named explicitly: any error would satisfy a weaker assertion,
			// including the malformed-framing error an incorrectly built
			// corruption produces, which says nothing about integrity.
			if code := minio.ToErrorResponse(err).Code; code != testCase.refusal {
				t.Fatalf("upload refused with %q (%v), want %s", code, err, testCase.refusal)
			}
			exists, err := honest.Exists(t.Context(), key)
			if err != nil {
				t.Fatalf("Exists: %v", err)
			}
			if exists {
				t.Fatal("a refused upload left an object at the staging key")
			}
		})
	}
}

// TestPutStagedRefusalSurfacesThroughTheAdapter is the shipped path end to
// end: the case above drives the client directly so it can vary the
// options, and this one proves PutStaged itself refuses and wraps.
func TestPutStagedRefusalSurfacesThroughTheAdapter(t *testing.T) {
	cfg := testConfig(t)
	honest := rawTestBlob(t, cfg)
	if err := honest.EnsureBucket(t.Context()); err != nil {
		t.Fatalf("EnsureBucket: %v", err)
	}
	body := []byte("the bytes the client hashed")
	key := "staging/org/corrupted"

	corrupting, err := newBlob(cfg, &corruptingTransport{needle: body})
	if err != nil {
		t.Fatalf("newBlob: %v", err)
	}

	version, err := corrupting.PutStaged(t.Context(), key, int64(len(body)), bytes.NewReader(body))
	if err == nil {
		t.Fatalf("PutStaged accepted corrupted bytes, returning version %q", version)
	}
	if !strings.Contains(err.Error(), key) {
		t.Fatalf("the failure does not name the object it was writing: %v", err)
	}
	exists, err := honest.Exists(t.Context(), key)
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if exists {
		t.Fatal("a refused upload left an object at the staging key")
	}
}

// corruptingTransport flips one byte of the object's DATA on the wire,
// after the client has hashed and signed it.
//
// It locates the payload rather than altering the end of the request,
// because enabling trailing headers puts the body in aws-chunked framing
// with the checksum in a trailer: corrupting the tail breaks the framing
// and the server answers with a malformed-encoding error, which would prove
// nothing about checksum enforcement. Flipping a byte inside the payload
// leaves every length and both the chunk framing intact, so a rejection can
// only come from content verification.
type corruptingTransport struct {
	needle []byte
}

func (c *corruptingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodPut || req.Body == nil {
		return http.DefaultTransport.RoundTrip(req)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, fmt.Errorf("read request body: %w", err)
	}
	if err := req.Body.Close(); err != nil {
		return nil, fmt.Errorf("close request body: %w", err)
	}
	at := bytes.Index(body, c.needle)
	if at < 0 {
		return nil, fmt.Errorf("payload not found in the %d-byte request body; the client's "+
			"encoding changed and this test is no longer corrupting the data", len(body))
	}
	body[at] ^= 0xff
	req.Body = io.NopCloser(bytes.NewReader(body))
	return http.DefaultTransport.RoundTrip(req)
}
