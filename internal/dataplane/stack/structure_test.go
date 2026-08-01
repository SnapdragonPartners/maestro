package stack

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"testing"
)

// The provisioning steps `up` must perform, in the order it must perform
// them, named by the function it calls for each.
//
// Order is the rule, not merely presence. Provisioning talks to a service,
// so it has to follow readiness; and it must precede the migration step,
// because a plane that reports itself ready and migrated while unable to
// store an object is exactly the state this item found on a live stack --
// healthy for forty-five hours, holding no bucket at all.
//
// Reconciliation comes last, and its position is as load-bearing as the
// others: it reads a table the migrations create, and it must finish before
// `up` returns, because a surviving deletion claim is condemned storage that
// may still be there AND a digest whose writers cannot take the
// existing-object shortcut until it clears.
var provisioningOrder = []string{"waitReady", "ensureBucket", "migrateLocked", "reconcileClaims"}

// TestUpProvisionsBetweenReadinessAndMigration reads `up`'s own source.
//
// It exists because the behavioural test for provisioning exercises
// ensureBucket directly -- running `up` from a test would take the
// lifecycle lock and drive Compose against the developer's live plane --
// and that leaves the CALL SITE unguarded: delete the call from `up` and
// every other test in this package still passes, while the original defect
// returns silently and the by-now-existing bucket hides it from manual
// reruns too.
//
// Reading the source is a blunt instrument, and it is the honest one here:
// the property is "this step happens, in this position", which is a
// statement about the orchestration and not about any value it produces.
func TestUpProvisionsBetweenReadinessAndMigration(t *testing.T) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "stack.go", nil, 0)
	if err != nil {
		t.Fatalf("parse stack.go: %v", err)
	}

	up := findFunc(file, "up")
	if up == nil {
		t.Fatal("stack.go no longer declares up(); this test names the wrong function")
	}

	var called []string
	ast.Inspect(up, func(node ast.Node) bool {
		call, isCall := node.(*ast.CallExpr)
		if !isCall {
			return true
		}
		name, isIdent := call.Fun.(*ast.Ident)
		if !isIdent {
			return true
		}
		if slices.Contains(provisioningOrder, name.Name) {
			called = append(called, name.Name)
		}
		return true
	})

	if !slices.Equal(called, provisioningOrder) {
		t.Fatalf("up() calls %v; it must call %v, in that order.\n"+
			"Provisioning follows readiness because it talks to the service, and precedes migration "+
			"because `up` must never report a ready plane that cannot store an object. Claim "+
			"reconciliation follows migration because it reads a migrated table, and `up` must never "+
			"report a plane ready while it still carries unfinished destructive work.",
			called, provisioningOrder)
	}
}

// keyUsingOperations are the lifecycle operations that need root key
// material. Every one of them must reach it through rootKeyFor.
var keyUsingOperations = []string{"up", "Migrate", "ForceVersion"}

// forbiddenKeySources are the ways to obtain a key while bypassing the
// create-versus-load decision. paths.EnsureKey CREATES; paths.LoadKey and
// secret.KeyFile each pick an access mode, which is precisely the choice
// rootKeyFor exists to make in one place.
var forbiddenKeySources = map[string]string{
	"EnsureKey": "paths",
	"LoadKey":   "paths",
	"KeyFile":   "secret",
}

// TestOnlyRootKeyForDecidesKeyCreation is item 7's D4 as a source rule, in
// both directions.
//
// Negative: nothing outside rootKeyFor may reach a key source directly.
// Banning only EnsureKey would leave two other ways to make the same
// decision somewhere else — LoadKey, and secret.KeyFile with an access mode
// chosen on the spot.
//
// Positive: each key-using operation must actually CALL rootKeyFor. Without
// this half, deleting the call from Migrate satisfies every ban and the rule
// passes while the operation quietly stops asking.
//
// It is a source rule because the defect is invisible to behavioural tests:
// an operation that creates a key when it should not still WORKS on any
// machine whose key is present, which is every machine that has run `up`
// once.
func TestOnlyRootKeyForDecidesKeyCreation(t *testing.T) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "stack.go", nil, 0)
	if err != nil {
		t.Fatalf("parse stack.go: %v", err)
	}

	reaches := map[string][]string{}
	callsHelper := map[string]bool{}

	for _, decl := range file.Decls {
		function, isFunc := decl.(*ast.FuncDecl)
		if !isFunc {
			continue
		}
		name := function.Name.Name
		ast.Inspect(function, func(node ast.Node) bool {
			call, isCall := node.(*ast.CallExpr)
			if !isCall {
				return true
			}
			if ident, isIdent := call.Fun.(*ast.Ident); isIdent && ident.Name == "rootKeyFor" {
				callsHelper[name] = true
				return true
			}
			selector, isSelector := call.Fun.(*ast.SelectorExpr)
			if !isSelector {
				return true
			}
			pkg, isPkg := selector.X.(*ast.Ident)
			if !isPkg {
				return true
			}
			if wantPkg, forbidden := forbiddenKeySources[selector.Sel.Name]; forbidden &&
				pkg.Name == wantPkg && name != "rootKeyFor" {
				reaches[name] = append(reaches[name], pkg.Name+"."+selector.Sel.Name)
			}
			return true
		})
	}

	for function, sources := range reaches {
		t.Errorf("%s reaches %v directly. Only rootKeyFor decides whether a lifecycle operation "+
			"may CREATE key material, and it decides from the operation AND whether the data root "+
			"is empty. A call outside it makes that decision somewhere nothing reviews — and it "+
			"passes every test on a machine whose key is already there, which is every machine "+
			"that has run `up` once.", function, sources)
	}

	for _, operation := range keyUsingOperations {
		if !callsHelper[operation] {
			t.Errorf("%s does not call rootKeyFor. Every lifecycle operation that needs key "+
				"material goes through the one decision; an operation that stops asking is not "+
				"caught by the bans above, because it no longer reaches anything to ban.", operation)
		}
	}
}

func findFunc(file *ast.File, name string) *ast.FuncDecl {
	for _, decl := range file.Decls {
		function, isFunc := decl.(*ast.FuncDecl)
		if isFunc && function.Recv == nil && function.Name.Name == name {
			return function
		}
	}
	return nil
}
