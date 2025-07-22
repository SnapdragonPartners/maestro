package architect

import (
	"context"
	"strings"
	"testing"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/state"
)

// Mock implementations for testing.
type mockLLMClient struct{}

func (m *mockLLMClient) GenerateResponse(_ context.Context, prompt string) (string, error) {
	return "mock response", nil
}

func TestEscalationTimeoutGuard(t *testing.T) {
	// Create test setup.
	stateStore, _ := state.NewStore("test_data")
	mockConfig := &config.ModelCfg{}
	mockLLM := &mockLLMClient{}
	mockOrchestratorConfig := &config.Config{}
	driver := NewDriver("test-architect", stateStore, mockConfig, mockLLM, nil, "test_work", "test_stories", mockOrchestratorConfig)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := driver.Initialize(ctx)
	if err != nil {
		t.Fatalf("Failed to initialize driver: %v", err)
	}
	defer driver.Shutdown(ctx)

	t.Run("Escalation timeout guard - immediate timeout", func(t *testing.T) {
		// Set up a scenario where escalation timeout is already exceeded.
		// For testing, we'll manually set an old escalation timestamp.
		pastTime := time.Now().Add(-8 * 24 * time.Hour) // 8 days ago (exceeds 7 day limit)

		// Manually transition to ESCALATED state with old timestamp.
		err := driver.transitionTo(ctx, StateEscalated, map[string]any{
			"escalated_at": pastTime,
			"reason":       "test_timeout",
		})
		if err != nil {
			t.Fatalf("Failed to transition to ESCALATED state: %v", err)
		}

		// Verify we're in ESCALATED state.
		if driver.GetCurrentState() != StateEscalated {
			t.Errorf("Expected ESCALATED state, got %s", driver.GetCurrentState())
		}

		// Process the ESCALATED state - should timeout and go to ERROR.
		nextState, err := driver.handleEscalated(ctx)
		if err == nil {
			t.Error("Expected timeout error, got nil")
		}
		if nextState != StateError {
			t.Errorf("Expected ERROR state after timeout, got %s", nextState)
		}

		// The error message should mention timeout.
		if err != nil && !contains(err.Error(), "timeout") {
			t.Errorf("Expected timeout error message, got: %v", err)
		}
	})

	t.Run("Escalation timeout guard - fresh escalation", func(t *testing.T) {
		// Reset to a fresh state.
		driver.currentState = StateRequest

		// Transition to ESCALATED state with current timestamp.
		err := driver.transitionTo(ctx, StateEscalated, map[string]any{
			"reason": "test_fresh_escalation",
		})
		if err != nil {
			t.Fatalf("Failed to transition to ESCALATED state: %v", err)
		}

		// Verify we're in ESCALATED state.
		if driver.GetCurrentState() != StateEscalated {
			t.Errorf("Expected ESCALATED state, got %s", driver.GetCurrentState())
		}

		// Process the ESCALATED state - should NOT timeout (fresh escalation).
		nextState, err := driver.handleEscalated(ctx)
		if err != nil {
			t.Errorf("Fresh escalation should not timeout, got error: %v", err)
		}

		// Should stay in ESCALATED or move to REQUEST (depending on pending escalations).
		if nextState != StateEscalated && nextState != StateRequest {
			t.Errorf("Expected ESCALATED or REQUEST state, got %s", nextState)
		}
	})

	t.Run("Escalation timeout guard - no timestamp error", func(t *testing.T) {
		// Manually set state to ESCALATED without escalated_at timestamp.
		driver.currentState = StateEscalated
		driver.stateData = make(map[string]any) // Clear state data

		// Process ESCALATED state without timestamp - should error.
		nextState, err := driver.handleEscalated(ctx)
		if err == nil {
			t.Error("Expected error for missing escalation timestamp, got nil")
		}
		if nextState != StateError {
			t.Errorf("Expected ERROR state for missing timestamp, got %s", nextState)
		}

		// Error should mention missing timestamp.
		if err != nil && !contains(err.Error(), "timestamp") {
			t.Errorf("Expected timestamp error message, got: %v", err)
		}
	})
}

