package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// StreamEvent represents a parsed event from Claude Code's stream-json output.
type StreamEvent struct {
	// Type is the event type (e.g., "assistant", "result", "tool_use", "tool_result").
	Type string `json:"type"`

	// Message contains assistant message content (for type="assistant").
	Message *AssistantMessage `json:"message,omitempty"`

	// ToolUse contains tool call information (for type="tool_use").
	ToolUse *ToolUse `json:"tool_use,omitempty"`

	// ToolResult contains tool execution result (for type="tool_result").
	ToolResult *ToolResult `json:"tool_result,omitempty"`

	// Result contains final result information (for type="result").
	Result *FinalResult `json:"result,omitempty"`

	// Error contains error information (for type="error").
	Error *ErrorInfo `json:"error,omitempty"`

	// Raw is the raw JSON for debugging.
	Raw string `json:"-"`
}

// AssistantMessage represents an assistant message in the stream.
type AssistantMessage struct {
	ID      string         `json:"id,omitempty"`
	Role    string         `json:"role,omitempty"`
	Content []ContentBlock `json:"content,omitempty"`
	Model   string         `json:"model,omitempty"`
	Usage   *UsageInfo     `json:"usage,omitempty"`
}

// ContentBlock represents a content block in an assistant message.
type ContentBlock struct {
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Input any    `json:"input,omitempty"`
}

// ToolUse represents a tool invocation by Claude Code.
type ToolUse struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Input any    `json:"input"`
}

// ToolResult represents the result of a tool execution.
type ToolResult struct {
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

// FinalResult represents the final result of a Claude Code session.
type FinalResult struct {
	Success bool   `json:"success,omitempty"`
	Message string `json:"message,omitempty"`
}

// UsageInfo contains token usage information.
type UsageInfo struct {
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
}

// ErrorInfo contains error details.
type ErrorInfo struct {
	Type    string `json:"type,omitempty"`
	Message string `json:"message,omitempty"`
}

// StreamParser parses Claude Code stream-json output.
type StreamParser struct {
	onEvent   func(StreamEvent)
	onError   func(error)
	lineCount int
}

// NewStreamParser creates a new parser with event callbacks.
func NewStreamParser(onEvent func(StreamEvent), onError func(error)) *StreamParser {
	return &StreamParser{
		onEvent: onEvent,
		onError: onError,
	}
}

// ParseLine parses a single line of stream-json output.
func (p *StreamParser) ParseLine(line string) *StreamEvent {
	p.lineCount++
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}

	var event StreamEvent
	event.Raw = line

	if err := json.Unmarshal([]byte(line), &event); err != nil {
		// Try to extract just the type for partial parsing
		var typeOnly struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(line), &typeOnly) == nil {
			event.Type = typeOnly.Type
		} else {
			// Report error via callback - let caller decide how to log
			if p.onError != nil {
				p.onError(err)
			}
			return nil
		}
	}

	if p.onEvent != nil {
		p.onEvent(event)
	}

	return &event
}

// ParseReader reads and parses stream-json from an io.Reader.
// Returns when the reader is exhausted or context is cancelled.
func (p *StreamParser) ParseReader(r io.Reader) error {
	scanner := bufio.NewScanner(r)
	// Increase buffer size for potentially long JSON lines
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		p.ParseLine(scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scanner error: %w", err)
	}
	return nil
}

// LineCount returns the number of lines parsed.
func (p *StreamParser) LineCount() int {
	return p.lineCount
}

// ExtractToolCalls extracts all tool calls from a stream event.
func ExtractToolCalls(events []StreamEvent) []ToolUse {
	var calls []ToolUse
	for i := range events {
		if events[i].ToolUse != nil {
			calls = append(calls, *events[i].ToolUse)
		}
		// Also check content blocks for tool_use
		if events[i].Message != nil {
			for j := range events[i].Message.Content {
				if events[i].Message.Content[j].Type == "tool_use" {
					calls = append(calls, ToolUse{
						ID:    events[i].Message.Content[j].ID,
						Name:  events[i].Message.Content[j].Name,
						Input: events[i].Message.Content[j].Input,
					})
				}
			}
		}
	}
	return calls
}

// ExtractTextContent extracts all text content from a stream event.
func ExtractTextContent(events []StreamEvent) string {
	var parts []string
	for i := range events {
		if events[i].Message != nil {
			for j := range events[i].Message.Content {
				if events[i].Message.Content[j].Type == "text" && events[i].Message.Content[j].Text != "" {
					parts = append(parts, events[i].Message.Content[j].Text)
				}
			}
		}
	}
	return strings.Join(parts, "\n")
}

// HasError checks if any event contains an error.
func HasError(events []StreamEvent) (bool, string) {
	for i := range events {
		if events[i].Type == "error" && events[i].Error != nil {
			return true, events[i].Error.Message
		}
	}
	return false, ""
}

// GetFinalResult returns the final result event if present.
func GetFinalResult(events []StreamEvent) *FinalResult {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == "result" && events[i].Result != nil {
			return events[i].Result
		}
	}
	return nil
}

// CountResponses counts the number of assistant responses in the events.
func CountResponses(events []StreamEvent) int {
	count := 0
	for i := range events {
		if events[i].Type == "assistant" && events[i].Message != nil {
			count++
		}
	}
	return count
}
