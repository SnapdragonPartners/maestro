package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"orchestrator/internal/dataplane/gen"
	"orchestrator/internal/dataplane/store"
)

// The attempt family (Phase 3 item 5 design, D4-D12): the named conditional
// transitions on an execution-bound tool call. Every write below is one
// statement guarded on the state it moves from, and a zero row count is
// classified in Go against the row -- a caller receives a reason, never a
// count. The boundary composes these inside the transactions D8's protocol
// names; nothing here opens one of its own.

// describeAbsent renders an absent optional value in a diagnostic.
const describeAbsent = "none"

// liveMutationIndex is the partial unique index that serializes attempts on
// one mutated resource (D12). Matched by NAME, as the provenance key is: the
// message is localised and reworded between Postgres versions.
const liveMutationIndex = "tool_calls_live_mutation_key"

// digestPattern (artifacts.go) is the schema's digest format, shared.
var (
	familyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*/[a-z][a-z0-9_]*$`)
	// reasonCodePattern is the schema's shape: <gate>/<kind>[/<qualifier>].
	reasonCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(/[a-z][a-z0-9_]*)+$`)
)

func rejectAttempt(transition string, toolCallID uuid.UUID, reason store.AttemptReason, detail string) error {
	return &store.AttemptRejected{Transition: transition, ToolCallID: toolCallID, Reason: reason, Detail: detail}
}

// attemptIdentity is what registration and denial share: the identity
// fields both inserts carry, validated once.
type attemptIdentity struct {
	family, toolName, requestDigest, argumentsDigest, targetKey string
	toolCallID                                                  uuid.UUID
}

func (a *attemptIdentity) validate() error {
	if a.toolCallID == uuid.Nil {
		return errors.New("attempt id is required; the caller mints it (design D5)")
	}
	if a.toolCallID.Version() != 7 {
		return fmt.Errorf("attempt id %s is a version %d uuid; the plane's identifiers are UUIDv7", a.toolCallID, a.toolCallID.Version())
	}
	if !familyPattern.MatchString(a.family) {
		return fmt.Errorf("family %q is not <kind>/<verb>", a.family)
	}
	if err := requireName(a.toolName, "tool_name"); err != nil {
		return err
	}
	if !digestPattern.MatchString(a.requestDigest) {
		return fmt.Errorf("request digest %q is not 64 lower-case hex characters", a.requestDigest)
	}
	if !digestPattern.MatchString(a.argumentsDigest) {
		return fmt.Errorf("arguments digest %q is not 64 lower-case hex characters", a.argumentsDigest)
	}
	if strings.TrimSpace(a.targetKey) == "" {
		return errors.New("target key is blank; every attempt names the target its family declared")
	}
	return nil
}

