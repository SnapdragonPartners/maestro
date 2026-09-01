//go:build integration

package migrations_test

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// Constraint tests for migration 000021 (docs/v2/phase_3/design_work-hierarchy.md).
//
// Item 2 lands schema, so item 2's tests are constraint tests: each asserts a
// statement the database must REFUSE, and names the rule that must do the
// refusing. The design's testing split assigns the application-behaviour
// obligations -- atomic dispatch creation, effective-view comparison,
// serialized graph mutation -- to items 3, 5 and 9.
//
// Every case names its constraint through rejectsWith, because these rows
// break more than one rule at a time by construction. A cross-Epic dispatch
// is also, unless the fixture is careful, an artifact-scope violation -- so
// the fixture below gives EVERY Story and Epic its own artifacts, and a case
// that means to exercise the Work Group key is not allowed to pass on the
// version-scope key instead.
//
// Naming the constraint is also what makes each case DEFECT-SHAPED without a
// separate mutation run: delete the named rule and the statement either
// succeeds (no error, so the case fails) or is refused by some other rule
// (name mismatch, so the case fails). A case that only asserted "this was
// rejected" would survive the deletion of the very constraint it exists for.

const (
	whEpic2        = "30000000-0000-7000-8000-000000000001"
	whStory2       = "30000000-0000-7000-8000-000000000002"
	whStorySame    = "30000000-0000-7000-8000-000000000003"
	whWorkGroup    = "30000000-0000-7000-8000-000000000004"
	whDispatch     = "30000000-0000-7000-8000-000000000005"
	whExecution    = "30000000-0000-7000-8000-000000000007"
	whStory1Plan   = "30000000-0000-7000-8000-000000000010"
	whEpic1Plan    = "30000000-0000-7000-8000-000000000011"
	whStory2Plan   = "30000000-0000-7000-8000-000000000012"
	whEpic2Plan    = "30000000-0000-7000-8000-000000000013"
	whSiblingPlan  = "30000000-0000-7000-8000-000000000014"
	whStory1Alt    = "30000000-0000-7000-8000-000000000015"
	whStory1Amend  = "30000000-0000-7000-8000-000000000016"
	whOtherFeature = "30000000-0000-7000-8000-000000000020"
	whOtherEpic    = "30000000-0000-7000-8000-000000000021"
	whEpic1Amend   = "30000000-0000-7000-8000-000000000023"
)

type wh struct {
	*fixture
	epic2, story2, storySameEpic string
	workGroup, dispatch          string
}

