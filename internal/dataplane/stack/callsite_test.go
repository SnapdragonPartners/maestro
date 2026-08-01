package stack

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// guardedVerbs maps each lifecycle entry point to the lifecycle constant it
// must guard itself with.
//
// The exported functions, not the internals: the lock-assuming forms are
// called from inside operations that have already made the decision, and
// requiring a guard there would forbid restore from verifying under its own
// marker.
//
//nolint:gochecknoglobals // Immutable expectation table for the test below.
var guardedVerbs = map[string]string{
	"Up":           "lifecycleUp",
	"Migrate":      "lifecycleMigrate",
	"ForceVersion": "lifecycleForceVersion",
	"Backup":       "lifecycleBackup",
	"Restore":      "lifecycleRestore",
	"Verify":       "lifecycleVerify",
}

// The marker policy is worthless if nothing consults it, and testing the
// policy helper cannot tell the difference.
//
// This is the same defect, one layer up, that mutation testing found in the
// restore tests: asserting a helper's behaviour proves nothing about whether
// its callers use it. Deleting the guardRestoreMarker call from Migrate
// leaves every table-driven test in marker_test.go green, because none of
// them calls Migrate. So the call sites are checked structurally, by parsing
// this package.
//
// Bidirectional on purpose. One direction catches a verb that stops
// guarding; the other catches a verb removed from the table but still in the
// package, which would silently shrink what is enforced.
func TestEveryLifecycleVerbGuardsOnTheMarker(t *testing.T) {
	fileSet := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}

	found := map[string]string{}
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
			fn, isFunc := decl.(*ast.FuncDecl)
			if !isFunc || fn.Recv != nil {
				continue
			}
			if _, watched := guardedVerbs[fn.Name.Name]; !watched {
				continue
			}
			if argument, guarded := guardArgument(fn); guarded {
				found[fn.Name.Name] = argument
			} else {
				found[fn.Name.Name] = ""
			}
		}
	}

	for verb, want := range guardedVerbs {
		got, present := found[verb]
		switch {
		case !present:
			t.Errorf("%s is in the guard table but not in this package: the table is enforcing nothing for it", verb)
		case got == "":
			t.Errorf("%s does not call guardRestoreMarker: it would act on a torn restore", verb)
		case got != want:
			t.Errorf("%s guards on %s, want %s: guarding under the wrong operation reads the wrong policy row", verb, got, want)
		}
	}
}

// guardArgument reports the lifecycle constant a function passes to
// guardRestoreMarker, if it calls it at all.
func guardArgument(fn *ast.FuncDecl) (string, bool) {
	var argument string
	ast.Inspect(fn, func(node ast.Node) bool {
		call, isCall := node.(*ast.CallExpr)
		if !isCall {
			return true
		}
		name, isIdent := call.Fun.(*ast.Ident)
		if !isIdent || name.Name != "guardRestoreMarker" || len(call.Args) != 2 {
			return true
		}
		if operation, ok := call.Args[1].(*ast.Ident); ok {
			argument = operation.Name
		}
		return false
	})
	return argument, argument != ""
}