// RegisterAttempt is D9's registration.
//
// The execution row is taken FOR SHARE in the caller's transaction, so a
// closure (FOR UPDATE) that began first has already committed and is seen,
// and one that begins later waits for this registration to commit and then
// settles it. The admission check is read from the locked row -- the
// linearization is the lock's, not the check's.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (t *tx) RegisterAttempt(ctx context.Context, input store.RegisterAttemptInput) (store.Registration, error) {
	var none store.Registration
	identity := attemptIdentity{
		family: input.Family, toolName: input.ToolName, requestDigest: input.RequestDigest,
		argumentsDigest: input.ArgumentsDigest, targetKey: input.TargetKey, toolCallID: input.ToolCallID,
	}
	if err := identity.validate(); err != nil {
		return none, err
	}
	if input.ClaimedBy == uuid.Nil {
		return none, errors.New("claimed_by is required; an unsettled attempt is driven by an instance (design D5)")
	}
	if input.MutationKey != nil && strings.TrimSpace(*input.MutationKey) == "" {
		return none, errors.New("mutation key is blank; a family that mutates nothing shared passes nil")
	}
	arguments, err := requiredJSON(input.Arguments, "arguments")
	if err != nil {
		return none, err
	}

	execution, err := t.queries.LockExecutionShared(ctx, gen.LockExecutionSharedParams{
		OrganizationID: toUUID(input.OrganizationID), ExecutionID: toUUID(input.ExecutionID),
	})
	if err != nil {
		return none, notFound(err, "execution", input.ExecutionID)
	}
	// An id already registered is classified, not re-admitted (D5): a
	// transport retry after closure receives its row, and only a genuinely
	// new attempt meets the closure refusal below (PR #383 review). Read
	// under the share lock, so the row seen is the row the insert would
	// conflict with.
	existing, found, readErr := t.existingRegistration(ctx, &identity, input.OrganizationID, input.ExecutionID)
	if readErr != nil {
		return none, readErr
	}
	if found {
		return store.Registration{Call: existing, Registered: false}, nil
	}
	if execution.AdmissionClosedAt.Valid {
		return none, fmt.Errorf("%w: execution %s closed admission at %s",
			store.ErrAdmissionClosed, input.ExecutionID, fromTimestamptz(execution.AdmissionClosedAt))
	}

	inserted, err := t.queries.RegisterToolCall(ctx, gen.RegisterToolCallParams{
		ToolCallID:          toUUID(input.ToolCallID),
		OrganizationID:      execution.OrganizationID,
		UserID:              execution.ActingUserID,
		PrincipalInstanceID: toUUID(input.PrincipalInstanceID),
		LlmCallID:           toNullUUID(input.LLMCallID),
		ProductID:           execution.ProductID,
		FeatureID:           execution.FeatureID,
		EpicID:              execution.EpicID,
		StoryID:             execution.StoryID,
		ToolName:            input.ToolName,
		Arguments:           arguments,
		ExecutionID:         execution.ExecutionID,
		Family:              &input.Family,
		RequestDigest:       &input.RequestDigest,
		ArgumentsDigest:     &input.ArgumentsDigest,
		CallerRef:           input.CallerRef,
		TargetKey:           &input.TargetKey,
		MutationKey:         input.MutationKey,
		ClaimedBy:           toUUID(input.ClaimedBy),
	})
	if err != nil {
		if violatesConstraint(err, liveMutationIndex) {
			return none, fmt.Errorf("%w: mutation key %q", store.ErrTargetBusy, describeKey(input.MutationKey))
		}
		return none, fmt.Errorf("register attempt %s: %w", input.ToolCallID, err)
	}
	row, err := t.readRegistered(ctx, &identity, input.OrganizationID, input.ExecutionID, inserted == 1)
	if err != nil {
		return none, err
	}
	return store.Registration{Call: row, Registered: inserted == 1}, nil
}

// existingRegistration reports whether the id already holds a row, checked
// as a conflict would be: the row this organization can see, bound to this
// logical action. Found=false with no error means the id is free here; an id
// taken elsewhere is still a mismatch, reported when the insert conflicts.
func (t *tx) existingRegistration(ctx context.Context, identity *attemptIdentity, organizationID, executionID uuid.UUID) (store.ToolCall, bool, error) {
	if _, err := t.queries.GetToolCall(ctx, gen.GetToolCallParams{
		ToolCallID: toUUID(identity.toolCallID), OrganizationID: toUUID(organizationID),
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return store.ToolCall{}, false, nil
		}
		return store.ToolCall{}, false, fmt.Errorf("read attempt %s: %w", identity.toolCallID, err)
	}
	call, err := t.readRegistered(ctx, identity, organizationID, executionID, false)
	if err != nil {
		return store.ToolCall{}, false, err
	}
	return call, true, nil
}

// readRegistered reads the row an insert-or-conflict left, and on a conflict
// checks it is THIS logical action's (D5): same execution, family and
// request digest. A mismatch is refused, never returned as a row to classify
// -- the boundary would read a foreign row's settlement as a replay (PR #383
// review). A conflict with a row this organization cannot see is a
// mismatch too: the id is taken, and nothing more can be said about it.
func (t *tx) readRegistered(ctx context.Context, identity *attemptIdentity, organizationID, executionID uuid.UUID, inserted bool) (store.ToolCall, error) {
	row, err := t.queries.GetToolCall(ctx, gen.GetToolCallParams{
		ToolCallID: toUUID(identity.toolCallID), OrganizationID: toUUID(organizationID),
	})
	if err != nil {
		if !inserted && errors.Is(err, pgx.ErrNoRows) {
			return store.ToolCall{}, fmt.Errorf("%w: attempt %s is taken outside this organization",
				store.ErrCorrelationMismatch, identity.toolCallID)
		}
		return store.ToolCall{}, notFound(err, "attempt", identity.toolCallID)
	}
	call := toolCallFromRow(&row)
	if inserted {
		return call, nil
	}
	switch {
	case call.ExecutionID == nil || *call.ExecutionID != executionID:
		return store.ToolCall{}, fmt.Errorf("%w: attempt %s belongs to execution %s, not %s",
			store.ErrCorrelationMismatch, identity.toolCallID, describeUUID(call.ExecutionID), executionID)
	case call.Family == nil || *call.Family != identity.family:
		return store.ToolCall{}, fmt.Errorf("%w: attempt %s is family %s, not %s",
			store.ErrCorrelationMismatch, identity.toolCallID, describeString(call.Family), identity.family)
	case call.RequestDigest == nil || *call.RequestDigest != identity.requestDigest:
		return store.ToolCall{}, fmt.Errorf("%w: attempt %s was presented with a different request digest",
			store.ErrCorrelationMismatch, identity.toolCallID)
	}
	return call, nil
}

