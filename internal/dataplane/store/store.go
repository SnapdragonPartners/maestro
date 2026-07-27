// Package store is the data plane's persistence seam (ADR 0022).
//
// It is the interface deployment modules swap at: the local Postgres module
// implements it today, and a cloud module implements it later. Everything
// above this line — the Phase 3 Orchestrator, item 9's importer — depends
// on this package and never on a driver type. Design D9 fixes that boundary
// concretely: uuid.UUID, time.Time and domain structs cross it; pgtype
// does not.
//
// The interface is deliberately narrower than the generated query set. An
// interface that grows with the schema is one nobody can implement twice,
// and a second implementation is the whole reason this exists.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/registry"
)

// Status is a Management artifact's lifecycle state (ADR 0021).
type Status string

// The lifecycle states. Audit artifacts have none: they are born final.
const (
	StatusDraft       Status = "draft"
	StatusInvalidated Status = "invalidated"
	StatusAccepted    Status = "accepted"
	StatusSuperseded  Status = "superseded"
	StatusArchived    Status = "archived"
)

// ScopeType names the entity an artifact is scoped to.
type ScopeType string

// The scope types the schema admits.
const (
	ScopeOrganization ScopeType = "organization"
	ScopeProduct      ScopeType = "product"
	ScopeFeature      ScopeType = "feature"
	ScopeEpic         ScopeType = "epic"
	ScopeStory        ScopeType = "story"
	// ScopeBenchmark has no column until item 9 adds the benchmark runs
	// table, so the schema's exactly-one-scope check refuses it today.
	ScopeBenchmark ScopeType = "benchmark"
)

// Decision is a review's verdict.
type Decision string

// The review decisions. This is the schema's full vocabulary: omitting
// changes_requested from the Go side would leave a decision the database
// accepts and no caller can name, and one a reader would decode into a
// Decision that matches neither constant.
const (
	DecisionAccepted         Decision = "accepted"
	DecisionRejected         Decision = "rejected"
	DecisionChangesRequested Decision = "changes_requested"
)

// PrincipalKind distinguishes who acts (ADR 0021).
type PrincipalKind string

// The principal kinds.
const (
	PrincipalAgent  PrincipalKind = "agent"
	PrincipalHuman  PrincipalKind = "human"
	PrincipalSystem PrincipalKind = "system"
)

// Sentinel errors. Each is distinguished because a caller acts differently
// on it, not merely to be specific.
var (
	// ErrNotFound reports that a named row does not exist, or exists in a
	// different organization — the two are deliberately indistinguishable,
	// so a caller cannot probe another tenant's identifiers.
	ErrNotFound = errors.New("not found")

	// ErrBaseMoved reports that an amendment's reviewed base no longer
	// matches the original's current effective view (ADR 0028, design D6).
	// It is separate from a rejected transition because the operator
	// response differs: this one needs re-review, not a retry.
	ErrBaseMoved = errors.New("amendment base has moved; re-review is required")

	// ErrInvariant reports that a conditional write affected no rows after
	// the seam's own classification said it should succeed. It is an
	// internal inconsistency, never a user-facing outcome.
	ErrInvariant = errors.New("internal invariant failure")
)

// RejectionReason names why a transition was refused. Classification
// happens in Go against the locked row (design D5) precisely so that a
// caller receives a reason rather than a row count.
type RejectionReason string

// The rejection reasons, one per rule in the design's matrix.
const (
	ReasonWrongStatus      RejectionReason = "artifact is not in a status this transition allows"
	ReasonIsAmendment      RejectionReason = "artifact is an amendment, which this transition forbids"
	ReasonNotAmendment     RejectionReason = "artifact is not an amendment"
	ReasonReviewNotFound   RejectionReason = "named review does not exist for this artifact"
	ReasonReviewNotAccept  RejectionReason = "named review is not an acceptance"
	ReasonDigestMismatch   RejectionReason = "review does not match the artifact's current content"
	ReasonReviewerIsAuthor RejectionReason = "reviewer is the artifact's author"
	ReasonReviewerKind     RejectionReason = "reviewer is not an agent or human principal"
	ReasonSupersedeTarget  RejectionReason = "superseding artifact does not name this artifact as its target"
	ReasonSupersedeStatus  RejectionReason = "superseding artifact is not accepted"
)

