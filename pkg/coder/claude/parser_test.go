package claude

import (
	"strings"
	"testing"
)

func TestParseLine_Empty(t *testing.T) {
	parser := NewStreamParser(nil, nil)
	result := parser.ParseLine("")
	if result != nil {
		t.Errorf("expected nil for empty line, got %v", result)
	}
	result = parser.ParseLine("   ")
	if result != nil {
		t.Errorf("expected nil for whitespace-only line, got %v", result)
	}
}

func TestParseLine_AssistantMessage(t *testing.T) {
	json := `{"type":"assistant","message":{"id":"msg_123","role":"assistant","content":[{"type":"text","text":"Hello world"}],"model":"claude-3-5-sonnet"}}`

	var received *StreamEvent
	parser := NewStreamParser(func(e StreamEvent) {
		received = &e
	}, nil)

	result := parser.ParseLine(json)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Type != "assistant" {
		t.Errorf("expected type 'assistant', got %q", result.Type)
	}
	if result.Message == nil {
		t.Fatal("expected Message to be non-nil")
	}
	if result.Message.ID != "msg_123" {
		t.Errorf("expected ID 'msg_123', got %q", result.Message.ID)
	}
	if len(result.Message.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(result.Message.Content))
	}
	if result.Message.Content[0].Text != "Hello world" {
		t.Errorf("expected text 'Hello world', got %q", result.Message.Content[0].Text)
	}
	if received == nil {
		t.Error("expected onEvent callback to be called")
	}
}

func TestParseLine_ToolUse(t *testing.T) {
	json := `{"type":"tool_use","tool_use":{"id":"tool_123","name":"write_file","input":{"path":"/tmp/test.txt","content":"hello"}}}`

	parser := NewStreamParser(nil, nil)
	result := parser.ParseLine(json)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Type != "tool_use" {
		t.Errorf("expected type 'tool_use', got %q", result.Type)
	}
	if result.ToolUse == nil {
		t.Fatal("expected ToolUse to be non-nil")
	}
	if result.ToolUse.ID != "tool_123" {
		t.Errorf("expected ID 'tool_123', got %q", result.ToolUse.ID)
	}
	if result.ToolUse.Name != "write_file" {
		t.Errorf("expected name 'write_file', got %q", result.ToolUse.Name)
	}
}

func TestParseLine_Error(t *testing.T) {
	json := `{"type":"error","error":{"type":"rate_limit","message":"Too many requests"}}`

	parser := NewStreamParser(nil, nil)
	result := parser.ParseLine(json)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Type != "error" {
		t.Errorf("expected type 'error', got %q", result.Type)
	}
	if result.Error == nil {
		t.Fatal("expected Error to be non-nil")
	}
	if result.Error.Message != "Too many requests" {
		t.Errorf("expected message 'Too many requests', got %q", result.Error.Message)
	}
}

func TestParseLine_InvalidJSON(t *testing.T) {
	var gotError error
	parser := NewStreamParser(nil, func(err error) {
		gotError = err
	})

	result := parser.ParseLine("not valid json")

	if result != nil {
		t.Errorf("expected nil result for invalid JSON, got %v", result)
	}
	if gotError == nil {
		t.Error("expected onError callback to be called")
	}
}

func TestParseLine_PartialParsing(t *testing.T) {
	// JSON that has a valid type field but other fields fail to parse
	json := `{"type":"assistant","message":"invalid_structure"}`

	parser := NewStreamParser(nil, nil)
	result := parser.ParseLine(json)

	// Should still extract the type
	if result == nil {
		t.Fatal("expected non-nil result even for partial parse")
	}
	if result.Type != "assistant" {
		t.Errorf("expected type 'assistant', got %q", result.Type)
	}
}

func TestParseReader(t *testing.T) {
	input := `{"type":"assistant","message":{"content":[{"type":"text","text":"Line 1"}]}}
{"type":"tool_use","tool_use":{"id":"t1","name":"read_file","input":{}}}
{"type":"result","result":{"success":true}}`

	var events []StreamEvent
	parser := NewStreamParser(func(e StreamEvent) {
		events = append(events, e)
	}, nil)

	reader := strings.NewReader(input)
	err := parser.ParseReader(reader)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(events) != 3 {
		t.Errorf("expected 3 events, got %d", len(events))
	}
	if parser.LineCount() != 3 {
		t.Errorf("expected line count 3, got %d", parser.LineCount())
	}
}

