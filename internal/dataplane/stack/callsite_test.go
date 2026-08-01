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

// unguardedVerbs are the entry points deliberately NOT guarded, each with
// the reason it is safe against a torn restore.
//
// Being listed here is a decision, not an omission. That distinction is the
// whole point: an entry point missing from BOTH tables fails the test, so a
// verb added later — new-key recovery is the next one — cannot become
// unguarded by nobody noticing it.
//
//nolint:gochecknoglobals // Immutable expectation table for the test below.
var unguardedVerbs = map[string]string{
	"Down":  "stopping an already-stopped torn plane cannot make it worse",
	"Reset": "discarding the plane is one of the two ways out of a torn restore",
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

	// Entry points are DISCOVERED from the package rather than read off the
	// table. A table-driven parser only inspects what the table already
	// names, so deleting a verb from it would make the test ignore that verb
	// and pass — the failure this whole test exists to prevent, one level up.
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
			if !isLifecycleEntryPoint(fn) {
				continue
			}
			argument, _ := guardArgument(fn)
			found[fn.Name.Name] = argument
		}
	}

	// Every DISCOVERED entry point must have been classified.
	for verb, got := range found {
		want, guarded := guardedVerbs[verb]
		reason, exempt := unguardedVerbs[verb]
		switch {
		case !guarded && !exempt:
			t.Errorf("%s is a lifecycle entry point in neither guardedVerbs nor unguardedVerbs: "+
				"decide whether it may run against a torn restore", verb)
		case exempt && got != "":
			t.Errorf("%s is listed as unguarded (%s) but calls guardRestoreMarker: the tables disagree with the code",
				verb, reason)
		case guarded && got == "":
			t.Errorf("%s does not call guardRestoreMarker: it would act on a torn restore", verb)
		case guarded && got != want:
			t.Errorf("%s guards on %s, want %s: guarding under the wrong operation reads the wrong policy row",
				verb, got, want)
		}
	}

	// And every classified verb must still exist, so a table entry cannot
	// outlive the function it describes and quietly enforce nothing.
	for verb := range guardedVerbs {
		if _, present := found[verb]; !present {
			t.Errorf("guardedVerbs names %s, which is not a lifecycle entry point in this package", verb)
		}
	}
	for verb := range unguardedVerbs {
		if _, present := found[verb]; !present {
			t.Errorf("unguardedVerbs names %s, which is not a lifecycle entry point in this package", verb)
		}
	}
}

// isLifecycleEntryPoint reports whether a function is an exported operation
// against a plane.
//
// The structural definition is "exported, and takes a *Config" — which is
// what every lifecycle verb has in common and what a new one will have too.
// Deriving it beats listing it: a list is exactly the thing that goes stale.
func isLifecycleEntryPoint(fn *ast.FuncDecl) bool {
	if !fn.Name.IsExported() || fn.Type.Params == nil {
		return false
	}
	for _, field := range fn.Type.Params.List {
		pointer, isPointer := field.Type.(*ast.StarExpr)
		if !isPointer {
			continue
		}
		if named, isNamed := pointer.X.(*ast.Ident); isNamed && named.Name == "Config" {
			return true
		}
	}
	return false
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
