package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// ClaudeClient wraps the Anthropic API client to implement LLMClient interface.
//
//nolint:govet // Simple client struct, logical grouping preferred
type ClaudeClient struct {
	client anthropic.Client
	model  anthropic.Model
}

// NewClaudeClient creates a new Claude client wrapper with default retry logic.
func NewClaudeClient(apiKey string) LLMClient {
	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	baseClient := &ClaudeClient{
		client: client,
		model:  "claude-3-5-sonnet-20241022", // Default model
	}

	// Wrap with both circuit breaker and retry logic.
	return NewResilientClient(baseClient)
}

// NewClaudeClientWithModel creates a new Claude client with specific model and retry logic.
func NewClaudeClientWithModel(apiKey, model string) LLMClient {
	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	baseClient := &ClaudeClient{
		client: client,
		model:  anthropic.Model(model),
	}

	// Wrap with both circuit breaker and retry logic.
	return NewResilientClient(baseClient)
}

// Complete implements the LLMClient interface.
func (c *ClaudeClient) Complete(ctx context.Context, in CompletionRequest) (CompletionResponse, error) {
	// Convert to Anthropic messages.
	messages := make([]anthropic.MessageParam, 0, len(in.Messages))
	for i := range in.Messages {
		msg := &in.Messages[i]
		role := anthropic.MessageParamRole(msg.Role)
		block := anthropic.NewTextBlock(msg.Content)
		messages = append(messages, anthropic.MessageParam{
			Role:    role,
			Content: []anthropic.ContentBlockParamUnion{block},
		})
	}

	// Prepare request parameters.
	maxTokens := int64(in.MaxTokens)
	params := anthropic.MessageNewParams{
		Model:     c.model,
		Messages:  messages,
		MaxTokens: maxTokens,
	}

	// Add tools if provided using correct v1.5.0 API.
	if len(in.Tools) > 0 {
		var tools []anthropic.ToolUnionParam
		for i := range in.Tools {
			tool := &in.Tools[i]
			// Convert tools.ToolDefinition directly to Anthropic format.
			var properties any
			var required []string

			// Convert InputSchema properties to the format expected by Anthropic API.
			if len(tool.InputSchema.Properties) > 0 {
				props := make(map[string]any)
				for name := range tool.InputSchema.Properties { //nolint:gocritic // Need to copy properties
					prop := tool.InputSchema.Properties[name]
					propMap := make(map[string]any)
					propMap["type"] = prop.Type
					if prop.Description != "" {
						propMap["description"] = prop.Description
					}
					if len(prop.Enum) > 0 {
						propMap["enum"] = prop.Enum
					}
					props[name] = propMap
				}
				properties = props
			}

			// Convert required fields.
			if len(tool.InputSchema.Required) > 0 {
				required = tool.InputSchema.Required
			}

			toolParam := anthropic.ToolParam{
				Name: tool.Name,
				// Description: anthropic.String(tool.Description), // Not used in current API
				InputSchema: anthropic.ToolInputSchemaParam{
					Type:       "object",
					Properties: properties,
					Required:   required,
				},
			}
			tools = append(tools, anthropic.ToolUnionParamOfTool(toolParam.InputSchema, toolParam.Name))
		}
		params.Tools = tools
		// Set tool choice to auto so Claude will decide when to use tools.
		params.ToolChoice = anthropic.ToolChoiceUnionParam{
			OfAuto: &anthropic.ToolChoiceAutoParam{},
		}
	}

	// Make API request.
	resp, err := c.client.Messages.New(ctx, params)

	if err != nil {
		return CompletionResponse{}, fmt.Errorf("failed to generate response from Claude: %w", err)
	}

	if resp == nil || len(resp.Content) == 0 {
		return CompletionResponse{}, fmt.Errorf("empty response from Claude")
	}

	// Extract text content and tool calls from the response using v1.5.0 API.
	var responseText string
	var toolCalls []ToolCall

	for i := range resp.Content {
		block := &resp.Content[i]
		switch block.Type {
		case "text":
			textBlock := block.AsText()
			responseText += textBlock.Text
		case "tool_use":
			toolUseBlock := block.AsToolUse()
			// Parse the input parameters from RawMessage.
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

	return CompletionResponse{
		Content:   responseText,
		ToolCalls: toolCalls,
	}, nil
}

// Stream implements the LLMClient interface.
func (c *ClaudeClient) Stream(ctx context.Context, in CompletionRequest) (<-chan StreamChunk, error) {
	// Return mock stream for now.
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
