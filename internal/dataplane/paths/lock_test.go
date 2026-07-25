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

	release, err := acquireLock(lockPath)
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

	release, err := acquireLock(filepath.Join(root, lockFileName))
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