// TransitionRejected is a refused transition, carrying the specific rule
// that refused it.
type TransitionRejected struct {
	Transition string
	Reason     RejectionReason
	Detail     string
	ArtifactID uuid.UUID
}

func (e *TransitionRejected) Error() string {
	message := fmt.Sprintf("%s refused for artifact %s: %s", e.Transition, e.ArtifactID, e.Reason)
	if e.Detail != "" {
		message += " (" + e.Detail + ")"
	}
	return message
}

// Scope is an artifact's exactly-one scope (the schema's exclusive arc).
type Scope struct {
	Type ScopeType
	ID   uuid.UUID
}

// Lineage is the denormalised hierarchy an artifact belongs to. Each field
// is populated as far up as the scope implies.
type Lineage struct {
	ProductID *uuid.UUID
	FeatureID *uuid.UUID
	EpicID    *uuid.UUID
	StoryID   *uuid.UUID
}

// ManagementArtifact is a review-bearing artifact (ADR 0021).
// Field order throughout this file is chosen for struct alignment, not for
// reading order. Grouping is preserved where it survives that constraint.
type ManagementArtifact struct {
	AcceptedAt           *time.Time
	ReviewerInstanceID   *uuid.UUID
	ProducedByToolCallID *uuid.UUID
	AmendsArtifactID     *uuid.UUID
	SupersedesArtifactID *uuid.UUID
	ReplacesArtifactID   *uuid.UUID
	AmendmentSequence    *int

	Lineage   Lineage
	CreatedAt time.Time

	Type          registry.Type
	Category      registry.Category
	Status        Status
	Summary       string
	PayloadDigest string
	ReviewDigest  string

	Payload json.RawMessage
	Scope   Scope

	ArtifactID       uuid.UUID
	OrganizationID   uuid.UUID
	UserID           uuid.UUID
	AuthorInstanceID uuid.UUID

	SchemaVersion int
	IsAmendment   bool
}

// AuditArtifact is exhaust: born final, no lifecycle (ADR 0021).
type AuditArtifact struct {
	UserID               *uuid.UUID
	ProducedByToolCallID *uuid.UUID

	Lineage   Lineage
	CreatedAt time.Time

	Type          registry.Type
	Category      registry.Category
	Summary       string
	PayloadDigest string

	Payload json.RawMessage
	Scope   Scope

	ArtifactID       uuid.UUID
	OrganizationID   uuid.UUID
	AuthorInstanceID uuid.UUID

	SchemaVersion int
}

// Review is a review record (ADR 0021, ADR 0028 digest binding).
type Review struct {
	BaseDigest   *string
	BaseSequence *int

	DecidedAt time.Time

	ReviewDigest string
	Decision     Decision
	Rationale    string

	ReviewID           uuid.UUID
	OrganizationID     uuid.UUID
	ArtifactID         uuid.UUID
	ReviewerInstanceID uuid.UUID
}

// PrincipalInstance is one acting principal's lifetime (ADR 0021).
type PrincipalInstance struct {
	AgentType         *string
	PromptPackID      *string
	PromptHash        *string
	HarnessConfigHash *string
	MaestroVersion    *string
	UserID            *uuid.UUID
	StopTime          *time.Time
	StopReason        *string

	Lineage   Lineage
	StartTime time.Time

	Kind  PrincipalKind
	Model string

	PrincipalInstanceID uuid.UUID
	OrganizationID      uuid.UUID
}

// SeededInput is one artifact an instance was seeded with, recorded with
// the digest AS SEEDED so a later comparison against the artifact's current
// digest shows the seed has moved.
type SeededInput struct {
	SeededAt     time.Time
	SeededDigest string
	ArtifactID   uuid.UUID
}

