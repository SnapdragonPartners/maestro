package stack

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// The matrix has to cover every operation, and this is what makes that
// structural rather than remembered.
//
// The failure it prevents is an operation added later — a new verb, or one
// split out of an existing one — that nobody adds to markerPermits and that
// therefore silently becomes permitted against a torn tree. Reviewing for
// that is exactly the kind of thing review is bad at: the omission is
// invisible in the diff that causes it, because the diff is somewhere else.
func TestEveryLifecycleHasAMarkerPolicy(t *testing.T) {
	for _, operation := range lifecycles {
		if _, defined := markerPermits[operation]; !defined {
			t.Errorf("%s has no entry in markerPermits: decide whether it may run against a torn restore", operation)
		}
	}
	if len(markerPermits) != len(lifecycles) {
		t.Errorf("markerPermits has %d entries for %d operations: one of them names something that is not an operation",
			len(markerPermits), len(lifecycles))
	}
}

// An operation with no policy at all must refuse rather than proceed.
// Unreachable while the test above passes, which is the point: the two
// together mean a forgotten operation fails loudly in tests and safely in
// production.
func TestUnknownLifecycleIsRefused(t *testing.T) {
	cfg := planeAt(t)
	mustWrite(t, markerPath(cfg), []byte("{}"))

	err := guardRestoreMarker(cfg, lifecycle(len(lifecycles)+1))
	if !errors.Is(err, ErrRestoreIncomplete) {
		t.Errorf("err = %v, want a refusal for an operation with no policy", err)
	}
}

// Each verb, against a marked root, either refuses or is one of the three
// documented ways to make progress.
func TestMarkerGatesEveryOperation(t *testing.T) {
	permitted := map[lifecycle]bool{lifecycleDown: true, lifecycleRestore: true, lifecycleReset: true}

	for _, operation := range lifecycles {
		t.Run(operation.String(), func(t *testing.T) {
			cfg := planeAt(t)
			mustWrite(t, markerPath(cfg), []byte("{}"))

			err := guardRestoreMarker(cfg, operation)
			switch {
			case permitted[operation] && err != nil:
				t.Errorf("%s refused, but it is one of the ways out of a torn restore: %v", operation, err)
			case !permitted[operation] && !errors.Is(err, ErrRestoreIncomplete):
				t.Errorf("%s was allowed to act on a torn restore (err = %v)", operation, err)
			}
		})
	}
}

// Without the marker every operation proceeds, so the guard cannot be
// passing for the trivial reason that it refuses everything.
func TestMarkerAbsentPermitsEveryOperation(t *testing.T) {
	cfg := planeAt(t)
	for _, operation := range lifecycles {
		if err := guardRestoreMarker(cfg, operation); err != nil {
			t.Errorf("%s refused on an unmarked root: %v", operation, err)
		}
	}
}

// The refusal has to name the marker and both ways out; an operator meeting
// this error is by definition mid-incident.
func TestMarkerRefusalIsActionable(t *testing.T) {
	cfg := planeAt(t)
	mustWrite(t, markerPath(cfg), []byte("{}"))

	err := guardRestoreMarker(cfg, lifecycleUp)
	if err == nil {
		t.Fatal("want a refusal")
	}
	for _, want := range []string{RestoreIncompleteMarker, "dataplane-restore", "dataplane-reset"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not mention %q: %v", want, err)
		}
	}
}

// Reset clears the marker, which is what makes it one of the ways out
// rather than an operation that merely tolerates the state.
func TestResetClearsTheMarker(t *testing.T) {
	cfg := planeAt(t)
	mustWrite(t, markerPath(cfg), []byte("{}"))

	if err := clearDataRoot(cfg); err != nil {
		t.Fatalf("clearDataRoot: %v", err)
	}
	if _, err := os.Stat(markerPath(cfg)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("marker survived reset (err = %v); the next up would refuse a plane just wiped", err)
	}
}

// The marker is fsynced before the first deletion, so the state survives
// the crash it exists to describe. Durability itself cannot be asserted
// without pulling power; what is asserted here is that the marker exists
// and is readable at the moment writeRestoreMarker returns, and the fsync
// beside it is stated in the code rather than implied by this test.
func TestWriteRestoreMarkerIsReadableImmediately(t *testing.T) {
	cfg := planeAt(t)
	if err := writeRestoreMarker(cfg); err != nil {
		t.Fatalf("writeRestoreMarker: %v", err)
	}
	body, err := os.ReadFile(markerPath(cfg))
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if len(body) == 0 {
		t.Error("marker is empty; it should say what it means for whoever finds it")
	}
	if err := clearRestoreMarker(cfg); err != nil {
		t.Fatalf("clearRestoreMarker: %v", err)
	}
	if _, err := os.Stat(markerPath(cfg)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("marker survived clearing: %v", err)
	}
}
