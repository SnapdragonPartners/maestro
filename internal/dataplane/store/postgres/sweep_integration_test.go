//go:build integration

package postgres_test

import (
	"bytes"
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/objects"
	"orchestrator/internal/dataplane/store"
	"orchestrator/internal/dataplane/store/postgres"
)

// The object sweep (item 6 design, D6).
//
// The sweep is the one operation here that deletes bytes, and every test is
// about what stops it deleting the wrong ones. Three mechanisms carry that,
// and they are separable on purpose:
//
//   - the advisory lock plus the reference recheck under it is the decision.
//     Establishing "unreferenced" in mutual exclusion with the commit that
//     would make it referenced is what makes the decision sound at all;
//   - the deletion claim is the fence for what the lock cannot reach. The
//     deletes are remote, so a connection can die with one in flight,
//     releasing the lock without cancelling anything;
//   - the grace period is defence in depth and nothing else. Design round 1
//     made age the whole protection and it was correctly rejected.
//
// So the tests aim the clock at the grace period and the barrier at the
// lock, and never let one stand in for the other.

// pastGrace is a clock far enough ahead that no residue is fresh.
//
// Tests that are about the LOCK read this clock, so that a survival they
// assert can only have come from the reference recheck. Design D6 requires
// the grace period to be aged past rather than switched off -- a test that
// disables a guard proves the guard is switchable -- and this is what aging
// past it looks like from the outside.
func pastGrace() time.Time { return time.Now().Add(24 * time.Hour) }

// storeAfterGrace is the seam with its clock aged past the grace period.
func (f *fixture) storeAfterGrace(t *testing.T) *postgres.Store {
	t.Helper()
	built, err := postgres.New(f.pool, testRegistry(t), f.blob, f.rootKey, postgres.WithClock(pastGrace))
	if err != nil {
		t.Fatalf("build a store whose clock is past the grace period: %v", err)
	}
	return built
}

// unreferencedObject writes bytes to their digest key with NO attachment
// row, and returns the digest.
//
// This is a real state and not a contrived one: it is exactly what a writer
// leaves behind when it promotes and then fails before its row commits, and
// what attachment truncation produces when it removes the last row
// referencing a digest. The sweep exists for it.
func (f *fixture) unreferencedObject(t *testing.T, body []byte) string {
	t.Helper()
	digest := digestOf(body)
	key := objectKeyFor(f.organizationID, digest)
	if _, err := f.blob.PutStaged(context.Background(), key,
		int64(len(body)), bytes.NewReader(body)); err != nil {
		t.Fatalf("write an unreferenced object at %s: %v", key, err)
	}
	return digest
}

// barrierTimeout bounds a single external call made while a test is holding a
// forced window open.
//
// Every such call needs a deadline of its own. A test that stalls a writer
// inside its transaction and then makes an unbounded call has arranged for one
// slow response to become a deadlock: the writer cannot proceed, whatever is
// blocked behind its lock cannot proceed, and the goroutine that would release
// the stall is the one waiting. Nothing breaks that cycle except the package
// timeout, which fails the whole suite twenty minutes later and names no cause.
//
// Generous on purpose. It is not a performance assertion — a loaded object store
// legitimately takes seconds — it exists so that "slow" cannot become "never".
const barrierTimeout = 60 * time.Second

// storedVersions counts what the bucket actually holds for a digest,
// including delete markers. Zero means the storage is genuinely reclaimed
// rather than merely hidden behind a marker.
//
// Bounded, because this is called inside forced windows: see barrierTimeout.
func (f *fixture) storedVersions(t *testing.T, digest string) int {
	t.Helper()
	key := objectKeyFor(f.organizationID, digest)
	ctx, cancel := context.WithTimeout(context.Background(), barrierTimeout)
	defer cancel()
	versions, err := f.blob.ListVersions(ctx, key)
	if err != nil {
		t.Fatalf("list versions of %s: %v", key, err)
	}
	return len(versions)
}

