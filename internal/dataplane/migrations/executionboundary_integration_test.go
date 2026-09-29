//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"orchestrator/internal/dataplane/migrations"
)

// Tests for migration 000024 (Phase 3 item 5 design, D11 and D12).
//
// Four groups: the executions rules (configuration, terminal result,
// immutability), the tool_calls rules (identity, claim, reason code,
// decision, drainage, serialization), the two references the migration adds
// (a live principal's execution; a blocked result's attempt), and the up
// migration's total-or-refuse guard with the down migration's refusals --
// each guard proven by the exact defect it claims to catch, per the design's
// mutant table. Constraint cases run against a schema this binary's SQL just
// built (the shared plane, migrated once), so a mutant of 000024 reaches
// every one of them.

const (
	ebAttempt   = "60000000-0000-7000-8000-000000000001"
	ebAttempt2  = "60000000-0000-7000-8000-000000000002"
	ebAttempt3  = "60000000-0000-7000-8000-000000000003"
	ebPrincipal = "60000000-0000-7000-8000-000000000010"
	ebStranger  = "60000000-0000-7000-8000-0000000000f0"
)

// eb is the work-hierarchy fixture with an ACCEPTED dispatch and an
// execution, which is what every attempt needs.
type eb struct {
	*wh
	execution string
}

// disposablePlane opens a DISPOSABLE database at the head of the ladder,
// not the shared plane openPlane migrates: that one is migrated once and is
// idempotent afterwards, so a change to 000024 -- or a mutant of it --
// never reaches the schema a test on it observes. Every constraint case in
// this file runs against a schema this binary's SQL just built.
func disposablePlane(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", disposableDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func seedExecutionBoundary(t *testing.T) *eb {
	t.Helper()
	w := seedWorkHierarchyOn(t, disposablePlane(t))
	w.insertDispatch(t)
	w.acceptDispatch(t)
	if _, err := w.tx.Exec(executionInsert(t, w.tx, w.user),
		whExecution, w.org, w.product, w.feature, w.epic, w.story, w.dispatch); err != nil {
		t.Fatalf("seed execution: %v", err)
	}
	return &eb{wh: w, execution: whExecution}
}

// rejectsSaying is rejectsWith for a TRIGGER: a RAISE carries no constraint
// name, so the refusal is matched on the message it renders.
func (f *fixture) rejectsSaying(t *testing.T, fragment, because, stmt string, args ...any) {
	t.Helper()
	if _, err := f.tx.Exec("SAVEPOINT reject_trigger_probe"); err != nil {
		t.Fatalf("savepoint: %v", err)
	}
	_, err := f.tx.Exec(stmt, args...)
	if err == nil {
		t.Fatal(because)
	}
	var pgErr *pgconn.PgError
	switch {
	case !errors.As(err, &pgErr):
		t.Fatalf("%s: wanted a trigger saying %q, got a non-Postgres error: %v", because, fragment, err)
	case !strings.Contains(pgErr.Message, fragment):
		t.Fatalf("%s: wanted a trigger saying %q, but the refusal was: %s (constraint %q)",
			because, fragment, pgErr.Message, pgErr.ConstraintName)
	}
	if _, rbErr := f.tx.Exec("ROLLBACK TO SAVEPOINT reject_trigger_probe"); rbErr != nil {
		t.Fatalf("rollback to savepoint: %v", rbErr)
	}
}

func (f *fixture) mustExec(t *testing.T, because, stmt string, args ...any) {
	t.Helper()
	if _, err := f.tx.Exec(stmt, args...); err != nil {
		t.Fatalf("%s: %v", because, err)
	}
}

// --- executions: the configuration ------------------------------------------

func TestExecutionConfigurationIsCanonicalAndRequired(t *testing.T) {
	w := seedWorkHierarchyOn(t, disposablePlane(t))
	w.insertDispatch(t)
	w.acceptDispatch(t)
	insert := `INSERT INTO executions
	    (execution_id, organization_id, product_id, feature_id, epic_id, story_id, story_dispatch_id,
	     capability_set, headless, acting_user_id)
	    VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9,$10)`
	args := func(set any, user string) []any {
		return []any{whExecution, w.org, w.product, w.feature, w.epic, w.story, w.dispatch, set, false, user}
	}

	for _, bad := range []struct{ because, set string }{
		{"an unsorted set", `["b","a"]`},
		{"a duplicated identity", `["a","a"]`},
		{"a blank identity", `[""]`},
		{"a non-string member", `[1]`},
		{"an object rather than an array", `{"a":true}`},
		{"a bare string", `"forge/story_pull_request"`},
	} {
		w.rejectsWith(t, "executions_capability_set_check", bad.because+" was stored as a capability set",
			insert, args(bad.set, w.user)...)
	}
	w.rejectsWith(t, "executions_acting_user_fkey",
		"an execution bound a user of another organization as its acting user",
		insert, args(`[]`, w.seedStrangerUser(t))...)
	w.rejects(t, "an execution with no capability set was accepted; the column is NOT NULL with no default",
		insert, args(nil, w.user)...)

	w.mustExec(t, "a canonical set was refused", insert, args(`["a","b"]`, w.user)...)
}

// seedStrangerUser provisions a user in ANOTHER organization, for the
// cross-tenant cases.
func (w *wh) seedStrangerUser(t *testing.T) string {
	t.Helper()
	const strangerOrg = "60000000-0000-7000-8000-0000000000e0"
	w.mustExec(t, "seed stranger organization",
		`INSERT INTO organizations (organization_id, slug, display_name) VALUES ($1,'stranger','Stranger')`, strangerOrg)
	w.mustExec(t, "seed stranger user",
		`INSERT INTO users (user_id, organization_id, handle, display_name) VALUES ($1,$2,'s','S')`, ebStranger, strangerOrg)
	return ebStranger
}

