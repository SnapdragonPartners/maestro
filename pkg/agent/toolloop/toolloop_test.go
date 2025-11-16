package toolloop_test

import (
	"context"
	"errors"
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

// Mock tool provider for testing.
type mockToolProvider struct {
	tools  map[string]func(context.Context, map[string]any) (any, error)
	called []string
}

func newMockToolProvider() *mockToolProvider {
	return &mockToolProvider{
		tools:  make(map[string]func(context.Context, map[string]any) (any, error)),
		called: make([]string, 0),
	}
}

func (m *mockToolProvider) AddTool(name string, fn func(context.Context, map[string]any) (any, error)) {
	m.tools[name] = fn
}

func (m *mockToolProvider) Execute(ctx context.Context, name string, params map[string]any) (any, error) {
	m.called = append(m.called, name)
	if fn, ok := m.tools[name]; ok {
		return fn(ctx, params)
	}
	return nil, errors.New("tool not found")
}

func (m *mockToolProvider) Get(name string) (tools.Tool, error) {
	if _, ok := m.tools[name]; !ok {
		return nil, errors.New("tool not found")
	}
	return mockTool{name: name, provider: m}, nil
}

func (m *mockToolProvider) List() []tools.ToolMeta {
	// Return minimal tool metadata
	result := make([]tools.ToolMeta, 0, len(m.tools))
	for name := range m.tools {
		result = append(result, tools.ToolMeta{
			Name:        name,
			Description: "Mock tool",
			InputSchema: tools.InputSchema{
				Type:       "object",
				Properties: make(map[string]tools.Property),
			},
		})
	}
	return result
}

type mockTool struct {
	name     string
	provider *mockToolProvider
}

func (m mockTool) Name() string {
	return m.name
}

func (m mockTool) Description() string {
	return "Mock tool"
}

func (m mockTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        m.name,
		Description: "Mock tool",
		InputSchema: tools.InputSchema{
			Type:       "object",
			Properties: make(map[string]tools.Property),
		},
	}
}

func (m mockTool) Exec(ctx context.Context, params map[string]any) (any, error) {
	// Track that this tool was called
	m.provider.called = append(m.provider.called, m.name)

	if fn, ok := m.provider.tools[m.name]; ok {
		return fn(ctx, params)
	}
	return nil, errors.New("tool function not found")
}

func (m mockTool) PromptDocumentation() string {
	return "Mock tool documentation"
}

// TestBasicFlow tests a simple LLM call with no tools.
func TestBasicFlow(t *testing.T) {
	ctx := context.Background()
	cm := contextmgr.NewContextManager()
	logger := logx.NewLogger("test")

	// Test no-tool safeguard: first response has no tools, second response after reminder errors
	llmClient := &mockLLMClient{
		responses: []agent.CompletionResponse{
			{Content: "Hello, world!", ToolCalls: nil},   // No tools - triggers reminder
			{Content: "Still no tools!", ToolCalls: nil}, // Still no tools - triggers error
		},
	}

	toolProvider := newMockToolProvider()

	loop := toolloop.New(llmClient, logger)
	cfg := toolloop.Config{
		ContextManager: cm,
		InitialPrompt:  "Say hello",
		ToolProvider:   toolProvider,
		MaxIterations:  5,
		MaxTokens:      1000,
	}

	signal, err := loop.Run(ctx, &cfg)
	// Should error on second no-tool response
	if err == nil {
		t.Fatal("expected error for consecutive no-tool responses, got nil")
	}

	if signal != "ERROR" {
		t.Errorf("expected signal 'ERROR', got %q", signal)
	}

	if llmClient.callCount != 2 {
		t.Errorf("expected 2 LLM calls, got %d", llmClient.callCount)
	}
}