// seedWorkHierarchy builds a second Epic with its own Story, a sibling Story
// in the ORIGINAL Epic, a Work Group, and a correctly-scoped artifact for
// every one of them -- plus a SECOND accepted original scoped to Story 1 and
// an amendment of Story 1's plan.
//
// The per-entity artifacts are what make the negative cases honest: without
// them a cross-Epic row fails on the version-scope foreign key before the
// constraint under test is ever consulted.
func seedWorkHierarchy(t *testing.T) *wh {
	t.Helper()
	f := seed(t, openPlane(t))
	w := &wh{
		fixture:       f,
		epic2:         whEpic2,
		story2:        whStory2,
		storySameEpic: whStorySame,
		workGroup:     whWorkGroup,
		dispatch:      whDispatch,
	}

	stmts := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO epics (epic_id, organization_id, user_id, product_id, feature_id, repository_id, title)
		  VALUES ($1,$2,$3,$4,$5,$6,'E2')`, []any{w.epic2, f.org, f.user, f.product, f.feature, f.repo}},
		{`INSERT INTO stories (story_id, organization_id, user_id, product_id, feature_id, epic_id, title)
		  VALUES ($1,$2,$3,$4,$5,$6,'S2')`, []any{w.story2, f.org, f.user, f.product, f.feature, w.epic2}},
		{`INSERT INTO stories (story_id, organization_id, user_id, product_id, feature_id, epic_id, title)
		  VALUES ($1,$2,$3,$4,$5,$6,'S-sibling')`, []any{w.storySameEpic, f.org, f.user, f.product, f.feature, f.epic}},
		{`INSERT INTO work_groups (work_group_id, organization_id, product_id, feature_id, epic_id)
		  VALUES ($1,$2,$3,$4,$5)`, []any{w.workGroup, f.org, f.product, f.feature, f.epic}},
	}
	for _, s := range stmts {
		if _, err := f.tx.Exec(s.sql, s.args...); err != nil {
			t.Fatalf("seed work hierarchy %q: %v", s.sql, err)
		}
	}

	// One correctly-scoped artifact per entity, plus a second original for
	// Story 1 (the divergence case repoints to a real artifact rather than
	// inventing a digest) and an amendment of Story 1's plan.
	w.storyArtifact(t, whStory1Plan, f.story, f.epic)
	w.storyArtifact(t, whStory1Alt, f.story, f.epic)
	w.storyArtifact(t, whStory2Plan, w.story2, w.epic2)
	w.storyArtifact(t, whSiblingPlan, w.storySameEpic, f.epic)
	w.epicArtifact(t, whEpic1Plan, f.epic)
	w.epicArtifact(t, whEpic2Plan, w.epic2)

	// Accepted amendments of both a Story-scoped and an Epic-scoped original.
	// Accepted rather than draft because an accepted amendment is what moves
	// an effective view -- the case the original-only rule exists for.
	if err := f.insertStoryArtifact(whStory1Amend, map[string]any{
		"amends_artifact_id": whStory1Plan,
		"scope_story_id":     f.story,
		"story_id":           f.story,
		"status":             "accepted",
		"accepted_at":        "now()",
		"amendment_sequence": 1,
	}); err != nil {
		t.Fatalf("seed story amendment: %v", err)
	}
	// The fixture asserts its own claim. "accepted" is passed as a column
	// value, so a typo or a status the check constraint tolerates would leave
	// these drafts while every case still passed -- and a case resting on a
	// draft original proves nothing about the accepted originals the seam
	// will actually hold.
	w.assertAccepted(t, whStory1Plan, whStory1Alt, whStory2Plan, whSiblingPlan, whEpic1Plan, whEpic2Plan)

	if err := f.insertStoryArtifact(whEpic1Amend, map[string]any{
		"amends_artifact_id": whEpic1Plan,
		"scope_type":         "epic",
		"scope_story_id":     nil,
		"scope_epic_id":      f.epic,
		"story_id":           nil,
		"epic_id":            f.epic,
		"status":             "accepted",
		"accepted_at":        "now()",
		"amendment_sequence": 1,
	}); err != nil {
		t.Fatalf("seed epic amendment: %v", err)
	}
	return w
}

// assertAccepted fails unless every named artifact is genuinely accepted with
// an acceptance timestamp.
func (w *wh) assertAccepted(t *testing.T, ids ...string) {
	t.Helper()
	for _, id := range ids {
		var status string
		var acceptedAt *string
		if err := w.tx.QueryRow(
			`SELECT status, accepted_at::text FROM management_artifacts WHERE artifact_id=$1`,
			id).Scan(&status, &acceptedAt); err != nil {
			t.Fatalf("read seeded artifact %s: %v", id, err)
		}
		if status != "accepted" || acceptedAt == nil {
			t.Fatalf("seeded artifact %s is status=%q accepted_at=%v, but the cases below "+
				"describe it as an accepted original", id, status, acceptedAt)
		}
	}
}

// The whole lineage tuple must reference a real Story, so the Epic travels
// with it: management_artifacts_story_lineage_fkey checks (story, epic,
// feature, product, org) together, not the story alone.
func (w *wh) storyArtifact(t *testing.T, id, story, epic string) {
	t.Helper()
	if err := w.insertStoryArtifact(id, map[string]any{
		"scope_story_id": story,
		"story_id":       story,
		"epic_id":        epic,
		"status":         "accepted",
		"accepted_at":    "now()",
	}); err != nil {
		t.Fatalf("seed story artifact %s: %v", id, err)
	}
}

func (w *wh) epicArtifact(t *testing.T, id, epic string) {
	t.Helper()
	if err := w.insertStoryArtifact(id, map[string]any{
		"scope_type":     "epic",
		"scope_story_id": nil,
		"scope_epic_id":  epic,
		"story_id":       nil,
		"epic_id":        epic,
		"status":         "accepted",
		"accepted_at":    "now()",
	}); err != nil {
		t.Fatalf("seed epic artifact %s: %v", id, err)
	}
}

// rejectsNotNull asserts a statement failed specifically because a NOT NULL
// column was given null, and names the column.
//
// "It was rejected" is not enough here: the discriminator sites are inside
// composite foreign keys, so a null could also surface as a key violation on
// a neighbouring column. Asserting 23502 plus the column keeps each case
// about the NOT NULL that closes the MATCH SIMPLE escape.
func (w *wh) rejectsNotNull(t *testing.T, column, because, stmt string, args ...any) {
	t.Helper()

	if _, err := w.tx.Exec("SAVEPOINT not_null_probe"); err != nil {
		t.Fatalf("savepoint: %v", err)
	}
	_, err := w.tx.Exec(stmt, args...)
	if err == nil {
		t.Fatal(because + "; the composite foreign key can now be skipped under MATCH SIMPLE")
	}
	var pgErr *pgconn.PgError
	switch {
	case !errors.As(err, &pgErr):
		t.Fatalf("%s: wanted a not-null violation on %s, got a non-Postgres error: %v", because, column, err)
	case pgErr.Code != "23502":
		t.Fatalf("%s: wanted a not-null violation (23502) on %s, got SQLSTATE %s: %v",
			because, column, pgErr.Code, err)
	case pgErr.ColumnName != column:
		t.Fatalf("%s: wanted column %s, but %s was the null one — the case is not exercising the "+
			"discriminator it names", because, column, pgErr.ColumnName)
	}
	if _, rbErr := w.tx.Exec("ROLLBACK TO SAVEPOINT not_null_probe"); rbErr != nil {
		t.Fatalf("rollback to savepoint: %v", rbErr)
	}
}

const dispatchInsert = `INSERT INTO story_dispatches (
    story_dispatch_id, organization_id, product_id, feature_id, epic_id, story_id,
    work_group_id, disposition,
    story_version_artifact_id, story_version_effective_digest, story_version_effective_sequence,
    epic_version_artifact_id,  epic_version_effective_digest,  epic_version_effective_sequence)
  VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,0,$11,$12,0)`

// dispatchArgs builds a well-formed pending dispatch for Story 1 in Epic 1,
// with correctly-scoped artifacts. Each case overrides exactly one thing.
func (w *wh) dispatchArgs() []any {
	return []any{w.dispatch, w.org, w.product, w.feature, w.epic, w.story,
		w.workGroup, "pending", whStory1Plan, digestA, whEpic1Plan, digestB}
}

func (w *wh) insertDispatch(t *testing.T) {
	t.Helper()
	if _, err := w.tx.Exec(dispatchInsert, w.dispatchArgs()...); err != nil {
		t.Fatalf("a well-formed dispatch was rejected: %v", err)
	}
}

func (w *wh) acceptDispatch(t *testing.T) {
	t.Helper()
	if _, err := w.tx.Exec(
		`UPDATE story_dispatches SET disposition='accepted', settled_at=now() WHERE story_dispatch_id=$1`,
		w.dispatch); err != nil {
		t.Fatalf("accepting a pending dispatch: %v", err)
	}
}

func TestWellFormedWorkHierarchyRowsAreAccepted(t *testing.T) {
	w := seedWorkHierarchy(t)
	w.insertDispatch(t)
	w.acceptDispatch(t)

	if _, err := w.tx.Exec(`INSERT INTO executions
	        (execution_id, organization_id, product_id, feature_id, epic_id, story_id, story_dispatch_id)
	        VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		whExecution, w.org, w.product, w.feature, w.epic, w.story, w.dispatch); err != nil {
		t.Fatalf("a well-formed execution was rejected: %v", err)
	}
	if _, err := w.tx.Exec(`INSERT INTO story_dependencies
	        (organization_id, product_id, feature_id, epic_id, successor_story_id, predecessor_story_id,
	         satisfying_completion_artifact_id)
	        VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		w.org, w.product, w.feature, w.epic, w.story, w.storySameEpic, whSiblingPlan); err != nil {
		t.Fatalf("a well-formed story edge with its completion pointer was rejected: %v", err)
	}
	if _, err := w.tx.Exec(`INSERT INTO dispatch_basis_dependencies
	        (story_dispatch_id, organization_id, product_id, feature_id, epic_id,
	         predecessor_story_id, completion_artifact_id,
	         completion_effective_digest, completion_effective_sequence)
	        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,0)`,
		w.dispatch, w.org, w.product, w.feature, w.epic, w.storySameEpic, whSiblingPlan, digestA); err != nil {
		t.Fatalf("a well-formed basis row was rejected: %v", err)
	}
	for _, ptr := range []struct{ stmt, arg string }{
		{`UPDATE stories SET governing_artifact_id=$1 WHERE story_id=$2`, whStory1Plan},
		{`UPDATE epics   SET governing_artifact_id=$1 WHERE epic_id=$2`, whEpic1Plan},
	} {
		id := w.story
		if ptr.arg == whEpic1Plan {
			id = w.epic
		}
		if _, err := w.tx.Exec(ptr.stmt, ptr.arg, id); err != nil {
			t.Fatalf("a well-formed governing pointer was rejected: %v", err)
		}
	}
}

func TestWorkGroupIsOnePerEpic(t *testing.T) {
	w := seedWorkHierarchy(t)
	w.rejectsWith(t, "work_groups_one_per_epic_key",
		"a second Work Group for one Epic was accepted; ADR 0018's MVP cardinality is not enforced",
		`INSERT INTO work_groups (work_group_id, organization_id, product_id, feature_id, epic_id)
		 VALUES ($1,$2,$3,$4,$5)`,
		"30000000-0000-7000-8000-0000000000aa", w.org, w.product, w.feature, w.epic)
}

// Story 2 and Epic 2 use their OWN artifacts here, so the only thing wrong
// with the row is the Work Group's Epic. An earlier version of this case
// reused Epic 1's artifacts and passed on the version-scope key instead --
// the constraint under test could have been deleted with the test still
// green.
func TestDispatchCannotBorrowAnotherEpicsWorkGroup(t *testing.T) {
	w := seedWorkHierarchy(t)
	w.rejectsWith(t, "story_dispatches_work_group_fkey",
		"a dispatch named a Work Group belonging to a different Epic",
		dispatchInsert,
		"30000000-0000-7000-8000-0000000000ab", w.org, w.product, w.feature,
		w.epic2, w.story2, w.workGroup, "pending",
		whStory2Plan, digestA, whEpic2Plan, digestB)
}

func TestDispatchShapeConstraints(t *testing.T) {
	w := seedWorkHierarchy(t)
	full := `INSERT INTO story_dispatches (
	             story_dispatch_id, organization_id, product_id, feature_id, epic_id, story_id,
	             work_group_id, disposition, settled_at, failure_code, failure_detail,
	             story_version_artifact_id, story_version_effective_digest, story_version_effective_sequence,
	             epic_version_artifact_id,  epic_version_effective_digest,  epic_version_effective_sequence)
	         VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,0,$14,$15,0)`

	probe := func(constraint, because, disposition string, settledAt, code, detail any) {
		w.rejectsWith(t, constraint, because, full,
			"30000000-0000-7000-8000-0000000000ac", w.org, w.product, w.feature, w.epic, w.story,
			w.workGroup, disposition, settledAt, code, detail,
			whStory1Plan, digestA, whEpic1Plan, digestB)
	}

	probe("story_dispatches_settled_check", "a pending dispatch carried settled_at",
		"pending", "now()", nil, nil)
	probe("story_dispatches_settled_check", "a terminal disposition had no settled_at",
		"invalidated", nil, nil, nil)
	probe("story_dispatches_failure_code_check", "a failed dispatch had no failure_code",
		"failed", "now()", nil, nil)
	probe("story_dispatches_failure_detail_check", "failure_detail was accepted without a failure_code",
		"invalidated", "now()", nil, "why")
	probe("story_dispatches_disposition_check", "an unknown disposition was accepted",
		"abandoned", "now()", nil, nil)
}

// Both governing references, each bound to its own work item through the
// scope column. Without scope in the key these pass the tenant check.
func TestDispatchVersionReferencesAreScopeBound(t *testing.T) {
	w := seedWorkHierarchy(t)
	args := func(story, epic string) []any {
		return []any{"30000000-0000-7000-8000-0000000000ad", w.org, w.product, w.feature,
			w.epic, w.story, w.workGroup, "pending", story, digestA, epic, digestB}
	}

	w.rejectsWith(t, "story_dispatches_story_version_fkey",
		"a dispatch's Story version named an artifact scoped to a different Story",
		dispatchInsert, args(whSiblingPlan, whEpic1Plan)...)

	w.rejectsWith(t, "story_dispatches_epic_version_fkey",
		"a dispatch's Epic version named an artifact scoped to a different Epic",
		dispatchInsert, args(whStory1Plan, whEpic2Plan)...)

	// An amendment is not an effective view's identity. The discriminator is
	// constant false, so pointing at an amendment finds no matching row.
	// BOTH references, because either guard can regress alone.
	w.rejectsWith(t, "story_dispatches_story_version_fkey",
		"a dispatch's Story version named an AMENDMENT rather than the original",
		dispatchInsert, args(whStory1Amend, whEpic1Plan)...)
	w.rejectsWith(t, "story_dispatches_epic_version_fkey",
		"a dispatch's Epic version named an AMENDMENT rather than the original",
		dispatchInsert, args(whStory1Plan, whEpic1Amend)...)
}

// Three statements: an execution against a pending dispatch passing proves
// nothing about failed or invalidated.
func TestExecutionRequiresAnAcceptedDispatch(t *testing.T) {
	w := seedWorkHierarchy(t)
	w.insertDispatch(t)

	insert := `INSERT INTO executions
	           (execution_id, organization_id, product_id, feature_id, epic_id, story_id, story_dispatch_id)
	           VALUES ($1,$2,$3,$4,$5,$6,$7)`
	args := []any{whExecution, w.org, w.product, w.feature, w.epic, w.story, w.dispatch}

	w.rejectsWith(t, "executions_dispatch_fkey",
		"an execution was created against a PENDING dispatch", insert, args...)

	for _, tc := range []struct{ disposition, update string }{
		{"failed", `UPDATE story_dispatches SET disposition='failed', settled_at=now(),
		            failure_code='handshake' WHERE story_dispatch_id=$1`},
		{"invalidated", `UPDATE story_dispatches SET disposition='invalidated', settled_at=now(),
		                 failure_code=NULL WHERE story_dispatch_id=$1`},
	} {
		if _, err := w.tx.Exec(tc.update, w.dispatch); err != nil {
			t.Fatalf("settling the dispatch as %s: %v", tc.disposition, err)
		}
		w.rejectsWith(t, "executions_dispatch_fkey",
			"an execution was created against a "+tc.disposition+" dispatch", insert, args...)
	}
}

// The accepted-dispatch key carries the full Story lineage. Without it an
// execution could hold Story B's lineage while naming Story A's dispatch.
func TestExecutionIsBoundToItsDispatchesStory(t *testing.T) {
	w := seedWorkHierarchy(t)
	w.insertDispatch(t)
	w.acceptDispatch(t)

	w.rejectsWith(t, "executions_dispatch_fkey",
		"an execution carried Story 2's lineage while referencing Story 1's accepted dispatch",
		`INSERT INTO executions
		 (execution_id, organization_id, product_id, feature_id, epic_id, story_id, story_dispatch_id)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		whExecution, w.org, w.product, w.feature, w.epic2, w.story2, w.dispatch)
}

