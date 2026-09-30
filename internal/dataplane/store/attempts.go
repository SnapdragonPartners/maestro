package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// The attempt family: the mediated action's record as the execution boundary
// writes it (ADR 0030 section 8; Phase 3 item 5 design, D4-D12). A tool call
// registered under an execution IS an attempt; these verbs are the named,
// conditional transitions on that row, and the boundary composes them in
// the transactions D8's protocol describes. Nothing here decides anything:
// the gates are the boundary's, the record is the seam's.

// AttemptState is a tool call's state (migration 000022's vocabulary).
type AttemptState string

// The four states.
const (
	AttemptOpen            AttemptState = "open"
	AttemptOperatorWaiting AttemptState = "operator_waiting"
	AttemptResourceWaiting AttemptState = "resource_waiting"
	AttemptSettled         AttemptState = "settled"
)

// DrainDisposition is ADR 0032 section 6's per-attempt disposition, plus the
// absence of one (design D11). Set at settle from what the family attests;
// it moves later in one direction only, from unresolved.
type DrainDisposition string

// The four dispositions.
const (
	// DrainStoppedBeforeCommit: the effect never reached its commit point.
	DrainStoppedBeforeCommit DrainDisposition = "stopped_before_commit"
	// DrainCommitted: the effect is known to have landed.
	DrainCommitted DrainDisposition = "committed"
	// DrainInFencedDomain: the effect was confined to a domain that is now
	// fenced (item 7's value; the vocabulary exists so item 7 adds a
	// producer, not a column).
	DrainInFencedDomain DrainDisposition = "in_fenced_domain"
	// DrainUnresolved: the mutation may yet commit remotely. An execution
	// with an unresolved attempt has no fence receipt.
	DrainUnresolved DrainDisposition = "unresolved"
)

// OperatorDecision is the durable action-scoped decision (ADR 0030 section
// 4). Two values until a consumer adds the deferred *_for_story grants.
type OperatorDecision string

// The two decisions.
const (
	DecisionApproveOnce OperatorDecision = "approve_once"
	DecisionDenyOnce    OperatorDecision = "deny_once"
)

// ReasonCode is ADR 0030 section 8's "with the reason code": <gate>/<kind>,
// lower-case, from the boundary's closed enumeration. The seam constrains
// the shape; the vocabulary is the boundary's.
type ReasonCode string

// The reason codes the SEAM itself writes. The boundary's own vocabulary --
// admission denials, policy failures, staleness at gate 3 -- is declared
// beside the gates that produce it.
const (
	// ReasonOperatorDenied: the operator answered deny_once.
	ReasonOperatorDenied ReasonCode = "operator/denied"
	// ReasonStaleInterruptedWait: a wait interrupted by a restart (D5).
	ReasonStaleInterruptedWait ReasonCode = "stale/interrupted_wait"
	// ReasonStaleAuthoritySuperseded: a wait cut short by supersession (D10).
	ReasonStaleAuthoritySuperseded ReasonCode = "stale/authority_superseded"
)

// Sentinel errors, each distinguished because the boundary acts differently
// on it.
var (
	// ErrAttemptRejected is the sentinel every refused attempt transition
	// wraps.
	ErrAttemptRejected = errors.New("attempt transition rejected")
	// ErrAdmissionClosed reports a registration refused because the
	// execution's admission is closed (D9). A denial is still recordable
	// through RecordDeniedToolCall, which is not registration.
	ErrAdmissionClosed = errors.New("admission is closed for this execution")
	// ErrTargetBusy reports a registration refused by the one-live-attempt
	// rule on the mutation key (D12): another attempt on that resource is
	// unsettled or settled with unresolved drainage.
	ErrTargetBusy = errors.New("another attempt on this resource is live")
	// ErrCorrelationMismatch reports an attempt id presented with a different
	// execution, family or request digest from the row that holds it (D5).
	// The id is taken: the presentation is refused, not recorded as a new
	// attempt and not replayed -- returning the row's result for a request
	// that is not the row's logical action would be a replay of an action
	// nobody asked for. A caller defect, logged as an invariant violation by
	// the boundary.
	ErrCorrelationMismatch = errors.New("attempt id is bound to a different logical action")
)

