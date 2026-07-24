package main

// ENGINE-OWNED MUTATION ORACLE for the api-option-lookup story (scratch mode).
//
// The companion in-solution oracle proves the agent's IMPLEMENTATION is
// correct. This proves the agent's AUTHORED TESTS actually exercise EACH
// required behaviour, not just one. A single always-not-found mutant is caught
// by any one real assertion, so an exact-match test plus three empty subtests
// would pass a naive check. This replaces findOption with FOUR reference
// implementations — each correct except one clause of the precedence contract —
// and requires the agent's tests to FAIL against every one. Miss the ambiguity
// case and its mutant survives; the story is rejected, naming the uncovered
// behaviour.
//
// It runs in SCRATCH mode: the engine hands us $ORACLE_SCRATCH, a clean checkout
// of the immutable solution commit that carries the agent's server.go and
// server_test.go and NO engine oracle — so only the agent's own tests judge a
// mutant. We mutate only inside the scratch; the engine owns its removal. This
// program's own assets live in a separate tool dir and never enter the graded
// tree.
//
// findOption is located and removed with go/parser rather than a text regex, so
// mutation depends on BEHAVIOUR, not on how the agent named the parameters or
// shaped the function. A mutant must COMPILE before a test failure counts as
// detection: a non-compiling mutant is a broken check (exit 2), never scored as
// coverage — the exact bug the shell medium kept reintroducing.
//
// Exit: 0 = every mutant detected (pass); 1 = at least one survived, naming the
// uncovered behaviour(s); 2 = a check bug (mutant would not compile, or the
// scratch could not be read).

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
)

// mutant is a reference findOption correct except for one clause of the
// contract. The agent's tests must fail against each, or that clause is
// untested.
type mutant struct {
	label string
	fn    string
}

var mutants = []mutant{
	{"exact-id-wins", `func findOption(o []ModelOption, id string) (ModelOption, bool) {
	var f ModelOption
	n := 0
	for _, x := range o {
		if x.ProviderName == id {
			f = x
			n++
		}
	}
	if n == 1 {
		return f, true
	}
	return ModelOption{}, false
}`},
	{"provider-fallback", `func findOption(o []ModelOption, id string) (ModelOption, bool) {
	for _, x := range o {
		if x.ID == id {
			return x, true
		}
	}
	return ModelOption{}, false
}`},
	{"ambiguity-not-found", `func findOption(o []ModelOption, id string) (ModelOption, bool) {
	for _, x := range o {
		if x.ID == id {
			return x, true
		}
	}
	for _, x := range o {
		if x.ProviderName == id {
			return x, true
		}
	}
	return ModelOption{}, false
}`},
	{"unknown-not-found", `func findOption(o []ModelOption, id string) (ModelOption, bool) {
	for _, x := range o {
		if x.ID == id {
			return x, true
		}
	}
	var f ModelOption
	n := 0
	for _, x := range o {
		if x.ProviderName == id {
			f = x
			n++
		}
	}
	if n == 1 {
		return f, true
	}
	if n > 1 {
		// Ambiguity preserved (correct), so an ambiguity test cannot kill this
		// mutant — it violates ONLY the unknown-not-found clause below.
		return ModelOption{}, false
	}
	if len(o) > 0 {
		return o[0], true
	}
	return ModelOption{}, false
}`},
}

func main() {
	scratch := os.Getenv("ORACLE_SCRATCH")
	if scratch == "" {
		checkBug("ORACLE_SCRATCH not set")
	}
	serverPath := filepath.Join(scratch, "server.go")
	orig, err := os.ReadFile(serverPath)
	if err != nil {
		checkBug("read scratch server.go: %v", err)
	}
	base, err := stripFindOption(serverPath, orig)
	if err != nil {
		checkBug("locate findOption in scratch: %v", err)
	}
	// Whatever happens, leave the scratch's server.go as the agent wrote it.
	defer func() { _ = os.WriteFile(serverPath, orig, 0o644) }()

	var uncovered []string
	for _, m := range mutants {
		mutated := append(append([]byte{}, base...), []byte("\n"+m.fn+"\n")...)
		if err := os.WriteFile(serverPath, mutated, 0o644); err != nil {
			checkBug("write mutant %s: %v", m.label, err)
		}
		// Compile-before-detection, INCLUDING the authored tests: `go build`
		// skips _test.go, so a mutant that breaks the test's compilation would
		// otherwise be credited as detection when the following `go test` fails
		// to build. `go test -run '^$'` compiles every package and its tests but
		// runs nothing, so a failure here is a genuine compile fault (check bug).
		if out, err := run(scratch, "go", "test", "-run", "^$", "-count=1", "./..."); err != nil {
			checkBug("mutant %s did not compile (incl. authored tests):\n%s", m.label, out)
		}
		// The agent's tests: PASS against the mutant means the mutant survived,
		// so that clause is untested. FAIL means the tests detected it. Because
		// compilation is already proven above, this failure is an assertion
		// failure, never a build error.
		if _, err := run(scratch, "go", "test", "-count=1", "./..."); err == nil {
			uncovered = append(uncovered, m.label)
		}
	}

	if len(uncovered) > 0 {
		for _, u := range uncovered {
			fmt.Fprintf(os.Stderr, "uncovered behaviour: authored tests do not detect the %q mutant\n", u)
		}
		os.Exit(1)
	}
}

// stripFindOption returns src with the top-level findOption function removed,
// located via the AST so parameter naming and formatting are irrelevant.
func stripFindOption(path string, src []byte) ([]byte, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	for _, decl := range file.Decls {
		switch fd := decl.(type) {
		case *ast.FuncDecl:
			if fd.Recv != nil || fd.Name.Name != "findOption" {
				continue
			}
			start := fset.Position(fd.Pos()).Offset
			end := fset.Position(fd.End()).Offset
			out := make([]byte, 0, len(src)-(end-start))
			out = append(out, src[:start]...)
			out = append(out, src[end:]...)
			return out, nil
		}
	}
	return nil, fmt.Errorf("no top-level findOption function found")
}

// run executes a command in dir, inheriting the environment (so the scratch's
// go.mod and the module cache are in scope), and returns its combined output.
func run(dir, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}

// checkBug reports a fault in the oracle itself (not the agent) and exits 2, so
// it is never silently scored as coverage.
func checkBug(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "oracle check bug: "+format+"\n", a...)
	os.Exit(2)
}
