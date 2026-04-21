package architect

import (
	"testing"
	"time"

	"orchestrator/pkg/persistence"
)

// newTestQueue creates a Queue with nil persistence channel for testing.
func newTestQueue() *Queue {
	return NewQueue(nil)
}

// addQueueStory adds a story to the queue with minimal fields for testing.
// Uses raw Status field to allow setting terminal statuses (failed, done)
// which SetStatus would reject.
func addQueueStory(q *Queue, id string, status StoryStatus) *QueuedStory {
	now := time.Now()
	story := &QueuedStory{
		Story: persistence.Story{
			ID:          id,
			SpecID:      "spec-1",
			Title:       "Test story " + id,
			Content:     "Test content for " + id,
			Priority:    1,
			LastUpdated: now,
			CreatedAt:   now,
		},
	}
	story.Status = string(status)

	q.mutex.Lock()
	q.stories[id] = story
	q.mutex.Unlock()

	return story
}

// --- RetryFailedStory tests ---

func TestRetryFailedStory_Success(t *testing.T) {
	q := newTestQueue()

	// Create a failed story with some prior state
	story := addQueueStory(q, "story-1", StatusFailed)
	story.AssignedAgent = "coder-001"
	story.ApprovedPlan = "original plan"
	story.AttemptCount = 2
	startTime := time.Now().Add(-time.Hour)
	story.StartedAt = &startTime

	err := q.RetryFailedStory("story-1")
	if err != nil {
		t.Fatalf("RetryFailedStory returned unexpected error: %v", err)
	}

	// Verify the story was reset to pending
	updated, exists := q.GetStory("story-1")
	if !exists {
		t.Fatal("story-1 should still exist in queue")
	}

	if updated.GetStatus() != StatusPending {
		t.Errorf("expected status %q, got %q", StatusPending, updated.GetStatus())
	}

	// AssignedAgent should be cleared
	if updated.AssignedAgent != "" {
		t.Errorf("expected empty AssignedAgent, got %q", updated.AssignedAgent)
	}

	// ApprovedPlan should be cleared
	if updated.ApprovedPlan != "" {
		t.Errorf("expected empty ApprovedPlan, got %q", updated.ApprovedPlan)
	}

	// StartedAt should be cleared
	if updated.StartedAt != nil {
		t.Errorf("expected nil StartedAt, got %v", updated.StartedAt)
	}

	// AttemptCount should be preserved (not reset)
	if updated.AttemptCount != 2 {
		t.Errorf("expected AttemptCount=2 (preserved), got %d", updated.AttemptCount)
	}
}

func TestRetryFailedStory_NotFailed(t *testing.T) {
	q := newTestQueue()
	addQueueStory(q, "story-1", StatusPending)

	err := q.RetryFailedStory("story-1")
	if err == nil {
		t.Fatal("expected error for non-failed story, got nil")
	}

	if got := err.Error(); got == "" {
		t.Error("expected non-empty error message")
	}
}

func TestRetryFailedStory_NotFound(t *testing.T) {
	q := newTestQueue()

	err := q.RetryFailedStory("nonexistent-story")
	if err == nil {
		t.Fatal("expected error for missing story, got nil")
	}

	if got := err.Error(); got == "" {
		t.Error("expected non-empty error message")
	}
}

func TestRetryFailedStory_DoneStory(t *testing.T) {
	q := newTestQueue()
	addQueueStory(q, "story-1", StatusDone)

	err := q.RetryFailedStory("story-1")
	if err == nil {
		t.Fatal("expected error for done story, got nil")
	}
}

// --- RequeueOrphanedDispatched tests ---
// These tests use leasedStoryIDs (the dispatcher's lease table) as the source of truth
// for whether a dispatched story is orphaned, matching the live dispatch flow.

