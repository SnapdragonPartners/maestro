package persistence

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDatabaseOperations(t *testing.T) {
	// Helper function to create a new database for each test
	createTestDB := func(t *testing.T) (*DatabaseOperations, func()) {
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

		// Complete story1
		updateReq := &UpdateStoryStatusRequest{
			StoryID: story1ID,
			Status:  StatusCommitted,
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
	validStatuses := []string{StatusNew, StatusPlanning, StatusCoding, StatusCommitted, StatusMerged, StatusError, StatusDuplicate}

	for _, status := range validStatuses {
		if !IsValidStatus(status) {
			t.Errorf("Expected %s to be valid", status)
		}
	}

	if IsValidStatus("invalid") {
		t.Error("Expected 'invalid' to be invalid")
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
