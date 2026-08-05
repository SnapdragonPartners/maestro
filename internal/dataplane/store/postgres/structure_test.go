package postgres

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"testing"

	"orchestrator/internal/dataplane/store"
)

// TestStagingIsEmptiedByOneProtocol reads the source, and does so because
// the property is about which code runs rather than about any value it
// produces.
//
// There are two callers that must empty a staging key: cleanup, and the
// writer's own release after a completed write. Emptying means BOTH
// enumerations -- abort the incomplete uploads, then delete the versions --
// because the two crash windows leave different residue and neither
// vocabulary can see the other's.
//
// The writer's release originally did versions only. That defect is
// invisible to behavioural tests: a write that SUCCEEDS leaves no
// incomplete upload for the abort step to find, so the release's own tests
// pass either way, and the case where it matters -- a write that died
// during a multipart upload -- needs a body large enough to be multipart
// and a failure injected between its parts.
//
// So what is pinned here is that both callers go through the one function
// whose behaviour IS tested (TestCleanupAbortsAnIncompleteUpload), rather
// than re-implementing a subset of it.
func TestStagingIsEmptiedByOneProtocol(t *testing.T) {
	const protocol = "emptyStagingKey"

	fileSet := token.NewFileSet()
	for _, source := range []struct {
		file   string
		caller string
	}{
		{"objects.go", "releaseStaging"},
		{"staging.go", "releaseOneLease"},
		{"staging.go", "collectStagingOrphans"},
	} {
		parsed, err := parser.ParseFile(fileSet, source.file, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", source.file, err)
		}
		body := findMethod(parsed, source.caller)
		if body == nil {
			t.Errorf("%s no longer declares %s; this test names the wrong function",
				source.file, source.caller)
			continue
		}
		if !callsFunction(body, protocol) {
			t.Errorf("%s does not call %s. Emptying a staging key means aborting its incomplete "+
				"uploads AND deleting its versions; a caller that does one of those is a caller that "+
				"leaves the other kind of residue behind, and no behavioural test of a successful "+
				"write can see it.", source.caller, protocol)
		}
	}
}

// findMethod returns a function or method declaration by name.
func findMethod(file *ast.File, name string) *ast.FuncDecl {
	for _, decl := range file.Decls {
		function, isFunc := decl.(*ast.FuncDecl)
		if isFunc && function.Name.Name == name {
			return function
		}
	}
	return nil
}

// callsFunction reports whether a declaration calls the named function,
// whether directly or as a method on a receiver.
func callsFunction(decl *ast.FuncDecl, name string) bool {
	found := false
	ast.Inspect(decl, func(node ast.Node) bool {
		call, isCall := node.(*ast.CallExpr)
		if !isCall {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			if fun.Name == name {
				found = true
			}
		case *ast.SelectorExpr:
			if fun.Sel.Name == name {
				found = true
			}
		}
		return !found
	})
	return found
}

// ledgerWriter is the narrow interface the assertions below are about.
type ledgerWriter interface {
	RecordBenchmarkAttempt(ctx context.Context, input store.RecordBenchmarkAttemptInput) (store.Bootstrapped[store.BenchmarkAttempt], error)
}

// TestLedgerWriteIsTransactionOnly asserts the shape of the SEAM rather than
// of its implementation.
//
// The rule it protects cannot be expressed as a runtime check. The ledger row
// and the Audit artifact it names must commit together, and every Store
// method opens a transaction of its own — so a Store delegate would not fail,
// it would silently do the forbidden thing. The only way to forbid it is for
// the method not to be on the interface.
//
// Both assertions are about store.Tx and store.Store, NOT about *tx and
// *Store. An earlier version asserted the concrete types, which proved only
// that the implementation carried the method: deleting BenchmarkTxWriter from
// store.Tx left it compiling, because *tx still satisfied both interfaces
// independently of whether the seam exposed either.
func TestLedgerWriteIsTransactionOnly(t *testing.T) {
	// Present on the Tx INTERFACE. A compile-time assertion, so removing the
	// embedding from store.Tx stops this package building.
	var seam store.Tx
	var _ ledgerWriter = seam

	// Absent from the Store INTERFACE. Read off the interface's own method
	// set, because Go cannot assert that a type does not satisfy one, and
	// because asking the implementation would answer a different question.
	storeInterface := reflect.TypeOf((*store.Store)(nil)).Elem()
	if _, exposed := storeInterface.MethodByName("RecordBenchmarkAttempt"); exposed {
		t.Error("store.Store exposes RecordBenchmarkAttempt: the ledger row would commit in a " +
			"transaction of its own, apart from the Audit artifact it names, which is the split " +
			"the ledger exists to prevent")
	}

	// And the local implementation still satisfies both, so the split above
	// is a narrowing of the seam rather than a hole in this package.
	var _ store.Store = (*Store)(nil)
	var _ store.Tx = (*tx)(nil)
}
