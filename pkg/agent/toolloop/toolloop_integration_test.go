//go:build integration

package toolloop_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/toolloop"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/tools"
)

// TestIterationLimitError tests the IterationLimitError.Error() method.
func TestIterationLimitError(t *testing.T) {
	err := &toolloop.IterationLimitError{
		Key:       "test-key",
		Limit:     10,
		Iteration: 10,
	}

	expectedMsg := `iteration limit (10) exceeded for key "test-key" at iteration 10`
	if err.Error() != expectedMsg {
		t.Errorf("Expected error message %q, got %q", expectedMsg, err.Error())
	}
}

// TestLLMError tests handling of LLM client errors.
func TestLLMError(t *testing.T) {
	ctx := context.Background()
	cm := contextmgr.NewContextManager()
	logger := logx.NewLogger("test")

	// LLM that returns an error
	llmClient := &mockLLMClient{
		responses: []agent.CompletionResponse{},
		// Will return "no more mock responses" error on first call
	}

	terminalTool := &mockTerminalTool{
		name: "submit",
		execFunc: func(_ context.Context, _ map[string]any) (*tools.ExecResult, error) {
			return &tools.ExecResult{
				Content: "submitted",
				ProcessEffect: &tools.ProcessEffect{
					Signal: "DONE",
					Data:   map[string]any{},
				},
			}, nil
		},
	}

	loop := toolloop.New(llmClient, logger)
	cfg := &toolloop.Config[string]{
		ContextManager: cm,
		TerminalTool:   terminalTool,
		MaxIterations:  5,
		MaxTokens:      1000,
		AgentID:        "test-agent",
	}

	out := toolloop.Run(loop, ctx, cfg)

	// Should return error outcome
	if out.Kind == toolloop.OutcomeProcessEffect {
		t.Fatal("Expected error outcome, got ProcessEffect")
	}

	if out.Err == nil {
		t.Fatal("Expected error, got nil")
	}

	// Check for error message (wrapped with additional context)
	expectedMsg := "LLM completion failed: no more mock responses"
	if out.Err.Error() != expectedMsg {
		t.Errorf("Expected error %q, got: %v", expectedMsg, out.Err)
	}
}

// TestToolExecutionError tests handling of tool execution errors.
// Tool errors are logged but don't stop the loop - the LLM gets an error result
// and can decide how to proceed.
func TestToolExecutionError(t *testing.T) {
	ctx := context.Background()
	cm := contextmgr.NewContextManager()
	logger := logx.NewLogger("test")

	llmClient := &mockLLMClient{
		responses: []agent.CompletionResponse{
			{
				Content: "Calling tool",
				ToolCalls: []agent.ToolCall{
					{ID: "call1", Name: "failing-tool", Parameters: map[string]any{}},
				},
			},
			{
				Content: "Recovering from error",
				ToolCalls: []agent.ToolCall{
					{ID: "call2", Name: "submit", Parameters: map[string]any{}},
				},
			},
		},
	}

	failingTool := &mockGeneralTool{
		name: "failing-tool",
		execFunc: func(_ context.Context, _ map[string]any) (*tools.ExecResult, error) {
			return nil, errors.New("tool execution failed")
		},
	}

	terminalTool := &mockTerminalTool{
		name: "submit",
		execFunc: func(_ context.Context, _ map[string]any) (*tools.ExecResult, error) {
			return &tools.ExecResult{
				Content: "recovered",
				ProcessEffect: &tools.ProcessEffect{
					Signal: "COMPLETE",
					Data:   map[string]any{},
				},
			}, nil
		},
	}

	loop := toolloop.New(llmClient, logger)
	cfg := &toolloop.Config[string]{
		ContextManager: cm,
		GeneralTools:   []tools.Tool{failingTool},
		TerminalTool:   terminalTool,
		MaxIterations:  5,
		MaxTokens:      1000,
		AgentID:        "test-agent",
	}

	out := toolloop.Run(loop, ctx, cfg)

	// Tool errors don't stop the loop - the LLM can recover
	if out.Kind != toolloop.OutcomeProcessEffect {
		t.Fatalf("Expected ProcessEffect after recovery, got %v with error: %v", out.Kind, out.Err)
	}

	if out.Signal != "COMPLETE" {
		t.Errorf("Expected signal 'COMPLETE', got %q", out.Signal)
	}

	if llmClient.callCount != 2 {
		t.Errorf("Expected 2 LLM calls (error + recovery), got %d", llmClient.callCount)
	}
}