func TestRequeueOrphanedDispatched(t *testing.T) {
	q := newTestQueue()

	// story-1: dispatched, leased to a coder (in lease table)
	addQueueStory(q, "story-1", StatusDispatched)

	// story-2: dispatched, NOT in lease table (orphaned)
	addQueueStory(q, "story-2", StatusDispatched)

	// story-3: pending (should be untouched)
	addQueueStory(q, "story-3", StatusPending)

	// Only story-1 is leased
	leased := map[string]bool{"story-1": true}
	requeued := q.RequeueOrphanedDispatched(leased)

	// Only story-2 should be requeued (orphaned)
	if len(requeued) != 1 {
		t.Fatalf("expected 1 requeued story, got %d: %v", len(requeued), requeued)
	}
	if requeued[0] != "story-2" {
		t.Errorf("expected requeued story to be story-2, got %q", requeued[0])
	}

	// Verify story-2 was reset
	updated2, _ := q.GetStory("story-2")
	if updated2.GetStatus() != StatusPending {
		t.Errorf("story-2: expected status %q, got %q", StatusPending, updated2.GetStatus())
	}
	if updated2.AssignedAgent != "" {
		t.Errorf("story-2: expected empty AssignedAgent, got %q", updated2.AssignedAgent)
	}
	if updated2.ApprovedPlan != "" {
		t.Errorf("story-2: expected empty ApprovedPlan, got %q", updated2.ApprovedPlan)
	}
	if updated2.StartedAt != nil {
		t.Errorf("story-2: expected nil StartedAt, got %v", updated2.StartedAt)
	}

	// Verify story-1 is still dispatched (leased)
	updated1, _ := q.GetStory("story-1")
	if updated1.GetStatus() != StatusDispatched {
		t.Errorf("story-1: expected status %q, got %q", StatusDispatched, updated1.GetStatus())
	}

	// Verify story-3 is still pending (untouched)
	updated3, _ := q.GetStory("story-3")
	if updated3.GetStatus() != StatusPending {
		t.Errorf("story-3: expected status %q, got %q", StatusPending, updated3.GetStatus())
	}
}

func TestRequeueOrphanedDispatched_NoOrphans(t *testing.T) {
	q := newTestQueue()

	addQueueStory(q, "story-1", StatusDispatched)
	addQueueStory(q, "story-2", StatusDispatched)

	// Both stories are leased
	leased := map[string]bool{"story-1": true, "story-2": true}
	requeued := q.RequeueOrphanedDispatched(leased)

	if len(requeued) != 0 {
		t.Errorf("expected 0 requeued stories when all are leased, got %d: %v", len(requeued), requeued)
	}
}

func TestRequeueOrphanedDispatched_AllOrphans(t *testing.T) {
	q := newTestQueue()

	addQueueStory(q, "story-1", StatusDispatched)
	addQueueStory(q, "story-2", StatusDispatched)

	// No leases — all dispatched stories are orphaned
	requeued := q.RequeueOrphanedDispatched(map[string]bool{})

	if len(requeued) != 2 {
		t.Errorf("expected 2 requeued stories when no leases exist, got %d: %v", len(requeued), requeued)
	}

	// Verify both are pending now
	for _, id := range []string{"story-1", "story-2"} {
		story, _ := q.GetStory(id)
		if story.GetStatus() != StatusPending {
			t.Errorf("%s: expected status %q, got %q", id, StatusPending, story.GetStatus())
		}
	}
}

func TestRequeueOrphanedDispatched_SkipsNonDispatched(t *testing.T) {
	q := newTestQueue()

	// Various non-dispatched statuses
	addQueueStory(q, "story-pending", StatusPending)
	addQueueStory(q, "story-coding", StatusCoding)
	addQueueStory(q, "story-done", StatusDone)
	addQueueStory(q, "story-failed", StatusFailed)

	// No leases — but none of these are dispatched so none should be requeued
	requeued := q.RequeueOrphanedDispatched(map[string]bool{})

	if len(requeued) != 0 {
		t.Errorf("expected 0 requeued stories for non-dispatched statuses, got %d: %v", len(requeued), requeued)
	}
}

func TestRequeueOrphanedDispatched_EmptyQueue(t *testing.T) {
	q := newTestQueue()

	requeued := q.RequeueOrphanedDispatched(map[string]bool{"story-1": true})

	if len(requeued) != 0 {
		t.Errorf("expected 0 requeued stories for empty queue, got %d", len(requeued))
	}
}

// --- SkipStory tests ---

func TestSkipStory_FromFailed(t *testing.T) {
	q := newTestQueue()
	story := addQueueStory(q, "story-1", StatusFailed)
	story.AssignedAgent = "coder-001"
	story.AttemptCount = 3
	story.LastFailReason = "build failed"

	if err := q.SkipStory("story-1"); err != nil {
		t.Fatalf("SkipStory failed: %v", err)
	}

	story, _ = q.GetStory("story-1")
	if story.GetStatus() != StatusSkipped {
		t.Errorf("expected status %s, got %s", StatusSkipped, story.GetStatus())
	}
	if story.AssignedAgent != "" {
		t.Errorf("expected AssignedAgent cleared, got %q", story.AssignedAgent)
	}
	if story.AttemptCount != 3 {
		t.Errorf("expected AttemptCount preserved at 3, got %d", story.AttemptCount)
	}
}

