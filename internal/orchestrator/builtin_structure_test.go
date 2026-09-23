package orchestrator_test

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

// One loader path, asserted STRUCTURALLY (item 4 design, D2).
//
// The built-in ships empty, so the production import path -- loader,
// digest, gate, install -- is exercised over content only by fixtures. A
// fixture that reached the seam through a hand-built store.BuiltinPromptPack
// would prove the seam and leave the loader out of every test that carries
// content; the design names that diversion as the mutation it most wants
// caught, and no behavioural test can catch it, because the diverted suite
// passes. So this reads the source, TEST FILES INCLUDED, and requires that
// the only construction of store.BuiltinPromptPack in the module is
// LoadBuiltin's, which sits behind prompt.Load; and that prompt.Load is
// called from LoadBuiltin and nowhere else outside its own package.
//
// Composite literals WITH fields and new() are what it sees; an empty
// literal on an error path constructs nothing. A zero value assigned field
// by field would pass; this guards the ordinary way of writing the shortcut,
// not a determined one.

const (
	storeImportPath  = "orchestrator/internal/dataplane/store"
	promptImportPath = "orchestrator/internal/prompt"
	builtinTypeName  = "BuiltinPromptPack"
)

func TestTheBuiltinPackIsConstructedOnlyByTheLoader(t *testing.T) {
	root := moduleRoot(t)
	fileSet := token.NewFileSet()

	var constructions, loadCallers []string
	parsed := 0

	for _, top := range []string{"cmd", "internal", "pkg"} {
		walkErr := filepath.WalkDir(filepath.Join(root, top), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			file, parseErr := parser.ParseFile(fileSet, path, nil, parser.SkipObjectResolution)
			if parseErr != nil {
				return parseErr
			}
			parsed++
			rel, _ := filepath.Rel(root, path)
			dir := filepath.ToSlash(filepath.Dir(rel))
			inStore := dir == strings.TrimPrefix(storeImportPath, "orchestrator/")
			inPrompt := dir == strings.TrimPrefix(promptImportPath, "orchestrator/")
			storeAlias := aliasOf(file, storeImportPath)
			promptAlias := aliasOf(file, promptImportPath)

			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				where := rel + ":" + fn.Name.Name
				ast.Inspect(fn.Body, func(node ast.Node) bool {
					switch typed := node.(type) {
					case *ast.CompositeLit:
						if len(typed.Elts) > 0 && namesType(typed.Type, builtinTypeName, inStore, storeAlias) {
							constructions = append(constructions, where)
						}
					case *ast.CallExpr:
						if ident, ok := typed.Fun.(*ast.Ident); ok && ident.Name == "new" && len(typed.Args) == 1 &&
							namesType(typed.Args[0], builtinTypeName, inStore, storeAlias) {
							constructions = append(constructions, where)
						}
						if sel, ok := typed.Fun.(*ast.SelectorExpr); ok && !inPrompt {
							if pkg, ok := sel.X.(*ast.Ident); ok && promptAlias != "" && pkg.Name == promptAlias && sel.Sel.Name == "Load" {
								loadCallers = append(loadCallers, where)
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

	if parsed < 100 {
		t.Fatalf("parsed %d files; the module has far more, so the walk did not see the tree", parsed)
	}

	want := []string{"internal/orchestrator/builtin.go:LoadBuiltin"}
	slices.Sort(constructions)
	if !slices.Equal(constructions, want) {
		t.Errorf("store.%s is constructed in %v, want exactly %v: every pack that reaches the seam, "+
			"fixtures included, travels the loader (design D2), or the production import path is "+
			"only ever exercised over the empty built-in", builtinTypeName, constructions, want)
	}
	slices.Sort(loadCallers)
	if !slices.Equal(loadCallers, want) {
		t.Errorf("prompt.Load is called from %v, want exactly %v: the loader has one consumer, and a "+
			"second would be a second place the file-system shape is interpreted", loadCallers, want)
	}
}

// namesType reports whether a type expression names `name` from the store
// package, as the package itself or an importer would write it.
func namesType(expr ast.Expr, name string, inStore bool, alias string) bool {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	switch typed := expr.(type) {
	case *ast.Ident:
		return inStore && typed.Name == name
	case *ast.SelectorExpr:
		pkg, ok := typed.X.(*ast.Ident)
		return ok && alias != "" && pkg.Name == alias && typed.Sel.Name == name
	}
	return false
}

// aliasOf returns the name a file refers to an import path by, or "".
func aliasOf(file *ast.File, path string) string {
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