// liveClaims counts the deletion claims outstanding for this organization.
func (f *fixture) liveClaims(t *testing.T) int {
	t.Helper()
	var claims int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM deletion_claims WHERE organization_id = $1`,
		f.organizationID).Scan(&claims); err != nil {
		t.Fatalf("count deletion claims: %v", err)
	}
	return claims
}

// TestSweepReclaimsAnObjectTruncationUnreferenced walks the whole
// reclamation path in the order the design puts it: attachment truncation
// removes the row, which makes the object unreachable, and only then can the
// sweep reclaim the bytes.
//
// The two steps cannot be one. Truncation runs under a single REPEATABLE
// READ snapshot and object deletion cannot participate in a snapshot, which
// is why "deleting the row does not delete the object" is a property of this
// design rather than an oversight in it (D6a).
func TestSweepReclaimsAnObjectTruncationUnreferenced(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	body := []byte("evidence whose retention has run out")

	attachment, err := f.store.PutAttachment(ctx, putInput(f.organizationID, body))
	if err != nil {
		t.Fatalf("PutAttachment: %v", err)
	}
	if f.storedVersions(t, attachment.Digest) == 0 {
		t.Fatal("nothing was stored, so this test cannot show anything being reclaimed")
	}

	// The real pass, not a hand-written DELETE: the sweep's whole premise is
	// that its reachable set is exactly the rows truncation removes.
	truncated, err := f.store.TruncateAuditBefore(ctx, f.organizationID, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("TruncateAuditBefore: %v", err)
	}
	if truncated.PerTable[store.TableAttachments].Deleted != 1 {
		t.Fatalf("truncation deleted %d attachment rows, want 1; the sweep would have nothing to reclaim",
			truncated.PerTable[store.TableAttachments].Deleted)
	}

	swept, err := f.storeAfterGrace(t).DeleteUnpinned(ctx, f.organizationID)
	if err != nil {
		t.Fatalf("DeleteUnpinned: %v", err)
	}
	if swept.DigestsReclaimed != 1 || swept.VersionsDeleted != 1 {
		t.Fatalf("sweep reported %+v, want one digest reclaimed and one version deleted", swept)
	}
	if remaining := f.storedVersions(t, attachment.Digest); remaining != 0 {
		t.Fatalf("%d versions survive at the digest key; the storage was not reclaimed", remaining)
	}
	// The claim is gone too. A claim left behind is not merely untidy: it
	// forbids the existing-object shortcut for this digest until it clears.
	if claims := f.liveClaims(t); claims != 0 {
		t.Fatalf("%d deletion claims survive a completed sweep, want 0", claims)
	}
}

// TestSweepLeavesAReferencedObjectAlone is the case that must hold on every
// pass, since it is nearly every object.
func TestSweepLeavesAReferencedObjectAlone(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	attachment, err := f.store.PutAttachment(ctx,
		putInput(f.organizationID, []byte("evidence something still references")))
	if err != nil {
		t.Fatalf("PutAttachment: %v", err)
	}

	swept, err := f.storeAfterGrace(t).DeleteUnpinned(ctx, f.organizationID)
	if err != nil {
		t.Fatalf("DeleteUnpinned: %v", err)
	}
	if swept.DigestsReclaimed != 0 || swept.VersionsDeleted != 0 {
		t.Fatalf("sweep reported %+v over a referenced digest, want nothing touched", swept)
	}
	if f.storedVersions(t, attachment.Digest) != 1 {
		t.Fatal("the referenced object is gone")
	}
	// And it is still readable, which is the assertion that would catch a
	// delete marker written over live bytes.
	if _, _, err = f.store.GetAttachment(ctx, f.organizationID, attachment.AttachmentID); err != nil {
		t.Fatalf("GetAttachment after a sweep: %v", err)
	}
}

// TestSweepFindsADigestKeyWithOnlyIncompleteUploads is why candidate
// discovery unions the two enumerations.
//
// A promote is a server-side copy, multipart for large objects, and it can
// die halfway. What survives is parts: no version at all. Version
// enumeration cannot see them, and the reachability check cannot either --
// there is no object to be unreferenced -- so a candidate set built from
// versions alone leaves that storage in the bucket forever.
func TestSweepFindsADigestKeyWithOnlyIncompleteUploads(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// A digest nothing was ever stored under, carrying only the residue of a
	// promote that died partway.
	digest := digestOf([]byte("the object this promote never finished"))
	key := objectKeyFor(f.organizationID, digest)
	startAbandonedUpload(t, f.blobConfig, key)

	if f.storedVersions(t, digest) != 0 {
		t.Fatal("the fixture stored a version; this test is about a key that has none")
	}
	uploads, err := f.blob.ListUploadsForKey(ctx, key)
	if err != nil {
		t.Fatalf("ListUploadsForKey: %v", err)
	}
	if len(uploads) != 1 {
		t.Fatalf("%d incomplete uploads on the digest key, want the one this test started", len(uploads))
	}

	swept, err := f.storeAfterGrace(t).DeleteUnpinned(ctx, f.organizationID)
	if err != nil {
		t.Fatalf("DeleteUnpinned: %v", err)
	}
	if swept.DigestsReclaimed != 1 || swept.UploadsAborted != 1 {
		t.Fatalf("sweep reported %+v, want the upload-only digest reclaimed", swept)
	}
	remaining, err := f.blob.ListUploadsForKey(ctx, key)
	if err != nil {
		t.Fatalf("ListUploadsForKey after the sweep: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("%d incomplete uploads survive; a candidate set built from versions alone would "+
			"have missed this key entirely", len(remaining))
	}
}

// TestSweepDefersResidueInsideTheGracePeriod exercises the backstop by
// MOVING THE CLOCK, which is the only way design D6 permits: a test that
// switched the rule off would prove the rule is switchable and nothing else.
//
// The same fixture is swept twice. Nothing about the bucket changes between
// the passes -- only what time the sweep believes it is.
func TestSweepDefersResidueInsideTheGracePeriod(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	digest := f.unreferencedObject(t, []byte("promoted a moment ago, row still to come"))

	// The seam's real clock: this residue appeared seconds ago.
	fresh, err := f.store.DeleteUnpinned(ctx, f.organizationID)
	if err != nil {
		t.Fatalf("DeleteUnpinned: %v", err)
	}
	if fresh.DeferredYoung != 1 || fresh.DigestsReclaimed != 0 {
		t.Fatalf("sweep reported %+v over residue seconds old, want it deferred as young", fresh)
	}
	if f.storedVersions(t, digest) != 1 {
		t.Fatal("fresh residue was deleted inside the grace period")
	}
	// Nothing was condemned, so nothing may have been recorded either: a
	// claim written for a digest the pass then declined to touch would block
	// the existing-object shortcut for storage that is still there.
	if claims := f.liveClaims(t); claims != 0 {
		t.Fatalf("%d deletion claims exist after a deferred digest, want 0", claims)
	}

	// The same bucket, a clock a day later.
	aged, err := f.storeAfterGrace(t).DeleteUnpinned(ctx, f.organizationID)
	if err != nil {
		t.Fatalf("DeleteUnpinned with an aged clock: %v", err)
	}
	if aged.DeferredYoung != 0 || aged.DigestsReclaimed != 1 {
		t.Fatalf("sweep reported %+v once the residue was old, want it reclaimed", aged)
	}
	if f.storedVersions(t, digest) != 0 {
		t.Fatal("residue past the grace period was not reclaimed")
	}
}

// TestSweepBlocksOnTheLockAndThenFindsTheReference is the barrier test, and
// it is the one that proves the sweep's decision is sound rather than
// merely usually right.
//
// The window is real: between the object landing at its digest key and the
// attachment row committing, the object is legitimately unreferenced, and a
// sweep running in that window deletes the bytes of a commit in progress.
// What closes it is that both parties serialise on the digest lock and the
// sweep rechecks references under it.
//
// Launching the two concurrently and hoping they collide would be flaky when
// it fails and vacuous when it passes, so the collision is FORCED: the
// writer is stalled inside its transaction, after the promote and before the
// insert, with the lock held; the sweep is then started and waited for until
// Postgres itself reports a backend blocked on a lock.
//
// The sweep's clock is aged past the grace period on purpose. If the object
// survived because it was young, this test would pass with the lock protocol
// removed entirely.
func TestSweepBlocksOnTheLockAndThenFindsTheReference(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	body := []byte("bytes whose attachment row has not committed yet")
	digest := digestOf(body)

	stall := newStallAfterPromote(t, objectKeyFor(f.organizationID, digest))
	writer := f.storeWithTransport(t, stall)

	written := make(chan error, 1)
	go func() { _, err := writer.PutAttachment(ctx, putInput(f.organizationID, body)); written <- err }()

	select {
	case <-stall.reached:
	case err := <-written:
		t.Fatalf("the write finished without ever stalling (%v), so no window was held open", err)
	case <-time.After(barrierTimeout):
		t.Fatal("the writer never reached its post-promote read-back")
	}

	// The object is at the digest key, no row references it, and the writer
	// holds the digest lock. This is the window.
	if f.storedVersions(t, digest) == 0 {
		t.Fatal("the promote has not landed, so the sweep would find nothing to delete")
	}

	swept := make(chan store.ObjectSweep, 1)
	sweepFailed := make(chan error, 1)
	go func() {
		result, err := f.storeAfterGrace(t).DeleteUnpinned(ctx, f.organizationID)
		if err != nil {
			sweepFailed <- err
			return
		}
		swept <- result
	}()

	// Postgres reporting a backend waiting on a lock is the proof that the
	// sweep got past discovery and is now blocked in condemn. Sleeping here
	// instead would make the test pass whether or not the lock was ever
	// taken.
	waitForLockWait(t, f)
	stall.free()

	if err := <-written; err != nil {
		t.Fatalf("the stalled write failed: %v", err)
	}
	select {
	case err := <-sweepFailed:
		t.Fatalf("DeleteUnpinned: %v", err)
	case result := <-swept:
		if result.DeferredReferenced != 1 {
			t.Fatalf("sweep reported %+v, want the digest deferred because the recheck found the "+
				"reference the writer committed while the sweep waited", result)
		}
		if result.DigestsReclaimed != 0 || result.VersionsDeleted != 0 {
			t.Fatalf("sweep reported %+v, want nothing deleted", result)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the sweep never completed after the writer committed")
	}

	if f.storedVersions(t, digest) != 1 {
		t.Fatal("the committed write's object was deleted; the recheck under the lock did not hold")
	}
}

// stallAfterPromote holds the first read-back that follows a successful
// server-side copy onto one key, and lets everything else through.
//
// It watches for the copy rather than counting requests, because the request
// it must not stall is the SHORTCUT's existence check on the same key: that
// one runs before anything is stored, and stalling there would hold the lock
// over an empty digest key -- a window in which the sweep correctly finds
// nothing, which is not the window under test.
type stallAfterPromote struct {
	key      string
	reached  chan struct{}
	release  chan struct{}
	promoted atomic.Bool
	once     sync.Once
	// freed guards the release channel so it can be closed from both the test's
	// success path and its cleanup. See newStallAfterPromote.
	freed sync.Once
}

// newStallAfterPromote builds a stall and registers its release as CLEANUP.
//
// The release must happen on every exit path, not only the successful one, and
// the reason is more specific than a leaked goroutine. The stalled writer holds
// an ACQUIRED POOLED CONNECTION with an open transaction, and `pgxpool.Close()`
// waits for acquired connections to be released. So a `t.Fatal` between the
// stall and its release does not merely strand a goroutine that might collide
// with a later test — it blocks this fixture's own teardown, forever, every
// time. That is why the failure presents as a package-wide timeout rather than
// as one test failing.
//
// MEASURED: removing this registration and forcing a failure inside the window
// reproduces `panic: test timed out after 1m30s`; with it, the same assertion
// fails in 0.63s.
//
// Registering the release here rather than remembering it at each call site is
// the point: the paths that need it most are the ones nobody writes out.
func newStallAfterPromote(t *testing.T, key string) *stallAfterPromote {
	t.Helper()
	stall := &stallAfterPromote{
		key:     key,
		reached: make(chan struct{}),
		release: make(chan struct{}),
	}
	t.Cleanup(stall.free)
	return stall
}

// free releases the stalled writer, and is safe to call more than once — the
// test's own success path calls it too, and a cleanup that panicked on a
// second close would convert a passing test into a failing one.
func (s *stallAfterPromote) free() {
	s.freed.Do(func() { close(s.release) })
}

func (s *stallAfterPromote) RoundTrip(req *http.Request) (*http.Response, error) {
	onKey := strings.Contains(req.URL.Path, s.key)
	isCopy := req.Method == http.MethodPut && req.Header.Get("X-Amz-Copy-Source") != ""

	switch {
	case onKey && isCopy:
		response, err := http.DefaultTransport.RoundTrip(req)
		if err == nil {
			s.promoted.Store(true)
		}
		return response, err

	case onKey && s.promoted.Load() && (req.Method == http.MethodGet || req.Method == http.MethodHead):
		// Once only. A client retry arriving after the barrier was released
		// must pass straight through rather than wait on a channel that has
		// already served its purpose.
		s.once.Do(func() {
			close(s.reached)
			<-s.release
		})
	}
	return http.DefaultTransport.RoundTrip(req)
}

// storeWithTransport builds a second seam over the same database and bucket,
// reaching the object store through a caller-supplied transport.
func (f *fixture) storeWithTransport(t *testing.T, transport http.RoundTripper) *postgres.Store {
	t.Helper()
	cfg := f.blobConfig
	cfg.Transport = transport
	blob, err := objects.New(cfg)
	if err != nil {
		t.Fatalf("build a blob over the injected transport: %v", err)
	}
	built, err := postgres.New(f.pool, testRegistry(t), blob, f.rootKey)
	if err != nil {
		t.Fatalf("build the store: %v", err)
	}
	return built
}

// TestAClaimAbortsOnlyTheUploadItRecorded is the fence the durable claim
// exists to provide, and the reason every id in it is specific.
//
// A claim records INTENT, not completion. The delete it describes may still
// be in flight when a later writer starts work on the same digest key, so a
// recovery that aborted "the uploads on this key" would kill that writer's
// upload. What makes a late arrival harmless is that it can only name what
// was condemned.
//
// The sequence is built out of shipped code paths rather than hand-written
// rows: a sweep whose object store refuses deletions leaves a real claim
// behind, which is also the proof that the claim was committed BEFORE the
// first remote call.
func TestAClaimAbortsOnlyTheUploadItRecorded(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	digest := digestOf([]byte("a digest key two writers used in turn"))
	key := objectKeyFor(f.organizationID, digest)
	condemned := startAbandonedUpload(t, f.blobConfig, key)

	// A sweep that can condemn but cannot delete. The claim it writes is
	// durable; the abort it then issues fails.
	broken := f.storeThatCannotDelete(t, postgres.WithClock(pastGrace))
	if _, err := broken.DeleteUnpinned(ctx, f.organizationID); err == nil {
		t.Fatal("the sweep reported success although its object store refuses deletions")
	}
	if claims := f.liveClaims(t); claims != 1 {
		t.Fatalf("%d deletion claims survive a sweep that failed mid-delete, want the one it "+
			"recorded before issuing anything", claims)
	}

	// A newer upload on the same key, which the surviving claim does not
	// name. This is the writer a key-scoped abort would destroy.
	survivor := startAbandonedUpload(t, f.blobConfig, key)
	if survivor == condemned {
		t.Fatal("the two uploads share an id, so this test cannot tell them apart")
	}

	reconciled, err := f.store.ReconcileDeletionClaims(ctx)
	if err != nil {
		t.Fatalf("ReconcileDeletionClaims: %v", err)
	}
	if reconciled.ClaimsCleared != 1 || reconciled.UploadsAborted != 1 {
		t.Fatalf("reconciler reported %+v, want one claim cleared and one upload aborted", reconciled)
	}

	remaining, err := f.blob.ListUploadsForKey(ctx, key)
	if err != nil {
		t.Fatalf("ListUploadsForKey: %v", err)
	}
	ids := make([]string, 0, len(remaining))
	for _, upload := range remaining {
		ids = append(ids, upload.UploadID)
	}
	if !slices.Equal(ids, []string{survivor}) {
		t.Fatalf("uploads on the key are %v, want only the newer one (%s): a claim may abort only the "+
			"ids it recorded", ids, survivor)
	}
	if claims := f.liveClaims(t); claims != 0 {
		t.Fatalf("%d claims survive reconciliation, want 0", claims)
	}
}

// TestSweepLeavesAClaimedDigestToItsOwner covers ordinary post-crash state,
// which the sweep meets on its very next pass.
//
// Taking over another actor's claim does not fence the original delete --
// which may still be in flight -- so a second claim over the same storage
// would condemn it twice and fence neither attempt. Without this the digest
// reaches the insert, trips the one-claim-per-digest constraint, and kills
// the whole pass: ordinary residue turning into a hard failure.
func TestSweepLeavesAClaimedDigestToItsOwner(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	digest := f.unreferencedObject(t, []byte("condemned by a sweep that then died"))
	broken := f.storeThatCannotDelete(t, postgres.WithClock(pastGrace))
	if _, err := broken.DeleteUnpinned(ctx, f.organizationID); err == nil {
		t.Fatal("the sweep reported success although its object store refuses deletions")
	}
	if claims := f.liveClaims(t); claims != 1 {
		t.Fatalf("%d claims after a sweep that failed mid-delete, want 1", claims)
	}

	// A healthy sweep now meets the claim. It must decline, report, and not
	// fail.
	swept, err := f.storeAfterGrace(t).DeleteUnpinned(ctx, f.organizationID)
	if err != nil {
		t.Fatalf("a second sweep over a claimed digest failed: %v", err)
	}
	if swept.DeferredClaimed != 1 {
		t.Fatalf("sweep reported %+v, want the digest deferred as already claimed", swept)
	}
	if swept.DigestsReclaimed != 0 || swept.VersionsDeleted != 0 {
		t.Fatalf("sweep reported %+v, want it to have deleted nothing", swept)
	}
	if f.liveClaims(t) != 1 {
		t.Fatal("the second sweep interfered with the first's claim")
	}
	if f.storedVersions(t, digest) != 1 {
		t.Fatal("the second sweep deleted storage the first had already condemned")
	}

	// And the reconciler is what finishes it.
	reconciled, err := f.store.ReconcileDeletionClaims(ctx)
	if err != nil {
		t.Fatalf("ReconcileDeletionClaims: %v", err)
	}
	if reconciled.ClaimsCleared != 1 || reconciled.VersionsDeleted != 1 {
		t.Fatalf("reconciler reported %+v, want the claim finished", reconciled)
	}
	if f.storedVersions(t, digest) != 0 {
		t.Fatal("the reconciler cleared the claim without reclaiming its storage")
	}
}

// TestReconcilerIsIdempotent is what makes recovery safe to run at any time,
// including at every `dataplane-up` on a plane with nothing to recover.
func TestReconcilerIsIdempotent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	digest := f.unreferencedObject(t, []byte("reclaimed once, asked for twice"))
	broken := f.storeThatCannotDelete(t, postgres.WithClock(pastGrace))
	if _, err := broken.DeleteUnpinned(ctx, f.organizationID); err == nil {
		t.Fatal("the sweep reported success although its object store refuses deletions")
	}

	first, err := f.store.ReconcileDeletionClaims(ctx)
	if err != nil {
		t.Fatalf("first reconciliation: %v", err)
	}
	if first.ClaimsCleared != 1 {
		t.Fatalf("first reconciliation reported %+v, want one claim cleared", first)
	}

	second, err := f.store.ReconcileDeletionClaims(ctx)
	if err != nil {
		t.Fatalf("second reconciliation: %v", err)
	}
	if second != (store.ClaimReconciliation{}) {
		t.Fatalf("second reconciliation reported %+v over an empty table, want nothing", second)
	}
	if f.storedVersions(t, digest) != 0 {
		t.Fatal("the storage came back")
	}
}

// TestReconcilerFinishesAClaimWhoseStorageIsAlreadyGone covers the exact
// window the deletion claim exists for, which nothing else reached.
//
// The claim is committed BEFORE the first remote call, so the crash it
// anticipates is the one AFTER the deletes have landed and before the row
// clears. Recovery then re-issues a delete for a version that is no longer
// there. If that reads as a failure, the claim is stranded permanently -- and
// it goes on forbidding the existing-object shortcut for its digest at every
// startup, forever, on the one path whose purpose is to finish work an
// earlier actor could not.
//
// The pinned server answers a repeated version delete with no error, so what
// this test can show is that the OUTCOME is right end to end. That the
// tolerance is in the adapter rather than in the server's manners is pinned
// separately, against a canned NoSuchVersion, in the objects package.
func TestReconcilerFinishesAClaimWhoseStorageIsAlreadyGone(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	digest := f.unreferencedObject(t, []byte("deleted, but the claim never cleared"))
	broken := f.storeThatCannotDelete(t, postgres.WithClock(pastGrace))
	if _, err := broken.DeleteUnpinned(ctx, f.organizationID); err == nil {
		t.Fatal("the sweep reported success although its object store refuses deletions")
	}
	if f.liveClaims(t) != 1 {
		t.Fatal("no claim survived, so this test is not exercising recovery")
	}

	// The deletes land after all -- the crash is between them and the clear.
	f.deleteStoredObject(t, digest)
	if f.storedVersions(t, digest) != 0 {
		t.Fatal("the storage is still there; this test is about a claim whose work is already done")
	}

	recovered, err := f.store.ReconcileDeletionClaims(ctx)
	if err != nil {
		t.Fatalf("reconciling a claim whose storage is already gone: %v", err)
	}
	if recovered.ClaimsCleared != 1 {
		t.Fatalf("reconciler reported %+v, want the claim cleared", recovered)
	}
	if claims := f.liveClaims(t); claims != 0 {
		t.Fatalf("%d claims survive; a claim stranded here is retried at every startup forever", claims)
	}
}

// TestSweepIgnoresAnotherOrganizationsStorage is the tenancy boundary, which
// applies to the sweep exactly as it does to every read and write on this
// seam. Organization-first keys are what make it a prefix question rather
// than a cross-tenant reference count -- the query most likely to be wrong
// and least likely to be noticed.
func TestSweepIgnoresAnotherOrganizationsStorage(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	body := []byte("another tenant's unreferenced object")
	digest := digestOf(body)
	foreignKey := objectKeyFor(f.otherOrgID, digest)
	if _, err := f.blob.PutStaged(ctx, foreignKey, int64(len(body)), bytes.NewReader(body)); err != nil {
		t.Fatalf("write the other organization's object: %v", err)
	}

	swept, err := f.storeAfterGrace(t).DeleteUnpinned(ctx, f.organizationID)
	if err != nil {
		t.Fatalf("DeleteUnpinned: %v", err)
	}
	if swept != (store.ObjectSweep{}) {
		t.Fatalf("sweep of one organization reported %+v with only another's storage present", swept)
	}
	versions, err := f.blob.ListVersions(ctx, foreignKey)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions) != 1 {
		t.Fatal("the other organization's object was swept")
	}
}

// TestSweepLeavesStagingAndUnrecognisedKeysAlone covers what lives under an
// organization's prefix without being the sweep's to touch.
//
// Staging belongs to the lease-driven path, which locks a lease row and
// rechecks expiry under it; the sweep does neither, and ADR 0027 forbids
// removing another actor's in-progress work. The staging prefix is outside
// the sweep's discovery prefix entirely, which is the mechanism -- asserted
// here because the two prefixes only differ by a convention that a later edit
// could collapse.
//
// A misfiled key is a different case, and mutation testing sharpened what is
// actually worth asserting about it. The parse in noteCandidate refuses keys
// this module would not have written, but loosening that rule changes
// NOTHING observable: a spurious candidate's canonical key is empty and the
// pass declines it. What protects a misfiled object is that every destructive
// call is ADDRESSED -- the key is rebuilt from the digest, never taken from
// the listing that discovered it.
//
// So the assertion below is built to see that: the same digest has residue at
// its canonical address AND at a misfiled one, and the sweep must reclaim
// exactly the first. A delete addressed from the discovered key, or by a
// fan-out someone simplified away, takes the wrong one and fails here.
func TestSweepLeavesStagingAndUnrecognisedKeysAlone(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	staged := stagingKeyFor(t, f.organizationID)
	f.seedLease(t, staged, 3600)
	if _, err := f.blob.PutStaged(ctx, staged, 4, bytes.NewReader([]byte("live"))); err != nil {
		t.Fatalf("stage an object under a live lease: %v", err)
	}

	// One digest, two addresses. The canonical one is a real sweep candidate;
	// the other is the same bytes filed where nothing addresses them.
	body := []byte("misfiled")
	digest := f.unreferencedObject(t, body)
	misfiled := f.organizationID.String() + "/" + digest
	// And a key whose last segment is not a digest at all, which no amount of
	// addressing would make sense of.
	nonsense := f.organizationID.String() + "/aa/bb/not-a-digest"
	for _, stray := range []string{misfiled, nonsense} {
		if _, err := f.blob.PutStaged(ctx, stray, int64(len(body)), bytes.NewReader(body)); err != nil {
			t.Fatalf("write %s: %v", stray, err)
		}
	}

	swept, err := f.storeAfterGrace(t).DeleteUnpinned(ctx, f.organizationID)
	if err != nil {
		t.Fatalf("DeleteUnpinned: %v", err)
	}
	if swept.DigestsReclaimed != 1 || swept.VersionsDeleted != 1 {
		t.Fatalf("sweep reported %+v, want exactly the canonically addressed object reclaimed", swept)
	}
	if f.storedVersions(t, digest) != 0 {
		t.Fatal("the object at the canonical digest key was not reclaimed")
	}
	for _, key := range []string{misfiled, nonsense, staged} {
		versions, listErr := f.blob.ListVersions(ctx, key)
		if listErr != nil {
			t.Fatalf("ListVersions(%s): %v", key, listErr)
		}
		if len(versions) != 1 {
			t.Fatalf("%s was swept; nothing here is addressed by that key", key)
		}
	}
}

// TestClaimedDigestsNeverConsumeTheBudget is the starvation case, and it is
// the difference between a deferral and a permanent one.
//
// A referenced digest stops being a candidate the moment it is referenced. A
// CLAIMED digest does not: the claim survives until its owner or the
// reconciler finishes it, so the digest is discovered again on every pass. If
// classifying it consumed a slot of the per-pass budget, a full budget's
// worth of claims sorting ahead of one ordinary unreferenced object would
// spend every pass declining the same digests and never reach that object --
// not for a pass or two, but for as long as the claims lasted.
//
// The claims are made to sort FIRST rather than left to chance: digests are
// examined in lexical order, so the reclaimable one is the largest of the set
// and every claimed one precedes it.
func TestClaimedDigestsNeverConsumeTheBudget(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// One more than the pass condemns, so the budget cannot absorb them even
	// if it were spent on them.
	const claimed = 101

	// Every digest gets residue, because a digest with none is not a
	// candidate at all and would prove nothing.
	digests := make([]string, 0, claimed+1)
	for i := range claimed + 1 {
		digests = append(digests, f.unreferencedObject(t, []byte(strings.Repeat("y", i+1))))
	}
	slices.Sort(digests)
	// The largest sorts last; everything before it is claimed.
	reclaimable := digests[len(digests)-1]

	for _, digest := range digests[:len(digests)-1] {
		f.seedClaim(t, digest)
	}

	swept, err := f.storeAfterGrace(t).DeleteUnpinned(ctx, f.organizationID)
	if err != nil {
		t.Fatalf("DeleteUnpinned: %v", err)
	}
	if swept.DeferredClaimed != claimed {
		t.Fatalf("sweep reported %+v, want all %d claimed digests declined", swept, claimed)
	}
	if swept.DigestsReclaimed != 1 {
		t.Fatalf("sweep reported %+v: the one reclaimable digest sorts behind %d claimed ones, and "+
			"a budget spent on those never reaches it", swept, claimed)
	}
	if swept.DeferredForNextPass != 0 {
		t.Fatalf("sweep reported %+v, want the claimed digests filtered out BEFORE the bound rather "+
			"than counted against it", swept)
	}
	if f.storedVersions(t, reclaimable) != 0 {
		t.Fatal("the reclaimable object survived a pass that had budget for it")
	}
	// And the claims are untouched: declining is not finishing.
	if claims := f.liveClaims(t); claims != claimed {
		t.Fatalf("%d claims survive, want all %d left for their owners", claims, claimed)
	}
}

// seedClaim condemns a digest on behalf of an actor that never came back.
//
// Written directly, because what the test needs is the STATE a crashed sweep
// leaves, at a scale no sequence of failing sweeps would produce in
// reasonable time. The protocol that writes one is exercised, through the
// shipped path, by TestAClaimAbortsOnlyTheUploadItRecorded.
func (f *fixture) seedClaim(t *testing.T, digest string) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(),
		`INSERT INTO deletion_claims (deletion_claim_id, organization_id, object_digest, version_ids, upload_ids)
		 VALUES ($1, $2, $3, $4, $5)`,
		uuid.New(), f.organizationID, digest, []string{"stale-version"}, []string{}); err != nil {
		t.Fatalf("seed a deletion claim on %s: %v", digest, err)
	}
}

// TestSweepDefersWhatItsBoundLeaves proves the bound is a DEFERRAL rather
// than a cap. Candidates are discovered by their own residue, so the
// remainder is found again on the next pass -- but a bound that dropped work
// silently would be indistinguishable from a bucket with nothing left in it,
// which is why the count is reported and not merely logged.
func TestSweepDefersWhatItsBoundLeaves(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// One more than the pass condemns. Written straight to their digest keys:
	// what matters is the number of unreferenced digests, not how they got
	// there.
	const over = 101
	for i := range over {
		f.unreferencedObject(t, []byte(strings.Repeat("x", i+1)))
	}

	sweeper := f.storeAfterGrace(t)
	first, err := sweeper.DeleteUnpinned(ctx, f.organizationID)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if first.DeferredForNextPass != 1 {
		t.Fatalf("first pass reported %+v over %d candidates, want one deferred", first, over)
	}
	if first.DigestsReclaimed != over-1 {
		t.Fatalf("first pass reclaimed %d digests, want %d", first.DigestsReclaimed, over-1)
	}

	second, err := sweeper.DeleteUnpinned(ctx, f.organizationID)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if second.DigestsReclaimed != 1 || second.DeferredForNextPass != 0 {
		t.Fatalf("second pass reported %+v, want it to finish the remainder", second)
	}
}
