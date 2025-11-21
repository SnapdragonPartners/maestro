package toolloop_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	// Add a tool even though LLM won't use it - validation requires at least one tool
	toolProvider.AddTool("dummy_tool", func(_ context.Context, _ map[string]any) (any, error) {
		return map[string]any{"success": true}, nil
	})

	loop := toolloop.New(llmClient, logger)
	cfg := &toolloop.Config[struct{}]{
		ContextManager: cm,
		InitialPrompt:  "Say hello",
		ToolProvider:   toolProvider,
		MaxIterations:  5,
		MaxTokens:      1000,
		CheckTerminal: func(_ []agent.ToolCall, _ []any) string {
			return "" // Never signal completion - let it timeout/error
		},
		ExtractResult: func(_ []agent.ToolCall, _ []any) (struct{}, error) {
			return struct{}{}, nil
		},
	}

	out := toolloop.Run(loop, ctx, cfg)
	// Should error on second no-tool response
	if out.Kind == toolloop.OutcomeSuccess {
		t.Fatal("expected error for consecutive no-tool responses, got nil")
	}

	if out.Signal != "ERROR" {
		t.Errorf("expected signal 'ERROR', got %q", out.Signal)
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
	cfg := &toolloop.Config[struct{}]{
		ContextManager: cm,
		InitialPrompt:  "Run test",
		ToolProvider:   toolProvider,
		MaxIterations:  5,
		MaxTokens:      1000,
		CheckTerminal: func(_ []agent.ToolCall, _ []any) string {
			return "" // Never signal completion - let it timeout/error
		},
		ExtractResult: func(_ []agent.ToolCall, _ []any) (struct{}, error) {
			return struct{}{}, nil
		},
	}

	out := toolloop.Run(loop, ctx, cfg)
	// Should error on second no-tool response
	if out.Kind == toolloop.OutcomeSuccess {
		t.Fatal("expected error for consecutive no-tool responses, got nil")
	}

	if out.Signal != "ERROR" {
		t.Errorf("expected signal 'ERROR', got %q", out.Signal)
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
	cfg := &toolloop.Config[struct{}]{
		ContextManager: cm,
		ToolProvider:   toolProvider,
		MaxIterations:  5,
		MaxTokens:      1000,
		CheckTerminal: func(_ []agent.ToolCall, _ []any) string {
			return "" // Never signal completion - let it timeout/error
		},
		ExtractResult: func(_ []agent.ToolCall, _ []any) (struct{}, error) {
			return struct{}{}, nil
		},
	}

	out := toolloop.Run(loop, ctx, cfg)
	// Should error on second no-tool response
	if out.Kind == toolloop.OutcomeSuccess {
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

	if out.Signal != "ERROR" {
		t.Errorf("expected signal 'ERROR', got %q", out.Signal)
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
	cfg := &toolloop.Config[struct{}]{
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
		ExtractResult: func(_ []agent.ToolCall, _ []any) (struct{}, error) {
			return struct{}{}, nil
		},
	}

	out := toolloop.Run(loop, ctx, cfg)
	if out.Kind != toolloop.OutcomeSuccess {
		t.Fatalf("unexpected error: %v", out.Err)
	}

	if !terminalCalled {
		t.Error("expected CheckTerminal to be called")
	}

	if out.Signal != "SUBMITTED" {
		t.Errorf("expected signal 'SUBMITTED', got %q", out.Signal)
	}

	// Should only call LLM once (terminal signal exits loop)
	if llmClient.callCount != 1 {
		t.Errorf("expected 1 LLM call, got %d", llmClient.callCount)
	}
}

// TestIterationLimit tests that Escalation.OnHardLimit is called.
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

	hardLimitCalled := false
	loop := toolloop.New(llmClient, logger)
	cfg := &toolloop.Config[struct{}]{
		ContextManager: cm,
		ToolProvider:   toolProvider,
		MaxIterations:  3, // Hit limit
		MaxTokens:      1000,
		CheckTerminal: func(_ []agent.ToolCall, _ []any) string {
			return "" // Never signal completion - let it hit iteration limit
		},
		ExtractResult: func(_ []agent.ToolCall, _ []any) (struct{}, error) {
			return struct{}{}, nil
		},
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
				// Return nil so toolloop returns IterationLimitError (not this error)
				return nil
			},
		},
	}

	out := toolloop.Run(loop, ctx, cfg)
	if out.Kind == toolloop.OutcomeSuccess {
		t.Fatal("expected error for hard limit exceeded")
	}

	// Verify we got the typed IterationLimitError
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

	if out.Signal != "" {
		t.Errorf("expected empty signal on hard limit error, got %q", out.Signal)
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
	cfg := &toolloop.Config[struct{}]{
		ContextManager: cm,
		ToolProvider:   toolProvider,
		MaxIterations:  5,
		MaxTokens:      1000,
		CheckTerminal: func(_ []agent.ToolCall, _ []any) string {
			return "" // Never signal completion - let it timeout/error
		},
		ExtractResult: func(_ []agent.ToolCall, _ []any) (struct{}, error) {
			return struct{}{}, nil
		},
	}

	out := toolloop.Run(loop, ctx, cfg)
	// Should error on second no-tool response
	if out.Kind == toolloop.OutcomeSuccess {
		t.Fatal("expected error for consecutive no-tool responses, got nil")
	}

	if out.Signal != "ERROR" {
		t.Errorf("expected signal 'ERROR', got %q", out.Signal)
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
	cfg := &toolloop.Config[struct{}]{
		ToolProvider:  newMockToolProvider(),
		MaxIterations: 5,
		ExtractResult: func(_ []agent.ToolCall, _ []any) (struct{}, error) {
			return struct{}{}, nil
		},
	}
	out := toolloop.Run(loop, ctx, cfg)
	if out.Kind == toolloop.OutcomeSuccess || out.Err.Error() != "ContextManager is required" {
		t.Errorf("expected ContextManager required error, got %v", out.Err)
	}

	// Test missing ToolProvider
	cfg = &toolloop.Config[struct{}]{
		ContextManager: contextmgr.NewContextManager(),
		MaxIterations:  5,
		ExtractResult: func(_ []agent.ToolCall, _ []any) (struct{}, error) {
			return struct{}{}, nil
		},
	}
	out = toolloop.Run(loop, ctx, cfg)
	if out.Kind == toolloop.OutcomeSuccess || out.Err.Error() != "ToolProvider is required" {
		t.Errorf("expected ToolProvider required error, got %v", out.Err)
	}

	// Test missing CheckTerminal
	cfg = &toolloop.Config[struct{}]{
		ContextManager: contextmgr.NewContextManager(),
		ToolProvider:   newMockToolProvider(),
		MaxIterations:  5,
		ExtractResult: func(_ []agent.ToolCall, _ []any) (struct{}, error) {
			return struct{}{}, nil
		},
	}
	out = toolloop.Run(loop, ctx, cfg)
	if out.Kind == toolloop.OutcomeSuccess || out.Err.Error() != "CheckTerminal is required - every toolloop must have a way to exit" {
		t.Errorf("expected CheckTerminal required error, got %v", out.Err)
	}

	// Test missing ExtractResult
	cfg = &toolloop.Config[struct{}]{
		ContextManager: contextmgr.NewContextManager(),
		ToolProvider:   newMockToolProvider(),
		MaxIterations:  5,
		CheckTerminal: func(_ []agent.ToolCall, _ []any) string {
			return ""
		},
	}
	out = toolloop.Run(loop, ctx, cfg)
	if out.Kind == toolloop.OutcomeSuccess || out.Err.Error() != "ExtractResult is required for type-safe result extraction" {
		t.Errorf("expected ExtractResult required error, got %v", out.Err)
	}
}

// TestResultExtraction tests that ExtractResult properly extracts typed results.
func TestResultExtraction(t *testing.T) {
	ctx := context.Background()
	cm := contextmgr.NewContextManager()
	logger := logx.NewLogger("test")

	// Define a custom result type
	type TestResult struct {
		Value   string
		Success bool
	}

	llmClient := &mockLLMClient{
		responses: []agent.CompletionResponse{
			{
				Content: "Calling result tool",
				ToolCalls: []agent.ToolCall{
					{ID: "call1", Name: "result_tool", Parameters: map[string]any{"value": "test_data"}},
				},
			},
		},
	}

	toolProvider := newMockToolProvider()
	toolProvider.AddTool("result_tool", func(_ context.Context, params map[string]any) (any, error) {
		return map[string]any{"success": true, "value": params["value"]}, nil
	})

	loop := toolloop.New(llmClient, logger)
	cfg := &toolloop.Config[TestResult]{
		ContextManager: cm,
		ToolProvider:   toolProvider,
		MaxIterations:  5,
		MaxTokens:      1000,
		CheckTerminal: func(calls []agent.ToolCall, _ []any) string {
			// Signal terminal when result_tool is called
			for i := range calls {
				if calls[i].Name == "result_tool" {
					return "RESULT_READY"
				}
			}
			return ""
		},
		ExtractResult: func(calls []agent.ToolCall, results []any) (TestResult, error) {
			// Extract result from result_tool
			for i := range calls {
				if calls[i].Name == "result_tool" {
					if resultMap, ok := results[i].(map[string]any); ok {
						value, valueOK := resultMap["value"].(string)
						success, successOK := resultMap["success"].(bool)
						if !valueOK || !successOK {
							return TestResult{}, errors.New("invalid result data types")
						}
						return TestResult{
							Value:   value,
							Success: success,
						}, nil
					}
				}
			}
			return TestResult{}, errors.New("result_tool not found")
		},
	}

	out := toolloop.Run(loop, ctx, cfg)
	if out.Kind != toolloop.OutcomeSuccess {
		t.Fatalf("unexpected outcome: %v, err: %v", out.Kind, out.Err)
	}

	if out.Signal != "RESULT_READY" {
		t.Errorf("expected signal 'RESULT_READY', got %q", out.Signal)
	}

	if out.Value.Value != "test_data" {
		t.Errorf("expected result.Value='test_data', got %q", out.Value.Value)
	}

	if !out.Value.Success {
		t.Error("expected result.Success=true, got false")
	}
}

// TestResultExtractionError tests that ExtractResult errors are properly handled.
func TestResultExtractionError(t *testing.T) {
	ctx := context.Background()
	cm := contextmgr.NewContextManager()
	logger := logx.NewLogger("test")

	llmClient := &mockLLMClient{
		responses: []agent.CompletionResponse{
			{
				Content: "Calling tool",
				ToolCalls: []agent.ToolCall{
					{ID: "call1", Name: "some_tool", Parameters: map[string]any{}},
				},
			},
		},
	}

	toolProvider := newMockToolProvider()
	toolProvider.AddTool("some_tool", func(_ context.Context, _ map[string]any) (any, error) {
		return map[string]any{"success": true}, nil
	})

	loop := toolloop.New(llmClient, logger)
	cfg := &toolloop.Config[string]{
		ContextManager: cm,
		ToolProvider:   toolProvider,
		MaxIterations:  5,
		MaxTokens:      1000,
		CheckTerminal: func(_ []agent.ToolCall, _ []any) string {
			return "DONE" // Signal terminal immediately
		},
		ExtractResult: func(_ []agent.ToolCall, _ []any) (string, error) {
			// Extraction fails
			return "", errors.New("extraction failed: missing required data")
		},
	}

	out := toolloop.Run(loop, ctx, cfg)

	// Should return OutcomeExtractionError with error from ExtractResult
	if out.Kind != toolloop.OutcomeExtractionError {
		t.Fatalf("expected OutcomeExtractionError, got %v", out.Kind)
	}

	if out.Err == nil {
		t.Fatal("expected error from ExtractResult, got nil")
	}

	if !strings.Contains(out.Err.Error(), "extraction failed") {
		t.Errorf("expected extraction error, got %v", out.Err)
	}

	if out.Signal != "DONE" {
		t.Errorf("expected signal 'DONE', got %q", out.Signal)
	}

	if out.Value != "" {
		t.Errorf("expected empty result on error, got %q", out.Value)
	}
}

// TestEscalationSoftLimit tests that OnSoftLimit callback is invoked.
func TestEscalationSoftLimit(t *testing.T) {
	ctx := context.Background()
	cm := contextmgr.NewContextManager()
	logger := logx.NewLogger("test")

	// LLM calls 5 times before terminal
	llmClient := &mockLLMClient{
		responses: []agent.CompletionResponse{
			{Content: "Call 1", ToolCalls: []agent.ToolCall{{ID: "1", Name: "tool1", Parameters: map[string]any{}}}},
			{Content: "Call 2", ToolCalls: []agent.ToolCall{{ID: "2", Name: "tool1", Parameters: map[string]any{}}}},
			{Content: "Call 3", ToolCalls: []agent.ToolCall{{ID: "3", Name: "tool1", Parameters: map[string]any{}}}},
			{Content: "Call 4", ToolCalls: []agent.ToolCall{{ID: "4", Name: "tool1", Parameters: map[string]any{}}}},
			{Content: "Done", ToolCalls: []agent.ToolCall{{ID: "5", Name: "done_tool", Parameters: map[string]any{}}}},
		},
	}

	toolProvider := newMockToolProvider()
	toolProvider.AddTool("tool1", func(_ context.Context, _ map[string]any) (any, error) {
		return map[string]any{"success": true}, nil
	})
	toolProvider.AddTool("done_tool", func(_ context.Context, _ map[string]any) (any, error) {
		return map[string]any{"success": true, "done": true}, nil
	})

	softLimitCalled := false
	softLimitCount := 0
	hardLimitCalled := false

	loop := toolloop.New(llmClient, logger)
	cfg := &toolloop.Config[struct{}]{
		ContextManager: cm,
		ToolProvider:   toolProvider,
		MaxIterations:  10,
		MaxTokens:      1000,
		CheckTerminal: func(calls []agent.ToolCall, _ []any) string {
			// Terminal when done_tool is called
			for i := range calls {
				if calls[i].Name == "done_tool" {
					return "DONE"
				}
			}
			return ""
		},
		ExtractResult: func(_ []agent.ToolCall, _ []any) (struct{}, error) {
			return struct{}{}, nil
		},
		Escalation: &toolloop.EscalationConfig{
			Key:       "test_soft_limit",
			SoftLimit: 3, // Warn at 3 iterations
			HardLimit: 10,
			OnSoftLimit: func(count int) {
				softLimitCalled = true
				softLimitCount = count
			},
			OnHardLimit: func(_ context.Context, _ string, _ int) error {
				hardLimitCalled = true
				return errors.New("hard limit")
			},
		},
	}

	out := toolloop.Run(loop, ctx, cfg)
	if out.Kind != toolloop.OutcomeSuccess {
		t.Fatalf("unexpected error: %v", out.Err)
	}

	if out.Signal != "DONE" {
		t.Errorf("expected signal 'DONE', got %q", out.Signal)
	}

	if !softLimitCalled {
		t.Error("expected OnSoftLimit to be called")
	}

	if softLimitCount != 3 {
		t.Errorf("expected soft limit count 3, got %d", softLimitCount)
	}

	if hardLimitCalled {
		t.Error("OnHardLimit should not have been called")
	}
}

// TestEscalationHardLimit tests that OnHardLimit stops execution.
func TestEscalationHardLimit(t *testing.T) {
	ctx := context.Background()
	cm := contextmgr.NewContextManager()
	logger := logx.NewLogger("test")

	// LLM calls indefinitely
	responses := make([]agent.CompletionResponse, 10)
	for i := range responses {
		responses[i] = agent.CompletionResponse{
			Content:   fmt.Sprintf("Call %d", i+1),
			ToolCalls: []agent.ToolCall{{ID: fmt.Sprintf("%d", i+1), Name: "tool1", Parameters: map[string]any{}}},
		}
	}
	llmClient := &mockLLMClient{responses: responses}

	toolProvider := newMockToolProvider()
	toolProvider.AddTool("tool1", func(_ context.Context, _ map[string]any) (any, error) {
		return map[string]any{"success": true}, nil
	})

	hardLimitCalled := false
	hardLimitKey := ""
	hardLimitCount := 0

	loop := toolloop.New(llmClient, logger)
	cfg := &toolloop.Config[struct{}]{
		ContextManager: cm,
		ToolProvider:   toolProvider,
		MaxIterations:  5,
		MaxTokens:      1000,
		CheckTerminal: func(_ []agent.ToolCall, _ []any) string {
			return "" // Never signal - will hit hard limit
		},
		ExtractResult: func(_ []agent.ToolCall, _ []any) (struct{}, error) {
			return struct{}{}, nil
		},
		Escalation: &toolloop.EscalationConfig{
			Key:       "test_hard_limit",
			SoftLimit: 3,
			HardLimit: 5,
			OnSoftLimit: func(_ int) {
				// Soft limit callback
			},
			OnHardLimit: func(_ context.Context, key string, count int) error {
				hardLimitCalled = true
				hardLimitKey = key
				hardLimitCount = count
				// Return nil so toolloop returns IterationLimitError (not this error)
				return nil
			},
		},
	}

	out := toolloop.Run(loop, ctx, cfg)

	// Should get IterationLimitError
	if out.Kind == toolloop.OutcomeSuccess {
		t.Fatal("expected IterationLimitError, got nil")
	}

	// Verify we got the typed IterationLimitError
	var iterErr *toolloop.IterationLimitError
	if !errors.As(out.Err, &iterErr) {
		t.Fatalf("expected IterationLimitError, got %T: %v", out.Err, out.Err)
	}

	if iterErr.Key != "test_hard_limit" {
		t.Errorf("expected IterationLimitError.Key='test_hard_limit', got %q", iterErr.Key)
	}

	if iterErr.Limit != 5 {
		t.Errorf("expected IterationLimitError.Limit=5, got %d", iterErr.Limit)
	}

	if iterErr.Iteration != 5 {
		t.Errorf("expected IterationLimitError.Iteration=5, got %d", iterErr.Iteration)
	}

	if !hardLimitCalled {
		t.Error("expected OnHardLimit to be called")
	}

	if hardLimitKey != "test_hard_limit" {
		t.Errorf("expected key 'test_hard_limit', got %q", hardLimitKey)
	}

	if hardLimitCount != 5 {
		t.Errorf("expected hard limit count 5, got %d", hardLimitCount)
	}

	if out.Signal != "" {
		t.Errorf("expected empty signal on error, got %q", out.Signal)
	}

	if llmClient.callCount != 5 {
		t.Errorf("expected 5 LLM calls before hard limit, got %d", llmClient.callCount)
	}
}
