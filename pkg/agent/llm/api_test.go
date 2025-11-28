package llm

import (
	"context"
	"io"
	"testing"
	"time"

	"orchestrator/pkg/tools"
)

// TestCompletionRole tests role constant values.
func TestCompletionRole(t *testing.T) {
	tests := []struct {
		name     string
		role     CompletionRole
		expected string
	}{
		{"system role", RoleSystem, "system"},
		{"user role", RoleUser, "user"},
		{"assistant role", RoleAssistant, "assistant"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.role) != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, string(tt.role))
			}
		})
	}
}

// TestConstants tests LLM constant values.
func TestConstants(t *testing.T) {
	if ArchitectMaxTokens != 5000 {
		t.Errorf("expected ArchitectMaxTokens=5000, got %d", ArchitectMaxTokens)
	}
	if TemperatureDefault != 0.3 {
		t.Errorf("expected TemperatureDefault=0.3, got %f", TemperatureDefault)
	}
	if TemperatureDeterministic != 0.2 {
		t.Errorf("expected TemperatureDeterministic=0.2, got %f", TemperatureDeterministic)
	}
}

// TestNewCompletionRequest tests completion request creation with defaults.
func TestNewCompletionRequest(t *testing.T) {
	messages := []CompletionMessage{
		{Role: RoleUser, Content: "test"},
	}

	req := NewCompletionRequest(messages)

	if len(req.Messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(req.Messages))
	}
	if req.MaxTokens != 4096 {
		t.Errorf("expected MaxTokens=4096, got %d", req.MaxTokens)
	}
	if req.Temperature != TemperatureDefault {
		t.Errorf("expected Temperature=%f, got %f", TemperatureDefault, req.Temperature)
	}
}

// TestNewSystemMessage tests system message creation.
func TestNewSystemMessage(t *testing.T) {
	content := "You are a helpful assistant"
	msg := NewSystemMessage(content)

	if msg.Role != RoleSystem {
		t.Errorf("expected role %q, got %q", RoleSystem, msg.Role)
	}
	if msg.Content != content {
		t.Errorf("expected content %q, got %q", content, msg.Content)
	}
}

// TestNewUserMessage tests user message creation.
func TestNewUserMessage(t *testing.T) {
	content := "Hello, world!"
	msg := NewUserMessage(content)

	if msg.Role != RoleUser {
		t.Errorf("expected role %q, got %q", RoleUser, msg.Role)
	}
	if msg.Content != content {
		t.Errorf("expected content %q, got %q", content, msg.Content)
	}
}

