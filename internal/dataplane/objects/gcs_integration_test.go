//go:build gcs

// These tests run against a REAL Google Cloud Storage bucket, never an
// emulator. Everything the GCS adapter claims is a claim about what the
// service actually does — that deleting a live object adds no listing entry,
// that a generation names one immutable object, that the service reports its
// own CRC32C — and fake-gcs-server would only replay the belief being tested.
// blob_integration_test.go makes the same argument for MinIO and it applies
// with more force here, because the divergences from S3 are the entire reason
// this adapter exists.
//
// They are behind their own build tag rather than `integration` on purpose.
// The pre-push gate runs `make test-integration`, and requiring cloud
// credentials to push would either block anyone without them or quietly skip
// and look green. Run them deliberately:
//
//	MAESTRO_GCS_TEST_BUCKET=maestro-objects-286 make test-gcs
//
// The bucket must be versioned and have soft delete DISABLED. With soft
// delete on, the deletion tests below still pass while reclaiming nothing —
// which is exactly the failure recorded on #286, and the reason the
// provisioning path has to verify the policy rather than assume it.
//
// This is an INTERNAL test package because some states under test are ones
// the adapter deliberately cannot create.

package objects

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"testing"
	"time"
)

const testBucketEnv = "MAESTRO_GCS_TEST_BUCKET"

// newTestGCS builds an adapter against the configured bucket and returns a
// prefix unique to this run, so concurrent runs cannot see each other's
// objects and a failure leaves evidence under a name that identifies it.
func newTestGCS(t *testing.T) (*GCS, string) {
	t.Helper()
	bucket := os.Getenv(testBucketEnv)
	if bucket == "" {
		t.Skipf("%s is not set; these tests require a real GCS bucket", testBucketEnv)
	}
	ctx := context.Background()
	adapter, err := NewGCS(ctx, GCSConfig{Bucket: bucket})
	if err != nil {
		t.Fatalf("build adapter: %v", err)
	}
	t.Cleanup(func() {
		if err := adapter.Close(); err != nil {
			t.Errorf("close adapter: %v", err)
		}
	})

	nonce := make([]byte, 8)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatalf("generate run nonce: %v", err)
	}
	prefix := fmt.Sprintf("test/%s-%s/", time.Now().UTC().Format("20060102T150405"), hex.EncodeToString(nonce))

	t.Cleanup(func() {
		// Remove every generation this run created. Storage that outlives the
		// test bills, and the sweep these objects belong to does not run here.
		versions, err := adapter.ListVersions(context.Background(), prefix)
		if err != nil {
			t.Errorf("cleanup: list %s: %v", prefix, err)
			return
		}
		for _, v := range versions {
			if err := adapter.DeleteVersion(context.Background(), v.Key, v.VersionID); err != nil {
				t.Errorf("cleanup: delete %s generation %s: %v", v.Key, v.VersionID, err)
			}
		}
	})
	return adapter, prefix
}

func put(t *testing.T, g *GCS, key, content string) string {
	t.Helper()
	version, err := g.PutStaged(context.Background(), key, int64(len(content)), bytes.NewReader([]byte(content)))
	if err != nil {
		t.Fatalf("put %s: %v", key, err)
	}
	return version
}

// TestGCSPutStagedReturnsAFenceableGeneration covers the write path's whole
// contract: the bytes arrive intact and the returned id can name them later.
func TestGCSPutStagedReturnsAFenceableGeneration(t *testing.T) {
	g, prefix := newTestGCS(t)
	ctx := context.Background()
	key := prefix + "staged.txt"

	version := put(t, g, key, "the bytes that were sent")

	generation, err := strconv.ParseInt(version, 10, 64)
	if err != nil || generation <= 0 {
		t.Fatalf("PutStaged returned %q, which cannot name a generation for a later delete", version)
	}

	reader, err := g.Get(ctx, key)
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}
	defer func() { _ = reader.Close() }()
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read %s: %v", key, err)
	}
	if string(got) != "the bytes that were sent" {
		t.Fatalf("round-tripped %q, want %q", got, "the bytes that were sent")
	}
}

