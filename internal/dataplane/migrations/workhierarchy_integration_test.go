//go:build integration

package migrations_test

import (
	"testing"
)

// Constraint tests for migration 000021 (docs/v2/phase_3/design_work-hierarchy.md).
//
// Item 2 lands schema, so item 2's tests are constraint tests: each asserts a
// statement the database must REFUSE. The design's testing split assigns the
// application-behaviour obligations -- atomic dispatch creation, effective-view
// comparison, serialized graph mutation -- to items 3, 5 and 9, because
// reenacting them in raw SQL here would only demonstrate that the reenactment
// agrees with itself.
//
// Every case reuses the fixture in schema_integration_test.go: a seeded
// transaction rolled back on cleanup, and `rejects`, which runs each probe
// inside a savepoint so one refusal does not wreck the statements after it.

// A second Epic and Story, for the cross-lineage cases. They live under the
// same Feature so a rejection cannot be explained by the Feature differing.
type wh struct {
	*fixture
	epic2, story2, storySameEpic string
	workGroup, dispatch          string
	planArtifact                 string
}

const (
	whEpic2        = "30000000-0000-7000-8000-000000000001"
	whStory2       = "30000000-0000-7000-8000-000000000002"
	whStorySame    = "30000000-0000-7000-8000-000000000003"
	whWorkGroup    = "30000000-0000-7000-8000-000000000004"
	whDispatch     = "30000000-0000-7000-8000-000000000005"
	whPlanArtifact = "30000000-0000-7000-8000-000000000006"
	whExecution    = "30000000-0000-7000-8000-000000000007"
	whEpicArtifact = "30000000-0000-7000-8000-000000000008"
)

// seedWorkHierarchy adds a second Epic, a second Story under it, a sibling
// Story in the ORIGINAL Epic, and the artifacts a dispatch's two version
// references need.
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
		planArtifact:  whPlanArtifact,
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

	// A Story-scoped artifact (for the Story version reference and for
	// completions) and an Epic-scoped one (for the Epic version reference).
	if err := f.insertStoryArtifact(w.planArtifact, nil); err != nil {
		t.Fatalf("seed story artifact: %v", err)
	}
	if err := f.insertStoryArtifact(whEpicArtifact, map[string]any{
		"scope_type":     "epic",
		"scope_story_id": nil,
		"scope_epic_id":  f.epic,
		"story_id":       nil,
	}); err != nil {
		t.Fatalf("seed epic artifact: %v", err)
	}
	return w
}

// insertDispatch writes a well-formed pending dispatch, overriding whichever
// columns a case wants to break. Returned rather than executed so callers can
// hand the statement to `rejects`.
func (w *wh) dispatchSQL() (string, []any) {
	return `INSERT INTO story_dispatches (
	            story_dispatch_id, organization_id, product_id, feature_id, epic_id, story_id,
	            work_group_id, disposition,
	            story_version_artifact_id, story_version_effective_digest, story_version_effective_sequence,
	            epic_version_artifact_id,  epic_version_effective_digest,  epic_version_effective_sequence)
	        VALUES ($1,$2,$3,$4,$5,$6,$7,'pending',$8,$9,0,$10,$11,0)`,
		[]any{w.dispatch, w.org, w.product, w.feature, w.epic, w.story,
			w.workGroup, w.planArtifact, digestA, whEpicArtifact, digestB}
}

func (w *wh) insertDispatch(t *testing.T) {
	t.Helper()
	stmt, args := w.dispatchSQL()
	if _, err := w.tx.Exec(stmt, args...); err != nil {
		t.Fatalf("a well-formed dispatch was rejected: %v", err)
	}
}

