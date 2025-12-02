package architect

import (
	"testing"
	"time"

	"orchestrator/pkg/persistence"
)

func TestMaintenanceTracker_InitialState(t *testing.T) {
	tracker := MaintenanceTracker{}

	if tracker.InProgress {
		t.Error("expected InProgress to be false initially")
	}
	if tracker.SpecsCompleted != 0 {
		t.Error("expected SpecsCompleted to be 0 initially")
	}
	if tracker.CurrentCycleID != "" {
		t.Error("expected CurrentCycleID to be empty initially")
	}
	if len(tracker.CompletedSpecIDs) != 0 {
		t.Error("expected CompletedSpecIDs to be empty initially")
	}
}

func TestMaintenanceStatus_Copy(t *testing.T) {
	testTime := time.Now()
	tracker := MaintenanceTracker{
		InProgress:      true,
		SpecsCompleted:  3,
		CurrentCycleID:  "test-cycle",
		LastMaintenance: testTime,
	}

	// Create a status copy (simulating GetMaintenanceStatus)
	status := MaintenanceStatus{
		InProgress:      tracker.InProgress,
		CurrentCycleID:  tracker.CurrentCycleID,
		SpecsCompleted:  tracker.SpecsCompleted,
		LastMaintenance: tracker.LastMaintenance,
	}

	if status.InProgress != true {
		t.Error("expected InProgress to be true")
	}
	if status.SpecsCompleted != 3 {
		t.Errorf("expected SpecsCompleted to be 3, got %d", status.SpecsCompleted)
	}
	if status.CurrentCycleID != "test-cycle" {
		t.Errorf("expected CurrentCycleID to be 'test-cycle', got %s", status.CurrentCycleID)
	}
	if !status.LastMaintenance.Equal(testTime) {
		t.Errorf("expected LastMaintenance to be %v, got %v", testTime, status.LastMaintenance)
	}
}

func TestQueue_CheckSpecComplete_NoStories(t *testing.T) {
	q := NewQueue(nil)

	// Empty queue should return false - spec can't be complete if it never existed
	if q.CheckSpecComplete("nonexistent-spec") {
		t.Error("expected CheckSpecComplete to return false for non-existent spec")
	}
}

func TestQueue_CheckSpecComplete_AllDone(t *testing.T) {
	q := NewQueue(nil)

	// Add some stories all in done status
	q.stories["story-1"] = &QueuedStory{
		Story: persistence.Story{
			ID:     "story-1",
			SpecID: "spec-1",
			Status: string(StatusDone),
		},
	}
	q.stories["story-2"] = &QueuedStory{
		Story: persistence.Story{
			ID:     "story-2",
			SpecID: "spec-1",
			Status: string(StatusDone),
		},
	}

	if !q.CheckSpecComplete("spec-1") {
		t.Error("expected CheckSpecComplete to return true when all stories are done")
	}
}

func TestQueue_CheckSpecComplete_NotAllDone(t *testing.T) {
	q := NewQueue(nil)

	// Add stories with mixed statuses
	q.stories["story-1"] = &QueuedStory{
		Story: persistence.Story{
			ID:     "story-1",
			SpecID: "spec-1",
			Status: string(StatusDone),
		},
	}
	q.stories["story-2"] = &QueuedStory{
		Story: persistence.Story{
			ID:     "story-2",
			SpecID: "spec-1",
			Status: string(StatusCoding), // Not done
		},
	}

	if q.CheckSpecComplete("spec-1") {
		t.Error("expected CheckSpecComplete to return false when not all stories are done")
	}
}

func TestQueue_CheckSpecComplete_DifferentSpecs(t *testing.T) {
	q := NewQueue(nil)

	// Add stories for different specs
	q.stories["story-1"] = &QueuedStory{
		Story: persistence.Story{
			ID:     "story-1",
			SpecID: "spec-1",
			Status: string(StatusDone),
		},
	}
	q.stories["story-2"] = &QueuedStory{
		Story: persistence.Story{
			ID:     "story-2",
			SpecID: "spec-2",
			Status: string(StatusCoding), // Different spec, not done
		},
	}

	// spec-1 should be complete (only has story-1 which is done)
	if !q.CheckSpecComplete("spec-1") {
		t.Error("expected spec-1 to be complete")
	}

	// spec-2 should NOT be complete
	if q.CheckSpecComplete("spec-2") {
		t.Error("expected spec-2 to NOT be complete")
	}
}

func TestQueue_GetSpecStoryCount(t *testing.T) {
	q := NewQueue(nil)

	// Add stories
	q.stories["story-1"] = &QueuedStory{
		Story: persistence.Story{
			ID:     "story-1",
			SpecID: "spec-1",
			Status: string(StatusDone),
		},
	}
	q.stories["story-2"] = &QueuedStory{
		Story: persistence.Story{
			ID:     "story-2",
			SpecID: "spec-1",
			Status: string(StatusCoding),
		},
	}
	q.stories["story-3"] = &QueuedStory{
		Story: persistence.Story{
			ID:     "story-3",
			SpecID: "spec-1",
			Status: string(StatusDone),
		},
	}

	total, completed := q.GetSpecStoryCount("spec-1")

	if total != 3 {
		t.Errorf("expected total to be 3, got %d", total)
	}
	if completed != 2 {
		t.Errorf("expected completed to be 2, got %d", completed)
	}
}

