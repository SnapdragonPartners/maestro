package persistence

import (
	"os"
	"path/filepath"
	"testing"
)

// Helper function to create a new database for each test.
func createTestDB(t *testing.T) (*DatabaseOperations, func()) {
	tempDir, err := os.MkdirTemp("", "persistence_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	dbPath := filepath.Join(tempDir, "test.db")

	db, err := InitializeDatabase(dbPath)
	if err != nil {
		t.Fatalf("Failed to initialize database: %v", err)
	}

	cleanup := func() {
		db.Close()
		os.RemoveAll(tempDir)
	}

	return NewDatabaseOperations(db), cleanup
}

func TestDatabaseOperations(t *testing.T) {
	// Test spec operations
	t.Run("SpecOperations", func(t *testing.T) {
		ops, cleanup := createTestDB(t)
		defer cleanup()

		specID := GenerateSpecID()
		spec := &Spec{
			ID:      specID,
			Content: "Test specification content",
		}

		// Upsert spec
		err := ops.UpsertSpec(spec)
		if err != nil {
			t.Fatalf("Failed to upsert spec: %v", err)
		}

		// Retrieve spec
		retrievedSpec, err := ops.GetSpecByID(specID)
		if err != nil {
			t.Fatalf("Failed to get spec: %v", err)
		}

		if retrievedSpec.Content != spec.Content {
			t.Errorf("Expected content %q, got %q", spec.Content, retrievedSpec.Content)
		}
	})

	// Test story operations
	t.Run("StoryOperations", func(t *testing.T) {
		ops, cleanup := createTestDB(t)
		defer cleanup()

		specID := GenerateSpecID()
		spec := &Spec{
			ID:      specID,
			Content: "Parent spec",
		}
		err := ops.UpsertSpec(spec)
		if err != nil {
			t.Fatalf("Failed to create parent spec: %v", err)
		}

		storyID, err := GenerateStoryID()
		if err != nil {
			t.Fatalf("Failed to generate story ID: %v", err)
		}

		story := &Story{
			ID:        storyID,
			SpecID:    specID,
			Title:     "Test Story",
			Content:   "Story content",
			Status:    StatusNew,
			StoryType: "app",
			Priority:  1,
		}

		// Upsert story
		err = ops.UpsertStory(story)
		if err != nil {
			t.Fatalf("Failed to upsert story: %v", err)
		}

		// Retrieve story
		retrievedStory, err := ops.GetStoryByID(storyID)
		if err != nil {
			t.Fatalf("Failed to get story: %v", err)
		}

		if retrievedStory.Title != story.Title {
			t.Errorf("Expected title %q, got %q", story.Title, retrievedStory.Title)
		}

		if retrievedStory.Status != StatusNew {
			t.Errorf("Expected status %q, got %q", StatusNew, retrievedStory.Status)
		}
	})

	// Test status updates
	t.Run("StatusUpdates", func(t *testing.T) {
		ops, cleanup := createTestDB(t)
		defer cleanup()

		// Create parent spec first
		specID := GenerateSpecID()
		spec := &Spec{
			ID:      specID,
			Content: "Parent spec for status test",
		}
		err := ops.UpsertSpec(spec)
		if err != nil {
			t.Fatalf("Failed to create parent spec: %v", err)
		}

		storyID, err := GenerateStoryID()
		if err != nil {
			t.Fatalf("Failed to generate story ID: %v", err)
		}

		story := &Story{
			ID:        storyID,
			SpecID:    specID,
			Title:     "Status Test Story",
			Content:   "Content",
			Status:    StatusNew,
			StoryType: "app",
		}

		err = ops.UpsertStory(story)
		if err != nil {
			t.Fatalf("Failed to create story: %v", err)
		}

		// Update to planning status
		updateReq := &UpdateStoryStatusRequest{
			StoryID: storyID,
			Status:  StatusPlanning,
		}

		err = ops.UpdateStoryStatus(updateReq)
		if err != nil {
			t.Fatalf("Failed to update story status: %v", err)
		}

		// Verify status update
		updated, err := ops.GetStoryByID(storyID)
		if err != nil {
			t.Fatalf("Failed to get updated story: %v", err)
		}

		if updated.Status != StatusPlanning {
			t.Errorf("Expected status %q, got %q", StatusPlanning, updated.Status)
		}

		if updated.StartedAt == nil {
			t.Error("Expected started_at to be set for planning status")
		}
	})

	// Test dependencies
	t.Run("Dependencies", func(t *testing.T) {
		ops, cleanup := createTestDB(t)
		defer cleanup()

		// Create parent spec first
		specID := GenerateSpecID()
		spec := &Spec{
			ID:      specID,
			Content: "Parent spec for dependencies test",
		}
		err := ops.UpsertSpec(spec)
		if err != nil {
			t.Fatalf("Failed to create parent spec: %v", err)
		}

		// Create two stories
		story1ID, _ := GenerateStoryID()
		story2ID, _ := GenerateStoryID()

		story1 := &Story{
			ID:        story1ID,
			SpecID:    specID,
			Title:     "Story 1",
			Content:   "Content 1",
			Status:    StatusNew,
			StoryType: "devops",
		}

		story2 := &Story{
			ID:        story2ID,
			SpecID:    specID,
			Title:     "Story 2",
			Content:   "Content 2",
			Status:    StatusNew,
			StoryType: "app",
		}

		err = ops.UpsertStory(story1)
		if err != nil {
			t.Fatalf("Failed to create story1: %v", err)
		}

		err = ops.UpsertStory(story2)
		if err != nil {
			t.Fatalf("Failed to create story2: %v", err)
		}

		// Add dependency: story2 depends on story1
		err = ops.AddStoryDependency(story2ID, story1ID)
		if err != nil {
			t.Fatalf("Failed to add dependency: %v", err)
		}

		// Get dependencies
		deps, err := ops.GetStoryDependencies(story2ID)
		if err != nil {
			t.Fatalf("Failed to get dependencies: %v", err)
		}

		if len(deps) != 1 || deps[0] != story1ID {
			t.Errorf("Expected dependency [%s], got %v", story1ID, deps)
		}

		// Test pending stories - story2 should not be pending because story1 is not completed
		pending, err := ops.QueryPendingStories()
		if err != nil {
			t.Fatalf("Failed to query pending stories: %v", err)
		}

		// Only story1 should be pending
		if len(pending) != 1 || pending[0].ID != story1ID {
			t.Errorf("Expected pending story [%s], got %v", story1ID, getStoryIDs(pending))
		}

		// Complete story1 with PR and commit information
		prID := "123"
		commitHash := "abc123def456"
		completionSummary := "Story completed via merge. PR: https://github.com/test/repo/pull/123, Commit: abc123def456"
		updateReq := &UpdateStoryStatusRequest{
			StoryID:           story1ID,
			Status:            StatusDone,
			PRID:              &prID,
			CommitHash:        &commitHash,
			CompletionSummary: &completionSummary,
		}
		err = ops.UpdateStoryStatus(updateReq)
		if err != nil {
			t.Fatalf("Failed to complete story1: %v", err)
		}

		// Now story2 should be pending
		pending, err = ops.QueryPendingStories()
		if err != nil {
			t.Fatalf("Failed to query pending stories after completion: %v", err)
		}

		if len(pending) != 1 || pending[0].ID != story2ID {
			t.Errorf("Expected pending story [%s], got %v", story2ID, getStoryIDs(pending))
		}
	})

	// Test queries
	t.Run("Queries", func(t *testing.T) {
		ops, cleanup := createTestDB(t)
		defer cleanup()

		// Create parent spec first
		specID := GenerateSpecID()
		spec := &Spec{
			ID:      specID,
			Content: "Parent spec for queries test",
		}
		err := ops.UpsertSpec(spec)
		if err != nil {
			t.Fatalf("Failed to create parent spec: %v", err)
		}

		// Create stories with different statuses
		stories := []*Story{
			{ID: mustGenerateStoryID(), SpecID: specID, Title: "New Story", Content: "Content", Status: StatusNew, StoryType: "app"},
			{ID: mustGenerateStoryID(), SpecID: specID, Title: "Planning Story", Content: "Content", Status: StatusPlanning, StoryType: "app"},
			{ID: mustGenerateStoryID(), SpecID: specID, Title: "Coding Story", Content: "Content", Status: StatusCoding, StoryType: "devops"},
		}

		for _, story := range stories {
			if upsertErr := ops.UpsertStory(story); upsertErr != nil {
				t.Fatalf("Failed to create story %s: %v", story.ID, upsertErr)
			}
		}

		// Query by status
		filter := &StoryFilter{Status: &[]string{StatusNew}[0]}
		results, err := ops.QueryStoriesByFilter(filter)
		if err != nil {
			t.Fatalf("Failed to query by status: %v", err)
		}

		if len(results) != 1 || results[0].Status != StatusNew {
			t.Errorf("Expected 1 new story, got %d", len(results))
		}

		// Query by multiple statuses
		filter = &StoryFilter{Statuses: []string{StatusPlanning, StatusCoding}}
		results, err = ops.QueryStoriesByFilter(filter)
		if err != nil {
			t.Fatalf("Failed to query by multiple statuses: %v", err)
		}

		if len(results) != 2 {
			t.Errorf("Expected 2 stories, got %d", len(results))
		}
	})
}

func TestIDGeneration(t *testing.T) {
	// Test spec ID generation
	specID := GenerateSpecID()
	if len(specID) != 36 { // UUID length
		t.Errorf("Expected spec ID length 36, got %d", len(specID))
	}

	// Test story ID generation
	storyID, err := GenerateStoryID()
	if err != nil {
		t.Fatalf("Failed to generate story ID: %v", err)
	}
	if len(storyID) != 8 {
		t.Errorf("Expected story ID length 8, got %d", len(storyID))
	}

	// Test uniqueness
	ids := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id, err := GenerateStoryID()
		if err != nil {
			t.Fatalf("Failed to generate story ID: %v", err)
		}
		if ids[id] {
			t.Errorf("Generated duplicate ID: %s", id)
		}
		ids[id] = true
	}
}

