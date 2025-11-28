package anthropic

import (
	"testing"

	"orchestrator/pkg/agent/llm"
)

// TestEnsureAlternation tests the message alternation logic.
func TestEnsureAlternation(t *testing.T) {
	tests := []struct {
		name          string
		input         []llm.CompletionMessage
		expectSystem  string
		expectMsgLen  int
		expectErr     bool
		errContains   string
	}{
		{
			name:         "empty messages",
			input:        []llm.CompletionMessage{},
			expectErr:    true,
			errContains:  "message list cannot be empty",
		},
		{
			name: "system message extracted",
			input: []llm.CompletionMessage{
				{Role: llm.RoleSystem, Content: "You are helpful"},
				{Role: llm.RoleUser, Content: "Hello"},
			},
			expectSystem: "You are helpful",
			expectMsgLen: 1,
			expectErr:    false,
		},
		{
			name: "multiple system messages concatenated",
			input: []llm.CompletionMessage{
				{Role: llm.RoleSystem, Content: "You are helpful"},
				{Role: llm.RoleSystem, Content: "And concise"},
				{Role: llm.RoleUser, Content: "Hello"},
			},
			expectSystem: "You are helpful\n\nAnd concise",
			expectMsgLen: 1,
			expectErr:    false,
		},
		{
			name: "proper alternation maintained",
			input: []llm.CompletionMessage{
				{Role: llm.RoleUser, Content: "Hello"},
				{Role: llm.RoleAssistant, Content: "Hi"},
				{Role: llm.RoleUser, Content: "How are you?"},
			},
			expectSystem: "",
			expectMsgLen: 3,
			expectErr:    false,
		},
		{
			name: "consecutive user messages merged",
			input: []llm.CompletionMessage{
				{Role: llm.RoleUser, Content: "Hello"},
				{Role: llm.RoleUser, Content: "Anyone there?"},
			},
			expectSystem: "",
			expectMsgLen: 1,
			expectErr:    false,
		},
		{
			name: "ends with assistant returns error",
			input: []llm.CompletionMessage{
				{Role: llm.RoleUser, Content: "Hello"},
				{Role: llm.RoleAssistant, Content: "Hi"},
			},
			expectErr:   true,
			errContains: "last message must be user",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			system, msgs, err := ensureAlternation(tt.input)

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

			if len(msgs) != tt.expectMsgLen {
				t.Errorf("expected %d messages, got %d", tt.expectMsgLen, len(msgs))
			}
		})
	}
}

// TestValidatePreSend tests the pre-send validation logic.
func TestValidatePreSend(t *testing.T) {
	tests := []struct {
		name        string
		messages    []llm.CompletionMessage
		expectErr   bool
		errContains string
	}{
		{
			name: "valid alternating messages",
			messages: []llm.CompletionMessage{
				{Role: llm.RoleUser, Content: "Hello"},
				{Role: llm.RoleAssistant, Content: "Hi"},
				{Role: llm.RoleUser, Content: "Bye"},
			},
			expectErr: false,
		},
		{
			name: "system message in array",
			messages: []llm.CompletionMessage{
				{Role: llm.RoleUser, Content: "Hello"},
				{Role: llm.RoleSystem, Content: "You are helpful"},
			},
			expectErr:   true,
			errContains: "system message found",
		},
		{
			name: "consecutive user messages",
			messages: []llm.CompletionMessage{
				{Role: llm.RoleUser, Content: "Hello"},
				{Role: llm.RoleUser, Content: "Anyone?"},
			},
			expectErr:   true,
			errContains: "alternation violation",
		},
		{
			name: "consecutive assistant messages",
			messages: []llm.CompletionMessage{
				{Role: llm.RoleUser, Content: "Hello"},
				{Role: llm.RoleAssistant, Content: "Hi"},
				{Role: llm.RoleAssistant, Content: "There"},
			},
			expectErr:   true,
			errContains: "alternation violation",
		},
		{
			name: "starts with assistant",
			messages: []llm.CompletionMessage{
				{Role: llm.RoleAssistant, Content: "Hello"},
			},
			expectErr:   true,
			errContains: "first message must be user",
		},
		{
			name: "ends with assistant",
			messages: []llm.CompletionMessage{
				{Role: llm.RoleUser, Content: "Hello"},
				{Role: llm.RoleAssistant, Content: "Hi"},
			},
			expectErr:   true,
			errContains: "last message must be user",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePreSend("test-model", tt.messages)

			if tt.expectErr {
				if err == nil {
					t.Error("expected error, got nil")
				} else if tt.errContains != "" && !contains(err.Error(), tt.errContains) {
					t.Errorf("expected error containing %q, got %q", tt.errContains, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}


// TestGetModelName tests model name retrieval.
func TestGetModelName(t *testing.T) {
	client := NewClaudeClientWithModel("test-key", "claude-3-opus-20240229")

	modelName := client.GetModelName()

	if modelName != "claude-3-opus-20240229" {
		t.Errorf("expected model %q, got %q", "claude-3-opus-20240229", modelName)
	}
}


// TestNewClaudeClient tests client creation.
func TestNewClaudeClient(t *testing.T) {
	client := NewClaudeClient("test-api-key")

	if client == nil {
		t.Fatal("expected client, got nil")
	}

	// Verify it implements the interface
	var _ llm.LLMClient = client
}

// TestNewClaudeClientWithModel tests client creation with custom model.
func TestNewClaudeClientWithModel(t *testing.T) {
	client := NewClaudeClientWithModel("test-api-key", "claude-3-sonnet-20240229")

	if client == nil {
		t.Fatal("expected client, got nil")
	}

	modelName := client.GetModelName()
	if modelName != "claude-3-sonnet-20240229" {
		t.Errorf("expected model %q, got %q", "claude-3-sonnet-20240229", modelName)
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
