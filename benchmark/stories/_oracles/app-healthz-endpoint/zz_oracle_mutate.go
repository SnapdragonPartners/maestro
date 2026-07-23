package main

// ENGINE-OWNED MUTATION ORACLE for the app-healthz-endpoint story (scratch
// mode).
//
// The companion in-solution oracle verifies the ENDPOINT. This proves the
// agent's AUTHORED TEST is meaningful — that it actually issues a request and
// asserts on the response, rather than merely standing the router up and
// naming httptest/newServer/status.
//
// It WRAPS the constructed handler rather than registering a second /healthz
// route. A duplicate registration would make net/http panic during
// construction, so a test that merely builds the router (asserting nothing)
// would "detect" that and pass for the wrong reason. Wrapping keeps
// construction succeeding and breaks only the /healthz RESPONSE, so detection
// requires actually issuing a request and asserting on it. The agent's test
// must FAIL against this mutant, or it is not a real behavioural test.
//
// Scratch mode: the engine checks out the immutable solution commit into
// $ORACLE_SCRATCH (the agent's server.go + health_test.go, no oracle), and the
// helper mutates only there. newServer is renamed via go/parser (robust to
// formatting), and the wrapper is appended. A mutant must COMPILE before a test
// failure counts as detection (a non-compiling mutant is a check bug, never
// scored as coverage). The engine owns scratch cleanup on every exit path.
//
// Exit: 0 = the agent's test detected the mutant (pass); 1 = the mutant
// survived, i.e. the test is not behavioural (fail); 2 = a check bug (the
// mutant would not compile, or the scratch could not be read).

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
)

// mutant is a wrapping of newServer that breaks /healthz along exactly ONE
// clause of the response contract, leaving construction and every other route
// intact. Two clauses are specified — status 200 and body {"status":"ok"} — so
// one mutant per clause is required: a single mutant that broke BOTH would be
// detected by a test asserting only one, falsely certifying a half-test.
type mutant struct {
	label string
	// body is the handler body for the /healthz branch, breaking one clause.
	body string
}

var mutants = []mutant{
	// Wrong status, VALID body — killed only by a status assertion.
	{"status-code", `w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(` + "`" + `{"status":"ok"}` + "`" + `))`},
	// Status 200, WRONG body — killed only by a body assertion.
	{"status-body", `w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(` + "`" + `{"status":"broken"}` + "`" + `))`},
}

// wrapperFor builds the replacement newServer for a mutant: it delegates every
// route to the original (renamed) constructor except /healthz, whose response
// it breaks along one clause.
func wrapperFor(m mutant) string {
	return `

func newServer(options []ModelOption, logger *log.Logger) http.Handler {
	inner := zzOrigNewServer(options, logger)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			` + m.body + `
			return
		}
		inner.ServeHTTP(w, r)
	})
}
`
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
	renamed, err := renameNewServer(serverPath, orig)
	if err != nil {
		checkBug("rename newServer in scratch: %v", err)
	}
	// Whatever happens, leave the scratch's server.go as the agent wrote it.
	defer func() { _ = os.WriteFile(serverPath, orig, 0o644) }()

	var uncovered []string
	for _, m := range mutants {
		mutated := append(append([]byte{}, renamed...), []byte(wrapperFor(m))...)
		if err := os.WriteFile(serverPath, mutated, 0o644); err != nil {
			checkBug("write mutant %s: %v", m.label, err)
		}
		// Compile-before-detection, INCLUDING the authored test: `go build`
		// skips _test.go, so a mutant that broke the test's compilation would
		// otherwise be credited as detection when the following `go test` fails
		// to build. `go test -run '^$'` compiles every package and its tests but
		// runs nothing, so a failure here is a genuine compile fault.
		if out, err := run(scratch, "go", "test", "-run", "^$", "-count=1", "./..."); err != nil {
			checkBug("mutant %s did not compile (incl. authored test):\n%s", m.label, out)
		}
		// The agent's test: PASS against the mutant means it did not notice
		// /healthz was broken along this clause, so it is not fully behavioural.
		if _, err := run(scratch, "go", "test", "-count=1", "./..."); err == nil {
			uncovered = append(uncovered, m.label)
		}
	}

	if len(uncovered) > 0 {
		for _, u := range uncovered {
			fmt.Fprintf(os.Stderr, "authored test does not detect a broken /healthz %s — it is not fully behavioural\n", u)
		}
		os.Exit(1)
	}
}

// renameNewServer renames the top-level newServer function to zzOrigNewServer,
// located via the AST so formatting is irrelevant. Call sites (main, the
// agent's test) then resolve to the appended wrapper.
func renameNewServer(path string, src []byte) ([]byte, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	const (
		from = "newServer"
		to   = "zzOrigNewServer" // must match the wrapper's call site
	)
	for _, decl := range file.Decls {
		switch fd := decl.(type) {
		case *ast.FuncDecl:
			if fd.Recv != nil || fd.Name.Name != from {
				continue
			}
			off := fset.Position(fd.Name.Pos()).Offset
			out := make([]byte, 0, len(src)+len(to)-len(from))
			out = append(out, src[:off]...)
			out = append(out, []byte(to)...)
			out = append(out, src[off+len(from):]...)
			return out, nil
		}
	}
	return nil, fmt.Errorf("no top-level newServer function found")
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
