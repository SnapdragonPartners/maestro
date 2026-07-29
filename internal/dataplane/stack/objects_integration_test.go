//go:build integration

package stack

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"strconv"
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"orchestrator/internal/dataplane/objects"
	"orchestrator/internal/dataplane/paths"
	"orchestrator/internal/dataplane/secret"
)

// TestEnsureBucketMakesThePlaneAbleToStoreAnObject covers the step `up`
// gained in this item. Nothing had ever created the bucket the config names
// and the bootstrap pointer publishes, so the plane reported itself ready
// and its first write would have failed — which is exactly what a live
// stack that had been up for two days turned out to be doing.
//
// The assertion is therefore the criterion itself: after this step an
// object can be written and read back. Checking that a bucket exists would
// test less and mean less.
//
// It exercises ensureBucket rather than up. Running up from a test would
// take the lifecycle lock and drive Compose against the developer's live
// plane; what is specific to this wiring — deriving the object credentials,
// taking the endpoint from the pointer callers are given, and provisioning
// through the module — is all here. The call site is exercised by
// `dataplane-up` itself, which is the everyday command.
//
// The bucket name is disposable, and that is what keeps the test able to
// fail: against the configured bucket, which by now exists on any machine
// that has run `up`, a provisioning step that did nothing at all would
// still leave every assertion below passing.
func TestEnsureBucketMakesThePlaneAbleToStoreAnObject(t *testing.T) {
	roots, err := paths.Resolve()
	if err != nil {
		t.Skipf("cannot resolve storage roots: %v", err)
	}
	rootKey, err := paths.EnsureKey(roots.Config)
	if err != nil {
		t.Skipf("cannot read the root-of-trust key: %v", err)
	}
	cfg, err := NewConfig(roots)
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	suffix := make([]byte, 8)
	if _, randErr := rand.Read(suffix); randErr != nil {
		t.Fatalf("generate bucket suffix: %v", randErr)
	}
	cfg.Bucket = "maestro-stack-it-" + hex.EncodeToString(suffix)
	t.Cleanup(func() { removeBucket(t, cfg) })

	if err = ensureBucket(t.Context(), cfg, rootKey); err != nil {
		t.Fatalf("ensureBucket: %v", err)
	}
	// Twice: `up` is idempotent, and the second run is the normal case.
	if err = ensureBucket(t.Context(), cfg, rootKey); err != nil {
		t.Fatalf("second ensureBucket: %v", err)
	}

	// Built the way a caller would, from the pointer `up` publishes: a
	// wrong endpoint or a mismatched credential fails here.
	accessKey, err := secret.Derive(rootKey, secret.ContextObjectAccessKey)
	if err != nil {
		t.Fatalf("derive object access key: %v", err)
	}
	secretKey, err := secret.Derive(rootKey, secret.ContextObjectSecretKey)
	if err != nil {
		t.Fatalf("derive object secret key: %v", err)
	}
	blob, err := objects.New(objects.Config{
		Endpoint:  cfg.Bootstrap().Objects.Endpoint,
		Bucket:    cfg.Bucket,
		AccessKey: accessKey,
		SecretKey: secretKey,
	})
	if err != nil {
		t.Fatalf("build client: %v", err)
	}

	key := "staging/stack-provisioning-test/object"
	body := []byte("a plane that is ready can store this")

	version, err := blob.PutStaged(t.Context(), key, int64(len(body)), bytes.NewReader(body))
	if err != nil {
		t.Fatalf("the provisioned plane could not store an object: %v", err)
	}
	// The version id is what every fence in the object sweep names, and it
	// exists only because provisioning enabled versioning.
	if version == "" {
		t.Fatal("the stored object carries no version id; the bucket is not versioned")
	}
	reader, err := blob.Get(t.Context(), key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = reader.Close() }()
	stored, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(stored, body) {
		t.Fatalf("read back %q, want %q", stored, body)
	}
}

// removeBucket tears down the disposable bucket.
//
// It uses the client directly rather than the adapter: emptying a bucket is
// the adapter's vocabulary, but removing one is not, and the module offers
// no bucket deletion on purpose — nothing in the running system should ever
// remove the bucket the plane stores its evidence in.
func removeBucket(t *testing.T, cfg *Config) {
	t.Helper()
	ctx := context.WithoutCancel(t.Context())

	rootKey, err := paths.EnsureKey(cfg.Roots.Config)
	if err != nil {
		t.Errorf("cleanup: read root key: %v", err)
		return
	}
	accessKey, keyErr := secret.Derive(rootKey, secret.ContextObjectAccessKey)
	secretKey, secretErr := secret.Derive(rootKey, secret.ContextObjectSecretKey)
	if keyErr != nil || secretErr != nil {
		t.Errorf("cleanup: derive credentials: %v %v", keyErr, secretErr)
		return
	}
	client, err := minio.New("127.0.0.1:"+strconv.Itoa(cfg.MinIOPort), &minio.Options{
		Creds: credentials.NewStaticV4(accessKey, secretKey, ""),
	})
	if err != nil {
		t.Errorf("cleanup: build client: %v", err)
		return
	}
	exists, err := client.BucketExists(ctx, cfg.Bucket)
	if err != nil || !exists {
		return
	}
	// Versioned: every version has to go by name, delete markers included,
	// or the bucket refuses removal.
	for info := range client.ListObjects(ctx, cfg.Bucket, minio.ListObjectsOptions{
		Recursive: true, WithVersions: true,
	}) {
		if info.Err != nil {
			t.Errorf("cleanup: list %s: %v", cfg.Bucket, info.Err)
			return
		}
		if rmErr := client.RemoveObject(ctx, cfg.Bucket, info.Key,
			minio.RemoveObjectOptions{VersionID: info.VersionID}); rmErr != nil {
			t.Errorf("cleanup: remove %s@%s: %v", info.Key, info.VersionID, rmErr)
		}
	}
	if rmErr := client.RemoveBucket(ctx, cfg.Bucket); rmErr != nil {
		t.Errorf("cleanup: remove bucket %s: %v", cfg.Bucket, rmErr)
	}
}
