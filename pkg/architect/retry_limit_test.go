package architect

import (
	"testing"

	"orchestrator/pkg/persistence"
)

func TestMaxStoryAttempts(t *testing.T) {
	// Verify the constant is set to expected value
	if MaxStoryAttempts != 3 {
		t.Errorf("MaxStoryAttempts should be 3, got %d", MaxStoryAttempts)
	}
}

func TestStoryAttemptTracking(t *testing.T) {
	// Create a story with attempt tracking
	story := &QueuedStory{
		Story: persistence.Story{
			ID:           "test-story-001",
			Title:        "Test Story",
			Status:       string(StatusPending),
			AttemptCount: 0,
		},
	}

	// Verify initial state
	if story.AttemptCount != 0 {
		t.Errorf("Initial AttemptCount should be 0, got %d", story.AttemptCount)
	}

	// Simulate failure tracking
	story.AttemptCount++
	story.LastFailReason = "test failure 1"

	if story.AttemptCount != 1 {
		t.Errorf("AttemptCount after first failure should be 1, got %d", story.AttemptCount)
	}
	if story.LastFailReason != "test failure 1" {
		t.Errorf("LastFailReason should be 'test failure 1', got '%s'", story.LastFailReason)
	}

	// Simulate second failure
	story.AttemptCount++
	story.LastFailReason = "test failure 2"

	if story.AttemptCount != 2 {
		t.Errorf("AttemptCount after second failure should be 2, got %d", story.AttemptCount)
	}

	// Verify still under limit
	if story.AttemptCount >= MaxStoryAttempts {
		t.Error("Story should still be under retry limit after 2 attempts")
	}

	// Simulate third failure - should hit limit
	story.AttemptCount++
	story.LastFailReason = "test failure 3"

	if story.AttemptCount < MaxStoryAttempts {
		t.Errorf("AttemptCount (%d) should be >= MaxStoryAttempts (%d) after 3 failures",
			story.AttemptCount, MaxStoryAttempts)
	}
}

func TestStatusFailed(t *testing.T) {
	// Verify StatusFailed constant exists and is correct
	if StatusFailed != "failed" {
		t.Errorf("StatusFailed should be 'failed', got '%s'", StatusFailed)
	}

	// Verify a story can be set to failed status
	story := &QueuedStory{
		Story: persistence.Story{
			ID:     "test-story-002",
			Status: string(StatusPending),
		},
	}

	if err := story.SetStatus(StatusFailed); err != nil {
		t.Errorf("SetStatus should not fail for pending story: %v", err)
	}
	if story.GetStatus() != StatusFailed {
		t.Errorf("Story status should be '%s' after SetStatus, got '%s'", StatusFailed, story.GetStatus())
	}
}

func TestAllStoriesTerminal(t *testing.T) {
	q := NewQueue(nil)

	// Add stories
	q.AddStory("s1", "spec1", "Story 1", "content", "app", nil, 1)
	q.AddStory("s2", "spec1", "Story 2", "content", "app", nil, 1)

	// Initially neither completed nor terminal (both pending)
	if q.AllStoriesCompleted() {
		t.Error("AllStoriesCompleted should be false when stories are pending")
	}
	if q.AllStoriesTerminal() {
		t.Error("AllStoriesTerminal should be false when stories are pending")
	}

	// Mark s1 as done
	s1, _ := q.GetStory("s1")
	_ = s1.SetStatus(StatusDone)

	// Still not all complete or terminal
	if q.AllStoriesCompleted() {
		t.Error("AllStoriesCompleted should be false when only one story is done")
	}
	if q.AllStoriesTerminal() {
		t.Error("AllStoriesTerminal should be false when one story is still pending")
	}

	// Mark s2 as failed
	s2, _ := q.GetStory("s2")
	_ = s2.SetStatus(StatusFailed)

	// AllStoriesCompleted should be false (s2 is failed, not done)
	if q.AllStoriesCompleted() {
		t.Error("AllStoriesCompleted should be false when a story is failed (not done)")
	}
	// AllStoriesTerminal should be true (both done or failed)
	if !q.AllStoriesTerminal() {
		t.Error("AllStoriesTerminal should be true when all stories are done or failed")
	}
}

