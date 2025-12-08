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

// Integration Tests for Maintenance Mode Workflow

func TestMaintenanceStoryTracking_Initialization(t *testing.T) {
	tracker := MaintenanceTracker{
		StoryResults: make(map[string]*MaintenanceStoryResult),
		Metrics:      MaintenanceMetrics{},
	}

	// Add a story result
	tracker.StoryResults["test-story-1"] = &MaintenanceStoryResult{
		StoryID: "test-story-1",
		Title:   "Test Story",
		Status:  "pending",
	}
	tracker.Metrics.StoriesTotal = 1

	if len(tracker.StoryResults) != 1 {
		t.Errorf("expected 1 story result, got %d", len(tracker.StoryResults))
	}
	if tracker.Metrics.StoriesTotal != 1 {
		t.Errorf("expected StoriesTotal 1, got %d", tracker.Metrics.StoriesTotal)
	}
}

func TestMaintenanceStoryResult_StatusTransitions(t *testing.T) {
	result := &MaintenanceStoryResult{
		StoryID: "test-story",
		Title:   "Test Story",
		Status:  "pending",
	}

	// Verify initial state
	if result.StoryID != "test-story" {
		t.Errorf("expected StoryID 'test-story', got '%s'", result.StoryID)
	}
	if result.Title != "Test Story" {
		t.Errorf("expected Title 'Test Story', got '%s'", result.Title)
	}

	// Transition to in_progress
	result.Status = "in_progress"
	if result.Status != "in_progress" {
		t.Errorf("expected status 'in_progress', got '%s'", result.Status)
	}

	// Transition to completed
	result.Status = "completed"
	completedAt := time.Now()
	result.CompletedAt = completedAt
	result.PRNumber = 42
	result.PRMerged = true
	result.Summary = "Test passed"

	if result.Status != "completed" {
		t.Errorf("expected status 'completed', got '%s'", result.Status)
	}
	if result.PRNumber != 42 {
		t.Errorf("expected PRNumber 42, got %d", result.PRNumber)
	}
	if !result.PRMerged {
		t.Error("expected PRMerged to be true")
	}
	if result.CompletedAt != completedAt {
		t.Error("expected CompletedAt to match")
	}
	if result.Summary != "Test passed" {
		t.Errorf("expected Summary 'Test passed', got '%s'", result.Summary)
	}
}

func TestMaintenanceMetrics_Aggregation(t *testing.T) {
	metrics := MaintenanceMetrics{}

	// Simulate adding stories
	metrics.StoriesTotal = 5

	// Simulate completions and failures
	metrics.StoriesCompleted = 3
	metrics.StoriesFailed = 1
	metrics.PRsMerged = 2
	metrics.BranchesDeleted = 4

	// Verify metrics
	if metrics.StoriesTotal != 5 {
		t.Errorf("expected StoriesTotal 5, got %d", metrics.StoriesTotal)
	}
	if metrics.StoriesCompleted != 3 {
		t.Errorf("expected StoriesCompleted 3, got %d", metrics.StoriesCompleted)
	}
	if metrics.StoriesFailed != 1 {
		t.Errorf("expected StoriesFailed 1, got %d", metrics.StoriesFailed)
	}
	if metrics.PRsMerged != 2 {
		t.Errorf("expected PRsMerged 2, got %d", metrics.PRsMerged)
	}
	if metrics.BranchesDeleted != 4 {
		t.Errorf("expected BranchesDeleted 4, got %d", metrics.BranchesDeleted)
	}

	// Verify invariant: completed + failed + pending = total
	pending := metrics.StoriesTotal - metrics.StoriesCompleted - metrics.StoriesFailed
	if pending != 1 {
		t.Errorf("expected 1 pending story, got %d", pending)
	}
}

func TestMaintenanceCycleComplete_AllDone(t *testing.T) {
	tracker := MaintenanceTracker{
		StoryResults: map[string]*MaintenanceStoryResult{
			"story-1": {StoryID: "story-1", Status: "completed"},
			"story-2": {StoryID: "story-2", Status: "completed"},
			"story-3": {StoryID: "story-3", Status: "failed"},
		},
	}

	// All stories have terminal status (completed or failed)
	allDone := true
	for _, result := range tracker.StoryResults {
		if result.Status == "pending" || result.Status == "in_progress" {
			allDone = false
			break
		}
	}

	if !allDone {
		t.Error("expected all stories to be done")
	}
}

