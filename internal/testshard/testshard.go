// Package testshard discovers which CI shard each integration test in a
// package belongs to, so a workflow can split one slow package across
// runner VMs with a `-run` pattern it COMPUTES rather than one somebody
// wrote down.
//
// The assignment lives beside each test, as a directive in its doc comment:
//
//	//ci:shard 2
//	func TestBackupRestoreRoundTrip(t *testing.T) {
//
// and this package reads it back from the AST. The alternative — a `-run`
// alternation in the workflow file, or a table of names in a test — is an
// enumeration nobody edits while writing Go, and the stack suite has
// already been bitten by that shape three times (a lock table that omitted
// three verbs, a marker table that would have, a CI `-run` pattern widened
// twice in two review rounds). Its failure is the quietest kind: a new test
// passes locally and is never run in CI, and the suite stays green.
//
// So the rules are strict, and every one of them is a way that failure was
// observed or is one edit away:
//
//   - A test in a file compiled ONLY under the tag must name exactly one
//     shard. Naming none means it runs in no shard; naming two means it runs
//     twice and the count the verdict expects is wrong.
//   - A test in any other file must name no shard: it runs in the unit
//     suite, and a directive on it claims a placement that does not exist.
//   - A directive must be in a test's doc comment. Separated from the
//     function by a blank line it is no longer the doc comment, the test has
//     no shard, and the file reads as if it had one.
//   - Shard ids are consecutive from zero and no shard is empty. The
//     workflow matrix is the same list, so an id nobody runs is an error
//     here rather than a job that never starts.
//
// Errors are collected rather than returned one at a time, so the guard
// that runs this reports every unassigned test in one round.
package testshard

import (
	"errors"
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/doc"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Directive is the comment prefix that assigns a test to a shard. The form
// `//word:word` is what gofmt and godoc recognise as a directive: neither
// reformats it, and it does not appear in the package documentation.
const Directive = "//ci:shard"

// Assignment is the discovered shard membership of one package's
// integration tests.
type Assignment struct {
	// Shards holds each shard's test names, sorted, indexed by shard id.
	Shards [][]string
}

// Count reports how many tests the shard holds. Subtests are not counted:
// the value is a LOWER bound on the passes a run of the shard produces,
// which is what a verdict that refuses an empty result needs.
func (a *Assignment) Count(shard int) int {
	return len(a.Shards[shard])
}

// RunPattern returns the `go test -run` pattern that selects exactly the
// shard's tests. It is anchored at both ends: `go test` matches -run
// unanchored, and one test's name is often another's prefix.
func (a *Assignment) RunPattern(shard int) string {
	quoted := make([]string, 0, len(a.Shards[shard]))
	for _, name := range a.Shards[shard] {
		quoted = append(quoted, regexp.QuoteMeta(name))
	}
	return "^(" + strings.Join(quoted, "|") + ")$"
}

// Discover reads the `_test.go` files in dir and returns the shard
// assignment of the tests that exist only under the build tag, or an error
// describing every violation of the rules above.
//
// Build constraints are evaluated with the given tag satisfied and nothing
// else. A constraint that mentions any other tag — an OS, an architecture,
// a second feature tag — is refused rather than guessed at, because which
// tests such a file contributes would depend on where this ran. The same
// goes for the constraint Go reads from a FILE NAME (`x_linux_test.go`,
// `x_amd64_test.go`, `x_linux_amd64_test.go`): it is as real as a
// `//go:build` line and quieter, so it is refused by the same rule. Files
// Go ignores outright — names starting with `_` or `.` — are skipped.
func Discover(dir, tag string) (*Assignment, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read the package directory: %w", err)
	}

	fileSet := token.NewFileSet()
	byShard := map[int][]string{}
	var problems []error
	taggedTests := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, "_test.go") || name[0] == '_' || name[0] == '.' {
			continue
		}
		if platform := fileNameConstraint(name); platform != "" {
			problems = append(problems, fmt.Errorf("%s: the file name is a build constraint (%s), which this tool does not evaluate",
				filepath.Join(dir, name), platform))
			continue
		}
		found, tagged, fileProblems := discoverFile(fileSet, filepath.Join(dir, name), tag)
		problems = append(problems, fileProblems...)
		taggedTests += tagged
		for name, shard := range found {
			byShard[shard] = append(byShard[shard], name)
		}
	}
	if taggedTests == 0 && len(problems) == 0 {
		problems = append(problems, fmt.Errorf("no test exists only under the %q tag in %s: nothing to shard, and a guard on this is enforcing nothing", tag, dir))
	}

	assignment, shardProblems := buildShards(byShard)
	problems = append(problems, shardProblems...)
	if joined := errors.Join(problems...); joined != nil {
		return nil, fmt.Errorf("shard assignment under %s:\n%w", dir, joined)
	}
	return assignment, nil
}

