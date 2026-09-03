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
)

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
	SettledAt     *time.Time
	FailureCode   *string
	FailureDetail *string
	Basis         []BasisDependency
	DispatchedAt  time.Time
	Disposition   Disposition
	StoryVersion  VersionRef
	EpicVersion   VersionRef

	StoryDispatchID uuid.UUID
	OrganizationID  uuid.UUID
	ProductID       uuid.UUID
	FeatureID       uuid.UUID
	EpicID          uuid.UUID
	StoryID         uuid.UUID
	WorkGroupID     uuid.UUID
}

// Execution is one logical Story-scoped execution, carrying identity and
// authority only (item 2, D4). Configuration and bindings are items 5/6's.
type Execution struct {
	AdmissionClosedAt *time.Time
	CreatedAt         time.Time
	AuthorityState    AuthorityState
	ExecutionID       uuid.UUID
	OrganizationID    uuid.UUID
	ProductID         uuid.UUID
	FeatureID         uuid.UUID
	EpicID            uuid.UUID
	StoryID           uuid.UUID
	StoryDispatchID   uuid.UUID
}

// DispatchReader is the dispatch family's read surface.
type DispatchReader interface {
	GetDispatch(ctx context.Context, organizationID, dispatchID uuid.UUID) (*StoryDispatch, error)
	ListDispatchesByDisposition(ctx context.Context, organizationID uuid.UUID, disposition Disposition) ([]StoryDispatch, error)
	GetExecutionByDispatch(ctx context.Context, organizationID, dispatchID uuid.UUID) (*Execution, error)
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
	CreateDispatch(ctx context.Context, organizationID, storyID uuid.UUID) (*StoryDispatch, error)

	// AcceptDispatch flips pending → accepted and creates the execution in
	// the same transaction: an accepted dispatch has at least one execution,
	// which is the seam's half of item 2's invariant.
	AcceptDispatch(ctx context.Context, organizationID, dispatchID uuid.UUID) (*Execution, error)
	// FailDispatch flips pending → failed with a stable code.
	FailDispatch(ctx context.Context, organizationID, dispatchID uuid.UUID, failureCode, failureDetail string) error
	// InvalidateDispatch flips pending → invalidated.
	InvalidateDispatch(ctx context.Context, organizationID, dispatchID uuid.UUID) error
}