func TestExtractToolCalls(t *testing.T) {
	events := []StreamEvent{
		{Type: "assistant", Message: &AssistantMessage{
			Content: []ContentBlock{
				{Type: "text", Text: "Let me help"},
				{Type: "tool_use", ID: "t1", Name: "read_file", Input: map[string]any{"path": "/test"}},
			},
		}},
		{Type: "tool_use", ToolUse: &ToolUse{ID: "t2", Name: "write_file", Input: map[string]any{"path": "/out"}}},
		{Type: "tool_result", ToolResult: &ToolResult{ToolUseID: "t1", Content: "file content"}},
	}

	calls := ExtractToolCalls(events)

	if len(calls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(calls))
	}

	// First from content block
	if calls[0].ID != "t1" || calls[0].Name != "read_file" {
		t.Errorf("first call: expected t1/read_file, got %s/%s", calls[0].ID, calls[0].Name)
	}

	// Second from dedicated tool_use event
	if calls[1].ID != "t2" || calls[1].Name != "write_file" {
		t.Errorf("second call: expected t2/write_file, got %s/%s", calls[1].ID, calls[1].Name)
	}
}

func TestExtractTextContent(t *testing.T) {
	events := []StreamEvent{
		{Type: "assistant", Message: &AssistantMessage{
			Content: []ContentBlock{
				{Type: "text", Text: "First part"},
				{Type: "tool_use", Name: "something"},
				{Type: "text", Text: "Second part"},
			},
		}},
		{Type: "tool_result"},
		{Type: "assistant", Message: &AssistantMessage{
			Content: []ContentBlock{
				{Type: "text", Text: "Third part"},
			},
		}},
	}

	text := ExtractTextContent(events)

	expected := "First part\nSecond part\nThird part"
	if text != expected {
		t.Errorf("expected %q, got %q", expected, text)
	}
}

func TestHasError(t *testing.T) {
	tests := []struct {
		name     string
		events   []StreamEvent
		hasError bool
		message  string
	}{
		{
			name:     "no error",
			events:   []StreamEvent{{Type: "assistant"}, {Type: "result"}},
			hasError: false,
			message:  "",
		},
		{
			name: "has error",
			events: []StreamEvent{
				{Type: "assistant"},
				{Type: "error", Error: &ErrorInfo{Message: "something failed"}},
			},
			hasError: true,
			message:  "something failed",
		},
		{
			name:     "error type without Error struct",
			events:   []StreamEvent{{Type: "error"}},
			hasError: false,
			message:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hasErr, msg := HasError(tc.events)
			if hasErr != tc.hasError {
				t.Errorf("expected hasError=%v, got %v", tc.hasError, hasErr)
			}
			if msg != tc.message {
				t.Errorf("expected message=%q, got %q", tc.message, msg)
			}
		})
	}
}

func TestGetFinalResult(t *testing.T) {
	t.Run("has result", func(t *testing.T) {
		events := []StreamEvent{
			{Type: "assistant"},
			{Type: "result", Result: &FinalResult{Success: true, Message: "done"}},
		}

		result := GetFinalResult(events)
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if !result.Success {
			t.Error("expected Success=true")
		}
		if result.Message != "done" {
			t.Errorf("expected message 'done', got %q", result.Message)
		}
	})

	t.Run("no result", func(t *testing.T) {
		events := []StreamEvent{
			{Type: "assistant"},
			{Type: "tool_use"},
		}

		result := GetFinalResult(events)
		if result != nil {
			t.Errorf("expected nil, got %v", result)
		}
	})

	t.Run("multiple results returns last", func(t *testing.T) {
		events := []StreamEvent{
			{Type: "result", Result: &FinalResult{Message: "first"}},
			{Type: "result", Result: &FinalResult{Message: "second"}},
		}

		result := GetFinalResult(events)
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if result.Message != "second" {
			t.Errorf("expected 'second', got %q", result.Message)
		}
	})
}

func TestCountResponses(t *testing.T) {
	events := []StreamEvent{
		{Type: "assistant", Message: &AssistantMessage{}},
		{Type: "tool_use"},
		{Type: "tool_result"},
		{Type: "assistant", Message: &AssistantMessage{}},
		{Type: "assistant"}, // no message
		{Type: "result"},
	}

	count := CountResponses(events)
	if count != 2 {
		t.Errorf("expected 2 responses, got %d", count)
	}
}
