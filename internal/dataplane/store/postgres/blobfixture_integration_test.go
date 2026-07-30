//go:build integration

package postgres_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"os"
	"strconv"
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"orchestrator/internal/dataplane/objects"
	"orchestrator/internal/dataplane/paths"
	"orchestrator/internal/dataplane/secret"
)

// disposableBlob gives a test its own bucket, created and versioned the way
// `up` creates the real one, and removed afterwards.
//
// One bucket per test rather than a shared one, for the reason the
// migrations suite uses one database per test: these tests write objects at
// content-addressed keys, and two tests storing the same bytes would share
// a key. Cross-test interference on a content-addressed store is not a
// hypothetical -- it is the normal case.
//
// The endpoint is assembled here rather than taken from stack.Config
// because this package's tests already build their DSN the same way, and
// because a test helper is not a good enough reason to import the
// launcher.
func disposableBlob(t *testing.T) (*objects.Blob, objects.Config) {
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

	suffix := make([]byte, 8)
	if _, randErr := rand.Read(suffix); randErr != nil {
		t.Fatalf("generate bucket suffix: %v", randErr)
	}
	cfg := objects.Config{
		Endpoint:  net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		Bucket:    "maestro-seam-it-" + hex.EncodeToString(suffix),
		AccessKey: accessKey,
		SecretKey: secretKey,
	}

	blob, err := objects.New(cfg)
	if err != nil {
		t.Fatalf("build object adapter: %v", err)
	}
	if err := blob.EnsureBucket(context.Background()); err != nil {
		t.Skipf("object store unavailable (run `make dataplane-up`): %v", err)
	}
	t.Cleanup(func() { removeTestBucket(t, cfg) })
	// The config comes back with the adapter because some states a test
	// needs cannot be produced through it: an incomplete multipart upload
	// is precisely what the adapter refuses to leave behind, and cleanup
	// exists to collect it.
	return blob, cfg
}

// startAbandonedUpload begins a multipart upload and never completes it,
// using the raw client, and returns its id. The adapter offers no way to do
// this, which is why the state has to be built here rather than through it.
//
// The id matters to the sweep's fence: a claim may abort only the upload ids
// it recorded, so a test proving that needs to name the ones it created.
func startAbandonedUpload(t *testing.T, cfg objects.Config, key string) string {
	t.Helper()
	ctx := context.Background()

	core, err := minio.NewCore(cfg.Endpoint, &minio.Options{
		Creds: credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
	})
	if err != nil {
		t.Fatalf("build raw client: %v", err)
	}
	uploadID, err := core.NewMultipartUpload(ctx, cfg.Bucket, key, minio.PutObjectOptions{})
	if err != nil {
		t.Fatalf("start upload on %s: %v", key, err)
	}
	// One part, far below the five-mebibyte minimum, which is legal because
	// the upload is never completed: the floor is enforced at completion.
	part := []byte("abandoned part")
	if _, err := core.PutObjectPart(ctx, cfg.Bucket, key, uploadID, 1,
		bytes.NewReader(part), int64(len(part)), minio.PutObjectPartOptions{}); err != nil {
		t.Fatalf("upload part on %s: %v", key, err)
	}
	return uploadID
}

// removeTestBucket empties and drops a disposable bucket. A versioned
// bucket refuses removal while anything remains, and "anything" includes
// delete markers and incomplete uploads.
func removeTestBucket(t *testing.T, cfg objects.Config) {
	t.Helper()
	ctx := context.Background()

	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds: credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
	})
	if err != nil {
		t.Errorf("cleanup: build client: %v", err)
		return
	}
	exists, err := client.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		t.Errorf("cleanup: check bucket %s: %v", cfg.Bucket, err)
		return
	}
	if !exists {
		// A test may have removed it deliberately, to make every remote
		// call fail.
		return
	}
	if err := client.RemoveBucketWithOptions(ctx, cfg.Bucket,
		minio.RemoveBucketOptions{ForceDelete: true}); err != nil {
		t.Errorf("cleanup: remove bucket %s: %v", cfg.Bucket, err)
	}
}

// digestOf is what a caller computes before handing bytes to the seam.
func digestOf(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