func TestQueue_GetSpecStoryCount_NonExistent(t *testing.T) {
	q := NewQueue(nil)

	total, completed := q.GetSpecStoryCount("nonexistent-spec")

	if total != 0 {
		t.Errorf("expected total to be 0, got %d", total)
	}
	if completed != 0 {
		t.Errorf("expected completed to be 0, got %d", completed)
	}
}

func TestQueue_GetUniqueSpecIDs(t *testing.T) {
	q := NewQueue(nil)

	// Add stories with different spec IDs
	q.stories["story-1"] = &QueuedStory{
		Story: persistence.Story{
			ID:     "story-1",
			SpecID: "spec-1",
		},
	}
	q.stories["story-2"] = &QueuedStory{
		Story: persistence.Story{
			ID:     "story-2",
			SpecID: "spec-2",
		},
	}
	q.stories["story-3"] = &QueuedStory{
		Story: persistence.Story{
			ID:     "story-3",
			SpecID: "spec-1", // Duplicate
		},
	}

	specIDs := q.GetUniqueSpecIDs()

	if len(specIDs) != 2 {
		t.Errorf("expected 2 unique spec IDs, got %d", len(specIDs))
	}

	// Check both specs are present
	found := make(map[string]bool)
	for _, id := range specIDs {
		found[id] = true
	}
	if !found["spec-1"] || !found["spec-2"] {
		t.Errorf("expected spec-1 and spec-2, got %v", specIDs)
	}
}

func TestQueue_GetUniqueSpecIDs_Empty(t *testing.T) {
	q := NewQueue(nil)

	specIDs := q.GetUniqueSpecIDs()

	if len(specIDs) != 0 {
		t.Errorf("expected 0 spec IDs, got %d", len(specIDs))
	}
}

func TestQueue_AddMaintenanceStory(t *testing.T) {
	q := NewQueue(nil)

	q.AddMaintenanceStory(
		"maint-story-1",
		"maint-spec-1",
		"Test Maintenance Story",
		"This is a test maintenance story",
		true, // express
		true, // isMaintenance
	)

	// Verify story was added
	story, exists := q.stories["maint-story-1"]
	if !exists {
		t.Fatal("expected story to be added to queue")
	}

	if story.ID != "maint-story-1" {
		t.Errorf("expected ID 'maint-story-1', got '%s'", story.ID)
	}
	if story.SpecID != "maint-spec-1" {
		t.Errorf("expected SpecID 'maint-spec-1', got '%s'", story.SpecID)
	}
	if story.Title != "Test Maintenance Story" {
		t.Errorf("expected Title 'Test Maintenance Story', got '%s'", story.Title)
	}
	if story.Content != "This is a test maintenance story" {
		t.Errorf("unexpected Content: %s", story.Content)
	}
	if !story.Express {
		t.Error("expected Express to be true")
	}
	if !story.IsMaintenance {
		t.Error("expected IsMaintenance to be true")
	}
	if story.StoryType != "maintenance" {
		t.Errorf("expected StoryType 'maintenance', got '%s'", story.StoryType)
	}
	if story.GetStatus() != StatusPending {
		t.Errorf("expected status StatusPending, got %s", story.GetStatus())
	}
}

func TestQueue_AddMaintenanceStory_NotExpress(t *testing.T) {
	q := NewQueue(nil)

	q.AddMaintenanceStory(
		"maint-story-2",
		"maint-spec-1",
		"Non-Express Story",
		"Content",
		false, // express
		true,  // isMaintenance
	)

	story := q.stories["maint-story-2"]
	if story.Express {
		t.Error("expected Express to be false")
	}
}

func TestQueue_AddMaintenanceStory_Timestamps(t *testing.T) {
	q := NewQueue(nil)

	before := time.Now()
	q.AddMaintenanceStory(
		"maint-story-3",
		"maint-spec-1",
		"Timestamp Test",
		"Content",
		false,
		true,
	)
	after := time.Now()

	story := q.stories["maint-story-3"]

	if story.CreatedAt.Before(before) || story.CreatedAt.After(after) {
		t.Error("CreatedAt timestamp is not within expected range")
	}
	if story.LastUpdated.Before(before) || story.LastUpdated.After(after) {
		t.Error("LastUpdated timestamp is not within expected range")
	}
}

func TestProgrammaticReport(t *testing.T) {
	report := &ProgrammaticReport{
		BranchesDeleted: []string{"feature/old-1", "fix/resolved"},
		Errors:          []string{"failed to delete branch xyz"},
	}

	if len(report.BranchesDeleted) != 2 {
		t.Errorf("expected 2 deleted branches, got %d", len(report.BranchesDeleted))
	}
	if len(report.Errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(report.Errors))
	}
}