func describeString(value *string) string {
	if value == nil {
		return describeAbsent
	}
	return *value
}

// RecordDeniedAttempt inserts a row already settled/denied (D4). The
// lineage and accountable user are still the execution's, read WITHOUT a
// lock: a denial takes no part in D9's linearization, which is the point.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (t *tx) RecordDeniedAttempt(ctx context.Context, input store.RecordDeniedAttemptInput) (store.Registration, error) {
	var none store.Registration
	identity := attemptIdentity{
		family: input.Family, toolName: input.ToolName, requestDigest: input.RequestDigest,
		argumentsDigest: input.ArgumentsDigest, targetKey: input.TargetKey, toolCallID: input.ToolCallID,
	}
	if err := identity.validate(); err != nil {
		return none, err
	}
	if err := checkReasonCode(input.ReasonCode); err != nil {
		return none, err
	}
	arguments, err := requiredJSON(input.Arguments, "arguments")
	if err != nil {
		return none, err
	}
	execution, err := t.queries.GetExecution(ctx, gen.GetExecutionParams{
		OrganizationID: toUUID(input.OrganizationID), ExecutionID: toUUID(input.ExecutionID),
	})
	if err != nil {
		return none, notFound(err, "execution", input.ExecutionID)
	}
	reason := string(input.ReasonCode)
	inserted, err := t.queries.RecordDeniedToolCall(ctx, gen.RecordDeniedToolCallParams{
		ToolCallID:          toUUID(input.ToolCallID),
		OrganizationID:      execution.OrganizationID,
		UserID:              execution.ActingUserID,
		PrincipalInstanceID: toUUID(input.PrincipalInstanceID),
		LlmCallID:           toNullUUID(input.LLMCallID),
		ProductID:           execution.ProductID,
		FeatureID:           execution.FeatureID,
		EpicID:              execution.EpicID,
		StoryID:             execution.StoryID,
		ToolName:            input.ToolName,
		Arguments:           arguments,
		ExecutionID:         execution.ExecutionID,
		Family:              &input.Family,
		RequestDigest:       &input.RequestDigest,
		ArgumentsDigest:     &input.ArgumentsDigest,
		CallerRef:           input.CallerRef,
		TargetKey:           &input.TargetKey,
		ReasonCode:          &reason,
	})
	if err != nil {
		return none, fmt.Errorf("record denied attempt %s: %w", input.ToolCallID, err)
	}
	row, err := t.readRegistered(ctx, &identity, input.OrganizationID, input.ExecutionID, inserted == 1)
	if err != nil {
		return none, err
	}
	return store.Registration{Call: row, Registered: inserted == 1}, nil
}

// StoryWaitingAttempts takes the Story row FOR UPDATE and reads its waiting
// attempts under it (D4). The lock is the guard's and the wait entry's,
// held for the caller's transaction; the read is what the guard decides on.
func (t *tx) StoryWaitingAttempts(ctx context.Context, organizationID, storyID uuid.UUID) ([]store.ToolCall, error) {
	if _, err := t.queries.LockStory(ctx, gen.LockStoryParams{
		OrganizationID: toUUID(organizationID), StoryID: toUUID(storyID),
	}); err != nil {
		return nil, notFound(err, "story", storyID)
	}
	rows, err := t.queries.ListStoryWaitingAttempts(ctx, gen.ListStoryWaitingAttemptsParams{
		OrganizationID: toUUID(organizationID), StoryID: toNullUUID(&storyID),
	})
	if err != nil {
		return nil, fmt.Errorf("list waiting attempts of story %s: %w", storyID, err)
	}
	return attemptsFromRows(rows), nil
}

// EnterOperatorWait moves open → operator_waiting with the requirement set.
func (t *tx) EnterOperatorWait(ctx context.Context, organizationID, toolCallID uuid.UUID, requirementSet json.RawMessage, requirementSetDigest string) error {
	const transition = "EnterOperatorWait"
	set, err := requirementSetJSON(requirementSet, requirementSetDigest)
	if err != nil {
		return err
	}
	rows, err := t.queries.EnterOperatorWait(ctx, gen.EnterOperatorWaitParams{
		ToolCallID: toUUID(toolCallID), OrganizationID: toUUID(organizationID),
		RequirementSet: set, RequirementSetDigest: &requirementSetDigest,
	})
	return t.attemptTransition(ctx, transition, organizationID, toolCallID, rows, err, store.AttemptOpen)
}