// TestGCSDeletingLiveObjectAddsNoListingEntry is the measurement behind the
// IsDeleteMarker documentation, and the sharpest divergence from S3.
//
// On a versioned S3 bucket, deleting the live object WRITES A DELETE MARKER —
// a new listing entry blob.go must report. Here the count must not change:
// the generation that was live simply becomes noncurrent. A future client
// change that started synthesising marker entries would break the sweep's
// accounting, and this is what would catch it.
func TestGCSDeletingLiveObjectAddsNoListingEntry(t *testing.T) {
	g, prefix := newTestGCS(t)
	ctx := context.Background()
	key := prefix + "overwritten.txt"

	put(t, g, key, "one")
	put(t, g, key, "two")

	before, err := g.ListVersions(ctx, prefix)
	if err != nil {
		t.Fatalf("list before: %v", err)
	}
	if len(before) != 2 {
		t.Fatalf("two writes produced %d versions, want 2", len(before))
	}
	for _, v := range before {
		if v.IsDeleteMarker {
			t.Fatalf("version %s is reported as a delete marker; GCS has no such entry, so the "+
				"sweep would be told storage exists where there are no bytes", v.VersionID)
		}
		if v.Size == 0 {
			t.Fatalf("version %s reports zero bytes; a noncurrent GCS generation retains its "+
				"content, and a zero size would make the sweep think it had nothing to reclaim",
				v.VersionID)
		}
		if v.LastModified.IsZero() {
			t.Fatalf("version %s has no timestamp; the sweep's grace period has nothing else to "+
				"date an unreferenced object by", v.VersionID)
		}
	}

	// Delete the LIVE object by naming its generation — the only delete this
	// adapter offers. On S3 the equivalent key-level call would add a marker.
	live := before[len(before)-1]
	if err := g.DeleteVersion(ctx, live.Key, live.VersionID); err != nil {
		t.Fatalf("delete live generation: %v", err)
	}

	after, err := g.ListVersions(ctx, prefix)
	if err != nil {
		t.Fatalf("list after: %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("after deleting one of two generations the listing holds %d, want 1 — no delete "+
			"marker should have been added", len(after))
	}
	if after[0].VersionID == live.VersionID {
		t.Fatal("the deleted generation is still listed")
	}
}

// TestGCSDeleteVersionIsRepeatable covers the tolerance a deletion claim
// depends on: a crash after the remote delete and before the row clears
// leaves the reconciler re-issuing a delete for something already gone.
// Reporting that as a failure would strand the claim permanently.
func TestGCSDeleteVersionIsRepeatable(t *testing.T) {
	g, prefix := newTestGCS(t)
	ctx := context.Background()
	key := prefix + "twice.txt"

	version := put(t, g, key, "delete me")
	if err := g.DeleteVersion(ctx, key, version); err != nil {
		t.Fatalf("first delete: %v", err)
	}
	if err := g.DeleteVersion(ctx, key, version); err != nil {
		t.Fatalf("repeating a version-specific delete must be harmless, got: %v", err)
	}

	exists, err := g.Exists(ctx, key)
	if err != nil {
		t.Fatalf("exists: %v", err)
	}
	if exists {
		t.Fatal("the key still has a live generation after its only one was deleted")
	}
}

// TestGCSPromoteCopiesTheNamedGeneration proves the property the seam's
// Promote contract is written around: the copy takes the generation it was
// given, not whatever is current at the staging key.
func TestGCSPromoteCopiesTheNamedGeneration(t *testing.T) {
	g, prefix := newTestGCS(t)
	ctx := context.Background()
	stagingKey := prefix + "staging.bin"
	digestKey := prefix + "digest.bin"

	staged := put(t, g, stagingKey, "the staged bytes")
	// Overwrite the key. A promote that took "whatever is live" would copy
	// this instead, which is the exact defect naming a generation prevents.
	put(t, g, stagingKey, "a concurrent overwrite")

	promoted, err := g.Promote(ctx, stagingKey, staged, digestKey)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if promoted == "" {
		t.Fatal("promote returned an empty generation")
	}

	reader, err := g.Get(ctx, digestKey)
	if err != nil {
		t.Fatalf("get promoted: %v", err)
	}
	defer func() { _ = reader.Close() }()
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read promoted: %v", err)
	}
	if string(got) != "the staged bytes" {
		t.Fatalf("promote copied %q; it took the live generation rather than the named one", got)
	}
}