func TestExecutionConfigurationIsImmutable(t *testing.T) {
	e := seedExecutionBoundary(t)
	for _, tc := range []struct{ because, update string }{
		{"the capability set was rewritten", `UPDATE executions SET capability_set='["x"]'::jsonb WHERE execution_id=$1`},
		{"headless was flipped", `UPDATE executions SET headless=true WHERE execution_id=$1`},
		{"the acting user was moved", `UPDATE executions SET acting_user_id=$2 WHERE execution_id=$1`},
	} {
		args := []any{e.execution}
		if strings.Contains(tc.update, "$2") {
			args = append(args, e.seedStrangerUser(t))
		}
		e.rejectsSaying(t, "configuration is immutable", tc.because, tc.update, args...)
	}
	// The trigger is not a blanket refusal: closing admission is an update
	// of the same row and must pass.
	e.mustExec(t, "closing admission was refused by the immutability trigger",
		`UPDATE executions SET admission_closed_at=now() WHERE execution_id=$1`, e.execution)
}

// --- executions: the terminal result ----------------------------------------

// terminalUpdate writes the terminal columns directly, as a writer that
// bypasses the seam would.
const terminalUpdate = `UPDATE executions
    SET status=$2, completion_disposition=$3, cancellation_reason=$4, failure_class=$5,
        blocked_tool_call_id=$6, error_message=$7, terminated_at=$8
    WHERE execution_id=$1`

func TestTerminalAxesAreClosedVocabularies(t *testing.T) {
	e := seedExecutionBoundary(t)
	e.mustExec(t, "close admission", `UPDATE executions SET admission_closed_at=now() WHERE execution_id=$1`, e.execution)

	e.rejectsWith(t, "executions_status_check", "a status outside the five was stored",
		terminalUpdate, e.execution, "bogus", nil, nil, nil, nil, nil, "now()")
	e.rejectsWith(t, "executions_completion_disposition_check", "a completion disposition outside the two",
		terminalUpdate, e.execution, "completed", "partial", nil, nil, nil, nil, "now()")
	e.rejectsWith(t, "executions_cancellation_reason_check", "a cancellation reason outside the three",
		terminalUpdate, e.execution, "cancelled", nil, "bored", nil, nil, nil, "now()")
	e.rejectsWith(t, "executions_failure_class_check", "a failure class outside the two",
		terminalUpdate, e.execution, "failed", nil, nil, "fatal", nil, "x", "now()")
}

// The eight invalid shapes of design D11's validator, each refused by
// exactly one constraint, plus the valid shape of every status.
func TestTerminalApplicabilityIsUnrepresentable(t *testing.T) {
	e := seedExecutionBoundary(t)
	e.mustExec(t, "close admission", `UPDATE executions SET admission_closed_at=now() WHERE execution_id=$1`, e.execution)
	blocked := e.seedSettledAttempt(t, ebAttempt, "blocked")

	for _, tc := range []struct {
		because, constraint string
		args                []any
	}{
		{"completed without a disposition", "executions_completion_axis_check", []any{"completed", nil, nil, nil, nil, nil}},
		{"a disposition on a cancelled result", "executions_completion_axis_check", []any{"cancelled", "changed", "superseded", nil, nil, nil}},
		{"cancelled without a reason", "executions_cancellation_axis_check", []any{"cancelled", nil, nil, nil, nil, nil}},
		{"a reason on a completed result", "executions_cancellation_axis_check", []any{"completed", "changed", "superseded", nil, nil, nil}},
		{"failed without a class", "executions_failure_axis_check", []any{"failed", nil, nil, nil, nil, "x"}},
		{"a class on a timed-out result", "executions_failure_axis_check", []any{"timed_out", nil, nil, "non_retryable_agent", nil, nil}},
		{"blocked without its attempt", "executions_blocked_axis_check", []any{"blocked", nil, nil, nil, nil, nil}},
		{"an attempt on a completed result", "executions_blocked_axis_check", []any{"completed", "changed", nil, nil, blocked, nil}},
		{"failed with no diagnostic", "executions_error_message_check", []any{"failed", nil, nil, "retryable_infrastructure", nil, nil}},
		{"failed with a blank diagnostic", "executions_error_message_check", []any{"failed", nil, nil, "retryable_infrastructure", nil, " \t"}},
		{"a diagnostic on a completed result", "executions_error_message_check", []any{"completed", "changed", nil, nil, nil, "x"}},
	} {
		args := append([]any{e.execution}, tc.args...)
		e.rejectsWith(t, tc.constraint, tc.because+" was stored", terminalUpdate, append(args, "now()")...)
	}
	e.rejectsWith(t, "executions_terminated_at_check", "a status with no instant was stored",
		terminalUpdate, e.execution, "completed", "changed", nil, nil, nil, nil, nil)
	e.rejectsWith(t, "executions_terminated_at_check", "an instant with no status was stored",
		terminalUpdate, e.execution, nil, nil, nil, nil, nil, nil, "now()")

	// The positive controls: every status in its valid shape, each inside a
	// savepoint because a recorded result is immutable.
	for _, ok := range []struct {
		status string
		args   []any
	}{
		{"completed", []any{"completed", "already_satisfied", nil, nil, nil, nil}},
		{"blocked", []any{"blocked", nil, nil, nil, blocked, nil}},
		{"cancelled", []any{"cancelled", nil, "shutdown", nil, nil, nil}},
		{"timed_out", []any{"timed_out", nil, nil, nil, nil, nil}},
		{"failed", []any{"failed", nil, nil, "non_retryable_agent", nil, "protocol violation: x"}},
	} {
		e.mustExec(t, "savepoint", "SAVEPOINT valid_shape")
		args := append([]any{e.execution}, ok.args...)
		e.mustExec(t, "the valid "+ok.status+" shape was refused", terminalUpdate, append(args, "now()")...)
		e.mustExec(t, "rollback", "ROLLBACK TO SAVEPOINT valid_shape")
	}
}

