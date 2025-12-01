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
// When the context is cancelled, the toolloop should exit cleanly with OutcomeGracefulShutdown.
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

	// Should return graceful shutdown outcome due to context cancellation
	if out.Kind != toolloop.OutcomeGracefulShutdown {
		t.Fatalf("Expected OutcomeGracefulShutdown, got %v with error: %v", out.Kind, out.Err)
	}

	if out.Err == nil {
		t.Fatal("Expected ErrGracefulShutdown error, got nil")
	}

	if !errors.Is(out.Err, toolloop.ErrGracefulShutdown) {
		t.Errorf("Expected ErrGracefulShutdown error, got: %v", out.Err)
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

// =============================================================================
// Graceful Shutdown Tests
// =============================================================================
// These tests verify the graceful shutdown behavior which is critical for
// session persistence and resume functionality.

// TestGracefulShutdownCallback tests that OnShutdown callback is invoked on context cancellation.
func TestGracefulShutdownCallback(t *testing.T) {
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

	var callbackInvoked bool
	var callbackIteration int

	loop := toolloop.New(llmClient, logger)
	cfg := &toolloop.Config[string]{
		ContextManager: cm,
		TerminalTool:   terminalTool,
		MaxIterations:  5,
		MaxTokens:      1000,
		AgentID:        "test-agent",
		OnShutdown: func(iteration int) {
			callbackInvoked = true
			callbackIteration = iteration
		},
	}

	out := toolloop.Run(loop, ctx, cfg)

	// Verify outcome
	if out.Kind != toolloop.OutcomeGracefulShutdown {
		t.Fatalf("Expected OutcomeGracefulShutdown, got %v", out.Kind)
	}

	// Verify callback was invoked
	if !callbackInvoked {
		t.Error("OnShutdown callback was not invoked")
	}

	// Verify iteration is 1 (shutdown at start of first iteration)
	if callbackIteration != 1 {
		t.Errorf("Expected callback iteration 1, got %d", callbackIteration)
	}

	// Verify iteration in outcome matches
	if out.Iteration != 1 {
		t.Errorf("Expected outcome iteration 1, got %d", out.Iteration)
	}
}

// TestGracefulShutdownMidIteration tests shutdown during multi-iteration execution.
// This simulates a real scenario where context is cancelled after some work is done.
func TestGracefulShutdownMidIteration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cm := contextmgr.NewContextManager()
	logger := logx.NewLogger("test")

	iterationCount := 0

	llmClient := &mockLLMClient{
		responses: []agent.CompletionResponse{
			{
				Content: "First iteration - general tool",
				ToolCalls: []agent.ToolCall{
					{ID: "call1", Name: "read", Parameters: map[string]any{"file": "a.txt"}},
				},
			},
			{
				Content: "Second iteration - general tool",
				ToolCalls: []agent.ToolCall{
					{ID: "call2", Name: "read", Parameters: map[string]any{"file": "b.txt"}},
				},
			},
			{
				Content: "Third iteration - should not reach",
				ToolCalls: []agent.ToolCall{
					{ID: "call3", Name: "submit", Parameters: map[string]any{}},
				},
			},
		},
	}

	readTool := &mockGeneralTool{
		name: "read",
		execFunc: func(_ context.Context, params map[string]any) (*tools.ExecResult, error) {
			iterationCount++
			// Cancel after second iteration completes
			if iterationCount == 2 {
				cancel()
			}
			return &tools.ExecResult{Content: "read result"}, nil
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

	var shutdownIteration int

	loop := toolloop.New(llmClient, logger)
	cfg := &toolloop.Config[string]{
		ContextManager: cm,
		GeneralTools:   []tools.Tool{readTool},
		TerminalTool:   terminalTool,
		MaxIterations:  10,
		MaxTokens:      1000,
		AgentID:        "test-agent",
		OnShutdown: func(iteration int) {
			shutdownIteration = iteration
		},
	}

	out := toolloop.Run(loop, ctx, cfg)

	// Should get graceful shutdown
	if out.Kind != toolloop.OutcomeGracefulShutdown {
		t.Fatalf("Expected OutcomeGracefulShutdown, got %v with error: %v", out.Kind, out.Err)
	}

	// Should have completed 2 iterations before shutdown
	if iterationCount != 2 {
		t.Errorf("Expected 2 tool executions before shutdown, got %d", iterationCount)
	}

	// Shutdown should be at iteration 3 (detected at start of third iteration)
	if shutdownIteration != 3 {
		t.Errorf("Expected shutdown at iteration 3, got %d", shutdownIteration)
	}

	if out.Iteration != 3 {
		t.Errorf("Expected outcome iteration 3, got %d", out.Iteration)
	}
}

// TestGracefulShutdownNoCallback tests that shutdown works correctly without OnShutdown callback.
func TestGracefulShutdownNoCallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	cm := contextmgr.NewContextManager()
	logger := logx.NewLogger("test")

	llmClient := &mockLLMClient{
		responses: []agent.CompletionResponse{
			{
				Content: "Should not reach",
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
		// OnShutdown is nil - should still work
	}

	out := toolloop.Run(loop, ctx, cfg)

	// Should still get graceful shutdown without callback
	if out.Kind != toolloop.OutcomeGracefulShutdown {
		t.Fatalf("Expected OutcomeGracefulShutdown without callback, got %v", out.Kind)
	}

	if !errors.Is(out.Err, toolloop.ErrGracefulShutdown) {
		t.Errorf("Expected ErrGracefulShutdown, got: %v", out.Err)
	}
}

// TestGracefulShutdownErrorType tests that ErrGracefulShutdown is properly typed.
func TestGracefulShutdownErrorType(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cm := contextmgr.NewContextManager()
	logger := logx.NewLogger("test")

	llmClient := &mockLLMClient{
		responses: []agent.CompletionResponse{},
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

	// Verify error message
	if out.Err.Error() != "graceful shutdown requested" {
		t.Errorf("Expected error message 'graceful shutdown requested', got: %v", out.Err.Error())
	}

	// Verify errors.Is works for checking shutdown
	if !errors.Is(out.Err, toolloop.ErrGracefulShutdown) {
		t.Error("errors.Is(err, ErrGracefulShutdown) should return true")
	}

	// Verify it's NOT context.Canceled (we use our own error type)
	if errors.Is(out.Err, context.Canceled) {
		t.Error("errors.Is(err, context.Canceled) should return false")
	}
}

// TestOutcomeGracefulShutdownString tests the String() method of OutcomeGracefulShutdown.
func TestOutcomeGracefulShutdownString(t *testing.T) {
	kind := toolloop.OutcomeGracefulShutdown
	str := kind.String()

	if str != "GracefulShutdown" {
		t.Errorf("Expected OutcomeGracefulShutdown.String() = 'GracefulShutdown', got %q", str)
	}
}

// TestGracefulShutdownPreservesContext tests that context manager state is preserved on shutdown.
// This is critical for session resume - we need the conversation history intact.
func TestGracefulShutdownPreservesContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cm := contextmgr.NewContextManager()
	logger := logx.NewLogger("test")

	llmClient := &mockLLMClient{
		responses: []agent.CompletionResponse{
			{
				Content: "Working on it",
				ToolCalls: []agent.ToolCall{
					{ID: "call1", Name: "read", Parameters: map[string]any{}},
				},
			},
		},
	}

	readTool := &mockGeneralTool{
		name: "read",
		execFunc: func(_ context.Context, _ map[string]any) (*tools.ExecResult, error) {
			// Cancel during tool execution
			cancel()
			return &tools.ExecResult{Content: "file content"}, nil
		},
	}

	terminalTool := &mockTerminalTool{
		name: "submit",
	}

	loop := toolloop.New(llmClient, logger)
	cfg := &toolloop.Config[string]{
		ContextManager: cm,
		GeneralTools:   []tools.Tool{readTool},
		TerminalTool:   terminalTool,
		MaxIterations:  5,
		MaxTokens:      1000,
		AgentID:        "test-agent",
		InitialPrompt:  "Please process this task",
	}

	out := toolloop.Run(loop, ctx, cfg)

	if out.Kind != toolloop.OutcomeGracefulShutdown {
		t.Fatalf("Expected OutcomeGracefulShutdown, got %v", out.Kind)
	}

	// Verify context manager has accumulated messages from the toolloop execution
	// (initial prompt, LLM response, tool result)
	messages := cm.GetMessages()
	if len(messages) == 0 {
		t.Error("Expected context manager to have messages after partial execution, got 0")
	}

	// Verify we can serialize the context (critical for resume functionality)
	serialized, err := cm.Serialize()
	if err != nil {
		t.Errorf("Failed to serialize context manager after shutdown: %v", err)
	}
	if len(serialized) == 0 {
		t.Error("Serialized context should not be empty")
	}

	// Verify we can deserialize and the data is preserved
	cm2 := contextmgr.NewContextManager()
	if err := cm2.Deserialize(serialized); err != nil {
		t.Errorf("Failed to deserialize context manager: %v", err)
	}

	messages2 := cm2.GetMessages()
	if len(messages2) != len(messages) {
		t.Errorf("Deserialized context has %d messages, expected %d", len(messages2), len(messages))
	}
}

// TestGracefulShutdownDuringLLMCall tests that context cancellation during LLM call
// is treated as graceful shutdown, not as an LLM error.
func TestGracefulShutdownDuringLLMCall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cm := contextmgr.NewContextManager()
	logger := logx.NewLogger("test")

	// LLM client that cancels context during the call
	llmClient := &cancelingLLMClient{
		cancel: cancel,
	}

	terminalTool := &mockTerminalTool{
		name: "submit",
	}

	var callbackInvoked bool
	var callbackIteration int

	loop := toolloop.New(llmClient, logger)
	cfg := &toolloop.Config[string]{
		ContextManager: cm,
		TerminalTool:   terminalTool,
		MaxIterations:  5,
		MaxTokens:      1000,
		AgentID:        "test-agent",
		OnShutdown: func(iteration int) {
			callbackInvoked = true
			callbackIteration = iteration
		},
	}

	out := toolloop.Run(loop, ctx, cfg)

	// Should get graceful shutdown, NOT LLM error
	if out.Kind != toolloop.OutcomeGracefulShutdown {
		t.Fatalf("Expected OutcomeGracefulShutdown when context cancelled during LLM call, got %v with error: %v", out.Kind, out.Err)
	}

	// Verify callback was invoked
	if !callbackInvoked {
		t.Error("OnShutdown callback was not invoked when context cancelled during LLM call")
	}

	// Should be iteration 1 since cancellation happened during first LLM call
	if callbackIteration != 1 {
		t.Errorf("Expected callback iteration 1, got %d", callbackIteration)
	}

	// Verify it's ErrGracefulShutdown, not a wrapped LLM error
	if !errors.Is(out.Err, toolloop.ErrGracefulShutdown) {
		t.Errorf("Expected ErrGracefulShutdown, got: %v", out.Err)
	}
}

// cancelingLLMClient is a mock LLM client that cancels the context during Complete().
type cancelingLLMClient struct {
	cancel context.CancelFunc
}

func (c *cancelingLLMClient) Complete(ctx context.Context, _ agent.CompletionRequest) (agent.CompletionResponse, error) {
	// Cancel the context to simulate shutdown during LLM call
	c.cancel()
	// Return context.Canceled as real LLM clients would when cancelled
	return agent.CompletionResponse{}, context.Canceled
}

func (c *cancelingLLMClient) Stream(_ context.Context, _ agent.CompletionRequest) (<-chan agent.StreamChunk, error) {
	return nil, errors.New("not implemented")
}

func (c *cancelingLLMClient) GetModelName() string {
	return "canceling-mock"
}
