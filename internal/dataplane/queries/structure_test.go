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

// namedTransitions maps each statement permitted to write `status` to the
// single literal destination it may write, from the design's D5 rejection
// matrix.
//
// Mapping to a LITERAL rather than to a bool is what makes the rule hold.
// An allow-list of names only checks who may write status, not what they
// may write it to -- so rewriting an approved transition's SET clause to
// `status = @status` keeps its allow-listed name, passes a name-only
// check, and hands sqlc a generic status parameter: exactly the generic
// update D4 forbids, reached by editing a permitted statement instead of
// adding a new one.
//
// Adding an entry here is the reviewable act, and it now requires naming
// the destination as well as the name.
var namedTransitions = map[string]string{
	// Creation writes the initial 'draft', which is a status write by any
	// honest reading of the rule.
	"CreateManagementArtifact": "draft",

	"AcceptManagementArtifact":     "accepted",
	"AcceptManagementAmendment":    "accepted",
	"InvalidateManagementArtifact": "invalidated",
	"SupersedeManagementArtifact":  "superseded",
	"ArchiveManagementArtifact":    "archived",
}

var (
	queryNamePattern  = regexp.MustCompile(`(?m)^--\s*name:\s*(\w+)\s*:(\w+)\s*$`)
	lineCommentCutter = regexp.MustCompile(`--[^\n]*`)
	statusAssignment  = regexp.MustCompile(`(?i)\bstatus\s*=`)
	statusColumn      = regexp.MustCompile(`(?i)(^|[\s,(])status([\s,)]|$)`)

	// Captures the assigned expression: a quoted literal, or anything up to
	// the next comma -- so `@status` and `r.status` are captured and then
	// fail the literal comparison rather than slipping through unmatched.
	statusAssignmentValue = regexp.MustCompile(`(?i)\bstatus\s*=\s*('[^']*'|[^,]+)`)

	// onceOnlyGuard is the predicate that makes completion once-only.
	onceOnlyGuard = regexp.MustCompile(`(?i)finished_at\s+IS\s+NULL`)
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

		want, permitted := namedTransitions[stmt.name]
		if !permitted {
			t.Errorf("%s: query %q writes `status` but is not a named transition.\n"+
				"There is no generic status update (design D4). Either express this as one of the "+
				"named transitions, or -- if it genuinely is a new lifecycle transition -- add it to "+
				"the rejection matrix in design_queries_artifacts.md, give it its own preconditions, "+
				"and then add it to namedTransitions here.", stmt.file, stmt.name)
			continue
		}

		if isInsert(stmt.sql) {
			// An INSERT has no SET clause. Its destination is verified
			// against the VALUES clause by TestInsertedStatusIsALiteral,
			// which covers every status-writing INSERT -- so skipping here
			// delegates the check rather than dropping it.
			continue
		}

		got := assignedStatus(stmt.sql)
		if got != "'"+want+"'" {
			t.Errorf("%s: query %q writes status = %s, want the literal '%s'.\n"+
				"A named transition must write one fixed destination. Taking status from a parameter "+
				"turns an approved transition into the generic update D4 forbids, without adding a "+
				"single new query.", stmt.file, stmt.name, describeAssignment(got), want)
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
	for name := range namedTransitions { //nolint:gocritic // keys only
		if !found[name] {
			t.Errorf("namedTransitions permits %q, but no query by that name writes status; "+
				"remove the stale entry rather than leaving it available for reuse", name)
		}
	}
}

// TestInsertedStatusIsALiteral closes the way in that is not a transition
// at all. If an INSERT took status as a parameter, a caller could create an
// already-accepted artifact and skip the entire review lifecycle -- no
// transition, no review record, no digest binding -- without ever touching
// a statement this file's other rules police.
//
// It covers every status-writing INSERT, not only Create-prefixed ones, so
// the shared loop above can delegate to it without leaving a gap.
func TestInsertedStatusIsALiteral(t *testing.T) {
	var checked int
	for _, stmt := range loadStatements(t) {
		if !isInsert(stmt.sql) || !writesStatus(stmt.sql) {
			continue
		}
		checked++
		values := valuesClause(stmt.sql)
		if values == "" {
			t.Fatalf("%s: could not find the VALUES clause of %q", stmt.file, stmt.name)
		}
		want := "'" + namedTransitions[stmt.name] + "'"
		if !strings.Contains(strings.ToLower(values), want) {
			t.Errorf("%s: %q does not insert the literal %s; creation must not let a caller "+
				"choose a status, or review can be skipped entirely", stmt.file, stmt.name, want)
		}
	}
	if checked == 0 {
		t.Fatal("no status-writing INSERT was found, so this test enforced nothing")
	}
}

// assignedStatus returns the right-hand side of an UPDATE's `status =`
// assignment.
func assignedStatus(sql string) string {
	upper := strings.ToUpper(sql)
	match := statusAssignmentValue.FindStringSubmatch(between(sql, upper, "SET", "WHERE"))
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func describeAssignment(got string) string {
	if got == "" {
		return "an unparseable expression"
	}
	return got
}

// isInsert reports whether a statement is an INSERT rather than an UPDATE.
func isInsert(sql string) bool {
	return strings.Contains(strings.ToUpper(sql), "INSERT ")
}

// callTables are the two tables with an open → completed lifecycle. Their
// mutability surface is enforced structurally, for the same reason
// namedTransitions guards artifact status: "no other column is updatable"
// is an intention until something fails the build.
var callTables = []string{"llm_calls", "tool_calls"}

// namedCompletions are the only statements permitted to UPDATE a call.
// Each must carry the once-only guard.
var namedCompletions = map[string]bool{
	"CompleteLLMCall":  true,
	"CompleteToolCall": true,
}

// outcomeColumns must never appear in a call's INSERT column list.
//
// Not just the three lifecycle flags: an outcome is everything a call
// learns by ENDING. Tokens, cost and a tool's result are all outcome, so a
// creation naming any of them writes a terminal row directly and bypasses
// the once-only guard -- and unlike a status column, nothing about a call
// record's shape makes that obvious in review.
var outcomeColumns = []string{
	"finished_at", "succeeded", "error_message",
	"input_tokens", "output_tokens", "reasoning_tokens", "cached_tokens", "cost_usd",
	"result",
}

// completionSetColumns are the ONLY columns a completion may assign.
//
// An allow-listed completion could otherwise rewrite the request side --
// provider, model, arguments -- or the lineage, mutating history through a
// statement that passes the name check. What a call was asked to do is not
// something ending it can change.
var completionSetColumns = map[string]bool{
	"finished_at": true, "succeeded": true, "error_message": true,
	"input_tokens": true, "output_tokens": true, "reasoning_tokens": true,
	"cached_tokens": true, "cost_usd": true, "result": true,
}

// bornFinalTables are written once and never updated or deleted outside
// truncation. The asymmetry with the call tables is the point.
var bornFinalTables = []string{"metric_events", "audit_events"}

// namedTruncations are the only statements permitted to DELETE from the
// call or event families. Deletion is retention policy (design D6), not
// something any query may do incidentally.
var namedTruncations = map[string]bool{
	"TruncateAuditEvents":    true,
	"TruncateMetricEvents":   true,
	"TruncateAuditArtifacts": true,
	"TruncateToolCalls":      true,
	"TruncateLLMCalls":       true,
}

// TestCallsAreCreatedOpenAndCompletedOnce enforces the call family's
// mutability surface.
func TestCallsAreCreatedOpenAndCompletedOnce(t *testing.T) {
	statements := loadStatements(t)

	var inserts, updates int
	for _, stmt := range statements {
		table := callTableWritten(stmt.sql)
		if table == "" {
			continue
		}

		switch {
		case isInsert(stmt.sql):
			inserts++
			columns := between(stmt.sql, strings.ToUpper(stmt.sql), "(", ")")
			for _, forbidden := range outcomeColumns {
				if columnPattern(forbidden).MatchString(columns) {
					t.Errorf("%s: %q inserts %s into %s. A call must be created OPEN: writing a terminal "+
						"row directly bypasses the once-only completion guard, and Audit history becomes "+
						"mutable through a path no lifecycle rule polices.",
						stmt.file, stmt.name, forbidden, table)
				}
			}

		case strings.Contains(strings.ToUpper(stmt.sql), "UPDATE "):
			updates++
			if !namedCompletions[stmt.name] {
				t.Errorf("%s: %q updates %s but is not a named completion. There is no generic update on a "+
					"call record; add a completion to namedCompletions here only if it genuinely is one.",
					stmt.file, stmt.name, table)
				continue
			}
			// The guard must be in the WHERE clause, not merely somewhere in
			// the statement: the phrase appears in SET expressions and in
			// comments too, and matching those would accept a completion
			// with no guard at all.
			where := between(stmt.sql, strings.ToUpper(stmt.sql), "WHERE", ";")
			if !onceOnlyGuard.MatchString(where) {
				t.Errorf("%s: %q updates %s without `finished_at IS NULL` in its WHERE clause. That guard "+
					"is what makes completion once-only, so the first outcome wins when two paths observe "+
					"one call ending.", stmt.file, stmt.name, table)
			}
			// And it may only assign outcome columns.
			setClause := between(stmt.sql, strings.ToUpper(stmt.sql), "SET", "WHERE")
			for _, assigned := range assignedColumns(setClause) {
				if !completionSetColumns[assigned] {
					t.Errorf("%s: %q assigns %s while completing %s. A completion records what the call "+
						"LEARNED by ending; rewriting the request side or the lineage mutates history "+
						"through a statement that passed the name check.",
						stmt.file, stmt.name, assigned, table)
				}
			}

		case strings.Contains(strings.ToUpper(stmt.sql), "DELETE "):
			if !namedTruncations[stmt.name] {
				t.Errorf("%s: %q deletes from %s but is not a named truncation. Deletion here is retention "+
					"policy (design D6), with its own horizon and retention guards -- not something a query "+
					"may do incidentally.", stmt.file, stmt.name, table)
			}
		}
	}

	// Guard against the rule going quiet: a rename or a restructure that
	// matched nothing would leave every check above passing vacuously.
	if inserts == 0 || updates == 0 {
		t.Fatalf("found %d call inserts and %d call updates; this test enforced nothing", inserts, updates)
	}
}

// TestBornFinalTablesAreNeverUpdated covers the event tables, which the
// first version of this guard did not mention at all. They have no
// lifecycle, so an UPDATE against one is not a wrong transition -- it is a
// statement with no meaning the schema can express.
func TestBornFinalTablesAreNeverUpdated(t *testing.T) {
	var seen int
	for _, stmt := range loadStatements(t) {
		upper := strings.ToUpper(stmt.sql)
		for _, table := range bornFinalTables {
			name := strings.ToUpper(table)
			if strings.Contains(upper, "INTO "+name) || strings.Contains(upper, "FROM "+name) {
				seen++
			}
			if strings.Contains(upper, "UPDATE "+name) {
				t.Errorf("%s: %q updates %s, which is born final and has no lifecycle to move through",
					stmt.file, stmt.name, table)
			}
			if strings.Contains(upper, "DELETE ") && strings.Contains(upper, "FROM "+name) &&
				!namedTruncations[stmt.name] {
				t.Errorf("%s: %q deletes from %s but is not a named truncation", stmt.file, stmt.name, table)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no statement touched a born-final table, so this test enforced nothing")
	}
}

// TestEveryNamedCompletionExists is the other direction: a name left here
// after its query was renamed silently widens what may update a call.
func TestEveryNamedCompletionExists(t *testing.T) {
	found := map[string]bool{}
	for _, stmt := range loadStatements(t) {
		if callTableWritten(stmt.sql) != "" && strings.Contains(strings.ToUpper(stmt.sql), "UPDATE ") {
			found[stmt.name] = true
		}
	}
	for name := range namedCompletions {
		if !found[name] {
			t.Errorf("namedCompletions permits %q, but no query by that name updates a call table; "+
				"remove the stale entry rather than leaving it available for reuse", name)
		}
	}
}

// callTableWritten returns the call table a statement writes, or "".
func callTableWritten(sql string) string {
	upper := strings.ToUpper(sql)
	for _, table := range callTables {
		name := strings.ToUpper(table)
		for _, verb := range []string{"INTO " + name, "UPDATE " + name, "FROM " + name} {
			if strings.Contains(upper, verb) {
				return table
			}
		}
	}
	return ""
}

// assignedColumns lists the columns a SET clause assigns.
func assignedColumns(setClause string) []string {
	var assigned []string
	for _, part := range strings.Split(setClause, ",") {
		name, _, found := strings.Cut(part, "=")
		if !found {
			continue
		}
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			assigned = append(assigned, strings.ToLower(trimmed))
		}
	}
	return assigned
}

// columnPattern matches a column name as a whole word in a column list.
func columnPattern(column string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)(^|[\s,(])` + column + `([\s,)]|$)`)
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
