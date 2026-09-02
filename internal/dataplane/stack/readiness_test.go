package stack

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/dataplane/configkeys"
	"orchestrator/internal/dataplane/readiness"
	"orchestrator/internal/dataplane/registry"
)

// TestEverySentinelIsClassifiedOrExcused is the two-table guard: every
// exported Err* in this package is either mapped to a readiness cause or
// listed as not reaching the use path, with a reason. Absence from both
// fails, so a sentinel added later cannot go unclassified by nobody
// noticing it. Presence in both fails too: the tables must not disagree.
//
// It is SECONDARY evidence. The AST says which names exist; it cannot say
// which reach OpenSeam or what to do about them. The behavioural tests
// below are the primary proof of the mapping.
func TestEverySentinelIsClassifiedOrExcused(t *testing.T) {
	mapped := map[string]bool{}
	for _, entry := range localCauses {
		mapped[entry.name] = true
	}

	fileSet := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fileSet, filepath.Join(".", name), nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", name, parseErr)
		}
		for _, decl := range file.Decls {
			gen, isGen := decl.(*ast.GenDecl)
			if !isGen || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				value, isValue := spec.(*ast.ValueSpec)
				if !isValue {
					continue
				}
				for _, ident := range value.Names {
					if strings.HasPrefix(ident.Name, "Err") && ident.IsExported() {
						found[ident.Name] = true
					}
				}
			}
		}
	}
	if len(found) == 0 {
		t.Fatal("no exported sentinels found; the parser is looking in the wrong place")
	}
	for name := range found {
		_, excused := notOnUsePath[name]
		switch {
		case !mapped[name] && !excused:
			t.Errorf("%s is neither mapped to a readiness cause nor excused as off the use path: decide", name)
		case mapped[name] && excused:
			t.Errorf("%s is both mapped and excused; the tables disagree", name)
		}
	}
	for name := range mapped {
		if !found[name] {
			t.Errorf("localCauses names %s, which is not an exported sentinel in this package", name)
		}
	}
	for name := range notOnUsePath {
		if !found[name] {
			t.Errorf("notOnUsePath names %s, which is not an exported sentinel in this package", name)
		}
	}
}

// TestLocalCausesNameTheirOwnSentinels: the table's name column and its
// value column agree, so a behavioural test that reads a cause is reading
// the sentinel it thinks it is.
func TestLocalCausesNameTheirOwnSentinels(t *testing.T) {
	byName := map[string]error{
		"ErrNoPlane":                  ErrNoPlane,
		"ErrPlaneLocked":              ErrPlaneLocked,
		"ErrRestoreIncomplete":        ErrRestoreIncomplete,
		"ErrRestoreUnverifiedPending": ErrRestoreUnverifiedPending,
		"ErrRecoveryInterrupted":      ErrRecoveryInterrupted,
	}
	for _, entry := range localCauses {
		if byName[entry.name] != entry.sentinel { //nolint:errorlint // identity is the point
			t.Errorf("localCauses row %s carries a different sentinel", entry.name)
		}
		wrapped := fmt.Errorf("outer: %w", entry.sentinel)
		cause, ok := readiness.CauseOf(classifyLocal(wrapped))
		if !ok || cause != entry.cause {
			t.Errorf("%s classifies as %q, want %q", entry.name, cause, entry.cause)
		}
	}
	plain := errors.New("an I/O failure")
	if classifyLocal(plain) != plain { //nolint:errorlint // identity is the point
		t.Error("an unmapped error was given a cause; it would send the operator to the wrong remedy")
	}
}

// openRefusal drives OpenSeam against a scratch plane and returns the
// refusal. The guards run before any DSN is built, so no service is needed
// to reach every marker and key state.
func openRefusal(t *testing.T, cfg *Config) error {
	t.Helper()
	types, err := registry.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	seam, err := OpenSeam(context.Background(), cfg, types, configkeys.MustNew(nil))
	if err == nil {
		seam.Close()
		t.Fatal("OpenSeam succeeded against a plane that is not ready")
	}
	return err
}

