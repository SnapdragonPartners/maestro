package stack

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"orchestrator/internal/dataplane/paths"
)

// testConfig builds a Config rooted in a temporary MAESTRO_HOME.
func testConfig(t *testing.T) *Config {
	t.Helper()
	t.Setenv(paths.HomeEnv, t.TempDir())

	roots, err := paths.Resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	cfg, err := NewConfig(roots)
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	return cfg
}

// Every lifecycle operation must take the lifecycle lock, and must take it
// before doing anything else — a `reset` that deletes service data while
// another process is midway through initdb is the failure this prevents.
//
// The assertion is on BLOCKING, not on outcome: each operation is expected
// to fail afterwards (the compose file is bogus and Docker may be absent),
// which is fine and deliberately irrelevant. What matters is that it does
// not proceed while the lock is held elsewhere. This is the ADR 0027 rule
// that a lock needs a test which fails without it — removing the
// lockLifecycle call from any of the three makes its case return
// immediately, and the case fails.
func TestLifecycleOperationsTakeTheLock(t *testing.T) {
	operations := map[string]func(context.Context, *Config, string) error{
		"Up":    Up,
		"Down":  Down,
		"Reset": Reset,
	}

	for name, operate := range operations {
		t.Run(name, func(t *testing.T) {
			cfg := testConfig(t)

			// Hold the lock the way a concurrent process would.
			if err := cfg.Roots.Ensure(); err != nil {
				t.Fatalf("Ensure: %v", err)
			}
			release, err := paths.AcquireLock(filepath.Join(cfg.Roots.Data, LifecycleLockFile))
			if err != nil {
				t.Fatalf("AcquireLock: %v", err)
			}

			done := make(chan error, 1)
			go func() {
				// A path that cannot resolve, so the operation fails fast
				// once it does get the lock. We never inspect the error.
				done <- operate(context.Background(), cfg, "testdata/does-not-exist.yaml")
			}()

			select {
			case <-done:
				_ = release()
				t.Fatal("operation completed while the lifecycle lock was held: it is not taking the lock")
			case <-time.After(250 * time.Millisecond):
			}

			if relErr := release(); relErr != nil {
				t.Fatalf("release: %v", relErr)
			}

			select {
			case <-done:
			case <-time.After(30 * time.Second):
				t.Fatal("operation did not proceed after the lifecycle lock was released")
			}
		})
	}
}