// EnterResourceWait moves open → resource_waiting (item 7's producer).
func (t *tx) EnterResourceWait(ctx context.Context, organizationID, toolCallID uuid.UUID) error {
	rows, err := t.queries.EnterResourceWait(ctx, gen.EnterResourceWaitParams{
		ToolCallID: toUUID(toolCallID), OrganizationID: toUUID(organizationID),
	})
	return t.attemptTransition(ctx, "EnterResourceWait", organizationID, toolCallID, rows, err, store.AttemptOpen)
}

// LeaveResourceWait moves resource_waiting → open.
func (t *tx) LeaveResourceWait(ctx context.Context, organizationID, toolCallID uuid.UUID) error {
	rows, err := t.queries.LeaveResourceWait(ctx, gen.LeaveResourceWaitParams{
		ToolCallID: toUUID(toolCallID), OrganizationID: toUUID(organizationID),
	})
	return t.attemptTransition(ctx, "LeaveResourceWait", organizationID, toolCallID, rows, err, store.AttemptResourceWaiting)
}

// RecordOperatorDecision records the answer on a waiting attempt (D7).
func (t *tx) RecordOperatorDecision(ctx context.Context, organizationID, toolCallID uuid.UUID, decision store.OperatorDecision, decidedBy uuid.UUID) (store.ToolCall, error) {
	const transition = "RecordOperatorDecision"
	var none store.ToolCall
	if decidedBy == uuid.Nil {
		return none, errors.New("decided_by is required; a decision is a member's act")
	}
	var rows int64
	var err error
	switch decision {
	case store.DecisionApproveOnce:
		rows, err = t.queries.RecordOperatorApproval(ctx, gen.RecordOperatorApprovalParams{
			ToolCallID: toUUID(toolCallID), OrganizationID: toUUID(organizationID), DecidedBy: toNullUUID(&decidedBy),
		})
	case store.DecisionDenyOnce:
		rows, err = t.queries.RecordOperatorDenial(ctx, gen.RecordOperatorDenialParams{
			ToolCallID: toUUID(toolCallID), OrganizationID: toUUID(organizationID), DecidedBy: toNullUUID(&decidedBy),
		})
	default:
		return none, fmt.Errorf("%q is not an operator decision", decision)
	}
	if err != nil {
		return none, fmt.Errorf("%s %s: %w", transition, toolCallID, err)
	}
	if rows != 1 {
		current, readErr := t.GetToolCall(ctx, organizationID, toolCallID)
		if readErr != nil {
			return none, readErr
		}
		if current.OperatorDecision != nil {
			return none, rejectAttempt(transition, toolCallID, store.ReasonDecisionAlreadyRecorded,
				string(current.OperatorDecision.Decision))
		}
		return none, rejectAttempt(transition, toolCallID, store.ReasonAttemptWrongState,
			fmt.Sprintf("state is %s, want %s", current.State, store.AttemptOperatorWaiting))
	}
	current, err := t.GetToolCall(ctx, organizationID, toolCallID)
	if err != nil {
		return none, err
	}
	return *current, nil
}

// ConsumeOperatorDecision is gate 3's first act (D7). Consumed=false is not
// an error: it is what every re-presentation but one reads, and the row it
// carries is what that caller classifies.
func (t *tx) ConsumeOperatorDecision(ctx context.Context, organizationID, toolCallID, claimedBy uuid.UUID) (store.Consumption, error) {
	var none store.Consumption
	if claimedBy == uuid.Nil {
		return none, errors.New("claimed_by is required; consumption transfers the claim to the consuming instance (design D5)")
	}
	rows, err := t.queries.ConsumeOperatorDecision(ctx, gen.ConsumeOperatorDecisionParams{
		ToolCallID: toUUID(toolCallID), OrganizationID: toUUID(organizationID), ClaimedBy: toUUID(claimedBy),
	})
	if err != nil {
		return none, fmt.Errorf("consume decision on attempt %s: %w", toolCallID, err)
	}
	current, err := t.GetToolCall(ctx, organizationID, toolCallID)
	if err != nil {
		return none, err
	}
	return store.Consumption{Call: *current, Consumed: rows == 1}, nil
}