func TestFailedStoryIsTerminal(t *testing.T) {
	story := &QueuedStory{
		Story: persistence.Story{
			ID:     "test-terminal",
			Status: string(StatusPending),
		},
	}

	// Can transition to failed
	if err := story.SetStatus(StatusFailed); err != nil {
		t.Errorf("Should be able to set pending story to failed: %v", err)
	}

	// Cannot transition from failed to anything
	if err := story.SetStatus(StatusPending); err == nil {
		t.Error("Should not be able to change failed story status")
	}
}

func TestOnHoldStoryLifecycle(t *testing.T) {
	q := NewQueue(nil)
	q.AddStory("s1", "spec1", "Story 1", "content", "app", nil, 1)

	// Hold the story
	if err := q.HoldStory("s1", "blocked by failure", "architect", "fail-123", "needs fix"); err != nil {
		t.Fatalf("HoldStory failed: %v", err)
	}

	s1, _ := q.GetStory("s1")
	if s1.GetStatus() != StatusOnHold {
		t.Errorf("Expected on_hold, got %s", s1.GetStatus())
	}
	if s1.HoldReason != "blocked by failure" {
		t.Errorf("Expected hold reason, got %s", s1.HoldReason)
	}
	if s1.BlockedByFailureID != "fail-123" {
		t.Errorf("Expected failure ID, got %s", s1.BlockedByFailureID)
	}

	// Story should not be ready
	ready := q.GetReadyStories()
	if len(ready) != 0 {
		t.Errorf("On-hold story should not be ready, got %d ready stories", len(ready))
	}

	// AllStoriesTerminal should be false (on_hold is not terminal)
	if q.AllStoriesTerminal() {
		t.Error("AllStoriesTerminal should be false when a story is on_hold")
	}

	// Release the story
	released, err := q.ReleaseHeldStories([]string{"s1"}, "failure resolved")
	if err != nil {
		t.Fatalf("ReleaseHeldStories failed: %v", err)
	}
	if len(released) != 1 || released[0] != "s1" {
		t.Errorf("Expected [s1] released, got %v", released)
	}

	s1, _ = q.GetStory("s1")
	if s1.GetStatus() != StatusPending {
		t.Errorf("Expected pending after release, got %s", s1.GetStatus())
	}
	if s1.HoldReason != "" {
		t.Errorf("Hold metadata should be cleared, got reason=%s", s1.HoldReason)
	}
	if s1.BlockedByFailureID != "" {
		t.Errorf("BlockedByFailureID should be cleared, got %s", s1.BlockedByFailureID)
	}
}

func TestReleaseHeldStoriesByFailure(t *testing.T) {
	q := NewQueue(nil)
	q.AddStory("s1", "spec1", "Story 1", "content", "app", nil, 1)
	q.AddStory("s2", "spec1", "Story 2", "content", "app", nil, 1)
	q.AddStory("s3", "spec1", "Story 3", "content", "app", nil, 1)

	// Hold s1 and s2 with same failure, s3 with different failure
	_ = q.HoldStory("s1", "reason", "architect", "fail-AAA", "")
	_ = q.HoldStory("s2", "reason", "architect", "fail-AAA", "")
	_ = q.HoldStory("s3", "reason", "architect", "fail-BBB", "")

	released, err := q.ReleaseHeldStoriesByFailure("fail-AAA", "resolved")
	if err != nil {
		t.Fatalf("ReleaseHeldStoriesByFailure failed: %v", err)
	}
	if len(released) != 2 {
		t.Errorf("Expected 2 released, got %d", len(released))
	}

	// s3 should still be on hold
	s3, _ := q.GetStory("s3")
	if s3.GetStatus() != StatusOnHold {
		t.Errorf("s3 should still be on_hold, got %s", s3.GetStatus())
	}
}