// StopOutcome reports the result of stopping an instance. Stopping is
// idempotent rather than an error on repeat (design D7): two paths finalise
// one agent lifecycle about a millisecond apart, so making the loser an
// error would turn correct shutdown into spurious failure.
type StopOutcome struct {
	StopTime time.Time
	Reason   string
	// Recorded is true only for the caller whose values were written. A
	// supervisor deciding whether to requeue can still tell the difference.
	Recorded bool
}

// CreateManagementArtifactInput is everything a caller supplies to write a
// Management artifact.
//
// It carries no category, schema version or digests: the seam takes those
// from the registry and computes the digests itself (design D3). A
// caller-supplied digest is a caller-asserted one, and the point is that it
// is derived.
type CreateManagementArtifactInput struct {
	ProducedByToolCallID *uuid.UUID
	AmendsArtifactID     *uuid.UUID
	SupersedesArtifactID *uuid.UUID
	ReplacesArtifactID   *uuid.UUID

	Lineage Lineage

	Type    registry.Type
	Summary string

	Payload json.RawMessage
	Scope   Scope

	// ArtifactID may be preallocated, and must be a UUIDv7 when it is.
	// Item 6's cross-store commit order writes the object first, under a
	// key derived from this id, then the row — which is impossible if the
	// id does not exist until the INSERT. Leave it zero to have the seam
	// allocate one.
	ArtifactID uuid.UUID

	OrganizationID   uuid.UUID
	UserID           uuid.UUID
	AuthorInstanceID uuid.UUID
}

// CreateAuditArtifactInput is the Audit equivalent. UserID is optional
// because system principals emit exhaust that genuinely precedes any user.
type CreateAuditArtifactInput struct {
	UserID               *uuid.UUID
	ProducedByToolCallID *uuid.UUID

	Lineage Lineage

	Type    registry.Type
	Summary string

	Payload json.RawMessage
	Scope   Scope

	// ArtifactID may be preallocated; see CreateManagementArtifactInput.
	ArtifactID uuid.UUID

	OrganizationID   uuid.UUID
	AuthorInstanceID uuid.UUID
}

// CreateReviewInput records a decision.
//
// ReviewDigest, BaseDigest and BaseSequence are supplied by the caller and
// stored verbatim: they are what the REVIEWER SAW (design D3a). The seam
// does not recompute them, because recomputing would bind the review to
// content the reviewer never saw — manufacturing the false attestation the
// digest binding exists to prevent.
type CreateReviewInput struct {
	BaseDigest   *string
	BaseSequence *int

	ReviewDigest string
	Rationale    string
	Decision     Decision

	OrganizationID     uuid.UUID
	ArtifactID         uuid.UUID
	ReviewerInstanceID uuid.UUID
}

// CreatePrincipalInstanceInput describes an instance and its MPH signature.
type CreatePrincipalInstanceInput struct {
	AgentType         *string
	PromptPackID      *string
	PromptHash        *string
	HarnessConfigHash *string
	MaestroVersion    *string
	UserID            *uuid.UUID

	Lineage Lineage

	Kind  PrincipalKind
	Model string

	// Seeds are written in the SAME transaction as the instance. ADR 0021
	// promises "what was this agent given to start?" is always a query; an
	// instance observable without its inputs makes that false for exactly
	// as long as the gap.
	Seeds []SeedInput

	OrganizationID uuid.UUID
}

// SeedInput is one seeding-set entry.
type SeedInput struct {
	SeededDigest string
	ArtifactID   uuid.UUID
}

// AmendmentBase is what a reviewer must record to review an amendment: the
// effective view they read, its digest, and the amendment sequence it
// reflects.
//
// It exists because the schema stores base digest and sequence as a PAIR
// and design D6 compares both at acceptance. Without a way to obtain them
// together, a caller would have to assemble the view, digest it, and count
// amendments separately — three reads that can disagree, producing a review
// bound to a base that never existed at any single instant.
type AmendmentBase struct {
	Digest   string
	View     json.RawMessage
	Sequence int
}

// MPHQuery selects principal instances along one signature axis.
type MPHQuery struct {
	Model             *string
	PromptHash        *string
	HarnessConfigHash *string

	OrganizationID uuid.UUID
}