func TestMaintenanceCycleComplete_StillPending(t *testing.T) {
	tracker := MaintenanceTracker{
		StoryResults: map[string]*MaintenanceStoryResult{
			"story-1": {StoryID: "story-1", Status: "completed"},
			"story-2": {StoryID: "story-2", Status: "pending"},
		},
	}

	// Not all stories are done
	allDone := true
	for _, result := range tracker.StoryResults {
		if result.Status == "pending" || result.Status == "in_progress" {
			allDone = false
			break
		}
	}

	if allDone {
		t.Error("expected some stories to still be pending")
	}
}

func TestMaintenanceCycleComplete_InProgress(t *testing.T) {
	tracker := MaintenanceTracker{
		StoryResults: map[string]*MaintenanceStoryResult{
			"story-1": {StoryID: "story-1", Status: "completed"},
			"story-2": {StoryID: "story-2", Status: "in_progress"},
		},
	}

	// Not all stories are done
	allDone := true
	for _, result := range tracker.StoryResults {
		if result.Status == "pending" || result.Status == "in_progress" {
			allDone = false
			break
		}
	}

	if allDone {
		t.Error("expected some stories to still be in progress")
	}
}

func TestMaintenanceCycleComplete_EmptyResults(t *testing.T) {
	tracker := MaintenanceTracker{
		StoryResults: map[string]*MaintenanceStoryResult{},
	}

	// Empty map should NOT count as complete (need at least one story)
	isComplete := len(tracker.StoryResults) > 0
	for _, result := range tracker.StoryResults {
		if result.Status == "pending" || result.Status == "in_progress" {
			isComplete = false
			break
		}
	}

	if isComplete {
		t.Error("expected empty story results to not count as complete")
	}
}

func TestMaintenanceTrackerFull_CycleWorkflow(t *testing.T) {
	// Simulates a full maintenance cycle workflow
	cycleStarted := time.Now().Add(-30 * time.Minute)
	programmaticReport := &ProgrammaticReport{
		BranchesDeleted: []string{"feature/old-1", "feature/old-2"},
		Errors:          nil,
	}
	tracker := MaintenanceTracker{
		InProgress:         true,
		CurrentCycleID:     "maintenance-2024-01-15-120000",
		CycleStartedAt:     cycleStarted,
		StoryResults:       make(map[string]*MaintenanceStoryResult),
		Metrics:            MaintenanceMetrics{},
		ProgrammaticReport: programmaticReport,
	}
	tracker.Metrics.BranchesDeleted = 2

	// Verify initial state
	if tracker.CurrentCycleID != "maintenance-2024-01-15-120000" {
		t.Errorf("expected CycleID 'maintenance-2024-01-15-120000', got '%s'", tracker.CurrentCycleID)
	}
	if tracker.CycleStartedAt != cycleStarted {
		t.Error("CycleStartedAt mismatch")
	}
	if tracker.ProgrammaticReport != programmaticReport {
		t.Error("ProgrammaticReport mismatch")
	}

	// Step 1: Add maintenance stories
	stories := []struct {
		id    string
		title string
	}{
		{"story-knowledge", "Update knowledge.md"},
		{"story-docs", "Verify documentation"},
		{"story-tests", "Run test suite"},
	}

	for _, s := range stories {
		tracker.StoryResults[s.id] = &MaintenanceStoryResult{
			StoryID: s.id,
			Title:   s.title,
			Status:  "pending",
		}
		tracker.Metrics.StoriesTotal++
	}

	if tracker.Metrics.StoriesTotal != 3 {
		t.Errorf("expected 3 total stories, got %d", tracker.Metrics.StoriesTotal)
	}

	// Step 2: Start first story
	tracker.StoryResults["story-knowledge"].Status = "in_progress"

	// Step 3: Complete first story with PR
	tracker.StoryResults["story-knowledge"].Status = "completed"
	tracker.StoryResults["story-knowledge"].PRNumber = 123
	tracker.StoryResults["story-knowledge"].PRMerged = true
	tracker.StoryResults["story-knowledge"].CompletedAt = time.Now()
	tracker.StoryResults["story-knowledge"].Summary = "Updated knowledge.md with new patterns"
	tracker.Metrics.StoriesCompleted++
	tracker.Metrics.PRsMerged++

	// Step 4: Complete second story (no PR)
	tracker.StoryResults["story-docs"].Status = "in_progress"
	tracker.StoryResults["story-docs"].Status = "completed"
	tracker.StoryResults["story-docs"].CompletedAt = time.Now()
	tracker.Metrics.StoriesCompleted++

	// Step 5: Fail third story
	tracker.StoryResults["story-tests"].Status = "in_progress"
	tracker.StoryResults["story-tests"].Status = "failed"
	tracker.StoryResults["story-tests"].CompletedAt = time.Now()
	tracker.Metrics.StoriesFailed++

	// Verify final metrics
	if tracker.Metrics.StoriesCompleted != 2 {
		t.Errorf("expected 2 completed, got %d", tracker.Metrics.StoriesCompleted)
	}
	if tracker.Metrics.StoriesFailed != 1 {
		t.Errorf("expected 1 failed, got %d", tracker.Metrics.StoriesFailed)
	}
	if tracker.Metrics.PRsMerged != 1 {
		t.Errorf("expected 1 PR merged, got %d", tracker.Metrics.PRsMerged)
	}

	// Verify cycle is complete
	allDone := len(tracker.StoryResults) > 0
	for _, result := range tracker.StoryResults {
		if result.Status == "pending" || result.Status == "in_progress" {
			allDone = false
			break
		}
	}
	if !allDone {
		t.Error("expected maintenance cycle to be complete")
	}

	// Complete the cycle
	tracker.InProgress = false
	lastMaint := time.Now()
	tracker.LastMaintenance = lastMaint

	if tracker.InProgress {
		t.Error("expected InProgress to be false after cycle completion")
	}
	if tracker.LastMaintenance != lastMaint {
		t.Error("expected LastMaintenance to be set")
	}
}

