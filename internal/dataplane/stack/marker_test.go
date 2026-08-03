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

// The pending-verification marker is a SECOND state with a second policy
// table, and it needs the same structural completeness guarantee. An
// operation added later that nobody adds here would silently become
// permitted to act on a plane nothing has ever checked.
func TestEveryLifecycleHasAnUnverifiedPolicy(t *testing.T) {
	for _, operation := range lifecycles {
		if _, defined := unverifiedPermits[operation]; !defined {
			t.Errorf("%s has no entry in unverifiedPermits: decide whether it may run against a plane "+
				"that owes a verification pass", operation)
		}
	}
	if len(unverifiedPermits) != len(lifecycles) {
		t.Errorf("unverifiedPermits has %d entries for %d operations: one of them names something that "+
			"is not an operation", len(unverifiedPermits), len(lifecycles))
	}
}

// The two tables answer differently, and this is the assertion that says so.
//
// A single table would have been simpler and wrong: `up` must refuse a torn
// tree and must PROCEED against an unverified one, because proceeding is how
// the verification happens at all. If the two states ever collapsed into one
// policy, the two-part restore could never be completed -- the plane would
// refuse the only operation that can settle its debt.
//
// `verify` is on the refused side here, which is the correction Codex's
// review forced. Settlement is a pass plus its consequences, the exported
// Verify delivers neither, and a verb that reported "healthy" while leaving
// the debt outstanding would be the most convincing possible way to tell an
// operator the problem is gone.
func TestUnverifiedMarkerGatesEveryOperation(t *testing.T) {
	permitted := map[lifecycle]bool{
		lifecycleUp:   true,
		lifecycleDown: true, lifecycleRestore: true, lifecycleReset: true,
		// recover-key is a COMPOUND settlement path, not an exemption: it
		// ends by calling `up` internally and so reaches the identical
		// verification. Refusing it made ADR 0022's second restore branch
		// unreachable from the situation it exists for -- a keyless restore
		// records this debt, and recovery is how such a plane is opened.
		lifecycleRecoverKey: true,
	}

	for _, operation := range lifecycles {
		t.Run(operation.String(), func(t *testing.T) {
			cfg := planeAt(t)
			if err := markRestoreUnverified(cfg); err != nil {
				t.Fatalf("markRestoreUnverified: %v", err)
			}

			err := guardRestoreState(cfg, operation)
			switch {
			case permitted[operation] && err != nil:
				t.Errorf("%s refused against a plane owing verification, but it is one of the "+
					"operations that must proceed: %v", operation, err)
			case !permitted[operation] && !errors.Is(err, ErrRestoreUnverifiedPending):
				t.Errorf("%s was allowed to act on a plane nothing has ever checked (err = %v)",
					operation, err)
			}
		})
	}
}

// Without the marker every operation proceeds, so the guard is not passing
// for the trivial reason that it refuses everything.
func TestUnverifiedMarkerAbsentPermitsEveryOperation(t *testing.T) {
	cfg := planeAt(t)
	for _, operation := range lifecycles {
		if err := guardRestoreState(cfg, operation); err != nil {
			t.Errorf("%s refused on a plane owing nothing: %v", operation, err)
		}
	}
}

// An operation with no pending-verification policy refuses rather than
// proceeds. Unreachable while the completeness test passes, which is the
// point.
func TestUnknownLifecycleIsRefusedByTheUnverifiedGuard(t *testing.T) {
	cfg := planeAt(t)
	if err := markRestoreUnverified(cfg); err != nil {
		t.Fatalf("markRestoreUnverified: %v", err)
	}

	err := guardUnverifiedMarker(cfg, lifecycle(len(lifecycles)+1))
	if !errors.Is(err, ErrRestoreUnverifiedPending) {
		t.Errorf("err = %v, want a refusal for an operation with no policy", err)
	}
}

// The refusal names the marker and a way out, like the torn one does. An
// operator meeting it has a plane that will not back up and needs to be told
// what will make it.
func TestUnverifiedRefusalIsActionable(t *testing.T) {
	cfg := planeAt(t)
	if err := markRestoreUnverified(cfg); err != nil {
		t.Fatalf("markRestoreUnverified: %v", err)
	}

	err := guardRestoreState(cfg, lifecycleBackup)
	if err == nil {
		t.Fatal("want a refusal")
	}
	for _, want := range []string{RestoreUnverifiedMarker, "dataplane-up", "dataplane-reset"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not mention %q: %v", want, err)
		}
	}
}