// Reader is the read surface, available inside and outside a transaction.
type Reader interface {
	GetManagementArtifact(ctx context.Context, organizationID, artifactID uuid.UUID) (*ManagementArtifact, error)
	GetAuditArtifact(ctx context.Context, organizationID, artifactID uuid.UUID) (*AuditArtifact, error)

	// EffectiveView returns the original's payload with every accepted
	// amendment applied in sequence order (ADR 0028, design D8).
	EffectiveView(ctx context.Context, organizationID, artifactID uuid.UUID) (json.RawMessage, error)

	// AmendmentBase returns the base a reviewer records when reviewing an
	// amendment of this original, read at one instant under the original's
	// lock.
	AmendmentBase(ctx context.Context, organizationID, originalID uuid.UUID) (AmendmentBase, error)

	ListManagementArtifactsByScope(ctx context.Context, organizationID uuid.UUID, scope Scope) ([]ManagementArtifact, error)
	ListManagementArtifactsByStory(ctx context.Context, organizationID, storyID uuid.UUID) ([]ManagementArtifact, error)
	ListAuditArtifactsByScope(ctx context.Context, organizationID uuid.UUID, scope Scope) ([]AuditArtifact, error)

	ListReviews(ctx context.Context, organizationID, artifactID uuid.UUID) ([]Review, error)

	GetPrincipalInstance(ctx context.Context, organizationID, instanceID uuid.UUID) (*PrincipalInstance, error)
	ListSeededInputs(ctx context.Context, organizationID, instanceID uuid.UUID) ([]SeededInput, error)
	FindPrincipalInstances(ctx context.Context, query MPHQuery) ([]PrincipalInstance, error)
}

// Writer is the write and transition surface.
type Writer interface {
	CreateManagementArtifact(ctx context.Context, input CreateManagementArtifactInput) (*ManagementArtifact, error)
	CreateAuditArtifact(ctx context.Context, input CreateAuditArtifactInput) (*AuditArtifact, error)
	CreateReview(ctx context.Context, input CreateReviewInput) (*Review, error)

	// AcceptArtifact names the specific review being acted on. Multiple
	// accepted reviews can exist for one digest, and choosing among them by
	// join would write an arbitrary reviewer into the row (design D5).
	AcceptArtifact(ctx context.Context, organizationID, artifactID, reviewID uuid.UUID) error

	// AcceptAmendment additionally checks the reviewed base against the
	// original's current effective view, returning ErrBaseMoved when they
	// differ (design D6).
	AcceptAmendment(ctx context.Context, organizationID, amendmentID, reviewID uuid.UUID) error

	InvalidateArtifact(ctx context.Context, organizationID, artifactID uuid.UUID) error

	// SupersedeArtifact accepts the superseding artifact and marks its
	// target superseded in ONE transaction. Split across two, a reader
	// between them observes two authoritative artifacts for one subject.
	SupersedeArtifact(ctx context.Context, organizationID, targetID, supersedingID, reviewID uuid.UUID) error

	ArchiveArtifact(ctx context.Context, organizationID, artifactID uuid.UUID) error

	CreatePrincipalInstance(ctx context.Context, input CreatePrincipalInstanceInput) (*PrincipalInstance, error)

	// StopPrincipalInstance is once-only and idempotent (design D7).
	StopPrincipalInstance(ctx context.Context, organizationID, instanceID uuid.UUID, reason string) (StopOutcome, error)
}

// Tx is the surface available inside a transaction.
type Tx interface {
	Reader
	Writer
}

// Store is the persistence seam.
type Store interface {
	Reader
	Writer

	// WithTx runs fn inside one transaction, committing when it returns nil
	// and rolling back otherwise. Every multi-statement operation above
	// runs inside one already; this is for callers composing several.
	//
	// Item 6 builds the cross-store commit order — object first, pin
	// recorded, row last — on this rather than inventing its own.
	WithTx(ctx context.Context, fn func(Tx) error) error

	Close()
}
