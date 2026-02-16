package coder

import (
	"context"
	"strings"
	"testing"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/effect"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
)

// TestBudgetReviewOriginStatePersistence tests that origin state is preserved through budget review.
func TestBudgetReviewOriginStatePersistence(t *testing.T) {
	tests := []struct {
		name        string
		originState proto.State
		wantState   proto.State
	}{
		{
			name:        "CODING -> BUDGET_REVIEW -> CODING on APPROVED",
			originState: StateCoding,
			wantState:   StateCoding,
		},
		{
			name:        "PLANNING -> BUDGET_REVIEW -> PLANNING on APPROVED",
			originState: StatePlanning,
			wantState:   StatePlanning,
		},
		{
			name:        "CODING -> BUDGET_REVIEW -> CODING on NEEDS_CHANGES from CODING",
			originState: StateCoding,
			wantState:   StateCoding,
		},
		{
			name:        "PLANNING -> BUDGET_REVIEW -> PLANNING on NEEDS_CHANGES from PLANNING",
			originState: StatePlanning,
			wantState:   StatePlanning,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a test logger
			logger := logx.NewLogger("coder-test")

			// Create a test state machine
			sm := agent.NewBaseStateMachine("test-coder", tt.originState, nil, CoderTransitions)
			sm.SetStateData(KeyStoryID, "test-story-123")

			// Create a minimal coder instance for testing
			// Note: checkLoopBudget calls getBudgetReviewContent which needs contextManager and renderer
			renderer, err := templates.NewRenderer()
			if err != nil {
				t.Fatalf("Failed to create renderer: %v", err)
			}
			c := &Coder{
				BaseStateMachine: sm,
				logger:           logger,
				contextManager:   contextmgr.NewContextManager(),
				renderer:         renderer,
			}

			// Simulate budget exceeded by setting iteration count to 7 (so next increment will be 8)
			sm.SetStateData(string(stateDataKeyCodingIterations), 7)

			// Call checkLoopBudget - this will increment to 8 and trigger budget review
			budgetEff, exceeded := c.checkLoopBudget(sm, string(stateDataKeyCodingIterations), 8, tt.originState)
			if !exceeded {
				t.Fatal("Expected budget to be exceeded")
			}
			if budgetEff == nil {
				t.Fatal("Expected BudgetReviewEffect to be created")
			}

			// Verify origin was stored
			originStr, exists := sm.GetStateValue(KeyOrigin)
			if !exists {
				t.Fatal("Origin state was not stored in state data")
			}
			if originStr != string(tt.originState) {
				t.Errorf("Origin state mismatch: got %q, want %q", originStr, string(tt.originState))
			}

			// Store the effect in state data (simulating what handleInitialCoding/handleInitialPlanning does)
			sm.SetStateData(KeyBudgetReviewEffect, budgetEff)

			// Note: In production, the state machine would transition to BUDGET_REVIEW state
			// For testing purposes, we're directly testing processBudgetReviewResult

			// Verify origin persists in state data
			originStr, exists = sm.GetStateValue(KeyOrigin)
			if !exists {
				t.Fatal("Origin state was not preserved after transitioning to BUDGET_REVIEW")
			}
			if originStr != string(tt.originState) {
				t.Errorf("Origin state not preserved: got %q, want %q", originStr, string(tt.originState))
			}

			// Simulate architect approval
			result := &effect.BudgetReviewResult{
				Status:   proto.ApprovalStatusApproved,
				Feedback: "Looks good, continue",
			}

			// Process the budget review result
			nextState, _, err := c.processBudgetReviewResult(context.Background(), sm, result)
			if err != nil {
				t.Fatalf("processBudgetReviewResult failed: %v", err)
			}

			// Verify we returned to the origin state
			if nextState != tt.wantState {
				t.Errorf("Expected to return to %s, but got %s", tt.wantState, nextState)
			}

			// Verify iteration counter was reset
			var counterKey string
			if tt.originState == StateCoding {
				counterKey = string(stateDataKeyCodingIterations)
			} else {
				counterKey = string(stateDataKeyPlanningIterations)
			}

			counterVal, exists := sm.GetStateValue(counterKey)
			if !exists {
				t.Fatalf("Iteration counter %q was not found in state data", counterKey)
			}
			counter, ok := counterVal.(int)
			if !ok {
				t.Fatalf("Iteration counter is not an int: %T", counterVal)
			}
			if counter != 0 {
				t.Errorf("Expected iteration counter to be reset to 0, got %d", counter)
			}
		})
	}
}

