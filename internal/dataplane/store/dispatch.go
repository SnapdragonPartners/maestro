package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// The dispatch family (ADR 0019 as amended; Phase 3 item 3, design D10):
// governing pointers, dispatch creation that derives its basis, the named
// conditional disposition transitions, and the execution an acceptance
// creates.

// Disposition is a dispatch's lifecycle state: pending, then exactly one of
// three terminal states, each immutable.
type Disposition string

// The dispositions.
const (
	DispositionPending     Disposition = "pending"
	DispositionAccepted    Disposition = "accepted"
	DispositionFailed      Disposition = "failed"
	DispositionInvalidated Disposition = "invalidated"
)

// AuthorityState is an execution's authority (ADR 0019 as amended).
type AuthorityState string

// The authority states.
const (
	AuthorityCurrent    AuthorityState = "current"
	AuthoritySuperseded AuthorityState = "superseded"
)

// ErrDispatchRejected is the sentinel every dispatch refusal wraps.
var ErrDispatchRejected = errors.New("dispatch rejected")

// DispatchReason names why a dispatch operation was refused.
type DispatchReason string

// The reasons, one per rule.
const (
	// ReasonNotDependencyReady: an incoming edge has no satisfying completion.
	ReasonNotDependencyReady DispatchReason = "a predecessor has not completed"
	// ReasonNoGoverningArtifact: the Story or its Epic has no governing pointer.
	ReasonNoGoverningArtifact DispatchReason = "no governing artifact is pointed at"
	// ReasonGoverningWrongType: the pointed-at artifact is not the expected type.
	ReasonGoverningWrongType DispatchReason = "governing artifact is not of the expected type"
	// ReasonGoverningNotAccepted: the pointed-at artifact is not accepted.
	ReasonGoverningNotAccepted DispatchReason = "governing artifact is not accepted"
	// ReasonGoverningWrongScope: the artifact is not scoped to the work item.
	ReasonGoverningWrongScope DispatchReason = "artifact is not scoped to this work item"
	// ReasonGoverningIsAmendment: a pointer must name an original.
	ReasonGoverningIsAmendment DispatchReason = "artifact is an amendment; a pointer names an original"
	// ReasonNoWorkGroup: the Epic has no Work Group to dispatch into.
	ReasonNoWorkGroup DispatchReason = "the epic has no work group"
	// ReasonNotPending: a transition was attempted on a settled dispatch.
	ReasonNotPending DispatchReason = "dispatch is not pending; terminal dispositions are immutable"
	// ReasonFailureCodeRequired: a failure must carry a stable code.
	ReasonFailureCodeRequired DispatchReason = "a failed dispatch needs a failure code"

	// The prompt-pack refusals (item 4 design, D8), one per producer that
	// exists at item 4. A reason no code can emit would be a guess about a
	// future caller, so the coverage refusal waits for item 6's subject.

	// ReasonNoPromptSelector: neither the dispatch nor any scope supplies a
	// selector. The remedy is to seed one (provisioning does).
	ReasonNoPromptSelector DispatchReason = "no prompt pack selector applies to this story"
	// ReasonPromptSelectorUnresolved: the selector names no plane-owned pack
	// in this organization.
	ReasonPromptSelectorUnresolved DispatchReason = "the prompt pack selector names no pack in this organization"
	// ReasonPromptPackIncompatible: the installation's declared range
	// excludes the running Maestro version.
	ReasonPromptPackIncompatible DispatchReason = "the prompt pack declares a maestro version range that excludes this harness"
	// ReasonPromptPackUnusable: the harness contract re-run at dispatch
	// refused the pack.
	ReasonPromptPackUnusable DispatchReason = "the prompt pack is refused by this harness's contract"
)

// PromptRangeCheck is what the declared-range check recorded on a
// resolution: passed, or not evaluated under a development build. There is
// no third value; a refused range is a dispatch that was never written.
type PromptRangeCheck string

// The two results.
const (
	PromptRangePassed       PromptRangeCheck = "passed"
	PromptRangeNotEvaluated PromptRangeCheck = "not-evaluated"
)

