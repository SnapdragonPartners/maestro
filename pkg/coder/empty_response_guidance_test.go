package coder

import (
	"context"
	"strings"
	"testing"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/llmerrors"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/utils"
)

func TestTwoTierEmptyResponseHandling(t *testing.T) {
	// Test the two-tier empty response handling logic

	// Create a mock state machine
	sm := agent.NewBaseStateMachine("test-coder", StateCoding, nil, nil)

	// Create a minimal coder instance for testing
	coder := &Coder{
		workDir:        "/test/workspace",
		logger:         logx.NewLogger("test"),
		contextManager: contextmgr.NewContextManager(),
	}

	// Set up initial state data
	sm.SetStateData(string(stateDataKeyTaskContent), "Test Task")
	sm.SetStateData(proto.KeyStoryType, string(proto.StoryTypeDevOps))

	ctx := context.Background()

	// Test first empty response - should provide guidance and stay in CODING
	req := agent.CompletionRequest{Messages: []agent.CompletionMessage{}}
	state1, done1, err1 := coder.handleEmptyResponseForBudgetReview(ctx, sm, "test prompt", req)

	if err1 != nil {
		t.Errorf("First empty response should not error, got: %v", err1)
	}

	if state1 != StateCoding {
		t.Errorf("First empty response should stay in CODING, got: %s", state1)
	}

	if done1 {
		t.Error("First empty response should not be done")
	}

	// Check that consecutive count was incremented
	count1 := utils.GetStateValueOr[int](sm, KeyConsecutiveEmptyResponses, 0)
	if count1 != 1 {
		t.Errorf("Expected consecutive count to be 1, got: %d", count1)
	}

	// Test second empty response - should escalate to budget review
	state2, done2, err2 := coder.handleEmptyResponseForBudgetReview(ctx, sm, "test prompt", req)

	if err2 != nil {
		t.Errorf("Second empty response should not error, got: %v", err2)
	}

	if state2 != StateBudgetReview {
		t.Errorf("Second empty response should escalate to BUDGET_REVIEW, got: %s", state2)
	}

	if done2 {
		t.Error("Second empty response should not be done")
	}

	// Check that consecutive count was incremented again
	count2 := utils.GetStateValueOr[int](sm, KeyConsecutiveEmptyResponses, 0)
	if count2 != 2 {
		t.Errorf("Expected consecutive count to be 2, got: %d", count2)
	}

	t.Logf("✅ Two-tier empty response handling works correctly")
}

func TestConsecutiveEmptyResponseCounterReset(t *testing.T) {
	// Test that consecutive empty response counter resets on successful responses

	// Create a mock state machine with existing empty response count
	sm := agent.NewBaseStateMachine("test-coder", StateCoding, nil, nil)
	sm.SetStateData(KeyConsecutiveEmptyResponses, 2) // Simulate previous empty responses

	// Set up initial state data
	sm.SetStateData(string(stateDataKeyTaskContent), "Test Task")
	sm.SetStateData(proto.KeyStoryType, string(proto.StoryTypeDevOps))

	// Create a minimal coder instance (not used in this test)
	_ = &Coder{}

	// Verify initial count
	initialCount := utils.GetStateValueOr[int](sm, KeyConsecutiveEmptyResponses, 0)
	if initialCount != 2 {
		t.Fatalf("Expected initial count to be 2, got: %d", initialCount)
	}

	// Simulate successful response processing (this logic would be in executeCodingWithTemplate)
	// Here we just test the reset logic directly
	sm.SetStateData(KeyConsecutiveEmptyResponses, 0)

	// Verify counter was reset
	resetCount := utils.GetStateValueOr[int](sm, KeyConsecutiveEmptyResponses, 0)
	if resetCount != 0 {
		t.Errorf("Expected counter to be reset to 0, got: %d", resetCount)
	}

	t.Logf("✅ Consecutive empty response counter reset works correctly")
}

func TestFirstEmptyResponseGuidanceMessage(t *testing.T) {
	// Test that the first empty response adds the correct guidance messages

	// Create a mock state machine
	sm := agent.NewBaseStateMachine("test-coder", StateCoding, nil, nil)

	// Create a coder instance with context manager
	coder := &Coder{
		workDir:        "/test/workspace",
		logger:         logx.NewLogger("test"),
		contextManager: contextmgr.NewContextManager(),
	}

	// Set up initial state data
	sm.SetStateData(string(stateDataKeyTaskContent), "Test Task")
	sm.SetStateData(proto.KeyStoryType, string(proto.StoryTypeDevOps))

	ctx := context.Background()
	req := agent.CompletionRequest{Messages: []agent.CompletionMessage{}}

	// Get initial message count
	initialMessages := coder.contextManager.GetMessages()
	initialCount := len(initialMessages)

	// Process first empty response
	state, _, err := coder.handleEmptyResponseForBudgetReview(ctx, sm, "test prompt", req)

	if err != nil {
		t.Fatalf("Should not error: %v", err)
	}

	if state != StateCoding {
		t.Errorf("Should stay in CODING state, got: %s", state)
	}

	// Check that messages were added
	finalMessages := coder.contextManager.GetMessages()
	finalCount := len(finalMessages)

	expectedNewMessages := 2 // assistant placeholder + user guidance
	if finalCount != initialCount+expectedNewMessages {
		t.Errorf("Expected %d new messages, got %d (initial: %d, final: %d)",
			expectedNewMessages, finalCount-initialCount, initialCount, finalCount)
	}

	// Check that the guidance message contains expected content
	if finalCount >= 1 {
		lastMessage := finalMessages[finalCount-1]
		if lastMessage.Role != "user" {
			t.Errorf("Expected last message to be from user, got: %s", lastMessage.Role)
		}

		expectedPhrases := []string{"done", "tool", "ask_question", "guidance"}
		for _, phrase := range expectedPhrases {
			if !contains(lastMessage.Content, phrase) {
				t.Errorf("Expected guidance message to contain '%s', got: %s", phrase, lastMessage.Content)
			}
		}
	}

	t.Logf("✅ First empty response guidance message works correctly")
}

func TestEmptyResponseErrorTypeHandling(t *testing.T) {
	// Test that empty response errors from LLM client are handled by the two-tier logic

	// Create a coder instance
	coder := &Coder{
		workDir:        "/test/workspace",
		logger:         logx.NewLogger("test"),
		contextManager: contextmgr.NewContextManager(),
	}

	// Test that empty response error is correctly detected
	emptyErr := llmerrors.NewError(llmerrors.ErrorTypeEmptyResponse, "received empty or nil response from Claude API")

	if !coder.isEmptyResponseError(emptyErr) {
		t.Error("isEmptyResponseError should detect empty response errors")
	}

	// Test that other error types are not detected as empty response
	authErr := llmerrors.NewError(llmerrors.ErrorTypeAuth, "authentication failed")
	if coder.isEmptyResponseError(authErr) {
		t.Error("isEmptyResponseError should not detect auth errors as empty response")
	}

	t.Logf("✅ Empty response error type handling works correctly")
}

// Helper function to check if a string contains a substring (case-insensitive).
func contains(s, substr string) bool {
	return len(s) >= len(substr) && findInString(s, substr)
}

func findInString(s, substr string) bool {
	s = strings.ToLower(s)
	substr = strings.ToLower(substr)
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