// InheritOperatorDecision marks a stale attempt's approval consumed by a
// new attempt (D5), once.
func (t *tx) InheritOperatorDecision(ctx context.Context, organizationID, staleToolCallID, consumedBy uuid.UUID, requirementSetDigest string) error {
	const transition = "InheritOperatorDecision"
	if consumedBy == uuid.Nil || consumedBy == staleToolCallID {
		return errors.New("consumed_by must name the inheriting attempt, which is not the stale one")
	}
	if !digestPattern.MatchString(requirementSetDigest) {
		return fmt.Errorf("requirement set digest %q is not 64 lower-case hex characters", requirementSetDigest)
	}
	rows, err := t.queries.InheritOperatorDecision(ctx, gen.InheritOperatorDecisionParams{
		ToolCallID: toUUID(staleToolCallID), OrganizationID: toUUID(organizationID), ConsumedBy: toNullUUID(&consumedBy),
		RequirementSetDigest: &requirementSetDigest,
	})
	if err != nil {
		return fmt.Errorf("%s %s: %w", transition, staleToolCallID, err)
	}
	if rows == 1 {
		return nil
	}
	if _, readErr := t.GetToolCall(ctx, organizationID, staleToolCallID); readErr != nil {
		return readErr
	}
	return rejectAttempt(transition, staleToolCallID, store.ReasonDecisionNotInheritable, "")
}

// MarkRevalidated is D8's T2 record for the allow path.
func (t *tx) MarkRevalidated(ctx context.Context, organizationID, toolCallID uuid.UUID) error {
	rows, err := t.queries.MarkRevalidated(ctx, gen.MarkRevalidatedParams{
		ToolCallID: toUUID(toolCallID), OrganizationID: toUUID(organizationID),
	})
	return t.attemptTransition(ctx, "MarkRevalidated", organizationID, toolCallID, rows, err, store.AttemptOpen)
}

// SettleAttempt records the outcome, once (D11).
//
// Lock and classify before validating, as CompleteToolCall does: a repeat
// is a repeat whatever it proposes. Then the reason-code rule, the
// requirement-set rule and the disposition rule -- the checks the old
// refusal at this seam was standing in for.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (t *tx) SettleAttempt(ctx context.Context, input store.SettleAttemptInput) (store.ToolCompletion, error) {
	const transition = "SettleAttempt"
	locked, err := t.queries.LockToolCall(ctx, gen.LockToolCallParams{
		ToolCallID: toUUID(input.ToolCallID), OrganizationID: toUUID(input.OrganizationID),
	})
	if err != nil {
		return store.ToolCompletion{}, notFound(err, "tool call", input.ToolCallID)
	}
	if locked.ToolCall.FinishedAt.Valid {
		return store.ToolCompletion{Call: toolCallFromRow(&locked.ToolCall), Recorded: false}, nil
	}
	if store.AttemptState(locked.ToolCall.State) != store.AttemptOpen {
		// A wait leaves through its own transitions; settling it directly
		// would bypass the decision it is waiting for (PR #383 review).
		return store.ToolCompletion{}, rejectAttempt(transition, input.ToolCallID, store.ReasonAttemptWrongState,
			fmt.Sprintf("state is %s, want %s", locked.ToolCall.State, store.AttemptOpen))
	}
	if settleErr := checkSettlement(&input, &locked.ToolCall); settleErr != nil {
		return store.ToolCompletion{}, settleErr
	}
	finishedAt := completionInstant(input.FinishedAt, locked.LockedAt)
	if intervalErr := checkCompletionInterval(finishedAt, fromTimestamptz(locked.ToolCall.StartedAt), input.ToolCallID); intervalErr != nil {
		return store.ToolCompletion{}, intervalErr
	}
	result, err := optionalJSON(input.Result, "result")
	if err != nil {
		return store.ToolCompletion{}, err
	}
	var requirementSet []byte
	if len(input.RequirementSet) != 0 {
		digest := ""
		if input.RequirementSetDigest != nil {
			digest = *input.RequirementSetDigest
		}
		if requirementSet, err = requirementSetJSON(input.RequirementSet, digest); err != nil {
			return store.ToolCompletion{}, err
		}
	}

	outcome := string(input.Outcome)
	var reason, disposition *string
	if input.ReasonCode != nil {
		value := string(*input.ReasonCode)
		reason = &value
	}
	if input.Disposition != nil {
		value := string(*input.Disposition)
		disposition = &value
	}
	affected, err := t.queries.SettleToolCall(ctx, gen.SettleToolCallParams{
		FinishedAt:           toTimestamptz(finishedAt),
		Outcome:              &outcome,
		Result:               result,
		ErrorMessage:         input.ErrorMessage,
		ReasonCode:           reason,
		DrainDisposition:     disposition,
		RequirementSet:       requirementSet,
		RequirementSetDigest: input.RequirementSetDigest,
		ToolCallID:           toUUID(input.ToolCallID),
		OrganizationID:       toUUID(input.OrganizationID),
	})
	if err != nil {
		return store.ToolCompletion{}, fmt.Errorf("%s %s: %w", transition, input.ToolCallID, err)
	}
	if affected != 1 {
		return store.ToolCompletion{}, fmt.Errorf(
			"%w: settling attempt %s affected no rows while holding its lock with a null finished_at",
			store.ErrInvariant, input.ToolCallID)
	}
	settled, err := t.queries.GetToolCall(ctx, gen.GetToolCallParams{
		ToolCallID: toUUID(input.ToolCallID), OrganizationID: toUUID(input.OrganizationID),
	})
	if err != nil {
		return store.ToolCompletion{}, notFound(err, "tool call", input.ToolCallID)
	}
	return store.ToolCompletion{Call: toolCallFromRow(&settled), Recorded: true}, nil
}