// AttemptReason names why an attempt transition was refused.
type AttemptReason string

// The reasons, one per rule.
const (
	// ReasonAttemptWrongState: the row is not in the state the transition
	// moves from.
	ReasonAttemptWrongState AttemptReason = "attempt is not in a state this transition allows"
	// ReasonAttemptNotClaimed: the row's claim is not what the caller named.
	ReasonAttemptNotClaimed AttemptReason = "attempt is not claimed as the caller believes"
	// ReasonDecisionAlreadyRecorded: a decision exists on the row.
	ReasonDecisionAlreadyRecorded AttemptReason = "a decision is already recorded on this attempt"
	// ReasonDecisionNotInheritable: the row has no unconsumed approval to
	// inherit.
	ReasonDecisionNotInheritable AttemptReason = "attempt carries no unconsumed approval"
	// ReasonDrainNotUnresolved: only an unresolved disposition moves.
	ReasonDrainNotUnresolved AttemptReason = "drain disposition is not unresolved"
	// ReasonReasonCodeRequired: denied, stale and unknown carry one.
	ReasonReasonCodeRequired AttemptReason = "this outcome requires a reason code"
	// ReasonReasonCodeForbidden: succeeded and blocked carry none.
	ReasonReasonCodeForbidden AttemptReason = "this outcome forbids a reason code"
	// ReasonRequirementSetRequired: a blocked outcome preserves it.
	ReasonRequirementSetRequired AttemptReason = "a blocked outcome requires the requirement set"
	// ReasonDispositionRequired: an execution-bound settlement declares one.
	ReasonDispositionRequired AttemptReason = "an execution-bound settlement requires a drain disposition"
	// ReasonDispositionForbidden: a row outside any execution carries none.
	ReasonDispositionForbidden AttemptReason = "a settlement outside an execution carries no drain disposition"
	// ReasonDispositionMismatch: the disposition contradicts the outcome --
	// a denial that committed, a success that stopped before commit.
	ReasonDispositionMismatch AttemptReason = "drain disposition contradicts the outcome"
	// ReasonRequirementSetRecorded: a settlement offered a requirement set
	// for a row that already carries one. The recorded set is the question
	// that was (or would have been) answered and is never rewritten; only a
	// headless block, which never entered a wait, writes one at settlement.
	ReasonRequirementSetRecorded AttemptReason = "a recorded requirement set is not rewritten at settlement"
	// ReasonRequirementSetForbidden: a requirement set at settlement belongs
	// to a blocked outcome only.
	ReasonRequirementSetForbidden AttemptReason = "only a blocked settlement writes a requirement set"
)

// AttemptRejected is a refused attempt transition.
type AttemptRejected struct {
	Transition string
	Reason     AttemptReason
	Detail     string
	ToolCallID uuid.UUID
}

func (e *AttemptRejected) Error() string {
	message := fmt.Sprintf("%s refused for attempt %s: %s", e.Transition, e.ToolCallID, e.Reason)
	if e.Detail != "" {
		message += " (" + e.Detail + ")"
	}
	return message
}

// Is lets callers match the sentinel without unwrapping the detail.
func (e *AttemptRejected) Is(target error) bool { return target == ErrAttemptRejected }

// OperatorDecisionRecord is the durable decision as it stands on the row.
type OperatorDecisionRecord struct {
	// ConsumedAt and ConsumedBy are both present or both absent; the
	// consumer is this attempt or the re-request that inherited it.
	ConsumedAt *time.Time
	ConsumedBy *uuid.UUID
	DecidedAt  time.Time
	Decision   OperatorDecision
	DecidedBy  uuid.UUID
}

