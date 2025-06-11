package tools

import (
	"testing"
)

func TestMCPParser_ParseToolCalls(t *testing.T) {
	parser := NewMCPParser()
	
	// Test empty text
	calls, err := parser.ParseToolCalls("")
	if err != nil {
		t.Errorf("Expected no error parsing empty text, got %v", err)
	}
	if len(calls) != 0 {
		t.Errorf("Expected 0 tool calls, got %d", len(calls))
	}
	
	// Test text with no tool calls
	calls, err = parser.ParseToolCalls("This is regular text with no tools")
	if err != nil {
		t.Errorf("Expected no error parsing regular text, got %v", err)
	}
	if len(calls) != 0 {
		t.Errorf("Expected 0 tool calls, got %d", len(calls))
	}
	
	// Test single tool call
	text := `<tool name="shell">ls -la</tool>`
	calls, err = parser.ParseToolCalls(text)
	if err != nil {
		t.Errorf("Expected no error parsing single tool call, got %v", err)
	}
	if len(calls) != 1 {
		t.Errorf("Expected 1 tool call, got %d", len(calls))
	}
	
	call := calls[0]
	if call.Name != "shell" {
		t.Errorf("Expected tool name 'shell', got '%s'", call.Name)
	}
	if call.RawArgs != "ls -la" {
		t.Errorf("Expected raw args 'ls -la', got '%s'", call.RawArgs)
	}
	if cmd, ok := call.Args["cmd"]; !ok {
		t.Error("Expected 'cmd' in args")
	} else if cmdStr, ok := cmd.(string); !ok {
		t.Error("Expected 'cmd' to be string")
	} else if cmdStr != "ls -la" {
		t.Errorf("Expected cmd 'ls -la', got '%s'", cmdStr)
	}
	
	// Test multiple tool calls
	text = `Some text <tool name="shell">echo hello</tool> more text <tool name="other">test args</tool>`
	calls, err = parser.ParseToolCalls(text)
	if err != nil {
		t.Errorf("Expected no error parsing multiple tool calls, got %v", err)
	}
	if len(calls) != 2 {
		t.Errorf("Expected 2 tool calls, got %d", len(calls))
	}
	
	if calls[0].Name != "shell" {
		t.Errorf("Expected first tool name 'shell', got '%s'", calls[0].Name)
	}
	if calls[1].Name != "other" {
		t.Errorf("Expected second tool name 'other', got '%s'", calls[1].Name)
	}
	
	// Test tool call with whitespace
	text = `<tool name="shell">  
		echo "hello world"  
	</tool>`
	calls, err = parser.ParseToolCalls(text)
	if err != nil {
		t.Errorf("Expected no error parsing tool call with whitespace, got %v", err)
	}
	if len(calls) != 1 {
		t.Errorf("Expected 1 tool call, got %d", len(calls))
	}
	
	// The raw args should be trimmed
	if calls[0].RawArgs == "" {
		t.Error("Expected non-empty raw args after trimming")
	}
}

func TestMCPParser_HasToolCalls(t *testing.T) {
	parser := NewMCPParser()
	
	// Test text without tool calls
	if parser.HasToolCalls("Regular text") {
		t.Error("Expected HasToolCalls to return false for regular text")
	}
	
	// Test text with tool calls
	if !parser.HasToolCalls(`<tool name="shell">ls</tool>`) {
		t.Error("Expected HasToolCalls to return true for text with tool call")
	}
	
	// Test mixed text
	if !parser.HasToolCalls(`Some text <tool name="test">args</tool> more text`) {
		t.Error("Expected HasToolCalls to return true for mixed text with tool call")
	}
}

func TestMCPParser_ExtractToolNames(t *testing.T) {
	parser := NewMCPParser()
	
	// Test empty text
	names := parser.ExtractToolNames("")
	if len(names) != 0 {
		t.Errorf("Expected 0 tool names, got %d", len(names))
	}
	
	// Test text without tools
	names = parser.ExtractToolNames("Regular text")
	if len(names) != 0 {
		t.Errorf("Expected 0 tool names, got %d", len(names))
	}
	
	// Test single tool
	names = parser.ExtractToolNames(`<tool name="shell">ls</tool>`)
	if len(names) != 1 {
		t.Errorf("Expected 1 tool name, got %d", len(names))
	}
	if names[0] != "shell" {
		t.Errorf("Expected tool name 'shell', got '%s'", names[0])
	}
	
	// Test multiple tools
	names = parser.ExtractToolNames(`<tool name="shell">ls</tool> text <tool name="other">args</tool>`)
	if len(names) != 2 {
		t.Errorf("Expected 2 tool names, got %d", len(names))
	}
	if names[0] != "shell" {
		t.Errorf("Expected first tool name 'shell', got '%s'", names[0])
	}
	if names[1] != "other" {
		t.Errorf("Expected second tool name 'other', got '%s'", names[1])
	}
}

func TestGlobalParserFunctions(t *testing.T) {
	text := `<tool name="shell">echo test</tool>`
	
	// Test global ParseToolCalls
	calls, err := ParseToolCalls(text)
	if err != nil {
		t.Errorf("Expected no error with global ParseToolCalls, got %v", err)
	}
	if len(calls) != 1 {
		t.Errorf("Expected 1 tool call, got %d", len(calls))
	}
	
	// Test global HasToolCalls
	if !HasToolCalls(text) {
		t.Error("Expected global HasToolCalls to return true")
	}
	
	// Test global ExtractToolNames
	names := ExtractToolNames(text)
	if len(names) != 1 {
		t.Errorf("Expected 1 tool name, got %d", len(names))
	}
	if names[0] != "shell" {
		t.Errorf("Expected tool name 'shell', got '%s'", names[0])
	}
}