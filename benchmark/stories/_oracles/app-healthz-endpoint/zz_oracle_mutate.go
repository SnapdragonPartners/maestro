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

// wrapper is appended after newServer is renamed to zzOrigNewServer. It breaks
// only the /healthz response, leaving construction (and every other route)
// intact.
const wrapper = `

func newServer(options []ModelOption, logger *log.Logger) http.Handler {
	inner := zzOrigNewServer(options, logger)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		inner.ServeHTTP(w, r)
	})
}
`

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

	if err := os.WriteFile(serverPath, append(renamed, []byte(wrapper)...), 0o644); err != nil {
		checkBug("write mutant: %v", err)
	}
	// Compile-before-detection: a mutant that does not build is a broken check.
	if out, err := run(scratch, "go", "build", "./..."); err != nil {
		checkBug("mutant did not compile:\n%s", out)
	}
	// The agent's test: PASS against the mutant means it did not notice /healthz
	// was broken, so it is not a real behavioural test.
	if _, err := run(scratch, "go", "test", "-count=1", "./..."); err == nil {
		fmt.Fprintln(os.Stderr, "authored test did not detect a broken /healthz response — it is not behavioural")
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
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Recv != nil || fd.Name.Name != from {
			continue
		}
		off := fset.Position(fd.Name.Pos()).Offset
		out := make([]byte, 0, len(src)+len(to)-len(from))
		out = append(out, src[:off]...)
		out = append(out, []byte(to)...)
		out = append(out, src[off+len(from):]...)
		return out, nil
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