func TestExecutionAuthorityConstraints(t *testing.T) {
	w := seedWorkHierarchy(t)
	w.insertDispatch(t)
	w.acceptDispatch(t)
	if _, err := w.tx.Exec(`INSERT INTO executions
	        (execution_id, organization_id, product_id, feature_id, epic_id, story_id, story_dispatch_id)
	        VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		whExecution, w.org, w.product, w.feature, w.epic, w.story, w.dispatch); err != nil {
		t.Fatalf("seed execution: %v", err)
	}

	w.rejectsWith(t, "executions_superseded_closes_admission_check",
		"authority was superseded without closing admission",
		`UPDATE executions SET authority_state='superseded' WHERE execution_id=$1`, whExecution)

	w.rejectsWith(t, "executions_authority_state_check",
		"an unknown authority_state was accepted",
		`UPDATE executions SET authority_state='revoked' WHERE execution_id=$1`, whExecution)

	w.rejectsWith(t, "executions_one_per_dispatch_key",
		"a second execution was created for one dispatch",
		`INSERT INTO executions
		 (execution_id, organization_id, product_id, feature_id, epic_id, story_id, story_dispatch_id)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		"30000000-0000-7000-8000-0000000000ae", w.org, w.product, w.feature, w.epic, w.story, w.dispatch)

	// The inverse: closing admission while authority is still current is a
	// REAL state -- ADR 0032's headless block is a forced stop with no basis
	// supersession -- so this must succeed.
	if _, err := w.tx.Exec(
		`UPDATE executions SET admission_closed_at=now() WHERE execution_id=$1`, whExecution); err != nil {
		t.Fatalf("closing admission under current authority was rejected, but ADR 0032's "+
			"headless block is exactly that state: %v", err)
	}
}

