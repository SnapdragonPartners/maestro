//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/store"
)

// hierarchy provisions product -> repository -> feature -> epic -> story
// through the seam and returns the rows, for tests above this file too.
type hierarchy struct {
	product    uuid.UUID
	repository uuid.UUID
	feature    *store.Feature
	epic       *store.Epic
	story      *store.Story
}

func provisionHierarchy(t *testing.T, f *fixture) hierarchy {
	t.Helper()
	ctx := context.Background()
	product := provisionProduct(t, f, "core")
	repo, err := f.store.ProvisionRepository(ctx, store.ProvisionRepositoryInput{
		Slug: "api", DisplayName: "API", OrganizationID: f.organizationID, PrimaryProductID: product, UserID: f.userID,
	})
	if err != nil {
		t.Fatalf("repository: %v", err)
	}
	feature, err := f.store.CreateFeature(ctx, store.CreateFeatureInput{
		Title: "Flags", OrganizationID: f.organizationID, UserID: f.userID, ProductID: product,
	})
	if err != nil {
		t.Fatalf("feature: %v", err)
	}
	epic, err := f.store.CreateEpic(ctx, store.CreateEpicInput{
		Title: "Instance flag", OrganizationID: f.organizationID, UserID: f.userID,
		FeatureID: feature.FeatureID, RepositoryID: repo.Record.RepositoryID,
	})
	if err != nil {
		t.Fatalf("epic: %v", err)
	}
	story, err := f.store.CreateStory(ctx, store.CreateStoryInput{
		Title: "Add --instance-name", OrganizationID: f.organizationID, UserID: f.userID, EpicID: epic.EpicID,
	})
	if err != nil {
		t.Fatalf("story: %v", err)
	}
	return hierarchy{product: product, repository: repo.Record.RepositoryID, feature: feature, epic: epic, story: story}
}

// TestHierarchyLineageIsDerivedFromTheParent: the caller names only the
// immediate parent, and every ancestor column is the parent's.
//
// THE MUTANT: have CreateStory take the caller's product instead of the
// Epic's. Nothing in the input carries one, so the mutant must invent it —
// and the assertions below on the read-back row catch a zero or foreign
// value either way.
func TestHierarchyLineageIsDerivedFromTheParent(t *testing.T) {
	f := newFixture(t)
	h := provisionHierarchy(t, f)

	if h.epic.ProductID != h.product || h.epic.FeatureID != h.feature.FeatureID {
		t.Fatalf("epic lineage %+v is not its feature's (%s/%s)", h.epic, h.product, h.feature.FeatureID)
	}
	if h.story.ProductID != h.product || h.story.FeatureID != h.feature.FeatureID || h.story.EpicID != h.epic.EpicID {
		t.Fatalf("story lineage %+v is not its epic's", h.story)
	}
	if h.story.GoverningArtifactID != nil || h.epic.GoverningArtifactID != nil {
		t.Fatal("a fresh row points at a governing artifact; nothing has been accepted")
	}
	stories, err := f.store.ListStoriesByEpic(context.Background(), f.organizationID, h.epic.EpicID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stories) != 1 || stories[0].StoryID != h.story.StoryID {
		t.Fatalf("ListStoriesByEpic = %+v", stories)
	}
}

// TestHierarchyRefusesAParentInAnotherOrganization: the parent is resolved
// in the caller's organization, so another tenant's Feature is ErrNotFound
// — indistinguishable from absent, by the seam's rule.
func TestHierarchyRefusesAParentInAnotherOrganization(t *testing.T) {
	f := newFixture(t)
	h := provisionHierarchy(t, f)
	ctx := context.Background()

	// The parent is resolved before the user, so no other-tenant user is
	// needed to reach the refusal; a wrong-tenant user would fail later.
	_, err := f.store.CreateEpic(ctx, store.CreateEpicInput{
		Title: "x", OrganizationID: f.otherOrgID, UserID: f.userID, FeatureID: h.feature.FeatureID, RepositoryID: h.repository,
	})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("an epic under another tenant's feature: %v, want ErrNotFound", err)
	}
	_, err = f.store.CreateStory(ctx, store.CreateStoryInput{
		Title: "x", OrganizationID: f.otherOrgID, UserID: f.userID, EpicID: h.epic.EpicID,
	})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a story under another tenant's epic: %v, want ErrNotFound", err)
	}
}

