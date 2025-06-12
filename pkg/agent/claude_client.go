package agent

import (
	"context"
	"fmt"
	"log"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// ClaudeClient wraps the Anthropic API client to implement LLMClient interface
type ClaudeClient struct {
	client anthropic.Client
	model  anthropic.Model
}

// NewClaudeClient creates a new Claude client wrapper with default retry logic
func NewClaudeClient(apiKey string) LLMClient {
	if SystemMode == ModeMock {
		return NewMockLLMClient([]CompletionResponse{{Content: "mock response"}}, nil)
	}

	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	baseClient := &ClaudeClient{
		client: client,
		model:  "claude-3-7-sonnet-20250219", // Default model
	}
	
	// Wrap with both circuit breaker and retry logic
	return NewResilientClient(baseClient)
}

// NewClaudeClientWithModel creates a new Claude client with specific model and retry logic
func NewClaudeClientWithModel(apiKey string, model string) LLMClient {
	if SystemMode == ModeMock {
		return NewMockLLMClient([]CompletionResponse{{Content: "mock response"}}, nil)
	}

	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	baseClient := &ClaudeClient{
		client: client,
		model:  anthropic.Model(model),
	}
	
	// Wrap with both circuit breaker and retry logic
	return NewResilientClient(baseClient)
}

// Complete implements the LLMClient interface
func (c *ClaudeClient) Complete(ctx context.Context, in CompletionRequest) (CompletionResponse, error) {
	if SystemMode == ModeDebug {
		log.Printf("Claude completing request with %d messages", len(in.Messages))
	}

	// Convert to Anthropic messages
	messages := make([]anthropic.BetaMessageParam, 0, len(in.Messages))
	for _, msg := range in.Messages {
		role := anthropic.BetaMessageParamRole(msg.Role)
		block := anthropic.NewBetaTextBlock(msg.Content)
		messages = append(messages, anthropic.BetaMessageParam{
			Role:    role,
			Content: []anthropic.BetaContentBlockParamUnion{block},
		})
	}

	// Make API request
	maxTokens := int64(in.MaxTokens)
	resp, err := c.client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
		Model:     c.model,
		Messages:  messages,
		MaxTokens: maxTokens,
	})

	if err != nil {
		return CompletionResponse{}, fmt.Errorf("failed to generate response from Claude: %w", err)
	}

	if resp == nil || len(resp.Content) == 0 {
		return CompletionResponse{}, fmt.Errorf("empty response from Claude")
	}

	// Extract text content from the response
	var responseText string
	for _, content := range resp.Content {
		if content.Type == "text" {
			responseText += content.Text
		}
	}

	if responseText == "" {
		return CompletionResponse{}, fmt.Errorf("no text content in Claude response")
	}

	return CompletionResponse{Content: responseText}, nil
}

// Stream implements the LLMClient interface
func (c *ClaudeClient) Stream(ctx context.Context, in CompletionRequest) (<-chan StreamChunk, error) {
	// Return mock stream for now
	ch := make(chan StreamChunk, 1)
	go func() {
		defer close(ch)
		resp, err := c.Complete(ctx, in)
		if err != nil {
			ch <- StreamChunk{Error: err}
			return
		}
		ch <- StreamChunk{Content: resp.Content}
		ch <- StreamChunk{Done: true}
	}()
	return ch, nil
}