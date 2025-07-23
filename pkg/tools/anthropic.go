package tools

import (
	"encoding/json"
	"fmt"
)

const (
	// Anthropic content types.
	contentTypeToolUse    = "tool_use"
	contentTypeToolResult = "tool_result"
)

// AnthropicMessageContent represents a content block in an Anthropic message.
//
//nolint:govet // Struct layout matches JSON serialization requirements
type AnthropicMessageContent struct {
	Type       string            `json:"type"`
	Text       string            `json:"text,omitempty"`
	ToolUse    *AnthropicToolUse `json:"tool_use,omitempty"`
	ToolResult *ToolResult       `json:"tool_result,omitempty"`
}

// AnthropicMessage represents a message in the Anthropic API format.
type AnthropicMessage struct {
	Role    string                    `json:"role"`
	Content []AnthropicMessageContent `json:"content"`
}

// AnthropicToolResponse represents a Claude response with tool_use.
type AnthropicToolResponse struct {
	ID         string                    `json:"id"`
	Model      string                    `json:"model"`
	StopReason string                    `json:"stop_reason"`
	Role       string                    `json:"role"`
	Content    []AnthropicMessageContent `json:"content"`
}

// AnthropicToolRequest creates a properly formatted tool request for Claude.
func AnthropicToolRequest(tools []ToolDefinition, prompt string) (map[string]interface{}, error) {
	// Base request structure.
	request := map[string]interface{}{
		"model":      "claude-3-opus-20240229", // Default model, can be made configurable
		"max_tokens": 4000,                     // Default max tokens
		"tools":      tools,
		"messages": []map[string]interface{}{
			{
				"role": "user",
				"content": []map[string]interface{}{
					{
						"type": "text",
						"text": prompt,
					},
				},
			},
		},
	}

	return request, nil
}

// ParseAnthropicResponse parses a raw JSON response from Claude into a structured format.
func ParseAnthropicResponse(responseJSON string) (*AnthropicToolResponse, error) {
	var response AnthropicToolResponse
	if err := json.Unmarshal([]byte(responseJSON), &response); err != nil {
		return nil, fmt.Errorf("failed to parse Anthropic response: %w", err)
	}
	return &response, nil
}

// ExtractToolUses extracts any tool_use blocks from an Anthropic response.
func ExtractToolUses(response *AnthropicToolResponse) []AnthropicToolUse {
	var toolUses []AnthropicToolUse

	if response == nil || len(response.Content) == 0 {
		return toolUses
	}

	for i := range response.Content {
		content := &response.Content[i]
		if content.Type == contentTypeToolUse && content.ToolUse != nil {
			toolUses = append(toolUses, *content.ToolUse)
		}
	}

	return toolUses
}

// FormatToolResults creates a properly formatted user message with tool results.
func FormatToolResults(toolResults []ToolResult) (map[string]interface{}, error) {
	// Create content blocks for each tool result.
	contentBlocks := make([]map[string]interface{}, len(toolResults))
	for i := range toolResults {
		result := &toolResults[i]
		contentBlocks[i] = map[string]interface{}{
			"type":        contentTypeToolResult,
			"tool_use_id": result.ToolUseID,
			"content":     result.Content,
		}
	}

	// Create the user message with tool results.
	message := map[string]interface{}{
		"role":    "user",
		"content": contentBlocks,
	}

	return message, nil
}

// FormatContinuationRequest creates a request to continue a conversation with tool results.
func FormatContinuationRequest(tools []ToolDefinition, messages []interface{}, toolResults []ToolResult) (map[string]interface{}, error) {
	// Convert tool results to a properly formatted user message.
	toolResultsMessage, err := FormatToolResults(toolResults)
	if err != nil {
		return nil, err
	}

	// Append the tool results message to the conversation.
	messages = append(messages, toolResultsMessage)

	// Create the continuation request.
	request := map[string]interface{}{
		"model":      "claude-3-opus-20240229", // Default model, can be made configurable
		"max_tokens": 4000,                     // Default max tokens
		"tools":      tools,
		"messages":   messages,
	}

	return request, nil
}
