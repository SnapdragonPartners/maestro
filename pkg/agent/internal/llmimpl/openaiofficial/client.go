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

// Complete implements the llm.LLMClient interface using the responses API.
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

	// Create responses request params
	params := responses.ResponseNewParams{
		Model:           o.model,
		MaxOutputTokens: openai.Int(int64(in.MaxTokens)),
		Input:           responses.ResponseNewParamsInputUnion{OfString: openai.String(inputText)},
	}

	// Add tools if provided using responses API format
	if len(in.Tools) > 0 {
		tools := make([]responses.ToolUnionParam, len(in.Tools))
		for i := range in.Tools {
			tool := &in.Tools[i]
			// Convert tool definition to responses API format
			properties := make(map[string]interface{})
			for name := range tool.InputSchema.Properties {
				prop := tool.InputSchema.Properties[name]
				propDef := map[string]interface{}{
					"type":        prop.Type,
					"description": prop.Description,
				}
				if len(prop.Enum) > 0 {
					propDef["enum"] = prop.Enum
				}
				properties[name] = propDef
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
		return llm.CompletionResponse{}, fmt.Errorf("official OpenAI responses API failed: %w", err)
	}

	if resp == nil {
		return llm.CompletionResponse{}, fmt.Errorf("empty response from official OpenAI responses API")
	}

	// Extract content and tool calls from responses API
	content := resp.OutputText() // Use the built-in OutputText() method
	var toolCalls []llm.ToolCall

	// Process response output for tool calls
	for i := range resp.Output {
		item := &resp.Output[i]
		if item.Type == "function_call" {
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
		}
	}

	return llm.CompletionResponse{
		Content:   content,
		ToolCalls: toolCalls,
	}, nil
}

// Stream implements the llm.LLMClient interface with streaming support.
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

// GetDefaultConfig returns default model configuration for OpenAI Official.
func (o *OfficialClient) GetDefaultConfig() config.Model {
	return config.Model{
		Name:           o.model,
		MaxTPM:         100000, // 100k tokens per minute for O3
		DailyBudget:    500.0,  // $500 daily budget (higher for official client)
		MaxConnections: 3,      // 3 concurrent connections
		CPM:            20.0,   // $20 per million tokens (O3 pricing)
	}
}
