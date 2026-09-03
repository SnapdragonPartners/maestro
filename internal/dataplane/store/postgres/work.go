package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/gen"
	"orchestrator/internal/dataplane/store"
)

// The work family (design D10): rows with derived lineage.

func featureFromRow(row *gen.Feature) store.Feature {
	return store.Feature{
		CreatedAt: fromTimestamptz(row.CreatedAt), Title: row.Title,
		FeatureID: fromUUID(row.FeatureID), OrganizationID: fromUUID(row.OrganizationID),
		UserID: fromUUID(row.UserID), ProductID: fromUUID(row.ProductID), IsWrapper: row.IsWrapper,
	}
}

func epicFromRow(row *gen.Epic) store.Epic {
	return store.Epic{
		GoverningArtifactID: fromNullUUID(row.GoverningArtifactID),
		CreatedAt:           fromTimestamptz(row.CreatedAt), Title: row.Title,
		EpicID: fromUUID(row.EpicID), OrganizationID: fromUUID(row.OrganizationID),
		UserID: fromUUID(row.UserID), ProductID: fromUUID(row.ProductID),
		FeatureID: fromUUID(row.FeatureID), RepositoryID: fromUUID(row.RepositoryID),
	}
}

func storyFromRow(row *gen.Story) store.Story {
	return store.Story{
		GoverningArtifactID: fromNullUUID(row.GoverningArtifactID),
		CreatedAt:           fromTimestamptz(row.CreatedAt), Title: row.Title,
		StoryID: fromUUID(row.StoryID), OrganizationID: fromUUID(row.OrganizationID),
		UserID: fromUUID(row.UserID), ProductID: fromUUID(row.ProductID),
		FeatureID: fromUUID(row.FeatureID), EpicID: fromUUID(row.EpicID),
	}
}

func workGroupFromRow(row *gen.WorkGroup) store.WorkGroup {
	return store.WorkGroup{
		CreatedAt: fromTimestamptz(row.CreatedAt), WorkGroupID: fromUUID(row.WorkGroupID),
		OrganizationID: fromUUID(row.OrganizationID), ProductID: fromUUID(row.ProductID),
		FeatureID: fromUUID(row.FeatureID), EpicID: fromUUID(row.EpicID),
	}
}

// checkTitle refuses a blank title before SQL sees it.
func checkTitle(kind, title string) error {
	if strings.TrimSpace(title) == "" {
		return fmt.Errorf("%s title is blank", kind)
	}
	return nil
}

func (t *tx) GetFeature(ctx context.Context, organizationID, featureID uuid.UUID) (*store.Feature, error) {
	row, err := t.queries.GetFeature(ctx, gen.GetFeatureParams{OrganizationID: toUUID(organizationID), FeatureID: toUUID(featureID)})
	if err != nil {
		return nil, notFound(err, "feature", featureID)
	}
	feature := featureFromRow(&row)
	return &feature, nil
}

func (t *tx) GetEpic(ctx context.Context, organizationID, epicID uuid.UUID) (*store.Epic, error) {
	row, err := t.queries.GetEpic(ctx, gen.GetEpicParams{OrganizationID: toUUID(organizationID), EpicID: toUUID(epicID)})
	if err != nil {
		return nil, notFound(err, "epic", epicID)
	}
	epic := epicFromRow(&row)
	return &epic, nil
}

func (t *tx) GetStory(ctx context.Context, organizationID, storyID uuid.UUID) (*store.Story, error) {
	row, err := t.queries.GetStory(ctx, gen.GetStoryParams{OrganizationID: toUUID(organizationID), StoryID: toUUID(storyID)})
	if err != nil {
		return nil, notFound(err, "story", storyID)
	}
	story := storyFromRow(&row)
	return &story, nil
}

func (t *tx) ListStoriesByEpic(ctx context.Context, organizationID, epicID uuid.UUID) ([]store.Story, error) {
	rows, err := t.queries.ListStoriesByEpic(ctx, gen.ListStoriesByEpicParams{OrganizationID: toUUID(organizationID), EpicID: toUUID(epicID)})
	if err != nil {
		return nil, fmt.Errorf("list stories of epic %s: %w", epicID, err)
	}
	stories := make([]store.Story, 0, len(rows))
	for i := range rows {
		stories = append(stories, storyFromRow(&rows[i]))
	}
	return stories, nil
}

func (t *tx) GetWorkGroupByEpic(ctx context.Context, organizationID, epicID uuid.UUID) (*store.WorkGroup, error) {
	row, err := t.queries.GetWorkGroupByEpic(ctx, gen.GetWorkGroupByEpicParams{OrganizationID: toUUID(organizationID), EpicID: toUUID(epicID)})
	if err != nil {
		return nil, notFound(err, "work group of epic", epicID)
	}
	group := workGroupFromRow(&row)
	return &group, nil
}

// ListIncomingStoryDependencies resolves the Story first, so the edges are
// read against its own Epic rather than an Epic the caller asserts.
func (t *tx) ListIncomingStoryDependencies(ctx context.Context, organizationID, storyID uuid.UUID) ([]store.StoryDependency, error) {
	story, err := t.GetStory(ctx, organizationID, storyID)
	if err != nil {
		return nil, err
	}
	return t.incomingEdges(ctx, story)
}