// PromptResolution is the pack a dispatch resolved, persisted beside the
// dispatch basis in the same transaction (design D8). It snapshots every
// installation value that affected the decision, so a later correction to
// the installation cannot make this dispatch look decided on facts that did
// not yet exist.
type PromptResolution struct {
	ResolvedAt              time.Time
	Identity                PromptIdentity
	ResolvedName            string
	ValidatedMaestroVersion string
	RangeCheck              PromptRangeCheck
	Snapshot                PromptResolutionSnapshot
	InstallationRevision    int
	ResolutionID            uuid.UUID
	StoryDispatchID         uuid.UUID
	ContentID               uuid.UUID
	InstallationID          uuid.UUID
}

// PromptResolutionSnapshot is the metadata the decision read.
type PromptResolutionSnapshot struct {
	DisplayName       string   `json:"display_name"`
	MinMaestroVersion string   `json:"min_maestro_version"`
	MaxMaestroVersion string   `json:"max_maestro_version"`
	DeclaredRoles     []string `json:"declared_roles"`
	// ContractRerun records whether dispatch re-ran parse and the variable
	// contract (a moved or development harness) or accepted the
	// installation's validation.
	ContractRerun bool `json:"contract_rerun"`
}

// DispatchRejected is a refused dispatch operation, carrying the rule and
// the reference that failed it.
type DispatchRejected struct {
	Operation string
	Reason    DispatchReason
	Detail    string
	Subject   uuid.UUID
}

func (e *DispatchRejected) Error() string {
	message := fmt.Sprintf("%s refused for %s: %s", e.Operation, e.Subject, e.Reason)
	if e.Detail != "" {
		message += " (" + e.Detail + ")"
	}
	return message
}

// Is lets callers match the sentinel without unwrapping the detail.
func (e *DispatchRejected) Is(target error) bool { return target == ErrDispatchRejected }

// VersionRef is one governing or completion reference as item 2's snapshot
// carries it: the ORIGINAL's id, and the effective view's digest and
// amendment sequence at the moment of reference. All three halves are kept
// because each catches a move the others miss.
type VersionRef struct {
	Digest     string
	ArtifactID uuid.UUID
	Sequence   int
}

// BasisDependency is one predecessor in a dispatch's snapshot, with the
// completion that satisfied it then.
type BasisDependency struct {
	Completion         VersionRef
	PredecessorStoryID uuid.UUID
}

// StoryDispatch is a dispatch record with its disposition and its basis.
type StoryDispatch struct {
	DispatchedAt     time.Time
	FailureCode      *string
	FailureDetail    *string
	SettledAt        *time.Time
	Disposition      Disposition
	Basis            []BasisDependency
	EpicVersion      VersionRef
	StoryVersion     VersionRef
	PromptResolution PromptResolution
	StoryDispatchID  uuid.UUID
	OrganizationID   uuid.UUID
	ProductID        uuid.UUID
	FeatureID        uuid.UUID
	EpicID           uuid.UUID
	StoryID          uuid.UUID
	WorkGroupID      uuid.UUID
}

// Execution is one logical Story-scoped execution: identity and authority
// (item 2, D4), the resolved configuration (item 5, D12) and, once recorded,
// the four-axis terminal result (item 5, D11).
type Execution struct {
	CreatedAt         time.Time
	AdmissionClosedAt *time.Time
	// Terminal and TerminatedAt are present together once a terminal result
	// is recorded, and never change afterwards.
	Terminal     *TerminalResult
	TerminatedAt *time.Time

	AuthorityState AuthorityState
	// CapabilitySet is the canonical (sorted, de-duplicated) set of family
	// identities this execution may request, immutable from insert.
	CapabilitySet []string

	ExecutionID     uuid.UUID
	OrganizationID  uuid.UUID
	ProductID       uuid.UUID
	FeatureID       uuid.UUID
	EpicID          uuid.UUID
	StoryID         uuid.UUID
	StoryDispatchID uuid.UUID
	// ActingUserID is the operator who accepted the dispatch: the member the
	// vault resolves secrets for, immutable, never read from a request (D6).
	ActingUserID uuid.UUID

	// Headless declares that no responder exists for an operator
	// requirement, known at dispatch (ADR 0030 section 4; D7).
	Headless bool
}

// ExecutionConfiguration is what AcceptDispatch resolves into the execution
// row (D12). The capability set is validated against the closed family set
// through the composition's ActionContract and canonicalised by the seam --
// sorted and de-duplicated -- before it is written; an identity that is
// blank is refused before the contract sees it, because no registry could
// name it.
type ExecutionConfiguration struct {
	CapabilitySet []string
	ActingUserID  uuid.UUID
	Headless      bool
}

