package google

import (
	"testing"

	"google.golang.org/genai"

	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/tools"
)

// TestNewGeminiClientWithModel tests client creation with custom model.
func TestNewGeminiClientWithModel(t *testing.T) {
	client := NewGeminiClientWithModel("test-api-key", "gemini-3-pro-preview")

	if client == nil {
		t.Fatal("expected client, got nil")
	}

	// Verify it implements the interface
	var _ llm.LLMClient = client
}

// TestGetModelName tests model name retrieval.
func TestGetModelName(t *testing.T) {
	client := NewGeminiClientWithModel("test-key", "gemini-2.5-flash")

	modelName := client.GetModelName()

	if modelName != "gemini-2.5-flash" {
		t.Errorf("expected model %q, got %q", "gemini-2.5-flash", modelName)
	}
}

// TestConvertMessagesToGemini tests message conversion logic.
func TestConvertMessagesToGemini(t *testing.T) {
	tests := []struct {
		name             string
		messages         []llm.CompletionMessage
		cache            []*genai.Content
		expectSystem     string
		expectContentLen int
		expectErr        bool
		errContains      string
	}{
		{
			name:        "empty messages",
			messages:    []llm.CompletionMessage{},
			expectErr:   true,
			errContains: "message list cannot be empty",
		},
		{
			name: "system message extracted",
			messages: []llm.CompletionMessage{
				{Role: llm.RoleSystem, Content: "You are helpful"},
				{Role: llm.RoleUser, Content: "Hello"},
			},
			expectSystem:     "You are helpful",
			expectContentLen: 1,
			expectErr:        false,
		},
		{
			name: "multiple system messages concatenated",
			messages: []llm.CompletionMessage{
				{Role: llm.RoleSystem, Content: "You are helpful"},
				{Role: llm.RoleSystem, Content: "And concise"},
				{Role: llm.RoleUser, Content: "Hello"},
			},
			expectSystem:     "You are helpful\n\nAnd concise",
			expectContentLen: 1,
			expectErr:        false,
		},
		{
			name: "user and assistant messages",
			messages: []llm.CompletionMessage{
				{Role: llm.RoleUser, Content: "Hello"},
				{Role: llm.RoleAssistant, Content: "Hi there"},
			},
			expectSystem:     "",
			expectContentLen: 2,
			expectErr:        false,
		},
		{
			name: "tool call message",
			messages: []llm.CompletionMessage{
				{Role: llm.RoleUser, Content: "What's the weather?"},
				{
					Role: llm.RoleAssistant,
					ToolCalls: []llm.ToolCall{
						{ID: "call_1", Name: "get_weather", Parameters: map[string]any{"city": "SF"}},
					},
				},
				{Role: llm.RoleUser, Content: "Thanks"},
			},
			expectSystem:     "",
			expectContentLen: 3,
			expectErr:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			contents, system, err := convertMessagesToGemini(tt.messages, tt.cache)

			if tt.expectErr {
				if err == nil {
					t.Error("expected error, got nil")
				} else if tt.errContains != "" && !contains(err.Error(), tt.errContains) {
					t.Errorf("expected error containing %q, got %q", tt.errContains, err.Error())
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if system != tt.expectSystem {
				t.Errorf("expected system %q, got %q", tt.expectSystem, system)
			}

			if len(contents) != tt.expectContentLen {
				t.Errorf("expected %d contents, got %d", tt.expectContentLen, len(contents))
			}
		})
	}
}

// TestConvertToolsToGemini tests tool definition conversion.
func TestConvertToolsToGemini(t *testing.T) {
	tool := tools.ToolDefinition{
		Name:        "calculator",
		Description: "Perform calculations",
		InputSchema: tools.InputSchema{
			Type: "object",
			Properties: map[string]tools.Property{
				"operation": {
					Type:        "string",
					Description: "The operation",
					Enum:        []string{"add", "subtract"},
				},
				"a": {
					Type:        "number",
					Description: "First number",
				},
			},
			Required: []string{"operation", "a"},
		},
	}

	result := convertToolsToGemini([]tools.ToolDefinition{tool})

	if len(result) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result))
	}

	converted := result[0]

	if converted.Name != "calculator" {
		t.Errorf("expected name %q, got %q", "calculator", converted.Name)
	}

	if converted.Description != "Perform calculations" {
		t.Errorf("expected description %q, got %q", "Perform calculations", converted.Description)
	}

	if converted.Parameters == nil {
		t.Fatal("expected parameters to be set")
	}

	if converted.Parameters.Type != genai.TypeObject {
		t.Errorf("expected type object, got %v", converted.Parameters.Type)
	}
}

// TestConvertFunctionCallsFromGemini tests function call conversion.
func TestConvertFunctionCallsFromGemini(t *testing.T) {
	calls := []*genai.FunctionCall{
		{
			ID:   "call_123",
			Name: "get_weather",
			Args: map[string]any{
				"location": "San Francisco",
			},
		},
		{
			// Gemini may not provide ID
			Name: "calculate",
			Args: map[string]any{
				"operation": "add",
				"a":         5,
				"b":         3,
			},
		},
	}

	result := convertFunctionCallsFromGemini(calls)

	if len(result) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(result))
	}

	// First call has ID
	if result[0].ID != "call_123" {
		t.Errorf("expected ID %q, got %q", "call_123", result[0].ID)
	}
	if result[0].Name != "get_weather" {
		t.Errorf("expected name %q, got %q", "get_weather", result[0].Name)
	}

	// Second call uses name as ID fallback
	if result[1].ID != "calculate" {
		t.Errorf("expected ID to fallback to name %q, got %q", "calculate", result[1].ID)
	}
	if result[1].Name != "calculate" {
		t.Errorf("expected name %q, got %q", "calculate", result[1].Name)
	}
}

// contains is a helper to check if a string contains a substring.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && hasSubstring(s, substr)))
}

func hasSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
