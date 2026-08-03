//go:build integration

package stack

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// TestResetBlocksForTheWHOLEOfARestore is test-plan item 7, and the word
// doing the work is "whole".
//
// That every verb takes the lock is asserted without Docker, one operation
// at a time. What that cannot show is how LONG a verb holds it, and the
// duration is the property ADR 0027 actually needs: round 1 of this design
// released the lock after the copy and restarted the plane outside it,
// leaving a window in which a concurrent `reset` deletes service data while
// `up` is midway through starting Postgres against it.
//
// A restore is stop → clear → copy → up → verify. The failure mode is
// invisible to any test that only checks the copy phase, because the copy
// phase is correctly protected in both the right and the wrong version. So
// this asserts on ORDER: the `reset` may not complete until the restore has
// returned, restart and verification included.
func TestResetBlocksForTheWholeOfARestore(t *testing.T) {
	cfg := isolatedPlane(t)
	if err := Up(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Up: %v", err)
	}
	seedCrossStore(t, cfg)

	archive := filepath.Join(t.TempDir(), "archive")
	if err := Backup(t.Context(), cfg, testComposeFile(), archive); err != nil {
		t.Fatalf("Backup: %v", err)
	}

	type finish struct {
		at  time.Time
		err error
	}
	restoreDone := make(chan finish, 1)
	go func() {
		err := Restore(context.WithoutCancel(t.Context()), cfg, testComposeFile(), archive, true)
		restoreDone <- finish{at: time.Now(), err: err}
	}()

	// The restore must hold the lock BEFORE the reset asks for it, or this
	// measures the reverse race: reset first, restore blocked behind it,
	// and an assertion that passes for a restore holding no lock at all.
	// Services going down is the first externally visible evidence that the
	// restore is past its lock acquisition.
	deadline := time.Now().Add(2 * time.Minute)
	for len(runningServices(t, cfg)) > 0 {
		if time.Now().After(deadline) {
			t.Fatal("the restore never stopped the plane, so it cannot be shown to hold the lock")
		}
		time.Sleep(50 * time.Millisecond)
	}

	resetDone := make(chan finish, 1)
	go func() {
		err := Reset(context.WithoutCancel(t.Context()), cfg, testComposeFile())
		resetDone <- finish{at: time.Now(), err: err}
	}()

	restore := <-restoreDone
	if restore.err != nil {
		t.Fatalf("Restore: %v", restore.err)
	}
	reset := <-resetDone
	if reset.err != nil {
		t.Fatalf("Reset: %v", reset.err)
	}

	// The whole assertion. If the lock were released after the copy, the
	// reset would run during `up` and finish first -- while deleting the
	// data directory that `up` was starting Postgres against.
	if !reset.at.After(restore.at) {
		t.Errorf("the concurrent reset finished at %s, before the restore returned at %s: the "+
			"lifecycle lock does not span the restore's restart and verification, so a reset can "+
			"delete service data out from under a starting plane",
			reset.at.Format(time.RFC3339Nano), restore.at.Format(time.RFC3339Nano))
	}
}
