package agent

import (
	"testing"

	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
)

func TestNewClaudeClient(t *testing.T) {
	client := NewClaudeClient("test-api-key")
	if client == nil {
		t.Error("Expected non-nil Claude client")
	}
}

func TestNewClaudeClientWithLogger(t *testing.T) {
	logger := logx.NewLogger("test")
	client := NewClaudeClientWithLogger("test-api-key", logger)
	if client == nil {
		t.Error("Expected non-nil Claude client with logger")
	}
}

func TestNewClaudeClientWithModel(t *testing.T) {
	client := NewClaudeClientWithModel("test-api-key", config.ModelClaudeSonnetLatest)
	if client == nil {
		t.Error("Expected non-nil Claude client with model")
	}
}

func TestNewClaudeClientWithModelAndLogger(t *testing.T) {
	logger := logx.NewLogger("test")
	client := NewClaudeClientWithModelAndLogger("test-api-key", config.ModelClaudeSonnetLatest, logger)
	if client == nil {
		t.Error("Expected non-nil Claude client with model and logger")
	}
}

func TestNewO3Client(t *testing.T) {
	client := NewO3Client("test-api-key")
	if client == nil {
		t.Error("Expected non-nil O3 client")
	}
}

func TestNewO3ClientWithModel(t *testing.T) {
	client := NewO3ClientWithModel("test-api-key", "o3-mini")
	if client == nil {
		t.Error("Expected non-nil O3 client with model")
	}
}

func TestNewCompletionRequest(t *testing.T) {
	messages := []CompletionMessage{
		{Role: RoleUser, Content: "Hello"},
	}

	req := NewCompletionRequest(messages)
	if len(req.Messages) != 1 {
		t.Error("Expected 1 message in request")
	}
	if req.MaxTokens != 4096 {
		t.Error("Expected default max tokens to be 4096")
	}
	if req.Temperature != 0.0 {
		t.Errorf("Expected default temperature to be 0.0 (unset), got %v", req.Temperature)
	}
}

func TestNewSystemMessage(t *testing.T) {
	msg := NewSystemMessage("System prompt")
	if msg.Role != RoleSystem {
		t.Error("Expected system role")
	}
	if msg.Content != "System prompt" {
		t.Error("Expected content to match")
	}
}

func TestNewUserMessage(t *testing.T) {
	msg := NewUserMessage("User message")
	if msg.Role != RoleUser {
		t.Error("Expected user role")
	}
	if msg.Content != "User message" {
		t.Error("Expected content to match")
	}
}

func TestCompletionRoles(t *testing.T) {
	// Test role constants
	if RoleSystem != "system" {
		t.Error("Expected system role constant to be 'system'")
	}
	if RoleUser != "user" {
		t.Error("Expected user role constant to be 'user'")
	}
	if RoleAssistant != "assistant" {
		t.Error("Expected assistant role constant to be 'assistant'")
	}
}