func TestMaintenanceStatus_FromTracker(t *testing.T) {
	testTime := time.Now()
	lastMaintTime := testTime.Add(-24 * time.Hour)
	completedSpecs := []string{"spec-1", "spec-2"}
	tracker := MaintenanceTracker{
		InProgress:       true,
		CurrentCycleID:   "maint-123",
		SpecsCompleted:   5,
		LastMaintenance:  lastMaintTime,
		CycleStartedAt:   testTime,
		CompletedSpecIDs: completedSpecs,
		Metrics: MaintenanceMetrics{
			StoriesTotal:     3,
			StoriesCompleted: 2,
			StoriesFailed:    0,
			PRsMerged:        1,
			BranchesDeleted:  5,
		},
	}

	// Verify tracker state includes CompletedSpecIDs
	if len(tracker.CompletedSpecIDs) != 2 {
		t.Errorf("expected 2 completed spec IDs, got %d", len(tracker.CompletedSpecIDs))
	}

	// Create status copy (similar to GetMaintenanceStatus)
	status := MaintenanceStatus{
		InProgress:      tracker.InProgress,
		CurrentCycleID:  tracker.CurrentCycleID,
		SpecsCompleted:  tracker.SpecsCompleted,
		LastMaintenance: tracker.LastMaintenance,
		CycleStartedAt:  tracker.CycleStartedAt,
		Metrics:         tracker.Metrics,
	}

	// Verify all fields are copied correctly
	if status.InProgress != true {
		t.Error("expected InProgress true")
	}
	if status.CurrentCycleID != "maint-123" {
		t.Errorf("expected CycleID 'maint-123', got '%s'", status.CurrentCycleID)
	}
	if status.SpecsCompleted != 5 {
		t.Errorf("expected SpecsCompleted 5, got %d", status.SpecsCompleted)
	}
	if status.LastMaintenance != lastMaintTime {
		t.Error("expected LastMaintenance to match")
	}
	if status.CycleStartedAt != testTime {
		t.Error("expected CycleStartedAt to match")
	}
	if status.Metrics.StoriesTotal != 3 {
		t.Errorf("expected StoriesTotal 3, got %d", status.Metrics.StoriesTotal)
	}
	if status.Metrics.BranchesDeleted != 5 {
		t.Errorf("expected BranchesDeleted 5, got %d", status.Metrics.BranchesDeleted)
	}
}