// Reset clears the debt along with the plane it was about. It is swept by
// the ordinary whole-root clear rather than by a special case, and this is
// what says so -- a plane that has been discarded owes nothing about
// contents that are gone.
func TestResetClearsTheVerificationDebt(t *testing.T) {
	cfg := planeAt(t)
	if err := markRestoreUnverified(cfg); err != nil {
		t.Fatalf("markRestoreUnverified: %v", err)
	}

	if err := clearDataRoot(cfg); err != nil {
		t.Fatalf("clearDataRoot: %v", err)
	}
	owed, err := restoreOwesVerification(cfg)
	if err != nil {
		t.Fatalf("restoreOwesVerification: %v", err)
	}
	if owed {
		t.Error("the debt survived reset; the next backup would refuse a plane just wiped")
	}
}

// The interrupted-recovery marker is a THIRD state with a third policy
// table, and it needs the same structural completeness guarantee as the
// other two.
func TestEveryLifecycleHasARecoveryPolicy(t *testing.T) {
	for _, operation := range lifecycles {
		if _, defined := recoveryPermits[operation]; !defined {
			t.Errorf("%s has no entry in recoveryPermits: decide whether it may run against a plane "+
				"whose recovery was interrupted, and whose isolated postmaster may still hold "+
				"PGDATA open", operation)
		}
	}
	if len(recoveryPermits) != len(lifecycles) {
		t.Errorf("recoveryPermits has %d entries for %d operations: one of them names something "+
			"that is not an operation", len(recoveryPermits), len(lifecycles))
	}
}

// The gate itself. `up` is on the REFUSED side here and permitted by the
// other two tables, which is the asymmetry worth asserting: an unverified
// plane must be started because starting it is how it gets verified, while a
// plane with an orphaned postmaster must not be started at all, because
// starting it puts a second postmaster over the same cluster.
func TestRecoveryMarkerGatesEveryOperation(t *testing.T) {
	permitted := map[lifecycle]bool{
		lifecycleRecoverKey: true, lifecycleDown: true,
		lifecycleReset: true, lifecycleRestore: true,
	}

	for _, operation := range lifecycles {
		t.Run(operation.String(), func(t *testing.T) {
			cfg := planeAt(t)
			if err := writeRecoveryMarker(cfg, recoveryMarker{
				Container: recoveryContainerName(cfg),
				StagedKey: stagedKeyPath(cfg),
			}); err != nil {
				t.Fatalf("writeRecoveryMarker: %v", err)
			}

			err := guardRestoreState(cfg, operation)
			switch {
			case permitted[operation] && err != nil:
				t.Errorf("%s refused against an interrupted recovery, but it is one of the ways "+
					"out of one: %v", operation, err)
			case !permitted[operation] && !errors.Is(err, ErrRecoveryInterrupted):
				t.Errorf("%s was allowed to act on a plane whose recovery container may still own "+
					"PGDATA (err = %v)", operation, err)
			}
		})
	}
}

// Without the marker every operation proceeds, so this guard is not passing
// for the trivial reason that it refuses everything.
func TestRecoveryMarkerAbsentPermitsEveryOperation(t *testing.T) {
	cfg := planeAt(t)
	for _, operation := range lifecycles {
		if err := guardRestoreState(cfg, operation); err != nil {
			t.Errorf("%s refused on a plane with no interrupted recovery: %v", operation, err)
		}
	}
}

// An operation with no interrupted-recovery policy refuses rather than
// proceeds.
func TestUnknownLifecycleIsRefusedByTheRecoveryGuard(t *testing.T) {
	cfg := planeAt(t)
	if err := writeRecoveryMarker(cfg, recoveryMarker{
		Container: recoveryContainerName(cfg),
		StagedKey: stagedKeyPath(cfg),
	}); err != nil {
		t.Fatalf("writeRecoveryMarker: %v", err)
	}
	if err := guardRecoveryMarker(cfg, lifecycle(len(lifecycles)+1)); !errors.Is(err, ErrRecoveryInterrupted) {
		t.Errorf("err = %v, want a refusal for an operation with no policy", err)
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