// TestContextCancellation tests handling of context cancellation.
func TestContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	cm := contextmgr.NewContextManager()
	logger := logx.NewLogger("test")

	llmClient := &mockLLMClient{
		responses: []agent.CompletionResponse{
			{
				Content: "Should not reach here",
				ToolCalls: []agent.ToolCall{
					{ID: "call1", Name: "submit", Parameters: map[string]any{}},
				},
			},
		},
	}

	terminalTool := &mockTerminalTool{
		name: "submit",
	}

	loop := toolloop.New(llmClient, logger)
	cfg := &toolloop.Config[string]{
		ContextManager: cm,
		TerminalTool:   terminalTool,
		MaxIterations:  5,
		MaxTokens:      1000,
		AgentID:        "test-agent",
	}

	out := toolloop.Run(loop, ctx, cfg)

	// Should return error outcome due to context cancellation
	if out.Kind == toolloop.OutcomeProcessEffect {
		t.Fatal("Expected error outcome due to context cancellation, got ProcessEffect")
	}

	if out.Err == nil {
		t.Fatal("Expected context cancellation error, got nil")
	}

	if !errors.Is(out.Err, context.Canceled) {
		t.Errorf("Expected context.Canceled error, got: %v", out.Err)
	}
}

// TestMultipleToolCalls tests handling of multiple tool calls in a single iteration.
func TestMultipleToolCalls(t *testing.T) {
	ctx := context.Background()
	cm := contextmgr.NewContextManager()
	logger := logx.NewLogger("test")

	var executionOrder []string

	llmClient := &mockLLMClient{
		responses: []agent.CompletionResponse{
			{
				Content: "Multiple tools",
				ToolCalls: []agent.ToolCall{
					{ID: "call1", Name: "read", Parameters: map[string]any{"file": "a.txt"}},
					{ID: "call2", Name: "read", Parameters: map[string]any{"file": "b.txt"}},
					{ID: "call3", Name: "read", Parameters: map[string]any{"file": "c.txt"}},
				},
			},
			{
				Content: "Done",
				ToolCalls: []agent.ToolCall{
					{ID: "call4", Name: "submit", Parameters: map[string]any{}},
				},
			},
		},
	}

	readTool := &mockGeneralTool{
		name: "read",
		execFunc: func(_ context.Context, params map[string]any) (*tools.ExecResult, error) {
			file, _ := params["file"].(string)
			executionOrder = append(executionOrder, file)
			return &tools.ExecResult{Content: fmt.Sprintf("read %s", file)}, nil
		},
	}

	terminalTool := &mockTerminalTool{
		name: "submit",
		execFunc: func(_ context.Context, _ map[string]any) (*tools.ExecResult, error) {
			executionOrder = append(executionOrder, "submit")
			return &tools.ExecResult{
				Content: "done",
				ProcessEffect: &tools.ProcessEffect{
					Signal: "COMPLETE",
					Data:   map[string]any{},
				},
			}, nil
		},
	}

	loop := toolloop.New(llmClient, logger)
	cfg := &toolloop.Config[string]{
		ContextManager: cm,
		GeneralTools:   []tools.Tool{readTool},
		TerminalTool:   terminalTool,
		MaxIterations:  5,
		MaxTokens:      1000,
		AgentID:        "test-agent",
	}

	out := toolloop.Run(loop, ctx, cfg)

	if out.Kind != toolloop.OutcomeProcessEffect {
		t.Fatalf("Expected ProcessEffect, got %v with error: %v", out.Kind, out.Err)
	}

	// Verify all tools were called in order
	expectedOrder := []string{"a.txt", "b.txt", "c.txt", "submit"}
	if len(executionOrder) != len(expectedOrder) {
		t.Fatalf("Expected %d tool calls, got %d", len(expectedOrder), len(executionOrder))
	}

	for i, expected := range expectedOrder {
		if executionOrder[i] != expected {
			t.Errorf("Tool call %d: expected %q, got %q", i, expected, executionOrder[i])
		}
	}
}