// checkSettlement is the reason-code, requirement-set and disposition rules
// (D11, D12), mirrored from the schema so the caller reads which field it
// got wrong rather than a constraint name. The constraints remain as the
// backstop for writers that bypass the seam.
func checkSettlement(input *store.SettleAttemptInput, row *gen.ToolCall) error {
	const transition = "SettleAttempt"
	if err := checkOutcomeAndReason(input); err != nil {
		return err
	}
	if err := checkSettlementRequirement(input, row); err != nil {
		return err
	}
	bound := row.ExecutionID.Valid
	switch {
	case bound && input.Disposition == nil:
		return rejectAttempt(transition, input.ToolCallID, store.ReasonDispositionRequired, "")
	case !bound && input.Disposition != nil:
		return rejectAttempt(transition, input.ToolCallID, store.ReasonDispositionForbidden, "")
	case bound:
		return checkDispositionAgainstOutcome(input.ToolCallID, input.Outcome, *input.Disposition)
	}
	return nil
}

// checkSettlementRequirement is the requirement-set rule at settlement (D7,
// D12): a blocked outcome preserves one; only the headless block -- which
// never entered a wait -- writes one here, and only onto a row that never
// recorded one, so the question that was approved is never rewritten (PR
// #383 review).
func checkSettlementRequirement(input *store.SettleAttemptInput, row *gen.ToolCall) error {
	const transition = "SettleAttempt"
	offered := len(input.RequirementSet) != 0 || input.RequirementSetDigest != nil
	if input.Outcome == store.ToolOutcomeBlocked && len(row.RequirementSet) == 0 && !offered {
		return rejectAttempt(transition, input.ToolCallID, store.ReasonRequirementSetRequired, "")
	}
	if !offered {
		return nil
	}
	switch {
	case len(row.RequirementSet) != 0:
		return rejectAttempt(transition, input.ToolCallID, store.ReasonRequirementSetRecorded, "")
	case input.Outcome != store.ToolOutcomeBlocked:
		return rejectAttempt(transition, input.ToolCallID, store.ReasonRequirementSetForbidden, string(input.Outcome))
	case len(input.RequirementSet) == 0 || input.RequirementSetDigest == nil:
		return errors.New("a requirement set written at settlement needs both the set and its digest")
	}
	return nil
}

// checkOutcomeAndReason is the vocabulary, the coherence rule and the
// reason-code rule (D12): required for denied, stale and unknown; optional
// for failed; forbidden for succeeded and blocked.
func checkOutcomeAndReason(input *store.SettleAttemptInput) error {
	const transition = "SettleAttempt"
	switch input.Outcome {
	case store.ToolOutcomeSucceeded, store.ToolOutcomeFailed:
		// The schema's coherence constraint binds these two only; the four
		// boundary outcomes may carry a message or not (000022, step 6).
		if err := checkOutcomeCoherence(input.Outcome == store.ToolOutcomeSucceeded, input.ErrorMessage); err != nil {
			return err
		}
	case store.ToolOutcomeDenied, store.ToolOutcomeBlocked, store.ToolOutcomeStale, store.ToolOutcomeUnknown:
	default:
		return fmt.Errorf("%q is not a tool-call outcome", input.Outcome)
	}
	switch input.Outcome {
	case store.ToolOutcomeDenied, store.ToolOutcomeStale, store.ToolOutcomeUnknown:
		if input.ReasonCode == nil {
			return rejectAttempt(transition, input.ToolCallID, store.ReasonReasonCodeRequired, string(input.Outcome))
		}
	case store.ToolOutcomeSucceeded, store.ToolOutcomeBlocked:
		if input.ReasonCode != nil {
			return rejectAttempt(transition, input.ToolCallID, store.ReasonReasonCodeForbidden, string(input.Outcome))
		}
	case store.ToolOutcomeFailed:
		// Optional.
	}
	if input.ReasonCode != nil {
		return checkReasonCode(*input.ReasonCode)
	}
	return nil
}

