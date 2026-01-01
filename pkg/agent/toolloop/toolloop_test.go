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

// Mock LLM client for testing.
type mockLLMClient struct {
	responses []agent.CompletionResponse
	callCount int
}

func (m *mockLLMClient) Complete(ctx context.Context, _ agent.CompletionRequest) (agent.CompletionResponse, error) {
	// Check if context is cancelled before proceeding
	select {
	case <-ctx.Done():
		return agent.CompletionResponse{}, ctx.Err()
	default:
	}

	if m.callCount >= len(m.responses) {
		return agent.CompletionResponse{}, errors.New("no more mock responses")
	}
	resp := m.responses[m.callCount]
	m.callCount++
	return resp, nil
}

func (m *mockLLMClient) Stream(_ context.Context, _ agent.CompletionRequest) (<-chan agent.StreamChunk, error) {
	return nil, errors.New("not implemented")
}

func (m *mockLLMClient) GetModelName() string {
	return "mock-model"
}

// Simple mock tool for general tools.
type mockGeneralTool struct {
	name     string
	execFunc func(context.Context, map[string]any) (*tools.ExecResult, error)
	called   *[]string // Pointer to track calls
}

func (m *mockGeneralTool) Name() string {
	return m.name
}

func (m *mockGeneralTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        m.name,
		Description: "Mock general tool",
		InputSchema: tools.InputSchema{
			Type:       "object",
			Properties: make(map[string]tools.Property),
		},
	}
}

func (m *mockGeneralTool) Exec(ctx context.Context, params map[string]any) (*tools.ExecResult, error) {
	if m.called != nil {
		*m.called = append(*m.called, m.name)
	}
	if m.execFunc != nil {
		return m.execFunc(ctx, params)
	}
	return &tools.ExecResult{Content: "ok"}, nil
}

func (m *mockGeneralTool) PromptDocumentation() string {
	return "Mock tool documentation"
}

// Mock terminal tool for testing.
type mockTerminalTool struct {
	name        string
	extractFunc func([]agent.ToolCall, []any) (string, error)
	execFunc    func(context.Context, map[string]any) (*tools.ExecResult, error)
	called      *[]string
}

func (m *mockTerminalTool) Name() string {
	return m.name
}

func (m *mockTerminalTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        m.name,
		Description: "Mock terminal tool",
		InputSchema: tools.InputSchema{
			Type:       "object",
			Properties: make(map[string]tools.Property),
		},
	}
}

func (m *mockTerminalTool) Exec(ctx context.Context, params map[string]any) (*tools.ExecResult, error) {
	if m.called != nil {
		*m.called = append(*m.called, m.name)
	}
	if m.execFunc != nil {
		return m.execFunc(ctx, params)
	}
	return &tools.ExecResult{Content: "terminal tool executed"}, nil
}

func (m *mockTerminalTool) PromptDocumentation() string {
	return "Mock terminal tool documentation"
}

func (m *mockTerminalTool) ExtractResult(calls []agent.ToolCall, results []any) (string, error) {
	if m.extractFunc != nil {
		return m.extractFunc(calls, results)
	}
	return "result", nil
}

// Verify mockTerminalTool implements TerminalTool[string].
var _ toolloop.TerminalTool[string] = (*mockTerminalTool)(nil)

