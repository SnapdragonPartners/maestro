package postgres

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestRecoveryReadLocksNothing is the deterministic form of design D9's
// "the projection never aborts on a concurrent artifact write". The
// timing-dependent form — a write racing the snapshot — cannot be forced
// without an injection hook, so the property is held structurally: no call
// in recovery.go reaches a locking primitive, and the snapshot is
// REPEATABLE READ. THE MUTANT this kills is reintroducing AmendmentBase (or
// LockManagementArtifact, or LockEpic) in recovery.go, which under
// REPEATABLE READ is exactly the 40001.
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
		case *ast.SelectorExpr:
			if n.Sel.Name == "RepeatableRead" {
				repeatableRead = true
			}
		}
		return true
	})
	if !repeatableRead {
		t.Error("recovery.go does not open a REPEATABLE READ snapshot")
	}
}
