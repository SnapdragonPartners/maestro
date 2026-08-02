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
// the reason it is safe against both marked states.
//
// Being listed here is a decision, not an omission. That distinction is the
// whole point: an entry point missing from BOTH tables fails the test, so a
// verb added later — new-key recovery is the next one — cannot become
// unguarded by nobody noticing it.
//
//nolint:gochecknoglobals // Immutable expectation table for the test below.
var unguardedVerbs = map[string]string{
	"Down":  "stopping an already-stopped torn or unverified plane cannot make it worse",
	"Reset": "discarding the plane is a way out of both states, and sweeps both markers",
}

// The marker policy is worthless if nothing consults it, and testing the
// policy helper cannot tell the difference.
//
// This is the same defect, one layer up, that mutation testing found in the
// restore tests: asserting a helper's behaviour proves nothing about whether
// its callers use it. Deleting the guardRestoreState call from Migrate
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
			t.Errorf("%s is listed as unguarded (%s) but calls guardRestoreState: the tables disagree with the code",
				verb, reason)
		case guarded && got == "":
			t.Errorf("%s does not call guardRestoreState: it would act on a torn restore, or on a "+
				"plane nothing has ever verified", verb)
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
// guardRestoreState, if it calls it at all.
//
// guardRestoreState and not guardRestoreMarker: the combined guard is what
// applies BOTH marker policies, and a verb that reached past it to the torn
// half alone would be guarded against the failure it remembered and open to
// the other.
func guardArgument(fn *ast.FuncDecl) (string, bool) {
	var argument string
	ast.Inspect(fn, func(node ast.Node) bool {
		call, isCall := node.(*ast.CallExpr)
		if !isCall {
			return true
		}
		name, isIdent := call.Fun.(*ast.Ident)
		if !isIdent || name.Name != "guardRestoreState" || len(call.Args) != 2 {
			return true
		}
		if operation, ok := call.Args[1].(*ast.Ident); ok {
			argument = operation.Name
		}
		return false
	})
	return argument, argument != ""
}

// The same property one level down: `up`'s own debt-bearing shutdown must be
// armed before Compose starts anything.
//
// This is the defect the test below covers, rediscovered inside the function
// that test's subject calls. `up` invokes Compose and then does four more
// things — readiness, bucket setup, migrations, claim reconciliation — any
// of which can fail with the containers already running. When the only
// shutdown lived beside the settlement step, every one of those returned a
// live plane carrying unsettled verification debt, which is exactly what
// D4a forbids.
//
// So the arming point is asserted to precede the FIRST thing that starts
// anything, and the disarm to follow the settlement. Checked structurally
// for the reason the next test is: this is a property of statement ORDER,
// which a parser sees exactly and which review demonstrably does not.
func TestUpArmsItsDebtShutdownBeforeStartingAnything(t *testing.T) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "stack.go", nil, 0)
	if err != nil {
		t.Fatalf("parse stack.go: %v", err)
	}

	var body *ast.BlockStmt
	for _, decl := range file.Decls {
		if fn, isFunc := decl.(*ast.FuncDecl); isFunc && fn.Name.Name == "up" && fn.Recv == nil {
			body = fn.Body
		}
	}
	if body == nil {
		t.Fatal("up is gone: this guard is enforcing nothing")
	}

	deferAt, armedAt, composeAt, disarmedAt := -1, -1, -1, -1
	for i, statement := range body.List {
		if deferred, isDefer := statement.(*ast.DeferStmt); isDefer && deferAt < 0 && callsFunc(deferred, "down") {
			deferAt = i
			// The arming assignment is the declaration of the flag that
			// defer consults, which must exist before it.
			continue
		}
		ast.Inspect(statement, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.AssignStmt:
				if assignsIdent(typed, "settled") {
					if armedAt < 0 {
						armedAt = i
					}
					if assignsTrue(typed, "settled") && disarmedAt < 0 {
						disarmedAt = i
					}
				}
			case *ast.CallExpr:
				if name, isIdent := typed.Fun.(*ast.Ident); isIdent && name.Name == "compose" && composeAt < 0 {
					composeAt = i
				}
			}
			return true
		})
	}

	switch {
	case composeAt < 0:
		t.Fatal("up never calls compose: this guard is enforcing nothing")
	case deferAt < 0:
		t.Fatal("up has no deferred call to down: a failure after Compose starts the containers would " +
			"leave a plane carrying verification debt running for connected writers")
	case armedAt < 0:
		t.Fatal("up never assigns the settled flag: the defer would consult nothing")
	case armedAt > composeAt:
		t.Errorf("the debt flag is set at statement %d but compose runs at %d: readiness, bucket setup, "+
			"migrations and claim reconciliation would each leave an unverified plane running",
			armedAt, composeAt)
	case deferAt > composeAt:
		t.Errorf("the shutdown defer is registered at statement %d but compose runs at %d: a defer "+
			"registered after a call cannot run for a failure inside it", deferAt, composeAt)
	case disarmedAt < 0:
		t.Fatal("up never disarms the debt shutdown: a healthy settlement would stop the plane it just " +
			"vouched for")
	case disarmedAt < composeAt:
		t.Errorf("the debt shutdown is disarmed at statement %d, before compose at %d: it would cover "+
			"nothing at all", disarmedAt, composeAt)
	}
}