// ErrExecutionRejected is the sentinel every refused execution transition
// wraps.
var ErrExecutionRejected = errors.New("execution transition rejected")

// ExecutionReason names why an execution transition was refused.
type ExecutionReason string

// The reasons, one per rule.
const (
	// ReasonAlreadySuperseded: authority is not current.
	ReasonAlreadySuperseded ExecutionReason = "authority is already superseded"
	// ReasonAdmissionOpen: a terminal result needs admission closed first
	// (D7's forced-stop order; D11).
	ReasonAdmissionOpen ExecutionReason = "admission is still open; close it before recording a terminal result"
	// ReasonAlreadyTerminal: a terminal result is recorded at most once.
	ReasonAlreadyTerminal ExecutionReason = "a terminal result is already recorded"
	// ReasonActionsNotDrained: an attempt is unsettled or has unresolved
	// drainage, so no fence receipt exists (D11).
	ReasonActionsNotDrained ExecutionReason = "an admitted action is not drained"
	// ReasonBlockedAttemptInvalid: the blocked reference is not a settled,
	// blocked attempt of this execution.
	ReasonBlockedAttemptInvalid ExecutionReason = "blocked_tool_call_id is not a settled blocked attempt of this execution"
	// ReasonCapabilityBlank: a capability identity is blank.
	ReasonCapabilityBlank ExecutionReason = "a capability identity is blank"
	// ReasonCapabilityUnknown: a capability identity names no family the
	// composition's ActionContract knows (D12).
	ReasonCapabilityUnknown ExecutionReason = "a capability identity names no registered action family"
)

// ExecutionRejected is a refused execution transition.
type ExecutionRejected struct {
	Operation   string
	Reason      ExecutionReason
	Detail      string
	ExecutionID uuid.UUID
}

func (e *ExecutionRejected) Error() string {
	message := fmt.Sprintf("%s refused for execution %s: %s", e.Operation, e.ExecutionID, e.Reason)
	if e.Detail != "" {
		message += " (" + e.Detail + ")"
	}
	return message
}

// Is lets callers match the sentinel without unwrapping the detail.
func (e *ExecutionRejected) Is(target error) bool { return target == ErrExecutionRejected }

// Supersession is what superseding reports: the attempts registered and
// unsettled at that moment -- the caller's DRAIN LIST (D8) -- and the waits
// the verb itself settled stale (D10).
type Supersession struct {
	Drain  []ToolCall
	Staled []ToolCall
}

// DispatchReader is the dispatch family's read surface.
type DispatchReader interface {
	GetDispatch(ctx context.Context, organizationID, dispatchID uuid.UUID) (*StoryDispatch, error)
	ListDispatchesByDisposition(ctx context.Context, organizationID uuid.UUID, disposition Disposition) ([]StoryDispatch, error)
	GetExecutionByDispatch(ctx context.Context, organizationID, dispatchID uuid.UUID) (*Execution, error)
	GetExecution(ctx context.Context, organizationID, executionID uuid.UUID) (*Execution, error)
}

