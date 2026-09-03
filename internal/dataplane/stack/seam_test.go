package stack

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"orchestrator/internal/dataplane/configkeys"
	"orchestrator/internal/dataplane/paths"
	"orchestrator/internal/dataplane/registry"
)

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
	if _, openErr := OpenSeam(context.Background(), cfg, types, configkeys.MustNew(nil)); openErr == nil {
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
