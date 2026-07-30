//go:build integration

package postgres_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"orchestrator/internal/dataplane/gen"
	"orchestrator/internal/dataplane/objects"
	"orchestrator/internal/dataplane/store"
	"orchestrator/internal/dataplane/store/postgres"
)

// Staging cleanup (item 6 design, D6).
//
// ADR 0027 forbids destructive recovery that removes another actor's
// in-progress work, and this is the operation most able to do it. Every
// test here is about what it must NOT delete, or about residue that only
// one of the two enumerations can see.

// stagingKeyFor mirrors the seam's layout, which the lease table's CHECK
// also enforces: `staging/<organization>/<uuid>`. Duplicated deliberately
// -- a test computing the key by calling the code under test would agree
// with it however wrong both were.
func stagingKeyFor(t *testing.T, organizationID uuid.UUID) string {
	t.Helper()
	uploadID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("allocate staging id: %v", err)
	}
	return "staging/" + organizationID.String() + "/" + uploadID.String()
}

// seedLease writes a lease expiring termSeconds from now. A negative term
// is a lease that has already lapsed.
//
// An expired lease has to be AGED, not born expired: the schema's
// expires_at > created_at check refuses a lease whose term was empty from
// the start, so the row is written as one that was valid an hour ago and
// has since lapsed -- which is the state cleanup actually meets.
// The owner token is generated and not returned: cleanup holds none -- that
// is the whole reason it cannot simply delete what it finds -- so no test
// here has any use for it.
func (f *fixture) seedLease(t *testing.T, stagingKey string, termSeconds int) {
	t.Helper()
	token := uuid.New()
	if _, err := f.pool.Exec(context.Background(), `
		INSERT INTO staging_leases (staging_lease_id, organization_id, staging_key, owner_token,
			created_at, expires_at)
		VALUES (gen_random_uuid(), $1, $2, $3,
		        clock_timestamp() - interval '1 hour',
		        clock_timestamp() + make_interval(secs => $4::double precision))`,
		f.organizationID, stagingKey, token, termSeconds); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
}

