package architect

import (
	"testing"
	"time"
)

func TestNewQueue(t *testing.T) {
	queue := NewQueue("/tmp/test_stories")

	if queue == nil {
		t.Fatal("NewQueue returned nil")
	}

	if queue.storiesDir != "/tmp/test_stories" {
		t.Errorf("Expected storiesDir '/tmp/test_stories', got '%s'", queue.storiesDir)
	}

	if len(queue.stories) != 0 {
		t.Errorf("Expected empty stories map, got %d stories", len(queue.stories))
	}
}

func TestGetReadyStories(t *testing.T) {
	queue := NewQueue("/tmp/test")

	// Manually add stories to test dependency resolution.
	queue.stories["001"] = &QueuedStory{
		ID:        "001",
		Title:     "Independent Story",
		Status:    StatusPending,
		DependsOn: []string{},
	}

	queue.stories["002"] = &QueuedStory{
		ID:        "002",
		Title:     "Depends on 001",
		Status:    StatusPending,
		DependsOn: []string{"001"},
	}

	queue.stories["003"] = &QueuedStory{
		ID:        "003",
		Title:     "Depends on completed story",
		Status:    StatusPending,
		DependsOn: []string{"004"},
	}

	queue.stories["004"] = &QueuedStory{
		ID:     "004",
		Title:  "Completed Story",
		Status: StatusCompleted,
	}

	ready := queue.GetReadyStories()

	// Should return stories 001 and 003 (001 has no deps, 003's deps are completed)
	if len(ready) != 2 {
		t.Errorf("Expected 2 ready stories, got %d", len(ready))
	}

	readyIDs := make(map[string]bool)
	for _, story := range ready {
		readyIDs[story.ID] = true
	}

	if !readyIDs["001"] {
		t.Error("Story 001 should be ready")
	}

	if !readyIDs["003"] {
		t.Error("Story 003 should be ready")
	}

	if readyIDs["002"] {
		t.Error("Story 002 should not be ready (depends on pending 001)")
	}
}

func TestNextReadyStory(t *testing.T) {
	queue := NewQueue("/tmp/test")

	// Add stories with different point values.
	queue.stories["001"] = &QueuedStory{
		ID:              "001",
		Status:          StatusPending,
		DependsOn:       []string{},
		EstimatedPoints: 3,
	}

	queue.stories["002"] = &QueuedStory{
		ID:              "002",
		Status:          StatusPending,
		DependsOn:       []string{},
		EstimatedPoints: 1,
	}

	queue.stories["003"] = &QueuedStory{
		ID:              "003",
		Status:          StatusPending,
		DependsOn:       []string{},
		EstimatedPoints: 2,
	}

	next := queue.NextReadyStory()
	if next == nil {
		t.Fatal("NextReadyStory returned nil")
	}

	// Should return the story with smallest points (002)
	if next.ID != "002" {
		t.Errorf("Expected story 002 (smallest points), got %s", next.ID)
	}
}

func TestStoryStatusTransitions(t *testing.T) {
	queue := NewQueue("/tmp/test")

	queue.stories["001"] = &QueuedStory{
		ID:     "001",
		Status: StatusPending,
	}

	// Test marking as in progress.
	err := queue.MarkInProgress("001", "agent-123")
	if err != nil {
		t.Fatalf("Failed to mark in progress: %v", err)
	}

	story, _ := queue.GetStory("001")
	if story.Status != StatusInProgress {
		t.Errorf("Expected status in_progress, got %s", story.Status)
	}

	if story.AssignedAgent != "agent-123" {
		t.Errorf("Expected assigned agent 'agent-123', got '%s'", story.AssignedAgent)
	}

	if story.StartedAt == nil {
		t.Error("StartedAt should be set")
	}

	// Test marking as waiting review.
	err = queue.MarkWaitingReview("001")
	if err != nil {
		t.Fatalf("Failed to mark waiting review: %v", err)
	}

	story, _ = queue.GetStory("001")
	if story.Status != StatusWaitingReview {
		t.Errorf("Expected status waiting_review, got %s", story.Status)
	}

	// Test marking as completed.
	err = queue.MarkCompleted("001")
	if err != nil {
		t.Fatalf("Failed to mark completed: %v", err)
	}

	story, _ = queue.GetStory("001")
	if story.Status != StatusCompleted {
		t.Errorf("Expected status completed, got %s", story.Status)
	}

	if story.CompletedAt == nil {
		t.Error("CompletedAt should be set")
	}
}