// discoverFile parses one test file and returns the shard of each assigned
// test, how many of its tests exist only under the tag, and its problems.
func discoverFile(fileSet *token.FileSet, path, tag string) (found map[string]int, taggedTests int, problems []error) {
	file, err := parser.ParseFile(fileSet, path, nil, parser.ParseComments)
	if err != nil {
		return nil, 0, []error{err}
	}
	included, tagOnly, err := inclusion(fileSet, file, tag)
	if err != nil {
		return nil, 0, []error{err}
	}
	// A file the tag excludes (`//go:build !integration`) contributes no
	// tests, but its directives are still checked: a shard named on a test
	// that never runs in the shard job is a claim about CI that is false.
	found, problems = assignFile(fileSet, file, included && tagOnly)
	if !included {
		return nil, 0, problems
	}
	if tagOnly {
		taggedTests = countTests(file)
	}
	return found, taggedTests, problems
}

// buildShards turns the per-shard name lists into an Assignment indexed by
// id, reporting every id below the highest that has no tests.
//
// Consecutive non-empty ids from 0 mean the highest id is below the number
// of assigned tests, so an id at or above that count is refused BEFORE the
// slice is sized by it: `//ci:shard 1000000000` is a typo, and the promised
// diagnostic is worth more than an out-of-memory failure on the way to it.
func buildShards(byShard map[int][]string) (*Assignment, []error) {
	assignment := &Assignment{}
	var problems []error
	assigned := 0
	for _, names := range byShard {
		assigned += len(names)
	}
	for id, names := range byShard {
		if id >= assigned {
			problems = append(problems, fmt.Errorf("shard %d is out of range: %d tests are assigned, so consecutive ids from 0 cannot reach it (%s)",
				id, assigned, strings.Join(names, ", ")))
			delete(byShard, id)
		}
	}
	for id := range byShard {
		if id >= len(assignment.Shards) {
			assignment.Shards = append(assignment.Shards, make([][]string, id+1-len(assignment.Shards))...)
		}
	}
	for id := range assignment.Shards {
		names := byShard[id]
		if len(names) == 0 {
			problems = append(problems, fmt.Errorf("shard %d is empty: ids must be consecutive from 0, and the workflow matrix lists every one", id))
			continue
		}
		sort.Strings(names)
		assignment.Shards[id] = names
	}
	return assignment, problems
}

// fileNameConstraint reports the platform constraint Go reads from a file
// name, or "" when there is none. Go's rule (go/build's goodOSArchFile):
// after dropping `_test`, a trailing `_GOOS`, `_GOARCH` or `_GOOS_GOARCH`
// element constrains the file. `unix` is a tag, not a file-name element.
func fileNameConstraint(name string) string {
	stem := strings.TrimSuffix(strings.TrimSuffix(name, ".go"), "_test")
	parts := strings.Split(stem, "_")
	if len(parts) < 2 {
		return ""
	}
	last := parts[len(parts)-1]
	if knownArch[last] {
		if len(parts) >= 3 && knownOS[parts[len(parts)-2]] {
			return "GOOS=" + parts[len(parts)-2] + " GOARCH=" + last
		}
		return "GOARCH=" + last
	}
	if knownOS[last] {
		return "GOOS=" + last
	}
	return ""
}

// The lists go/build keeps unexported (internal/syslist, checked against Go 1.26).
//
//nolint:gochecknoglobals // Fixed vocabulary of the toolchain.
var knownOS = set("aix", "android", "darwin", "dragonfly", "freebsd", "hurd", "illumos", "ios", "js",
	"linux", "nacl", "netbsd", "openbsd", "plan9", "solaris", "wasip1", "windows", "zos")

