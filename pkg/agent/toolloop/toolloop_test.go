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

func (m *mockLLMClient) Complete(_ context.Context, _ agent.CompletionRequest) (agent.CompletionResponse, error) {
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
	name         string
	checkFunc    func([]agent.ToolCall, []any) string
	extractFunc  func([]agent.ToolCall, []any) (string, error)
	execFunc     func(context.Context, map[string]any) (*tools.ExecResult, error)
	called       *[]string
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
		execFunc: func(_ context.Context, params map[string]any) (*tools.ExecResult, error) {
			return &tools.ExecResult{Content: "submitted"}, nil
		},
		extractFunc: func(calls []agent.ToolCall, results []any) (string, error) {
			for i := range calls {
				if calls[i].Name == "submit" {
					if val, ok := calls[i].Parameters["value"].(string); ok {
						return val, nil
					}
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
	if out.Kind != toolloop.OutcomeSuccess {
		t.Fatalf("unexpected error: %v", out.Err)
	}

	if out.Value != "test" {
		t.Errorf("expected result 'test', got %q", out.Value)
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
	if out.Kind != toolloop.OutcomeSuccess {
		t.Fatalf("unexpected error: %v", out.Err)
	}

	if out.Value != "processed" {
		t.Errorf("expected result 'processed', got %q", out.Value)
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
	if out.Kind == toolloop.OutcomeSuccess {
		t.Fatal("expected error for hard limit exceeded")
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
	if out.Kind != toolloop.OutcomeSuccess {
		t.Fatalf("unexpected error: %v", out.Err)
	}

	if out.Value != "ok" {
		t.Errorf("expected result 'ok', got %q", out.Value)
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

// TestExtractionError tests that extraction errors are properly handled.
func TestExtractionError(t *testing.T) {
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

	terminalTool := &mockTerminalTool{
		name: "submit",
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

	if out.Kind != toolloop.OutcomeExtractionError {
		t.Fatalf("expected OutcomeExtractionError, got %v", out.Kind)
	}

	if out.Err == nil {
		t.Fatal("expected extraction error, got nil")
	}
	if out.Err.Error() != "result extraction failed: extraction failed: missing required data" {
		t.Errorf("expected 'result extraction failed: extraction failed: missing required data', got %v", out.Err)
	}

	if out.Value != "" {
		t.Errorf("expected empty result on error, got %q", out.Value)
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
	if out.Kind != toolloop.OutcomeSuccess {
		t.Fatalf("unexpected error: %v", out.Err)
	}

	if out.Value != "done" {
		t.Errorf("expected result 'done', got %q", out.Value)
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
	if out.Kind == toolloop.OutcomeSuccess || out.Err.Error() != "ContextManager is required" {
		t.Errorf("expected ContextManager required error, got %v", out.Err)
	}

	// Test missing TerminalTool
	cfg = &toolloop.Config[string]{
		ContextManager: contextmgr.NewContextManager(),
		MaxIterations:  5,
	}
	out = toolloop.Run(loop, ctx, cfg)
	if out.Kind == toolloop.OutcomeSuccess || out.Err.Error() != "TerminalTool is required - every toolloop must have exactly one terminal tool" {
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
			question := params["question"].(string)
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