func TestWellFormedWorkHierarchyRowsAreAccepted(t *testing.T) {
	w := seedWorkHierarchy(t)
	w.insertDispatch(t)

	// Accepting the dispatch, then an execution against it.
	if _, err := w.tx.Exec(
		`UPDATE story_dispatches SET disposition='accepted', settled_at=now() WHERE story_dispatch_id=$1`,
		w.dispatch); err != nil {
		t.Fatalf("accepting a pending dispatch was rejected: %v", err)
	}
	if _, err := w.tx.Exec(`INSERT INTO executions
	        (execution_id, organization_id, product_id, feature_id, epic_id, story_id, story_dispatch_id)
	        VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		whExecution, w.org, w.product, w.feature, w.epic, w.story, w.dispatch); err != nil {
		t.Fatalf("a well-formed execution was rejected: %v", err)
	}

	// A well-formed edge, and a Story governing pointer.
	if _, err := w.tx.Exec(`INSERT INTO story_dependencies
	        (organization_id, product_id, feature_id, epic_id, successor_story_id, predecessor_story_id)
	        VALUES ($1,$2,$3,$4,$5,$6)`,
		w.org, w.product, w.feature, w.epic, w.story, w.storySameEpic); err != nil {
		t.Fatalf("a well-formed story edge was rejected: %v", err)
	}
	if _, err := w.tx.Exec(`UPDATE stories
	        SET governing_artifact_id=$1, governing_effective_digest=$2, governing_effective_sequence=0
	        WHERE story_id=$3`, w.planArtifact, digestA, w.story); err != nil {
		t.Fatalf("a well-formed governing pointer was rejected: %v", err)
	}
}

func TestWorkGroupIsOnePerEpic(t *testing.T) {
	w := seedWorkHierarchy(t)
	w.rejects(t, "a second Work Group for one Epic was accepted; ADR 0018's MVP cardinality is not enforced",
		`INSERT INTO work_groups (work_group_id, organization_id, product_id, feature_id, epic_id)
		 VALUES ($1,$2,$3,$4,$5)`,
		"30000000-0000-7000-8000-00000000000a", w.org, w.product, w.feature, w.epic)
}

// The Work Group foreign key travels the Epic lineage, so a dispatch cannot
// borrow the Work Group of another Epic.
func TestDispatchCannotBorrowAnotherEpicsWorkGroup(t *testing.T) {
	w := seedWorkHierarchy(t)
	w.rejects(t, "a dispatch named a Work Group belonging to a different Epic",
		`INSERT INTO story_dispatches (
		     story_dispatch_id, organization_id, product_id, feature_id, epic_id, story_id,
		     work_group_id, disposition,
		     story_version_artifact_id, story_version_effective_digest, story_version_effective_sequence,
		     epic_version_artifact_id,  epic_version_effective_digest,  epic_version_effective_sequence)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,'pending',$8,$9,0,$10,$11,0)`,
		"30000000-0000-7000-8000-00000000000b", w.org, w.product, w.feature,
		w.epic2, w.story2, w.workGroup, // Story 2 is in Epic 2; the Work Group is Epic 1's
		w.planArtifact, digestA, whEpicArtifact, digestB)
}

func TestDispatchShapeConstraints(t *testing.T) {
	w := seedWorkHierarchy(t)
	base := `INSERT INTO story_dispatches (
	             story_dispatch_id, organization_id, product_id, feature_id, epic_id, story_id,
	             work_group_id, disposition, settled_at, failure_code, failure_detail,
	             story_version_artifact_id, story_version_effective_digest, story_version_effective_sequence,
	             epic_version_artifact_id,  epic_version_effective_digest,  epic_version_effective_sequence)
	         VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,0,$14,$15,0)`

	probe := func(because, disposition string, settledAt, failureCode, failureDetail any) {
		w.rejects(t, because, base,
			"30000000-0000-7000-8000-00000000000c", w.org, w.product, w.feature, w.epic, w.story,
			w.workGroup, disposition, settledAt, failureCode, failureDetail,
			w.planArtifact, digestA, whEpicArtifact, digestB)
	}

	probe("a pending dispatch carried settled_at", "pending", "now()", nil, nil)
	probe("a terminal disposition had no settled_at", "invalidated", nil, nil, nil)
	probe("a failed dispatch had no failure_code", "failed", "now()", nil, nil)
	probe("failure_detail was accepted without a failure_code", "invalidated", "now()", nil, "why")
	probe("an unknown disposition was accepted", "abandoned", "now()", nil, nil)
}

// Three statements rather than one: an execution against a pending dispatch
// passing proves nothing about failed or invalidated, and the constant column
// plus its foreign key is the only thing excluding any of them.
func TestExecutionRequiresAnAcceptedDispatch(t *testing.T) {
	w := seedWorkHierarchy(t)
	w.insertDispatch(t)

	insert := `INSERT INTO executions
	           (execution_id, organization_id, product_id, feature_id, epic_id, story_id, story_dispatch_id)
	           VALUES ($1,$2,$3,$4,$5,$6,$7)`

	w.rejects(t, "an execution was created against a PENDING dispatch", insert,
		whExecution, w.org, w.product, w.feature, w.epic, w.story, w.dispatch)

	for _, disposition := range []string{"failed", "invalidated"} {
		var stmt string
		if disposition == "failed" {
			stmt = `UPDATE story_dispatches SET disposition='failed', settled_at=now(), failure_code='handshake'
			        WHERE story_dispatch_id=$1`
		} else {
			stmt = `UPDATE story_dispatches SET disposition='invalidated', settled_at=now(), failure_code=NULL
			        WHERE story_dispatch_id=$1`
		}
		if _, err := w.tx.Exec(stmt, w.dispatch); err != nil {
			t.Fatalf("settling the dispatch as %s: %v", disposition, err)
		}
		w.rejects(t, "an execution was created against a "+disposition+" dispatch", insert,
			whExecution, w.org, w.product, w.feature, w.epic, w.story, w.dispatch)
	}
}

// The accepted-dispatch key carries the full Story lineage. Without that, an
// execution could hold Story B's lineage while naming Story A's dispatch --
// the misattribution migration 000005 calls worse than none.
func TestExecutionIsBoundToItsDispatchesStory(t *testing.T) {
	w := seedWorkHierarchy(t)
	w.insertDispatch(t)
	if _, err := w.tx.Exec(
		`UPDATE story_dispatches SET disposition='accepted', settled_at=now() WHERE story_dispatch_id=$1`,
		w.dispatch); err != nil {
		t.Fatalf("accept dispatch: %v", err)
	}

	w.rejects(t, "an execution carried Story 2's lineage while referencing Story 1's accepted dispatch",
		`INSERT INTO executions
		 (execution_id, organization_id, product_id, feature_id, epic_id, story_id, story_dispatch_id)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		whExecution, w.org, w.product, w.feature, w.epic2, w.story2, w.dispatch)
}