func (f *fixture) leaseExists(t *testing.T, stagingKey string) bool {
	t.Helper()
	var exists bool
	if err := f.pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM staging_leases WHERE organization_id = $1 AND staging_key = $2)`,
		f.organizationID, stagingKey).Scan(&exists); err != nil {
		t.Fatalf("check lease: %v", err)
	}
	return exists
}

// TestCleanupRemovesAnAbandonedStagingObject is the ordinary case: a writer
// completed its upload and died before promoting.
func TestCleanupRemovesAnAbandonedStagingObject(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	key := stagingKeyFor(t, f.organizationID)
	f.seedLease(t, key, -60)
	body := []byte("an object nobody will promote")
	if _, err := f.blob.PutStaged(ctx, key, int64(len(body)), bytes.NewReader(body)); err != nil {
		t.Fatalf("stage the object: %v", err)
	}

	result, err := f.store.CleanUpStaging(ctx, f.organizationID)
	if err != nil {
		t.Fatalf("CleanUpStaging: %v", err)
	}
	if result.LeasesReleased != 1 {
		t.Fatalf("released %d leases, want 1", result.LeasesReleased)
	}

	versions, err := f.blob.ListVersions(ctx, key)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions) != 0 {
		t.Fatalf("%d versions survived cleanup: %+v", len(versions), versions)
	}
	if f.leaseExists(t, key) {
		t.Fatal("the lease row survived its own cleanup")
	}
}

// TestCleanupAbortsAnIncompleteUpload covers the other crash window, and
// the residue only ONE of the two enumerations can see.
//
// A writer that died during a multipart upload leaves parts: not an object
// version, so version enumeration cannot find them, and DeleteVersion
// cannot remove them. Cleanup that only deleted versions would leave them
// occupying storage forever.
func TestCleanupAbortsAnIncompleteUpload(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	key := stagingKeyFor(t, f.organizationID)
	f.seedLease(t, key, -60)
	startAbandonedUpload(t, f.blobConfig, key)

	// Invisible to versions, which is the whole point.
	versions, err := f.blob.ListVersions(ctx, key)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions) != 0 {
		t.Fatalf("an incomplete upload appeared as %d versions; this test describes nothing", len(versions))
	}

	if _, err := f.store.CleanUpStaging(ctx, f.organizationID); err != nil {
		t.Fatalf("CleanUpStaging: %v", err)
	}

	uploads, uploadsErr := f.blob.ListUploadsForKey(ctx, key)
	if uploadsErr != nil {
		t.Fatalf("ListUploadsForKey: %v", uploadsErr)
	}
	if len(uploads) != 0 {
		t.Fatalf("%d incomplete uploads survived cleanup", len(uploads))
	}
	if f.leaseExists(t, key) {
		t.Fatal("the lease row survived its own cleanup")
	}
}

// TestCleanupSparesALiveLease is the rule ADR 0027 demands. A staging
// object under a live lease is another actor's work in progress, and
// "the writer's promote will fail afterwards" is not a mitigation.
func TestCleanupSparesALiveLease(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	key := stagingKeyFor(t, f.organizationID)
	f.seedLease(t, key, 3600)
	body := []byte("an object being uploaded right now")
	if _, err := f.blob.PutStaged(ctx, key, int64(len(body)), bytes.NewReader(body)); err != nil {
		t.Fatalf("stage the object: %v", err)
	}

	result, err := f.store.CleanUpStaging(ctx, f.organizationID)
	if err != nil {
		t.Fatalf("CleanUpStaging: %v", err)
	}
	if result.LeasesReleased != 0 {
		t.Fatalf("released %d live leases", result.LeasesReleased)
	}

	versions, err := f.blob.ListVersions(ctx, key)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("the live writer's object has %d versions, want the one it staged", len(versions))
	}
	if !f.leaseExists(t, key) {
		t.Fatal("a live lease was deleted")
	}
}

// TestCleanupIsIdempotent covers the re-run. A pass that appeared after
// another had already collected the lease must not fail, and a version that
// appears after one pass is removed by the next.
func TestCleanupIsIdempotent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	key := stagingKeyFor(t, f.organizationID)
	f.seedLease(t, key, -60)
	body := []byte("collected twice")
	if _, err := f.blob.PutStaged(ctx, key, int64(len(body)), bytes.NewReader(body)); err != nil {
		t.Fatalf("stage the object: %v", err)
	}

	for pass := range 2 {
		if _, err := f.store.CleanUpStaging(ctx, f.organizationID); err != nil {
			t.Fatalf("pass %d: %v", pass+1, err)
		}
	}
}

// TestCleanupIsOrganizationScoped keeps the pass inside its tenant.
func TestCleanupIsOrganizationScoped(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	otherKey := "staging/" + f.otherOrgID.String() + "/" + uuid.New().String()
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO staging_leases (staging_lease_id, organization_id, staging_key, owner_token,
			created_at, expires_at)
		VALUES (gen_random_uuid(), $1, $2, gen_random_uuid(),
		        clock_timestamp() - interval '1 hour', clock_timestamp() - interval '1 minute')`,
		f.otherOrgID, otherKey); err != nil {
		t.Fatalf("seed the other organization's lease: %v", err)
	}
	body := []byte("another tenant's staging object")
	if _, err := f.blob.PutStaged(ctx, otherKey, int64(len(body)), bytes.NewReader(body)); err != nil {
		t.Fatalf("stage in the other organization: %v", err)
	}

	result, err := f.store.CleanUpStaging(ctx, f.organizationID)
	if err != nil {
		t.Fatalf("CleanUpStaging: %v", err)
	}
	if result.LeasesReleased != 0 {
		t.Fatalf("released %d leases belonging to another organization", result.LeasesReleased)
	}
	if result.OrphansCollected != 0 {
		t.Fatalf("collected %d orphans from another organization's staging prefix",
			result.OrphansCollected)
	}
	versions, versionsErr := f.blob.ListVersions(ctx, otherKey)
	if versionsErr != nil {
		t.Fatalf("ListVersions: %v", versionsErr)
	}
	if len(versions) != 1 {
		t.Fatal("another organization's staging object was deleted")
	}
}

