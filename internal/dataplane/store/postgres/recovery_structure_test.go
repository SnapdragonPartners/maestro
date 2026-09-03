package postgres

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestRecoveryReadLocksNothing is the structural half of design D9's
// lock-free read: no call in recovery.go reaches a locking primitive, and
// BeginTx itself asks for REPEATABLE READ. The behavioural half --
// TestOpenWorkDoesNotWaitOnAHeldArtifactLock -- holds a governing record's
// row lock across OpenWork and is what catches a lock taken transitively.
//
// THE MUTANT this kills is reintroducing AmendmentBase (or
// LockManagementArtifact, or LockEpic) in recovery.go. What that actually
// does, observed rather than assumed: the snapshot is opened READ-ONLY, so
// PostgreSQL refuses the locking read at the statement -- "cannot execute
// SELECT FOR UPDATE in a read-only transaction". The 40001 this design was
// originally written against is what a read-write version of the same
// mistake would produce once the concurrent update committed; it is not what
// the mutant here reaches.
//
// Parsed rather than grepped, so a comment that names the forbidden call
// does not count as one.
func TestRecoveryReadLocksNothing(t *testing.T) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "recovery.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	forbidden := map[string]bool{"AmendmentBase": true, "LockManagementArtifact": true, "LockEpic": true}
	repeatableRead := false
	ast.Inspect(file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.CallExpr:
			if sel, ok := n.Fun.(*ast.SelectorExpr); ok && forbidden[sel.Sel.Name] {
				t.Errorf("recovery.go calls %s at %s; the projection's read must not lock (design D9)",
					sel.Sel.Name, fileSet.Position(n.Pos()))
			}
		}
		// The isolation is asserted ON the BeginTx call, not anywhere in the
		// file: a stray RepeatableRead selector elsewhere must not satisfy it.
		if call, ok := node.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "BeginTx" {
				for _, arg := range call.Args {
					ast.Inspect(arg, func(inner ast.Node) bool {
						if s, ok := inner.(*ast.SelectorExpr); ok && s.Sel.Name == "RepeatableRead" {
							repeatableRead = true
						}
						return true
					})
				}
			}
		}
		return true
	})
	if !repeatableRead {
		t.Error("recovery.go does not open a REPEATABLE READ snapshot")
	}
}
