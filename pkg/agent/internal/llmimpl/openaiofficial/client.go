// Package openaiofficial provides OpenAI client implementation using the official OpenAI Go package.
package openaiofficial

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"

	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/config"
	"orchestrator/pkg/tools"
)

// OfficialClient wraps the official OpenAI Go client to implement llm.LLMClient interface.
//
//nolint:govet // Simple struct, field alignment not critical for placeholder
type OfficialClient struct {
	client openai.Client
	model  string
}

// NewOfficialClient creates a new OpenAI client using the official Go package (raw client, middleware applied at higher level).
func NewOfficialClient(apiKey string) llm.LLMClient {
	return NewOfficialClientWithModel(apiKey, config.ModelGPT5)
}

// NewOfficialClientWithModel creates a new OpenAI client with specific model using the official package (raw client, middleware applied at higher level).
func NewOfficialClientWithModel(apiKey, model string) llm.LLMClient {
	client := openai.NewClient(option.WithAPIKey(apiKey))
	return &OfficialClient{
		client: client,
		model:  model,
	}
}

// convertPropertyToSchema recursively converts a Property to OpenAI schema format.
func convertPropertyToSchema(prop *tools.Property) map[string]interface{} {
	schema := map[string]interface{}{
		"type":        prop.Type,
		"description": prop.Description,
	}

	// Add enum if present
	if len(prop.Enum) > 0 {
		schema["enum"] = prop.Enum
	}

	// Handle array items recursively
	if prop.Type == "array" && prop.Items != nil {
		schema["items"] = convertPropertyToSchema(prop.Items)
	}

	// Handle object properties recursively
	if prop.Type == "object" && prop.Properties != nil {
		properties := make(map[string]interface{})
		for name, childProp := range prop.Properties {
			if childProp != nil {
				properties[name] = convertPropertyToSchema(childProp)
			}
		}
		schema["properties"] = properties
	}

	return schema
}

// Complete implements the llm.LLMClient interface using Responses API for optimal GPT-5 performance.
//
//nolint:gocritic // 80 bytes is reasonable for interface compliance
func (o *OfficialClient) Complete(ctx context.Context, in llm.CompletionRequest) (llm.CompletionResponse, error) {
	// Combine messages into a single input string for responses API
	var inputText string
	for i := range in.Messages {
		msg := &in.Messages[i]
		if msg.Role == "system" {
			inputText += fmt.Sprintf("System: %s\n\n", msg.Content)
		} else if msg.Role == "user" {
			inputText += msg.Content
		} else if msg.Role == "assistant" {
			inputText += fmt.Sprintf("Assistant: %s\n\n", msg.Content)
		}
	}

	// Cap MaxTokens to model's actual limit to prevent API errors
	maxTokens := in.MaxTokens
	if modelInfo, exists := config.KnownModels[o.model]; exists && modelInfo.MaxOutputTokens > 0 {
		if maxTokens > modelInfo.MaxOutputTokens {
			maxTokens = modelInfo.MaxOutputTokens
		}
	}

	// Create responses request params with GPT-5 optimized settings
	params := responses.ResponseNewParams{
		Model:           o.model,
		MaxOutputTokens: openai.Int(int64(maxTokens)),
		Input:           responses.ResponseNewParamsInputUnion{OfString: openai.String(inputText)},
		// TODO: HARD-CODED GPT-5 PARAMETERS - make configurable later
		// These parameters optimize GPT-5 for faster responses while maintaining quality
		// Based on: https://platform.openai.com/docs/guides/latest-model
	}

	// TODO: Add GPT-5 specific parameters once SDK supports them
	// For now, the increased timeout should allow GPT-5 to complete reasoning
	// Future parameters to add:
	// - Reasoning: { effort: "minimal" } - faster responses, still good for most tasks
	// - Text: { verbosity: "medium" } - balanced output length

	// Add tools if provided using responses API format
	if len(in.Tools) > 0 {
		tools := make([]responses.ToolUnionParam, len(in.Tools))
		for i := range in.Tools {
			tool := &in.Tools[i]
			// Convert tool definition to responses API format
			properties := make(map[string]interface{})
			for name, prop := range tool.InputSchema.Properties {
				properties[name] = convertPropertyToSchema(&prop)
			}

			tools[i] = responses.ToolUnionParam{
				OfFunction: &responses.FunctionToolParam{
					Name:        tool.Name,
					Description: openai.String(tool.Description),
					Parameters: openai.FunctionParameters(map[string]interface{}{
						"type":       "object",
						"properties": properties,
						"required":   tool.InputSchema.Required,
					}),
				},
			}
		}
		params.Tools = tools
	}

	resp, err := o.client.Responses.New(ctx, params)
	if err != nil {
		return llm.CompletionResponse{}, fmt.Errorf("OpenAI Responses API failed: %w", err)
	}

	if resp == nil {
		return llm.CompletionResponse{}, fmt.Errorf("empty response from OpenAI Responses API")
	}

	// Extract content and tool calls from Responses API format
	var content string
	var toolCalls []llm.ToolCall

	// Process response output to extract text and tool calls
	for i := range resp.Output {
		item := &resp.Output[i]

		switch item.Type {
		case "text":
			// Text content from the model - extract from content array
			// TODO: Handle different content types properly
			// For now, skip complex content extraction and rely on OutputText() fallback
			continue
		case "function_call":
			// Tool/function calls
			funcItem := item.AsFunctionCall()
			// Parse function arguments
			var parameters map[string]interface{}
			if funcItem.Arguments != "" {
				if err := json.Unmarshal([]byte(funcItem.Arguments), &parameters); err != nil {
					// If parsing fails, skip this tool call
					continue
				}
			}

			toolCalls = append(toolCalls, llm.ToolCall{
				ID:         funcItem.ID,
				Name:       funcItem.Name,
				Parameters: parameters,
			})
		case "reasoning":
			// Reasoning output - GPT-5 internal reasoning, don't include in final content
			// This is the chain-of-thought that makes GPT-5 so powerful
			continue
		default:
			// Unknown output type - log for debugging but don't fail
			continue
		}
	}

	// If no text output, try using the built-in OutputText() method as fallback
	if content == "" {
		content = resp.OutputText()
	}

	return llm.CompletionResponse{
		Content:   content,
		ToolCalls: toolCalls,
	}, nil
}

// Stream implements the llm.LLMClient interface with streaming support.
//
//nolint:gocritic // 80 bytes is reasonable for interface compliance
func (o *OfficialClient) Stream(_ context.Context, _ llm.CompletionRequest) (<-chan llm.StreamChunk, error) {
	// For now, return a placeholder implementation since the official OpenAI package
	// streaming API is experimental and may change. This can be updated when the responses
	// endpoint is fully implemented.
	ch := make(chan llm.StreamChunk, 1)
	go func() {
		defer close(ch)
		ch <- llm.StreamChunk{Content: "Official OpenAI streaming placeholder - implementation in progress"}
		ch <- llm.StreamChunk{Done: true}
	}()
	return ch, nil
}

// GetModelName returns the model name for this client.
func (o *OfficialClient) GetModelName() string {
	return o.model
}