// TestCleanupBlocksOnAPromotionHoldingTheLeaseRow is the barrier test, and
// the case the design flags as the one round 4 left as a race.
//
// A promoting transaction holds the lease's row lock across its ownership
// check, the server-side copy, the read-back and the attachment insert --
// remote calls, during which the lease can lapse. Cleanup taking only
// expiry into account would delete the staging object out from under an
// authorised promotion. Taking the SAME row lock is what makes that
// impossible: cleanup either waits, or finds the lease alive.
//
// The promotion is stood in for by a transaction that locks the row and
// holds it, which is exactly the state the real one is in at that point.
// Nothing is timed: the wait is observed in pg_stat_activity, and the
// object's survival is asserted while cleanup is provably blocked.
func TestCleanupBlocksOnAPromotionHoldingTheLeaseRow(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	key := stagingKeyFor(t, f.organizationID)
	// Already expired, so expiry alone would authorise deletion. The lock
	// is the only thing standing between cleanup and the live promotion.
	f.seedLease(t, key, -60)
	body := []byte("being promoted as cleanup arrives")
	if _, err := f.blob.PutStaged(ctx, key, int64(len(body)), bytes.NewReader(body)); err != nil {
		t.Fatalf("stage the object: %v", err)
	}

	promoter, err := f.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin the promoting transaction: %v", err)
	}
	defer func() { _ = promoter.Rollback(ctx) }()
	var lockedKey string
	if lockErr := promoter.QueryRow(ctx,
		`SELECT staging_key FROM staging_leases
		 WHERE organization_id = $1 AND staging_key = $2 FOR UPDATE`,
		f.organizationID, key).Scan(&lockedKey); lockErr != nil {
		t.Fatalf("lock the lease as the promotion would: %v", lockErr)
	}

	cleanupDone := make(chan error, 1)
	go func() {
		_, cleanupErr := f.store.CleanUpStaging(context.Background(), f.organizationID)
		cleanupDone <- cleanupErr
	}()

	// Blocked, observed rather than assumed.
	waitForLockWait(t, f)

	// While it is blocked, the object it would have deleted is intact --
	// which is the assertion the whole design argument is about.
	blockedVersions, err := f.blob.ListVersions(ctx, key)
	if err != nil {
		t.Fatalf("ListVersions while cleanup is blocked: %v", err)
	}
	if len(blockedVersions) != 1 {
		t.Fatalf("the promotion's staging object has %d versions while cleanup waits; cleanup acted "+
			"under a held lock", len(blockedVersions))
	}
	select {
	case err := <-cleanupDone:
		t.Fatalf("cleanup completed (%v) while the promotion held the lease row", err)
	default:
	}

	// The promotion finishes and releases. Cleanup may now proceed -- and
	// by this point the object has been promoted, so collecting the staging
	// copy is correct rather than destructive.
	if commitErr := promoter.Commit(ctx); commitErr != nil {
		t.Fatalf("commit the promoting transaction: %v", commitErr)
	}
	select {
	case err := <-cleanupDone:
		if err != nil {
			t.Fatalf("cleanup failed after the lock was released: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("cleanup never returned after the lock was released")
	}
}

// TestCleanupSkipsALeaseRenewedWhileItWaited is why the expiry recheck
// under the lock exists, and it is the only case that can prove it.
//
// The listing returns only expired leases, so an ordinarily-live lease
// never reaches the recheck at all -- which means every other test here
// passes with the recheck deleted. What it defends is the window between
// the listing and the lock: cleanup decides a lease looks abandoned, waits
// for a lock the writer holds, and by the time it gets in the writer has
// RENEWED. Acting on the stale answer is exactly the destructive recovery
// ADR 0027 forbids.
//
// The renewal is the GENERATED one, performed while the lease is still
// live, which is the only renewal production permits: RenewStagingLease
// requires expires_at > clock_timestamp(), so an expired lease revived by a
// hand-written UPDATE is a state no actor could ever produce. An earlier
// version of this test did exactly that and proved nothing about the
// system.
//
// The sequence, and nothing in it is timed loosely:
//
//  1. a lease with a short but LIVE term;
//  2. the writer renews it through the generated query and holds the
//     transaction open, so the committed row still shows the old expiry;
//  3. wait -- polling the server's clock -- until that old expiry passes;
//  4. cleanup lists the lease as expired and blocks on the row lock,
//     observed in pg_stat_activity;
//  5. the writer commits; cleanup takes the lock, sees the committed
//     extension, and leaves everything alone.
func TestCleanupSkipsALeaseRenewedWhileItWaited(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	key := stagingKeyFor(t, f.organizationID)
	const shortTerm = 2
	token := f.seedLiveLease(t, key, shortTerm)
	body := []byte("still being uploaded, and the lease will be renewed")
	if _, err := f.blob.PutStaged(ctx, key, int64(len(body)), bytes.NewReader(body)); err != nil {
		t.Fatalf("stage the object: %v", err)
	}

	// The writer renews through the SHIPPED query, while the lease is still
	// live, and holds the transaction. This is also what takes the row lock.
	writer, err := f.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin the writer's transaction: %v", err)
	}
	defer func() { _ = writer.Rollback(ctx) }()
	renewed, err := gen.New(writer).RenewStagingLease(ctx, gen.RenewStagingLeaseParams{
		OrganizationID: toPgUUID(f.organizationID),
		StagingKey:     key,
		OwnerToken:     toPgUUID(token),
		TermSeconds:    3600,
	})
	if err != nil {
		t.Fatalf("RenewStagingLease: %v", err)
	}
	if !renewed.ExpiresAt.Time.After(time.Now().Add(time.Hour / 2)) {
		t.Fatalf("the renewal set expires_at to %s, which is not the extension it was asked for",
			renewed.ExpiresAt.Time)
	}

	// The OLD expiry passes. Polled against the server's clock rather than
	// slept against the client's.
	f.waitPastCommittedExpiry(t, key)

	type outcome struct {
		result store.StagingCleanup
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, cleanupErr := f.store.CleanUpStaging(context.Background(), f.organizationID)
		done <- outcome{result, cleanupErr}
	}()
	waitForLockWait(t, f)

	if commitErr := writer.Commit(ctx); commitErr != nil {
		t.Fatalf("commit the renewal: %v", commitErr)
	}

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("CleanUpStaging: %v", got.err)
		}
		if got.result.LeasesReleased != 0 {
			t.Fatalf("released %d leases; the lease was renewed before cleanup got the lock, so there "+
				"was nothing abandoned to collect", got.result.LeasesReleased)
		}
		if got.result.OrphansCollected != 0 {
			t.Fatalf("collected %d orphans; the key still has a lease, so the orphan pass must leave "+
				"it to the lease-driven path", got.result.OrphansCollected)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("cleanup never returned after the renewal committed")
	}

	versions, versionsErr := f.blob.ListVersions(ctx, key)
	if versionsErr != nil {
		t.Fatalf("ListVersions: %v", versionsErr)
	}
	if len(versions) != 1 {
		t.Fatalf("the renewed writer's object has %d versions, want the one it staged", len(versions))
	}
	if !f.leaseExists(t, key) {
		t.Fatal("a renewed lease was deleted")
	}
}

