//go:build integration

package postgres_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

	released, err := f.store.CleanUpStaging(ctx, f.organizationID)
	if err != nil {
		t.Fatalf("CleanUpStaging: %v", err)
	}
	if released != 1 {
		t.Fatalf("released %d leases, want 1", released)
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

	released, err := f.store.CleanUpStaging(ctx, f.organizationID)
	if err != nil {
		t.Fatalf("CleanUpStaging: %v", err)
	}
	if released != 0 {
		t.Fatalf("released %d live leases", released)
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

	released, err := f.store.CleanUpStaging(ctx, f.organizationID)
	if err != nil {
		t.Fatalf("CleanUpStaging: %v", err)
	}
	if released != 0 {
		t.Fatalf("released %d leases belonging to another organization", released)
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
// passes with the recheck deleted. What the recheck defends against is the
// window between the listing and the lock: cleanup decides a lease looks
// abandoned, waits for a lock the writer holds, and by the time it gets in
// the writer has RENEWED. Acting on the stale answer is exactly the
// destructive recovery ADR 0027 forbids.
//
// Deterministic: cleanup is observed blocking in pg_stat_activity, the
// renewal happens under the lock it is waiting for, and nothing is timed.
func TestCleanupSkipsALeaseRenewedWhileItWaited(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	key := stagingKeyFor(t, f.organizationID)
	f.seedLease(t, key, -60)
	body := []byte("still being uploaded, and the lease will be renewed")
	if _, err := f.blob.PutStaged(ctx, key, int64(len(body)), bytes.NewReader(body)); err != nil {
		t.Fatalf("stage the object: %v", err)
	}

	// The writer takes the row lock, as its renewal does.
	writer, err := f.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin the writer's transaction: %v", err)
	}
	defer func() { _ = writer.Rollback(ctx) }()
	var locked string
	if err := writer.QueryRow(ctx,
		`SELECT staging_key FROM staging_leases
		 WHERE organization_id = $1 AND staging_key = $2 FOR UPDATE`,
		f.organizationID, key).Scan(&locked); err != nil {
		t.Fatalf("lock the lease as the writer would: %v", err)
	}

	// Cleanup lists the lease -- still expired at this instant -- and then
	// blocks on the lock.
	type outcome struct {
		released int
		err      error
	}
	done := make(chan outcome, 1)
	go func() {
		released, cleanupErr := f.store.CleanUpStaging(context.Background(), f.organizationID)
		done <- outcome{released, cleanupErr}
	}()
	waitForLockWait(t, f)

	// The renewal, under the lock cleanup is waiting for. From here on the
	// lease is alive and the listing's answer is stale.
	if _, renewErr := writer.Exec(ctx,
		`UPDATE staging_leases SET expires_at = clock_timestamp() + interval '1 hour'
		 WHERE organization_id = $1 AND staging_key = $2`,
		f.organizationID, key); renewErr != nil {
		t.Fatalf("renew the lease: %v", renewErr)
	}
	if commitErr := writer.Commit(ctx); commitErr != nil {
		t.Fatalf("commit the renewal: %v", commitErr)
	}

	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("CleanUpStaging: %v", result.err)
		}
		if result.released != 0 {
			t.Fatalf("released %d leases; the lease was renewed before cleanup got the lock, so "+
				"there was nothing abandoned to collect", result.released)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("cleanup never returned after the renewal committed")
	}

	// The writer's object and its lease both survive.
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