//nolint:gochecknoglobals // Fixed vocabulary of the toolchain.
var knownArch = set("386", "amd64", "amd64p32", "arm", "armbe", "arm64", "arm64be", "loong64", "mips",
	"mipsle", "mips64", "mips64le", "mips64p32", "mips64p32le", "ppc", "ppc64", "ppc64le", "riscv",
	"riscv64", "s390", "s390x", "sparc", "sparc64", "wasm")

func set(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}

// inclusion reports whether the file is compiled with the tag set, and
// whether it is compiled ONLY then.
func inclusion(fileSet *token.FileSet, file *ast.File, tag string) (included, tagOnly bool, err error) {
	expr, found, err := buildConstraint(fileSet, file)
	if err != nil {
		return false, false, err
	}
	if !found {
		return true, false, nil
	}
	if foreign := foreignTags(expr, tag); len(foreign) > 0 {
		return false, false, fmt.Errorf("%s: build constraint mentions %s, which this tool does not evaluate",
			fileSet.Position(file.Pos()).Filename, strings.Join(foreign, ", "))
	}
	withTag := expr.Eval(func(t string) bool { return t == tag })
	without := expr.Eval(func(string) bool { return false })
	return withTag, withTag && !without, nil
}

// buildConstraint returns the file's `//go:build` expression and whether it
// has one. Only comments before the package clause can be constraints.
//
// A legacy `// +build` line is refused. Go 1.26 still honours a file that
// has only the legacy form — `go list -tags=integration` includes it,
// `go test` runs it, vet says nothing — so reading only `//go:build` would
// take such a file for an untagged one and shard none of its tests, with
// no error: the exact silence this package exists to remove. gofmt has
// written the `//go:build` form since Go 1.17; a file without it is fixed
// by running gofmt, not by teaching this tool a second grammar.
func buildConstraint(fileSet *token.FileSet, file *ast.File) (constraint.Expr, bool, error) {
	var header []*ast.Comment
	for _, group := range file.Comments {
		if group.Pos() > file.Package {
			break
		}
		header = append(header, group.List...)
	}
	for _, comment := range header {
		if constraint.IsPlusBuild(comment.Text) {
			return nil, false, fmt.Errorf("%s: legacy `// +build` constraint; write it as `//go:build` (gofmt adds the line)",
				fileSet.Position(comment.Pos()))
		}
	}
	for _, comment := range header {
		if !constraint.IsGoBuild(comment.Text) {
			continue
		}
		expr, err := constraint.Parse(comment.Text)
		if err != nil {
			return nil, false, fmt.Errorf("%s: %w", fileSet.Position(comment.Pos()), err)
		}
		return expr, true, nil
	}
	return nil, false, nil
}

