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

	story.SetStatus(StatusFailed)
	if story.GetStatus() != StatusFailed {
		t.Errorf("Story status should be '%s' after SetStatus, got '%s'", StatusFailed, story.GetStatus())
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