func TestToDatabaseStatusMapping(t *testing.T) {
	if StatusFailed.ToDatabaseStatus() != persistence.StatusFailed {
		t.Errorf("StatusFailed should map to persistence.StatusFailed, got %s", StatusFailed.ToDatabaseStatus())
	}
	if StatusOnHold.ToDatabaseStatus() != persistence.StatusOnHold {
		t.Errorf("StatusOnHold should map to persistence.StatusOnHold, got %s", StatusOnHold.ToDatabaseStatus())
	}
	if StatusDone.ToDatabaseStatus() != persistence.StatusDone {
		t.Errorf("StatusDone should map to persistence.StatusDone, got %s", StatusDone.ToDatabaseStatus())
	}
}

func TestBudgetIndependence(t *testing.T) {
	q := NewQueue(nil)
	q.AddStory("s1", "spec1", "Story 1", "content", "app", nil, 1)

	// Exhaust attempt budget
	for range MaxAttemptRetries {
		q.IncrementBudget("s1", BudgetClassAttempt)
	}
	if !q.IsBudgetExhausted("s1", BudgetClassAttempt) {
		t.Error("Attempt budget should be exhausted")
	}
	// Rewrite budget should still be available
	if q.IsBudgetExhausted("s1", BudgetClassRewrite) {
		t.Error("Rewrite budget should not be exhausted when only attempt budget is used")
	}

	// Now increment rewrite budget
	count, maxBudget, err := q.IncrementBudget("s1", BudgetClassRewrite)
	if err != nil {
		t.Fatalf("IncrementBudget failed: %v", err)
	}
	if count != 1 || maxBudget != MaxStoryRewrites {
		t.Errorf("Expected count=1, max=%d, got count=%d, max=%d", MaxStoryRewrites, count, maxBudget)
	}
	if q.IsBudgetExhausted("s1", BudgetClassRewrite) {
		t.Error("Rewrite budget should not be exhausted after 1 use")
	}
}

func TestBudgetReconstruction(t *testing.T) {
	q := NewQueue(nil)
	q.AddStory("s1", "spec1", "Story 1", "content", "app", nil, 1)

	// Simulate resume: reconstruct budgets from failure counts
	failureCounts := map[string]int{
		"retry_attempt": 2,
		"rewrite_story": 1,
	}
	q.ReconstructBudgetsFromFailures("s1", failureCounts)

	s1, _ := q.GetStory("s1")
	if s1.AttemptRetryBudget != 2 {
		t.Errorf("Expected attempt budget 2, got %d", s1.AttemptRetryBudget)
	}
	if s1.RewriteBudget != 1 {
		t.Errorf("Expected rewrite budget 1, got %d", s1.RewriteBudget)
	}

	// One more attempt should not exhaust
	if q.IsBudgetExhausted("s1", BudgetClassAttempt) {
		t.Error("Attempt budget should not be exhausted at 2/3")
	}
	// One more increment should exhaust
	q.IncrementBudget("s1", BudgetClassAttempt)
	if !q.IsBudgetExhausted("s1", BudgetClassAttempt) {
		t.Error("Attempt budget should be exhausted at 3/3")
	}
}

func TestRetryLimitCircuitBreaker(t *testing.T) {
	// Test the circuit breaker logic pattern
	testCases := []struct {
		name         string
		attemptCount int
		shouldTrip   bool
		description  string
	}{
		{
			name:         "first_attempt",
			attemptCount: 1,
			shouldTrip:   false,
			description:  "First attempt should not trip circuit breaker",
		},
		{
			name:         "second_attempt",
			attemptCount: 2,
			shouldTrip:   false,
			description:  "Second attempt should not trip circuit breaker",
		},
		{
			name:         "third_attempt_trips",
			attemptCount: 3,
			shouldTrip:   true,
			description:  "Third attempt should trip circuit breaker",
		},
		{
			name:         "fourth_attempt_trips",
			attemptCount: 4,
			shouldTrip:   true,
			description:  "Fourth attempt should also trip (past limit)",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tripped := tc.attemptCount >= MaxStoryAttempts
			if tripped != tc.shouldTrip {
				t.Errorf("%s: expected tripped=%v, got tripped=%v", tc.description, tc.shouldTrip, tripped)
			}
		})
	}
}
