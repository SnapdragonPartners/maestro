package agent

import (
	"context"
	"fmt"

	anthropic "github.com/liushuangls/go-anthropic/v2"
)

// ClaudeClient wraps the Anthropic API client to implement LLMClient interface
type ClaudeClient struct {
	client *anthropic.Client
	model  anthropic.Model
}

// NewClaudeClient creates a new Claude client wrapper
func NewClaudeClient(apiKey string) *ClaudeClient {
	return &ClaudeClient{
		client: anthropic.NewClient(apiKey),
		model:  anthropic.ModelClaude3Sonnet20240229, // Default model
	}
}

// NewClaudeClientWithModel creates a new Claude client with specific model
func NewClaudeClientWithModel(apiKey string, model anthropic.Model) *ClaudeClient {
	return &ClaudeClient{
		client: anthropic.NewClient(apiKey),
		model:  model,
	}
}

// GenerateResponse implements the LLMClient interface
func (c *ClaudeClient) GenerateResponse(ctx context.Context, prompt string) (string, error) {
	resp, err := c.client.CreateMessages(ctx, anthropic.MessagesRequest{
		Model: c.model,
		Messages: []anthropic.Message{
			{
				Role: anthropic.RoleUser,
				Content: []anthropic.MessageContent{
					anthropic.NewTextMessageContent(prompt),
				},
			},
		},
		MaxTokens: 4096,
	})

	if err != nil {
		return "", fmt.Errorf("failed to generate response from Claude: %w", err)
	}

	if len(resp.Content) == 0 {
		return "", fmt.Errorf("empty response from Claude")
	}

	// Extract text content from the response
	var responseText string
	for _, content := range resp.Content {
		if content.Type == "text" {
			responseText += content.GetText()
		}
	}

	if responseText == "" {
		return "", fmt.Errorf("no text content in Claude response")
	}

	return responseText, nil
}