//go:build integration

package postgres_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"orchestrator/internal/dataplane/objects"
	"orchestrator/internal/dataplane/planetest"
)

// disposableBlob is the shared helper under this suite's label: its own
// bucket, created and versioned the way `up` creates the real one, and
// removed afterwards. The config comes back with the adapter because some
// states these tests need cannot be produced through it.
func disposableBlob(t *testing.T) (*objects.Blob, objects.Config) {
	t.Helper()
	return planetest.Blob(t, "seam")
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

// digestOf is what a caller computes before handing bytes to the seam.
func digestOf(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
