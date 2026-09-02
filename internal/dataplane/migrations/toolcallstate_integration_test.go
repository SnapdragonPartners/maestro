//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"orchestrator/internal/dataplane/migrations"
)

// Constraint tests for migration 000022 (design D10 and D11).
//
// Three groups: the row constraints, the backfill over rows written under the
// OLD schema, and the down migration's refusals. The second and third need a
// database at a specific version, which is what disposableDatabaseAt exists
// for -- applying every migration to an empty database can never show what a
// migration does to existing data.

// planeFixture is the minimum a tool call needs: an organization, a user, and
// a principal instance. Written against a raw *sql.DB rather than the shared
// transaction fixture, because these cases run migrations, and a migration
// cannot see rows held in an uncommitted transaction.
type planeFixture struct{ org, user, principal string }

func seedForBackfill(t *testing.T, db *sql.DB) planeFixture {
	t.Helper()
	f := planeFixture{
		org:       "44000000-0000-7000-8000-000000000001",
		user:      "44000000-0000-7000-8000-000000000002",
		principal: "44000000-0000-7000-8000-000000000003",
	}
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO organizations (organization_id, slug, display_name) VALUES ($1,'b','B')`,
			[]any{f.org}},
		{`INSERT INTO users (user_id, organization_id, handle, display_name) VALUES ($1,$2,'u','U')`,
			[]any{f.user, f.org}},
		{`INSERT INTO principal_instances (principal_instance_id, organization_id, kind, model, agent_type)
		  VALUES ($1,$2,'agent','opus','coder')`, []any{f.principal, f.org}},
	} {
		if _, err := db.Exec(stmt.sql, stmt.args...); err != nil {
			t.Fatalf("seed plane %q: %v", stmt.sql, err)
		}
	}
	return f
}

// seedExecutionForPlane builds the whole chain an execution needs, in a real
// database rather than the shared transaction. Returns the execution id and
// the lineage a correlated tool call must repeat.
func seedExecutionForPlane(t *testing.T, db *sql.DB, f planeFixture) (execution, product, feature, epic, story string) {
	t.Helper()
	product = "45000000-0000-7000-8000-000000000001"
	repo := "45000000-0000-7000-8000-000000000002"
	feature = "45000000-0000-7000-8000-000000000003"
	epic = "45000000-0000-7000-8000-000000000004"
	story = "45000000-0000-7000-8000-000000000005"
	workGroup := "45000000-0000-7000-8000-000000000006"
	dispatch := "45000000-0000-7000-8000-000000000007"
	execution = "45000000-0000-7000-8000-000000000008"
	storyPlan := "45000000-0000-7000-8000-000000000009"
	epicPlan := "45000000-0000-7000-8000-00000000000a"

	tx, txErr := db.Begin()
	if txErr != nil {
		t.Fatalf("begin: %v", txErr)
	}
	defer func() { _ = tx.Rollback() }()

	artifact := `INSERT INTO management_artifacts
	    (artifact_id, organization_id, user_id, artifact_type, scope_type, scope_story_id, scope_epic_id,
	     product_id, feature_id, epic_id, story_id, author_instance_id, schema_version, summary,
	     payload, payload_digest, review_digest)
	  VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,1,'s','{}'::jsonb,$13,$14)`

	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO products (product_id, organization_id, user_id, slug, display_name)
		  VALUES ($1,$2,$3,'p','P')`, []any{product, f.org, f.user}},
		{`INSERT INTO repositories (repository_id, organization_id, primary_product_id, user_id, slug, display_name)
		  VALUES ($1,$2,$3,$4,'r','R')`, []any{repo, f.org, product, f.user}},
		{`INSERT INTO product_repositories (product_id, repository_id, organization_id)
		  VALUES ($1,$2,$3)`, []any{product, repo, f.org}},
		{`INSERT INTO features (feature_id, organization_id, user_id, product_id, title)
		  VALUES ($1,$2,$3,$4,'F')`, []any{feature, f.org, f.user, product}},
		{`INSERT INTO epics (epic_id, organization_id, user_id, product_id, feature_id, repository_id, title)
		  VALUES ($1,$2,$3,$4,$5,$6,'E')`, []any{epic, f.org, f.user, product, feature, repo}},
		{`INSERT INTO stories (story_id, organization_id, user_id, product_id, feature_id, epic_id, title)
		  VALUES ($1,$2,$3,$4,$5,$6,'S')`, []any{story, f.org, f.user, product, feature, epic}},
		{artifact, []any{storyPlan, f.org, f.user, "story_plan", "story", story, nil,
			product, feature, epic, story, f.principal, digestA, digestB}},
		{artifact, []any{epicPlan, f.org, f.user, "epic_plan", "epic", nil, epic,
			product, feature, epic, nil, f.principal, digestA, digestB}},
		{`INSERT INTO work_groups (work_group_id, organization_id, product_id, feature_id, epic_id)
		  VALUES ($1,$2,$3,$4,$5)`, []any{workGroup, f.org, product, feature, epic}},
		{`INSERT INTO story_dispatches
		    (story_dispatch_id, organization_id, product_id, feature_id, epic_id, story_id, work_group_id,
		     disposition, settled_at,
		     story_version_artifact_id, story_version_effective_digest, story_version_effective_sequence,
		     epic_version_artifact_id, epic_version_effective_digest, epic_version_effective_sequence)
		  VALUES ($1,$2,$3,$4,$5,$6,$7,'accepted',now(),$8,$9,0,$10,$11,0)`,
			[]any{dispatch, f.org, product, feature, epic, story, workGroup,
				storyPlan, digestA, epicPlan, digestB}},
		{`INSERT INTO executions
		    (execution_id, organization_id, product_id, feature_id, epic_id, story_id, story_dispatch_id)
		  VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			[]any{execution, f.org, product, feature, epic, story, dispatch}},
	} {
		if _, err := tx.Exec(stmt.sql, stmt.args...); err != nil {
			t.Fatalf("seed execution chain %q: %v", stmt.sql, err)
		}
	}
	// One committed transaction, because repositories_primary_is_member_fkey
	// is DEFERRABLE INITIALLY DEFERRED: the repository names a primary
	// Product it is not yet a member of, and product_repositories closes the
	// cycle before commit. Statement-at-a-time autocommit fires the check
	// after the first of the pair and fails.
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit execution chain: %v", err)
	}
	return execution, product, feature, epic, story
}

const toolCallInsert = `INSERT INTO tool_calls
    (tool_call_id, organization_id, principal_instance_id, tool_name, arguments,
     state, outcome, finished_at, requirement_set, requirement_set_digest, error_message)
  VALUES ($1,$2,$3,'t','{}'::jsonb,$4,$5,$6,$7,$8,$9)`

// toolCallArgs is a well-formed OPEN row; each case overrides one thing.
// A `failed` outcome carries a diagnostic, because migration 000022 restored
// tool_calls_outcome_coherence_check over the new vocabulary and a failure
// with no diagnostic is refused. Supplied here rather than per case, so a
// case about the state vocabulary is not also a coherence case.
func (w *wh) toolCallArgs(id, state string, outcome, finishedAt, reqSet, reqDigest any) []any {
	var errorMessage any
	if outcome == "failed" {
		errorMessage = "boom"
	}
	return []any{id, w.org, w.principal, state, outcome, finishedAt, reqSet, reqDigest, errorMessage}
}

func TestToolCallStateAndOutcomeMoveTogether(t *testing.T) {
	w := seedWorkHierarchy(t)

	w.rejectsWith(t, "tool_calls_settled_outcome_check",
		"a settled tool call carried no outcome", toolCallInsert,
		w.toolCallArgs("40000000-0000-7000-8000-000000000001", "settled", nil, "now()", nil, nil)...)

	w.rejectsWith(t, "tool_calls_settled_outcome_check",
		"an unsettled tool call carried an outcome", toolCallInsert,
		w.toolCallArgs("40000000-0000-7000-8000-000000000002", "open", "succeeded", nil, nil, nil)...)

	w.rejectsWith(t, "tool_calls_settled_finished_check",
		"a settled tool call had no finished_at", toolCallInsert,
		w.toolCallArgs("40000000-0000-7000-8000-000000000003", "settled", "succeeded", nil, nil, nil)...)

	w.rejectsWith(t, "tool_calls_settled_finished_check",
		"an open tool call carried a finished_at", toolCallInsert,
		w.toolCallArgs("40000000-0000-7000-8000-000000000004", "open", nil, "now()", nil, nil)...)
}

func TestToolCallVocabularies(t *testing.T) {
	w := seedWorkHierarchy(t)

	w.rejectsWith(t, "tool_calls_state_check",
		"a state outside the four was accepted", toolCallInsert,
		w.toolCallArgs("40000000-0000-7000-8000-000000000005", "waiting", nil, nil, nil, nil)...)

	w.rejectsWith(t, "tool_calls_outcome_check",
		"an outcome outside the six was accepted", toolCallInsert,
		w.toolCallArgs("40000000-0000-7000-8000-000000000006", "settled", "cancelled", "now()", nil, nil)...)

	// All six ARE accepted. A vocabulary check that rejected a legitimate
	// value would be caught by nothing above.
	for i, outcome := range []string{"succeeded", "failed", "denied", "blocked", "stale", "unknown"} {
		id := "40000000-0000-7000-8000-00000000001" + string(rune('0'+i))
		var reqSet, reqDigest any
		if outcome == "blocked" {
			reqSet, reqDigest = `{"gate.human":{"reason":"spec amendment"}}`, digestA
		}
		if _, err := w.tx.Exec(toolCallInsert,
			w.toolCallArgs(id, "settled", outcome, "now()", reqSet, reqDigest)...); err != nil {
			t.Fatalf("outcome %q was rejected but is in the vocabulary: %v", outcome, err)
		}
	}
}

func TestRequirementSetIsANonEmptyObject(t *testing.T) {
	w := seedWorkHierarchy(t)
	id := "40000000-0000-7000-8000-000000000020"

	w.rejectsWith(t, "tool_calls_requirement_object_check",
		"an ARRAY was accepted as a requirement set; JCS sorts object keys but leaves array "+
			"order untouched, so an array-encoded set does not compare equal under reordering",
		toolCallInsert, w.toolCallArgs(id, "open", nil, nil, `[{"gate":"human"}]`, digestA)...)

	w.rejectsWith(t, "tool_calls_requirement_nonempty_check",
		"an EMPTY object was accepted; it passes jsonb_typeof and records no requirement at all",
		toolCallInsert, w.toolCallArgs(id, "open", nil, nil, `{}`, digestA)...)

	w.rejectsWith(t, "tool_calls_requirement_pairing_check",
		"a requirement set was accepted without its digest",
		toolCallInsert, w.toolCallArgs(id, "open", nil, nil, `{"g":{}}`, nil)...)

	w.rejectsWith(t, "tool_calls_requirement_pairing_check",
		"a digest was accepted without a requirement set",
		toolCallInsert, w.toolCallArgs(id, "open", nil, nil, nil, digestA)...)

	for _, bad := range []string{"abc", strings.ToUpper(digestA), strings.Repeat("z", 64)} {
		w.rejectsWith(t, "tool_calls_requirement_digest_check",
			"a malformed requirement digest was accepted: "+bad,
			toolCallInsert, w.toolCallArgs(id, "open", nil, nil, `{"g":{}}`, bad)...)
	}
}

func TestRequirementSetIsRequiredWhereItMustBe(t *testing.T) {
	w := seedWorkHierarchy(t)

	w.rejectsWith(t, "tool_calls_operator_wait_requirement_check",
		"an operator wait carried no requirement set, so nothing records what is being waited on",
		toolCallInsert,
		w.toolCallArgs("40000000-0000-7000-8000-000000000030", "operator_waiting", nil, nil, nil, nil)...)

	w.rejectsWith(t, "tool_calls_blocked_requirement_check",
		"a blocked outcome carried no requirement set; ADR 0032 requires a headless block to "+
			"preserve the requirement so the execution's result can reference it",
		toolCallInsert,
		w.toolCallArgs("40000000-0000-7000-8000-000000000031", "settled", "blocked", "now()", nil, nil)...)

	// A resource wait needs no requirement -- no operator is involved -- and
	// constraining it would be a rule ADR 0030 does not state.
	if _, err := w.tx.Exec(toolCallInsert,
		w.toolCallArgs("40000000-0000-7000-8000-000000000032", "resource_waiting", nil, nil, nil, nil)...); err != nil {
		t.Fatalf("a resource wait was refused for lacking a requirement set, but no operator is "+
			"involved in one: %v", err)
	}
}

// Correlation travels the whole lineage, and the lineage guard is what stops
// MATCH SIMPLE skipping the key on a partially-filled row.
func TestToolCallExecutionCorrelation(t *testing.T) {
	w := seedWorkHierarchy(t)
	w.insertDispatch(t)
	w.acceptDispatch(t)
	if _, err := w.tx.Exec(`INSERT INTO executions
	        (execution_id, organization_id, product_id, feature_id, epic_id, story_id, story_dispatch_id)
	        VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		whExecution, w.org, w.product, w.feature, w.epic, w.story, w.dispatch); err != nil {
		t.Fatalf("seed execution: %v", err)
	}

	withExecution := `INSERT INTO tool_calls
	    (tool_call_id, organization_id, principal_instance_id, tool_name, arguments,
	     execution_id, product_id, feature_id, epic_id, story_id)
	  VALUES ($1,$2,$3,'t','{}'::jsonb,$4,$5,$6,$7,$8)`

	w.rejectsWith(t, "tool_calls_execution_lineage_check",
		"a tool call named an execution while its lineage was only partly filled, which would "+
			"skip the whole composite key under MATCH SIMPLE",
		withExecution, "40000000-0000-7000-8000-000000000040", w.org, w.principal,
		whExecution, w.product, w.feature, w.epic, nil)

	w.rejectsWith(t, "tool_calls_execution_fkey",
		"a tool call carried Story 2's lineage while naming Story 1's execution",
		withExecution, "40000000-0000-7000-8000-000000000041", w.org, w.principal,
		whExecution, w.product, w.feature, w.epic2, w.story2)

	// CROSS-ORGANIZATION. A whole second tenant, with its own chain, and a
	// tool call in organization A naming organization B's execution.
	//
	// Note what this can and cannot isolate. `stories.story_id` is a PRIMARY
	// KEY, so it is globally unique and two organizations can never hold the
	// same Story. An execution in another organization therefore always
	// carries different lineage as well, and no mutation can vary the
	// organization column ALONE. The column stays in the key as defence in
	// depth and for consistency with every other composite key here; what is
	// testable is that a cross-tenant correlation is refused, and that is
	// what this asserts rather than claiming more.
	otherOrg := "40000000-0000-7000-8000-0000000000f0"
	otherExecution := w.seedForeignOrgExecution(t, otherOrg)

	w.rejectsWith(t, "tool_calls_execution_fkey",
		"a tool call named an execution belonging to a different organization",
		withExecution, "40000000-0000-7000-8000-000000000043", w.org, w.principal,
		otherExecution, w.product, w.feature, w.epic, w.story)

	// The well-formed case must be accepted.
	if _, err := w.tx.Exec(withExecution, "40000000-0000-7000-8000-000000000042", w.org, w.principal,
		whExecution, w.product, w.feature, w.epic, w.story); err != nil {
		t.Fatalf("a correctly correlated tool call was rejected: %v", err)
	}
}