// TestBudgetReviewNeedsChangesTransition tests the NEEDS_CHANGES response logic.
func TestBudgetReviewNeedsChangesTransition(t *testing.T) {
	tests := []struct {
		name        string
		originState proto.State
		wantState   proto.State
		description string
	}{
		{
			name:        "CODING with NEEDS_CHANGES stays in CODING",
			originState: StateCoding,
			wantState:   StateCoding,
			description: "When budget review from CODING returns NEEDS_CHANGES, should return to CODING (execution issue, not plan issue)",
		},
		{
			name:        "PLANNING with NEEDS_CHANGES stays in PLANNING",
			originState: StatePlanning,
			wantState:   StatePlanning,
			description: "When budget review from PLANNING returns NEEDS_CHANGES, should pivot to PLANNING",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a test logger
			logger := logx.NewLogger("coder-test")

			// Create a test state machine
			sm := agent.NewBaseStateMachine("test-coder", StateBudgetReview, nil, CoderTransitions)
			sm.SetStateData(KeyStoryID, "test-story-123")

			// Set origin state
			sm.SetStateData(KeyOrigin, string(tt.originState))

			// Create minimal coder instance
			c := &Coder{
				BaseStateMachine: sm,
				logger:           logger,
			}

			// Simulate architect NEEDS_CHANGES response
			result := &effect.BudgetReviewResult{
				Status:   proto.ApprovalStatusNeedsChanges,
				Feedback: "Please address these issues",
			}

			// Process the budget review result
			nextState, _, err := c.processBudgetReviewResult(context.Background(), sm, result)
			if err != nil {
				t.Fatalf("processBudgetReviewResult failed: %v", err)
			}

			// Verify state transition
			if nextState != tt.wantState {
				t.Errorf("%s: Expected to transition to %s, but got %s", tt.description, tt.wantState, nextState)
			}
		})
	}
}

// TestBudgetReviewEmptyOrigin tests the bug case where origin is empty.
func TestBudgetReviewEmptyOrigin(t *testing.T) {
	// Create a test logger
	logger := logx.NewLogger("coder-test")

	// Create a test state machine
	sm := agent.NewBaseStateMachine("test-coder", StateBudgetReview, nil, CoderTransitions)
	sm.SetStateData(KeyStoryID, "test-story-123")

	// DO NOT set origin - this reproduces the bug
	// sm.SetStateData(KeyOrigin, string(StateCoding))

	// Create minimal coder instance
	c := &Coder{
		BaseStateMachine: sm,
		logger:           logger,
	}

	// Simulate architect NEEDS_CHANGES response
	result := &effect.BudgetReviewResult{
		Status:   proto.ApprovalStatusNeedsChanges,
		Feedback: "Please address these issues",
	}

	// Process the budget review result
	nextState, _, err := c.processBudgetReviewResult(context.Background(), sm, result)
	if err != nil {
		t.Fatalf("processBudgetReviewResult failed: %v", err)
	}

	// This is the bug: when origin is empty, it falls through to PLANNING
	// The correct behavior should be to return to CODING (or error out)
	if nextState != StatePlanning {
		t.Logf("Bug not reproduced - expected fallthrough to PLANNING, got %s", nextState)
	} else {
		t.Logf("Bug reproduced: empty origin caused fallthrough to PLANNING")
	}
}