// TestBasicTerminalTool tests a simple terminal tool execution.
func TestBasicTerminalTool(t *testing.T) {
	ctx := context.Background()
	cm := contextmgr.NewContextManager()
	logger := logx.NewLogger("test")

	var called []string
	llmClient := &mockLLMClient{
		responses: []agent.CompletionResponse{
			{
				Content: "Calling terminal tool",
				ToolCalls: []agent.ToolCall{
					{ID: "call1", Name: "submit", Parameters: map[string]any{"value": "test"}},
				},
			},
		},
	}

	terminalTool := &mockTerminalTool{
		name:   "submit",
		called: &called,
		execFunc: func(_ context.Context, args map[string]any) (*tools.ExecResult, error) {
			// Return ProcessEffect with signal and data
			val, _ := args["value"].(string)
			return &tools.ExecResult{
				Content: "submitted",
				ProcessEffect: &tools.ProcessEffect{
					Signal: "SUBMIT_COMPLETE",
					Data: map[string]any{
						"value": val,
					},
				},
			}, nil
		},
		extractFunc: func(_ []agent.ToolCall, _ []any) (string, error) {
			// Legacy - not used anymore
			return "", toolloop.ErrNoTerminalTool
		},
	}

	loop := toolloop.New(llmClient, logger)
	cfg := &toolloop.Config[string]{
		ContextManager: cm,
		GeneralTools:   []tools.Tool{},
		TerminalTool:   terminalTool,
		MaxIterations:  5,
		MaxTokens:      1000,
		AgentID:        "test-agent",
	}

	out := toolloop.Run(loop, ctx, cfg)
	if out.Kind != toolloop.OutcomeProcessEffect {
		t.Fatalf("unexpected outcome: %v, error: %v", out.Kind, out.Err)
	}

	if out.Signal != "SUBMIT_COMPLETE" {
		t.Errorf("expected signal 'SUBMIT_COMPLETE', got %q", out.Signal)
	}

	// Extract value from EffectData
	effectData, ok := out.EffectData.(map[string]any)
	if !ok {
		t.Fatalf("expected EffectData to be map[string]any, got %T", out.EffectData)
	}

	value, _ := effectData["value"].(string)
	if value != "test" {
		t.Errorf("expected value 'test', got %q", value)
	}

	if len(called) != 1 || called[0] != "submit" {
		t.Errorf("expected terminal tool to be called once, got %v", called)
	}

	if llmClient.callCount != 1 {
		t.Errorf("expected 1 LLM call, got %d", llmClient.callCount)
	}
}

// TestGeneralToolsBeforeTerminal tests that general tools can be used before terminal.
func TestGeneralToolsBeforeTerminal(t *testing.T) {
	ctx := context.Background()
	cm := contextmgr.NewContextManager()
	logger := logx.NewLogger("test")

	var called []string
	llmClient := &mockLLMClient{
		responses: []agent.CompletionResponse{
			{
				Content: "Reading data",
				ToolCalls: []agent.ToolCall{
					{ID: "call1", Name: "read", Parameters: map[string]any{}},
				},
			},
			{
				Content: "Submitting",
				ToolCalls: []agent.ToolCall{
					{ID: "call2", Name: "submit", Parameters: map[string]any{"data": "processed"}},
				},
			},
		},
	}

	generalTool := &mockGeneralTool{
		name:   "read",
		called: &called,
		execFunc: func(_ context.Context, _ map[string]any) (*tools.ExecResult, error) {
			return &tools.ExecResult{Content: "data read"}, nil
		},
	}

	terminalTool := &mockTerminalTool{
		name:   "submit",
		called: &called,
		execFunc: func(_ context.Context, args map[string]any) (*tools.ExecResult, error) {
			// Return ProcessEffect with signal and data
			data, _ := args["data"].(string)
			return &tools.ExecResult{
				Content: "submitted",
				ProcessEffect: &tools.ProcessEffect{
					Signal: "SUBMIT_COMPLETE",
					Data: map[string]any{
						"value": data,
					},
				},
			}, nil
		},
		extractFunc: func(calls []agent.ToolCall, _ []any) (string, error) {
			for i := range calls {
				if calls[i].Name == "submit" {
					if data, ok := calls[i].Parameters["data"].(string); ok {
						return data, nil
					}
				}
			}
			return "", toolloop.ErrNoTerminalTool
		},
	}

	loop := toolloop.New(llmClient, logger)
	cfg := &toolloop.Config[string]{
		ContextManager: cm,
		GeneralTools:   []tools.Tool{generalTool},
		TerminalTool:   terminalTool,
		MaxIterations:  5,
		MaxTokens:      1000,
		AgentID:        "test-agent",
	}

	out := toolloop.Run(loop, ctx, cfg)
	if out.Kind != toolloop.OutcomeProcessEffect {
		t.Fatalf("unexpected outcome: %v, error: %v", out.Kind, out.Err)
	}

	if out.Signal != "SUBMIT_COMPLETE" {
		t.Errorf("expected signal 'SUBMIT_COMPLETE', got %q", out.Signal)
	}

	// Extract value from EffectData
	effectData, ok := out.EffectData.(map[string]any)
	if !ok {
		t.Fatalf("expected EffectData to be map[string]any, got %T", out.EffectData)
	}

	value, _ := effectData["value"].(string)
	if value != "processed" {
		t.Errorf("expected value 'processed', got %q", value)
	}

	if len(called) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(called))
	}

	if called[0] != "read" || called[1] != "submit" {
		t.Errorf("expected [read, submit], got %v", called)
	}

	if llmClient.callCount != 2 {
		t.Errorf("expected 2 LLM calls, got %d", llmClient.callCount)
	}
}