// seedForeignOrgExecution builds a second tenant with a complete chain and
// returns its execution id. Inside the fixture transaction, so the deferrable
// repository/product cycle never has to settle.
func (w *wh) seedForeignOrgExecution(t *testing.T, org string) string {
	t.Helper()
	var (
		user      = "40000000-0000-7000-8000-0000000000f1"
		principal = "40000000-0000-7000-8000-0000000000f2"
		product   = "40000000-0000-7000-8000-0000000000f3"
		repo      = "40000000-0000-7000-8000-0000000000f4"
		feature   = "40000000-0000-7000-8000-0000000000f5"
		epic      = "40000000-0000-7000-8000-0000000000f6"
		story     = "40000000-0000-7000-8000-0000000000f7"
		workGroup = "40000000-0000-7000-8000-0000000000f8"
		dispatch  = "40000000-0000-7000-8000-0000000000f9"
		execution = "40000000-0000-7000-8000-0000000000fa"
		storyPlan = "40000000-0000-7000-8000-0000000000fb"
		epicPlan  = "40000000-0000-7000-8000-0000000000fc"
	)
	artifact := `INSERT INTO management_artifacts
	    (artifact_id, organization_id, user_id, artifact_type, scope_type, scope_story_id, scope_epic_id,
	     product_id, feature_id, epic_id, story_id, author_instance_id, schema_version, summary,
	     payload, payload_digest, review_digest)
	  VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,1,'s','{}'::jsonb,$13,$14)`

	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO organizations (organization_id, slug, display_name) VALUES ($1,'other','Other')`,
			[]any{org}},
		{`INSERT INTO users (user_id, organization_id, handle, display_name) VALUES ($1,$2,'u2','U2')`,
			[]any{user, org}},
		{`INSERT INTO principal_instances (principal_instance_id, organization_id, kind, model, agent_type)
		  VALUES ($1,$2,'agent','opus','coder')`, []any{principal, org}},
		{`INSERT INTO products (product_id, organization_id, user_id, slug, display_name)
		  VALUES ($1,$2,$3,'p2','P2')`, []any{product, org, user}},
		{`INSERT INTO repositories (repository_id, organization_id, primary_product_id, user_id, slug, display_name)
		  VALUES ($1,$2,$3,$4,'r2','R2')`, []any{repo, org, product, user}},
		{`INSERT INTO product_repositories (product_id, repository_id, organization_id)
		  VALUES ($1,$2,$3)`, []any{product, repo, org}},
		{`INSERT INTO features (feature_id, organization_id, user_id, product_id, title)
		  VALUES ($1,$2,$3,$4,'F2')`, []any{feature, org, user, product}},
		{`INSERT INTO epics (epic_id, organization_id, user_id, product_id, feature_id, repository_id, title)
		  VALUES ($1,$2,$3,$4,$5,$6,'E')`, []any{epic, org, user, product, feature, repo}},
		{`INSERT INTO stories (story_id, organization_id, user_id, product_id, feature_id, epic_id, title)
		  VALUES ($1,$2,$3,$4,$5,$6,'S')`, []any{story, org, user, product, feature, epic}},
		{artifact, []any{storyPlan, org, user, "story_plan", "story", story, nil,
			product, feature, epic, story, principal, digestA, digestB}},
		{artifact, []any{epicPlan, org, user, "epic_plan", "epic", nil, epic,
			product, feature, epic, nil, principal, digestA, digestB}},
		{`INSERT INTO work_groups (work_group_id, organization_id, product_id, feature_id, epic_id)
		  VALUES ($1,$2,$3,$4,$5)`, []any{workGroup, org, product, feature, epic}},
		{`INSERT INTO story_dispatches
		    (story_dispatch_id, organization_id, product_id, feature_id, epic_id, story_id, work_group_id,
		     disposition, settled_at,
		     story_version_artifact_id, story_version_effective_digest, story_version_effective_sequence,
		     epic_version_artifact_id, epic_version_effective_digest, epic_version_effective_sequence)
		  VALUES ($1,$2,$3,$4,$5,$6,$7,'accepted',now(),$8,$9,0,$10,$11,0)`,
			[]any{dispatch, org, product, feature, epic, story, workGroup,
				storyPlan, digestA, epicPlan, digestB}},
		{`INSERT INTO executions
		    (execution_id, organization_id, product_id, feature_id, epic_id, story_id, story_dispatch_id)
		  VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			[]any{execution, org, product, feature, epic, story, dispatch}},
	} {
		if _, err := w.tx.Exec(stmt.sql, stmt.args...); err != nil {
			t.Fatalf("seed foreign-org chain %q: %v", stmt.sql, err)
		}
	}
	return execution
}