// RegisterAttemptInput opens an attempt against an execution (D5, D9). The
// id is the caller's -- the attempt identity the design binds at-most-once
// to -- and must be a UUIDv7. The lineage and the accountable user are NOT
// inputs: the seam copies them from the execution row it locks.
type RegisterAttemptInput struct {
	LLMCallID *uuid.UUID
	// CallerRef is the provider's tool-call string, a projection field for
	// correlation with the LLM turn and never a key.
	CallerRef *string
	// MutationKey names the shared resource the effect mutates, family-
	// independently; nil for a family that mutates nothing shared.
	MutationKey *string

	// Family is the Orchestrator-owned identity, <kind>/<verb>; ToolName is
	// what the record's tool_name carries, which the boundary sets to the
	// same string.
	Family   string
	ToolName string
	// RequestDigest is over the caller-supplied fields (the correlation key);
	// ArgumentsDigest over the substituted input (what the hook decided on).
	RequestDigest   string
	ArgumentsDigest string
	TargetKey       string

	// Arguments is the persisted projection, never the raw arguments.
	Arguments json.RawMessage

	ToolCallID          uuid.UUID
	OrganizationID      uuid.UUID
	ExecutionID         uuid.UUID
	PrincipalInstanceID uuid.UUID
	// ClaimedBy is the Orchestrator instance driving the attempt.
	ClaimedBy uuid.UUID
}

// Registration is what registering reports: the row as it stands, and
// whether THIS call wrote it. Registered=false is a transport retry -- the id
// was taken by THE SAME logical action, which the seam has checked -- and the
// row is what the boundary classifies (D5's table). An id taken by another
// execution, family or request digest is ErrCorrelationMismatch, never a
// Registration.
type Registration struct {
	Call       ToolCall
	Registered bool
}

// RecordDeniedAttemptInput opens and completes a denial in one insert (D4).
// Same shape as registration, plus the reason; no claim, because a settled
// row is driven by nobody.
type RecordDeniedAttemptInput struct {
	LLMCallID *uuid.UUID
	CallerRef *string

	Family          string
	ToolName        string
	RequestDigest   string
	ArgumentsDigest string
	TargetKey       string
	ReasonCode      ReasonCode

	Arguments json.RawMessage

	ToolCallID          uuid.UUID
	OrganizationID      uuid.UUID
	ExecutionID         uuid.UUID
	PrincipalInstanceID uuid.UUID
}

// SettleAttemptInput records an attempt's outcome (D11): one of the six, the
// reason code its outcome requires, permits or forbids, and the drain
// disposition the family attests. For a headless block the requirement set
// is written in the same statement; otherwise it is left as recorded.
type SettleAttemptInput struct {
	ErrorMessage *string
	FinishedAt   *time.Time
	ReasonCode   *ReasonCode
	// Disposition is required for an execution-bound attempt and forbidden
	// for a row outside any execution (the importer's).
	Disposition *DrainDisposition
	// RequirementSet and its digest, for a blocked settlement that never
	// entered a wait (the headless path). Refused on a row that already
	// carries one, and for any outcome but blocked.
	RequirementSet       json.RawMessage
	RequirementSetDigest *string

	Outcome ToolOutcome
	Result  json.RawMessage

	OrganizationID uuid.UUID
	ToolCallID     uuid.UUID
}

// Consumption reports a consumption attempt: whether THIS call consumed the
// decision. Exactly one of any number of concurrent re-presentations does;
// the rest read Consumed=false and classify the row they are handed.
type Consumption struct {
	Call     ToolCall
	Consumed bool
}

// AttemptReader is the attempt family's read surface.
type AttemptReader interface {
	// ListAttemptsForRecovery returns every unsettled execution-bound
	// attempt and every settled one whose drainage is unresolved, in start
	// order (D5's two enumerations).
	ListAttemptsForRecovery(ctx context.Context, organizationID uuid.UUID) ([]ToolCall, error)
	// ListExecutionAttempts returns every attempt of one execution.
	ListExecutionAttempts(ctx context.Context, organizationID, executionID uuid.UUID) ([]ToolCall, error)
	// FindInheritableDecision returns the stale attempt whose unconsumed
	// approval a re-request with this family, substituted digest and target
	// may inherit (D5), or ErrNotFound.
	FindInheritableDecision(ctx context.Context, organizationID, executionID uuid.UUID, family, argumentsDigest, targetKey string) (*ToolCall, error)
}