func TestTerminalResultNeedsClosedAdmissionAndIsRecordedOnce(t *testing.T) {
	e := seedExecutionBoundary(t)
	e.rejectsWith(t, "executions_terminal_closes_admission_check",
		"a terminal result was recorded while admission was open",
		terminalUpdate, e.execution, "completed", "changed", nil, nil, nil, nil, "now()")
	e.mustExec(t, "close admission", `UPDATE executions SET admission_closed_at=now() WHERE execution_id=$1`, e.execution)
	e.mustExec(t, "record", terminalUpdate, e.execution, "completed", "changed", nil, nil, nil, nil, "now()")
	e.rejectsSaying(t, "already has a terminal result", "a second terminal result overwrote the first",
		terminalUpdate, e.execution, "timed_out", nil, nil, nil, nil, nil, "now()")
	e.rejectsSaying(t, "already has a terminal result", "the disposition of a recorded result was rewritten",
		`UPDATE executions SET completion_disposition='already_satisfied' WHERE execution_id=$1`, e.execution)
}

func TestBlockedResultNamesASettledBlockedAttemptOfItsOwnExecution(t *testing.T) {
	e := seedExecutionBoundary(t)
	e.mustExec(t, "close admission", `UPDATE executions SET admission_closed_at=now() WHERE execution_id=$1`, e.execution)
	succeeded := e.seedSettledAttempt(t, ebAttempt, "succeeded")
	e.rejectsSaying(t, "not a settled blocked attempt", "a blocked result named a succeeded attempt",
		terminalUpdate, e.execution, "blocked", nil, nil, nil, succeeded, nil, "now()")

	open := ebAttempt2
	e.seedOpenAttempt(t, open, nil)
	e.rejectsSaying(t, "not a settled blocked attempt", "a blocked result named an unsettled attempt",
		terminalUpdate, e.execution, "blocked", nil, nil, nil, open, nil, "now()")

	// Another execution's blocked attempt: refused by the composite key,
	// which the trigger never reaches.
	other := e.seedSecondExecution(t)
	e.mustExec(t, "close the other execution's admission",
		`UPDATE executions SET admission_closed_at=now() WHERE execution_id=$1`, other)
	foreign := e.seedSettledAttemptFor(t, ebAttempt3, "blocked", other, e.epic2, e.story2)
	e.rejectsWith(t, "executions_blocked_tool_call_fkey", "a blocked result named another execution's attempt",
		terminalUpdate, e.execution, "blocked", nil, nil, nil, foreign, nil, "now()")
}

// --- tool_calls: the attempt ------------------------------------------------

// attemptInsert is an execution-bound attempt with every identity column,
// as RegisterToolCall writes it; cases override what they are about.
const attemptInsert = `INSERT INTO tool_calls
    (tool_call_id, organization_id, principal_instance_id, tool_name, arguments,
     execution_id, product_id, feature_id, epic_id, story_id,
     family, request_digest, arguments_digest, target_key, mutation_key, claimed_by,
     state, outcome, finished_at, reason_code, drain_disposition,
     requirement_set, requirement_set_digest, error_message)
    VALUES ($1,$2,$3,'t','{}'::jsonb,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22)`

// attempt is one row's variable part.
type attempt struct {
	execution, epic, story                         any
	family, requestDigest, argumentsDigest, target any
	mutationKey, claimedBy                         any
	state                                          string
	outcome, finishedAt, reasonCode, disposition   any
	requirementSet, requirementDigest, errorMsg    any
}

func (e *eb) open(id string) attempt {
	return attempt{execution: e.execution, epic: e.epic, story: e.story, family: "test/noop", requestDigest: digestA,
		argumentsDigest: digestB, target: "t", claimedBy: e.execution, state: "open"}
}

func (e *eb) args(id string, a attempt) []any {
	return []any{id, e.org, e.principal, a.execution, e.product, e.feature, a.epic, a.story,
		a.family, a.requestDigest, a.argumentsDigest, a.target, a.mutationKey, a.claimedBy,
		a.state, a.outcome, a.finishedAt, a.reasonCode, a.disposition,
		a.requirementSet, a.requirementDigest, a.errorMsg}
}

func (e *eb) seedOpenAttempt(t *testing.T, id string, mutationKey *string) {
	t.Helper()
	a := e.open(id)
	if mutationKey != nil {
		a.mutationKey = *mutationKey
	}
	e.mustExec(t, "seed open attempt "+id, attemptInsert, e.args(id, a)...)
}

func (e *eb) seedSettledAttempt(t *testing.T, id, outcome string) string {
	t.Helper()
	return e.seedSettledAttemptFor(t, id, outcome, e.execution, e.epic, e.story)
}

func (e *eb) seedSettledAttemptFor(t *testing.T, id, outcome string, execution, epic, story any) string {
	t.Helper()
	a := e.open(id)
	a.execution, a.epic, a.story = execution, epic, story
	a.claimedBy = nil
	a.state, a.outcome, a.finishedAt = "settled", outcome, "now()"
	switch outcome {
	case "succeeded":
		a.disposition = "committed"
	case "blocked":
		a.disposition = "stopped_before_commit"
		a.requirementSet, a.requirementDigest = `{"policy/operator_approval":{"question":"?"}}`, digestA
	case "unknown":
		a.disposition, a.reasonCode = "unresolved", "attempt/interrupted"
	case "denied", "stale":
		a.disposition, a.reasonCode = "stopped_before_commit", "test/case"
	case "failed":
		a.disposition, a.errorMsg = "stopped_before_commit", "boom"
	}
	e.mustExec(t, "seed settled "+outcome+" attempt "+id, attemptInsert, e.args(id, a)...)
	return id
}

