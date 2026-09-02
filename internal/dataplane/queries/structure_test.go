// Package queries holds the data plane's SQL. It has no Go source: this
// test-only package exists to enforce structural rules on the .sql files
// that no compiler or linter would otherwise check.
package queries

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
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

// completion is one permitted UPDATE on a call table: the ONE table it may
// touch, and the exact columns it may assign there.
type completion struct {
	table   string
	columns map[string]bool
}

// namedCompletions are the only statements permitted to UPDATE a call. Each
// must carry the once-only guard.
//
// Bound to a table and to its own column set, on the same reasoning as
// namedTruncations: the names are not interchangeable. A union of both
// families' vocabularies would let CompleteToolCall assign `cost_usd` or
// CompleteLLMCall assign `outcome` -- columns that do not exist on those
// tables today, so the statement would fail at runtime rather than at review,
// and a future column of that name would be silently permitted. Worse, a
// completion pointed at the other family's table would pass a name-only
// check entirely.
//
// The asymmetry with outcomeColumns below is deliberate: a union there is
// over-STRICT, banning columns from a creation that could not appear anyway.
// A union here would be over-PERMISSIVE, which is the direction that admits
// a defect.
var namedCompletions = map[string]completion{
	"CompleteLLMCall": {
		table: "llm_calls",
		columns: map[string]bool{
			"finished_at": true, "succeeded": true, "error_message": true,
			"input_tokens": true, "output_tokens": true, "reasoning_tokens": true,
			"cache_read_tokens": true, "cache_write_tokens": true, "cost_usd": true,
		},
	},
	"CompleteToolCall": {
		table: "tool_calls",
		columns: map[string]bool{
			// `succeeded` is absent since migration 000022: settling a tool
			// call moves state and outcome together.
			"finished_at": true, "state": true, "outcome": true,
			"error_message": true, "result": true,
		},
	},
}

// outcomeColumns must never appear in a call's INSERT column list.
//
// Not just the three lifecycle flags: an outcome is everything a call
// learns by ENDING. Tokens, cost and a tool's result are all outcome, so a
// creation naming any of them writes a terminal row directly and bypasses
// the once-only guard -- and unlike a status column, nothing about a call
// record's shape makes that obvious in review.
// `state` and `outcome` are tool_calls' half of this since migration 000022;
// `succeeded` remains llm_calls'. A creation naming `state` is forbidden even
// though 'open' would be harmless, because the column defaults to it and the
// only reason to name it at creation is to write something else.
var outcomeColumns = []string{
	"finished_at", "succeeded", "state", "outcome", "error_message",
	"input_tokens", "output_tokens", "reasoning_tokens",
	"cache_read_tokens", "cache_write_tokens", "cost_usd",
	"result",
}

// bornFinalTables are written once and never updated. The asymmetry with
// the call tables is the point.
//
// audit_artifacts belongs here as much as the event tables do: ADR 0021
// makes Audit artifacts born final, with retention pinning rather than a
// lifecycle. Its absence from an earlier version of this list meant a
// generic UPDATE or DELETE against the one pinnable family in scope would
// have passed every check here.
// benchmark_runs and benchmark_attempts join in item 9 for the same reason
// and a sharper one: the import contract is that re-importing is a NO-OP.
// An update statement against either would make a second import a write, and
// the ledger row is the record that the first import happened -- rewriting it
// is how a conflicting payload would come to overwrite a good one silently.
// benchmark_reports is born final for the sharpest reason of the three: the
// row IS the decision about which artifact is a suite's report, and a claim
// that could be rewritten would let a second import retarget the claim to its
// own draft -- which is the whole of what the uniqueness was added to stop.
var bornFinalTables = []string{
	"metric_events", "audit_events", "audit_artifacts",
	"benchmark_runs", "benchmark_attempts", "benchmark_reports",
}

// namedTruncations maps each permitted DELETE to the ONE table it may
// delete from. Deletion here is retention policy (design D6), with a
// per-table horizon and its own retention guards.
//
// A name→table map rather than a name allow-list, because the names are not
// interchangeable: TruncateAuditEvents deleting from llm_calls would apply
// the wrong cutoff column and skip every retention guard, while passing a
// check that only asked whether the name was approved.
var namedTruncations = map[string]string{
	"TruncateAuditEvents":    "audit_events",
	"TruncateMetricEvents":   "metric_events",
	"TruncateAuditArtifacts": "audit_artifacts",
	"TruncateToolCalls":      "tool_calls",
	"TruncateLLMCalls":       "llm_calls",
	"TruncateAttachments":    "binary_attachments",
}

// versionedTables are mutable by design and carry optimistic concurrency
// (item 7, D1 and D5). They belong to none of the categories above: they are
// not born final, and they have no lifecycle status, so nothing else in this
// file would have looked at them at all — a generic `UPDATE secrets SET …`
// or an unguarded DELETE would have passed every rule here.
var versionedTables = []string{"configuration_records", "secrets"}