// TestSingleToolCall tests execution of a single tool.
func TestSingleToolCall(t *testing.T) {
	ctx := context.Background()
	cm := contextmgr.NewContextManager()
	logger := logx.NewLogger("test")

	llmClient := &mockLLMClient{
		responses: []agent.CompletionResponse{
			{
				Content: "Calling test tool",
				ToolCalls: []agent.ToolCall{
					{ID: "call1", Name: "test_tool", Parameters: map[string]any{"arg": "value"}},
				},
			},
			{Content: "Done!", ToolCalls: nil},      // No tools - triggers reminder
			{Content: "Still done", ToolCalls: nil}, // Still no tools - triggers error
		},
	}

	toolProvider := newMockToolProvider()
	toolProvider.AddTool("test_tool", func(_ context.Context, _ map[string]any) (any, error) {
		return map[string]any{"success": true, "result": "ok"}, nil
	})

	loop := toolloop.New(llmClient, logger)
	cfg := toolloop.Config{
		ContextManager: cm,
		InitialPrompt:  "Run test",
		ToolProvider:   toolProvider,
		MaxIterations:  5,
		MaxTokens:      1000,
	}

	signal, err := loop.Run(ctx, &cfg)
	// Should error on second no-tool response
	if err == nil {
		t.Fatal("expected error for consecutive no-tool responses, got nil")
	}

	if signal != "ERROR" {
		t.Errorf("expected signal 'ERROR', got %q", signal)
	}

	if llmClient.callCount != 3 {
		t.Errorf("expected 3 LLM calls, got %d", llmClient.callCount)
	}

	if len(toolProvider.called) != 1 || toolProvider.called[0] != "test_tool" {
		t.Errorf("expected test_tool to be called once, got %v", toolProvider.called)
	}
}

// TestMultipleTools tests that ALL tools are executed (API requirement).
func TestMultipleTools(t *testing.T) {
	ctx := context.Background()
	cm := contextmgr.NewContextManager()
	logger := logx.NewLogger("test")

	llmClient := &mockLLMClient{
		responses: []agent.CompletionResponse{
			{
				Content: "Calling multiple tools",
				ToolCalls: []agent.ToolCall{
					{ID: "call1", Name: "tool1", Parameters: map[string]any{}},
					{ID: "call2", Name: "tool2", Parameters: map[string]any{}},
					{ID: "call3", Name: "tool3", Parameters: map[string]any{}},
				},
			},
			{Content: "All done", ToolCalls: nil},    // No tools - triggers reminder
			{Content: "Really done", ToolCalls: nil}, // Still no tools - triggers error
		},
	}

	toolProvider := newMockToolProvider()
	for _, name := range []string{"tool1", "tool2", "tool3"} {
		toolName := name
		toolProvider.AddTool(toolName, func(_ context.Context, _ map[string]any) (any, error) {
			return map[string]any{"success": true, "tool": toolName}, nil
		})
	}

	loop := toolloop.New(llmClient, logger)
	cfg := toolloop.Config{
		ContextManager: cm,
		ToolProvider:   toolProvider,
		MaxIterations:  5,
		MaxTokens:      1000,
	}

	signal, err := loop.Run(ctx, &cfg)
	// Should error on second no-tool response
	if err == nil {
		t.Fatal("expected error for consecutive no-tool responses, got nil")
	}

	// Verify all three tools were called
	if len(toolProvider.called) != 3 {
		t.Errorf("expected 3 tools to be called, got %d", len(toolProvider.called))
	}

	for i, expected := range []string{"tool1", "tool2", "tool3"} {
		if toolProvider.called[i] != expected {
			t.Errorf("expected tool %d to be %s, got %s", i, expected, toolProvider.called[i])
		}
	}

	if signal != "ERROR" {
		t.Errorf("expected signal 'ERROR', got %q", signal)
	}
}