// TestGCSPromoteRejectsAMissingGeneration covers the lost-lease path: cleanup
// removed the staged generation out from under a promote. It must surface as
// ErrNoSuchObject, which is a routine outcome, rather than an opaque error.
func TestGCSPromoteRejectsAMissingGeneration(t *testing.T) {
	g, prefix := newTestGCS(t)
	ctx := context.Background()
	stagingKey := prefix + "gone.bin"

	staged := put(t, g, stagingKey, "briefly here")
	if err := g.DeleteVersion(ctx, stagingKey, staged); err != nil {
		t.Fatalf("remove the staged generation: %v", err)
	}

	_, err := g.Promote(ctx, stagingKey, staged, prefix+"never.bin")
	if err == nil {
		t.Fatal("promoting a deleted generation succeeded")
	}
	if !errors.Is(err, ErrNoSuchObject) {
		t.Fatalf("a missing staged generation must report ErrNoSuchObject so the caller can treat "+
			"it as a lost lease rather than a fault, got: %v", err)
	}
}

// TestGCSGetReportsMissingKeyImmediately guards the difference from the S3
// client noted on Get: NewReader issues its request eagerly, so a missing key
// is an error here and not a surprise at EOF, where content verification
// failure also surfaces and the two would be indistinguishable.
func TestGCSGetReportsMissingKeyImmediately(t *testing.T) {
	g, prefix := newTestGCS(t)

	_, err := g.Get(context.Background(), prefix+"never-written.txt")
	if !errors.Is(err, ErrNoSuchObject) {
		t.Fatalf("want ErrNoSuchObject for a key that was never written, got: %v", err)
	}
}

// TestGCSIncompleteWritesAreUnenumerable is the measurement the entire
// capability split rests on: this adapter declares provider-reclaimed because
// it genuinely cannot see interrupted writes, not as a convenience.
//
// It asserts the claim from the outside — after abandoning a write, no
// listing surface this adapter offers reports anything — which is the only
// form available, since the point is that there is no API to ask.
func TestGCSIncompleteWritesAreUnenumerable(t *testing.T) {
	g, prefix := newTestGCS(t)
	ctx := context.Background()
	key := prefix + "abandoned.bin"

	// Abandon a write partway. The reader fails after some bytes, so the
	// resumable session has received data and is never finalized.
	interrupted := errors.New("reader failed partway")
	body := io.MultiReader(bytes.NewReader(make([]byte, 512*1024)), failingReader{interrupted})
	_, err := g.PutStaged(ctx, key, 1024*1024, body)
	if err == nil {
		t.Fatal("an interrupted upload was reported as successful")
	}

	versions, err := g.ListVersions(ctx, prefix)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(versions) != 0 {
		t.Fatalf("an unfinalized upload appeared in the version listing as %d entries; if GCS ever "+
			"exposes these, the provider-reclaimed declaration must be revisited", len(versions))
	}
	exists, err := g.Exists(ctx, key)
	if err != nil {
		t.Fatalf("exists: %v", err)
	}
	if exists {
		t.Fatal("an unfinalized upload produced a live object")
	}

	// The capability must match that blindness, and the adapter must not
	// offer a reclaimer it could only answer emptily.
	if got := g.IncompleteWrites(); got != IncompleteWritesProviderReclaimed {
		t.Fatalf("adapter declares %q against a provider whose interrupted writes it cannot "+
			"enumerate", got)
	}
	if _, ok := any(g).(IncompleteWriteReclaimer); ok {
		t.Fatal("adapter implements IncompleteWriteReclaimer but cannot observe what it would " +
			"claim to reclaim")
	}
}

type failingReader struct{ err error }

func (f failingReader) Read([]byte) (int, error) { return 0, f.err }