// TestEpicRepositoryMustBeAMemberOfItsProduct: the schema's
// epics_repository_membership_fkey, reached through the seam.
func TestEpicRepositoryMustBeAMemberOfItsProduct(t *testing.T) {
	f := newFixture(t)
	h := provisionHierarchy(t, f)
	ctx := context.Background()
	other := provisionProduct(t, f, "other")
	stranger, err := f.store.ProvisionRepository(ctx, store.ProvisionRepositoryInput{
		Slug: "stranger", DisplayName: "Stranger", OrganizationID: f.organizationID, PrimaryProductID: other, UserID: f.userID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateEpic(ctx, store.CreateEpicInput{
		Title: "x", OrganizationID: f.organizationID, UserID: f.userID,
		FeatureID: h.feature.FeatureID, RepositoryID: stranger.Record.RepositoryID,
	}); err == nil {
		t.Fatal("an epic was created in a repository its product has no membership of")
	}
	// A secondary membership makes it legal.
	if _, err := f.store.AddRepositoryToProduct(ctx, f.organizationID, h.product, stranger.Record.RepositoryID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateEpic(ctx, store.CreateEpicInput{
		Title: "x", OrganizationID: f.organizationID, UserID: f.userID,
		FeatureID: h.feature.FeatureID, RepositoryID: stranger.Record.RepositoryID,
	}); err != nil {
		t.Fatalf("an epic in a member repository was refused: %v", err)
	}
}

// TestOneWorkGroupPerEpic: EnsureWorkGroup creates once and reads after.
func TestOneWorkGroupPerEpic(t *testing.T) {
	f := newFixture(t)
	h := provisionHierarchy(t, f)
	ctx := context.Background()

	first, err := f.store.EnsureWorkGroup(ctx, f.organizationID, h.epic.EpicID)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created {
		t.Fatal("the first EnsureWorkGroup must create")
	}
	if first.Record.EpicID != h.epic.EpicID || first.Record.ProductID != h.product || first.Record.FeatureID != h.feature.FeatureID {
		t.Fatalf("work group lineage %+v is not the epic's", first.Record)
	}
	second, err := f.store.EnsureWorkGroup(ctx, f.organizationID, h.epic.EpicID)
	if err != nil {
		t.Fatal(err)
	}
	if second.Created || second.Record.WorkGroupID != first.Record.WorkGroupID {
		t.Fatalf("a second EnsureWorkGroup returned %+v, want the first's row", second.Record)
	}
	if _, err := f.store.GetWorkGroupByEpic(ctx, f.otherOrgID, h.epic.EpicID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("another tenant read this epic's work group: %v", err)
	}
}

// TestIncomingEdgesAreReadAgainstTheStoryOwnEpic: an edge planted by
// fixture is returned with a nil completion (not ready), and after the
// pointer is set (by fixture, since item 10 owns the writer) with it.
func TestIncomingEdgesAreReadAgainstTheStoryOwnEpic(t *testing.T) {
	f := newFixture(t)
	h := provisionHierarchy(t, f)
	ctx := context.Background()
	predecessor, err := f.store.CreateStory(ctx, store.CreateStoryInput{
		Title: "before", OrganizationID: f.organizationID, UserID: f.userID, EpicID: h.epic.EpicID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO story_dependencies
		(organization_id, product_id, feature_id, epic_id, successor_story_id, predecessor_story_id)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		f.organizationID, h.product, h.feature.FeatureID, h.epic.EpicID, h.story.StoryID, predecessor.StoryID); err != nil {
		t.Fatal(err)
	}
	edges, err := f.store.ListIncomingStoryDependencies(ctx, f.organizationID, h.story.StoryID)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 || edges[0].PredecessorStoryID != predecessor.StoryID || edges[0].SatisfyingCompletionArtifactID != nil {
		t.Fatalf("edges = %+v, want one unsatisfied edge from %s", edges, predecessor.StoryID)
	}
	none, err := f.store.ListIncomingStoryDependencies(ctx, f.organizationID, predecessor.StoryID)
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("the predecessor has incoming edges %+v; the edge runs the other way", none)
	}
}