// AttemptWriter is the attempt family's transition surface. Every verb is a
// named conditional transition; a zero row count is a typed refusal, never
// a silent success.
type AttemptWriter interface {
	// RegisterAttempt is D9's registration: inside the caller's transaction
	// it takes the execution row FOR SHARE, refuses ErrAdmissionClosed if
	// admission is closed, and inserts the row open with its claim; a
	// conflict on the id returns the existing row with Registered=false. A
	// conflict on the live mutation key is ErrTargetBusy.
	RegisterAttempt(ctx context.Context, input RegisterAttemptInput) (Registration, error)
	// RecordDeniedAttempt inserts a row already settled/denied (D4). Not
	// registration: no lock, no admission check, permitted after closure.
	// An existing id returns the existing row with Registered=false.
	RecordDeniedAttempt(ctx context.Context, input RecordDeniedAttemptInput) (Registration, error)

	// StoryWaitingAttempts takes the Story row FOR UPDATE -- the lock D4's
	// guard and D7's wait entry share -- and returns the Story's attempts in
	// operator_waiting. The lock is held for the caller's transaction.
	StoryWaitingAttempts(ctx context.Context, organizationID, storyID uuid.UUID) ([]ToolCall, error)

	// EnterOperatorWait moves open → operator_waiting with the requirement
	// set (D7). EnterResourceWait and LeaveResourceWait are the resource
	// wait's transitions, whose first producer is item 7.
	EnterOperatorWait(ctx context.Context, organizationID, toolCallID uuid.UUID, requirementSet json.RawMessage, requirementSetDigest string) error
	EnterResourceWait(ctx context.Context, organizationID, toolCallID uuid.UUID) error
	LeaveResourceWait(ctx context.Context, organizationID, toolCallID uuid.UUID) error

	// RecordOperatorDecision records the operator's answer on a waiting
	// attempt (D7). An approval is durable and leaves the row waiting; a
	// denial settles it denied with operator/denied in the same statement.
	RecordOperatorDecision(ctx context.Context, organizationID, toolCallID uuid.UUID, decision OperatorDecision, decidedBy uuid.UUID) (ToolCall, error)
	// ConsumeOperatorDecision is gate 3's first act (D7): one conditional
	// update that consumes an unconsumed approval, moves the row to open,
	// records revalidation and transfers the claim to claimedBy.
	ConsumeOperatorDecision(ctx context.Context, organizationID, toolCallID, claimedBy uuid.UUID) (Consumption, error)
	// InheritOperatorDecision marks a stale attempt's unconsumed approval
	// consumed by a new attempt (D5), once -- and only by an attempt of the
	// same execution, family, substituted digest and target, whose
	// recomputed requirement-set digest equals the one approved. All of it
	// is one conditional update; a mismatch on any is
	// ReasonDecisionNotInheritable.
	InheritOperatorDecision(ctx context.Context, organizationID, staleToolCallID, consumedBy uuid.UUID, requirementSetDigest string) error

	// MarkRevalidated is D8's T2 record for the allow path.
	MarkRevalidated(ctx context.Context, organizationID, toolCallID uuid.UUID) error

	// SettleAttempt records the outcome, once only (D11), from open only: a
	// wait leaves through its own transitions, never through a bare
	// settlement. A repeat returns the recorded row with Recorded=false.
	SettleAttempt(ctx context.Context, input SettleAttemptInput) (ToolCompletion, error)
	// ResolveDrainDisposition moves an unresolved disposition to the
	// evidence a later reconciliation obtained (D11).
	ResolveDrainDisposition(ctx context.Context, organizationID, toolCallID uuid.UUID, disposition DrainDisposition) error

	// StaleInterruptedWait settles a wait claimed by another instance stale
	// with its decision preserved (D5's Recover step).
	StaleInterruptedWait(ctx context.Context, organizationID, toolCallID, foreignClaim uuid.UUID) error
	// TakeClaim transfers an open attempt's claim from one instance to
	// another, conditionally, so exactly one reconciler proceeds (D5).
	TakeClaim(ctx context.Context, organizationID, toolCallID, from, to uuid.UUID) error
}
