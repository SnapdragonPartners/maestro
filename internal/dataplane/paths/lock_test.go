package paths

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// The lock's contract is exclusion, so assert exclusion directly rather
// than inferring it from observed interleaving. A timing-based concurrency
// test cannot stand in for this: TestEnsureKeyConcurrent passes with the
// lock removed entirely, because the hard-link protocol already guarantees
// a single final key. ADR 0027 requires a test that fails without the lock.
//
// No subprocess is needed to prove the cross-process guarantee. flock is
// associated with the open file description, not the process, so a second
// independent descriptor contends through exactly the same kernel path
// another process would use. (POSIX fcntl locks are per-process and would
// require a subprocess; flock is not.)
func TestAcquireLockExcludesSecondHolder(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), lockFileName)

	release, err := acquireLock(lockPath, syscall.LOCK_EX)
	if err != nil {
		t.Fatalf("acquireLock: %v", err)
	}

	second, err := os.OpenFile(lockPath, os.O_RDWR, lockPerm)
	if err != nil {
		t.Fatalf("open lock independently: %v", err)
	}
	defer func() { _ = second.Close() }()

	err = syscall.Flock(int(second.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if !errors.Is(err, syscall.EWOULDBLOCK) {
		t.Fatalf("second holder acquired the lock (err=%v), want EWOULDBLOCK", err)
	}

	if relErr := release(); relErr != nil {
		t.Fatalf("release: %v", relErr)
	}

	// The lock must be genuinely released, not merely dropped on close.
	if err := syscall.Flock(int(second.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("second holder still blocked after release: %v", err)
	}
	if err := syscall.Flock(int(second.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatalf("unlock second holder: %v", err)
	}
}

// Proving the lock excludes is only half of it: EnsureKey must actually
// take it. This fails loudly if EnsureKey stops doing so, because it would
// then complete while the lock is held elsewhere.
func TestEnsureKeyWaitsForTheLock(t *testing.T) {
	root := t.TempDir()

	release, err := acquireLock(filepath.Join(root, lockFileName), syscall.LOCK_EX)
	if err != nil {
		t.Fatalf("acquireLock: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, keyErr := EnsureKey(root)
		done <- keyErr
	}()

	// Completing here means EnsureKey never contended for the lock. The
	// window only has to be long enough for an unlocked EnsureKey to
	// finish, which is a few filesystem operations; a slow machine makes
	// this test weaker, never flaky, since the failure is "completed too
	// early" rather than "did not complete in time".
	select {
	case keyErr := <-done:
		t.Fatalf("EnsureKey completed while the lock was held (err=%v): it is not taking the lock", keyErr)
	case <-time.After(200 * time.Millisecond):
	}

	if relErr := release(); relErr != nil {
		t.Fatalf("release: %v", relErr)
	}

	select {
	case keyErr := <-done:
		if keyErr != nil {
			t.Fatalf("EnsureKey after release: %v", keyErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("EnsureKey did not complete after the lock was released")
	}
}

// The shared lock's contract is BOTH halves: several holders coexist, and
// every one of them excludes an exclusive holder.
//
// Asserted directly, in both directions, because the two halves fail
// differently and only together do they say what "shared use versus
// exclusive lifecycle" means. A shared lock that excluded its own kind
// would serialize every import; one that admitted an exclusive holder would
// let a reset delete the data root under a live one.
func TestSharedLockAdmitsItsOwnKindAndExcludesExclusive(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), lockFileName)

	first, err := acquireLock(lockPath, syscall.LOCK_SH)
	if err != nil {
		t.Fatalf("acquire shared: %v", err)
	}

	// A second shared holder proceeds. Two imports have no reason to queue.
	second, err := os.OpenFile(lockPath, os.O_RDWR, lockPerm)
	if err != nil {
		t.Fatalf("open lock independently: %v", err)
	}
	defer func() { _ = second.Close() }()
	if shErr := syscall.Flock(int(second.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); shErr != nil {
		t.Fatalf("a second shared holder was excluded: %v", shErr)
	}

	// An exclusive holder does not. This is the whole of what the marker
	// guard could not do on its own: a preflight check reports what was
	// true a moment ago, and a held lock keeps it true.
	exclusive, openErr := os.OpenFile(lockPath, os.O_RDWR, lockPerm)
	if openErr != nil {
		t.Fatalf("open lock independently: %v", openErr)
	}
	defer func() { _ = exclusive.Close() }()
	err = syscall.Flock(int(exclusive.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if !errors.Is(err, syscall.EWOULDBLOCK) {
		t.Fatalf("an exclusive holder acquired the lock beside a shared one (err=%v), want "+
			"EWOULDBLOCK: a reset could delete the data root under a live import", err)
	}

	// And it is still excluded while ANY shared holder remains, which is
	// what makes the lock's lifetime the seam's rather than the call's.
	if relErr := first(); relErr != nil {
		t.Fatalf("release the first shared holder: %v", relErr)
	}
	err = syscall.Flock(int(exclusive.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if !errors.Is(err, syscall.EWOULDBLOCK) {
		t.Fatalf("an exclusive holder acquired the lock while a shared holder remained (err=%v)", err)
	}

	if err := syscall.Flock(int(second.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatalf("release the second shared holder: %v", err)
	}
	if err := syscall.Flock(int(exclusive.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("the exclusive holder is still blocked after every shared holder released: %v", err)
	}
	if err := syscall.Flock(int(exclusive.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatalf("unlock: %v", err)
	}
}