// seedSecondExecution accepts a dispatch for Story 2 and creates its
// execution, for the cross-execution cases.
func (e *eb) seedSecondExecution(t *testing.T) string {
	t.Helper()
	const dispatch2 = "60000000-0000-7000-8000-0000000000d2"
	const execution2 = "60000000-0000-7000-8000-0000000000e2"
	const workGroup2 = "60000000-0000-7000-8000-0000000000a2"
	e.mustExec(t, "seed work group 2", `INSERT INTO work_groups (work_group_id, organization_id, product_id, feature_id, epic_id)
	    VALUES ($1,$2,$3,$4,$5)`, workGroup2, e.org, e.product, e.feature, e.epic2)
	e.mustExec(t, "seed dispatch 2", dispatchInsert,
		dispatch2, e.org, e.product, e.feature, e.epic2, e.story2, workGroup2, "pending",
		whStory2Plan, digestA, whEpic2Plan, digestB, fixtureResolutionID(dispatch2))
	seedPromptResolution(t, e.tx, dispatchLineage{dispatch: dispatch2, org: e.org, product: e.product,
		feature: e.feature, epic: e.epic2, story: e.story2})
	e.mustExec(t, "accept dispatch 2",
		`UPDATE story_dispatches SET disposition='accepted', settled_at=now() WHERE story_dispatch_id=$1`, dispatch2)
	e.mustExec(t, "seed execution 2", executionInsert(t, e.tx, e.user),
		execution2, e.org, e.product, e.feature, e.epic2, e.story2, dispatch2)
	return execution2
}

func TestAttemptIdentityIsPresentExactlyOnBoundaryRows(t *testing.T) {
	e := seedExecutionBoundary(t)
	for _, tc := range []struct {
		because string
		mutate  func(a *attempt)
	}{
		{"a boundary attempt with no family", func(a *attempt) { a.family = nil }},
		{"a boundary attempt with no request digest", func(a *attempt) { a.requestDigest = nil }},
		{"a boundary attempt with no arguments digest", func(a *attempt) { a.argumentsDigest = nil }},
		{"a boundary attempt with no target", func(a *attempt) { a.target = nil }},
		{"a row outside any execution carrying a family", func(a *attempt) {
			a.execution, a.epic, a.story, a.claimedBy = nil, nil, nil, nil
			a.requestDigest, a.argumentsDigest, a.target = nil, nil, nil
		}},
	} {
		a := e.open(ebAttempt)
		tc.mutate(&a)
		e.rejectsWith(t, "tool_calls_boundary_identity_check", tc.because+" was accepted", attemptInsert, e.args(ebAttempt, a)...)
	}
	for _, tc := range []struct {
		because, constraint string
		mutate              func(a *attempt)
	}{
		{"a family that is not <kind>/<verb>", "tool_calls_family_check", func(a *attempt) { a.family = "Forge PR" }},
		{"a malformed request digest", "tool_calls_request_digest_check", func(a *attempt) { a.requestDigest = "abc" }},
		{"a malformed arguments digest", "tool_calls_arguments_digest_check", func(a *attempt) { a.argumentsDigest = strings.ToUpper(digestA) }},
		{"a blank target", "tool_calls_target_key_check", func(a *attempt) { a.target = " " }},
		{"a blank mutation key", "tool_calls_mutation_key_check", func(a *attempt) { a.mutationKey = "" }},
		{"a mutation key outside any execution", "tool_calls_mutation_key_check", func(a *attempt) {
			a.execution, a.epic, a.story, a.claimedBy = nil, nil, nil, nil
			a.family, a.requestDigest, a.argumentsDigest, a.target = nil, nil, nil, nil
			a.mutationKey = "forge:x"
		}},
		{"an open boundary attempt with no claim", "tool_calls_claim_check", func(a *attempt) { a.claimedBy = nil }},
		{"a settled attempt still claimed", "tool_calls_claim_check", func(a *attempt) {
			a.state, a.outcome, a.finishedAt, a.disposition = "settled", "succeeded", "now()", "committed"
		}},
	} {
		a := e.open(ebAttempt)
		tc.mutate(&a)
		e.rejectsWith(t, tc.constraint, tc.because+" was accepted", attemptInsert, e.args(ebAttempt, a)...)
	}
	// A non-mutating family registers with a NULL mutation key (D12).
	e.seedOpenAttempt(t, ebAttempt, nil)
}

func TestReasonCodeIsRequiredOptionalOrForbiddenPerOutcome(t *testing.T) {
	e := seedExecutionBoundary(t)
	settled := func(outcome string, reason any) attempt {
		a := e.open(ebAttempt)
		a.claimedBy = nil
		a.state, a.outcome, a.finishedAt, a.reasonCode = "settled", outcome, "now()", reason
		a.disposition = "stopped_before_commit"
		if outcome == "succeeded" {
			a.disposition = "committed"
		}
		if outcome == "unknown" {
			a.disposition = "unresolved"
		}
		if outcome == "failed" {
			a.errorMsg = "boom"
		}
		if outcome == "blocked" {
			a.requirementSet, a.requirementDigest = `{"g":{}}`, digestA
		}
		return a
	}
	for _, outcome := range []string{"denied", "stale", "unknown"} {
		e.rejectsWith(t, "tool_calls_reason_code_check", outcome+" settled with no reason code",
			attemptInsert, e.args(ebAttempt, settled(outcome, nil))...)
	}
	for _, outcome := range []string{"succeeded", "blocked"} {
		e.rejectsWith(t, "tool_calls_reason_code_check", outcome+" carried a reason code",
			attemptInsert, e.args(ebAttempt, settled(outcome, "gate/kind"))...)
	}
	open := e.open(ebAttempt)
	open.reasonCode = "gate/kind"
	e.rejectsWith(t, "tool_calls_reason_code_check", "an unsettled attempt carried a reason code",
		attemptInsert, e.args(ebAttempt, open)...)
	e.rejectsWith(t, "tool_calls_reason_code_format_check", "a reason code without a gate",
		attemptInsert, e.args(ebAttempt, settled("denied", "denied"))...)
	e.rejectsWith(t, "tool_calls_reason_code_format_check", "an upper-case reason code",
		attemptInsert, e.args(ebAttempt, settled("denied", "Authority/Superseded"))...)

	// failed is the one outcome where the code is optional: both forms pass.
	e.mustExec(t, "savepoint", "SAVEPOINT failed_shapes")
	e.mustExec(t, "failed without a reason code was refused", attemptInsert, e.args(ebAttempt, settled("failed", nil))...)
	e.mustExec(t, "failed with a reason code was refused", attemptInsert, e.args(ebAttempt2, settled("failed", "family/reported"))...)
	e.mustExec(t, "rollback", "ROLLBACK TO SAVEPOINT failed_shapes")
	for _, outcome := range []string{"denied", "stale", "unknown"} {
		e.mustExec(t, "savepoint", "SAVEPOINT required_shape")
		e.mustExec(t, outcome+" with a reason code was refused", attemptInsert,
			e.args(ebAttempt, settled(outcome, "gate/kind"))...)
		e.mustExec(t, "rollback", "ROLLBACK TO SAVEPOINT required_shape")
	}
}

