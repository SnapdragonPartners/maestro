//nolint:all // Legacy test file - needs migration to new APIs
package agent

import (
	"context"
	"log"
	"testing"
)

func TestLLMConfigBasic(t *testing.T) {
	config := &LLMConfig{
		MaxTokens:   1000,
		Temperature: 0.7,
		ModelName:   "test-model",
	}

	if config.MaxTokens != 1000 {
		t.Error("Expected MaxTokens to be set correctly")
	}
	if config.Temperature != 0.7 {
		t.Error("Expected Temperature to be set correctly")
	}
	if config.ModelName != "test-model" {
		t.Error("Expected ModelName to be set correctly")
	}
}

func TestNewConfig(t *testing.T) {
	ctx := Context{
		Context: context.Background(),
		Logger:  log.Default(),
	}

	config := NewConfig("test-agent", "coder", ctx)

	if config.ID != "test-agent" {
		t.Errorf("Expected ID 'test-agent', got '%s'", config.ID)
	}
	if config.Type != "coder" {
		t.Errorf("Expected type 'coder', got '%s'", config.Type)
	}
}

func TestConfigWithLLM(t *testing.T) {
	ctx := Context{
		Context: context.Background(),
		Logger:  log.Default(),
	}

	config := NewConfig("test-agent", "coder", ctx)
	llmConfig := &LLMConfig{
		MaxTokens: 1000,
		ModelName: "test-model",
	}

	newConfig := config.WithLLM(llmConfig)
	if newConfig.LLMConfig != llmConfig {
		t.Error("Expected LLM config to be set")
	}
	if config.LLMConfig == nil {
		t.Error("Expected original config to be modified")
	}
}

func TestCompletionMessage(t *testing.T) {
	message := CompletionMessage{
		Role:    RoleUser,
		Content: "Test message",
	}

	if message.Role != RoleUser {
		t.Error("Expected role to be set correctly")
	}
	if message.Content != "Test message" {
		t.Error("Expected content to be set correctly")
	}
}

func TestCompletionRequest(t *testing.T) {
	req := CompletionRequest{
		Messages: []CompletionMessage{
			{Role: RoleUser, Content: "Hello"},
		},
		MaxTokens:   1000,
		Temperature: 0.7,
	}

	if len(req.Messages) != 1 {
		t.Error("Expected 1 message")
	}
	if req.MaxTokens != 1000 {
		t.Error("Expected max tokens to match")
	}
	if req.Temperature != 0.7 {
		t.Error("Expected temperature to match")
	}
}
