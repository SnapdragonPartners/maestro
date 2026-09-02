package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// The work family: Feature, Epic, Story and Work Group rows, and the reads a
// dispatch basis is assembled from (ADR 0018; Phase 3 item 3, design D10).
//
// Rows carry identity, lineage and title. Everything reviewable — intent,
// mode, the reviewed head — is a governing artifact (internal/dataplane/work),
// pointed at by GoverningArtifactID. One authority per fact.

// Feature is the cross-repository intent unit.
type Feature struct {
	CreatedAt      time.Time
	Title          string
	FeatureID      uuid.UUID
	OrganizationID uuid.UUID
	UserID         uuid.UUID
	ProductID      uuid.UUID
	IsWrapper      bool
}

// Epic is the repository-scoped integration and acceptance unit.
type Epic struct {
	// GoverningArtifactID names the accepted work.epic_record that governs
	// this Epic, or nil before one is pointed at. The pointer names an
	// ORIGINAL; the effective view is computed at read time.
	GoverningArtifactID *uuid.UUID
	CreatedAt           time.Time
	Title               string
	EpicID              uuid.UUID
	OrganizationID      uuid.UUID
	UserID              uuid.UUID
	ProductID           uuid.UUID
	FeatureID           uuid.UUID
	RepositoryID        uuid.UUID
}

// Story is the PR-sized unit assigned to one Coder.
type Story struct {
	GoverningArtifactID *uuid.UUID
	CreatedAt           time.Time
	Title               string
	StoryID             uuid.UUID
	OrganizationID      uuid.UUID
	UserID              uuid.UUID
	ProductID           uuid.UUID
	FeatureID           uuid.UUID
	EpicID              uuid.UUID
}

// WorkGroup is the per-Epic unit of execution, one per Epic in the MVP.
type WorkGroup struct {
	CreatedAt      time.Time
	WorkGroupID    uuid.UUID
	OrganizationID uuid.UUID
	ProductID      uuid.UUID
	FeatureID      uuid.UUID
	EpicID         uuid.UUID
}

// StoryDependency is one incoming edge of a Story: its predecessor, and the
// completion that currently satisfies it — nil while the predecessor has
// not completed, which is what "not dependency-ready" is.
type StoryDependency struct {
	SatisfyingCompletionArtifactID *uuid.UUID
	CreatedAt                      time.Time
	PredecessorStoryID             uuid.UUID
}

// CreateFeatureInput creates a Feature under a Product. FeatureID may be
// preallocated as a UUIDv7; zero has the seam allocate one.
type CreateFeatureInput struct {
	Title          string
	FeatureID      uuid.UUID
	OrganizationID uuid.UUID
	UserID         uuid.UUID
	ProductID      uuid.UUID
	IsWrapper      bool
}

// CreateEpicInput creates an Epic under a Feature. The Product is derived
// from the Feature; the repository must be a member of that Product.
type CreateEpicInput struct {
	Title          string
	EpicID         uuid.UUID
	OrganizationID uuid.UUID
	UserID         uuid.UUID
	FeatureID      uuid.UUID
	RepositoryID   uuid.UUID
}

// CreateStoryInput creates a Story under an Epic. Feature and Product are
// derived from the Epic.
type CreateStoryInput struct {
	Title          string
	StoryID        uuid.UUID
	OrganizationID uuid.UUID
	UserID         uuid.UUID
	EpicID         uuid.UUID
}

// WorkReader is the work family's read surface.
type WorkReader interface {
	GetFeature(ctx context.Context, organizationID, featureID uuid.UUID) (*Feature, error)
	GetEpic(ctx context.Context, organizationID, epicID uuid.UUID) (*Epic, error)
	GetStory(ctx context.Context, organizationID, storyID uuid.UUID) (*Story, error)
	ListStoriesByEpic(ctx context.Context, organizationID, epicID uuid.UUID) ([]Story, error)
	GetWorkGroupByEpic(ctx context.Context, organizationID, epicID uuid.UUID) (*WorkGroup, error)
	// ListIncomingStoryDependencies returns a Story's incoming edges in
	// predecessor order.
	ListIncomingStoryDependencies(ctx context.Context, organizationID, storyID uuid.UUID) ([]StoryDependency, error)
}

// WorkWriter is the work family's write surface.
//
// Governing-pointer writes and dispatch are NOT here; they are the dispatch
// family's, because they validate artifacts under the lock order design D10
// fixes.
type WorkWriter interface {
	CreateFeature(ctx context.Context, input CreateFeatureInput) (*Feature, error)
	CreateEpic(ctx context.Context, input CreateEpicInput) (*Epic, error)
	CreateStory(ctx context.Context, input CreateStoryInput) (*Story, error)
	// EnsureWorkGroup returns the Epic's Work Group, creating it if absent.
	// Idempotent by the one-per-Epic key.
	EnsureWorkGroup(ctx context.Context, organizationID, epicID uuid.UUID) (Bootstrapped[WorkGroup], error)
}