// seedLiveLease writes a lease that is valid now and expires termSeconds
// from now, returning its owner token so a test can renew it the way a
// writer does.
func (f *fixture) seedLiveLease(t *testing.T, stagingKey string, termSeconds int) uuid.UUID {
	t.Helper()
	token := uuid.New()
	if _, err := f.pool.Exec(context.Background(), `
		INSERT INTO staging_leases (staging_lease_id, organization_id, staging_key, owner_token, expires_at)
		VALUES (gen_random_uuid(), $1, $2, $3,
		        clock_timestamp() + make_interval(secs => $4::double precision))`,
		f.organizationID, stagingKey, token, termSeconds); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	return token
}

// waitPastCommittedExpiry blocks until the server's clock passes the expiry
// visible in the COMMITTED row -- which, with a renewal held uncommitted, is
// still the original one.
//
// Polled on a separate connection so it reads committed state, and against
// the server's clock so no client-side timing is involved.
func (f *fixture) waitPastCommittedExpiry(t *testing.T, stagingKey string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		var expired bool
		if err := f.pool.QueryRow(context.Background(),
			`SELECT expires_at <= clock_timestamp() FROM staging_leases
			 WHERE organization_id = $1 AND staging_key = $2`,
			f.organizationID, stagingKey).Scan(&expired); err != nil {
			t.Fatalf("read the committed expiry: %v", err)
		}
		if expired {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the committed expiry never passed")
}

// TestCleanupCollectsResidueThatOutlivedItsLease covers what had no next
// pass at all.
//
// The idempotence claim is that "a version appearing after one pass is
// removed by the next". That was not true: cleanup deletes the lease row
// when it finishes with a key, and the lease is the only record by which
// the key can be found -- the final object sweep never considers the
// staging prefix, and nothing else looked there. So anything appearing
// afterwards was permanent.
//
// A paused writer resuming is the realistic route: the owner token stops it
// PROMOTING, and stops nothing about writing to its own staging key.
func TestCleanupCollectsResidueThatOutlivedItsLease(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	key := stagingKeyFor(t, f.organizationID)
	f.seedLease(t, key, -60)

	// First pass collects the abandoned lease and removes the record.
	first, err := f.store.CleanUpStaging(ctx, f.organizationID)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if first.LeasesReleased != 1 {
		t.Fatalf("the first pass released %d leases, want 1", first.LeasesReleased)
	}
	if f.leaseExists(t, key) {
		t.Fatal("the lease survived, so this test is not producing the state it describes")
	}

	// The paused writer wakes up and writes. Both residue shapes, since
	// each is invisible to the other's enumeration.
	body := []byte("written after the lease was gone")
	if _, writeErr := f.blob.PutStaged(ctx, key, int64(len(body)), bytes.NewReader(body)); writeErr != nil {
		t.Fatalf("late staging write: %v", writeErr)
	}
	lateUploadKey := stagingKeyFor(t, f.organizationID)
	startAbandonedUpload(t, f.blobConfig, lateUploadKey)

	second, err := f.store.CleanUpStaging(ctx, f.organizationID)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if second.OrphansCollected != 2 {
		t.Fatalf("the second pass collected %d orphans, want the late version and the late upload",
			second.OrphansCollected)
	}
	// Reported as orphans and not as leases: a released lease is routine,
	// while residue that outlived its record says something went wrong
	// earlier, and folding the two together would hide that.
	if second.LeasesReleased != 0 {
		t.Fatalf("the second pass released %d leases; there were none left", second.LeasesReleased)
	}

	for _, orphaned := range []string{key, lateUploadKey} {
		versions, versionsErr := f.blob.ListVersions(ctx, orphaned)
		if versionsErr != nil {
			t.Fatalf("ListVersions(%s): %v", orphaned, versionsErr)
		}
		uploads, uploadsErr := f.blob.ListUploadsForKey(ctx, orphaned)
		if uploadsErr != nil {
			t.Fatalf("ListUploadsForKey(%s): %v", orphaned, uploadsErr)
		}
		if len(versions) != 0 || len(uploads) != 0 {
			t.Errorf("%s still holds %d versions and %d uploads after the orphan pass",
				orphaned, len(versions), len(uploads))
		}
	}
}

// TestOrphanCollectionSparesAnOwnedKey is the other half of the licence.
//
// The absence of a lease is what permits deleting an orphan, so a key that
// HAS one must be left to the lease-driven path however it looks -- that
// path locks the row and rechecks expiry, and the orphan pass does neither.
func TestOrphanCollectionSparesAnOwnedKey(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	key := stagingKeyFor(t, f.organizationID)
	f.seedLease(t, key, 3600)
	body := []byte("owned by a live writer")
	if _, err := f.blob.PutStaged(ctx, key, int64(len(body)), bytes.NewReader(body)); err != nil {
		t.Fatalf("stage the object: %v", err)
	}

	result, err := f.store.CleanUpStaging(ctx, f.organizationID)
	if err != nil {
		t.Fatalf("CleanUpStaging: %v", err)
	}
	if result.OrphansCollected != 0 {
		t.Fatalf("collected %d orphans; the key has a live lease", result.OrphansCollected)
	}
	versions, err := f.blob.ListVersions(ctx, key)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions) != 1 {
		t.Fatal("a live writer's staging object was collected as an orphan")
	}
}

