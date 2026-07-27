// Package queries holds the data plane's SQL. It has no Go source: this
// test-only package exists to enforce structural rules on the .sql files
// that no compiler or linter would otherwise check.
package queries

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// namedTransitions is the complete set of statements permitted to write
// `status` on an artifact row, from the design's D5 rejection matrix.
//
// Adding a name here is the reviewable act. Design D4 says there is no
// generic status update -- the point of the rule is that a future
// SetArtifactStatus fails the build rather than passing review on a quiet
// day, and quietly extending this map is the only way to defeat it.
var namedTransitions = map[string]bool{
	// Creation writes the initial 'draft', which is a status write by any
	// honest reading of the rule.
	"CreateManagementArtifact": true,

	"AcceptManagementArtifact":     true,
	"AcceptManagementAmendment":    true,
	"InvalidateManagementArtifact": true,
	"SupersedeManagementArtifact":  true,
	"ArchiveManagementArtifact":    true,
}

var (
	queryNamePattern  = regexp.MustCompile(`(?m)^--\s*name:\s*(\w+)\s*:(\w+)\s*$`)
	lineCommentCutter = regexp.MustCompile(`--[^\n]*`)
	statusAssignment  = regexp.MustCompile(`(?i)\bstatus\s*=`)
	statusColumn      = regexp.MustCompile(`(?i)(^|[\s,(])status([\s,)]|$)`)
)

// statement is one sqlc query: its name and its SQL with comments removed.
type statement struct {
	name string
	file string
	sql  string
}

func loadStatements(t *testing.T) []statement {
	t.Helper()

	paths, err := filepath.Glob("*.sql")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no .sql files found; this test would pass vacuously")
	}

	var statements []statement
	for _, path := range paths {
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read %s: %v", path, readErr)
		}
		contents := string(raw)

		matches := queryNamePattern.FindAllStringSubmatchIndex(contents, -1)
		if len(matches) == 0 {
			t.Fatalf("%s contains no sqlc query annotations", path)
		}
		for i, match := range matches {
			end := len(contents)
			if i+1 < len(matches) {
				end = matches[i+1][0]
			}
			body := contents[match[1]:end]
			statements = append(statements, statement{
				name: contents[match[2]:match[3]],
				file: path,
				sql:  lineCommentCutter.ReplaceAllString(body, " "),
			})
		}
	}
	return statements
}

// TestOnlyNamedTransitionsWriteStatus is design D4's enforcement.
func TestOnlyNamedTransitionsWriteStatus(t *testing.T) {
	statements := loadStatements(t)

	var checked int
	for _, stmt := range statements {
		if !writesStatus(stmt.sql) {
			continue
		}
		checked++
		if !namedTransitions[stmt.name] {
			t.Errorf("%s: query %q writes `status` but is not a named transition.\n"+
				"There is no generic status update (design D4). Either express this as one of the "+
				"named transitions, or -- if it genuinely is a new lifecycle transition -- add it to "+
				"the rejection matrix in design_queries_artifacts.md, give it its own preconditions, "+
				"and then add it to namedTransitions here.", stmt.file, stmt.name)
		}
	}

	// Guard against the rule silently going quiet. If a refactor renamed
	// the status column or restructured the files so nothing matched, every
	// check above would pass while enforcing nothing.
	if checked == 0 {
		t.Fatal("no status-writing statements were found at all, so this test enforced nothing")
	}
}

// TestEveryNamedTransitionExists is the other direction: a name left in the
// allow-list after its query was deleted or renamed silently widens what is
// permitted, since the entry stays available for a future query to reuse.
func TestEveryNamedTransitionExists(t *testing.T) {
	statements := loadStatements(t)

	found := map[string]bool{}
	for _, stmt := range statements {
		if writesStatus(stmt.sql) {
			found[stmt.name] = true
		}
	}
	for name := range namedTransitions {
		if !found[name] {
			t.Errorf("namedTransitions permits %q, but no query by that name writes status; "+
				"remove the stale entry rather than leaving it available for reuse", name)
		}
	}
}

// TestCreateDoesNotParameteriseStatus closes the way in that is not a
// transition at all. If creation took status as a parameter, a caller could
// insert an already-accepted artifact and skip the entire review lifecycle
// -- no transition, no review record, no digest binding -- without ever
// touching a statement this file's other rules police.
func TestCreateDoesNotParameteriseStatus(t *testing.T) {
	var checked int
	for _, stmt := range loadStatements(t) {
		if !strings.HasPrefix(stmt.name, "Create") || !writesStatus(stmt.sql) {
			continue
		}
		checked++
		values := valuesClause(stmt.sql)
		if values == "" {
			t.Fatalf("%s: could not find the VALUES clause of %q", stmt.file, stmt.name)
		}
		if !strings.Contains(strings.ToLower(values), "'draft'") {
			t.Errorf("%s: %q does not insert the literal 'draft'; creation must not let a caller "+
				"choose a status, or review can be skipped entirely", stmt.file, stmt.name)
		}
	}
	if checked == 0 {
		t.Fatal("no status-writing Create statement was found, so this test enforced nothing")
	}
}

// writesStatus reports whether a statement assigns the status column, as
// opposed to merely reading it in a WHERE clause.
func writesStatus(sql string) bool {
	upper := strings.ToUpper(sql)

	switch {
	case strings.Contains(upper, "UPDATE "):
		// Only the SET clause counts. `WHERE status = 'draft'` is a
		// precondition read and must not be flagged, or every transition
		// would trip the rule for guarding itself.
		setClause := between(sql, upper, "SET", "WHERE")
		return statusAssignment.MatchString(setClause)

	case strings.Contains(upper, "INSERT "):
		columns := between(sql, upper, "(", ")")
		return statusColumn.MatchString(columns)
	}
	return false
}

// valuesClause returns the text inside an INSERT's VALUES (...) list.
func valuesClause(sql string) string {
	upper := strings.ToUpper(sql)
	index := strings.Index(upper, "VALUES")
	if index < 0 {
		return ""
	}
	rest := sql[index:]
	open := strings.Index(rest, "(")
	closeAt := strings.LastIndex(rest, ")")
	if open < 0 || closeAt <= open {
		return ""
	}
	return rest[open+1 : closeAt]
}

// between returns the text of sql lying between the first occurrence of
// open and the following close, matching against the upper-cased copy so
// the search is case-insensitive while the returned text is not.
func between(sql, upper, open, closing string) string {
	start := strings.Index(upper, open)
	if start < 0 {
		return ""
	}
	start += len(open)
	end := strings.Index(upper[start:], closing)
	if end < 0 {
		return sql[start:]
	}
	return sql[start : start+end]
}