func TestExecutionAuthorityConstraints(t *testing.T) {
	w := seedWorkHierarchy(t)
	w.insertDispatch(t)
	if _, err := w.tx.Exec(
		`UPDATE story_dispatches SET disposition='accepted', settled_at=now() WHERE story_dispatch_id=$1`,
		w.dispatch); err != nil {
		t.Fatalf("accept dispatch: %v", err)
	}
	if _, err := w.tx.Exec(`INSERT INTO executions
	        (execution_id, organization_id, product_id, feature_id, epic_id, story_id, story_dispatch_id)
	        VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		whExecution, w.org, w.product, w.feature, w.epic, w.story, w.dispatch); err != nil {
		t.Fatalf("seed execution: %v", err)
	}

	w.rejects(t, "authority was superseded without closing admission",
		`UPDATE executions SET authority_state='superseded' WHERE execution_id=$1`, whExecution)

	w.rejects(t, "an unknown authority_state was accepted",
		`UPDATE executions SET authority_state='revoked' WHERE execution_id=$1`, whExecution)

	w.rejects(t, "a second execution was created for one dispatch",
		`INSERT INTO executions
		 (execution_id, organization_id, product_id, feature_id, epic_id, story_id, story_dispatch_id)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		"30000000-0000-7000-8000-00000000000d", w.org, w.product, w.feature, w.epic, w.story, w.dispatch)

	// The inverse: closing admission while authority is still current is a
	// REAL state (a headless block is a forced stop with no basis
	// supersession), so this must succeed.
	if _, err := w.tx.Exec(
		`UPDATE executions SET admission_closed_at=now() WHERE execution_id=$1`, whExecution); err != nil {
		t.Fatalf("closing admission under current authority was rejected, but ADR 0032's "+
			"headless block is exactly that state: %v", err)
	}
}