// TestIterationLimit tests that hard limit stops execution.
func TestIterationLimit(t *testing.T) {
	ctx := context.Background()
	cm := contextmgr.NewContextManager()
	logger := logx.NewLogger("test")

	// LLM always returns general tools, never terminal
	responses := make([]agent.CompletionResponse, 5)
	for i := range responses {
		responses[i] = agent.CompletionResponse{
			Content:   fmt.Sprintf("Call %d", i+1),
			ToolCalls: []agent.ToolCall{{ID: fmt.Sprintf("%d", i+1), Name: "read", Parameters: map[string]any{}}},
		}
	}
	llmClient := &mockLLMClient{responses: responses}

	generalTool := &mockGeneralTool{
		name: "read",
		execFunc: func(_ context.Context, _ map[string]any) (*tools.ExecResult, error) {
			return &tools.ExecResult{Content: "ok"}, nil
		},
	}

	terminalTool := &mockTerminalTool{
		name: "submit",
		extractFunc: func(_ []agent.ToolCall, _ []any) (string, error) {
			return "", toolloop.ErrNoTerminalTool
		},
	}

	hardLimitCalled := false
	loop := toolloop.New(llmClient, logger)
	cfg := &toolloop.Config[string]{
		ContextManager: cm,
		GeneralTools:   []tools.Tool{generalTool},
		TerminalTool:   terminalTool,
		MaxIterations:  3,
		MaxTokens:      1000,
		AgentID:        "test-agent",
		Escalation: &toolloop.EscalationConfig{
			Key:       "test_escalation",
			HardLimit: 3,
			OnHardLimit: func(_ context.Context, key string, count int) error {
				hardLimitCalled = true
				if key != "test_escalation" {
					t.Errorf("expected key 'test_escalation', got %q", key)
				}
				if count != 3 {
					t.Errorf("expected count 3, got %d", count)
				}
				return nil
			},
		},
	}

	out := toolloop.Run(loop, ctx, cfg)
	if out.Kind == toolloop.OutcomeProcessEffect {
		t.Fatal("expected error for hard limit exceeded, not ProcessEffect")
	}

	var iterErr *toolloop.IterationLimitError
	if !errors.As(out.Err, &iterErr) {
		t.Fatalf("expected IterationLimitError, got %T: %v", out.Err, out.Err)
	}

	if iterErr.Key != "test_escalation" {
		t.Errorf("expected IterationLimitError.Key='test_escalation', got %q", iterErr.Key)
	}

	if iterErr.Limit != 3 {
		t.Errorf("expected IterationLimitError.Limit=3, got %d", iterErr.Limit)
	}

	if iterErr.Iteration != 3 {
		t.Errorf("expected IterationLimitError.Iteration=3, got %d", iterErr.Iteration)
	}

	if !hardLimitCalled {
		t.Error("expected OnHardLimit to be called")
	}

	if llmClient.callCount != 3 {
		t.Errorf("expected 3 LLM calls, got %d", llmClient.callCount)
	}
}