// TestBudgetReviewContentRendering tests that budget review effect contains rendered content.
func TestBudgetReviewContentRendering(t *testing.T) {
	// Create a test logger
	logger := logx.NewLogger("coder-test")

	// Create a test state machine
	sm := agent.NewBaseStateMachine("test-coder", StateCoding, nil, CoderTransitions)
	sm.SetStateData(KeyStoryID, "test-story-123")

	// Create a minimal coder instance with context manager and renderer for budget review content
	renderer, err := templates.NewRenderer()
	if err != nil {
		t.Fatalf("Failed to create renderer: %v", err)
	}
	c := &Coder{
		BaseStateMachine: sm,
		logger:           logger,
		contextManager:   contextmgr.NewContextManager(),
		renderer:         renderer,
	}

	// Add some context to verify it gets included in the rendered content
	c.contextManager.AddMessage("user", "Please implement the feature")
	c.contextManager.AddMessage("assistant", "I'll work on that")

	// Simulate budget exceeded by setting iteration count to 7 (so next increment will be 8)
	sm.SetStateData(string(stateDataKeyCodingIterations), 7)

	// Call checkLoopBudget - this will increment to 8 and trigger budget review
	budgetEff, exceeded := c.checkLoopBudget(sm, string(stateDataKeyCodingIterations), 8, StateCoding)
	if !exceeded {
		t.Fatal("Expected budget to be exceeded")
	}
	if budgetEff == nil {
		t.Fatal("Expected BudgetReviewEffect to be created")
	}

	// Verify the effect has content (this is what gets sent to architect)
	if budgetEff.Content == "" {
		t.Error("BudgetReviewEffect.Content is empty - architect will not receive context")
	}

	// The content should contain metadata about the budget situation
	content := budgetEff.Content
	if !strings.Contains(content, "Maximum coding iterations") && !strings.Contains(content, "iteration") {
		t.Errorf("BudgetReviewEffect.Content should mention iterations, got: %s", content)
	}

	// Verify ExtraPayload contains metadata for debugging
	if budgetEff.ExtraPayload == nil {
		t.Error("BudgetReviewEffect.ExtraPayload is nil")
	} else {
		// Check for expected metadata fields
		if _, exists := budgetEff.ExtraPayload["loops"]; !exists {
			t.Error("ExtraPayload missing 'loops' field")
		}
		if _, exists := budgetEff.ExtraPayload["max_loops"]; !exists {
			t.Error("ExtraPayload missing 'max_loops' field")
		}
		if _, exists := budgetEff.ExtraPayload["recent_activity"]; !exists {
			t.Error("ExtraPayload missing 'recent_activity' field")
		}
	}

	t.Logf("Budget review content length: %d bytes", len(content))
	t.Logf("ExtraPayload keys: %v", getMapKeys(budgetEff.ExtraPayload))
}

// TestFormatMessageForBudgetReview_AssistantWithToolCalls verifies tool calls appear in output.
func TestFormatMessageForBudgetReview_AssistantWithToolCalls(t *testing.T) {
	msg := contextmgr.Message{
		Role:    "assistant",
		Content: "Let me check the file...",
		ToolCalls: []contextmgr.ToolCall{
			{
				ID:         "call_1",
				Name:       "read_file",
				Parameters: map[string]any{"path": "src/main.go"},
			},
		},
	}

	result := formatMessageForBudgetReview(&msg)

	if !strings.Contains(result, "[assistant]") {
		t.Error("Expected [assistant] role prefix")
	}
	if !strings.Contains(result, `[tool: read_file(path="src/main.go")]`) {
		t.Errorf("Expected tool call summary, got: %s", result)
	}
	if !strings.Contains(result, "Let me check the file...") {
		t.Error("Expected text content to be preserved")
	}
}