func TestValidStatus(t *testing.T) {
	validStatuses := []string{StatusNew, StatusPlanning, StatusCoding, StatusDone, StatusError, StatusDuplicate}

	for _, status := range validStatuses {
		if !IsValidStatus(status) {
			t.Errorf("Expected %s to be valid", status)
		}
	}

	if IsValidStatus("invalid") {
		t.Error("Expected 'invalid' to be invalid")
	}
}

func TestBatchUpsertStoriesWithDependencies(t *testing.T) {
	ops, cleanup := createTestDB(t)
	defer cleanup()

	// Create parent spec first
	specID := GenerateSpecID()
	spec := &Spec{
		ID:      specID,
		Content: "Parent spec for batch test",
	}
	err := ops.UpsertSpec(spec)
	if err != nil {
		t.Fatalf("Failed to create parent spec: %v", err)
	}

	// Create stories for batch insert
	story1ID, _ := GenerateStoryID()
	story2ID, _ := GenerateStoryID()
	story3ID, _ := GenerateStoryID()

	stories := []*Story{
		{
			ID:        story1ID,
			SpecID:    specID,
			Title:     "Batch Story 1",
			Content:   "Content 1",
			Status:    StatusNew,
			StoryType: "app",
			Priority:  1,
		},
		{
			ID:        story2ID,
			SpecID:    specID,
			Title:     "Batch Story 2",
			Content:   "Content 2",
			Status:    StatusNew,
			StoryType: "app",
			Priority:  2,
		},
		{
			ID:        story3ID,
			SpecID:    specID,
			Title:     "Batch Story 3",
			Content:   "Content 3",
			Status:    StatusNew,
			StoryType: "devops",
			Priority:  3,
		},
	}

	// Create dependencies: story2 depends on story1, story3 depends on both
	dependencies := []*StoryDependency{
		{StoryID: story2ID, DependsOn: story1ID},
		{StoryID: story3ID, DependsOn: story1ID},
		{StoryID: story3ID, DependsOn: story2ID},
	}

	// Execute batch operation
	batchReq := &BatchUpsertStoriesWithDependenciesRequest{
		Stories:      stories,
		Dependencies: dependencies,
	}

	err = ops.BatchUpsertStoriesWithDependencies(batchReq)
	if err != nil {
		t.Fatalf("Failed to batch upsert stories with dependencies: %v", err)
	}

	// Verify all stories were inserted
	for _, expectedStory := range stories {
		story, getErr := ops.GetStoryByID(expectedStory.ID)
		if getErr != nil {
			t.Fatalf("Failed to get story %s: %v", expectedStory.ID, getErr)
		}
		if story.Title != expectedStory.Title {
			t.Errorf("Expected title %q, got %q", expectedStory.Title, story.Title)
		}
	}

	// Verify dependencies were inserted
	deps2, err := ops.GetStoryDependencies(story2ID)
	if err != nil {
		t.Fatalf("Failed to get dependencies for story2: %v", err)
	}
	if len(deps2) != 1 || deps2[0] != story1ID {
		t.Errorf("Expected story2 to depend on [%s], got %v", story1ID, deps2)
	}

	deps3, err := ops.GetStoryDependencies(story3ID)
	if err != nil {
		t.Fatalf("Failed to get dependencies for story3: %v", err)
	}
	if len(deps3) != 2 {
		t.Errorf("Expected story3 to have 2 dependencies, got %d", len(deps3))
	}

	// Verify pending stories query works correctly with batch-inserted dependencies
	pending, err := ops.QueryPendingStories()
	if err != nil {
		t.Fatalf("Failed to query pending stories: %v", err)
	}

	// Only story1 should be pending (no dependencies)
	if len(pending) != 1 || pending[0].ID != story1ID {
		t.Errorf("Expected only story1 to be pending, got %v", getStoryIDs(pending))
	}
}

// Helper functions.
func getStoryIDs(stories []*Story) []string {
	ids := make([]string, len(stories))
	for i, story := range stories {
		ids[i] = story.ID
	}
	return ids
}

func mustGenerateStoryID() string {
	id, err := GenerateStoryID()
	if err != nil {
		panic(err)
	}
	return id
}