// checkDispositionAgainstOutcome refuses the pairings the design rules out
// (D11): an outcome that never reached the effect cannot have committed,
// and a success is a commit by definition.
func checkDispositionAgainstOutcome(toolCallID uuid.UUID, outcome store.ToolOutcome, disposition store.DrainDisposition) error {
	switch disposition {
	case store.DrainStoppedBeforeCommit, store.DrainCommitted, store.DrainInFencedDomain, store.DrainUnresolved:
	default:
		return fmt.Errorf("%q is not a drain disposition", disposition)
	}
	var permitted bool
	switch outcome {
	case store.ToolOutcomeDenied, store.ToolOutcomeStale, store.ToolOutcomeBlocked:
		permitted = disposition == store.DrainStoppedBeforeCommit
	case store.ToolOutcomeSucceeded:
		permitted = disposition == store.DrainCommitted
	case store.ToolOutcomeUnknown:
		permitted = disposition == store.DrainUnresolved
	case store.ToolOutcomeFailed:
		permitted = disposition != store.DrainInFencedDomain
	}
	if !permitted {
		return rejectAttempt("SettleAttempt", toolCallID, store.ReasonDispositionMismatch,
			fmt.Sprintf("%s with %s", outcome, disposition))
	}
	return nil
}

// ResolveDrainDisposition moves unresolved → the evidence (D11).
func (t *tx) ResolveDrainDisposition(ctx context.Context, organizationID, toolCallID uuid.UUID, disposition store.DrainDisposition) error {
	const transition = "ResolveDrainDisposition"
	switch disposition {
	case store.DrainCommitted, store.DrainStoppedBeforeCommit, store.DrainInFencedDomain:
	case store.DrainUnresolved:
		return rejectAttempt(transition, toolCallID, store.ReasonDrainNotUnresolved, "resolving to unresolved is not a resolution")
	default:
		return fmt.Errorf("%q is not a drain disposition", disposition)
	}
	value := string(disposition)
	rows, err := t.queries.ResolveDrainDisposition(ctx, gen.ResolveDrainDispositionParams{
		ToolCallID: toUUID(toolCallID), OrganizationID: toUUID(organizationID), DrainDisposition: &value,
	})
	if err != nil {
		return fmt.Errorf("%s %s: %w", transition, toolCallID, err)
	}
	if rows == 1 {
		return nil
	}
	current, readErr := t.GetToolCall(ctx, organizationID, toolCallID)
	if readErr != nil {
		return readErr
	}
	detail := "unsettled"
	if current.DrainDisposition != nil {
		detail = string(*current.DrainDisposition)
	}
	return rejectAttempt(transition, toolCallID, store.ReasonDrainNotUnresolved, detail)
}

// StaleInterruptedWait settles a foreign-claimed wait stale (D5).
func (t *tx) StaleInterruptedWait(ctx context.Context, organizationID, toolCallID, foreignClaim uuid.UUID) error {
	const transition = "StaleInterruptedWait"
	rows, err := t.queries.StaleInterruptedWait(ctx, gen.StaleInterruptedWaitParams{
		ToolCallID: toUUID(toolCallID), OrganizationID: toUUID(organizationID), ClaimedBy: toUUID(foreignClaim),
	})
	if err != nil {
		return fmt.Errorf("%s %s: %w", transition, toolCallID, err)
	}
	if rows == 1 {
		return nil
	}
	current, readErr := t.GetToolCall(ctx, organizationID, toolCallID)
	if readErr != nil {
		return readErr
	}
	if current.State != store.AttemptOperatorWaiting && current.State != store.AttemptResourceWaiting {
		return rejectAttempt(transition, toolCallID, store.ReasonAttemptWrongState, "state is "+string(current.State))
	}
	return rejectAttempt(transition, toolCallID, store.ReasonAttemptNotClaimed, "claimed by "+describeUUID(current.ClaimedBy))
}

