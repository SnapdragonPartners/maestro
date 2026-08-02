//go:build integration

package postgres_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"testing"
	"time"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/store"
)

// Verify's own suite (item 8, design D7).
//
// Verify is the only thing that can observe a TORN PAIR — a Postgres cluster
// and an object store whose contents disagree. Each store is internally
// consistent on its own, so the disagreement is invisible from either side;
// recomputing digests across both is what finds it.
//
// Which makes verify's own failure modes worth more than usual. A verifier
// that misses corruption is useless, and a verifier that reports corruption
// on a healthy plane is worse than useless: the response to a tool that
// cries wolf is to stop believing it, and the next real finding is the one
// that gets ignored.

// The object-key layout comes from objectKeyFor, which the object suite
// already mirrors. It is pinned by use here: every test below asserts
// something is at the key before acting on it, so a layout change fails
// loudly instead of leaving a test that quietly touches nothing.

// digestLockKeyFor reproduces the advisory-lock key writers, the sweep and
// verify serialise on.
//
// Also pinned by use rather than by inspection: the test that holds this
// lock waits for verify to BLOCK on it and fails if it never does. A key
// that did not match would produce no waiter and a clear failure, not a
// silently weaker test.
func digestLockKeyFor(organizationID uuid.UUID, digest string) int64 {
	sum := sha256.Sum256([]byte(organizationID.String() + "/" + digest))
	return int64(binary.BigEndian.Uint64(sum[:8])) //nolint:gosec // the key is opaque; the sign carries no meaning
}

// TestVerifyWalksBothStores is the baseline every other verification claim
// rests on: a healthy plane reports healthy, and reports how much it
// actually checked.
//
// The counts are the load-bearing half. An empty plane also reports no
// problems, so "healthy" alone distinguishes a verified plane from an
// unwalked one not at all.
func TestVerifyWalksBothStores(t *testing.T) {
	f := evidenceFixture(t)
	ctx := context.Background()

	f.acceptedOriginal(t)

	report, err := f.store.Verify(ctx)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !report.Healthy() {
		t.Fatalf("verify found %d problem(s) on an undamaged plane: %+v", len(report.Problems), report.Problems)
	}
	if report.Attachments != 1 {
		t.Errorf("verify read %d attachments, want 1: a pass that reads none recomputes nothing "+
			"across the object store and still reports healthy", report.Attachments)
	}
	if report.ManagementArtifacts != 1 {
		t.Errorf("verify checked %d Management artifacts, want 1", report.ManagementArtifacts)
	}
	if report.Skipped != 0 {
		t.Errorf("verify skipped %d attachments with nothing deleting rows, want 0", report.Skipped)
	}
}

// TestVerifyDetectsAnObjectThatDoesNotMatchItsAddress is the torn-pair
// detection itself.
//
// The object is corrupted through the S3 API — a second version written at
// the digest key — rather than by editing MinIO's on-disk files, whose
// layout is erasure-coded metadata and not the object body. Editing those
// would test the storage backend's internals and would not produce the
// state this is about.
//
// Existence is not the check. The row is there, the object is there, the
// sizes agree; only the CONTENT disagrees with the digest addressing it, and
// nothing short of recomputing the hash over the whole stream can tell.
func TestVerifyDetectsAnObjectThatDoesNotMatchItsAddress(t *testing.T) {
	f := evidenceFixture(t)
	ctx := context.Background()

	result := f.acceptedOriginal(t)
	attachment := result.Attachments[0]
	key := objectKeyFor(f.organizationID, attachment.Digest)

	// The key is pinned before it is used: an object written somewhere the
	// seam does not read would leave the original intact and the pass
	// healthy, and this test would report a detection that never happened.
	versions, err := f.blob.ListVersions(ctx, key)
	if err != nil {
		t.Fatalf("list versions at %s: %v", key, err)
	}
	if len(versions) == 0 {
		t.Fatalf("nothing is stored at %s, so this test would corrupt nothing", key)
	}

	corrupt := []byte("these bytes do not hash to the digest that addresses them")
	if _, err := f.blob.PutStaged(ctx, key, int64(len(corrupt)), bytes.NewReader(corrupt)); err != nil {
		t.Fatalf("overwrite the object at %s: %v", key, err)
	}

	report, err := f.store.Verify(ctx)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if report.Healthy() {
		t.Fatal("verify passed a plane whose object does not hash to its own address: " +
			"this is the torn pair the whole pass exists to find")
	}
	if report.Attachments != 1 {
		t.Errorf("verify read %d attachments, want 1", report.Attachments)
	}

	// NAMED, not merely counted. An operator who cannot tell which
	// attachment is damaged has been told the plane is broken and nothing
	// they can act on.
	var named bool
	for _, problem := range report.Problems {
		if problem.ID == attachment.AttachmentID && problem.OrganizationID == f.organizationID {
			named = true
		}
	}
	if !named {
		t.Errorf("verify reported %+v, none of which names attachment %s",
			report.Problems, attachment.AttachmentID)
	}
}