// TestSingleTurnMode tests that SingleTurn mode enforces immediate terminal tool usage.
func TestSingleTurnMode(t *testing.T) {
	ctx := context.Background()
	cm := contextmgr.NewContextManager()
	logger := logx.NewLogger("test")

	llmClient := &mockLLMClient{
		responses: []agent.CompletionResponse{
			{
				Content: "General tool",
				ToolCalls: []agent.ToolCall{
					{ID: "call1", Name: "read", Parameters: map[string]any{}},
				},
			},
		},
	}

	generalTool := &mockGeneralTool{
		name: "read",
	}

	terminalTool := &mockTerminalTool{
		name: "submit",
	}

	loop := toolloop.New(llmClient, logger)
	cfg := &toolloop.Config[string]{
		ContextManager: cm,
		GeneralTools:   []tools.Tool{generalTool},
		TerminalTool:   terminalTool,
		MaxIterations:  5,
		MaxTokens:      1000,
		AgentID:        "test-agent",
		SingleTurn:     true, // Enforce single-turn mode
	}

	out := toolloop.Run(loop, ctx, cfg)

	// Should error because SingleTurn requires terminal tool on first call
	if out.Kind == toolloop.OutcomeProcessEffect {
		t.Fatal("Expected error in SingleTurn mode when general tool is called, got ProcessEffect")
	}

	if out.Err == nil {
		t.Fatal("Expected error in SingleTurn mode, got nil")
	}
}

// TestEmptyToolCalls tests handling of LLM response with no tool calls.
func TestEmptyToolCalls(t *testing.T) {
	ctx := context.Background()
	cm := contextmgr.NewContextManager()
	logger := logx.NewLogger("test")

	llmClient := &mockLLMClient{
		responses: []agent.CompletionResponse{
			{
				Content:   "Just text, no tools",
				ToolCalls: []agent.ToolCall{}, // Empty tool calls
			},
			{
				Content: "Now calling tool",
				ToolCalls: []agent.ToolCall{
					{ID: "call1", Name: "submit", Parameters: map[string]any{}},
				},
			},
		},
	}

	terminalTool := &mockTerminalTool{
		name: "submit",
		execFunc: func(_ context.Context, _ map[string]any) (*tools.ExecResult, error) {
			return &tools.ExecResult{
				Content: "done",
				ProcessEffect: &tools.ProcessEffect{
					Signal: "COMPLETE",
					Data:   map[string]any{},
				},
			}, nil
		},
	}

	loop := toolloop.New(llmClient, logger)
	cfg := &toolloop.Config[string]{
		ContextManager: cm,
		TerminalTool:   terminalTool,
		MaxIterations:  5,
		MaxTokens:      1000,
		AgentID:        "test-agent",
	}

	out := toolloop.Run(loop, ctx, cfg)

	if out.Kind != toolloop.OutcomeProcessEffect {
		t.Fatalf("Expected ProcessEffect, got %v with error: %v", out.Kind, out.Err)
	}

	// Should succeed after second iteration with actual tool call
	if out.Signal != "COMPLETE" {
		t.Errorf("Expected signal 'COMPLETE', got %q", out.Signal)
	}

	if llmClient.callCount != 2 {
		t.Errorf("Expected 2 LLM calls, got %d", llmClient.callCount)
	}
}