// TestSoftLimit tests that soft limit callback is invoked.
func TestSoftLimit(t *testing.T) {
	ctx := context.Background()
	cm := contextmgr.NewContextManager()
	logger := logx.NewLogger("test")

	// LLM calls 4 times (read, read, read, submit)
	llmClient := &mockLLMClient{
		responses: []agent.CompletionResponse{
			{Content: "Call 1", ToolCalls: []agent.ToolCall{{ID: "1", Name: "read", Parameters: map[string]any{}}}},
			{Content: "Call 2", ToolCalls: []agent.ToolCall{{ID: "2", Name: "read", Parameters: map[string]any{}}}},
			{Content: "Call 3", ToolCalls: []agent.ToolCall{{ID: "3", Name: "read", Parameters: map[string]any{}}}},
			{Content: "Done", ToolCalls: []agent.ToolCall{{ID: "4", Name: "submit", Parameters: map[string]any{"result": "ok"}}}},
		},
	}

	generalTool := &mockGeneralTool{
		name: "read",
		execFunc: func(_ context.Context, _ map[string]any) (*tools.ExecResult, error) {
			return &tools.ExecResult{Content: "ok"}, nil
		},
	}

	terminalTool := &mockTerminalTool{
		name: "submit",
		execFunc: func(_ context.Context, args map[string]any) (*tools.ExecResult, error) {
			// Return ProcessEffect with signal and data
			result, _ := args["result"].(string)
			return &tools.ExecResult{
				Content: "submitted",
				ProcessEffect: &tools.ProcessEffect{
					Signal: "SUBMIT_COMPLETE",
					Data: map[string]any{
						"value": result,
					},
				},
			}, nil
		},
		extractFunc: func(calls []agent.ToolCall, _ []any) (string, error) {
			for i := range calls {
				if calls[i].Name == "submit" {
					if result, ok := calls[i].Parameters["result"].(string); ok {
						return result, nil
					}
				}
			}
			return "", toolloop.ErrNoTerminalTool
		},
	}

	softLimitCalled := false
	softLimitCount := 0

	loop := toolloop.New(llmClient, logger)
	cfg := &toolloop.Config[string]{
		ContextManager: cm,
		GeneralTools:   []tools.Tool{generalTool},
		TerminalTool:   terminalTool,
		MaxIterations:  10,
		MaxTokens:      1000,
		AgentID:        "test-agent",
		Escalation: &toolloop.EscalationConfig{
			Key:       "test_soft_limit",
			SoftLimit: 3,
			HardLimit: 10,
			OnSoftLimit: func(count int) {
				softLimitCalled = true
				softLimitCount = count
			},
			OnHardLimit: func(_ context.Context, _ string, _ int) error {
				t.Error("OnHardLimit should not be called")
				return nil
			},
		},
	}

	out := toolloop.Run(loop, ctx, cfg)
	if out.Kind != toolloop.OutcomeProcessEffect {
		t.Fatalf("unexpected outcome: %v, error: %v", out.Kind, out.Err)
	}

	if out.Signal != "SUBMIT_COMPLETE" {
		t.Errorf("expected signal 'SUBMIT_COMPLETE', got %q", out.Signal)
	}

	// Extract value from EffectData
	effectData, ok := out.EffectData.(map[string]any)
	if !ok {
		t.Fatalf("expected EffectData to be map[string]any, got %T", out.EffectData)
	}

	value, _ := effectData["value"].(string)
	if value != "ok" {
		t.Errorf("expected value 'ok', got %q", value)
	}

	if !softLimitCalled {
		t.Error("expected OnSoftLimit to be called")
	}

	if softLimitCount != 3 {
		t.Errorf("expected soft limit count 3, got %d", softLimitCount)
	}

	if llmClient.callCount != 4 {
		t.Errorf("expected 4 LLM calls, got %d", llmClient.callCount)
	}
}

// TestAutoWrap tests that terminal tools without ProcessEffect are auto-wrapped.
func TestAutoWrap(t *testing.T) {
	ctx := context.Background()
	cm := contextmgr.NewContextManager()
	logger := logx.NewLogger("test")

	llmClient := &mockLLMClient{
		responses: []agent.CompletionResponse{
			{
				Content: "Calling tool",
				ToolCalls: []agent.ToolCall{
					{ID: "call1", Name: "submit", Parameters: map[string]any{}},
				},
			},
		},
	}

	// Terminal tool that doesn't return ProcessEffect - should be auto-wrapped
	terminalTool := &mockTerminalTool{
		name: "submit",
		// No execFunc - will use default that doesn't return ProcessEffect
		extractFunc: func(_ []agent.ToolCall, _ []any) (string, error) {
			return "", errors.New("extraction failed: missing required data")
		},
	}

	loop := toolloop.New(llmClient, logger)
	cfg := &toolloop.Config[string]{
		ContextManager: cm,
		GeneralTools:   []tools.Tool{},
		TerminalTool:   terminalTool,
		MaxIterations:  5,
		MaxTokens:      1000,
		AgentID:        "test-agent",
	}

	out := toolloop.Run(loop, ctx, cfg)

	// Should succeed with auto-wrap, not fail with extraction error
	if out.Kind != toolloop.OutcomeProcessEffect {
		t.Fatalf("expected OutcomeProcessEffect (auto-wrapped), got %v with error: %v", out.Kind, out.Err)
	}

	if out.Signal != "TERMINAL_COMPLETE" {
		t.Errorf("expected auto-wrap signal 'TERMINAL_COMPLETE', got %q", out.Signal)
	}

	// Verify auto-wrap EffectData contains tool name
	effectData, ok := out.EffectData.(map[string]any)
	if !ok {
		t.Fatalf("expected EffectData to be map[string]any, got %T", out.EffectData)
	}

	toolName, _ := effectData["tool"].(string)
	if toolName != "submit" {
		t.Errorf("expected tool name 'submit' in auto-wrap data, got %q", toolName)
	}
}