// DispatchWriter is the dispatch family's write surface.
//
// Lock order, for every implementation and every later writer of these
// rows: the Epic row first, then artifact rows in ascending id. The
// artifact transitions take an artifact lock and never an Epic lock after
// it, so no cycle exists today; a writer that took an artifact first would
// create one.
type DispatchWriter interface {
	// SetStoryGoverningArtifact points a Story at an accepted
	// work.story_record scoped to it, validated under the artifact's own
	// lock. It is a basis transition (item 2, #3): item 9 owns making it
	// linearize with running work; here it is the initial pointing.
	SetStoryGoverningArtifact(ctx context.Context, organizationID, storyID, artifactID uuid.UUID) error
	// SetEpicGoverningArtifact is the Epic's counterpart (work.epic_record).
	SetEpicGoverningArtifact(ctx context.Context, organizationID, epicID, artifactID uuid.UUID) error

	// CreateDispatch derives the basis from authoritative rows — the caller
	// supplies the Story and nothing else — under the Epic lock, validates
	// every reference under its artifact lock, and writes the dispatch row,
	// both version references and every basis row in ONE transaction.
	//
	// Refused, typed, when the Story is not dependency-ready, when it or
	// its Epic has no accepted governing artifact of the expected type, when
	// a completion is not an accepted work.story_completion, or when the
	// Epic has no Work Group.
	//
	// It also resolves the prompt pack in the same transaction (item 4
	// design, D8). An explicit selector wins; nil falls back to scoped
	// configuration under PromptPackKey; failing both, the dispatch is
	// refused.
	CreateDispatch(ctx context.Context, organizationID, storyID uuid.UUID, selector *PromptSelector) (*StoryDispatch, error)

	// AcceptDispatch flips pending → accepted and creates the execution in
	// the same transaction: an accepted dispatch has at least one execution,
	// which is the seam's half of item 2's invariant. The configuration is
	// written in the execution's INSERT (item 5, D12): the anti-update
	// trigger leaves no other initialization path.
	AcceptDispatch(ctx context.Context, organizationID, dispatchID uuid.UUID, configuration ExecutionConfiguration) (*Execution, error)
	// FailDispatch flips pending → failed with a stable code.
	FailDispatch(ctx context.Context, organizationID, dispatchID uuid.UUID, failureCode, failureDetail string) error
	// InvalidateDispatch flips pending → invalidated.
	InvalidateDispatch(ctx context.Context, organizationID, dispatchID uuid.UUID) error
}

// ExecutionWriter is the execution's boundary surface (item 5 design, D9,
// D10, D11): closure, supersession and the terminal result, each a named
// conditional transition under the row's exclusive lock.
type ExecutionWriter interface {
	// CloseAdmission takes the execution row FOR UPDATE and closes admission
	// (D9). Idempotent: a second closure is the headless path followed by
	// Terminate's own, and changes nothing.
	CloseAdmission(ctx context.Context, organizationID, executionID uuid.UUID) error

	// SupersedeExecution marks authority superseded and closes admission in
	// one statement (D10), settles every waiting attempt stale with its
	// decision preserved, and returns the open attempts as the drain list
	// (D8). Refused when authority is already superseded.
	SupersedeExecution(ctx context.Context, organizationID, executionID uuid.UUID) (Supersession, error)

	// RecordTerminalResult records the four-axis result, once (D11). It
	// requires admission already closed -- the verb refuses rather than
	// closing it as a side effect -- validates the result, and accepts the
	// receipt only when every attempt of the execution has a resolved drain
	// disposition. A blocked result must name a settled blocked attempt of
	// this execution.
	RecordTerminalResult(ctx context.Context, organizationID, executionID uuid.UUID, result TerminalResult, receipt FenceReceipt) error
}

// ExecutionTxReader is the locking read the boundary's gate 3 needs and
// nothing outside a transaction can use (item 5 design, D8, T2): the
// execution row taken FOR UPDATE, so the authority and admission it reports
// hold for the rest of the caller's transaction and a supersession waits
// behind it. Present on Tx only; a Store delegate would take and release
// the lock in a transaction of its own, which is a read that promises
// nothing (PR #383 review).
type ExecutionTxReader interface {
	// LockExecution reads the execution under its exclusive row lock, held
	// until the enclosing transaction ends. ErrNotFound when it is not in
	// the organization.
	LockExecution(ctx context.Context, organizationID, executionID uuid.UUID) (*Execution, error)
}

// ActionContract is the closed action-family set as the seam sees it
// (Phase 3 item 5 design, D3 and D12).
//
// CONSUMER-OWNED, on PromptContract's pattern and for the same reason. Which
// families exist is decided by the execution boundary's registry, which
// lives above the seam; the seam must consult it at dispatch or the
// capability set is advisory -- an identity no registry knows could be
// stored now and become live under a later registry. But the seam must not
// import the boundary, which imports the seam. So the seam declares what it
// needs, internal/boundary.Registry implements it, and the composition root
// supplies it beside Types, Keys and Prompts as plane.Caller.Actions.
//
// Implementations must be safe for concurrent use and must not retain or
// modify what they are given.
type ActionContract interface {
	// ValidateCapabilities reports whether every identity names a family
	// the boundary knows. Order and repetition are the seam's to
	// canonicalise; a nil error means every identity is registered.
	ValidateCapabilities(identities []string) error
}