// TakeClaim transfers an open attempt's claim, conditionally (D5).
func (t *tx) TakeClaim(ctx context.Context, organizationID, toolCallID, from, to uuid.UUID) error {
	const transition = "TakeClaim"
	if to == uuid.Nil {
		return errors.New("the taking instance is required")
	}
	rows, err := t.queries.TakeClaim(ctx, gen.TakeClaimParams{
		ToolCallID: toUUID(toolCallID), OrganizationID: toUUID(organizationID),
		ClaimedBy: toUUID(to), PreviousClaim: toUUID(from),
	})
	if err != nil {
		return fmt.Errorf("%s %s: %w", transition, toolCallID, err)
	}
	if rows == 1 {
		return nil
	}
	current, readErr := t.GetToolCall(ctx, organizationID, toolCallID)
	if readErr != nil {
		return readErr
	}
	if current.State != store.AttemptOpen {
		return rejectAttempt(transition, toolCallID, store.ReasonAttemptWrongState, "state is "+string(current.State))
	}
	return rejectAttempt(transition, toolCallID, store.ReasonAttemptNotClaimed, "claimed by "+describeUUID(current.ClaimedBy))
}

// ListAttemptsForRecovery is D5's two enumerations in one read.
func (t *tx) ListAttemptsForRecovery(ctx context.Context, organizationID uuid.UUID) ([]store.ToolCall, error) {
	rows, err := t.queries.ListAttemptsForRecovery(ctx, toUUID(organizationID))
	if err != nil {
		return nil, fmt.Errorf("list attempts for recovery: %w", err)
	}
	return attemptsFromRows(rows), nil
}

// ListExecutionAttempts returns every attempt of one execution.
func (t *tx) ListExecutionAttempts(ctx context.Context, organizationID, executionID uuid.UUID) ([]store.ToolCall, error) {
	rows, err := t.queries.ListExecutionAttempts(ctx, gen.ListExecutionAttemptsParams{
		OrganizationID: toUUID(organizationID), ExecutionID: toNullUUID(&executionID),
	})
	if err != nil {
		return nil, fmt.Errorf("list attempts of execution %s: %w", executionID, err)
	}
	return attemptsFromRows(rows), nil
}

// FindInheritableDecision returns the stale attempt a re-request may
// inherit from (D5).
func (t *tx) FindInheritableDecision(ctx context.Context, organizationID, executionID uuid.UUID, family, argumentsDigest, targetKey string) (*store.ToolCall, error) {
	row, err := t.queries.FindInheritableDecision(ctx, gen.FindInheritableDecisionParams{
		OrganizationID: toUUID(organizationID), ExecutionID: toNullUUID(&executionID),
		Family: &family, ArgumentsDigest: &argumentsDigest, TargetKey: &targetKey,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: no inheritable decision for %s in execution %s", store.ErrNotFound, family, executionID)
		}
		return nil, fmt.Errorf("find inheritable decision: %w", err)
	}
	call := toolCallFromRow(&row)
	return &call, nil
}

// attemptTransition classifies a one-row conditional update: one row is
// success, zero is a typed refusal naming the state the row is in.
func (t *tx) attemptTransition(ctx context.Context, transition string, organizationID, toolCallID uuid.UUID, rows int64, err error, from store.AttemptState) error {
	if err != nil {
		return fmt.Errorf("%s %s: %w", transition, toolCallID, err)
	}
	if rows == 1 {
		return nil
	}
	current, readErr := t.GetToolCall(ctx, organizationID, toolCallID)
	if readErr != nil {
		return readErr
	}
	return rejectAttempt(transition, toolCallID, store.ReasonAttemptWrongState,
		fmt.Sprintf("state is %s, want %s", current.State, from))
}

// requirementSetJSON validates a requirement set the way the schema does: a
// non-empty object with a 64-hex digest beside it.
func requirementSetJSON(set json.RawMessage, digest string) ([]byte, error) {
	if !json.Valid(set) {
		return nil, errors.New("requirement set is not valid JSON")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(set, &object); err != nil {
		return nil, errors.New("requirement set is not a JSON object keyed by requirement identity")
	}
	if len(object) == 0 {
		return nil, errors.New("requirement set is empty; a wait with nothing to wait on is not a wait")
	}
	if !digestPattern.MatchString(digest) {
		return nil, fmt.Errorf("requirement set digest %q is not 64 lower-case hex characters", digest)
	}
	return set, nil
}

func checkReasonCode(code store.ReasonCode) error {
	if !reasonCodePattern.MatchString(string(code)) {
		return fmt.Errorf("reason code %q is not <gate>/<kind>[/<qualifier>] in lower case", code)
	}
	return nil
}

// violatesConstraint reports whether err is the named constraint's refusal.
func violatesConstraint(err error, name string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.ConstraintName == name
}

func describeKey(key *string) string {
	if key == nil {
		return describeAbsent
	}
	return *key
}

// attemptsFromRows is toolCallsFromRows (reads.go) without the error the
// paging helpers' shape requires.
func attemptsFromRows(rows []gen.ToolCall) []store.ToolCall {
	calls, _ := toolCallsFromRows(rows)
	return calls
}