// TestLLMConfigValidate tests configuration validation.
func TestLLMConfigValidate(t *testing.T) {
	tests := []struct {
		name      string
		config    LLMConfig
		expectErr bool
		errMsg    string
	}{
		{
			name: "valid config",
			config: LLMConfig{
				APIKey:      "sk-test",
				ModelName:   "claude-3",
				MaxTokens:   4096,
				Temperature: 0.5,
			},
			expectErr: false,
		},
		{
			name: "empty API key",
			config: LLMConfig{
				ModelName:   "claude-3",
				MaxTokens:   4096,
				Temperature: 0.5,
			},
			expectErr: true,
			errMsg:    "API key cannot be empty",
		},
		{
			name: "empty model name",
			config: LLMConfig{
				APIKey:      "sk-test",
				MaxTokens:   4096,
				Temperature: 0.5,
			},
			expectErr: true,
			errMsg:    "model name cannot be empty",
		},
		{
			name: "zero max tokens",
			config: LLMConfig{
				APIKey:      "sk-test",
				ModelName:   "claude-3",
				MaxTokens:   0,
				Temperature: 0.5,
			},
			expectErr: true,
			errMsg:    "max tokens must be positive",
		},
		{
			name: "negative max tokens",
			config: LLMConfig{
				APIKey:      "sk-test",
				ModelName:   "claude-3",
				MaxTokens:   -100,
				Temperature: 0.5,
			},
			expectErr: true,
			errMsg:    "max tokens must be positive",
		},
		{
			name: "temperature too low",
			config: LLMConfig{
				APIKey:      "sk-test",
				ModelName:   "claude-3",
				MaxTokens:   4096,
				Temperature: -0.1,
			},
			expectErr: true,
			errMsg:    "temperature must be between 0.0 and 2.0",
		},
		{
			name: "temperature too high",
			config: LLMConfig{
				APIKey:      "sk-test",
				ModelName:   "claude-3",
				MaxTokens:   4096,
				Temperature: 2.1,
			},
			expectErr: true,
			errMsg:    "temperature must be between 0.0 and 2.0",
		},
		{
			name: "temperature at lower bound",
			config: LLMConfig{
				APIKey:      "sk-test",
				ModelName:   "claude-3",
				MaxTokens:   4096,
				Temperature: 0.0,
			},
			expectErr: false,
		},
		{
			name: "temperature at upper bound",
			config: LLMConfig{
				APIKey:      "sk-test",
				ModelName:   "claude-3",
				MaxTokens:   4096,
				Temperature: 2.0,
			},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.expectErr {
				if err == nil {
					t.Error("expected error, got nil")
				} else if err.Error() != tt.errMsg {
					t.Errorf("expected error %q, got %q", tt.errMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
			}
		})
	}
}

// TestStreamToReader tests stream to reader conversion.
func TestStreamToReader(t *testing.T) {
	tests := []struct {
		name     string
		chunks   []StreamChunk
		expected string
		hasError bool
	}{
		{
			name: "successful stream",
			chunks: []StreamChunk{
				{Content: "Hello", Done: false},
				{Content: " ", Done: false},
				{Content: "World", Done: true},
			},
			expected: "Hello World",
			hasError: false,
		},
		{
			name: "empty stream",
			chunks: []StreamChunk{
				{Content: "", Done: true},
			},
			expected: "",
			hasError: false,
		},
		{
			name: "stream with error",
			chunks: []StreamChunk{
				{Content: "Hello", Done: false},
				{Error: io.ErrUnexpectedEOF, Done: false},
			},
			expected: "Hello",
			hasError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create stream channel
			stream := make(chan StreamChunk, len(tt.chunks))
			for _, chunk := range tt.chunks {
				stream <- chunk
			}
			close(stream)

			// Convert to reader
			reader := StreamToReader(stream)

			// Read all content using io.ReadAll (handles multiple reads)
			content, err := io.ReadAll(reader)

			if tt.hasError {
				if err == nil {
					t.Error("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}

			got := string(content)
			if got != tt.expected {
				t.Errorf("expected content %q, got %q", tt.expected, got)
			}
		})
	}
}

// TestCompletionMessageStructure tests message structure with tool calls and results.
func TestCompletionMessageStructure(t *testing.T) {
	// Test message with tool calls
	msg := CompletionMessage{
		Role:    RoleAssistant,
		Content: "Let me check that for you",
		ToolCalls: []ToolCall{
			{
				ID:   "call_1",
				Name: "get_weather",
				Parameters: map[string]any{
					"location": "San Francisco",
				},
			},
		},
	}

	if msg.Role != RoleAssistant {
		t.Errorf("expected role %q, got %q", RoleAssistant, msg.Role)
	}
	if msg.Content != "Let me check that for you" {
		t.Errorf("expected content, got %q", msg.Content)
	}
	if len(msg.ToolCalls) != 1 {
		t.Errorf("expected 1 tool call, got %d", len(msg.ToolCalls))
	}
	if msg.ToolCalls[0].Name != "get_weather" {
		t.Errorf("expected tool name %q, got %q", "get_weather", msg.ToolCalls[0].Name)
	}

	// Test message with tool results
	resultMsg := CompletionMessage{
		Role:    RoleUser,
		Content: "Here are the results:",
		ToolResults: []ToolResult{
			{
				ToolCallID: "call_1",
				Content:    `{"temp": 72, "condition": "sunny"}`,
				IsError:    false,
			},
		},
	}

	if resultMsg.Role != RoleUser {
		t.Errorf("expected role %q, got %q", RoleUser, resultMsg.Role)
	}
	if resultMsg.Content != "Here are the results:" {
		t.Errorf("expected content, got %q", resultMsg.Content)
	}
	if len(resultMsg.ToolResults) != 1 {
		t.Errorf("expected 1 tool result, got %d", len(resultMsg.ToolResults))
	}
	if resultMsg.ToolResults[0].ToolCallID != "call_1" {
		t.Errorf("expected tool call ID %q, got %q", "call_1", resultMsg.ToolResults[0].ToolCallID)
	}
	if resultMsg.ToolResults[0].IsError {
		t.Error("expected IsError=false, got true")
	}
}

// TestCacheControl tests prompt caching configuration.
func TestCacheControl(t *testing.T) {
	cache := &CacheControl{
		Type: "ephemeral",
		TTL:  "5m",
	}

	msg := CompletionMessage{
		Role:         RoleSystem,
		Content:      "System instructions",
		CacheControl: cache,
	}

	if msg.Role != RoleSystem {
		t.Errorf("expected role %q, got %q", RoleSystem, msg.Role)
	}
	if msg.Content != "System instructions" {
		t.Errorf("expected content, got %q", msg.Content)
	}
	if msg.CacheControl == nil {
		t.Fatal("expected CacheControl to be set")
	}
	if msg.CacheControl.Type != "ephemeral" {
		t.Errorf("expected Type=%q, got %q", "ephemeral", msg.CacheControl.Type)
	}
	if msg.CacheControl.TTL != "5m" {
		t.Errorf("expected TTL=%q, got %q", "5m", msg.CacheControl.TTL)
	}
}

// TestCompletionRequestWithTools tests request with tool definitions.
func TestCompletionRequestWithTools(t *testing.T) {
	toolDefs := []tools.ToolDefinition{
		{
			Name:        "calculator",
			Description: "Perform calculations",
			InputSchema: tools.InputSchema{
				Type: "object",
				Properties: map[string]tools.Property{
					"operation": {
						Type:        "string",
						Description: "The operation to perform",
					},
				},
				Required: []string{"operation"},
			},
		},
	}

	req := CompletionRequest{
		Messages: []CompletionMessage{
			NewUserMessage("Calculate 2 + 2"),
		},
		Tools:       toolDefs,
		ToolChoice:  "required",
		MaxTokens:   1000,
		Temperature: 0.0,
	}

	if len(req.Messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(req.Messages))
	}
	if len(req.Tools) != 1 {
		t.Errorf("expected 1 tool, got %d", len(req.Tools))
	}
	if req.Tools[0].Name != "calculator" {
		t.Errorf("expected tool name %q, got %q", "calculator", req.Tools[0].Name)
	}
	if req.ToolChoice != "required" {
		t.Errorf("expected ToolChoice=%q, got %q", "required", req.ToolChoice)
	}
	if req.MaxTokens != 1000 {
		t.Errorf("expected MaxTokens=1000, got %d", req.MaxTokens)
	}
	if req.Temperature != 0.0 {
		t.Errorf("expected Temperature=0.0, got %f", req.Temperature)
	}
}

// mockLLMClient is a simple mock implementation for testing.
type mockLLMClient struct {
	completeFunc     func(context.Context, CompletionRequest) (CompletionResponse, error)
	streamFunc       func(context.Context, CompletionRequest) (<-chan StreamChunk, error)
	getModelNameFunc func() string
}

func (m *mockLLMClient) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	if m.completeFunc != nil {
		return m.completeFunc(ctx, req)
	}
	return CompletionResponse{Content: "mock response"}, nil
}

func (m *mockLLMClient) Stream(ctx context.Context, req CompletionRequest) (<-chan StreamChunk, error) {
	if m.streamFunc != nil {
		return m.streamFunc(ctx, req)
	}
	ch := make(chan StreamChunk)
	close(ch)
	return ch, nil
}

func (m *mockLLMClient) GetModelName() string {
	if m.getModelNameFunc != nil {
		return m.getModelNameFunc()
	}
	return "mock-model"
}

// TestLLMClientInterface verifies the interface works with a mock.
func TestLLMClientInterface(t *testing.T) {
	mock := &mockLLMClient{
		getModelNameFunc: func() string {
			return "test-model"
		},
	}

	ctx := context.Background()
	req := NewCompletionRequest([]CompletionMessage{
		NewUserMessage("test"),
	})

	// Test Complete
	resp, err := mock.Complete(ctx, req)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if resp.Content != "mock response" {
		t.Errorf("expected 'mock response', got %q", resp.Content)
	}

	// Test GetModelName
	modelName := mock.GetModelName()
	if modelName != "test-model" {
		t.Errorf("expected 'test-model', got %q", modelName)
	}

	// Test Stream
	stream, err := mock.Stream(ctx, req)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	// Should be closed immediately
	select {
	case _, ok := <-stream:
		if ok {
			t.Error("expected closed channel")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("stream channel should be closed")
	}
}
