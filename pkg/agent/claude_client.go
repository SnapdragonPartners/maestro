package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/anthropics/anthropic-sdk-go"
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
		model:  "claude-3-5-sonnet-20241022", // Default model
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
		log.Printf("Claude completing request with %d messages, %d tools", len(in.Messages), len(in.Tools))
	}

	// Convert to Anthropic messages
	messages := make([]anthropic.MessageParam, 0, len(in.Messages))
	for _, msg := range in.Messages {
		role := anthropic.MessageParamRole(msg.Role)
		block := anthropic.NewTextBlock(msg.Content)
		messages = append(messages, anthropic.MessageParam{
			Role:    role,
			Content: []anthropic.ContentBlockParamUnion{block},
		})
	}

	// Prepare request parameters
	maxTokens := int64(in.MaxTokens)
	params := anthropic.MessageNewParams{
		Model:     c.model,
		Messages:  messages,
		MaxTokens: maxTokens,
	}

	// Add tools if provided using correct v1.5.0 API
	if len(in.Tools) > 0 {
		if SystemMode == ModeDebug {
			log.Printf("Processing %d tools for Claude API", len(in.Tools))
		}
		var tools []anthropic.ToolUnionParam
		for _, tool := range in.Tools {
			if SystemMode == ModeDebug {
				log.Printf("Adding tool: %s", tool.Name)
			}
			// Build the tool schema from the tool parameters
			var properties any
			var required []string

			// For JSON schema, we can just pass the entire schema structure
			if props, ok := tool.Parameters["properties"]; ok {
				// Pass the properties structure directly
				properties = props
			}
			if reqFields, ok := tool.Parameters["required"].([]any); ok {
				for _, field := range reqFields {
					if fieldStr, ok := field.(string); ok {
						required = append(required, fieldStr)
					}
				}
			}

			toolParam := anthropic.ToolParam{
				Name:        tool.Name,
				Description: anthropic.String(tool.Description),
				InputSchema: anthropic.ToolInputSchemaParam{
					Type:       "object",
					Properties: properties,
					Required:   required,
				},
			}
			tools = append(tools, anthropic.ToolUnionParamOfTool(toolParam.InputSchema, toolParam.Name))
		}
		params.Tools = tools
		// Set tool choice to auto so Claude will decide when to use tools
		params.ToolChoice = anthropic.ToolChoiceUnionParam{
			OfAuto: &anthropic.ToolChoiceAutoParam{},
		}
	}

	// Make API request
	resp, err := c.client.Messages.New(ctx, params)

	if err != nil {
		return CompletionResponse{}, fmt.Errorf("failed to generate response from Claude: %w", err)
	}

	if resp == nil || len(resp.Content) == 0 {
		return CompletionResponse{}, fmt.Errorf("empty response from Claude")
	}

	// Extract text content and tool calls from the response using v1.5.0 API
	var responseText string
	var toolCalls []ToolCall

	if SystemMode == ModeDebug {
		log.Printf("Claude response has %d content blocks", len(resp.Content))
	}

	for _, block := range resp.Content {
		if SystemMode == ModeDebug {
			log.Printf("Processing content block type: %s", block.Type)
		}
		switch block.Type {
		case "text":
			textBlock := block.AsText()
			responseText += textBlock.Text
		case "tool_use":
			toolUseBlock := block.AsToolUse()
			if SystemMode == ModeDebug {
				log.Printf("Found tool use: %s", toolUseBlock.Name)
			}
			// Parse the input parameters from RawMessage
			var params map[string]any
			if err := json.Unmarshal(toolUseBlock.Input, &params); err != nil {
				return CompletionResponse{}, fmt.Errorf("failed to parse tool input: %w", err)
			}

			toolCall := ToolCall{
				ID:         toolUseBlock.ID,
				Name:       toolUseBlock.Name,
				Parameters: params,
			}
			toolCalls = append(toolCalls, toolCall)
		}
	}

	if SystemMode == ModeDebug {
		log.Printf("Claude response: %d tool calls extracted", len(toolCalls))
		if len(toolCalls) > 0 {
			for i, tc := range toolCalls {
				log.Printf("Tool call %d: %s (id: %s)", i+1, tc.Name, tc.ID)
			}
		}
		log.Printf("Claude response text length: %d characters", len(responseText))
	}

	return CompletionResponse{
		Content:   responseText,
		ToolCalls: toolCalls,
	}, nil
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
