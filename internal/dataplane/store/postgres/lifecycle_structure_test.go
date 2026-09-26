package postgres

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The Lifecycle view, asserted STRUCTURALLY (Phase 3 item 4 design, D3).
//
// New refuses a zero harness version; OpenLifecycle builds a Store without
// one and hands back an interface that cannot reach anything the version is
// recorded by. The type system does not enforce that last part -- a type
// assertion would recover the *Store -- and no behavioural test can hold a
// property about which code exists. So this reads the module's source:
//
//   - build, the constructor without the version check, is called by New and
//     OpenLifecycle and nothing else;
//   - OpenLifecycle is called only by the local lifecycle, in `stack`;
//   - nothing outside tests asserts any value back to *postgres.Store.
//
// Files are parsed directly rather than listed through the toolchain, so
// every build tag is covered at once.

const postgresImportPath = "orchestrator/internal/dataplane/store/postgres"

func TestTheVersionFreeStoreCannotEscapeItsView(t *testing.T) {
	root := structureModuleRoot(t)
	fileSet := token.NewFileSet()

	var buildCallers, lifecycleCallers, assertions []string
	parsed := 0

	for _, top := range []string{"cmd", "internal", "pkg"} {
		walkErr := filepath.WalkDir(filepath.Join(root, top), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, parseErr := parser.ParseFile(fileSet, path, nil, parser.SkipObjectResolution)
			if parseErr != nil {
				return parseErr
			}
			parsed++
			rel, _ := filepath.Rel(root, path)
			inPackage := filepath.ToSlash(filepath.Dir(rel)) == strings.TrimPrefix(postgresImportPath, "orchestrator/")
			alias := importName(file, postgresImportPath)

			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				where := rel + ":" + fn.Name.Name
				ast.Inspect(fn.Body, func(node ast.Node) bool {
					switch typed := node.(type) {
					case *ast.CallExpr:
						switch callee := typed.Fun.(type) {
						case *ast.Ident:
							if inPackage && callee.Name == "build" {
								buildCallers = append(buildCallers, where)
							}
							if inPackage && callee.Name == "OpenLifecycle" {
								lifecycleCallers = append(lifecycleCallers, where)
							}
						case *ast.SelectorExpr:
							if pkg, ok := callee.X.(*ast.Ident); ok && alias != "" && pkg.Name == alias && callee.Sel.Name == "OpenLifecycle" {
								lifecycleCallers = append(lifecycleCallers, where)
							}
						}
					case *ast.TypeAssertExpr:
						if namesStore(typed.Type, inPackage, alias) {
							assertions = append(assertions, where)
						}
					case *ast.CaseClause:
						for _, expr := range typed.List {
							if namesStore(expr, inPackage, alias) {
								assertions = append(assertions, where)
							}
						}
					}
					return true
				})
			}
			return nil
		})
		if walkErr != nil && !os.IsNotExist(walkErr) {
			t.Fatalf("walk %s: %v", top, walkErr)
		}
	}

	// A walk that parsed nothing agrees with every expectation below.
	if parsed < 100 {
		t.Fatalf("parsed %d files; the module has far more, so the walk did not see the tree", parsed)
	}

	slices.Sort(buildCallers)
	wantBuild := []string{
		"internal/dataplane/store/postgres/postgres.go:New",
		"internal/dataplane/store/postgres/postgres.go:OpenLifecycle",
	}
	if !slices.Equal(buildCallers, wantBuild) {
		t.Errorf("build is called from %v, want exactly %v: it constructs a Store without checking "+
			"the harness version", buildCallers, wantBuild)
	}

	slices.Sort(lifecycleCallers)
	wantLifecycle := []string{
		"internal/dataplane/stack/stack.go:reconcileClaims",
		"internal/dataplane/stack/verify.go:verifyLocked",
	}
	if !slices.Equal(lifecycleCallers, wantLifecycle) {
		t.Errorf("OpenLifecycle is called from %v, want exactly %v: it is for the local lifecycle "+
			"tending the plane itself, not for a caller with a job", lifecycleCallers, wantLifecycle)
	}

	if len(assertions) != 0 {
		t.Errorf("%v assert a value to *postgres.Store; that is how a Store built without a harness "+
			"version would leave the Lifecycle view", assertions)
	}
}

// namesStore reports whether a type expression is Store or *Store, as this
// package or an importer would write it.
func namesStore(expr ast.Expr, inPackage bool, alias string) bool {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	switch typed := expr.(type) {
	case *ast.Ident:
		return inPackage && typed.Name == "Store"
	case *ast.SelectorExpr:
		pkg, ok := typed.X.(*ast.Ident)
		return ok && alias != "" && pkg.Name == alias && typed.Sel.Name == "Store"
	}
	return false
}

// importName returns the name a file refers to an import path by, or "".
func importName(file *ast.File, path string) string {
	for _, spec := range file.Imports {
		if strings.Trim(spec.Path.Value, `"`) != path {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name
		}
		return filepath.Base(path)
	}
	return ""
}

func structureModuleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test directory")
		}
		dir = parent
	}
}