func (t *tx) incomingEdges(ctx context.Context, story *store.Story) ([]store.StoryDependency, error) {
	rows, err := t.queries.ListIncomingStoryDependencies(ctx, gen.ListIncomingStoryDependenciesParams{
		OrganizationID: toUUID(story.OrganizationID), EpicID: toUUID(story.EpicID), SuccessorStoryID: toUUID(story.StoryID),
	})
	if err != nil {
		return nil, fmt.Errorf("list incoming edges of story %s: %w", story.StoryID, err)
	}
	edges := make([]store.StoryDependency, 0, len(rows))
	for i := range rows {
		edges = append(edges, store.StoryDependency{
			SatisfyingCompletionArtifactID: fromNullUUID(rows[i].SatisfyingCompletionArtifactID),
			CreatedAt:                      fromTimestamptz(rows[i].CreatedAt),
			PredecessorStoryID:             fromUUID(rows[i].PredecessorStoryID),
		})
	}
	return edges, nil
}

// CreateFeature creates a Feature under a Product the caller names.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (t *tx) CreateFeature(ctx context.Context, input store.CreateFeatureInput) (*store.Feature, error) {
	if err := checkTitle("feature", input.Title); err != nil {
		return nil, err
	}
	identifier, err := newIdentifier(input.FeatureID)
	if err != nil {
		return nil, err
	}
	row, err := t.queries.InsertFeature(ctx, gen.InsertFeatureParams{
		FeatureID: toUUID(identifier), OrganizationID: toUUID(input.OrganizationID), UserID: toUUID(input.UserID),
		ProductID: toUUID(input.ProductID), Title: input.Title, IsWrapper: input.IsWrapper,
	})
	if err != nil {
		return nil, fmt.Errorf("insert feature %q: %w", input.Title, err)
	}
	feature := featureFromRow(&row)
	return &feature, nil
}

// CreateEpic derives the Product from the Feature. The repository must be a
// member of that Product, which the schema enforces
// (epics_repository_membership_fkey).
//
//nolint:gocritic,dupl // hugeParam: by value, matching the seam interface. dupl: the two derive lineage from different parents, and a shared helper would hide which parent supplies what
func (t *tx) CreateEpic(ctx context.Context, input store.CreateEpicInput) (*store.Epic, error) {
	if err := checkTitle("epic", input.Title); err != nil {
		return nil, err
	}
	feature, err := t.GetFeature(ctx, input.OrganizationID, input.FeatureID)
	if err != nil {
		return nil, err
	}
	identifier, err := newIdentifier(input.EpicID)
	if err != nil {
		return nil, err
	}
	row, err := t.queries.InsertEpic(ctx, gen.InsertEpicParams{
		EpicID: toUUID(identifier), OrganizationID: toUUID(input.OrganizationID), UserID: toUUID(input.UserID),
		ProductID: toUUID(feature.ProductID), FeatureID: toUUID(feature.FeatureID),
		RepositoryID: toUUID(input.RepositoryID), Title: input.Title,
	})
	if err != nil {
		return nil, fmt.Errorf("insert epic %q: %w", input.Title, err)
	}
	epic := epicFromRow(&row)
	return &epic, nil
}

// CreateStory derives Feature and Product from the Epic.
//
//nolint:gocritic,dupl // hugeParam: by value, matching the seam interface. dupl: the two derive lineage from different parents, and a shared helper would hide which parent supplies what
func (t *tx) CreateStory(ctx context.Context, input store.CreateStoryInput) (*store.Story, error) {
	if err := checkTitle("story", input.Title); err != nil {
		return nil, err
	}
	epic, err := t.GetEpic(ctx, input.OrganizationID, input.EpicID)
	if err != nil {
		return nil, err
	}
	identifier, err := newIdentifier(input.StoryID)
	if err != nil {
		return nil, err
	}
	row, err := t.queries.InsertStory(ctx, gen.InsertStoryParams{
		StoryID: toUUID(identifier), OrganizationID: toUUID(input.OrganizationID), UserID: toUUID(input.UserID),
		ProductID: toUUID(epic.ProductID), FeatureID: toUUID(epic.FeatureID), EpicID: toUUID(epic.EpicID),
		Title: input.Title,
	})
	if err != nil {
		return nil, fmt.Errorf("insert story %q: %w", input.Title, err)
	}
	story := storyFromRow(&row)
	return &story, nil
}

// EnsureWorkGroup returns the Epic's Work Group, creating it if absent.
// Insert-or-nothing then read: the one-per-epic key is the arbiter.
func (t *tx) EnsureWorkGroup(ctx context.Context, organizationID, epicID uuid.UUID) (store.Bootstrapped[store.WorkGroup], error) {
	var empty store.Bootstrapped[store.WorkGroup]
	epic, err := t.GetEpic(ctx, organizationID, epicID)
	if err != nil {
		return empty, err
	}
	identifier, err := newIdentifier(uuid.Nil)
	if err != nil {
		return empty, err
	}
	inserted, err := t.queries.InsertWorkGroupIfAbsent(ctx, gen.InsertWorkGroupIfAbsentParams{
		WorkGroupID: toUUID(identifier), OrganizationID: toUUID(organizationID),
		ProductID: toUUID(epic.ProductID), FeatureID: toUUID(epic.FeatureID), EpicID: toUUID(epic.EpicID),
	})
	if err != nil {
		return empty, fmt.Errorf("insert work group for epic %s: %w", epicID, err)
	}
	stored, err := t.GetWorkGroupByEpic(ctx, organizationID, epicID)
	if err != nil {
		return empty, err
	}
	return store.Bootstrapped[store.WorkGroup]{Record: *stored, Created: inserted == 1}, nil
}