func TestStoryEdgeCannotCrossEpics(t *testing.T) {
	w := seedWorkHierarchy(t)
	w.rejectsWith(t, "story_dependencies_predecessor_fkey",
		"a Story edge crossed Epics; ADR 0024's within-an-Epic rule is not enforced",
		`INSERT INTO story_dependencies
		 (organization_id, product_id, feature_id, epic_id, successor_story_id, predecessor_story_id)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		w.org, w.product, w.feature, w.epic, w.story, w.story2)
}

func TestEpicEdgeCannotCrossFeatures(t *testing.T) {
	w := seedWorkHierarchy(t)
	if _, err := w.tx.Exec(`INSERT INTO features (feature_id, organization_id, user_id, product_id, title)
	        VALUES ($1,$2,$3,$4,'F2')`, whOtherFeature, w.org, w.user, w.product); err != nil {
		t.Fatalf("seed second feature: %v", err)
	}
	if _, err := w.tx.Exec(`INSERT INTO epics
	        (epic_id, organization_id, user_id, product_id, feature_id, repository_id, title)
	        VALUES ($1,$2,$3,$4,$5,$6,'E3')`,
		whOtherEpic, w.org, w.user, w.product, whOtherFeature, w.repo); err != nil {
		t.Fatalf("seed epic in second feature: %v", err)
	}

	w.rejectsWith(t, "epic_dependencies_predecessor_fkey",
		"an Epic edge crossed Features",
		`INSERT INTO epic_dependencies
		 (organization_id, product_id, feature_id, successor_epic_id, predecessor_epic_id)
		 VALUES ($1,$2,$3,$4,$5)`,
		w.org, w.product, w.feature, w.epic, whOtherEpic)
}

func TestNoSelfEdgeInEitherGraph(t *testing.T) {
	w := seedWorkHierarchy(t)
	w.rejectsWith(t, "story_dependencies_no_self_edge_check", "a Story depended on itself",
		`INSERT INTO story_dependencies
		 (organization_id, product_id, feature_id, epic_id, successor_story_id, predecessor_story_id)
		 VALUES ($1,$2,$3,$4,$5,$5)`,
		w.org, w.product, w.feature, w.epic, w.story)
	w.rejectsWith(t, "epic_dependencies_no_self_edge_check", "an Epic depended on itself",
		`INSERT INTO epic_dependencies
		 (organization_id, product_id, feature_id, successor_epic_id, predecessor_epic_id)
		 VALUES ($1,$2,$3,$4,$4)`,
		w.org, w.product, w.feature, w.epic)
}

// The edge's completion pointer is scoped to the PREDECESSOR -- the Story
// being depended upon, not the dependent one.
func TestEdgeCompletionPointerIsScopeBound(t *testing.T) {
	w := seedWorkHierarchy(t)
	insert := `INSERT INTO story_dependencies
	           (organization_id, product_id, feature_id, epic_id, successor_story_id,
	            predecessor_story_id, satisfying_completion_artifact_id)
	           VALUES ($1,$2,$3,$4,$5,$6,$7)`

	w.rejectsWith(t, "story_dependencies_completion_fkey",
		"an edge's completion was scoped to the successor rather than the predecessor",
		insert, w.org, w.product, w.feature, w.epic, w.story, w.storySameEpic, whStory1Plan)

	w.rejectsWith(t, "story_dependencies_completion_fkey",
		"an edge's completion named an AMENDMENT rather than the original",
		insert, w.org, w.product, w.feature, w.epic, w.storySameEpic, w.story, whStory1Amend)

	w.rejectsWith(t, "story_dependencies_completion_original_check",
		"an edge's completion discriminator was set true, so it could name an amendment",
		`INSERT INTO story_dependencies
		 (organization_id, product_id, feature_id, epic_id, successor_story_id,
		  predecessor_story_id, satisfying_completion_is_amendment)
		 VALUES ($1,$2,$3,$4,$5,$6,true)`,
		w.org, w.product, w.feature, w.epic, w.story, w.storySameEpic)
}

func TestBasisRowCannotCrossLineage(t *testing.T) {
	w := seedWorkHierarchy(t)
	w.insertDispatch(t)

	insert := `INSERT INTO dispatch_basis_dependencies
	           (story_dispatch_id, organization_id, product_id, feature_id, epic_id,
	            predecessor_story_id, completion_artifact_id,
	            completion_effective_digest, completion_effective_sequence)
	           VALUES ($1,$2,$3,$4,$5,$6,$7,$8,0)`

	// Story 2 carries its OWN completion, so the only thing wrong is the Epic.
	w.rejectsWith(t, "dispatch_basis_dependencies_predecessor_fkey",
		"a basis row named a predecessor in a different Epic",
		insert, w.dispatch, w.org, w.product, w.feature, w.epic, w.story2, whStory2Plan, digestA)

	// Same Epic, correct predecessor -- only the completion's scope is wrong.
	w.rejectsWith(t, "dispatch_basis_dependencies_completion_fkey",
		"a basis row's completion was scoped to a Story other than its predecessor",
		insert, w.dispatch, w.org, w.product, w.feature, w.epic, w.storySameEpic, whStory1Plan, digestA)

	w.rejectsWith(t, "dispatch_basis_dependencies_completion_fkey",
		"a basis row's completion named an AMENDMENT rather than the original",
		insert, w.dispatch, w.org, w.product, w.feature, w.epic, w.story, whStory1Amend, digestA)

	w.rejectsWith(t, "dispatch_basis_dependencies_completion_original_check",
		"a basis row's completion discriminator was set true",
		`INSERT INTO dispatch_basis_dependencies
		 (story_dispatch_id, organization_id, product_id, feature_id, epic_id,
		  predecessor_story_id, completion_artifact_id, completion_is_amendment,
		  completion_effective_digest, completion_effective_sequence)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,true,$8,0)`,
		w.dispatch, w.org, w.product, w.feature, w.epic, w.storySameEpic, whSiblingPlan, digestA)

	w.rejectsWith(t, "dispatch_basis_dependencies_completion_digest_check",
		"a basis row carried a malformed effective digest",
		insert, w.dispatch, w.org, w.product, w.feature, w.epic, w.storySameEpic, whSiblingPlan, "NOTHEX")
}

// Both governing pointers, both rules each: scope-bound, and original-only.
func TestGoverningPointersAreScopeBoundAndOriginalOnly(t *testing.T) {
	w := seedWorkHierarchy(t)

	w.rejectsWith(t, "stories_governing_fkey",
		"a Story's governing pointer named an artifact scoped to a different Story",
		`UPDATE stories SET governing_artifact_id=$1 WHERE story_id=$2`, whSiblingPlan, w.story)
	w.rejectsWith(t, "stories_governing_fkey",
		"a Story's governing pointer named an AMENDMENT rather than the original",
		`UPDATE stories SET governing_artifact_id=$1 WHERE story_id=$2`, whStory1Amend, w.story)
	w.rejectsWith(t, "stories_governing_original_check",
		"a Story's governing discriminator was set true",
		`UPDATE stories SET governing_is_amendment=true WHERE story_id=$1`, w.story)

	w.rejectsWith(t, "epics_governing_fkey",
		"an Epic's governing pointer named an artifact scoped to a different Epic",
		`UPDATE epics SET governing_artifact_id=$1 WHERE epic_id=$2`, whEpic2Plan, w.epic)
	w.rejectsWith(t, "epics_governing_fkey",
		"an Epic's governing pointer named an AMENDMENT rather than the original",
		`UPDATE epics SET governing_artifact_id=$1 WHERE epic_id=$2`, whEpic1Amend, w.epic)
	w.rejectsWith(t, "epics_governing_original_check",
		"an Epic's governing discriminator was set true",
		`UPDATE epics SET governing_is_amendment=true WHERE epic_id=$1`, w.epic)
}

// D13's rule at EVERY reference site: the pointer may be null, the
// discriminator may not. A null discriminator beside a non-null artifact id
// skips the whole composite key under MATCH SIMPLE, taking the original-only
// AND scope claims with it -- so the guard is worthless anywhere it is
// missing, and six sites means six cases.
func TestDiscriminatorCannotBeNulledAtAnySite(t *testing.T) {
	w := seedWorkHierarchy(t)

	// The two governing pointers, each with a VALID pointer already set, so
	// the case is about nulling the discriminator rather than about an empty
	// row.
	if _, err := w.tx.Exec(`UPDATE stories SET governing_artifact_id=$1 WHERE story_id=$2`,
		whStory1Plan, w.story); err != nil {
		t.Fatalf("seed story pointer: %v", err)
	}
	if _, err := w.tx.Exec(`UPDATE epics SET governing_artifact_id=$1 WHERE epic_id=$2`,
		whEpic1Plan, w.epic); err != nil {
		t.Fatalf("seed epic pointer: %v", err)
	}
	w.rejectsNotNull(t, "governing_is_amendment",
		"a Story's governing discriminator was nulled beside a live pointer",
		`UPDATE stories SET governing_is_amendment=NULL WHERE story_id=$1`, w.story)
	w.rejectsNotNull(t, "governing_is_amendment",
		"an Epic's governing discriminator was nulled beside a live pointer",
		`UPDATE epics SET governing_is_amendment=NULL WHERE epic_id=$1`, w.epic)

	// The two dispatch version references.
	dispatchWithNull := `INSERT INTO story_dispatches (
	        story_dispatch_id, organization_id, product_id, feature_id, epic_id, story_id,
	        work_group_id, disposition,
	        story_version_artifact_id, story_version_is_amendment,
	        story_version_effective_digest, story_version_effective_sequence,
	        epic_version_artifact_id, epic_version_is_amendment,
	        epic_version_effective_digest, epic_version_effective_sequence)
	      VALUES ($1,$2,$3,$4,$5,$6,$7,'pending',$8,$9,$10,0,$11,$12,$13,0)`
	w.rejectsNotNull(t, "story_version_is_amendment",
		"a dispatch nulled its Story version discriminator", dispatchWithNull,
		"30000000-0000-7000-8000-0000000000b1", w.org, w.product, w.feature, w.epic, w.story,
		w.workGroup, whStory1Plan, nil, digestA, whEpic1Plan, false, digestB)
	w.rejectsNotNull(t, "epic_version_is_amendment",
		"a dispatch nulled its Epic version discriminator", dispatchWithNull,
		"30000000-0000-7000-8000-0000000000b2", w.org, w.product, w.feature, w.epic, w.story,
		w.workGroup, whStory1Plan, false, digestA, whEpic1Plan, nil, digestB)

	// The basis snapshot's completion.
	w.insertDispatch(t)
	w.rejectsNotNull(t, "completion_is_amendment",
		"a basis row nulled its completion discriminator",
		`INSERT INTO dispatch_basis_dependencies
		 (story_dispatch_id, organization_id, product_id, feature_id, epic_id,
		  predecessor_story_id, completion_artifact_id, completion_is_amendment,
		  completion_effective_digest, completion_effective_sequence)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,0)`,
		w.dispatch, w.org, w.product, w.feature, w.epic, w.storySameEpic, whSiblingPlan, nil, digestA)

	// The edge's current-completion pointer.
	w.rejectsNotNull(t, "satisfying_completion_is_amendment",
		"an edge nulled its completion discriminator",
		`INSERT INTO story_dependencies
		 (organization_id, product_id, feature_id, epic_id, successor_story_id,
		  predecessor_story_id, satisfying_completion_artifact_id,
		  satisfying_completion_is_amendment)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		w.org, w.product, w.feature, w.epic, w.story, w.storySameEpic, whSiblingPlan, nil)
}