// namedVersionedMutations maps each permitted UPDATE or DELETE to the ONE
// table it may touch, on the same reasoning as namedTruncations: the names
// are not interchangeable, and a statement pointed at the other table would
// apply the wrong ownership rules while passing a name-only check.
var namedVersionedMutations = map[string]string{
	"UpdateConfigurationRecord": "configuration_records",
	"DeleteConfigurationRecord": "configuration_records",
	"ReplaceSecret":             "secrets",
	"DeleteSecret":              "secrets",
}

// versionedSetColumns are the ONLY columns these updates may assign.
//
// The exclusions carry the rule. `owner_user_id`, `name`, `scope_type` and
// the scope columns decide WHO MAY READ a secret and what it is for, so a
// statement that could rewrite them would retarget a live credential — and
// while the envelope's authenticated data makes such a row fail to decrypt,
// a defence that turns a working secret into an unreadable one is a
// backstop, not a reason to allow the statement.
// DEPENDENT CODE: postgres.ReplaceSecret reads its row WITHOUT a lock, and
// that is only sound because this allow-list makes every field it uses to
// rebuild the encryption binding — name, owner_user_id, scope_type and the
// scope columns — impossible to assign. Adding any of them here would turn
// that read into a time-of-check-to-time-of-use window silently: nothing in
// the vault's own tests would fail, because the race needs a concurrent
// writer doing something the schema does not currently permit.
//
// If one is ever added, ReplaceSecret must take a row lock first.
var versionedSetColumns = map[string]bool{
	"value": true, "scheme": true, "nonce": true, "ciphertext": true,
	"version": true, "updated_at": true,
}

var (
	expectedVersionGuard = regexp.MustCompile(`(?i)version\s*=\s*@expected_version`)
	membershipGuard      = regexp.MustCompile(`(?is)EXISTS\s*\(\s*SELECT\s+1\s+FROM\s+users`)
)

// ownershipBranches are BOTH halves of the acting-user predicate, and the
// test requires both because they fail in opposite directions.
//
// Without the individual branch a caller reaches every row in the
// organization. Without the shared branch a shared credential -- whose owner
// is NULL -- matches nobody, so the operation silently stops working for
// exactly the secrets a team holds in common. The second is not a security
// hole, which is why it would survive review: it is an availability defect
// that reports itself as "no such secret".
var ownershipBranches = []struct {
	name    string
	pattern *regexp.Regexp
	why     string
}{
	{
		name:    "the individual branch (owner_user_id = @acting_user_id)",
		pattern: regexp.MustCompile(`(?i)owner_user_id\s*=\s*@acting_user_id`),
		why:     "without it a caller reaches every row in the organization, individual ones included",
	},
	{
		name:    "the shared branch (owner_user_id IS NULL)",
		pattern: regexp.MustCompile(`(?i)owner_user_id\s+IS\s+NULL`),
		why: "without it a shared credential matches nobody, and the operation stops working for " +
			"every secret a team holds in common while reporting only 'no such secret'",
	},
}

// TestVersionedTablesAreMutatedOnlyUnderTheirGuards is the rule that would
// otherwise be a convention: every mutation of these tables is named, touches
// one table, assigns only its own columns, and carries the guards that make
// it safe.
//
// Without it "the structural tests pass" says nothing about these families,
// because no other rule in this file mentions them.
func TestVersionedTablesAreMutatedOnlyUnderTheirGuards(t *testing.T) {
	var checked int
	for _, stmt := range loadStatements(t) {
		table := writeTarget(stmt.sql)
		if !slices.Contains(versionedTables, table) {
			continue
		}
		upper := strings.ToUpper(stmt.sql)
		isUpdate := strings.Contains(upper, "UPDATE ")
		isDelete := deleteTarget(stmt.sql) == table
		if !isUpdate && !isDelete {
			continue // an INSERT, covered by its own rules below
		}
		checked++

		target, permitted := namedVersionedMutations[stmt.name]
		if !permitted {
			t.Errorf("%s: %q mutates %s but is not a named mutation. These tables carry optimistic "+
				"concurrency and, for secrets, ownership — a statement outside this list has neither "+
				"unless somebody remembered, which is what this rule replaces.",
				stmt.file, stmt.name, table)
			continue
		}
		if target != table {
			t.Errorf("%s: %q is the mutation for %s but touches %s; the names carry different "+
				"ownership rules and are not interchangeable", stmt.file, stmt.name, target, table)
		}

		where := between(stmt.sql, upper, "WHERE", ";")
		if !expectedVersionGuard.MatchString(where) {
			t.Errorf("%s: %q mutates %s without `version = @expected_version` in its WHERE. "+
				"ADR 0027 forbids resolving concurrent writes to shared state by last-writer-wins, "+
				"and an unconditional delete erases a rotation committed a moment earlier while "+
				"reporting success.", stmt.file, stmt.name, table)
		}

		if table == "secrets" {
			for _, branch := range ownershipBranches {
				if !branch.pattern.MatchString(where) {
					t.Errorf("%s: %q mutates secrets without %s — %s. Enforcing ownership on reads "+
						"alone would leave an access model where one user cannot SEE another's "+
						"credential but can freely destroy it.",
						stmt.file, stmt.name, branch.name, branch.why)
				}
			}
			if !membershipGuard.MatchString(where) {
				t.Errorf("%s: %q mutates secrets without checking the acting user's membership. "+
					"A shared secret has a NULL owner, so the ownership predicate alone is "+
					"satisfied by ANY acting id — including a user of another organization.",
					stmt.file, stmt.name)
			}
		}

		if isUpdate {
			for _, assigned := range assignedColumns(between(stmt.sql, upper, "SET", "WHERE")) {
				if !versionedSetColumns[assigned] {
					t.Errorf("%s: %q assigns %s on %s. Ownership, name and scope decide who may "+
						"read a secret and what it is for; a statement able to rewrite them "+
						"retargets a live credential.", stmt.file, stmt.name, assigned, table)
				}
			}
		}
	}

	if checked == 0 {
		t.Fatal("no mutation of a versioned table was found, so this test enforced nothing")
	}
}

