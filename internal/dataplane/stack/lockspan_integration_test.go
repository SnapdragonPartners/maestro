//go:build integration

package stack

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"orchestrator/internal/dataplane/registry"
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

// TestDownBlocksForTheWholeOfAnOpenSeam is the same question asked of
// ordinary USE rather than of a lifecycle operation.
//
// OpenSeam checks the restore and recovery markers, and an earlier version
// then operated with nothing held. That is a TOCTOU: the check reports what
// was true a moment ago, and a reset or a restore is free to take the
// exclusive lock immediately afterwards and begin deleting the data root
// under a live import.
//
// The lock is now taken SHARED and held until the store is closed, and the
// duration is the property -- exactly as it is for a restore. The unit tests
// can show that OpenSeam blocks while an exclusive holder exists, and that a
// sharedSeam releases on Close. Neither can show that the seam RETURNED by
// OpenSeam still holds it, which is the half that protects the import: a
// version releasing the lock as soon as the store was built passes both and
// leaves the import it was taken for entirely unprotected.
func TestDownBlocksForTheWholeOfAnOpenSeam(t *testing.T) {
	cfg := isolatedPlane(t)
	if err := Up(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Up: %v", err)
	}
	// Empty, like every other lifecycle caller's: this seam is opened to
	// hold a lock, not to read a payload.
	types, err := registry.New(nil)
	if err != nil {
		t.Fatalf("build registry: %v", err)
	}
	seam, err := OpenSeam(t.Context(), cfg, types)
	if err != nil {
		t.Fatalf("OpenSeam: %v", err)
	}

	type finish struct {
		at  time.Time
		err error
	}
	downDone := make(chan finish, 1)
	go func() {
		err := Down(context.WithoutCancel(t.Context()), cfg, testComposeFile())
		downDone <- finish{at: time.Now(), err: err}
	}()

	// It must NOT finish while the seam is open. A generous wait, because
	// what is being asserted is that nothing happens: too short a one would
	// pass against a `down` that was merely slow to start.
	select {
	case done := <-downDone:
		seam.Close()
		t.Fatalf("Down finished at %s while a seam was open (err=%v): a reset or a restore could "+
			"delete the data root under a live import",
			done.at.Format(time.RFC3339Nano), done.err)
	case <-time.After(5 * time.Second):
	}

	closedAt := time.Now()
	seam.Close()

	select {
	case done := <-downDone:
		if done.err != nil {
			t.Fatalf("Down: %v", done.err)
		}
		if !done.at.After(closedAt) {
			t.Errorf("Down finished at %s, before the seam was closed at %s",
				done.at.Format(time.RFC3339Nano), closedAt.Format(time.RFC3339Nano))
		}
	case <-time.After(2 * time.Minute):
		t.Fatal("Down never completed after the seam was closed; the shared lock was not released")
	}
}