// The backfill, against rows written under the PRE-000022 schema. Applying
// every migration to an empty database cannot show this.
func TestBackfillSettlesFinishedRowsAndLeavesOpenOnesOpen(t *testing.T) {
	ctx := context.Background()
	dsn := disposableDatabaseAt(t, 21)

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	f := seedForBackfill(t, db)

	// Three rows under the old shape: succeeded, failed, and still in flight.
	for _, row := range []struct {
		id        string
		succeeded any
		finished  any
	}{
		{"41000000-0000-7000-8000-000000000001", true, "now()"},
		{"41000000-0000-7000-8000-000000000002", false, "now()"},
		{"41000000-0000-7000-8000-000000000003", nil, nil},
	} {
		if _, execErr := db.Exec(`INSERT INTO tool_calls
		        (tool_call_id, organization_id, principal_instance_id, tool_name, arguments,
		         succeeded, finished_at, error_message)
		        VALUES ($1,$2,$3,'t','{}'::jsonb,$4,$5,$6)`,
			row.id, f.org, f.principal, row.succeeded, row.finished,
			errorMessageFor(row.succeeded)); execErr != nil {
			t.Fatalf("seed pre-migration row %s: %v", row.id, execErr)
		}
	}

	if upErr := migrations.Up(ctx, dsn); upErr != nil {
		t.Fatalf("migrating over existing rows: %v", upErr)
	}

	for _, want := range []struct{ id, state, outcome string }{
		{"41000000-0000-7000-8000-000000000001", "settled", "succeeded"},
		{"41000000-0000-7000-8000-000000000002", "settled", "failed"},
		{"41000000-0000-7000-8000-000000000003", "open", ""},
	} {
		var state string
		var outcome *string
		if scanErr := db.QueryRow(
			`SELECT state, outcome FROM tool_calls WHERE tool_call_id=$1`, want.id).
			Scan(&state, &outcome); scanErr != nil {
			t.Fatalf("read migrated row %s: %v", want.id, scanErr)
		}
		got := ""
		if outcome != nil {
			got = *outcome
		}
		if state != want.state || got != want.outcome {
			t.Errorf("row %s migrated to state=%q outcome=%q, want state=%q outcome=%q",
				want.id, state, got, want.state, want.outcome)
		}
	}

	// The equivalence must hold for EVERY row, which is the property the
	// backfill exists to preserve -- the column default alone would have left
	// the two finished rows settled-in-fact and open-in-column.
	var violations int
	if err := db.QueryRow(`SELECT count(*) FROM tool_calls
	                        WHERE (state = 'settled') <> (outcome IS NOT NULL)
	                           OR (state = 'settled') <> (finished_at IS NOT NULL)`).
		Scan(&violations); err != nil {
		t.Fatalf("check equivalence: %v", err)
	}
	if violations != 0 {
		t.Errorf("%d row(s) violate the settled equivalences after migrating", violations)
	}
}

