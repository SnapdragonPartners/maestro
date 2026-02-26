// Package anthropic provides Anthropic Claude client implementation for LLM interface.
package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/agent/llmerrors"
	"orchestrator/pkg/config"
)

// ClaudeClient wraps the Anthropic API client to implement llm.LLMClient interface.
//
//nolint:govet // Simple client struct, logical grouping preferred
type ClaudeClient struct {
	client anthropic.Client
	model  anthropic.Model
}

// NewClaudeClient creates a new Claude client wrapper (raw client, middleware applied at higher level).
func NewClaudeClient(apiKey string) llm.LLMClient {
	client := anthropic.NewClient(
		option.WithAPIKey(apiKey),
		option.WithMaxRetries(0), // Retries handled by our middleware layer
	)
	return &ClaudeClient{
		client: client,
		model:  config.ModelClaudeSonnetLatest, // Default model
	}
}

// NewClaudeClientWithModel creates a new Claude client with specific model (raw client, middleware applied at higher level).
func NewClaudeClientWithModel(apiKey, model string) llm.LLMClient {
	client := anthropic.NewClient(
		option.WithAPIKey(apiKey),
		option.WithMaxRetries(0), // Retries handled by our middleware layer
	)
	return &ClaudeClient{
		client: client,
		model:  anthropic.Model(model),
	}
}

// validatePreSend performs final validation before API call to catch common issues.
// - No system messages in messages array (should be in system parameter)
// - Proper alternation maintained
// - All roles are valid for Anthropic API.
func validatePreSend(_ string, messages []llm.CompletionMessage) error {
	// Check 1: Verify no system messages in messages array
	for i := range messages {
		msg := &messages[i]
		if msg.Role == llm.RoleSystem {
			return fmt.Errorf("system message found in messages array at index %d (should be extracted to system parameter)", i)
		}
	}

	// Check 2: Verify alternation
	for i := range messages {
		msg := &messages[i]
		if i > 0 {
			prevMsg := &messages[i-1]
			if msg.Role == prevMsg.Role {
				return fmt.Errorf("alternation violation at index %d: consecutive %s messages", i, msg.Role)
			}
		}
	}

	// Check 3: Verify first message is user
	if len(messages) > 0 && messages[0].Role != llm.RoleUser {
		return fmt.Errorf("first message must be user role, got: %s", messages[0].Role)
	}

	// Check 4: Verify last message is user
	if len(messages) > 0 && messages[len(messages)-1].Role != llm.RoleUser {
		return fmt.Errorf("last message must be user role, got: %s", messages[len(messages)-1].Role)
	}

	// Check 5: Verify only valid roles (user and assistant)
	for i := range messages {
		msg := &messages[i]
		if msg.Role != llm.RoleUser && msg.Role != llm.RoleAssistant {
			return fmt.Errorf("invalid role %s at index %d (Anthropic only supports user and assistant in messages array)", msg.Role, i)
		}
	}

	return nil
}

