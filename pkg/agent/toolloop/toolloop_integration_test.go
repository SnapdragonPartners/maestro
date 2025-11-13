//go:build integration

package toolloop_test

import (
	"context"
	"os"
	"testing"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/internal/llmimpl/anthropic"
	"orchestrator/pkg/agent/internal/llmimpl/openaiofficial"
	"orchestrator/pkg/agent/toolloop"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/tools"
)

// Integration tests with real LLMs
// Run with: go test -tags=integration ./pkg/agent/toolloop -v

// TestGPT4oBasicToolCall tests gpt-4o with a simple tool
func TestGPT4oBasicToolCall(t *testing.T) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY not set")
	}

	ctx := context.Background()
	cm := contextmgr.NewContextManager()
	logger := logx.NewLogger("integration-test")

	// Create OpenAI client
	llmClient := openaiofficial.NewOfficialClientWithModel(apiKey, "gpt-4o-mini")

	// Create simple calculator tool
	toolProvider := newCalculatorToolProvider()

	loop := toolloop.New(llmClient, logger)
	cfg := toolloop.Config{
		ContextManager: cm,
		InitialPrompt:  "Calculate 5 + 3 using the calculator tool",
		ToolProvider:   toolProvider,
		MaxIterations:  3,
		MaxTokens:      1000,
		DebugLogging:   false,
	}

	signal, err := loop.Run(ctx, &cfg)
	if err != nil {
		t.Fatalf("toolloop failed: %v", err)
	}

	t.Logf("GPT-4o completed with signal: %q", signal)

	// Verify tool was called
	if len(toolProvider.callHistory) == 0 {
		t.Error("expected calculator tool to be called")
	}
}

// TestSonnetBasicToolCall tests Claude Sonnet with a simple tool
func TestSonnetBasicToolCall(t *testing.T) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY not set")
	}

	ctx := context.Background()
	cm := contextmgr.NewContextManager()
	logger := logx.NewLogger("integration-test")

	// Create Anthropic client
	llmClient := anthropic.NewClaudeClientWithModel(apiKey, "claude-sonnet-4-5")

	// Create simple calculator tool
	toolProvider := newCalculatorToolProvider()

	loop := toolloop.New(llmClient, logger)
	cfg := toolloop.Config{
		ContextManager: cm,
		InitialPrompt:  "Calculate 5 + 3 using the calculator tool",
		ToolProvider:   toolProvider,
		MaxIterations:  3,
		MaxTokens:      1000,
		DebugLogging:   false,
	}

	signal, err := loop.Run(ctx, &cfg)
	if err != nil {
		t.Fatalf("toolloop failed: %v", err)
	}

	t.Logf("Sonnet completed with signal: %q", signal)

	// Verify tool was called
	if len(toolProvider.callHistory) == 0 {
		t.Error("expected calculator tool to be called")
	}
}

// TestGPT4oTerminalSignal tests terminal signal detection
func TestGPT4oTerminalSignal(t *testing.T) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY not set")
	}

	ctx := context.Background()
	cm := contextmgr.NewContextManager()
	logger := logx.NewLogger("integration-test")

	llmClient := openaiofficial.NewOfficialClientWithModel(apiKey, "gpt-4o-mini")

	// Create tool provider with submit tool
	toolProvider := newSubmitToolProvider()

	terminalDetected := false
	loop := toolloop.New(llmClient, logger)
	cfg := toolloop.Config{
		ContextManager: cm,
		InitialPrompt:  "Submit the result 'Task completed successfully' using the submit tool",
		ToolProvider:   toolProvider,
		MaxIterations:  3,
		MaxTokens:      1000,
		DebugLogging:   false,
		CheckTerminal: func(calls []agent.ToolCall, results []any) string {
			for i := range calls {
				if calls[i].Name == "submit" {
					terminalDetected = true
					if resultMap, ok := results[i].(map[string]any); ok {
						if result, ok := resultMap["result"].(string); ok {
							return result
						}
					}
				}
			}
			return ""
		},
	}

	signal, err := loop.Run(ctx, &cfg)
	if err != nil {
		t.Fatalf("toolloop failed: %v", err)
	}

	if !terminalDetected {
		t.Error("expected terminal signal to be detected")
	}

	if signal == "" {
		t.Error("expected non-empty signal from terminal tool")
	}

	t.Logf("Terminal signal detected: %q", signal)
}