// TestNoTerminalTool tests that ErrNoTerminalTool continues iteration.
func TestNoTerminalTool(t *testing.T) {
	ctx := context.Background()
	cm := contextmgr.NewContextManager()
	logger := logx.NewLogger("test")

	llmClient := &mockLLMClient{
		responses: []agent.CompletionResponse{
			{Content: "Try 1", ToolCalls: []agent.ToolCall{{ID: "1", Name: "read", Parameters: map[string]any{}}}},
			{Content: "Try 2", ToolCalls: []agent.ToolCall{{ID: "2", Name: "read", Parameters: map[string]any{}}}},
			{Content: "Done", ToolCalls: []agent.ToolCall{{ID: "3", Name: "submit", Parameters: map[string]any{}}}},
		},
	}

	generalTool := &mockGeneralTool{
		name: "read",
	}

	terminalTool := &mockTerminalTool{
		name: "submit",
		execFunc: func(_ context.Context, _ map[string]any) (*tools.ExecResult, error) {
			// Return ProcessEffect with signal and data
			return &tools.ExecResult{
				Content: "submitted",
				ProcessEffect: &tools.ProcessEffect{
					Signal: "SUBMIT_COMPLETE",
					Data: map[string]any{
						"value": "done",
					},
				},
			}, nil
		},
		extractFunc: func(calls []agent.ToolCall, _ []any) (string, error) {
			for i := range calls {
				if calls[i].Name == "submit" {
					return "done", nil
				}
			}
			return "", toolloop.ErrNoTerminalTool
		},
	}

	loop := toolloop.New(llmClient, logger)
	cfg := &toolloop.Config[string]{
		ContextManager: cm,
		GeneralTools:   []tools.Tool{generalTool},
		TerminalTool:   terminalTool,
		MaxIterations:  5,
		MaxTokens:      1000,
		AgentID:        "test-agent",
	}

	out := toolloop.Run(loop, ctx, cfg)
	if out.Kind != toolloop.OutcomeProcessEffect {
		t.Fatalf("unexpected outcome: %v, error: %v", out.Kind, out.Err)
	}

	if out.Signal != "SUBMIT_COMPLETE" {
		t.Errorf("expected signal 'SUBMIT_COMPLETE', got %q", out.Signal)
	}

	// Extract value from EffectData
	effectData, ok := out.EffectData.(map[string]any)
	if !ok {
		t.Fatalf("expected EffectData to be map[string]any, got %T", out.EffectData)
	}

	value, _ := effectData["value"].(string)
	if value != "done" {
		t.Errorf("expected value 'done', got %q", value)
	}

	if llmClient.callCount != 3 {
		t.Errorf("expected 3 LLM calls, got %d", llmClient.callCount)
	}
}

// TestMissingConfig tests validation of required configuration.
func TestMissingConfig(t *testing.T) {
	ctx := context.Background()
	logger := logx.NewLogger("test")

	llmClient := &mockLLMClient{
		responses: []agent.CompletionResponse{},
	}

	loop := toolloop.New(llmClient, logger)
	terminalTool := &mockTerminalTool{name: "submit"}

	// Test missing ContextManager
	cfg := &toolloop.Config[string]{
		TerminalTool:  terminalTool,
		MaxIterations: 5,
	}
	out := toolloop.Run(loop, ctx, cfg)
	if out.Kind == toolloop.OutcomeProcessEffect || out.Err.Error() != "ContextManager is required" {
		t.Errorf("expected ContextManager required error, got %v", out.Err)
	}

	// Test missing TerminalTool
	cfg = &toolloop.Config[string]{
		ContextManager: contextmgr.NewContextManager(),
		MaxIterations:  5,
	}
	out = toolloop.Run(loop, ctx, cfg)
	if out.Kind == toolloop.OutcomeProcessEffect || out.Err.Error() != "TerminalTool is required - every toolloop must have exactly one terminal tool" {
		t.Errorf("expected TerminalTool required error, got %v", out.Err)
	}
}