func TestSkipStory_FromOnHold(t *testing.T) {
	q := newTestQueue()
	story := addQueueStory(q, "story-1", StatusOnHold)
	story.HoldReason = "blocked by failure"
	story.HoldOwner = "architect"
	story.HoldNote = "needs fix"
	story.BlockedByFailureID = "fail-123"

	if err := q.SkipStory("story-1"); err != nil {
		t.Fatalf("SkipStory failed: %v", err)
	}

	story, _ = q.GetStory("story-1")
	if story.GetStatus() != StatusSkipped {
		t.Errorf("expected status %s, got %s", StatusSkipped, story.GetStatus())
	}
	if story.HoldReason != "" {
		t.Errorf("expected HoldReason cleared, got %q", story.HoldReason)
	}
	if story.HoldOwner != "" {
		t.Errorf("expected HoldOwner cleared, got %q", story.HoldOwner)
	}
	if story.HoldNote != "" {
		t.Errorf("expected HoldNote cleared, got %q", story.HoldNote)
	}
	if story.BlockedByFailureID != "" {
		t.Errorf("expected BlockedByFailureID cleared, got %q", story.BlockedByFailureID)
	}
}

func TestSkipStory_FromPending_Rejected(t *testing.T) {
	q := newTestQueue()
	addQueueStory(q, "story-1", StatusPending)

	err := q.SkipStory("story-1")
	if err == nil {
		t.Fatal("expected error when skipping pending story")
	}
	if story, _ := q.GetStory("story-1"); story.GetStatus() != StatusPending {
		t.Errorf("status should remain pending, got %s", story.GetStatus())
	}
}

func TestSkipStory_NotFound(t *testing.T) {
	q := newTestQueue()

	err := q.SkipStory("nonexistent")
	if err == nil {
		t.Fatal("expected error for missing story")
	}
}

func TestSkipStory_WithNonTerminalDependents_Rejected(t *testing.T) {
	q := newTestQueue()
	addQueueStory(q, "story-a", StatusFailed)
	storyB := addQueueStory(q, "story-b", StatusPending)
	storyB.DependsOn = []string{"story-a"}

	err := q.SkipStory("story-a")
	if err == nil {
		t.Fatal("expected error when skipping story with non-terminal dependents")
	}

	if storyA, _ := q.GetStory("story-a"); storyA.GetStatus() != StatusFailed {
		t.Errorf("story-a status should remain failed, got %s", storyA.GetStatus())
	}
}

func TestSkipStory_WithTerminalDependents_Allowed(t *testing.T) {
	q := newTestQueue()
	addQueueStory(q, "story-a", StatusFailed)
	storyB := addQueueStory(q, "story-b", StatusDone)
	storyB.DependsOn = []string{"story-a"}

	if err := q.SkipStory("story-a"); err != nil {
		t.Fatalf("SkipStory should succeed when dependents are terminal: %v", err)
	}

	if storyA, _ := q.GetStory("story-a"); storyA.GetStatus() != StatusSkipped {
		t.Errorf("expected status skipped, got %s", storyA.GetStatus())
	}
}

// --- GetNonTerminalDependents tests ---

func TestGetNonTerminalDependents(t *testing.T) {
	q := newTestQueue()
	addQueueStory(q, "story-a", StatusFailed)
	storyB := addQueueStory(q, "story-b", StatusPending)
	storyB.DependsOn = []string{"story-a"}
	storyC := addQueueStory(q, "story-c", StatusDone)
	storyC.DependsOn = []string{"story-a"}
	storyD := addQueueStory(q, "story-d", StatusOnHold)
	storyD.DependsOn = []string{"story-a"}

	deps := q.GetNonTerminalDependents("story-a")

	// story-b (pending) and story-d (on_hold) are non-terminal; story-c (done) is terminal
	if len(deps) != 2 {
		t.Fatalf("expected 2 non-terminal dependents, got %d: %+v", len(deps), deps)
	}

	ids := map[string]bool{}
	for _, d := range deps {
		ids[d.ID] = true
	}
	if !ids["story-b"] {
		t.Error("expected story-b in non-terminal dependents")
	}
	if !ids["story-d"] {
		t.Error("expected story-d in non-terminal dependents")
	}
}