// TestTerminalSignal tests that CheckTerminal can signal state transition.
func TestTerminalSignal(t *testing.T) {
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

	toolProvider := newMockToolProvider()
	toolProvider.AddTool("submit", func(_ context.Context, _ map[string]any) (any, error) {
		return map[string]any{"success": true, "submitted": true}, nil
	})

	terminalCalled := false
	loop := toolloop.New(llmClient, logger)
	cfg := toolloop.Config{
		ContextManager: cm,
		ToolProvider:   toolProvider,
		MaxIterations:  5,
		MaxTokens:      1000,
		CheckTerminal: func(calls []agent.ToolCall, results []any) string {
			terminalCalled = true
			// Check if submit was called
			for i := range calls {
				if calls[i].Name == "submit" {
					if resultMap, ok := results[i].(map[string]any); ok {
						if submitted, ok := resultMap["submitted"].(bool); ok && submitted {
							return "SUBMITTED"
						}
					}
				}
			}
			return ""
		},
	}

	signal, err := loop.Run(ctx, &cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !terminalCalled {
		t.Error("expected CheckTerminal to be called")
	}

	if signal != "SUBMITTED" {
		t.Errorf("expected signal 'SUBMITTED', got %q", signal)
	}

	// Should only call LLM once (terminal signal exits loop)
	if llmClient.callCount != 1 {
		t.Errorf("expected 1 LLM call, got %d", llmClient.callCount)
	}
}

// TestIterationLimit tests that OnIterationLimit is called.
func TestIterationLimit(t *testing.T) {
	ctx := context.Background()
	cm := contextmgr.NewContextManager()
	logger := logx.NewLogger("test")

	// LLM always returns tool calls
	llmClient := &mockLLMClient{
		responses: []agent.CompletionResponse{
			{Content: "Call 1", ToolCalls: []agent.ToolCall{{ID: "1", Name: "tool1", Parameters: map[string]any{}}}},
			{Content: "Call 2", ToolCalls: []agent.ToolCall{{ID: "2", Name: "tool1", Parameters: map[string]any{}}}},
			{Content: "Call 3", ToolCalls: []agent.ToolCall{{ID: "3", Name: "tool1", Parameters: map[string]any{}}}},
		},
	}

	toolProvider := newMockToolProvider()
	toolProvider.AddTool("tool1", func(_ context.Context, _ map[string]any) (any, error) {
		return map[string]any{"success": true}, nil
	})

	limitCalled := false
	loop := toolloop.New(llmClient, logger)
	cfg := toolloop.Config{
		ContextManager: cm,
		ToolProvider:   toolProvider,
		MaxIterations:  3, // Hit limit
		MaxTokens:      1000,
		OnIterationLimit: func(_ context.Context) (string, error) {
			limitCalled = true
			return "LIMIT_REACHED", nil
		},
	}

	signal, err := loop.Run(ctx, &cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !limitCalled {
		t.Error("expected OnIterationLimit to be called")
	}

	if signal != "LIMIT_REACHED" {
		t.Errorf("expected signal 'LIMIT_REACHED', got %q", signal)
	}

	if llmClient.callCount != 3 {
		t.Errorf("expected 3 LLM calls, got %d", llmClient.callCount)
	}
}

// TestToolError tests handling of tool execution errors.
func TestToolError(t *testing.T) {
	ctx := context.Background()
	cm := contextmgr.NewContextManager()
	logger := logx.NewLogger("test")

	llmClient := &mockLLMClient{
		responses: []agent.CompletionResponse{
			{
				Content: "Calling failing tool",
				ToolCalls: []agent.ToolCall{
					{ID: "call1", Name: "fail_tool", Parameters: map[string]any{}},
				},
			},
			{Content: "Handled error", ToolCalls: nil}, // No tools - triggers reminder
			{Content: "Still handled", ToolCalls: nil}, // Still no tools - triggers error
		},
	}

	toolProvider := newMockToolProvider()
	toolProvider.AddTool("fail_tool", func(_ context.Context, _ map[string]any) (any, error) {
		return nil, errors.New("tool failed")
	})

	loop := toolloop.New(llmClient, logger)
	cfg := toolloop.Config{
		ContextManager: cm,
		ToolProvider:   toolProvider,
		MaxIterations:  5,
		MaxTokens:      1000,
	}

	signal, err := loop.Run(ctx, &cfg)
	// Should error on second no-tool response
	if err == nil {
		t.Fatal("expected error for consecutive no-tool responses, got nil")
	}

	if signal != "ERROR" {
		t.Errorf("expected signal 'ERROR', got %q", signal)
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

	// Test missing ContextManager
	cfg := toolloop.Config{
		ToolProvider:  newMockToolProvider(),
		MaxIterations: 5,
	}
	_, err := loop.Run(ctx, &cfg)
	if err == nil || err.Error() != "ContextManager is required" {
		t.Errorf("expected ContextManager required error, got %v", err)
	}

	// Test missing ToolProvider
	cfg = toolloop.Config{
		ContextManager: contextmgr.NewContextManager(),
		MaxIterations:  5,
	}
	_, err = loop.Run(ctx, &cfg)
	if err == nil || err.Error() != "ToolProvider is required" {
		t.Errorf("expected ToolProvider required error, got %v", err)
	}
}