// ensureAlternation prepares messages for Anthropic API requirements.
// 1. Extracts system messages to top-level system parameter
// 2. Merges consecutive non-assistant messages into single user messages
// 3. Ensures strict user↔assistant alternation
// 4. Validates sequence ends with user message.
func ensureAlternation(messages []llm.CompletionMessage) (systemPrompt string, alternating []llm.CompletionMessage, err error) {
	if len(messages) == 0 {
		return "", nil, fmt.Errorf("message list cannot be empty")
	}

	// Step 1: Extract system messages
	var systemParts []string
	var nonSystemMessages []llm.CompletionMessage

	for i := range messages {
		msg := &messages[i]
		if msg.Role == llm.RoleSystem {
			systemParts = append(systemParts, msg.Content)
		} else {
			nonSystemMessages = append(nonSystemMessages, *msg)
		}
	}

	systemPrompt = strings.Join(systemParts, "\n\n")

	if len(nonSystemMessages) == 0 {
		return "", nil, fmt.Errorf("must have at least one non-system message")
	}

	// Step 2: Merge consecutive non-assistant messages
	var merged []llm.CompletionMessage
	var currentUserParts []string
	var currentUserCache *llm.CacheControl  // Track cache control for merged message
	var currentToolResults []llm.ToolResult // Track tool results for merged message

	for i := range nonSystemMessages {
		msg := &nonSystemMessages[i]

		if msg.Role == llm.RoleAssistant {
			// Flush any accumulated user messages first
			if len(currentUserParts) > 0 || len(currentToolResults) > 0 {
				merged = append(merged, llm.CompletionMessage{
					Role:         llm.RoleUser,
					Content:      strings.Join(currentUserParts, "\n\n"),
					CacheControl: currentUserCache,
					ToolResults:  currentToolResults,
				})
				currentUserParts = nil
				currentUserCache = nil
				currentToolResults = nil
			}

			// Add assistant message as-is
			merged = append(merged, *msg)
		} else {
			// Accumulate non-assistant message (user, tool, etc. all become user)
			if msg.Content != "" {
				currentUserParts = append(currentUserParts, msg.Content)
			}

			// Preserve tool results
			if len(msg.ToolResults) > 0 {
				currentToolResults = append(currentToolResults, msg.ToolResults...)
			}

			// Preserve cache control from last message in sequence (Anthropic only caches last block)
			if msg.CacheControl != nil {
				currentUserCache = msg.CacheControl
			}
		}
	}

	// Flush any remaining user messages
	if len(currentUserParts) > 0 || len(currentToolResults) > 0 {
		merged = append(merged, llm.CompletionMessage{
			Role:         llm.RoleUser,
			Content:      strings.Join(currentUserParts, "\n\n"),
			CacheControl: currentUserCache,
			ToolResults:  currentToolResults,
		})
	}

	// Step 3: Validate alternation
	for i := range merged {
		msg := &merged[i]

		// Check alternation pattern
		if i > 0 {
			prevMsg := &merged[i-1]
			if msg.Role == prevMsg.Role {
				return "", nil, fmt.Errorf("alternation violation at index %d: consecutive %s messages", i, msg.Role)
			}
		}

		// First message must be user
		if i == 0 && msg.Role != llm.RoleUser {
			return "", nil, fmt.Errorf("first message must be user role, got: %s", msg.Role)
		}
	}

	// Step 4: Ensure ends with user message
	lastMsg := &merged[len(merged)-1]
	if lastMsg.Role != llm.RoleUser {
		return "", nil, fmt.Errorf("last message must be user role, got: %s", lastMsg.Role)
	}

	// DEBUG: Log merged messages if debug logging enabled
	if os.Getenv("DEBUG_LLM") == "1" {
		fmt.Printf("[DEBUG ensureAlternation] Merged %d messages:\n", len(merged))
		for i := range merged {
			msg := &merged[i]
			contentPreview := msg.Content
			if len(contentPreview) > 50 {
				contentPreview = contentPreview[:minInt(50, len(contentPreview))]
			}
			fmt.Printf("  [%d] Role=%s Content=%q ToolCalls=%d ToolResults=%d\n",
				i, msg.Role, contentPreview, len(msg.ToolCalls), len(msg.ToolResults))
			if len(msg.ToolResults) > 0 {
				for j := range msg.ToolResults {
					tr := &msg.ToolResults[j]
					fmt.Printf("    ToolResult[%d] ID=%s IsError=%v\n", j, tr.ToolCallID, tr.IsError)
				}
			}
		}
	}

	return systemPrompt, merged, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Complete implements the llm.LLMClient interface.
//
//nolint:gocritic // CompletionRequest is 80 bytes but passing by value matches interface
func (c *ClaudeClient) Complete(ctx context.Context, in llm.CompletionRequest) (llm.CompletionResponse, error) {
	// Ensure alternation and extract system prompt
	systemPrompt, alternatingMessages, err := ensureAlternation(in.Messages)
	if err != nil {
		return llm.CompletionResponse{}, llmerrors.NewError(llmerrors.ErrorTypeBadPrompt, fmt.Sprintf("message alternation error: %v", err))
	}

	// Pre-send validation to catch issues before API call
	if validationErr := validatePreSend(systemPrompt, alternatingMessages); validationErr != nil {
		return llm.CompletionResponse{}, llmerrors.NewError(llmerrors.ErrorTypeBadPrompt, fmt.Sprintf("pre-send validation failed: %v", validationErr))
	}

	// Convert to Anthropic messages with prompt caching support (SDK v1.14.0+).
	messages := make([]anthropic.MessageParam, 0, len(alternatingMessages))
	for i := range alternatingMessages {
		msg := &alternatingMessages[i]
		role := anthropic.MessageParamRole(msg.Role)

		// Build content blocks array - can include text, tool_use, and tool_result blocks
		var contentBlocks []anthropic.ContentBlockParamUnion

		// IMPORTANT: Add tool_result blocks FIRST for user messages (Anthropic requires them immediately after tool_use)
		for j := range msg.ToolResults {
			tr := &msg.ToolResults[j]
			// Create tool result content with text block
			toolResultTextBlock := anthropic.TextBlockParam{
				Text: tr.Content,
				Type: "text",
			}
			toolResultContentUnion := anthropic.ToolResultBlockParamContentUnion{}
			toolResultContentUnion.OfText = &toolResultTextBlock

			toolResultBlock := anthropic.ToolResultBlockParam{
				Type:      "tool_result",
				ToolUseID: tr.ToolCallID,
				Content:   []anthropic.ToolResultBlockParamContentUnion{toolResultContentUnion},
				IsError:   anthropic.Bool(tr.IsError),
			}
			contentBlock := anthropic.ContentBlockParamUnion{}
			contentBlock.OfToolResult = &toolResultBlock
			contentBlocks = append(contentBlocks, contentBlock)
		}

		// Add text content after tool results
		if msg.Content != "" {
			textBlock := anthropic.TextBlockParam{
				Text: msg.Content,
				Type: "text",
			}

			// Add cache_control if present (Anthropic prompt caching)
			if msg.CacheControl != nil {
				cacheControl := anthropic.NewCacheControlEphemeralParam()

				// Set TTL if specified (defaults to 5m if not set)
				if msg.CacheControl.TTL != "" {
					switch msg.CacheControl.TTL {
					case "5m":
						cacheControl.TTL = anthropic.CacheControlEphemeralTTLTTL5m
					case "1h":
						cacheControl.TTL = anthropic.CacheControlEphemeralTTLTTL1h
						// Default: SDK will use 5m default if TTL not set
					}
				}

				textBlock.CacheControl = cacheControl
			}

			// Create content block for text
			if msg.CacheControl != nil {
				contentBlock := anthropic.ContentBlockParamUnion{}
				contentBlock.OfText = &textBlock
				contentBlocks = append(contentBlocks, contentBlock)
			} else {
				contentBlocks = append(contentBlocks, anthropic.NewTextBlock(textBlock.Text))
			}
		}

		// Add tool_use blocks for assistant messages with tool calls
		for j := range msg.ToolCalls {
			tc := &msg.ToolCalls[j]
			toolUseBlock := anthropic.ToolUseBlockParam{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Name,
				Input: tc.Parameters,
			}
			contentBlock := anthropic.ContentBlockParamUnion{}
			contentBlock.OfToolUse = &toolUseBlock
			contentBlocks = append(contentBlocks, contentBlock)
		}

		messageParam := anthropic.MessageParam{
			Role:    role,
			Content: contentBlocks,
		}

		messages = append(messages, messageParam)
	}

	// Prepare request parameters.
	maxTokens := int64(in.MaxTokens)
	params := anthropic.MessageNewParams{
		Model:       c.model,
		Messages:    messages,
		MaxTokens:   maxTokens,
		Temperature: anthropic.Float(float64(in.Temperature)),
	}

	// Add system prompt if present
	if systemPrompt != "" {
		params.System = []anthropic.TextBlockParam{{
			Text: systemPrompt,
			Type: "text",
		}}
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

		// Set tool choice based on request (default to "auto" if not specified)
		toolChoice := in.ToolChoice
		if toolChoice == "" {
			toolChoice = "auto"
		}

		switch toolChoice {
		case "any":
			// Force at least one tool call
			params.ToolChoice = anthropic.ToolChoiceUnionParam{
				OfAny: &anthropic.ToolChoiceAnyParam{},
			}
		case "auto":
			// Let Claude decide when to use tools
			params.ToolChoice = anthropic.ToolChoiceUnionParam{
				OfAuto: &anthropic.ToolChoiceAutoParam{},
			}
		case "tool":
			// Force specific tool (would need tool name parameter - not implemented yet)
			// For now, fall back to "any"
			params.ToolChoice = anthropic.ToolChoiceUnionParam{
				OfAny: &anthropic.ToolChoiceAnyParam{},
			}
		default:
			// Default to auto for unknown values
			params.ToolChoice = anthropic.ToolChoiceUnionParam{
				OfAuto: &anthropic.ToolChoiceAutoParam{},
			}
		}
	}

	// DEBUG: Log what's being sent to Anthropic API if debug logging enabled
	if os.Getenv("DEBUG_LLM") == "1" {
		fmt.Printf("[DEBUG API Request] Sending %d messages to Anthropic:\n", len(messages))
		for i := range messages {
			msg := &messages[i]
			fmt.Printf("  [%d] Role=%s ContentBlocks=%d\n", i, msg.Role, len(msg.Content))
			// Try to inspect content blocks
			for j, block := range msg.Content {
				if block.OfText != nil {
					textPreview := block.OfText.Text
					if len(textPreview) > 50 {
						textPreview = textPreview[:50]
					}
					fmt.Printf("    ContentBlock[%d] Type=text Text=%q\n", j, textPreview)
				} else if block.OfToolUse != nil {
					fmt.Printf("    ContentBlock[%d] Type=tool_use ID=%s Name=%s\n", j, block.OfToolUse.ID, block.OfToolUse.Name)
				} else if block.OfToolResult != nil {
					fmt.Printf("    ContentBlock[%d] Type=tool_result ToolUseID=%s\n", j, block.OfToolResult.ToolUseID)
				} else {
					fmt.Printf("    ContentBlock[%d] Type=unknown\n", j)
				}
			}
		}
	}

	// Make API request.
	resp, err := c.client.Messages.New(ctx, params)

	if err != nil {
		// Classify the error for proper retry handling
		classifiedErr := c.classifyError(err, nil)
		return llm.CompletionResponse{}, classifiedErr
	}

	if resp == nil || len(resp.Content) == 0 {
		// Empty response is a specific type of retryable error
		emptyErr := llmerrors.NewError(llmerrors.ErrorTypeEmptyResponse, "received empty or nil response from Claude API")
		return llm.CompletionResponse{}, emptyErr
	}

	// Extract text content and tool calls from the response using v1.5.0 API.
	var responseText string
	var toolCalls []llm.ToolCall

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
				return llm.CompletionResponse{}, fmt.Errorf("failed to parse tool input: %w", err)
			}

			toolCall := llm.ToolCall{
				ID:         toolUseBlock.ID,
				Name:       toolUseBlock.Name,
				Parameters: params,
			}
			toolCalls = append(toolCalls, toolCall)
		}
	}

	return llm.CompletionResponse{
		Content:    responseText,
		ToolCalls:  toolCalls,
		StopReason: string(resp.StopReason),
	}, nil
}

// Stream implements the llm.LLMClient interface.
//
//nolint:gocritic // CompletionRequest is 80 bytes but passing by value matches interface
func (c *ClaudeClient) Stream(ctx context.Context, in llm.CompletionRequest) (<-chan llm.StreamChunk, error) {
	// Return mock stream for now.
	ch := make(chan llm.StreamChunk, 1)
	go func() {
		defer close(ch)
		resp, err := c.Complete(ctx, in)
		if err != nil {
			ch <- llm.StreamChunk{Error: err}
			return
		}
		ch <- llm.StreamChunk{Content: resp.Content}
		ch <- llm.StreamChunk{Done: true}
	}()
	return ch, nil
}

// GetModelName returns the model name for this client.
func (c *ClaudeClient) GetModelName() string {
	return string(c.model)
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