// secretStatements is the EXACT set of statements permitted to touch the
// vault, and which guards each owes.
//
// An allowlist rather than a filter, because the first version of this rule
// selected statements BY the predicate it was checking for — it looked at
// reads containing `owner_user_id = @acting_user_id` and asserted they also
// carried membership. Deleting the ownership predicate from a query removed
// that query from the test rather than failing it, and the other queries
// kept the not-vacuous counter satisfied. The selector was the property.
//
// Named up front, the same deletion is a statement that touches secrets
// without its required guards, and a rename is a listed statement that does
// not exist.
var secretStatements = map[string]struct {
	ownership  bool // filters rows by the acting user
	membership bool // correlates that user with the organization
}{
	// Creation has no ownership predicate to carry: the owner is a column it
	// writes, not a row it selects. Membership is the whole guard, and the
	// reason it exists — a shared secret's owner is NULL, so nothing else in
	// the row mentions the caller.
	"CreateSecret": {ownership: false, membership: true},

	"ResolveSecretForRepository": {ownership: true, membership: true},
	"GetSecret":                  {ownership: true, membership: true},
	"ReplaceSecret":              {ownership: true, membership: true},
	"DeleteSecret":               {ownership: true, membership: true},
}

// membershipCorrelations are both halves the EXISTS must carry. An
// `EXISTS (SELECT 1 FROM users …)` that correlated only the user id would
// pass a shape check while proving nothing about the tenant, and one that
// correlated only the organization would prove nothing about the caller.
var membershipCorrelations = []*regexp.Regexp{
	regexp.MustCompile(`(?i)u\.user_id\s*=\s*@acting_user_id`),
	regexp.MustCompile(`(?i)u\.organization_id\s*=\s*@organization_id`),
}

// secretsTableReference matches a statement's use of the vault as a table,
// in any of the four positions SQL puts one.
var secretsTableReference = regexp.MustCompile(`(?is)\b(?:FROM|INTO|UPDATE|JOIN)\s+secrets\b`)

