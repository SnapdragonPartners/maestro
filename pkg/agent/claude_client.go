package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"orchestrator/pkg/agent/llmerrors"
	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
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
	return NewClaudeClientWithLogger(apiKey, nil)
}

// NewClaudeClientWithLogger creates a new Claude client wrapper with logging support.
func NewClaudeClientWithLogger(apiKey string, logger *logx.Logger) LLMClient {
	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	baseClient := &ClaudeClient{
		client: client,
		model:  "claude-3-5-sonnet-20241022", // Default model
	}

	// Wrap with circuit breaker, retry logic, and prompt logging.
	return NewResilientClientWithLogger(baseClient, logger)
}

// NewClaudeClientWithModel creates a new Claude client with specific model and retry logic.
func NewClaudeClientWithModel(apiKey, model string) LLMClient {
	return NewClaudeClientWithModelAndLogger(apiKey, model, nil)
}

// NewClaudeClientWithModelAndLogger creates a new Claude client with specific model and logging.
func NewClaudeClientWithModelAndLogger(apiKey, model string, logger *logx.Logger) LLMClient {
	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	baseClient := &ClaudeClient{
		client: client,
		model:  anthropic.Model(model),
	}

	// Wrap with circuit breaker, retry logic, and prompt logging.
	return NewResilientClientWithLogger(baseClient, logger)
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
		// Classify the error for proper retry handling
		classifiedErr := c.classifyError(err, nil)
		return CompletionResponse{}, classifiedErr
	}

	if resp == nil || len(resp.Content) == 0 {
		// Empty response is a specific type of retryable error
		emptyErr := llmerrors.NewError(llmerrors.ErrorTypeEmptyResponse, "received empty or nil response from Claude API")
		return CompletionResponse{}, emptyErr
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

// GetDefaultConfig returns default model configuration for Claude.
func (c *ClaudeClient) GetDefaultConfig() config.Model {
	return config.Model{
		Name:           "claude-3-5-sonnet-20241022",
		MaxTPM:         50000, // 50k tokens per minute for Claude 3.5 Sonnet
		DailyBudget:    200.0, // $200 daily budget
		MaxConnections: 4,     // 4 concurrent connections
		CPM:            3.0,   // $3 per million tokens (average)
	}
}

// classifyError maps Anthropic SDK errors to our structured error types.
func (c *ClaudeClient) classifyError(err error, _ *http.Response) *llmerrors.Error {
	if err == nil {
		return nil
	}

	errStr := err.Error()

	// Check for context-related errors first
	if errors.Is(err, context.DeadlineExceeded) {
		return llmerrors.NewErrorWithCause(llmerrors.ErrorTypeTransient, err, "request timeout")
	}
	if errors.Is(err, context.Canceled) {
		return llmerrors.NewErrorWithCause(llmerrors.ErrorTypeTransient, err, "request canceled")
	}

	// Parse HTTP status codes if available in error message
	// Anthropic SDK typically includes status codes in error messages
	statusCode := extractStatusCode(errStr)

	switch statusCode {
	case 401:
		return llmerrors.NewErrorWithStatus(llmerrors.ErrorTypeAuth, statusCode, "authentication failed - check API key")
	case 403:
		return llmerrors.NewErrorWithStatus(llmerrors.ErrorTypeAuth, statusCode, "permission denied - check API access")
	case 429:
		return llmerrors.NewErrorWithStatus(llmerrors.ErrorTypeRateLimit, statusCode, "rate limit exceeded")
	case 400:
		return llmerrors.NewErrorWithStatus(llmerrors.ErrorTypeBadPrompt, statusCode, "bad request - check prompt format and parameters")
	case 500, 502, 503, 504:
		return llmerrors.NewErrorWithStatus(llmerrors.ErrorTypeTransient, statusCode, "server error")
	}

	// Check for common network and connection errors
	if strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "connection") ||
		strings.Contains(errStr, "network") ||
		strings.Contains(errStr, "temporary") ||
		strings.Contains(errStr, "EOF") ||
		strings.Contains(errStr, "reset") {
		return llmerrors.NewErrorWithCause(llmerrors.ErrorTypeTransient, err, "network or connection error")
	}

	// Check for rate limiting text patterns
	if strings.Contains(strings.ToLower(errStr), "rate") ||
		strings.Contains(strings.ToLower(errStr), "quota") ||
		strings.Contains(strings.ToLower(errStr), "limit") {
		return llmerrors.NewErrorWithCause(llmerrors.ErrorTypeRateLimit, err, "rate limiting detected")
	}

	// Check for authentication-related text patterns
	if strings.Contains(strings.ToLower(errStr), "auth") ||
		strings.Contains(strings.ToLower(errStr), "key") ||
		strings.Contains(strings.ToLower(errStr), "unauthorized") {
		return llmerrors.NewErrorWithCause(llmerrors.ErrorTypeAuth, err, "authentication error")
	}

	// Check for prompt/request issues
	if strings.Contains(strings.ToLower(errStr), "invalid") ||
		strings.Contains(strings.ToLower(errStr), "malformed") ||
		strings.Contains(strings.ToLower(errStr), "too large") ||
		strings.Contains(strings.ToLower(errStr), "token") {
		return llmerrors.NewErrorWithCause(llmerrors.ErrorTypeBadPrompt, err, "prompt or request error")
	}

	// Default to unknown error type
	return llmerrors.NewErrorWithCause(llmerrors.ErrorTypeUnknown, err, "unclassified error")
}

// extractStatusCode attempts to extract HTTP status code from error string.
// Anthropic SDK often includes status codes in error messages.
func extractStatusCode(errStr string) int {
	// Common patterns in error messages
	patterns := []string{
		"status code: ",
		"status: ",
		"HTTP ",
		"code ",
	}

	for _, pattern := range patterns {
		if idx := strings.Index(strings.ToLower(errStr), pattern); idx != -1 {
			start := idx + len(pattern)
			if start < len(errStr) {
				// Extract next 3 characters and try to parse as int
				end := start + 3
				if end > len(errStr) {
					end = len(errStr)
				}
				statusStr := errStr[start:end]

				// Try to parse common status codes
				switch {
				case strings.HasPrefix(statusStr, "400"):
					return 400
				case strings.HasPrefix(statusStr, "401"):
					return 401
				case strings.HasPrefix(statusStr, "403"):
					return 403
				case strings.HasPrefix(statusStr, "429"):
					return 429
				case strings.HasPrefix(statusStr, "500"):
					return 500
				case strings.HasPrefix(statusStr, "502"):
					return 502
				case strings.HasPrefix(statusStr, "503"):
					return 503
				case strings.HasPrefix(statusStr, "504"):
					return 504
				}
			}
		}
	}

	return 0 // No status code found
}
