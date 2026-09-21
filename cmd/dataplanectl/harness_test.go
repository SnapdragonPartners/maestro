package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"orchestrator/internal/dataplane/harness"
	"orchestrator/internal/dataplane/stack"
	"orchestrator/pkg/version"
)

// stampVersion sets this binary's version for one test.
func stampVersion(t *testing.T, stamp string) {
	t.Helper()
	previous := version.Version
	version.Version = stamp
	t.Cleanup(func() { version.Version = previous })
}

// TestAMisStampedBinaryOpensNoSeam: both of this command's composition roots
// cross harness.Parse, and cross it BEFORE the plane is touched (Phase 3 item
// 4 design, D3 and D8). "2.0.0" is the design's named case -- a release built
// without its v -- and the thing it must never become is the development
// exception, which would bypass every declared range in the plane.
//
// "Before the plane" is asserted on the data root: opening a local seam
// creates the root and its lifecycle lock first thing, so neither may exist.
func TestAMisStampedBinaryOpensNoSeam(t *testing.T) {
	for _, stamp := range []string{"2.0.0", "", "v2", "devel"} {
		t.Run("stamp="+stamp, func(t *testing.T) {
			stampVersion(t, stamp)
			cfg := scratchPlane(t)
			lockPath := filepath.Join(cfg.Roots.Data, stack.LifecycleLockFile)

			if _, err := orchestratorOpener(cfg); !errors.Is(err, harness.ErrMalformedVersion) {
				t.Errorf("orchestratorOpener() = %v, want ErrMalformedVersion", err)
			}
			if _, err := openSeam(context.Background(), cfg); !errors.Is(err, harness.ErrMalformedVersion) {
				t.Errorf("openSeam() = %v, want ErrMalformedVersion", err)
			}
			if _, err := os.Stat(lockPath); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("the lifecycle lock exists (%v): a seam was reached under a malformed version", err)
			}
		})
	}
}

// TestBothAdmittedStampsCompose is the control: the refusals above are about
// the stamp, not about these roots refusing everything.
func TestBothAdmittedStampsCompose(t *testing.T) {
	for _, stamp := range []string{harness.Development, "v2.0.0-phase.3.0.0"} {
		stampVersion(t, stamp)
		running, err := runningHarness()
		if err != nil || running.String() != stamp {
			t.Errorf("runningHarness() under %q = %q, %v", stamp, running, err)
		}
		if _, err := orchestratorOpener(scratchPlane(t)); err != nil {
			t.Errorf("orchestratorOpener() under %q: %v", stamp, err)
		}
	}
}