func TestOperatorDecisionShapeAndConsumption(t *testing.T) {
	e := seedExecutionBoundary(t)
	e.seedOpenAttempt(t, ebAttempt, nil)
	e.mustExec(t, "enter the wait",
		`UPDATE tool_calls SET state='operator_waiting', requirement_set='{"g":{}}'::jsonb, requirement_set_digest=$2
		 WHERE tool_call_id=$1`, ebAttempt, digestA)

	e.rejectsWith(t, "tool_calls_operator_decision_shape_check", "a decision without a decider",
		`UPDATE tool_calls SET operator_decision='approve_once', operator_decided_at=now() WHERE tool_call_id=$1`, ebAttempt)
	e.rejectsWith(t, "tool_calls_operator_decision_check", "a decision outside the two",
		`UPDATE tool_calls SET operator_decision='approve_for_story', operator_decided_by=$2, operator_decided_at=now()
		 WHERE tool_call_id=$1`, ebAttempt, e.user)
	e.rejectsWith(t, "tool_calls_operator_decided_by_fkey", "a decider from another organization",
		`UPDATE tool_calls SET operator_decision='approve_once', operator_decided_by=$2, operator_decided_at=now()
		 WHERE tool_call_id=$1`, ebAttempt, e.seedStrangerUser(t))
	e.rejectsWith(t, "tool_calls_consumption_requires_approval_check", "consumption of no decision",
		`UPDATE tool_calls SET operator_decision_consumed_at=now(), operator_decision_consumed_by=tool_call_id
		 WHERE tool_call_id=$1`, ebAttempt)

	e.mustExec(t, "approve",
		`UPDATE tool_calls SET operator_decision='approve_once', operator_decided_by=$2, operator_decided_at=now()
		 WHERE tool_call_id=$1`, ebAttempt, e.user)
	e.rejectsWith(t, "tool_calls_consumption_shape_check", "consumed_at without consumed_by (D12: all-or-none)",
		`UPDATE tool_calls SET operator_decision_consumed_at=now() WHERE tool_call_id=$1`, ebAttempt)
	e.rejectsWith(t, "tool_calls_consumption_shape_check", "consumed_by without consumed_at",
		`UPDATE tool_calls SET operator_decision_consumed_by=tool_call_id WHERE tool_call_id=$1`, ebAttempt)
	e.rejectsWith(t, "tool_calls_consumed_by_fkey", "a consumer that is no attempt",
		`UPDATE tool_calls SET operator_decision_consumed_at=now(), operator_decision_consumed_by=$2
		 WHERE tool_call_id=$1`, ebAttempt, ebAttempt3)
	e.mustExec(t, "self-consumption was refused",
		`UPDATE tool_calls SET operator_decision_consumed_at=now(), operator_decision_consumed_by=tool_call_id,
		     state='open' WHERE tool_call_id=$1`, ebAttempt)

	// A decision on a row that never carried a requirement set is
	// unrepresentable: there was nothing to decide.
	e.seedOpenAttempt(t, ebAttempt2, nil)
	e.rejectsWith(t, "tool_calls_decision_requires_requirement_check", "a decision on a row with no requirement",
		`UPDATE tool_calls SET operator_decision='deny_once', operator_decided_by=$2, operator_decided_at=now()
		 WHERE tool_call_id=$1`, ebAttempt2, e.user)
	// Consumption of a denial is refused: only an approval is consumed.
	e.mustExec(t, "wait 2", `UPDATE tool_calls SET state='operator_waiting', requirement_set='{"g":{}}'::jsonb,
	    requirement_set_digest=$2 WHERE tool_call_id=$1`, ebAttempt2, digestA)
	e.mustExec(t, "deny 2", `UPDATE tool_calls SET operator_decision='deny_once', operator_decided_by=$2,
	    operator_decided_at=now() WHERE tool_call_id=$1`, ebAttempt2, e.user)
	e.rejectsWith(t, "tool_calls_consumption_requires_approval_check", "a denial was consumed",
		`UPDATE tool_calls SET operator_decision_consumed_at=now(), operator_decision_consumed_by=tool_call_id
		 WHERE tool_call_id=$1`, ebAttempt2)
}

func TestDrainDispositionIsPresentOnSettledBoundaryRowsAndMovesOnce(t *testing.T) {
	e := seedExecutionBoundary(t)
	settled := func(disposition any) attempt {
		a := e.open(ebAttempt)
		a.claimedBy = nil
		a.state, a.outcome, a.finishedAt, a.disposition = "settled", "succeeded", "now()", disposition
		return a
	}
	e.rejectsWith(t, "tool_calls_drain_disposition_presence_check", "a settled boundary attempt with no disposition",
		attemptInsert, e.args(ebAttempt, settled(nil))...)
	e.rejectsWith(t, "tool_calls_drain_disposition_check", "a disposition outside the four",
		attemptInsert, e.args(ebAttempt, settled("drained"))...)
	open := e.open(ebAttempt)
	open.disposition = "unresolved"
	e.rejectsWith(t, "tool_calls_drain_disposition_presence_check", "an unsettled attempt with a disposition",
		attemptInsert, e.args(ebAttempt, open)...)
	// The importer's row: settled, no execution, no disposition -- accepted,
	// and refused if it carried one.
	importer := attempt{state: "settled", outcome: "succeeded", finishedAt: "now()"}
	e.mustExec(t, "the importer's settled row was refused", attemptInsert, e.args(ebAttempt2, importer)...)
	importer.disposition = "committed"
	e.rejectsWith(t, "tool_calls_drain_disposition_presence_check", "an importer row carrying a disposition",
		attemptInsert, e.args(ebAttempt3, importer)...)

	// Monotonic: unresolved moves; anything else does not.
	unresolved := e.seedSettledAttempt(t, ebAttempt, "unknown")
	e.mustExec(t, "resolving unresolved to committed was refused",
		`UPDATE tool_calls SET drain_disposition='committed' WHERE tool_call_id=$1`, unresolved)
	e.rejectsSaying(t, "does not move", "a committed disposition was rewritten",
		`UPDATE tool_calls SET drain_disposition='stopped_before_commit' WHERE tool_call_id=$1`, unresolved)
	e.rejectsSaying(t, "does not move", "a committed disposition was moved back to unresolved",
		`UPDATE tool_calls SET drain_disposition='unresolved' WHERE tool_call_id=$1`, unresolved)
	// The trigger is not a blanket refusal of updates to a settled row.
	e.mustExec(t, "an unrelated update of a settled row was refused by the disposition trigger",
		`UPDATE tool_calls SET result='{"n":1}'::jsonb WHERE tool_call_id=$1`, unresolved)
}