func TestDetectCycles(t *testing.T) {
	queue := NewQueue("/tmp/test")

	// Create a cycle: 001 -> 002 -> 003 -> 001.
	queue.stories["001"] = &QueuedStory{
		ID:        "001",
		DependsOn: []string{"003"},
	}

	queue.stories["002"] = &QueuedStory{
		ID:        "002",
		DependsOn: []string{"001"},
	}

	queue.stories["003"] = &QueuedStory{
		ID:        "003",
		DependsOn: []string{"002"},
	}

	cycles := queue.DetectCycles()
	if len(cycles) == 0 {
		t.Error("Expected to detect a cycle")
	}

	// Verify the cycle contains our stories.
	if len(cycles[0]) < 3 {
		t.Errorf("Expected cycle length >= 3, got %d", len(cycles[0]))
	}
}

func TestQueueSerialization(t *testing.T) {
	queue := NewQueue("/tmp/test")

	now := time.Now().UTC()
	queue.stories["001"] = &QueuedStory{
		ID:              "001",
		Title:           "Test Story",
		Status:          StatusInProgress,
		DependsOn:       []string{"002"},
		EstimatedPoints: 2,
		AssignedAgent:   "agent-123",
		StartedAt:       &now,
		LastUpdated:     now,
	}

	// Test serialization.
	data, err := queue.ToJSON()
	if err != nil {
		t.Fatalf("Failed to serialize queue: %v", err)
	}

	// Test deserialization.
	newQueue := NewQueue("/tmp/test")
	err = newQueue.FromJSON(data)
	if err != nil {
		t.Fatalf("Failed to deserialize queue: %v", err)
	}

	story, exists := newQueue.GetStory("001")
	if !exists {
		t.Fatal("Story not found after deserialization")
	}

	if story.Title != "Test Story" {
		t.Errorf("Expected title 'Test Story', got '%s'", story.Title)
	}

	if story.Status != StatusInProgress {
		t.Errorf("Expected status in_progress, got %s", story.Status)
	}

	if story.AssignedAgent != "agent-123" {
		t.Errorf("Expected assigned agent 'agent-123', got '%s'", story.AssignedAgent)
	}
}

func TestGetQueueSummary(t *testing.T) {
	queue := NewQueue("/tmp/test")

	queue.stories["001"] = &QueuedStory{
		ID:              "001",
		Status:          StatusCompleted,
		EstimatedPoints: 2,
	}

	queue.stories["002"] = &QueuedStory{
		ID:              "002",
		Status:          StatusPending,
		EstimatedPoints: 3,
		DependsOn:       []string{},
	}

	queue.stories["003"] = &QueuedStory{
		ID:              "003",
		Status:          StatusInProgress,
		EstimatedPoints: 1,
	}

	summary := queue.GetQueueSummary()

	if summary["total_stories"] != 3 {
		t.Errorf("Expected 3 total stories, got %v", summary["total_stories"])
	}

	if summary["total_points"] != 6 {
		t.Errorf("Expected 6 total points, got %v", summary["total_points"])
	}

	if summary["completed_points"] != 2 {
		t.Errorf("Expected 2 completed points, got %v", summary["completed_points"])
	}

	if summary["ready_stories"] != 1 {
		t.Errorf("Expected 1 ready story, got %v", summary["ready_stories"])
	}
}

// Helper function to create a temporary directory for tests.