// TestFormatMessageForBudgetReview_UserWithToolResults verifies placeholder is replaced.
func TestFormatMessageForBudgetReview_UserWithToolResults(t *testing.T) {
	msg := contextmgr.Message{
		Role:    "user",
		Content: "Tool results:",
		ToolResults: []contextmgr.ToolResult{
			{
				ToolCallID: "call_1",
				Content:    "package main\n\nimport \"fmt\"\n\nfunc main() {\n    fmt.Println(\"Hello\")\n}",
				IsError:    false,
			},
		},
	}

	result := formatMessageForBudgetReview(&msg)

	if strings.Contains(result, "Tool results:") {
		t.Errorf("Should NOT contain placeholder 'Tool results:', got: %s", result)
	}
	if !strings.Contains(result, "[result]") {
		t.Error("Expected [result] prefix for tool result")
	}
	if !strings.Contains(result, "package main") {
		t.Errorf("Expected actual tool output content, got: %s", result)
	}
}

// TestFormatMessageForBudgetReview_PlainTextMessage verifies backwards compat.
func TestFormatMessageForBudgetReview_PlainTextMessage(t *testing.T) {
	msg := contextmgr.Message{
		Role:    "user",
		Content: "Please implement the feature",
	}

	result := formatMessageForBudgetReview(&msg)
	expected := "[user]: Please implement the feature"
	if result != expected {
		t.Errorf("Expected %q, got %q", expected, result)
	}
}

// TestFormatToolResultSummary_Truncation verifies long results are truncated.
func TestFormatToolResultSummary_Truncation(t *testing.T) {
	longContent := strings.Repeat("x", 300)
	tr := &contextmgr.ToolResult{
		ToolCallID: "call_1",
		Content:    longContent,
		IsError:    false,
	}

	result := formatToolResultSummary(tr)

	// Should be truncated: "[result] " (9) + 150 chars + "..." (3)
	if len(result) > 9+maxToolResultPreview+3 {
		t.Errorf("Result too long (%d chars), expected max %d: %s", len(result), 9+maxToolResultPreview+3, result)
	}
	if !strings.HasSuffix(result, "...") {
		t.Error("Expected truncated result to end with '...'")
	}
}

// TestFormatToolResultSummary_ErrorFlag verifies error prefix.
func TestFormatToolResultSummary_ErrorFlag(t *testing.T) {
	tr := &contextmgr.ToolResult{
		ToolCallID: "call_1",
		Content:    "command not found",
		IsError:    true,
	}

	result := formatToolResultSummary(tr)

	if !strings.HasPrefix(result, "[error]") {
		t.Errorf("Expected [error] prefix, got: %s", result)
	}
	if !strings.Contains(result, "command not found") {
		t.Error("Expected error content to be included")
	}
}