// foreignTags lists the tags an expression mentions other than the one
// being evaluated, sorted.
func foreignTags(expr constraint.Expr, tag string) []string {
	seen := map[string]bool{}
	var walk func(constraint.Expr)
	walk = func(e constraint.Expr) {
		switch node := e.(type) {
		case *constraint.TagExpr:
			if node.Tag != tag {
				seen[node.Tag] = true
			}
		case *constraint.NotExpr:
			walk(node.X)
		case *constraint.AndExpr:
			walk(node.X)
			walk(node.Y)
		case *constraint.OrExpr:
			walk(node.X)
			walk(node.Y)
		}
	}
	walk(expr)
	tags := make([]string, 0, len(seen))
	for t := range seen {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	return tags
}

// assignFile reads the directives of one file. It returns the shard of each
// test that has exactly one, and a problem for every rule the file breaks.
func assignFile(fileSet *token.FileSet, file *ast.File, tagOnly bool) (map[string]int, []error) {
	found := map[string]int{}
	var problems []error
	attached := map[*ast.Comment]bool{}

	runnable := runnableExamples(file)
	for _, decl := range file.Decls {
		fn, isFunc := decl.(*ast.FuncDecl)
		if !isFunc || !isSelectable(fn, runnable) {
			continue
		}
		var directives []*ast.Comment
		if fn.Doc != nil {
			for _, comment := range fn.Doc.List {
				if isDirective(comment.Text) {
					directives = append(directives, comment)
					attached[comment] = true
				}
			}
		}
		shard, err := shardOf(fileSet, fn, directives, tagOnly)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		if shard >= 0 {
			found[fn.Name.Name] = shard
		}
	}

	for _, group := range file.Comments {
		for _, comment := range group.List {
			if isDirective(comment.Text) && !attached[comment] {
				problems = append(problems, fmt.Errorf("%s: `%s` is not in a test's doc comment: it assigns nothing (a blank line before `func` detaches it)",
					fileSet.Position(comment.Pos()), strings.TrimSpace(comment.Text)))
			}
		}
	}
	return found, problems
}

// shardOf applies the per-test rules to one test's directives. It returns
// -1 for a unit test, which has no shard and must not claim one.
func shardOf(fileSet *token.FileSet, fn *ast.FuncDecl, directives []*ast.Comment, tagOnly bool) (int, error) {
	where := fileSet.Position(fn.Pos())
	switch {
	case !tagOnly && len(directives) > 0:
		return -1, fmt.Errorf("%s: %s names a shard but is not an integration-only test; it runs in the unit suite",
			where, fn.Name.Name)
	case !tagOnly:
		return -1, nil
	case len(directives) == 0:
		return -1, fmt.Errorf("%s: %s names no shard: add `%s N` to its doc comment or it runs in no CI job",
			where, fn.Name.Name, Directive)
	case len(directives) > 1:
		return -1, fmt.Errorf("%s: %s names %d shards; it must be in exactly one",
			where, fn.Name.Name, len(directives))
	}
	shard, err := parseDirective(directives[0].Text)
	if err != nil {
		return -1, fmt.Errorf("%s: %s: %w", fileSet.Position(directives[0].Pos()), fn.Name.Name, err)
	}
	return shard, nil
}

// countTests counts the top-level functions in a file that a `go test
// -run` pattern can select.
func countTests(file *ast.File) int {
	runnable := runnableExamples(file)
	n := 0
	for _, decl := range file.Decls {
		if fn, isFunc := decl.(*ast.FuncDecl); isFunc && isSelectable(fn, runnable) {
			n++
		}
	}
	return n
}

// isSelectable reports whether `go test -run` selects AND executes the
// function in an ordinary run, which is what a shard pattern has to cover:
// `TestX`, `FuzzX` (its seed corpus runs, and reports a pass) and an
// `ExampleX` that has an output comment. Verified on Go 1.26 with a
// package holding one of each: those three report pass events; an example
// with no output comment and a benchmark do not run. TestMain is never
// one. The name rule is `go test`'s: the prefix followed by something that
// is not a lower-case letter, or nothing.
func isSelectable(fn *ast.FuncDecl, runnableExamples map[string]bool) bool {
	name := fn.Name.Name
	if fn.Recv != nil || name == "TestMain" {
		return false
	}
	for _, prefix := range []string{"Test", "Fuzz", "Example"} {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		if prefix == "Example" && !runnableExamples[name] {
			return false
		}
		if len(name) == len(prefix) {
			return true
		}
		r, _ := utf8.DecodeRuneInString(name[len(prefix):])
		return !unicode.IsLower(r)
	}
	return false
}

// runnableExamples names the examples in a file that `go test` executes:
// those with an output comment, empty or not. go/doc applies the same
// parse the test runner does; its Name drops the `Example` prefix (a bare
// `Example` is ""), so the function name is rebuilt here.
func runnableExamples(file *ast.File) map[string]bool {
	runnable := map[string]bool{}
	for _, example := range doc.Examples(file) {
		if example.Output != "" || example.EmptyOutput {
			runnable["Example"+example.Name] = true
		}
	}
	return runnable
}

// isDirective reports whether a comment line is a shard directive, well
// formed or not. `//ci:sharding` is not one; `//ci:shard` alone is a
// malformed one, which parseDirective reports.
func isDirective(text string) bool {
	rest, ok := strings.CutPrefix(text, Directive)
	return ok && (rest == "" || rest[0] == ' ' || rest[0] == '\t')
}

// parseDirective reads the shard id out of a directive line.
func parseDirective(text string) (int, error) {
	arg := strings.TrimSpace(strings.TrimPrefix(text, Directive))
	shard, err := strconv.Atoi(arg)
	if err != nil || shard < 0 {
		return 0, fmt.Errorf("malformed directive %q: want `%s N` with N a non-negative integer", strings.TrimSpace(text), Directive)
	}
	return shard, nil
}