// TestUnknownToolCall tests handling of tool calls for tools not in the registry.
func TestUnknownToolCall(t *testing.T) {
	ctx := context.Background()
	cm := contextmgr.NewContextManager()
	logger := logx.NewLogger("test")

	llmClient := &mockLLMClient{
		responses: []agent.CompletionResponse{
			{
				Content: "Calling unknown tool",
				ToolCalls: []agent.ToolCall{
					{ID: "call1", Name: "unknown-tool", Parameters: map[string]any{}},
				},
			},
		},
	}

	terminalTool := &mockTerminalTool{
		name: "submit",
	}

	loop := toolloop.New(llmClient, logger)
	cfg := &toolloop.Config[string]{
		ContextManager: cm,
		TerminalTool:   terminalTool,
		MaxIterations:  5,
		MaxTokens:      1000,
		AgentID:        "test-agent",
	}

	out := toolloop.Run(loop, ctx, cfg)

	// Should return error for unknown tool
	if out.Kind == toolloop.OutcomeProcessEffect {
		t.Fatal("Expected error for unknown tool, got ProcessEffect")
	}

	if out.Err == nil {
		t.Fatal("Expected error for unknown tool, got nil")
	}
}

// TestDebugLogging tests that debug logging doesn't break execution.
func TestDebugLogging(t *testing.T) {
	ctx := context.Background()
	cm := contextmgr.NewContextManager()
	logger := logx.NewLogger("test")

	llmClient := &mockLLMClient{
		responses: []agent.CompletionResponse{
			{
				Content: "Calling terminal tool",
				ToolCalls: []agent.ToolCall{
					{ID: "call1", Name: "submit", Parameters: map[string]any{}},
				},
			},
		},
	}

	terminalTool := &mockTerminalTool{
		name: "submit",
		execFunc: func(_ context.Context, _ map[string]any) (*tools.ExecResult, error) {
			return &tools.ExecResult{
				Content: "done",
				ProcessEffect: &tools.ProcessEffect{
					Signal: "COMPLETE",
					Data:   map[string]any{},
				},
			}, nil
		},
	}

	loop := toolloop.New(llmClient, logger)
	cfg := &toolloop.Config[string]{
		ContextManager: cm,
		TerminalTool:   terminalTool,
		MaxIterations:  5,
		MaxTokens:      1000,
		AgentID:        "test-agent",
		DebugLogging:   true, // Enable debug logging
	}

	out := toolloop.Run(loop, ctx, cfg)

	if out.Kind != toolloop.OutcomeProcessEffect {
		t.Fatalf("Expected ProcessEffect with debug logging enabled, got %v with error: %v", out.Kind, out.Err)
	}

	if out.Signal != "COMPLETE" {
		t.Errorf("Expected signal 'COMPLETE', got %q", out.Signal)
	}
}

// TestMaxTokensConfiguration tests that MaxTokens is properly configured.
func TestMaxTokensConfiguration(t *testing.T) {
	ctx := context.Background()
	cm := contextmgr.NewContextManager()
	logger := logx.NewLogger("test")

	llmClient := &mockLLMClient{
		responses: []agent.CompletionResponse{
			{
				Content: "Done",
				ToolCalls: []agent.ToolCall{
					{ID: "call1", Name: "submit", Parameters: map[string]any{}},
				},
			},
		},
	}

	terminalTool := &mockTerminalTool{
		name: "submit",
		execFunc: func(_ context.Context, _ map[string]any) (*tools.ExecResult, error) {
			return &tools.ExecResult{
				Content: "done",
				ProcessEffect: &tools.ProcessEffect{
					Signal: "COMPLETE",
					Data:   map[string]any{},
				},
			}, nil
		},
	}

	loop := toolloop.New(llmClient, logger)

	// Test with custom MaxTokens
	cfg := &toolloop.Config[string]{
		ContextManager: cm,
		TerminalTool:   terminalTool,
		MaxIterations:  5,
		MaxTokens:      8000, // Custom token limit
		AgentID:        "test-agent",
	}

	out := toolloop.Run(loop, ctx, cfg)

	if out.Kind != toolloop.OutcomeProcessEffect {
		t.Fatalf("Expected ProcessEffect, got %v with error: %v", out.Kind, out.Err)
	}
}