// One live attempt per mutated resource, held through unresolved drainage
// (D12): the design's three index mutants each admit a second attempt this
// test refuses.
func TestOneLiveAttemptPerMutatedResource(t *testing.T) {
	e := seedExecutionBoundary(t)
	key := "forge:" + e.repo + "/maestro/story/x/maestro/epic/y"
	e.seedOpenAttempt(t, ebAttempt, &key)

	second := e.open(ebAttempt2)
	second.mutationKey = key
	e.rejectsWith(t, "tool_calls_live_mutation_key", "a second attempt registered on a resource with one open",
		attemptInsert, e.args(ebAttempt2, second)...)
	// A DIFFERENT family on the same resource: refused just the same, which
	// is why the index is on mutation_key alone.
	second.family = "other/family"
	e.rejectsWith(t, "tool_calls_live_mutation_key", "a second family registered on the same resource",
		attemptInsert, e.args(ebAttempt2, second)...)

	// Settle the first unknown/unresolved: the resource stays excluded.
	e.mustExec(t, "settle unresolved", `UPDATE tool_calls SET state='settled', outcome='unknown', finished_at=now(),
	    reason_code='attempt/interrupted', drain_disposition='unresolved', claimed_by=NULL WHERE tool_call_id=$1`, ebAttempt)
	second.family = "test/noop"
	e.rejectsWith(t, "tool_calls_live_mutation_key", "a second attempt registered while the first was unresolved",
		attemptInsert, e.args(ebAttempt2, second)...)

	// Drainage resolves: the resource is released.
	e.mustExec(t, "resolve", `UPDATE tool_calls SET drain_disposition='committed' WHERE tool_call_id=$1`, ebAttempt)
	e.mustExec(t, "a second attempt was refused after the first's drainage resolved",
		attemptInsert, e.args(ebAttempt2, second)...)

	// Non-mutating attempts never collide: two open rows with NULL keys.
	third := e.open(ebAttempt3)
	e.mustExec(t, "a non-mutating attempt was refused beside another", attemptInsert, e.args(ebAttempt3, third)...)
}

// --- principal_instances.execution_id ---------------------------------------

func TestLiveAgentPrincipalNamesItsExecution(t *testing.T) {
	e := seedExecutionBoundary(t)
	seedPromptResolutionRows(t, e.tx, e.org)
	resolved := func(id string, execution any) []any {
		return []any{id, e.org, "agent", "opus", "coder", nil,
			"resolved", "fixture", packScheme, fixtureContentDigest,
			fixtureContentID(e.org), fixtureInstallationID(e.org), 1, `{}`, execution}
	}
	insert := strings.Replace(principalInsert, "prompt_pack_metadata_snapshot)", "prompt_pack_metadata_snapshot, execution_id)", 1)
	insert = strings.Replace(insert, "$14::jsonb)", "$14::jsonb,$15)", 1)

	e.rejectsWith(t, "principal_instances_live_agent_execution_check", "a live agent with no execution",
		insert, resolved(ebPrincipal, nil)...)
	e.rejectsWith(t, "principal_instances_live_agent_execution_check", "a human principal under an execution",
		insert, ebPrincipal, e.org, "human", "human-u", nil, e.user, nil, nil, nil, nil, nil, nil, nil, nil, e.execution)
	e.rejectsWith(t, "principal_instances_live_agent_execution_check", "a foreign agent under an execution",
		insert, ebPrincipal, e.org, "agent", "opus", "coder", nil, "foreign", "default", legacyScheme,
		"sha256:"+digestA, nil, nil, nil, nil, e.execution)
	// Cross-tenant: an execution of another organization.
	other := e.seedForeignOrgExecution(t, "60000000-0000-7000-8000-0000000000c0")
	e.rejectsWith(t, "principal_instances_execution_fkey", "a live agent under another organization's execution",
		insert, resolved(ebPrincipal, other)...)

	e.mustExec(t, "a live agent under its execution was refused", insert, resolved(ebPrincipal, e.execution)...)

	// The NULL-origin agent UNDER an execution: 000023's shape check refuses
	// it first, so that sibling is dropped inside a savepoint and this check
	// is shown to refuse the row ALONE. Without the coalesce the right-hand
	// side is NULL, the equivalence is NULL, and the row PASSES -- an agent
	// of no origin bound to an execution -- 000023's own note on
	// biconditionals, applied to the one column here that can be NULL.
	e.mustExec(t, "savepoint", "SAVEPOINT without_shape_sibling")
	e.mustExec(t, "drop the shape sibling",
		`ALTER TABLE principal_instances DROP CONSTRAINT principal_instances_prompt_pack_shape_check`)
	e.rejectsWith(t, "principal_instances_live_agent_execution_check", "an agent with a NULL origin under an execution -- the row a biconditional passes",
		insert, "60000000-0000-7000-8000-000000000011", e.org, "agent", "opus", "coder", nil,
		nil, nil, nil, nil, nil, nil, nil, nil, e.execution)
	e.mustExec(t, "rollback", "ROLLBACK TO SAVEPOINT without_shape_sibling")
}