func expectRefusal(t *testing.T, err error, cause readiness.Cause, remedyFragment string, sentinel error) {
	t.Helper()
	got, ok := readiness.CauseOf(err)
	if !ok || got != cause {
		t.Fatalf("cause %q (%v), want %q: %v", got, ok, cause, err)
	}
	remedy, _ := readiness.RemedyOf(err)
	if !strings.Contains(remedy, remedyFragment) {
		t.Fatalf("remedy %q lacks %q", remedy, remedyFragment)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("the package's own sentinel is no longer in the chain: %v", err)
	}
}

// One behavioural test per local row of design D5. Each puts a scratch
// plane into the state and asserts cause AND remedy — the mutant that merges
// two rows fails on the remedy.

func TestOpenSeamRefusesNoPlane(t *testing.T) {
	cfg := planeAt(t)
	expectRefusal(t, openRefusal(t, cfg), readiness.NoPlane, "dataplane-up", ErrNoPlane)
}

func TestOpenSeamRefusesAMissingRootKey(t *testing.T) {
	cfg := planeAt(t)
	populate(t, cfg, "postgres")
	expectRefusal(t, openRefusal(t, cfg), readiness.RootKeyMissing, "recover-key", ErrPlaneLocked)
}

func TestOpenSeamRefusesAnIncompleteRestore(t *testing.T) {
	cfg := planeAt(t)
	if err := os.WriteFile(markerPath(cfg), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	expectRefusal(t, openRefusal(t, cfg), readiness.RestoreIncomplete, "dataplane-restore", ErrRestoreIncomplete)
}

func TestOpenSeamRefusesAnUnverifiedRestoreAndNamesUp(t *testing.T) {
	cfg := planeAt(t)
	if err := markRestoreUnverified(cfg); err != nil {
		t.Fatal(err)
	}
	// The remedy must name `up` specifically: it is the one verb that settles
	// the debt (Phase 2 D4a), and a categorical refusal would omit it.
	expectRefusal(t, openRefusal(t, cfg), readiness.RestoreUnverified, "dataplane-up", ErrRestoreUnverifiedPending)
}

func TestOpenSeamRefusesAnInterruptedRecoveryWithoutTouchingIt(t *testing.T) {
	cfg := planeAt(t)
	if err := os.WriteFile(recoveryMarkerPath(cfg), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	staged := stagedKeyPath(cfg)
	if err := os.WriteFile(staged, []byte("staged key material"), 0o600); err != nil {
		t.Fatal(err)
	}
	expectRefusal(t, openRefusal(t, cfg), readiness.RecoveryInterrupted, "recover-key", ErrRecoveryInterrupted)
	// Neither bypassed nor advanced: the marker and the staged key are
	// exactly as they were. THE MUTANT: make ordinary use clear the marker.
	for _, path := range []string{recoveryMarkerPath(cfg), staged} {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("ordinary startup disturbed the recovery protocol: %s is gone", filepath.Base(path))
		}
	}
}

func TestLocalizeProbeReplacesNeutralRemediesOnly(t *testing.T) {
	neutral := readiness.Refuse(readiness.SchemaBehind, "d", "apply the pending migrations", nil)
	remedy, _ := readiness.RemedyOf(localizeProbe(fmt.Errorf("w: %w", neutral)))
	if !strings.Contains(remedy, "dataplane-migrate") {
		t.Fatalf("local remedy %q does not name the command", remedy)
	}
	foreign := readiness.Refuse(readiness.NoPlane, "d", "keep", nil)
	if got, _ := readiness.RemedyOf(localizeProbe(foreign)); got != "keep" {
		t.Fatalf("a cause the probe does not produce had its remedy rewritten to %q", got)
	}
	plain := errors.New("plain")
	if localizeProbe(plain) != plain { //nolint:errorlint // identity is the point
		t.Fatal("an error with no cause was wrapped")
	}
}