// assignsIdent reports whether a statement assigns the named variable at
// all, whatever the value. The arming assignment is `settled := !owed`,
// which assignsTrue cannot see.
func assignsIdent(assignment *ast.AssignStmt, target string) bool {
	for _, left := range assignment.Lhs {
		if name, isIdent := left.(*ast.Ident); isIdent && name.Name == target {
			return true
		}
	}
	return false
}

// Shutdown must be armed BEFORE `up` is called, not after it returns.
//
// `up` starts containers and then does four more things — readiness,
// bucket setup, migrations, claim reconciliation — any of which can fail
// with the plane already running. Arming afterwards covers none of them,
// and the difference is invisible in review: both spellings are one
// assignment beside one call.
//
// Checked structurally because the behavioural test needs Docker, and this
// property is about statement ORDER, which a parser can see exactly.
func TestRestoreArmsShutdownBeforeStartingThePlane(t *testing.T) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "restore.go", nil, 0)
	if err != nil {
		t.Fatalf("parse restore.go: %v", err)
	}

	var body *ast.BlockStmt
	for _, decl := range file.Decls {
		if fn, isFunc := decl.(*ast.FuncDecl); isFunc && fn.Name.Name == "replaceDataRoot" {
			body = fn.Body
		}
	}
	if body == nil {
		t.Fatal("replaceDataRoot is gone: this guard is enforcing nothing")
	}

	// THREE things have to line up, and checking fewer of them leaves the
	// defect reachable:
	//
	//   - the shutdown defer must EXIST — deleting it restores the original
	//     bug outright;
	//   - it must be registered BEFORE `up`, since a defer registered after
	//     a call cannot run for a failure inside that call;
	//   - and the flag it consults must be armed with `true` before `up`
	//     too, or the defer runs and does nothing.
	//
	// An earlier version of this guard checked only the third. It passed
	// while the defer was deleted, and passed while the defer was moved
	// after `up` — so it certified exactly the arrangement it exists to
	// forbid. The lesson is the same one this package keeps relearning:
	// assert the mechanism, not a token near it.
	deferAt, armedAt, upAt := -1, -1, -1
	for i, statement := range body.List {
		if deferred, isDefer := statement.(*ast.DeferStmt); isDefer && deferAt < 0 && callsFunc(deferred, "down") {
			deferAt = i
		}
		ast.Inspect(statement, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.AssignStmt:
				if armedAt < 0 && assignsTrue(typed, "started") {
					armedAt = i
				}
			case *ast.CallExpr:
				if name, isIdent := typed.Fun.(*ast.Ident); isIdent && name.Name == "up" && upAt < 0 {
					upAt = i
				}
			}
			return true
		})
	}

	switch {
	case upAt < 0:
		t.Fatal("replaceDataRoot never calls up: this guard is enforcing nothing")
	case deferAt < 0:
		t.Fatal("replaceDataRoot has no deferred call to down: a failure after the plane starts " +
			"would leave an unverified plane running for connected writers")
	case deferAt > upAt:
		t.Errorf("the shutdown defer is registered at statement %d but up runs at %d: a defer registered "+
			"after a call cannot run for a failure inside it", deferAt, upAt)
	case armedAt < 0:
		t.Fatal("replaceDataRoot never arms shutdown with true: the defer would run and do nothing")
	case armedAt > upAt:
		t.Errorf("shutdown is armed at statement %d but up runs at %d: a failure inside up — readiness, "+
			"migrations, bucket setup, reconciliation — would leave an unverified plane running", armedAt, upAt)
	}
}

// callsFunc reports whether a deferred statement's body calls the named
// function.
func callsFunc(deferred *ast.DeferStmt, target string) bool {
	found := false
	ast.Inspect(deferred, func(node ast.Node) bool {
		call, isCall := node.(*ast.CallExpr)
		if !isCall {
			return true
		}
		if name, isIdent := call.Fun.(*ast.Ident); isIdent && name.Name == target {
			found = true
			return false
		}
		return true
	})
	return found
}

// assignsTrue reports whether a statement sets the named variable to the
// literal true.
func assignsTrue(assignment *ast.AssignStmt, target string) bool {
	for i, left := range assignment.Lhs {
		name, isIdent := left.(*ast.Ident)
		if !isIdent || name.Name != target || i >= len(assignment.Rhs) {
			continue
		}
		if value, isIdent := assignment.Rhs[i].(*ast.Ident); isIdent && value.Name == "true" {
			return true
		}
	}
	return false
}
