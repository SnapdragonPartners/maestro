package persistence

import (
	"fmt"
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

	// Use test-session as session ID for all test operations
	return NewDatabaseOperations(db, "test-session"), cleanup
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
	validStatuses := []string{StatusNew, StatusPending, StatusDispatched, StatusPlanning, StatusCoding, StatusDone}

	// Simple validation test - just check that the constants are defined correctly
	for _, status := range validStatuses {
		if status == "" {
			t.Errorf("Expected status to be non-empty")
		}
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

// TestSessionIsolation verifies that database operations are properly isolated by session_id.
func TestSessionIsolation(t *testing.T) {
	// Create a shared database for multiple sessions
	tempDir, err := os.MkdirTemp("", "session_isolation_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "test.db")
	db, err := InitializeDatabase(dbPath)
	if err != nil {
		t.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// sessionCounter ensures unique session IDs for each sub-test
	sessionCounter := 0
	createSessions := func() (*DatabaseOperations, *DatabaseOperations) {
		sessionCounter++
		session1 := fmt.Sprintf("session-%03d-a", sessionCounter)
		session2 := fmt.Sprintf("session-%03d-b", sessionCounter)
		return NewDatabaseOperations(db, session1), NewDatabaseOperations(db, session2)
	}

	// Test 1: Specs should be isolated by session
	t.Run("SpecIsolation", func(t *testing.T) {
		ops1, ops2 := createSessions()
		spec1 := &Spec{
			ID:      GenerateSpecID(),
			Content: "Spec from session 1",
		}
		spec2 := &Spec{
			ID:      GenerateSpecID(),
			Content: "Spec from session 2",
		}

		// Insert specs into different sessions
		if err := ops1.UpsertSpec(spec1); err != nil {
			t.Fatalf("Failed to insert spec1: %v", err)
		}
		if err := ops2.UpsertSpec(spec2); err != nil {
			t.Fatalf("Failed to insert spec2: %v", err)
		}

		// Session 1 should only see spec1
		retrieved1, err := ops1.GetSpecByID(spec1.ID)
		if err != nil {
			t.Fatalf("Failed to get spec1: %v", err)
		}
		if retrieved1.Content != spec1.Content {
			t.Errorf("Expected spec1 content %q, got %q", spec1.Content, retrieved1.Content)
		}

		// Session 1 should NOT see spec2
		_, err = ops1.GetSpecByID(spec2.ID)
		if err == nil {
			t.Error("Session 1 should not be able to retrieve spec2")
		}

		// Session 2 should only see spec2
		retrieved2, err := ops2.GetSpecByID(spec2.ID)
		if err != nil {
			t.Fatalf("Failed to get spec2: %v", err)
		}
		if retrieved2.Content != spec2.Content {
			t.Errorf("Expected spec2 content %q, got %q", spec2.Content, retrieved2.Content)
		}

		// Session 2 should NOT see spec1
		_, err = ops2.GetSpecByID(spec1.ID)
		if err == nil {
			t.Error("Session 2 should not be able to retrieve spec1")
		}
	})

	// Test 2: Stories should be isolated by session
	t.Run("StoryIsolation", func(t *testing.T) {
		ops1, ops2 := createSessions()
		// Create parent specs for each session
		spec1ID := GenerateSpecID()
		spec2ID := GenerateSpecID()

		spec1 := &Spec{ID: spec1ID, Content: "Parent spec 1"}
		spec2 := &Spec{ID: spec2ID, Content: "Parent spec 2"}

		if err := ops1.UpsertSpec(spec1); err != nil {
			t.Fatalf("Failed to insert spec1: %v", err)
		}
		if err := ops2.UpsertSpec(spec2); err != nil {
			t.Fatalf("Failed to insert spec2: %v", err)
		}

		story1ID, _ := GenerateStoryID()
		story2ID, _ := GenerateStoryID()

		story1 := &Story{
			ID:        story1ID,
			SpecID:    spec1ID,
			Title:     "Story from session 1",
			Content:   "Content 1",
			Status:    StatusNew,
			StoryType: "app",
		}
		story2 := &Story{
			ID:        story2ID,
			SpecID:    spec2ID,
			Title:     "Story from session 2",
			Content:   "Content 2",
			Status:    StatusNew,
			StoryType: "app",
		}

		// Insert stories into different sessions
		if err := ops1.UpsertStory(story1); err != nil {
			t.Fatalf("Failed to insert story1: %v", err)
		}
		if err := ops2.UpsertStory(story2); err != nil {
			t.Fatalf("Failed to insert story2: %v", err)
		}

		// Session 1 should only see story1
		retrieved1, err := ops1.GetStoryByID(story1ID)
		if err != nil {
			t.Fatalf("Failed to get story1: %v", err)
		}
		if retrieved1.Title != story1.Title {
			t.Errorf("Expected story1 title %q, got %q", story1.Title, retrieved1.Title)
		}

		// Session 1 should NOT see story2
		_, err = ops1.GetStoryByID(story2ID)
		if err == nil {
			t.Error("Session 1 should not be able to retrieve story2")
		}

		// GetAllStories should be session-isolated
		allStories1, err := ops1.GetAllStories()
		if err != nil {
			t.Fatalf("Failed to get all stories for session1: %v", err)
		}
		if len(allStories1) != 1 {
			t.Errorf("Session 1 should see 1 story, got %d", len(allStories1))
		}

		allStories2, err := ops2.GetAllStories()
		if err != nil {
			t.Fatalf("Failed to get all stories for session2: %v", err)
		}
		if len(allStories2) != 1 {
			t.Errorf("Session 2 should see 1 story, got %d", len(allStories2))
		}
	})

	// Test 3: Queries should be session-isolated
	t.Run("QueryIsolation", func(t *testing.T) {
		ops1, ops2 := createSessions()
		// Create parent specs for each session
		spec1ID := GenerateSpecID()
		spec2ID := GenerateSpecID()

		spec1 := &Spec{ID: spec1ID, Content: "Query spec 1"}
		spec2 := &Spec{ID: spec2ID, Content: "Query spec 2"}

		if err := ops1.UpsertSpec(spec1); err != nil {
			t.Fatalf("Failed to insert spec1: %v", err)
		}
		if err := ops2.UpsertSpec(spec2); err != nil {
			t.Fatalf("Failed to insert spec2: %v", err)
		}

		// Create multiple stories in each session
		for i := 0; i < 3; i++ {
			story1ID, _ := GenerateStoryID()
			story2ID, _ := GenerateStoryID()

			story1 := &Story{
				ID:        story1ID,
				SpecID:    spec1ID,
				Title:     fmt.Sprintf("Session1 Story %d", i),
				Content:   "Content",
				Status:    StatusNew,
				StoryType: "app",
			}
			story2 := &Story{
				ID:        story2ID,
				SpecID:    spec2ID,
				Title:     fmt.Sprintf("Session2 Story %d", i),
				Content:   "Content",
				Status:    StatusPlanning,
				StoryType: "app",
			}

			if err := ops1.UpsertStory(story1); err != nil {
				t.Fatalf("Failed to insert story1: %v", err)
			}
			if err := ops2.UpsertStory(story2); err != nil {
				t.Fatalf("Failed to insert story2: %v", err)
			}
		}

		// Query by status - each session should only see its own stories
		filter1 := &StoryFilter{Status: &[]string{StatusNew}[0]}
		results1, err := ops1.QueryStoriesByFilter(filter1)
		if err != nil {
			t.Fatalf("Failed to query stories for session1: %v", err)
		}
		if len(results1) != 3 {
			t.Errorf("Session 1 should see 3 StatusNew stories, got %d", len(results1))
		}

		filter2 := &StoryFilter{Status: &[]string{StatusPlanning}[0]}
		results2, err := ops2.QueryStoriesByFilter(filter2)
		if err != nil {
			t.Fatalf("Failed to query stories for session2: %v", err)
		}
		if len(results2) != 3 {
			t.Errorf("Session 2 should see 3 StatusPlanning stories, got %d", len(results2))
		}

		// QueryPendingStories should be session-isolated
		pending1, err := ops1.QueryPendingStories()
		if err != nil {
			t.Fatalf("Failed to query pending stories for session1: %v", err)
		}
		if len(pending1) != 3 {
			t.Errorf("Session 1 should see 3 pending stories, got %d", len(pending1))
		}

		pending2, err := ops2.QueryPendingStories()
		if err != nil {
			t.Fatalf("Failed to query pending stories for session2: %v", err)
		}
		if len(pending2) != 0 {
			t.Errorf("Session 2 should see 0 pending stories (all are StatusPlanning), got %d", len(pending2))
		}
	})

	// Test 4: Agent requests/responses should be session-isolated
	t.Run("AgentMessageIsolation", func(t *testing.T) {
		ops1, ops2 := createSessions()
		// Create parent specs and stories for each session
		spec1ID := GenerateSpecID()
		spec2ID := GenerateSpecID()

		spec1 := &Spec{ID: spec1ID, Content: "Agent spec 1"}
		spec2 := &Spec{ID: spec2ID, Content: "Agent spec 2"}

		if err := ops1.UpsertSpec(spec1); err != nil {
			t.Fatalf("Failed to insert spec1: %v", err)
		}
		if err := ops2.UpsertSpec(spec2); err != nil {
			t.Fatalf("Failed to insert spec2: %v", err)
		}

		story1ID, _ := GenerateStoryID()
		story2ID, _ := GenerateStoryID()

		story1 := &Story{
			ID:        story1ID,
			SpecID:    spec1ID,
			Title:     "Story 1",
			Content:   "Content 1",
			Status:    StatusNew,
			StoryType: "app",
		}
		story2 := &Story{
			ID:        story2ID,
			SpecID:    spec2ID,
			Title:     "Story 2",
			Content:   "Content 2",
			Status:    StatusNew,
			StoryType: "app",
		}

		if err := ops1.UpsertStory(story1); err != nil {
			t.Fatalf("Failed to insert story1: %v", err)
		}
		if err := ops2.UpsertStory(story2); err != nil {
			t.Fatalf("Failed to insert story2: %v", err)
		}

		// Create agent requests in each session
		req1 := &AgentRequest{
			ID:          GenerateSpecID(),
			StoryID:     &story1ID,
			RequestType: "question",
			FromAgent:   "coder-1",
			ToAgent:     "architect",
			Content:     "Question from session 1",
		}
		req2 := &AgentRequest{
			ID:          GenerateSpecID(),
			StoryID:     &story2ID,
			RequestType: "question",
			FromAgent:   "coder-2",
			ToAgent:     "architect",
			Content:     "Question from session 2",
		}

		if err := ops1.UpsertAgentRequest(req1); err != nil {
			t.Fatalf("Failed to insert request1: %v", err)
		}
		if err := ops2.UpsertAgentRequest(req2); err != nil {
			t.Fatalf("Failed to insert request2: %v", err)
		}

		// Session 1 should only see its own requests
		requests1, err := ops1.GetAgentRequestsByStory(story1ID)
		if err != nil {
			t.Fatalf("Failed to get requests for session1: %v", err)
		}
		if len(requests1) != 1 {
			t.Errorf("Session 1 should see 1 request, got %d", len(requests1))
		}

		// Session 1 should NOT see session 2's requests
		requests1ForStory2, err := ops1.GetAgentRequestsByStory(story2ID)
		if err != nil {
			t.Fatalf("Failed to query requests for story2 in session1: %v", err)
		}
		if len(requests1ForStory2) != 0 {
			t.Errorf("Session 1 should see 0 requests for story2, got %d", len(requests1ForStory2))
		}

		// GetRecentMessages should be session-isolated
		messages1, err := ops1.GetRecentMessages(10)
		if err != nil {
			t.Fatalf("Failed to get recent messages for session1: %v", err)
		}
		if len(messages1) != 1 {
			t.Errorf("Session 1 should see 1 message, got %d", len(messages1))
		}

		messages2, err := ops2.GetRecentMessages(10)
		if err != nil {
			t.Fatalf("Failed to get recent messages for session2: %v", err)
		}
		if len(messages2) != 1 {
			t.Errorf("Session 2 should see 1 message, got %d", len(messages2))
		}
	})

	// Test 5: UpdateStoryStatus should be session-isolated
	t.Run("UpdateStoryStatusIsolation", func(t *testing.T) {
		ops1, ops2 := createSessions()
		// Create parent specs for each session
		spec1ID := GenerateSpecID()
		spec2ID := GenerateSpecID()

		spec1 := &Spec{ID: spec1ID, Content: "Update spec 1"}
		spec2 := &Spec{ID: spec2ID, Content: "Update spec 2"}

		if err := ops1.UpsertSpec(spec1); err != nil {
			t.Fatalf("Failed to insert spec1: %v", err)
		}
		if err := ops2.UpsertSpec(spec2); err != nil {
			t.Fatalf("Failed to insert spec2: %v", err)
		}

		// Create stories in each session
		story1ID, _ := GenerateStoryID()
		story2ID, _ := GenerateStoryID()

		story1 := &Story{
			ID:        story1ID,
			SpecID:    spec1ID,
			Title:     "Story in session 1",
			Content:   "Content 1",
			Status:    StatusNew,
			StoryType: "app",
		}
		story2 := &Story{
			ID:        story2ID,
			SpecID:    spec2ID,
			Title:     "Story in session 2",
			Content:   "Content 2",
			Status:    StatusNew,
			StoryType: "app",
		}

		if err := ops1.UpsertStory(story1); err != nil {
			t.Fatalf("Failed to insert story1: %v", err)
		}
		if err := ops2.UpsertStory(story2); err != nil {
			t.Fatalf("Failed to insert story2: %v", err)
		}

		// Update status in session 1
		updateReq1 := &UpdateStoryStatusRequest{
			StoryID: story1ID,
			Status:  StatusCoding,
		}
		if err := ops1.UpdateStoryStatus(updateReq1); err != nil {
			t.Fatalf("Failed to update story status in session1: %v", err)
		}

		// Verify session 1 sees the update
		updated1, err := ops1.GetStoryByID(story1ID)
		if err != nil {
			t.Fatalf("Failed to get updated story1: %v", err)
		}
		if updated1.Status != StatusCoding {
			t.Errorf("Session 1 story should have status %q, got %q", StatusCoding, updated1.Status)
		}

		// Verify session 2 cannot see or update session 1's story
		_, err = ops1.GetStoryByID(story2ID)
		if err == nil {
			t.Error("Session 1 should not be able to retrieve story2 from session 2")
		}

		// Verify that attempting to update a story from a different session fails
		updateReq2 := &UpdateStoryStatusRequest{
			StoryID: story1ID, // Try to update session1's story from session2
			Status:  StatusDone,
		}
		err = ops2.UpdateStoryStatus(updateReq2)
		if err == nil {
			t.Error("Session 2 should not be able to update session 1's story")
		}

		// Verify session 1's story is still StatusCoding (not affected by session 2's attempt)
		stillCoding, err := ops1.GetStoryByID(story1ID)
		if err != nil {
			t.Fatalf("Failed to get story1 after session2 update attempt: %v", err)
		}
		if stillCoding.Status != StatusCoding {
			t.Errorf("Session 1 story should still be %q, got %q", StatusCoding, stillCoding.Status)
		}
	})

	// Test 6: BatchUpsertStoriesWithDependencies should be session-isolated
	t.Run("BatchUpsertIsolation", func(t *testing.T) {
		ops1, ops2 := createSessions()
		// Create parent specs for each session
		spec1ID := GenerateSpecID()
		spec2ID := GenerateSpecID()

		spec1 := &Spec{ID: spec1ID, Content: "Batch spec 1"}
		spec2 := &Spec{ID: spec2ID, Content: "Batch spec 2"}

		if err := ops1.UpsertSpec(spec1); err != nil {
			t.Fatalf("Failed to insert spec1: %v", err)
		}
		if err := ops2.UpsertSpec(spec2); err != nil {
			t.Fatalf("Failed to insert spec2: %v", err)
		}

		// Batch insert stories in session 1
		batchStory1ID, _ := GenerateStoryID()
		batchStory2ID, _ := GenerateStoryID()

		batch1 := &BatchUpsertStoriesWithDependenciesRequest{
			Stories: []*Story{
				{
					ID:        batchStory1ID,
					SpecID:    spec1ID,
					Title:     "Batch Story 1",
					Content:   "Content 1",
					Status:    StatusNew,
					StoryType: "app",
				},
				{
					ID:        batchStory2ID,
					SpecID:    spec1ID,
					Title:     "Batch Story 2",
					Content:   "Content 2",
					Status:    StatusNew,
					StoryType: "app",
				},
			},
			Dependencies: []*StoryDependency{
				{StoryID: batchStory2ID, DependsOn: batchStory1ID},
			},
		}

		if err := ops1.BatchUpsertStoriesWithDependencies(batch1); err != nil {
			t.Fatalf("Failed to batch upsert in session1: %v", err)
		}

		// Session 1 should see both stories
		allStories1, err := ops1.GetAllStories()
		if err != nil {
			t.Fatalf("Failed to get stories for session1: %v", err)
		}
		if len(allStories1) != 2 {
			t.Errorf("Session 1 should see 2 stories, got %d", len(allStories1))
		}

		// Session 2 should see 0 stories
		allStories2, err := ops2.GetAllStories()
		if err != nil {
			t.Fatalf("Failed to get stories for session2: %v", err)
		}
		if len(allStories2) != 0 {
			t.Errorf("Session 2 should see 0 stories, got %d", len(allStories2))
		}
	})
}
