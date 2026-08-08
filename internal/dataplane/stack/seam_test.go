package stack

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"orchestrator/internal/dataplane/paths"
	"orchestrator/internal/dataplane/registry"
	"orchestrator/internal/dataplane/store"
)

// closeOnlyStore is a seam that can be closed and nothing else.
//
// The embedded interface is nil on purpose: every other method would panic
// if reached, which is the assertion that this double is used for exactly
// one thing. sharedSeam adds behaviour to Close and delegates the rest, so
// Close is the whole of its contract.
type closeOnlyStore struct {
	store.Store
	closed bool
}

func (s *closeOnlyStore) Close() { s.closed = true }

// The lock's lifetime is the SEAM's, not the call's.
//
// This is the half the blocking case cannot see: that test opens a seam and
// closes it immediately, so a version that released the lock as soon as the
// store was built would satisfy it — and would leave the import the lock was
// taken for entirely unprotected. What has to hold is that Close, and only
// Close, gives the lock back.
func TestSharedSeamReleasesTheLockWhenItIsClosed(t *testing.T) {
	released := false
	inner := &closeOnlyStore{}
	seam := &sharedSeam{Store: inner, release: func() error { released = true; return nil }}

	if released {
		t.Fatal("the lock was released before the seam was closed")
	}
	seam.Close()

	if !inner.closed {
		t.Error("closing the seam did not close the plane beneath it")
	}
	if !released {
		t.Error("closing the seam did not release the lifecycle lock; every later lifecycle " +
			"operation would block until this process exits")
	}
}

// A seam that fails to open must not leave a holder behind.
//
// The lock is taken before the guards and the plane, so every failure
// between there and the return has to give it back. One that did not would
// block every lifecycle operation on a lock nobody is using — and the
// failure it followed would look like the only problem.
func TestOpenSeamReleasesTheLockWhenItCannotOpen(t *testing.T) {
	cfg := testConfig(t)
	types, err := registry.New(nil)
	if err != nil {
		t.Fatalf("build registry: %v", err)
	}

	// An empty data root: no plane has been provisioned, so rootKeyFor
	// refuses. The failure is the point; what matters is what it leaves.
	if _, openErr := OpenSeam(context.Background(), cfg, types); openErr == nil {
		t.Fatal("OpenSeam succeeded against an empty data root")
	}

	lockPath := filepath.Join(cfg.Roots.Data, LifecycleLockFile)
	held, err := os.OpenFile(lockPath, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open the lock file: %v", err)
	}
	defer func() { _ = held.Close() }()
	if lockErr := syscall.Flock(int(held.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); lockErr != nil {
		if errors.Is(lockErr, syscall.EWOULDBLOCK) {
			t.Fatal("the lifecycle lock is still held after OpenSeam failed")
		}
		t.Fatalf("acquire the lock: %v", lockErr)
	}
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	// And the release is genuine rather than an artefact of this process
	// also being the one that took it.
	if _, err := paths.AcquireSharedLock(lockPath); err != nil {
		t.Fatalf("the lock cannot be taken again: %v", err)
	}
}