// TestSonnetMultipleTools tests Sonnet with multiple tool calls in one turn
func TestSonnetMultipleTools(t *testing.T) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY not set")
	}

	ctx := context.Background()
	cm := contextmgr.NewContextManager()
	logger := logx.NewLogger("integration-test")

	llmClient := anthropic.NewClaudeClientWithModel(apiKey, "claude-sonnet-4-5")

	// Create tool provider with calculator
	toolProvider := newCalculatorToolProvider()

	loop := toolloop.New(llmClient, logger)
	cfg := toolloop.Config{
		ContextManager: cm,
		InitialPrompt:  "Calculate both 5+3 and 10+7 using the calculator tool",
		ToolProvider:   toolProvider,
		MaxIterations:  3,
		MaxTokens:      1000,
		DebugLogging:   false,
	}

	signal, err := loop.Run(ctx, &cfg)
	if err != nil {
		t.Fatalf("toolloop failed: %v", err)
	}

	t.Logf("Sonnet completed with signal: %q", signal)

	// Verify multiple calls were made
	callHistory := toolProvider.callHistory
	if len(callHistory) < 2 {
		t.Errorf("expected at least 2 calculator calls, got %d", len(callHistory))
	}

	t.Logf("Calculator was called %d times", len(callHistory))
}

// calculatorProvider implements a simple calculator tool for testing
type calculatorProvider struct {
	callHistory []map[string]any
}

func newCalculatorToolProvider() *calculatorProvider {
	return &calculatorProvider{
		callHistory: make([]map[string]any, 0),
	}
}

func (p *calculatorProvider) Get(name string) (tools.Tool, error) {
	if name != "calculator" {
		return nil, nil
	}
	return &calculatorTool{provider: p}, nil
}

func (p *calculatorProvider) List() []tools.ToolMeta {
	return []tools.ToolMeta{
		{
			Name:        "calculator",
			Description: "Performs basic arithmetic operations",
			InputSchema: tools.InputSchema{
				Type: "object",
				Properties: map[string]tools.Property{
					"operation": {
						Type:        "string",
						Description: "The operation to perform: add, subtract, multiply, divide",
						Enum:        []string{"add", "subtract", "multiply", "divide"},
					},
					"a": {
						Type:        "number",
						Description: "First number",
					},
					"b": {
						Type:        "number",
						Description: "Second number",
					},
				},
				Required: []string{"operation", "a", "b"},
			},
		},
	}
}

type calculatorTool struct {
	provider *calculatorProvider
}

func (t *calculatorTool) Name() string {
	return "calculator"
}

func (t *calculatorTool) Description() string {
	return "Performs basic arithmetic operations"
}

func (t *calculatorTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "calculator",
		Description: "Performs basic arithmetic operations",
		InputSchema: tools.InputSchema{
			Type: "object",
			Properties: map[string]tools.Property{
				"operation": {
					Type:        "string",
					Description: "The operation to perform",
					Enum:        []string{"add", "subtract", "multiply", "divide"},
				},
				"a": {
					Type:        "number",
					Description: "First number",
				},
				"b": {
					Type:        "number",
					Description: "Second number",
				},
			},
			Required: []string{"operation", "a", "b"},
		},
	}
}

func (t *calculatorTool) Exec(_ context.Context, params map[string]any) (any, error) {
	// Record call
	t.provider.callHistory = append(t.provider.callHistory, params)

	operation, _ := params["operation"].(string)
	a, _ := params["a"].(float64)
	b, _ := params["b"].(float64)

	var result float64
	switch operation {
	case "add":
		result = a + b
	case "subtract":
		result = a - b
	case "multiply":
		result = a * b
	case "divide":
		if b == 0 {
			return map[string]any{"success": false, "error": "division by zero"}, nil
		}
		result = a / b
	default:
		return map[string]any{"success": false, "error": "unknown operation"}, nil
	}

	return map[string]any{
		"success": true,
		"result":  result,
	}, nil
}

func (t *calculatorTool) PromptDocumentation() string {
	return "Calculator: performs basic arithmetic (add, subtract, multiply, divide)"
}

// submitToolProvider implements a submit tool for testing terminal signals
type submitToolProvider struct{}

func newSubmitToolProvider() *submitToolProvider {
	return &submitToolProvider{}
}

func (p *submitToolProvider) Get(name string) (tools.Tool, error) {
	if name != "submit" {
		return nil, nil
	}
	return &submitTool{}, nil
}

func (p *submitToolProvider) List() []tools.ToolMeta {
	return []tools.ToolMeta{
		{
			Name:        "submit",
			Description: "Submit a final result",
			InputSchema: tools.InputSchema{
				Type: "object",
				Properties: map[string]tools.Property{
					"result": {
						Type:        "string",
						Description: "The result to submit",
					},
				},
				Required: []string{"result"},
			},
		},
	}
}

type submitTool struct{}

func (t *submitTool) Name() string {
	return "submit"
}

func (t *submitTool) Description() string {
	return "Submit a final result"
}

func (t *submitTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "submit",
		Description: "Submit a final result",
		InputSchema: tools.InputSchema{
			Type: "object",
			Properties: map[string]tools.Property{
				"result": {
					Type:        "string",
					Description: "The result to submit",
				},
			},
			Required: []string{"result"},
		},
	}
}

func (t *submitTool) Exec(_ context.Context, params map[string]any) (any, error) {
	result, _ := params["result"].(string)
	return map[string]any{
		"success": true,
		"result":  result,
	}, nil
}

func (t *submitTool) PromptDocumentation() string {
	return "Submit: submit a final result (terminal action)"
}
