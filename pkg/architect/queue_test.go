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
