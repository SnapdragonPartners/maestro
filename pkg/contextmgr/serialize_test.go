package contextmgr

import (
	"context"
	"testing"
	"time"
)

func TestContextManagerSerializeDeserialize(t *testing.T) {
	// Create a context manager with various state
	cm := NewContextManagerWithModel("claude-sonnet-4-20250514")
	cm.agentID = "coder-001"
	cm.currentTemplate = "coding"

	// Add a system prompt
	cm.ResetSystemPrompt("You are a helpful coding assistant.")

	// Add some messages
	cm.AddAssistantMessage("I understand the task.")
	cm.AddMessage("tool-result", "File created successfully.")
	cm.AddAssistantMessageWithTools("Let me create that file.", []ToolCall{
		{ID: "tc1", Name: "write_file", Parameters: map[string]any{"path": "/foo.txt"}},
	})
	cm.AddToolResult("tc1", "Success", false)

	// Add to user buffer
	cm.userBuffer = append(cm.userBuffer, Fragment{
		Timestamp:  time.Now(),
		Provenance: "human-input",
		Content:    "Please help me.",
	})

	// Serialize
	data, err := cm.Serialize()
	if err != nil {
		t.Fatalf("Serialize failed: %v", err)
	}

	// Create a new context manager and deserialize
	cm2 := NewContextManager()
	if err := cm2.Deserialize(data); err != nil {
		t.Fatalf("Deserialize failed: %v", err)
	}

	// Verify restoration
	if cm2.modelName != cm.modelName {
		t.Errorf("modelName mismatch: got %q, want %q", cm2.modelName, cm.modelName)
	}
	if cm2.agentID != cm.agentID {
		t.Errorf("agentID mismatch: got %q, want %q", cm2.agentID, cm.agentID)
	}
	if cm2.currentTemplate != cm.currentTemplate {
		t.Errorf("currentTemplate mismatch: got %q, want %q", cm2.currentTemplate, cm.currentTemplate)
	}
	if len(cm2.messages) != len(cm.messages) {
		t.Errorf("messages count mismatch: got %d, want %d", len(cm2.messages), len(cm.messages))
	}
	if len(cm2.userBuffer) != len(cm.userBuffer) {
		t.Errorf("userBuffer count mismatch: got %d, want %d", len(cm2.userBuffer), len(cm.userBuffer))
	}
	if len(cm2.pendingToolResults) != len(cm.pendingToolResults) {
		t.Errorf("pendingToolResults count mismatch: got %d, want %d", len(cm2.pendingToolResults), len(cm.pendingToolResults))
	}

	// Verify system prompt content
	if cm2.messages[0].Role != "system" {
		t.Errorf("first message role mismatch: got %q, want %q", cm2.messages[0].Role, "system")
	}
	if cm2.messages[0].Content != "You are a helpful coding assistant." {
		t.Errorf("system prompt content mismatch")
	}

	// Verify tool call preservation
	found := false
	for _, msg := range cm2.messages {
		if len(msg.ToolCalls) > 0 {
			found = true
			if msg.ToolCalls[0].Name != "write_file" {
				t.Errorf("tool call name mismatch: got %q, want %q", msg.ToolCalls[0].Name, "write_file")
			}
		}
	}
	if !found {
		t.Error("expected to find message with tool calls")
	}

	// Verify pending tool results
	if len(cm2.pendingToolResults) > 0 {
		if cm2.pendingToolResults[0].ToolCallID != "tc1" {
			t.Errorf("pending tool result ID mismatch: got %q, want %q", cm2.pendingToolResults[0].ToolCallID, "tc1")
		}
	}
}

func TestSerializeEmptyContext(t *testing.T) {
	cm := NewContextManager()

	data, err := cm.Serialize()
	if err != nil {
		t.Fatalf("Serialize failed: %v", err)
	}

	cm2 := NewContextManager()
	if err := cm2.Deserialize(data); err != nil {
		t.Fatalf("Deserialize failed: %v", err)
	}

	if len(cm2.messages) != 0 {
		t.Errorf("expected empty messages, got %d", len(cm2.messages))
	}
}

func TestSerializePreservesProvenance(t *testing.T) {
	cm := NewContextManager()
	cm.ResetSystemPrompt("Test system prompt")
	cm.AddMessage("tool-shell", "command output")

	// Flush to move buffer to messages
	_ = cm.FlushUserBuffer(context.Background())

	data, err := cm.Serialize()
	if err != nil {
		t.Fatalf("Serialize failed: %v", err)
	}

	cm2 := NewContextManager()
	if err := cm2.Deserialize(data); err != nil {
		t.Fatalf("Deserialize failed: %v", err)
	}

	// Check provenance was preserved
	if len(cm2.messages) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(cm2.messages))
	}

	if cm2.messages[0].Provenance != "system-prompt" {
		t.Errorf("system prompt provenance mismatch: got %q, want %q", cm2.messages[0].Provenance, "system-prompt")
	}
}
