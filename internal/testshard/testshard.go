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
// tests such a file contributes would depend on where this ran.
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
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		found, tagged, fileProblems := discoverFile(fileSet, filepath.Join(dir, entry.Name()), tag)
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
	if !included {
		return nil, 0, nil
	}
	found, problems = assignFile(fileSet, file, tagOnly)
	if tagOnly {
		taggedTests = countTests(file)
	}
	return found, taggedTests, problems
}

// buildShards turns the per-shard name lists into an Assignment indexed by
// id, reporting every id below the highest that has no tests.
func buildShards(byShard map[int][]string) (*Assignment, []error) {
	assignment := &Assignment{}
	for id := range byShard {
		if id >= len(assignment.Shards) {
			assignment.Shards = append(assignment.Shards, make([][]string, id+1-len(assignment.Shards))...)
		}
	}
	var problems []error
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
func buildConstraint(fileSet *token.FileSet, file *ast.File) (constraint.Expr, bool, error) {
	for _, group := range file.Comments {
		if group.Pos() > file.Package {
			break
		}
		for _, comment := range group.List {
			if !constraint.IsGoBuild(comment.Text) {
				continue
			}
			expr, err := constraint.Parse(comment.Text)
			if err != nil {
				return nil, false, fmt.Errorf("%s: %w", fileSet.Position(comment.Pos()), err)
			}
			return expr, true, nil
		}
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

	for _, decl := range file.Decls {
		fn, isFunc := decl.(*ast.FuncDecl)
		if !isFunc || !isTest(fn) {
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

// countTests counts the top-level test functions in a file.
func countTests(file *ast.File) int {
	n := 0
	for _, decl := range file.Decls {
		if fn, isFunc := decl.(*ast.FuncDecl); isFunc && isTest(fn) {
			n++
		}
	}
	return n
}

// isTest applies `go test`'s own rule: a top-level function named `Test`
// followed by something that is not a lower-case letter, and not TestMain.
func isTest(fn *ast.FuncDecl) bool {
	name := fn.Name.Name
	if fn.Recv != nil || name == "TestMain" || !strings.HasPrefix(name, "Test") {
		return false
	}
	if len(name) == len("Test") {
		return true
	}
	r, _ := utf8.DecodeRuneInString(name[len("Test"):])
	return !unicode.IsLower(r)
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