func TestGetNonTerminalDependents_SkippedIsTerminal(t *testing.T) {
	q := newTestQueue()
	addQueueStory(q, "story-a", StatusFailed)
	storyB := addQueueStory(q, "story-b", StatusSkipped)
	storyB.DependsOn = []string{"story-a"}

	deps := q.GetNonTerminalDependents("story-a")
	if len(deps) != 0 {
		t.Errorf("expected 0 non-terminal dependents (skipped is terminal), got %d", len(deps))
	}
}

// --- AllStoriesTerminal / AllStoriesCompleted with skipped ---

func TestAllStoriesTerminal_WithSkipped(t *testing.T) {
	q := newTestQueue()
	addQueueStory(q, "s1", StatusDone)
	addQueueStory(q, "s2", StatusSkipped)
	addQueueStory(q, "s3", StatusFailed)

	if !q.AllStoriesTerminal() {
		t.Error("AllStoriesTerminal should be true when all stories are done, failed, or skipped")
	}
}

func TestAllStoriesCompleted_WithSkipped(t *testing.T) {
	q := newTestQueue()
	addQueueStory(q, "s1", StatusDone)
	addQueueStory(q, "s2", StatusSkipped)

	if q.AllStoriesCompleted() {
		t.Error("AllStoriesCompleted should be false when a story is skipped (not done)")
	}
}

// --- SetStatus guard for skipped ---

// --- ResetAllBudgets tests ---

func TestResetAllBudgets(t *testing.T) {
	q := newTestQueue()
	story := addQueueStory(q, "story-1", StatusFailed)
	story.AttemptRetryBudget = 3
	story.RewriteBudget = 2
	story.RepairBudget = 1
	story.HumanBudget = 1

	q.ResetAllBudgets("story-1")

	if story.AttemptRetryBudget != 0 {
		t.Errorf("AttemptRetryBudget: expected 0, got %d", story.AttemptRetryBudget)
	}
	if story.RewriteBudget != 0 {
		t.Errorf("RewriteBudget: expected 0, got %d", story.RewriteBudget)
	}
	if story.RepairBudget != 0 {
		t.Errorf("RepairBudget: expected 0, got %d", story.RepairBudget)
	}
	if story.HumanBudget != 0 {
		t.Errorf("HumanBudget: expected 0, got %d", story.HumanBudget)
	}
}

func TestResetAllBudgets_NotFound(_ *testing.T) {
	q := newTestQueue()
	// Should not panic
	q.ResetAllBudgets("nonexistent")
}

// --- HasHeldStoriesForFailure tests ---

func TestHasHeldStoriesForFailure_True(t *testing.T) {
	q := newTestQueue()
	story := addQueueStory(q, "story-1", StatusOnHold)
	story.BlockedByFailureID = "fail-abc"

	if !q.HasHeldStoriesForFailure("fail-abc") {
		t.Error("expected true when a story is held by the given failure ID")
	}
}

func TestHasHeldStoriesForFailure_False(t *testing.T) {
	q := newTestQueue()
	addQueueStory(q, "story-1", StatusFailed)

	if q.HasHeldStoriesForFailure("fail-abc") {
		t.Error("expected false when no stories are held by the given failure ID")
	}
}

func TestHasHeldStoriesForFailure_DifferentFailureID(t *testing.T) {
	q := newTestQueue()
	story := addQueueStory(q, "story-1", StatusOnHold)
	story.BlockedByFailureID = "fail-xyz"

	if q.HasHeldStoriesForFailure("fail-abc") {
		t.Error("expected false when held story has a different failure ID")
	}
}

// --- SetStatus guard for skipped ---

func TestSetStatus_RejectsFromSkipped(t *testing.T) {
	q := newTestQueue()
	story := addQueueStory(q, "story-1", StatusSkipped)

	if err := story.SetStatus(StatusPending); err == nil {
		t.Error("should not be able to transition from skipped to pending")
	}
	if err := story.SetStatus(StatusDone); err == nil {
		t.Error("should not be able to transition from skipped to done")
	}
	if story.GetStatus() != StatusSkipped {
		t.Errorf("status should remain skipped, got %s", story.GetStatus())
	}
}