// The constraint D13 FORBIDS. Divergence between the dispatch snapshot and
// the current pointer IS test 1's signal, so repointing must SUCCEED -- a
// failure means someone constrained them to agree and the comparison has gone
// vacuous.
//
// It repoints to a genuine second accepted original rather than editing a
// cached digest, because the pointer names an artifact and nothing else: the
// effective view is assembled by EffectiveView at read time, so there is no
// duplicate state here to drift.
func TestSnapshotMayDivergeFromTheCurrentPointer(t *testing.T) {
	w := seedWorkHierarchy(t)
	w.insertDispatch(t) // snapshot names whStory1Plan

	if _, err := w.tx.Exec(
		`UPDATE stories SET governing_artifact_id=$1 WHERE story_id=$2`, whStory1Plan, w.story); err != nil {
		t.Fatalf("set governing pointer: %v", err)
	}
	if _, err := w.tx.Exec(
		`UPDATE stories SET governing_artifact_id=$1 WHERE story_id=$2`, whStory1Alt, w.story); err != nil {
		t.Fatalf("the current pointer could not be moved off the dispatch's snapshot, so the "+
			"basis comparison has nothing to detect: %v", err)
	}

	var same bool
	if err := w.tx.QueryRow(`SELECT d.story_version_artifact_id = s.governing_artifact_id
	                           FROM story_dispatches d JOIN stories s ON s.story_id = d.story_id
	                          WHERE d.story_dispatch_id = $1`, w.dispatch).Scan(&same); err != nil {
		t.Fatalf("compare snapshot with pointer: %v", err)
	}
	if same {
		t.Fatal("snapshot and pointer still agree, so this case is not exercising divergence")
	}
}
