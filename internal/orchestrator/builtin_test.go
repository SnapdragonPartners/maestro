package orchestrator_test

import (
	"errors"
	"testing"
	"testing/fstest"

	"orchestrator/internal/dataplane/planetest"
	"orchestrator/internal/orchestrator"
	"orchestrator/internal/prompt"
)

// The production built-in, loaded through the same function the
// composition root calls, and judged by the same contract the seam holds
// (design D1, D2). What this proves: the embed is present, it is the empty
// pack the design says it is, it declares the Phase 3 band a tagged Phase 3
// binary satisfies, and the Orchestrator's own (empty) registry admits it.
// What it does not prove: anything about a pack with content. That is the
// fixture's job, below and in the integration tests.
func TestTheProductionBuiltinIsTheEmptyPackInThePhase3Band(t *testing.T) {
	builtin, err := orchestrator.LoadBuiltin(orchestrator.BuiltinPack())
	if err != nil {
		t.Fatalf("load the embedded built-in: %v", err)
	}
	switch {
	case len(builtin.Entries) != 0 || builtin.Entries == nil:
		t.Errorf("entries = %v; the item 4 built-in supplies none (design D1), as an empty non-nil map", builtin.Entries)
	case len(builtin.DeclaredRoles) != 0:
		t.Errorf("declared roles = %v; the item 4 built-in declares no coverage", builtin.DeclaredRoles)
	case builtin.DisplayName == "":
		t.Error("no display name")
	case builtin.MinMaestroVersion != "v2.0.0-phase.3.0.0" || builtin.MaxMaestroVersion != "v2.0.0-phase.4.0.0":
		t.Errorf("range [%s, %s), want the Phase 3 band", builtin.MinMaestroVersion, builtin.MaxMaestroVersion)
	}
	// InRange re-checks the declaration's own invariant first, so a
	// malformed range fails here with ErrMalformedRange rather than false.
	inRange, err := planetest.Harness(t).InRange(builtin.MinMaestroVersion, builtin.MaxMaestroVersion)
	if err != nil || !inRange {
		t.Fatalf("the test harness %s is not in the built-in's range (%v)", planetest.HarnessVersion, err)
	}
	if err := orchestrator.Prompts().ValidatePack(builtin.Entries, builtin.DeclaredRoles); err != nil {
		t.Fatalf("the Orchestrator's registry refuses its own built-in: %v", err)
	}
}

// TestLoadBuiltinCarriesContentFromAFixture is the non-vacuous half: the
// same function, over a file system with entries, yields them. Without this
// the previous test could pass against a loader that dropped every entry.
func TestLoadBuiltinCarriesContentFromAFixture(t *testing.T) {
	builtin, err := orchestrator.LoadBuiltin(fstest.MapFS{
		"pack.json":                 {Data: []byte(`{"display_name":"fixture","maestro_version":{"min":"v2.0.0-phase.3.0.0","max":"v2.0.0-phase.4.0.0"},"roles":["coder"]}`)},
		"entries/coder.system.tmpl": {Data: []byte("You are working on {{.Story}}.\n")},
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if builtin.Entries["coder.system"] != "You are working on {{.Story}}.\n" || len(builtin.DeclaredRoles) != 1 {
		t.Fatalf("loaded %+v", builtin)
	}
	// And the Orchestrator's registry refuses it, because it registers no
	// slot: the gate is the registry's, not the loader's, and an entry for
	// a slot the harness does not have is content nothing will render.
	if err := orchestrator.Prompts().ValidatePack(builtin.Entries, builtin.DeclaredRoles); !errors.Is(err, prompt.ErrUnknownSlot) {
		t.Fatalf("err = %v, want %v", err, prompt.ErrUnknownSlot)
	}
}

func TestLoadBuiltinReportsTheLoadersRefusal(t *testing.T) {
	_, err := orchestrator.LoadBuiltin(fstest.MapFS{})
	if !errors.Is(err, prompt.ErrLayout) {
		t.Fatalf("err = %v, want %v", err, prompt.ErrLayout)
	}
}