func TestEscalationTimestampRecording(t *testing.T) {
	// Create test setup.
	stateStore, _ := state.NewStore("test_data")
	mockConfig := &config.ModelCfg{}
	mockLLM := &mockLLMClient{}
	mockOrchestratorConfig := &config.Config{}
	driver := NewDriver("test-architect", stateStore, mockConfig, mockLLM, nil, "test_work", "test_stories", mockOrchestratorConfig)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := driver.Initialize(ctx)
	if err != nil {
		t.Fatalf("Failed to initialize driver: %v", err)
	}
	defer driver.Shutdown(ctx)

	t.Run("Escalation timestamp recorded on transition", func(t *testing.T) {
		// Record time before transition.
		beforeTransition := time.Now()

		// Transition to ESCALATED state.
		err := driver.transitionTo(ctx, StateEscalated, map[string]any{
			"reason": "test_timestamp",
		})
		if err != nil {
			t.Fatalf("Failed to transition to ESCALATED state: %v", err)
		}

		// Record time after transition.
		afterTransition := time.Now()

		// Verify escalated_at timestamp was recorded.
		escalatedAt, exists := driver.stateData["escalated_at"].(time.Time)
		if !exists {
			t.Fatal("escalated_at timestamp not recorded in state data")
		}

		// Verify timestamp is reasonable (between before and after).
		if escalatedAt.Before(beforeTransition) || escalatedAt.After(afterTransition) {
			t.Errorf("escalated_at timestamp %v not between %v and %v",
				escalatedAt, beforeTransition, afterTransition)
		}

		// Verify the timestamp is recent (within 1 second).
		timeDiff := time.Since(escalatedAt)
		if timeDiff > time.Second {
			t.Errorf("escalated_at timestamp too old: %v", timeDiff)
		}
	})

	t.Run("Other state transitions don't record escalation timestamp", func(t *testing.T) {
		// Clear state data.
		driver.stateData = make(map[string]any)

		// Transition to a non-ESCALATED state.
		err := driver.transitionTo(ctx, StateMonitoring, map[string]any{
			"reason": "test_non_escalated",
		})
		if err != nil {
			t.Fatalf("Failed to transition to MONITORING state: %v", err)
		}

		// Verify escalated_at timestamp was NOT recorded.
		_, exists := driver.stateData["escalated_at"]
		if exists {
			t.Error("escalated_at timestamp should not be recorded for non-ESCALATED transitions")
		}

		// But other metadata should be recorded.
		currentState, exists := driver.stateData["current_state"]
		if !exists || currentState != StateMonitoring.String() {
			t.Error("Standard transition metadata should still be recorded")
		}
	})
}

func TestEscalationTimeoutLogging(t *testing.T) {
	// Create test setup with logs.
	stateStore, _ := state.NewStore("test_data")
	mockConfig := &config.ModelCfg{}
	mockLLM := &mockLLMClient{}
	mockOrchestratorConfig := &config.Config{}
	driver := NewDriver("test-architect", stateStore, mockConfig, mockLLM, nil, "test_work", "test_stories", mockOrchestratorConfig)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := driver.Initialize(ctx)
	if err != nil {
		t.Fatalf("Failed to initialize driver: %v", err)
	}
	defer driver.Shutdown(ctx)

	t.Run("Timeout logging via escalation handler", func(t *testing.T) {
		// Test the LogTimeout method directly.
		pastTime := time.Now().Add(-8 * 24 * time.Hour)
		duration := time.Since(pastTime)

		err := driver.escalationHandler.LogTimeout(pastTime, duration)
		if err != nil {
			t.Errorf("Failed to log timeout: %v", err)
		}

		// Verify the log entry was created (we can't easily check the file in tests,.
		// but we can verify the method doesn't error).
		t.Log("Timeout logging completed successfully")
	})
}

// Helper function to check if a string contains a substring (case-insensitive).
func contains(str, substr string) bool {
	return len(str) >= len(substr) &&
		(str == substr ||
			len(str) > len(substr) &&
				(strings.Contains(strings.ToLower(str), strings.ToLower(substr))))
}
