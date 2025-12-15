// Package ollama provides Ollama client implementation for LLM interface.
// Ollama is a local LLM runtime that allows running open-source models.
package ollama

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/ollama/ollama/api"

	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/agent/llmerrors"
	"orchestrator/pkg/tools"
)

// Client wraps the Ollama API client to implement llm.LLMClient interface.
type Client struct {
	client  *api.Client
	model   string
	hostURL string
}

// NewOllamaClientWithModel creates a new Ollama client with specific model.
// hostURL should be the Ollama server URL (e.g., "http://localhost:11434").
func NewOllamaClientWithModel(hostURL, model string) llm.LLMClient {
	// Parse the host URL
	parsedURL, err := url.Parse(hostURL)
	if err != nil {
		// Fall back to default if URL is invalid
		parsedURL, _ = url.Parse("http://localhost:11434")
	}

	// Create the Ollama client
	client := api.NewClient(parsedURL, http.DefaultClient)

	return &Client{
		client:  client,
		model:   model,
		hostURL: hostURL,
	}
}

// Complete implements the llm.LLMClient interface.
//
//nolint:gocritic // CompletionRequest size acceptable for interface consistency
func (o *Client) Complete(ctx context.Context, in llm.CompletionRequest) (llm.CompletionResponse, error) {
	// Convert messages to Ollama format
	messages, err := convertMessagesToOllama(in.Messages)
	if err != nil {
		return llm.CompletionResponse{}, llmerrors.NewError(llmerrors.ErrorTypeBadPrompt, fmt.Sprintf("message conversion error: %v", err))
	}

	// Build chat request
	stream := false // We don't stream in Complete()
	req := &api.ChatRequest{
		Model:    o.model,
		Messages: messages,
		Stream:   &stream,
		Options: map[string]any{
			"temperature": in.Temperature,
			"num_predict": in.MaxTokens,
		},
	}

	// Convert tools if provided
	if len(in.Tools) > 0 {
		req.Tools = convertToolsToOllama(in.Tools)
	}

	// Call Ollama API
	var response api.ChatResponse
	err = o.client.Chat(ctx, req, func(resp api.ChatResponse) error {
		response = resp
		return nil
	})
	if err != nil {
		return llm.CompletionResponse{}, classifyError(err)
	}

	// Convert response to our format
	result := llm.CompletionResponse{
		Content:    response.Message.Content,
		StopReason: getStopReason(&response),
	}

	// Extract tool calls if present
	if len(response.Message.ToolCalls) > 0 {
		result.ToolCalls = convertToolCallsFromOllama(response.Message.ToolCalls)
	}

	return result, nil
}

// Stream implements the llm.LLMClient interface.
//
//nolint:revive,gocritic // ctx and in kept for interface consistency despite being unused
func (o *Client) Stream(ctx context.Context, in llm.CompletionRequest) (<-chan llm.StreamChunk, error) {
	// Streaming not currently used in our system
	return nil, llmerrors.NewError(llmerrors.ErrorTypeUnknown, "streaming not implemented for Ollama client")
}

// GetModelName returns the model name for this client.
func (o *Client) GetModelName() string {
	return o.model
}

// convertMessagesToOllama converts our message format to Ollama's Message format.
func convertMessagesToOllama(messages []llm.CompletionMessage) ([]api.Message, error) {
	if len(messages) == 0 {
		return nil, fmt.Errorf("message list cannot be empty")
	}

	result := make([]api.Message, 0, len(messages))

	for i := range messages {
		msg := &messages[i]

		ollamaMsg := api.Message{
			Role:    string(msg.Role),
			Content: msg.Content,
		}

		// Handle tool calls in assistant messages
		if len(msg.ToolCalls) > 0 {
			ollamaMsg.ToolCalls = make([]api.ToolCall, len(msg.ToolCalls))
			for j := range msg.ToolCalls {
				tc := &msg.ToolCalls[j]
				ollamaMsg.ToolCalls[j] = api.ToolCall{
					ID: tc.ID,
					Function: api.ToolCallFunction{
						Name:      tc.Name,
						Arguments: api.ToolCallFunctionArguments(tc.Parameters),
					},
				}
			}
		}

		// Handle tool results in user messages
		// In Ollama, tool results are sent as separate messages with role "tool"
		if len(msg.ToolResults) > 0 {
			for j := range msg.ToolResults {
				tr := &msg.ToolResults[j]
				toolMsg := api.Message{
					Role:       "tool",
					Content:    tr.Content,
					ToolCallID: tr.ToolCallID,
				}
				result = append(result, toolMsg)
			}
			// If there's also content in the message, add it as a user message
			if msg.Content != "" {
				result = append(result, ollamaMsg)
			}
			continue
		}

		result = append(result, ollamaMsg)
	}

	return result, nil
}