func errorMessageFor(succeeded any) any {
	if succeeded == false {
		return "boom"
	}
	return nil
}

// The down migration refuses on all four classes, and each class needs its
// own run: one passing proves nothing about the others.
func TestDownMigrationRefusesRatherThanCorrupts(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		name    string
		columns string
		values  string
		args    []any
		wants   string
	}{
		{"denied outcome", "state, outcome, finished_at", "'settled','denied',now()", nil,
			"1 tool call(s) hold an outcome no boolean can"},
		{"blocked outcome", "state, outcome, finished_at, requirement_set, requirement_set_digest",
			"'settled','blocked',now(),'{\"g\":{}}'::jsonb,$4", []any{digestA},
			"1 tool call(s) hold an outcome no boolean can"},
		{"stale outcome", "state, outcome, finished_at", "'settled','stale',now()", nil,
			"1 tool call(s) hold an outcome no boolean can"},
		{"unknown outcome", "state, outcome, finished_at", "'settled','unknown',now()", nil,
			"1 tool call(s) hold an outcome no boolean can"},
		{"operator wait", "state, requirement_set, requirement_set_digest",
			"'operator_waiting','{\"g\":{}}'::jsonb,$4", []any{digestA},
			"1 tool call(s) are in a declared wait"},
		{"resource wait", "state", "'resource_waiting'", nil, "1 tool call(s) are in a declared wait"},
		{"succeeded row with a requirement set",
			"state, outcome, finished_at, requirement_set, requirement_set_digest",
			"'settled','succeeded',now(),'{\"g\":{}}'::jsonb,$4", []any{digestA},
			"1 tool call(s) carry a requirement set"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dsn := disposableDatabase(t)
			db, err := sql.Open("pgx", dsn)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer func() { _ = db.Close() }()

			f := seedForBackfill(t, db)
			args := append([]any{"42000000-0000-7000-8000-000000000001", f.org, f.principal}, tc.args...)
			if _, execErr := db.Exec(`INSERT INTO tool_calls
			        (tool_call_id, organization_id, principal_instance_id, tool_name, arguments, `+
				tc.columns+`) VALUES ($1,$2,$3,'t','{}'::jsonb,`+tc.values+`)`, args...); execErr != nil {
				t.Fatalf("seed offending row: %v", execErr)
			}

			err = migrations.To(ctx, dsn, 21)
			if err == nil {
				t.Fatal("the down migration accepted a row the old shape cannot express, and has " +
					"silently rewritten it")
			}
			// The SUBSTITUTED count, not the bare wording. golang-migrate
			// echoes the whole migration source in its error, and every
			// RAISE message lives in that source -- so a phrase match
			// passes whenever the migration fails for ANY reason,
			// including a branch this case is not about. Only the
			// formatted count proves which branch fired. This repository
			// learned it on 000011 and it is documented there.
			if !strings.Contains(err.Error(), tc.wants) {
				t.Errorf("down refused for the wrong reason: wanted the rendered %q, got %v", tc.wants, err)
			}
			// And prove the trap is real rather than theoretical: EVERY
			// branch's bare wording is present in this error, including
			// branches that did not fire, because the source is echoed. A
			// case asserting the bare phrase would pass on any refusal at
			// all. If this ever stops holding, the rendered-count matching
			// above is belt without braces and the comment should say so.
			for _, bare := range []string{"hold an outcome no boolean can",
				"are in a declared wait", "carry a requirement set",
				"carry an execution correlation"} {
				if !strings.Contains(err.Error(), bare) {
					t.Errorf("expected the echoed migration source to contain every branch's wording, "+
						"but %q is absent -- the reason this test matches rendered counts no longer "+
						"applies and should be re-examined", bare)
				}
			}
		})
	}
}