// TestProcessEffect tests that ProcessEffect pauses the loop.
func TestProcessEffect(t *testing.T) {
	ctx := context.Background()
	cm := contextmgr.NewContextManager()
	logger := logx.NewLogger("test")

	llmClient := &mockLLMClient{
		responses: []agent.CompletionResponse{
			{
				Content: "Asking question",
				ToolCalls: []agent.ToolCall{
					{ID: "call1", Name: "ask", Parameters: map[string]any{"question": "How?"}},
				},
			},
		},
	}

	generalTool := &mockGeneralTool{
		name: "ask",
		execFunc: func(_ context.Context, params map[string]any) (*tools.ExecResult, error) {
			question, _ := params["question"].(string)
			return &tools.ExecResult{
				Content: "Question posted",
				ProcessEffect: &tools.ProcessEffect{
					Signal: "AWAIT_ANSWER",
					Data: map[string]string{
						"question": question,
					},
				},
			}, nil
		},
	}

	terminalTool := &mockTerminalTool{
		name: "submit",
		extractFunc: func(_ []agent.ToolCall, _ []any) (string, error) {
			return "", toolloop.ErrNoTerminalTool
		},
	}

	loop := toolloop.New(llmClient, logger)
	cfg := &toolloop.Config[string]{
		ContextManager: cm,
		GeneralTools:   []tools.Tool{generalTool},
		TerminalTool:   terminalTool,
		MaxIterations:  5,
		MaxTokens:      1000,
		AgentID:        "test-agent",
	}

	out := toolloop.Run(loop, ctx, cfg)

	if out.Kind != toolloop.OutcomeProcessEffect {
		t.Fatalf("expected OutcomeProcessEffect, got %v", out.Kind)
	}

	if out.Signal != "AWAIT_ANSWER" {
		t.Errorf("expected signal 'AWAIT_ANSWER', got %q", out.Signal)
	}

	if llmClient.callCount != 1 {
		t.Errorf("expected 1 LLM call, got %d", llmClient.callCount)
	}
}

// TestTerminalToolFailureContinuesLoop tests that when a terminal tool fails with an error,
// the loop continues so the LLM can see the error and retry with correct parameters.
func TestTerminalToolFailureContinuesLoop(t *testing.T) {
	ctx := context.Background()
	cm := contextmgr.NewContextManager()
	logger := logx.NewLogger("test")

	// First call: terminal tool fails (missing required param)
	// Second call: terminal tool succeeds
	llmClient := &mockLLMClient{
		responses: []agent.CompletionResponse{
			{
				Content: "Calling submit without summary",
				ToolCalls: []agent.ToolCall{
					{ID: "call1", Name: "submit", Parameters: map[string]any{"markdown": "# Spec"}},
				},
			},
			{
				Content: "Calling submit with all params",
				ToolCalls: []agent.ToolCall{
					{ID: "call2", Name: "submit", Parameters: map[string]any{"markdown": "# Spec", "summary": "A spec"}},
				},
			},
		},
	}

	callCount := 0
	terminalTool := &mockTerminalTool{
		name: "submit",
		execFunc: func(_ context.Context, params map[string]any) (*tools.ExecResult, error) {
			callCount++
			// First call fails (simulating missing required param)
			if callCount == 1 {
				if _, ok := params["summary"]; !ok {
					return nil, fmt.Errorf("summary parameter is required")
				}
			}
			// Second call succeeds
			return &tools.ExecResult{
				Content: "submitted",
				ProcessEffect: &tools.ProcessEffect{
					Signal: "SPEC_PREVIEW",
					Data: map[string]any{
						"markdown": params["markdown"],
						"summary":  params["summary"],
					},
				},
			}, nil
		},
		extractFunc: func(calls []agent.ToolCall, _ []any) (string, error) {
			for i := range calls {
				if calls[i].Name == "submit" {
					return "done", nil
				}
			}
			return "", toolloop.ErrNoTerminalTool
		},
	}

	loop := toolloop.New(llmClient, logger)
	cfg := &toolloop.Config[string]{
		ContextManager: cm,
		GeneralTools:   []tools.Tool{},
		TerminalTool:   terminalTool,
		MaxIterations:  5,
		MaxTokens:      1000,
		AgentID:        "test-agent",
	}

	out := toolloop.Run(loop, ctx, cfg)

	// Should eventually succeed after retry
	if out.Kind != toolloop.OutcomeProcessEffect {
		t.Fatalf("expected OutcomeProcessEffect, got %v with error: %v", out.Kind, out.Err)
	}

	if out.Signal != "SPEC_PREVIEW" {
		t.Errorf("expected signal 'SPEC_PREVIEW', got %q", out.Signal)
	}

	// LLM should have been called twice (first failure, then success)
	if llmClient.callCount != 2 {
		t.Errorf("expected 2 LLM calls (first fail, then retry), got %d", llmClient.callCount)
	}

	// Terminal tool should have been called twice
	if callCount != 2 {
		t.Errorf("expected terminal tool to be called 2 times, got %d", callCount)
	}
}