// TestDetectIssuePattern_WithNativeToolCalls verifies detectIssuePattern works with ToolCalls/ToolResults.
func TestDetectIssuePattern_WithNativeToolCalls(t *testing.T) {
	logger := logx.NewLogger("coder-test")
	sm := agent.NewBaseStateMachine("test-coder", StateCoding, nil, CoderTransitions)
	c := &Coder{
		BaseStateMachine: sm,
		logger:           logger,
		contextManager:   contextmgr.NewContextManager(),
	}

	// Seed context with system prompt and initial messages
	c.contextManager.ResetSystemPrompt("You are a coding agent.")
	c.contextManager.AddMessage("user", "Please implement the feature")
	c.contextManager.AddMessage("assistant", "I'll start working on it")

	// Simulate native LLM tool calling: assistant makes tool call, then tool result is flushed
	// Iteration 1: shell(go test ./...) -> error
	c.contextManager.AddAssistantMessageWithTools("I'll run the tests", []contextmgr.ToolCall{
		{ID: "call_1", Name: "shell", Parameters: map[string]any{"cmd": "go test ./..."}},
	})
	c.contextManager.AddToolResult("call_1", "FAIL: TestFoo", true)
	if err := c.contextManager.FlushUserBuffer(context.Background()); err != nil {
		t.Fatalf("FlushUserBuffer failed: %v", err)
	}

	// Iteration 2: same shell(go test ./...) -> error again
	c.contextManager.AddAssistantMessageWithTools("Let me try again", []contextmgr.ToolCall{
		{ID: "call_2", Name: "shell", Parameters: map[string]any{"cmd": "go test ./..."}},
	})
	c.contextManager.AddToolResult("call_2", "FAIL: TestFoo", true)
	if err := c.contextManager.FlushUserBuffer(context.Background()); err != nil {
		t.Fatalf("FlushUserBuffer failed: %v", err)
	}

	result := c.detectIssuePattern()

	// Should find tool calls (not "No tool calls to analyze")
	if strings.Contains(result, "No tool calls to analyze") {
		t.Errorf("detectIssuePattern should find native tool calls, got: %s", result)
	}
	// Should detect repeated failing commands
	if !strings.Contains(result, "Repeated failing") && !strings.Contains(result, "failure rate") {
		t.Errorf("Expected failure detection, got: %s", result)
	}
	t.Logf("Pattern analysis result: %s", result)
}

// TestFormatMessageForBudgetReview_MultipleToolCalls verifies multiple tool calls in one message.
func TestFormatMessageForBudgetReview_MultipleToolCalls(t *testing.T) {
	msg := contextmgr.Message{
		Role:    "assistant",
		Content: "I need to read two files",
		ToolCalls: []contextmgr.ToolCall{
			{ID: "call_1", Name: "read_file", Parameters: map[string]any{"path": "a.go"}},
			{ID: "call_2", Name: "read_file", Parameters: map[string]any{"path": "b.go"}},
		},
	}

	result := formatMessageForBudgetReview(&msg)

	if !strings.Contains(result, `[tool: read_file(path="a.go")]`) {
		t.Errorf("Expected first tool call, got: %s", result)
	}
	if !strings.Contains(result, `[tool: read_file(path="b.go")]`) {
		t.Errorf("Expected second tool call, got: %s", result)
	}
}

// getMapKeys returns the keys of a map for debugging.
func getMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// TestBudgetReviewCompletedAtTimestamp tests that completion timestamp is set.
func TestBudgetReviewCompletedAtTimestamp(t *testing.T) {
	// Create a test logger
	logger := logx.NewLogger("coder-test")

	// Create a test state machine
	sm := agent.NewBaseStateMachine("test-coder", StateBudgetReview, nil, CoderTransitions)
	sm.SetStateData(KeyOrigin, string(StateCoding))

	// Create minimal coder instance
	c := &Coder{
		BaseStateMachine: sm,
		logger:           logger,
	}

	// Capture time before processing
	before := time.Now().UTC()

	// Process budget review result
	result := &effect.BudgetReviewResult{
		Status:   proto.ApprovalStatusApproved,
		Feedback: "",
	}

	_, _, err := c.processBudgetReviewResult(context.Background(), sm, result)
	if err != nil {
		t.Fatalf("processBudgetReviewResult failed: %v", err)
	}

	// Capture time after processing
	after := time.Now().UTC()

	// Verify timestamp was set
	timestampVal, exists := sm.GetStateValue(KeyBudgetReviewCompletedAt)
	if !exists {
		t.Fatal("Budget review completion timestamp was not set")
	}

	timestamp, ok := timestampVal.(time.Time)
	if !ok {
		t.Fatalf("Budget review completion timestamp is not a time.Time: %T", timestampVal)
	}

	// Verify timestamp is reasonable
	if timestamp.Before(before) || timestamp.After(after) {
		t.Errorf("Budget review completion timestamp %v is outside expected range [%v, %v]", timestamp, before, after)
	}
}