// The eighth class, which needs a real execution and so does not fit the
// table above: a SUCCEEDED row carrying an execution correlation passes every
// other guard and still loses the binding ADR 0032 line 1250 requires
// persisted.
func TestDownMigrationRefusesASucceededRowCarryingAnExecution(t *testing.T) {
	ctx := context.Background()
	dsn := disposableDatabase(t)

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	f := seedForBackfill(t, db)
	execution, product, feature, epic, story := seedExecutionForPlane(t, db, f)

	if _, execErr := db.Exec(`INSERT INTO tool_calls
	        (tool_call_id, organization_id, principal_instance_id, tool_name, arguments,
	         state, outcome, finished_at, execution_id, product_id, feature_id, epic_id, story_id)
	        VALUES ($1,$2,$3,'t','{}'::jsonb,'settled','succeeded',now(),$4,$5,$6,$7,$8)`,
		"46000000-0000-7000-8000-000000000001", f.org, f.principal,
		execution, product, feature, epic, story); execErr != nil {
		t.Fatalf("seed correlated row: %v", execErr)
	}

	err = migrations.To(ctx, dsn, 21)
	if err == nil {
		t.Fatal("the down migration dropped an execution correlation from a succeeded row, which " +
			"every outcome and wait guard waves straight through")
	}
	// Rendered count, for the reason the table above documents.
	if !strings.Contains(err.Error(), "1 tool call(s) carry an execution correlation") {
		t.Errorf("down refused for the wrong reason: %v", err)
	}
}