func TestStoryEdgeCannotCrossEpics(t *testing.T) {
	w := seedWorkHierarchy(t)
	w.rejects(t, "a Story edge crossed Epics; ADR 0024's within-an-Epic rule is not enforced",
		`INSERT INTO story_dependencies
		 (organization_id, product_id, feature_id, epic_id, successor_story_id, predecessor_story_id)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		w.org, w.product, w.feature, w.epic, w.story, w.story2) // story2 is in epic2
}

func TestEpicEdgeCannotCrossFeatures(t *testing.T) {
	w := seedWorkHierarchy(t)
	otherFeature := "30000000-0000-7000-8000-00000000000e"
	otherEpic := "30000000-0000-7000-8000-00000000000f"
	if _, err := w.tx.Exec(`INSERT INTO features (feature_id, organization_id, user_id, product_id, title)
	        VALUES ($1,$2,$3,$4,'F2')`, otherFeature, w.org, w.user, w.product); err != nil {
		t.Fatalf("seed second feature: %v", err)
	}
	if _, err := w.tx.Exec(`INSERT INTO epics
	        (epic_id, organization_id, user_id, product_id, feature_id, repository_id, title)
	        VALUES ($1,$2,$3,$4,$5,$6,'E3')`,
		otherEpic, w.org, w.user, w.product, otherFeature, w.repo); err != nil {
		t.Fatalf("seed epic in second feature: %v", err)
	}

	w.rejects(t, "an Epic edge crossed Features",
		`INSERT INTO epic_dependencies
		 (organization_id, product_id, feature_id, successor_epic_id, predecessor_epic_id)
		 VALUES ($1,$2,$3,$4,$5)`,
		w.org, w.product, w.feature, w.epic, otherEpic)
}

func TestNoSelfEdgeInEitherGraph(t *testing.T) {
	w := seedWorkHierarchy(t)
	w.rejects(t, "a Story depended on itself",
		`INSERT INTO story_dependencies
		 (organization_id, product_id, feature_id, epic_id, successor_story_id, predecessor_story_id)
		 VALUES ($1,$2,$3,$4,$5,$5)`,
		w.org, w.product, w.feature, w.epic, w.story)
	w.rejects(t, "an Epic depended on itself",
		`INSERT INTO epic_dependencies
		 (organization_id, product_id, feature_id, successor_epic_id, predecessor_epic_id)
		 VALUES ($1,$2,$3,$4,$4)`,
		w.org, w.product, w.feature, w.epic)
}

// Each governing reference is bound to its own work item through the scope
// column. Without scope in the key these would pass the tenant check.
func TestGoverningReferenceIsBoundToItsOwnWork(t *testing.T) {
	w := seedWorkHierarchy(t)

	// An artifact scoped to the SIBLING story, used as Story 1's version.
	sibling := "30000000-0000-7000-8000-000000000010"
	if err := w.insertStoryArtifact(sibling, map[string]any{
		"scope_story_id": w.storySameEpic,
		"story_id":       w.storySameEpic,
	}); err != nil {
		t.Fatalf("seed sibling artifact: %v", err)
	}

	w.rejects(t, "a dispatch's Story version named an artifact scoped to a different Story",
		`INSERT INTO story_dispatches (
		     story_dispatch_id, organization_id, product_id, feature_id, epic_id, story_id,
		     work_group_id, disposition,
		     story_version_artifact_id, story_version_effective_digest, story_version_effective_sequence,
		     epic_version_artifact_id,  epic_version_effective_digest,  epic_version_effective_sequence)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,'pending',$8,$9,0,$10,$11,0)`,
		"30000000-0000-7000-8000-000000000011", w.org, w.product, w.feature, w.epic, w.story,
		w.workGroup, sibling, digestA, whEpicArtifact, digestB)

	w.rejects(t, "a Story's governing pointer named an artifact scoped to a different Story",
		`UPDATE stories SET governing_artifact_id=$1, governing_effective_digest=$2,
		 governing_effective_sequence=0 WHERE story_id=$3`,
		sibling, digestA, w.story)
}

// The pointer is nullable; the discriminator is not. A nullable discriminator
// beside a non-null artifact id would skip the whole composite key under
// MATCH SIMPLE, taking the original-only AND scope claims with it.
func TestDiscriminatorCannotBeNulled(t *testing.T) {
	w := seedWorkHierarchy(t)
	w.rejects(t, "governing_is_amendment was set to NULL, which would skip the composite foreign key",
		`UPDATE stories SET governing_is_amendment=NULL WHERE story_id=$1`, w.story)
	w.rejects(t, "governing_is_amendment was set true, so the pointer could name an amendment",
		`UPDATE stories SET governing_is_amendment=true WHERE story_id=$1`, w.story)
}

// All three parts of a reference or none: a half-filled pointer would leave
// the digest unusable while the artifact id reads as set.
func TestGoverningPointerIsAllOrNothing(t *testing.T) {
	w := seedWorkHierarchy(t)
	w.rejects(t, "a governing pointer was set without its digest and sequence",
		`UPDATE stories SET governing_artifact_id=$1 WHERE story_id=$2`, w.planArtifact, w.story)
	w.rejects(t, "a malformed digest was accepted",
		`UPDATE stories SET governing_artifact_id=$1, governing_effective_digest='NOTHEX',
		 governing_effective_sequence=0 WHERE story_id=$2`, w.planArtifact, w.story)
	w.rejects(t, "a negative effective sequence was accepted",
		`UPDATE stories SET governing_artifact_id=$1, governing_effective_digest=$2,
		 governing_effective_sequence=-1 WHERE story_id=$3`, w.planArtifact, digestA, w.story)
}

// The basis child must prove its predecessor and completion belong to the
// dispatch's own lineage, not merely its organization.
func TestBasisRowCannotCrossLineage(t *testing.T) {
	w := seedWorkHierarchy(t)
	w.insertDispatch(t)

	insert := `INSERT INTO dispatch_basis_dependencies
	           (story_dispatch_id, organization_id, product_id, feature_id, epic_id,
	            predecessor_story_id, completion_artifact_id,
	            completion_effective_digest, completion_effective_sequence)
	           VALUES ($1,$2,$3,$4,$5,$6,$7,$8,0)`

	w.rejects(t, "a basis row named a predecessor in a different Epic", insert,
		w.dispatch, w.org, w.product, w.feature, w.epic, w.story2, w.planArtifact, digestA)

	// A completion scoped to the DISPATCH's story rather than to the
	// predecessor. Same organization, same Epic -- only the scope is wrong.
	w.rejects(t, "a basis row's completion was scoped to a Story other than its predecessor", insert,
		w.dispatch, w.org, w.product, w.feature, w.epic, w.storySameEpic, w.planArtifact, digestA)
}

// The constraint D13 forbids. Divergence between the dispatch snapshot and the
// current pointer IS test 1's signal, so this must SUCCEED -- a failure means
// someone constrained them to agree and the detection mechanism is gone.
func TestSnapshotMayDivergeFromTheCurrentPointer(t *testing.T) {
	w := seedWorkHierarchy(t)
	w.insertDispatch(t)

	if _, err := w.tx.Exec(`UPDATE stories
	        SET governing_artifact_id=$1, governing_effective_digest=$2, governing_effective_sequence=0
	        WHERE story_id=$3`, w.planArtifact, digestA, w.story); err != nil {
		t.Fatalf("set governing pointer: %v", err)
	}

	// Move the pointer's effective view on while the dispatch keeps the old
	// snapshot. This is precisely the state test 1 detects.
	if _, err := w.tx.Exec(`UPDATE stories
	        SET governing_effective_digest=$1, governing_effective_sequence=1
	        WHERE story_id=$2`, digestB, w.story); err != nil {
		t.Fatalf("the current pointer could not diverge from the dispatch snapshot, so the "+
			"basis comparison has nothing to detect: %v", err)
	}
}
