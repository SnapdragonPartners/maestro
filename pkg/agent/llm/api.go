// Package llm provides interfaces and types for Large Language Model client implementations.
package llm

import (
	"context"
	"fmt"
	"io"

	"orchestrator/pkg/config"
	"orchestrator/pkg/tools"
)

// CompletionRole represents the role of a message in a conversation.
type CompletionRole string

const (
	// RoleSystem indicates a system message that provides instructions or context.
	RoleSystem CompletionRole = "system"
	// RoleUser indicates a message from the human user.
	RoleUser CompletionRole = "user"
	// RoleAssistant indicates a message from the AI assistant.
	RoleAssistant CompletionRole = "assistant"
)

const (
	// ArchitectMaxTokens defines the maximum tokens for architect LLM responses.
	// Used for comprehensive spec analysis and story generation with O3.
	ArchitectMaxTokens = 30000
)

// CompletionMessage represents a message in a completion request.
type CompletionMessage struct {
	Role    CompletionRole
	Content string
}

// Use tools.ToolDefinition directly instead of separate agent.Tool.

// ToolCall represents a tool call made by the LLM.
type ToolCall struct {
	Parameters map[string]any `json:"parameters"`
	ID         string         `json:"id"`
	Name       string         `json:"name"`
}

// CompletionRequest represents a request to generate a completion.
type CompletionRequest struct {
	Messages    []CompletionMessage
	Tools       []tools.ToolDefinition
	Temperature float32
	MaxTokens   int
}

// CompletionResponse represents a response from a completion request.
type CompletionResponse struct {
	Content   string
	ToolCalls []ToolCall
}

// StreamChunk represents a chunk of streamed completion response.
type StreamChunk struct {
	Error   error
	Content string
	Done    bool
}

// LLMClient defines the interface for language model interactions.
type LLMClient interface { //nolint:revive // Keep name for backward compatibility
	// Complete generates a completion synchronously.
	Complete(ctx context.Context, in CompletionRequest) (CompletionResponse, error)

	// Stream generates a completion as a stream of chunks.
	Stream(ctx context.Context, in CompletionRequest) (<-chan StreamChunk, error)

	// GetDefaultConfig returns default model configuration for this LLM client.
	GetDefaultConfig() config.Model
}

// NewCompletionRequest creates a new completion request with default values.
func NewCompletionRequest(messages []CompletionMessage) CompletionRequest {
	return CompletionRequest{
		Messages:    messages,
		MaxTokens:   4096, // Default to 4k tokens
		Temperature: 0.7,  // Default temperature
	}
}

// NewSystemMessage creates a new system message.
func NewSystemMessage(content string) CompletionMessage {
	return CompletionMessage{
		Role:    RoleSystem,
		Content: content,
	}
}

// NewUserMessage creates a new user message.
func NewUserMessage(content string) CompletionMessage {
	return CompletionMessage{
		Role:    RoleUser,
		Content: content,
	}
}

// LLMConfig represents configuration for an LLM client.
type LLMConfig struct { //nolint:revive // Keep name for backward compatibility
	APIKey           string
	ModelName        string
	MaxTokens        int
	Temperature      float32
	MaxContextTokens int
	MaxOutputTokens  int
	CompactIfOver    int
}

// Validate validates the LLM configuration.
func (c *LLMConfig) Validate() error {
	if c.APIKey == "" {
		return fmt.Errorf("API key cannot be empty")
	}
	if c.ModelName == "" {
		return fmt.Errorf("model name cannot be empty")
	}
	if c.MaxTokens <= 0 {
		return fmt.Errorf("max tokens must be positive")
	}
	if c.Temperature < 0.0 || c.Temperature > 2.0 {
		return fmt.Errorf("temperature must be between 0.0 and 2.0")
	}
	return nil
}

// StreamToReader converts a stream channel to an io.Reader.
func StreamToReader(stream <-chan StreamChunk) io.Reader {
	pr, pw := io.Pipe()

	go func() {
		defer func() {
			if err := pw.Close(); err != nil {
				// Log error but don't fail the stream processing.
				// This is cleanup code in a streaming context.
				_ = err // Ignore error in cleanup
			}
		}()
		for chunk := range stream {
			if chunk.Error != nil {
				pw.CloseWithError(chunk.Error)
				return
			}
			if _, err := pw.Write([]byte(chunk.Content)); err != nil {
				pw.CloseWithError(err)
				return
			}
			if chunk.Done {
				return
			}
		}
	}()

	return pr
}