// Recovering from a refusal takes TWO steps, and resolving the rows is only
// the first.
//
// golang-migrate marks the version before running, so the refusal leaves the
// recorded version at 21 and dirty while the transaction's rollback leaves
// the actual schema at 22. A retry from there runs from the wrong place. The
// whole recovery is walked here because the migration's comment instructs an
// operator to perform it, and 000011's first such instruction was
// mechanically wrong -- a recovery nobody executes is a recovery nobody
// knows is broken.
func TestRefusalRecoveryNeedsTheVersionForcedBack(t *testing.T) {
	ctx := context.Background()
	dsn := disposableDatabase(t)

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	f := seedForBackfill(t, db)
	const offending = "47000000-0000-7000-8000-000000000001"
	if _, execErr := db.Exec(`INSERT INTO tool_calls
	        (tool_call_id, organization_id, principal_instance_id, tool_name, arguments,
	         state, outcome, finished_at)
	        VALUES ($1,$2,$3,'t','{}'::jsonb,'settled','unknown',now())`,
		offending, f.org, f.principal); execErr != nil {
		t.Fatalf("seed offending row: %v", execErr)
	}

	if downErr := migrations.To(ctx, dsn, 21); downErr == nil {
		t.Fatal("the down migration accepted an `unknown` outcome")
	}

	// The metadata and the schema now DISAGREE, which is the whole point.
	version, dirty, versionErr := migrations.Version(dsn)
	if versionErr != nil {
		t.Fatalf("read version: %v", versionErr)
	}
	if !dirty || version != 21 {
		t.Fatalf("after a refusal the recorded version is %d dirty=%v; the migration's recovery "+
			"instruction assumes 21 and dirty, so if this changed the instruction is wrong",
			version, dirty)
	}
	// The schema is still at 22 -- the columns 000022 added are present,
	// because the refusal rolled its transaction back.
	var hasOutcome bool
	if scanErr := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM information_schema.columns
	                            WHERE table_name='tool_calls' AND column_name='outcome')`).
		Scan(&hasOutcome); scanErr != nil {
		t.Fatalf("inspect schema: %v", scanErr)
	}
	if !hasOutcome {
		t.Fatal("the refusal left the schema partially reversed; it must roll back whole")
	}

	// Step one alone is NOT enough: resolving the rows and retrying still
	// fails, because the recorded version is dirty.
	if _, execErr := db.Exec(`DELETE FROM tool_calls WHERE tool_call_id=$1`, offending); execErr != nil {
		t.Fatalf("resolve the offending row: %v", execErr)
	}
	if retryErr := migrations.To(ctx, dsn, 21); retryErr == nil {
		t.Fatal("reversing succeeded from a dirty version; if that is now allowed, the migration's " +
			"two-step recovery instruction is over-stated and should be simplified")
	}

	// Step two: force the metadata back to the version the schema really is.
	if forceErr := migrations.Force(dsn, 22); forceErr != nil {
		t.Fatalf("force back to 22: %v", forceErr)
	}
	if downErr := migrations.To(ctx, dsn, 21); downErr != nil {
		t.Fatalf("the documented recovery did not work: %v", downErr)
	}

	version, dirty, versionErr = migrations.Version(dsn)
	if versionErr != nil {
		t.Fatalf("read version after recovery: %v", versionErr)
	}
	if dirty || version != 21 {
		t.Fatalf("after recovery the version is %d dirty=%v, want 21 clean", version, dirty)
	}
}

// A plane holding only rows the old shape CAN express must still reverse, or
// the refusals above would be indistinguishable from a down migration that
// never works.
//
// All THREE expressible cases, because the old shape has three: settled and
// succeeded, settled and failed, and open. Checking only success would leave
// a wrong `failed` mapping and a rewritten open row both green -- and the
// failed case additionally has to keep its diagnostic, which is the column
// the restored coherence constraint reads.
func TestDownMigrationSucceedsOnExpressibleRows(t *testing.T) {
	ctx := context.Background()
	dsn := disposableDatabase(t)

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	f := seedForBackfill(t, db)
	const (
		succeededID = "43000000-0000-7000-8000-000000000001"
		failedID    = "43000000-0000-7000-8000-000000000002"
		openID      = "43000000-0000-7000-8000-000000000003"
	)
	for _, row := range []struct {
		id, columns, values string
	}{
		{succeededID, "state, outcome, finished_at", "'settled','succeeded',now()"},
		{failedID, "state, outcome, finished_at, error_message", "'settled','failed',now(),'provider timeout'"},
		{openID, "state", "'open'"},
	} {
		if _, execErr := db.Exec(`INSERT INTO tool_calls
		        (tool_call_id, organization_id, principal_instance_id, tool_name, arguments, `+
			row.columns+`) VALUES ($1,$2,$3,'t','{}'::jsonb,`+row.values+`)`,
			row.id, f.org, f.principal); execErr != nil {
			t.Fatalf("seed %s: %v", row.id, execErr)
		}
	}

	if downErr := migrations.To(ctx, dsn, 21); downErr != nil {
		t.Fatalf("the down migration refused a plane it can express: %v", downErr)
	}

	for _, want := range []struct {
		id         string
		succeeded  *bool
		finished   bool
		diagnostic string
	}{
		{succeededID, boolPtr(true), true, ""},
		{failedID, boolPtr(false), true, "provider timeout"},
		{openID, nil, false, ""},
	} {
		var succeeded *bool
		var finishedAt *string
		var errorMessage *string
		if scanErr := db.QueryRow(
			`SELECT succeeded, finished_at::text, error_message FROM tool_calls WHERE tool_call_id=$1`,
			want.id).Scan(&succeeded, &finishedAt, &errorMessage); scanErr != nil {
			t.Fatalf("read reversed row %s: %v", want.id, scanErr)
		}
		switch {
		case want.succeeded == nil && succeeded != nil:
			t.Errorf("row %s was open and reversed to succeeded=%v; an open row must stay unfinished "+
				"with a null boolean, which is the pre-000022 encoding", want.id, *succeeded)
		case want.succeeded != nil && (succeeded == nil || *succeeded != *want.succeeded):
			t.Errorf("row %s reversed to succeeded=%v, want %v", want.id, succeeded, *want.succeeded)
		}
		if (finishedAt != nil) != want.finished {
			t.Errorf("row %s reversed with finished_at=%v, want finished=%v", want.id, finishedAt, want.finished)
		}
		got := ""
		if errorMessage != nil {
			got = *errorMessage
		}
		if got != want.diagnostic {
			t.Errorf("row %s reversed with error_message=%q, want %q -- the failed row's diagnostic is "+
				"what the restored coherence constraint reads", want.id, got, want.diagnostic)
		}
	}
}

func boolPtr(b bool) *bool { return &b }
