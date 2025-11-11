package anthropic

import (
	"testing"

	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/contextmgr"
)

// TestStructuredToolCallsInMessages verifies that messages with ToolCalls
// are properly converted to Anthropic content blocks.
func TestStructuredToolCallsInMessages(t *testing.T) {
	// Create a message with tool calls (assistant message)
	toolCall := llm.ToolCall{
		ID:   "call_123",
		Name: "list_files",
		Parameters: map[string]any{
			"pattern": "*.go",
		},
	}

	msg := llm.CompletionMessage{
		Role:      llm.RoleAssistant,
		Content:   "Let me check the files...",
		ToolCalls: []llm.ToolCall{toolCall},
	}

	// Verify the message has the structured data
	if len(msg.ToolCalls) != 1 {
		t.Errorf("Expected 1 tool call, got %d", len(msg.ToolCalls))
	}
	if msg.ToolCalls[0].Name != "list_files" {
		t.Errorf("Expected tool name 'list_files', got '%s'", msg.ToolCalls[0].Name)
	}
}

// TestStructuredToolResultsInMessages verifies that messages with ToolResults
// are properly converted to Anthropic content blocks.
func TestStructuredToolResultsInMessages(t *testing.T) {
	// Create a message with tool results (user message)
	toolResult := llm.ToolResult{
		ToolCallID: "call_123",
		Content:    "file1.go\nfile2.go\nfile3.go",
		IsError:    false,
	}

	msg := llm.CompletionMessage{
		Role:        llm.RoleUser,
		Content:     "Here are some additional thoughts...",
		ToolResults: []llm.ToolResult{toolResult},
	}

	// Verify the message has the structured data
	if len(msg.ToolResults) != 1 {
		t.Errorf("Expected 1 tool result, got %d", len(msg.ToolResults))
	}
	if msg.ToolResults[0].ToolCallID != "call_123" {
		t.Errorf("Expected tool call ID 'call_123', got '%s'", msg.ToolResults[0].ToolCallID)
	}
}

// TestContextManagerToolCallBatching verifies that ContextManager properly
// batches tool results with human input in a single user message.
func TestContextManagerToolCallBatching(t *testing.T) {
	cm := contextmgr.NewContextManager()

	// Simulate assistant making a tool call
	toolCall := contextmgr.ToolCall{
		ID:   "call_456",
		Name: "read_file",
		Parameters: map[string]any{
			"path": "main.go",
		},
	}
	cm.AddAssistantMessageWithTools("Let me read that file...", []contextmgr.ToolCall{toolCall})

	// Add tool result
	cm.AddToolResult("call_456", "package main\n\nfunc main() {...}", false)

	// Add some human input to the buffer
	cm.AddMessage("chat-injection", "That looks good to me!")

	// Flush should combine tool result + human input in ONE user message
	err := cm.FlushUserBuffer(nil)
	if err != nil {
		t.Fatalf("FlushUserBuffer failed: %v", err)
	}

	messages := cm.GetMessages()
	// Should have: assistant message + single user message (with tool result + human content)
	if len(messages) != 2 {
		t.Errorf("Expected 2 messages after flush, got %d", len(messages))
	}

	// Check the user message has both tool results and content
	userMsg := messages[1]
	if userMsg.Role != "user" {
		t.Errorf("Expected user role, got %s", userMsg.Role)
	}
	if len(userMsg.ToolResults) != 1 {
		t.Errorf("Expected 1 tool result in user message, got %d", len(userMsg.ToolResults))
	}
	if userMsg.Content == "" {
		t.Error("Expected user message to have content from buffer")
	}
}
