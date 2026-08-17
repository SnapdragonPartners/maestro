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

	requireSafeBucket(t, adapter, bucket)

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

// policyStabilization is how long a bucket must have been settled before its
// soft-delete configuration can be believed.
//
// Google documents that disabling soft delete takes EFFECT up to 30 seconds
// after the change is accepted, and the attribute read reports the configured
// value immediately. So there is a window in which the bucket says retention
// is zero and deletions are still being retained — during which this suite
// would pass while every delete kept billing, which is precisely the outcome
// the guard exists to prevent. Doubled for margin, since the documented figure
// is a minimum.
const policyStabilization = 60 * time.Second

// requireSafeBucket refuses to run against a bucket whose policies would make
// these tests lie, and it runs BEFORE the first write.
//
// The checks guard a green-but-wrong outcome rather than a crash:
//
//   - Without VERSIONING, an overwrite discards the previous generation
//     instead of keeping it, so the listing tests have nothing to observe and
//     the no-delete-marker comparison cannot be made at all. (Versioning is
//     not what makes generation-specific fencing possible — GCS assigns a
//     generation to every object either way. What versioning changes is
//     whether noncurrent generations are PRESERVED.)
//   - With SOFT DELETE ON, every deletion test below still PASSES while
//     reclaiming nothing: the generations leave the listing exactly as
//     asserted, and the bytes are retained and billed for the retention
//     period. Cleanup would appear to work and would not. That is the defect
//     recorded on #286, so a suite that could be run against such a bucket
//     would be demonstrating the failure while reporting success.
//   - CONFIGURED IS NOT EFFECTIVE. A bucket whose policy was disabled moments
//     ago reports retention zero and can still retain what it deletes.
//
// Reading bucket attributes needs a permission the adapter itself deliberately
// does not exercise, which is why this lives in the test and not in NewGCS.
func requireSafeBucket(t *testing.T, g *GCS, bucket string) {
	t.Helper()
	attrs, err := g.client.Bucket(bucket).Attrs(context.Background())
	if err != nil {
		t.Fatalf("read attributes of %s: %v", bucket, err)
	}
	if !attrs.VersioningEnabled {
		t.Fatalf("bucket %s is not versioned, so an overwrite replaces the previous generation "+
			"rather than keeping it and the listing tests have nothing to observe", bucket)
	}
	// A nil policy means soft delete is off. A non-nil one with a zero
	// retention means it was explicitly disabled, which is how the API
	// reports the disabled state after a PATCH.
	if attrs.SoftDeletePolicy != nil && attrs.SoftDeletePolicy.RetentionDuration > 0 {
		t.Fatalf("bucket %s retains soft-deleted objects for %s. Every deletion test below would "+
			"still pass while reclaiming nothing, because the generations leave the listing and the "+
			"bytes keep billing until their hard-delete time. Disable soft delete on this bucket "+
			"(retention 0) before running: see #286",
			bucket, attrs.SoftDeletePolicy.RetentionDuration)
	}
	// The configuration above is only a claim about intent until it has
	// settled. Refusing here costs a minute; not refusing means a suite that
	// reports success while deletes retain bytes, which is indistinguishable
	// from a correct run by anything the suite can see afterwards.
	if settled := time.Since(attrs.Updated); settled < policyStabilization {
		t.Fatalf("bucket %s was modified %s ago and soft-delete changes take up to 30s to take "+
			"effect. Its attributes already report the new policy, so this preflight cannot tell a "+
			"settled bucket from one still retaining deletions. Wait %s and re-run: see #286",
			bucket, settled.Round(time.Second), (policyStabilization - settled).Round(time.Second))
	}
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

	// The deletion has to be UNQUALIFIED to test anything. An exact-generation
	// delete removes that generation on both providers and proves nothing
	// about markers — the listing would drop from two to one either way.
	//
	// The divergence is in the KEY-LEVEL delete: on a versioned S3 bucket that
	// call adds a delete marker, which is why blob.go refuses to offer one at
	// all and says so in its own comment. Here the same call must leave the
	// count unchanged, turning the live generation noncurrent while it keeps
	// its bytes.
	//
	// This adapter deliberately exposes no way to issue it, so the test
	// reaches through to the underlying handle — the same reason
	// blob_integration_test.go is an internal test package: the states worth
	// checking are the ones the adapter exists to prevent.
	live := before[len(before)-1]
	if delErr := g.client.Bucket(g.bucket).Object(live.Key).Delete(ctx); delErr != nil {
		t.Fatalf("unqualified delete of %s: %v", live.Key, delErr)
	}

	after, err := g.ListVersions(ctx, prefix)
	if err != nil {
		t.Fatalf("list after: %v", err)
	}
	if len(after) != 2 {
		t.Fatalf("an unqualified delete changed the listing from 2 entries to %d. On GCS it must "+
			"leave the count alone — the live generation becomes noncurrent and keeps its bytes. A "+
			"count of 3 would mean a delete marker was added, which is S3 behaviour and would make "+
			"the sweep account for storage that does not exist", len(after))
	}
	for _, v := range after {
		if v.IsDeleteMarker {
			t.Fatalf("version %s is reported as a delete marker after an unqualified delete", v.VersionID)
		}
		if v.Size == 0 {
			t.Fatalf("version %s reports zero bytes after an unqualified delete; the bytes are "+
				"retained on GCS, and a zero size would hide reclaimable storage from the sweep",
				v.VersionID)
		}
	}

	// The generation that was live must still be nameable, since that is how
	// the sweep will actually reclaim it.
	if delErr := g.DeleteVersion(ctx, live.Key, live.VersionID); delErr != nil {
		t.Fatalf("the formerly-live generation could not be deleted by name: %v", delErr)
	}
	remaining, err := g.ListVersions(ctx, prefix)
	if err != nil {
		t.Fatalf("list after generation delete: %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("naming the formerly-live generation left %d entries, want 1", len(remaining))
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
// The upload MUST cross a chunk boundary before it fails, or the test proves
// nothing. The pinned client buffers before sending, so an interrupted write
// smaller than one chunk never opens a resumable session at all — the bytes
// die in the client, no session is created, and "nothing was listed" is then
// a statement about a request that was never made. An earlier version of this
// test failed after 512 KiB against the default 16 MiB buffer and was exactly
// that vacuous.
//
// The failure is injected only after the SERVICE has acknowledged a chunk,
// which the writer's ProgressFunc reports — it fires after each completed
// upload request, so it describes bytes GCS took rather than bytes the client
// consumed. An earlier version counted the source reader instead, which shows
// only that the client accepted input and would look identical while it
// buffered everything and sent nothing.
//
// The chunk size is lowered rather than the body enlarged, so a session is
// genuinely opened and fed without uploading tens of megabytes per run.
func TestGCSIncompleteWritesAreUnenumerable(t *testing.T) {
	g, prefix := newTestGCS(t)
	ctx := context.Background()
	key := prefix + "abandoned.bin"

	const chunk = 256 * 1024
	g.chunkSize = chunk

	acknowledged := make(chan int64, 8)
	g.onUploadProgress = func(uploaded int64) {
		// Must not block: the client documents that ProgressFunc should
		// return quickly, and it runs on the upload path.
		select {
		case acknowledged <- uploaded:
		default:
		}
	}

	interrupted := errors.New("reader failed once a chunk was acknowledged")
	body := io.MultiReader(
		bytes.NewReader(make([]byte, chunk)),
		// Blocks until GCS confirms it has taken a chunk, then fails. If no
		// acknowledgement ever arrives — which is what happens if the chunk
		// size override stops being applied and the client buffers 16 MiB —
		// this times out and the test reports that rather than asserting the
		// absence of a request that was never made.
		&gatedFailingReader{gate: acknowledged, err: interrupted, wait: 90 * time.Second},
	)

	_, err := g.PutStaged(ctx, key, 8*chunk, body)
	if err == nil {
		t.Fatal("an interrupted upload was reported as successful")
	}
	if errors.Is(err, errNoChunkAcknowledged) {
		t.Fatalf("no chunk was acknowledged by the service before the write was abandoned, so this "+
			"test cannot establish that an incomplete write existed at all: %v", err)
	}
	if !errors.Is(err, interrupted) {
		t.Fatalf("the write failed for an unexpected reason: %v", err)
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

// errNoChunkAcknowledged marks the timeout case, so the test can tell "the
// service never took a chunk" apart from "the write failed as intended".
// Without the distinction a vacuous run and a real one look the same.
var errNoChunkAcknowledged = errors.New("no upload request was acknowledged")

// gatedFailingReader fails the write, but only once the service has
// acknowledged an upload request — turning the interrupted-upload test from an
// assertion about client behaviour into one about server state.
type gatedFailingReader struct {
	gate <-chan int64
	err  error
	wait time.Duration
}

func (g *gatedFailingReader) Read([]byte) (int, error) {
	select {
	case <-g.gate:
		return 0, g.err
	case <-time.After(g.wait):
		return 0, errNoChunkAcknowledged
	}
}

// TestGCSPutStagedRejectsASizeMismatch covers the check that exists because
// GCS will not perform it.
//
// Writer.Size is inherited from ObjectAttrs, where it is documented read-only:
// assigning it compiles, passes the client's own attribute validation, and is
// then ignored. So a body shorter or longer than the caller declared would be
// stored at whatever length arrived. blob.go's PutObject hands the size to the
// S3 client, which does enforce it — meaning without this check the two
// adapters would answer the same seam call differently, one refusing a
// truncated body and the other accepting it.
func TestGCSPutStagedRejectsASizeMismatch(t *testing.T) {
	g, prefix := newTestGCS(t)
	ctx := context.Background()

	for name, tc := range map[string]struct {
		declared int64
		content  string
	}{
		"stream shorter than declared": {declared: 64, content: "only a few bytes"},
		"stream longer than declared":  {declared: 4, content: "considerably more than four"},
	} {
		t.Run(name, func(t *testing.T) {
			key := prefix + "mismatch-" + name + ".txt"
			_, err := g.PutStaged(ctx, key, tc.declared, bytes.NewReader([]byte(tc.content)))
			if err == nil {
				t.Fatalf("a %d-byte body declared as %d bytes was accepted", len(tc.content), tc.declared)
			}
			// And it must leave nothing behind: a rejected write that stored
			// the object anyway would be worse than accepting it, because the
			// caller is told it failed.
			exists, existsErr := g.Exists(ctx, key)
			if existsErr != nil {
				t.Fatalf("exists: %v", existsErr)
			}
			if exists {
				t.Fatal("the rejected write left a live object at a key the caller was told failed")
			}
		})
	}
}