// TestEverySecretStatementCarriesItsGuards enforces the vault's access model
// at the only place it can be enforced statically: the SQL itself.
//
// Two directions, and both matter. Every statement touching secrets must be
// listed — so a new query cannot arrive without guards — and every listed
// statement must exist, so a rename cannot quietly retire a rule.
func TestEverySecretStatementCarriesItsGuards(t *testing.T) {
	seen := map[string]bool{}

	for _, stmt := range loadStatements(t) {
		if !secretsTableReference.MatchString(stmt.sql) {
			continue
		}
		required, listed := secretStatements[stmt.name]
		if !listed {
			t.Errorf("%s: %q touches the secrets table but is not listed in secretStatements. "+
				"Every statement against the vault carries an access model; add it there with the "+
				"guards it owes rather than letting it inherit none.", stmt.file, stmt.name)
			continue
		}
		seen[stmt.name] = true

		if required.ownership {
			for _, branch := range ownershipBranches {
				if !branch.pattern.MatchString(stmt.sql) {
					t.Errorf("%s: %q selects secrets without %s — %s.",
						stmt.file, stmt.name, branch.name, branch.why)
				}
			}
		}
		if !required.membership {
			continue
		}
		if !membershipGuard.MatchString(stmt.sql) {
			t.Errorf("%s: %q has no `EXISTS (SELECT 1 FROM users …)` membership check. The "+
				"ownership predicate admits shared rows, whose owner is NULL, so it is satisfied "+
				"by ANY acting id — including a user of another organization.", stmt.file, stmt.name)
			continue
		}
		for _, correlation := range membershipCorrelations {
			if !correlation.MatchString(stmt.sql) {
				t.Errorf("%s: %q has a membership check that does not correlate %s. Both halves "+
					"are required: the user id alone proves nothing about the tenant, and the "+
					"organization alone proves nothing about the caller.",
					stmt.file, stmt.name, correlation)
			}
		}
	}

	for name := range secretStatements {
		if !seen[name] {
			t.Errorf("secretStatements requires guards on %q, but no statement by that name touches "+
				"secrets; a renamed or deleted query must not silently retire its rule", name)
		}
	}
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
			allowed, named := namedCompletions[stmt.name]
			if !named {
				t.Errorf("%s: %q updates %s but is not a named completion. There is no generic update on a "+
					"call record; add a completion to namedCompletions here only if it genuinely is one.",
					stmt.file, stmt.name, table)
				continue
			}
			if allowed.table != table {
				t.Errorf("%s: %q is the completion for %s but updates %s. A completion pointed at the "+
					"other call family would apply that family's lifecycle to this one while passing a "+
					"name-only check.", stmt.file, stmt.name, allowed.table, table)
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
				if !allowed.columns[assigned] {
					t.Errorf("%s: %q assigns %s while completing %s. A completion records what the call "+
						"LEARNED by ending; rewriting the request side or the lineage mutates history "+
						"through a statement that passed the name check, and a column belonging to the "+
						"other call family is not this one's to assign.",
						stmt.file, stmt.name, assigned, table)
				}
			}

		case deleteTarget(stmt.sql) == table:
			assertNamedTruncation(t, stmt, table)
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
		target := writeTarget(stmt.sql)
		for _, table := range bornFinalTables {
			if target != table {
				continue
			}
			seen++
			upper := strings.ToUpper(stmt.sql)
			switch {
			case strings.Contains(upper, "UPDATE "):
				t.Errorf("%s: %q updates %s, which is born final and has no lifecycle to move through",
					stmt.file, stmt.name, table)
			case deleteTarget(stmt.sql) == table:
				assertNamedTruncation(t, stmt, table)
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

// deleteTarget returns the table a DELETE actually removes rows from.
//
// Naively searching for "FROM <table>" is wrong here and was: the
// truncation statements carry retention guards like
// `EXISTS (SELECT 1 FROM audit_artifacts ...)`, so a substring match finds
// the SUBQUERY's table and concludes the statement deletes from it. That
// misreads the one file these rules exist to police.
var deleteFromPattern = regexp.MustCompile(`(?is)\bDELETE\s+FROM\s+([a-z_]+)`)

func deleteTarget(sql string) string {
	match := deleteFromPattern.FindStringSubmatch(sql)
	if len(match) < 2 {
		return ""
	}
	return strings.ToLower(match[1])
}

// writeTarget returns the table a statement writes to, for any of the three
// verbs, or "".
var (
	insertIntoPattern = regexp.MustCompile(`(?is)\bINSERT\s+INTO\s+([a-z_]+)`)
	updatePattern     = regexp.MustCompile(`(?is)\bUPDATE\s+([a-z_]+)`)
)

func writeTarget(sql string) string {
	for _, pattern := range []*regexp.Regexp{insertIntoPattern, updatePattern, deleteFromPattern} {
		if match := pattern.FindStringSubmatch(sql); len(match) >= 2 {
			return strings.ToLower(match[1])
		}
	}
	return ""
}

// callTableWritten returns the call table a statement writes, or "".
func callTableWritten(sql string) string {
	target := writeTarget(sql)
	for _, table := range callTables {
		if target == table {
			return table
		}
	}
	return ""
}

// assertNamedTruncation checks that a DELETE is one of the permitted
// truncations AND that it targets the table that name is bound to.
func assertNamedTruncation(t *testing.T, stmt statement, table string) {
	t.Helper()

	target, permitted := namedTruncations[stmt.name]
	if !permitted {
		t.Errorf("%s: %q deletes from %s but is not a named truncation. Deletion here is retention policy "+
			"(design D6), with its own horizon and retention guards -- not something a query may do "+
			"incidentally.", stmt.file, stmt.name, table)
		return
	}
	if target != table {
		t.Errorf("%s: %q is the truncation for %s but deletes from %s. The names are not interchangeable: "+
			"each carries its own cutoff column and retention guards, and using one against another table "+
			"applies the wrong horizon and skips every guard.", stmt.file, stmt.name, target, table)
	}
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