// --- repository_forge_bindings ----------------------------------------------

func TestForgeBindingsAreOnePerProvider(t *testing.T) {
	f := seed(t, disposablePlane(t))
	insert := `INSERT INTO repository_forge_bindings (repository_id, organization_id, provider, base_url, owner, repo)
	    VALUES ($1,$2,$3,$4,$5,$6)`
	f.rejectsWith(t, "repository_forge_bindings_provider_check", "a provider outside the enumeration",
		insert, f.repo, f.org, "github", "https://github.com", "o", "r")
	f.rejectsWith(t, "repository_forge_bindings_base_url_check", "a base URL that is not http(s)",
		insert, f.repo, f.org, "gitea", "gitea.local", "o", "r")
	f.rejectsWith(t, "repository_forge_bindings_owner_check", "a blank owner",
		insert, f.repo, f.org, "gitea", "http://gitea:3000", " ", "r")
	f.rejectsWith(t, "repository_forge_bindings_repository_fkey", "a binding of another organization's repository",
		insert, f.repo, "60000000-0000-7000-8000-0000000000e0", "gitea", "http://gitea:3000", "o", "r")
	f.mustExec(t, "a well-formed binding was refused", insert, f.repo, f.org, "gitea", "http://gitea:3000", "o", "r")
	f.rejectsWith(t, "repository_forge_bindings_pkey", "a second binding for the same provider",
		insert, f.repo, f.org, "gitea", "http://other:3000", "o2", "r2")
}

// --- the up migration's guard -----------------------------------------------

// The total-or-refuse guard, with its recovery walked. The design's mutant:
// remove the guard, and the migration fails on NOT NULL instead -- the wrong
// reason, with no remedy in the message -- which the assertion on the
// rendered count distinguishes from the guard's own refusal.
func TestExecutionBoundaryUpRefusesAPopulatedPlaneAndRecovers(t *testing.T) {
	ctx := context.Background()
	dsn := disposableDatabaseAt(t, 23)
	db, openErr := sql.Open("pgx", dsn)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer func() { _ = db.Close() }()
	f := seedForBackfill(t, db)
	// The chain seeds the pack rows the resolved principal below names.
	execution, _, _, _, _ := seedExecutionForPlane(t, db, f) //nolint:dogsled // only the execution is needed
	if _, err := db.Exec(principalInsert, ebPrincipal, f.org, "agent", "opus", "coder", nil,
		"resolved", "fixture", packScheme, fixtureContentDigest,
		fixtureContentID(f.org), fixtureInstallationID(f.org), 1, `{}`); err != nil {
		t.Fatalf("seed resolved principal at 23: %v", err)
	}

	upErr := migrations.Up(ctx, dsn)
	if upErr == nil || !strings.Contains(upErr.Error(), "1 execution(s) and 1 live agent principal(s) predate the execution boundary") {
		t.Fatalf("Up over a pre-000024 execution = %v, want the total-or-refuse guard with its rendered counts", upErr)
	}
	version, dirty, err := migrations.Version(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if version != 24 || !dirty {
		t.Fatalf("after the refusal the recorded version is %d dirty=%v; the remedy assumes 24 dirty", version, dirty)
	}
	if hasColumn(t, db, "executions", "capability_set") {
		t.Fatal("the refusal left the schema partially applied; it must roll back whole")
	}
	for _, stmt := range []string{
		`DELETE FROM principal_instances WHERE principal_instance_id = '` + ebPrincipal + `'`,
		`DELETE FROM executions WHERE execution_id = '` + execution + `'`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := migrations.Up(ctx, dsn); err == nil {
		t.Fatal("Up succeeded from a dirty version; the two-step instruction is over-stated")
	}
	if stepErr := migrations.Force(dsn, 23); stepErr != nil {
		t.Fatal(stepErr)
	}
	if stepErr := migrations.Up(ctx, dsn); stepErr != nil {
		t.Fatalf("the documented recovery did not work: %v", stepErr)
	}
	version, dirty, err = migrations.Version(dsn)
	if err != nil || version < 24 || dirty {
		t.Fatalf("after recovery: version %d dirty=%v err=%v, want at least 24, clean", version, dirty, err)
	}
}

// The importer's rows survive the migration untouched, and a fresh import
// after it is accepted (review round 9's two cases).
func TestExecutionBoundaryUpLeavesImporterRowsAlone(t *testing.T) {
	ctx := context.Background()
	dsn := disposableDatabaseAt(t, 23)
	db, openErr := sql.Open("pgx", dsn)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer func() { _ = db.Close() }()
	f := seedForBackfill(t, db)
	importerInsert := `INSERT INTO tool_calls
	    (tool_call_id, organization_id, principal_instance_id, tool_name, arguments, state, outcome, finished_at)
	    VALUES ($1,$2,$3,'t','{}'::jsonb,'settled','succeeded',now())`
	if _, err := db.Exec(importerInsert, ebAttempt, f.org, f.principal); err != nil {
		t.Fatalf("seed importer row at 23: %v", err)
	}
	if err := migrations.Up(ctx, dsn); err != nil {
		t.Fatalf("Up over an importer's settled row: %v", err)
	}
	var disposition, family *string
	if err := db.QueryRow(`SELECT drain_disposition, family FROM tool_calls WHERE tool_call_id=$1`, ebAttempt).
		Scan(&disposition, &family); err != nil {
		t.Fatal(err)
	}
	if disposition != nil || family != nil {
		t.Fatalf("the importer's row was rewritten: disposition=%v family=%v", disposition, family)
	}
	if _, err := db.Exec(importerInsert, ebAttempt2, f.org, f.principal); err != nil {
		t.Fatalf("a fresh importer row after 000024 was refused: %v", err)
	}
}

// Lock before scan, on 000022's forced-interleaving pattern: an execution
// held uncommitted, the migration observed BLOCKED on executions, then the
// writer commits. With the lock first the guard sees the row and refuses
// with ITS message; without it the scan sees nothing, the ALTER waits for
// the writer, and the migration dies on NOT NULL -- the wrong reason.
func TestExecutionBoundaryUpLocksBeforeItScans(t *testing.T) {
	ctx := context.Background()
	dsn := disposableDatabaseAt(t, 23)
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	f := seedForBackfill(t, db)

	// The chain committed, the execution held back.
	writer, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Rollback() }()
	execution, product, feature, epic, story := seedExecutionForPlane(t, db, f)
	if _, err := db.Exec(`DELETE FROM executions WHERE execution_id=$1`, execution); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.ExecContext(ctx, `INSERT INTO executions
	        (execution_id, organization_id, product_id, feature_id, epic_id, story_id, story_dispatch_id)
	      VALUES ($1,$2,$3,$4,$5,$6,(SELECT story_dispatch_id FROM story_dispatches LIMIT 1))`,
		execution, f.org, product, feature, epic, story); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- migrations.Up(ctx, dsn) }()
	if err := waitForBlockedLock(t, db, "executions"); err != nil {
		t.Fatalf("the migration never blocked on executions, so it did not take the lock before scanning: %v", err)
	}
	if err := writer.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		switch {
		case err == nil:
			t.Fatal("the migration applied while a concurrent writer added an execution with no configuration")
		case !strings.Contains(err.Error(), "1 execution(s) and 0 live agent principal(s) predate the execution boundary"):
			t.Fatalf("the migration failed, but not by the guard observing the concurrent row: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the migration did not finish after the writer committed")
	}
}