// TestVerifyReportsSkipsRatherThanCorruptionUnderTruncation is the
// false-positive half, and it is deterministic rather than raced.
//
// The scenario is ordinary healthy behaviour: attachment truncation removes
// an unpinned row that verify has already listed, and the object sweep then
// reclaims the object the row made unreachable. A verifier that read its
// listing and went looking for the bytes would find them gone and report
// corruption — about a plane doing exactly what it was designed to do.
//
// The window is forced open with the advisory lock verify itself takes per
// (organization, digest). Holding it stops verify between its committed
// listing and its recheck, which is precisely the interval the deletions
// have to land in. Racing two goroutines and hoping would leave the
// interesting case untested most runs, and passing.
func TestVerifyReportsSkipsRatherThanCorruptionUnderTruncation(t *testing.T) {
	f := evidenceFixture(t)
	ctx := context.Background()

	// Two attachments with different fates. The pinned one must still be
	// verified — a pass that skipped everything would satisfy a bare
	// "no problems" assertion.
	pinned := f.acceptedOriginal(t)
	doomed, err := f.store.PutAttachment(ctx, putInput(f.organizationID, []byte("unpinned, and about to go")))
	if err != nil {
		t.Fatalf("PutAttachment: %v", err)
	}

	// Aged past the horizon, which is what makes it a truncation candidate
	// at all. Only the unpinned row is aged: the pinned one is retained by
	// its pin rather than by its age, and ageing both would leave which
	// rule protected it ambiguous.
	if _, err := f.pool.Exec(ctx,
		`UPDATE binary_attachments SET created_at = now() - interval '30 days' WHERE attachment_id = $1`,
		doomed.AttachmentID); err != nil {
		t.Fatalf("age the doomed attachment: %v", err)
	}

	doomedKey := objectKeyFor(f.organizationID, doomed.Digest)
	versions, err := f.blob.ListVersions(ctx, doomedKey)
	if err != nil {
		t.Fatalf("list versions at %s: %v", doomedKey, err)
	}
	if len(versions) == 0 {
		t.Fatalf("nothing is stored at %s, so the object deletion below would remove nothing and "+
			"this test would never present verify with a missing object", doomedKey)
	}

	// Hold the lock verify will want for the doomed digest.
	holder, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the lock holder: %v", err)
	}
	defer func() { _ = holder.Rollback(ctx) }()
	if _, err := holder.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`,
		digestLockKeyFor(f.organizationID, doomed.Digest)); err != nil {
		t.Fatalf("take the digest lock: %v", err)
	}

	type outcome struct {
		report store.VerifyReport
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		report, verifyErr := f.store.Verify(context.Background())
		done <- outcome{report: report, err: verifyErr}
	}()

	// Wait for verify to be BLOCKED, not merely started. Before it blocks,
	// its listing may not have been taken, and a deletion landing then would
	// simply never be listed -- producing a pass with nothing to skip and an
	// assertion that proves nothing.
	waitForAdvisoryWaiter(t, f)

	// Now the two deletions, in the order the design has them: the row goes
	// first, which makes the object unreachable, and only then is the object
	// itself reclaimed.
	if _, err := f.store.TruncateAuditBefore(ctx, f.organizationID, horizon()); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	// Deleted directly rather than through DeleteUnpinned, which would take
	// the very lock this test is holding. What matters to verify is that the
	// object is gone; which actor removed it is the sweep's own suite's
	// business.
	for _, version := range versions {
		if err := f.blob.DeleteVersion(ctx, doomedKey, version.VersionID); err != nil {
			t.Fatalf("delete the reclaimed object: %v", err)
		}
	}

	if err := holder.Rollback(ctx); err != nil {
		t.Fatalf("release the digest lock: %v", err)
	}

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("Verify: %v", got.err)
		}
		if !got.report.Healthy() {
			t.Fatalf("verify reported %d problem(s) about a plane where truncation and the sweep "+
				"did exactly what they are designed to do: %+v", len(got.report.Problems), got.report.Problems)
		}
		if got.report.Skipped != 1 {
			t.Errorf("verify skipped %d attachments, want exactly the truncated one: without the "+
				"recheck under the lock, the deleted row's object is simply missing and the pass "+
				"reports corruption", got.report.Skipped)
		}
		// The skip is subtracted from the count, so what remains is what was
		// actually read. A pass that skipped both would also report no
		// problems.
		if got.report.Attachments != 1 {
			t.Errorf("verify read %d attachments, want the one pinned row it should still have "+
				"checked (%s)", got.report.Attachments, pinned.Attachments[0].AttachmentID)
		}
	case <-time.After(2 * time.Minute):
		t.Fatal("verify did not return after the digest lock was released")
	}
}

// waitForAdvisoryWaiter blocks until something in this database is waiting
// on an advisory lock.
//
// The database filter is what keeps this specific: every test here runs
// against a disposable database of its own, so the only advisory lock
// contention visible under it is this test's.
//
// A timeout is a hard failure and says why: the most likely cause is that
// the lock key computed above no longer matches the seam's, in which case
// verify never blocks, the deletions land wherever they land, and every
// assertion below becomes a coin toss that usually reads as a pass.
func waitForAdvisoryWaiter(t *testing.T, f *fixture) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		var waiters int
		if err := f.pool.QueryRow(ctx, `
			SELECT count(*) FROM pg_locks
			WHERE locktype = 'advisory' AND NOT granted
			  AND database = (SELECT oid FROM pg_database WHERE datname = current_database())
		`).Scan(&waiters); err != nil {
			t.Fatalf("read pg_locks: %v", err)
		}
		if waiters > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("nothing ever blocked on the advisory lock this test holds: verify was never stopped " +
		"between its listing and its recheck, so the window the deletions must land in was never open. " +
		"The likeliest cause is that the lock key reproduced here no longer matches the seam's")
}