// TestWriterCleanupKeepsItsLeaseWhenEmptyingFails is the writer's own
// release path, and the reason it must not delete the lease
// unconditionally.
//
// The lease is the only record by which cleanup can find the key again, so
// removing it after a failed emptying converts recoverable residue into an
// orphan -- which, before orphan discovery existed, meant permanent.
// Leaving it in place means the lease expires and the next pass collects the
// key properly, under its lock.
//
// The write SUCCEEDS here and only its cleanup fails, which is the case
// that matters and the one no external condition can produce: a broken
// bucket fails the write itself long before the release. So the object
// store's DELETE calls are failed at the transport -- the fault injection
// design D7 asks for -- while everything else is left alone.
func TestWriterCleanupKeepsItsLeaseWhenEmptyingFails(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	brokenRelease := f.storeThatCannotDelete(t)

	body := []byte("stored successfully, cleaned up unsuccessfully")
	attachment, err := brokenRelease.PutAttachment(ctx, store.PutAttachmentInput{
		Body:           bytes.NewReader(body),
		Digest:         digestOf(body),
		MediaType:      "application/octet-stream",
		SizeBytes:      int64(len(body)),
		OrganizationID: f.organizationID,
	})
	if err != nil {
		t.Fatalf("the write itself failed, so this test is not exercising the release: %v", err)
	}
	if attachment == nil {
		t.Fatal("no attachment was returned")
	}

	// The lease it took before the first byte is still there. With the
	// release deleting it unconditionally this is zero, and the staging
	// object it could not delete is discoverable by nothing.
	var leases int
	if countErr := f.pool.QueryRow(ctx,
		`SELECT count(*) FROM staging_leases WHERE organization_id = $1`,
		f.organizationID).Scan(&leases); countErr != nil {
		t.Fatalf("count leases: %v", countErr)
	}
	if leases != 1 {
		t.Fatalf("%d staging leases survived a write whose cleanup failed, want the one it took: a "+
			"release that drops the lease after failing to empty leaves residue nothing can find", leases)
	}
}

// storeThatCannotDelete builds a store whose object backend refuses DELETE,
// and nothing else.
func (f *fixture) storeThatCannotDelete(t *testing.T) *postgres.Store {
	t.Helper()
	cfg := f.blobConfig
	cfg.Transport = refuseDeletes{}
	blob, err := objects.New(cfg)
	if err != nil {
		t.Fatalf("build a blob that cannot delete: %v", err)
	}
	built, err := postgres.New(f.pool, testRegistry(t), blob)
	if err != nil {
		t.Fatalf("build the store: %v", err)
	}
	return built
}

// refuseDeletes fails every DELETE and passes everything else through, so a
// write succeeds and the cleanup after it does not.
type refuseDeletes struct{}

func (refuseDeletes) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodDelete {
		return nil, errors.New("injected: the object store refuses deletions")
	}
	return http.DefaultTransport.RoundTrip(req)
}