// --- the down migration -----------------------------------------------------

func TestExecutionBoundaryDownRefusesBoundaryOwnedState(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		seed func(t *testing.T, db *sql.DB, f planeFixture)
		want string
	}{
		"an execution": {func(t *testing.T, db *sql.DB, f planeFixture) {
			seedExecutionForPlane(t, db, f)
		}, "1 execution(s) carry a resolved configuration"},
		"a forge binding": {func(t *testing.T, db *sql.DB, f planeFixture) {
			const repo = "61000000-0000-7000-8000-000000000001"
			const product = "61000000-0000-7000-8000-000000000002"
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			for _, stmt := range []struct {
				sql  string
				args []any
			}{
				{`INSERT INTO products (product_id, organization_id, user_id, slug, display_name) VALUES ($1,$2,$3,'p','P')`, []any{product, f.org, f.user}},
				{`INSERT INTO repositories (repository_id, organization_id, primary_product_id, user_id, slug, display_name) VALUES ($1,$2,$3,$4,'r','R')`, []any{repo, f.org, product, f.user}},
				{`INSERT INTO product_repositories (product_id, repository_id, organization_id) VALUES ($1,$2,$3)`, []any{product, repo, f.org}},
				{`INSERT INTO repository_forge_bindings (repository_id, organization_id, provider, base_url, owner, repo) VALUES ($1,$2,'gitea','http://g:3000','o','r')`, []any{repo, f.org}},
			} {
				if _, err := tx.Exec(stmt.sql, stmt.args...); err != nil {
					t.Fatal(err)
				}
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
		}, "1 repository forge binding(s) exist"},
	} {
		t.Run(name, func(t *testing.T) {
			dsn := disposableDatabase(t)
			db, err := sql.Open("pgx", dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			f := seedForBackfill(t, db)
			tc.seed(t, db, f)

			err = migrations.To(ctx, dsn, 23)
			if err == nil {
				t.Fatal("000024 reversed over boundary-owned state")
			}
			if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "VERSION=24 FORCE=1") {
				t.Fatalf("refused, but not by the expected class with its remedy: %v", err)
			}
			version, dirty, err := migrations.Version(dsn)
			if err != nil || version != 23 || !dirty {
				t.Fatalf("after the refusal: version %d dirty=%v err=%v; the remedy assumes 23 dirty", version, dirty, err)
			}
			if !hasColumn(t, db, "executions", "capability_set") {
				t.Fatal("the refusal left the schema partially reversed; it must roll back whole")
			}
		})
	}
}

// With nothing boundary-owned present, the reversal is clean and leaves no
// residue -- and the importer's rows survive it.
func TestExecutionBoundaryDownIsCleanWhenNothingIsOwned(t *testing.T) {
	ctx := context.Background()
	dsn := disposableDatabase(t)
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	f := seedForBackfill(t, db)
	if _, err := db.Exec(`INSERT INTO tool_calls
	    (tool_call_id, organization_id, principal_instance_id, tool_name, arguments, state, outcome, finished_at)
	    VALUES ($1,$2,$3,'t','{}'::jsonb,'settled','succeeded',now())`, ebAttempt, f.org, f.principal); err != nil {
		t.Fatal(err)
	}
	if err := migrations.To(ctx, dsn, 23); err != nil {
		t.Fatalf("reversing 000024 over an unowned plane: %v", err)
	}
	for _, residue := range []struct{ kind, name string }{
		{"function", "execution_capability_set_canonical"},
		{"function", "executions_refuse_rewrite"},
		{"function", "tool_calls_refuse_disposition_rewrite"},
		{"function", "executions_require_blocked_attempt"},
	} {
		var present bool
		if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_proc WHERE proname = $1)`, residue.name).Scan(&present); err != nil {
			t.Fatal(err)
		}
		if present {
			t.Errorf("%s %s survived the reversal", residue.kind, residue.name)
		}
	}
	for _, column := range []struct{ table, name string }{
		{"executions", "capability_set"}, {"executions", "status"},
		{"tool_calls", "family"}, {"tool_calls", "drain_disposition"},
		{"principal_instances", "execution_id"},
	} {
		if hasColumn(t, db, column.table, column.name) {
			t.Errorf("%s.%s survived the reversal", column.table, column.name)
		}
	}
	var survivors int
	if err := db.QueryRow(`SELECT count(*) FROM tool_calls WHERE tool_call_id=$1`, ebAttempt).Scan(&survivors); err != nil {
		t.Fatal(err)
	}
	if survivors != 1 {
		t.Fatal("the importer's row did not survive the reversal")
	}
	if err := migrations.Up(ctx, dsn); err != nil {
		t.Fatalf("re-applying after a clean reversal: %v", err)
	}
}