// convertToolsToOllama converts our tool definitions to Ollama's Tool format.
func convertToolsToOllama(toolDefs []tools.ToolDefinition) api.Tools {
	ollamaTools := make(api.Tools, len(toolDefs))

	for i := range toolDefs {
		td := &toolDefs[i]
		// Convert properties
		properties := make(map[string]api.ToolProperty)
		for name := range td.InputSchema.Properties {
			prop := td.InputSchema.Properties[name]
			properties[name] = convertPropertyToOllama(&prop)
		}

		ollamaTools[i] = api.Tool{
			Type: "function",
			Function: api.ToolFunction{
				Name:        td.Name,
				Description: td.Description,
				Parameters: api.ToolFunctionParameters{
					Type:       td.InputSchema.Type,
					Properties: properties,
					Required:   td.InputSchema.Required,
				},
			},
		}
	}

	return ollamaTools
}

// convertPropertyToOllama converts a tool property to Ollama format.
func convertPropertyToOllama(prop *tools.Property) api.ToolProperty {
	ollamaProp := api.ToolProperty{
		Type:        api.PropertyType{prop.Type},
		Description: prop.Description,
	}

	// Convert enum if present
	if len(prop.Enum) > 0 {
		enumVals := make([]any, len(prop.Enum))
		for i, v := range prop.Enum {
			enumVals[i] = v
		}
		ollamaProp.Enum = enumVals
	}

	// Handle nested properties for objects
	if prop.Properties != nil {
		// For nested objects, we need to handle the items field
		// This is a simplified conversion - complex nested schemas may need more work
		nestedProps := make(map[string]api.ToolProperty)
		for name, nestedProp := range prop.Properties {
			nestedProps[name] = convertPropertyToOllama(nestedProp)
		}
		ollamaProp.Items = map[string]any{
			"type":       "object",
			"properties": nestedProps,
		}
	}

	// Handle array items
	if prop.Items != nil {
		ollamaProp.Items = convertPropertyToOllama(prop.Items)
	}

	return ollamaProp
}

// convertToolCallsFromOllama extracts tool calls from Ollama response.
func convertToolCallsFromOllama(calls []api.ToolCall) []llm.ToolCall {
	result := make([]llm.ToolCall, len(calls))

	for i := range calls {
		call := &calls[i]
		// Generate an ID if not provided
		id := call.ID
		if id == "" {
			id = fmt.Sprintf("call_%d", i)
		}

		result[i] = llm.ToolCall{
			ID:         id,
			Name:       call.Function.Name,
			Parameters: map[string]any(call.Function.Arguments),
		}
	}

	return result
}

// getStopReason converts Ollama's done_reason to our stop reason format.
func getStopReason(resp *api.ChatResponse) string {
	if !resp.Done {
		return "incomplete"
	}

	switch resp.DoneReason {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "":
		// If done but no reason, assume normal completion
		return "end_turn"
	default:
		return resp.DoneReason
	}
}

// classifyError converts Ollama errors to our error types.
func classifyError(err error) error {
	if err == nil {
		return nil
	}

	errStr := err.Error()

	// Check for common error patterns
	switch {
	case strings.Contains(errStr, "connection refused"):
		return llmerrors.NewError(llmerrors.ErrorTypeTransient, fmt.Sprintf("Ollama server not reachable: %v", err))
	case strings.Contains(errStr, "model") && strings.Contains(errStr, "not found"):
		return llmerrors.NewError(llmerrors.ErrorTypeBadPrompt, fmt.Sprintf("Ollama model not found: %v", err))
	case strings.Contains(errStr, "context canceled"):
		return llmerrors.NewError(llmerrors.ErrorTypeTransient, fmt.Sprintf("request canceled: %v", err))
	case strings.Contains(errStr, "timeout"):
		return llmerrors.NewError(llmerrors.ErrorTypeTransient, fmt.Sprintf("request timeout: %v", err))
	default:
		return llmerrors.NewError(llmerrors.ErrorTypeUnknown, fmt.Sprintf("Ollama API error: %v", err))
	}
}
