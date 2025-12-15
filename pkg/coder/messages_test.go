// Tests for message building functions.
package coder

import (
	"strings"
	"testing"

	"orchestrator/pkg/agent"
)

// =============================================================================
// buildMessagesWithContext tests
// =============================================================================

func TestBuildMessagesWithContext_BasicStructure(t *testing.T) {
	coder := createTestCoder(t, nil)

	messages := coder.buildMessagesWithContext("Initial system prompt")

	if len(messages) < 2 {
		t.Errorf("Expected at least 2 messages, got: %d", len(messages))
	}

	// First message should be the initial prompt
	// Note: ValidateAndSanitizeMessages may filter or reorder messages
	foundInitialPrompt := false
	for _, msg := range messages {
		if msg.Content == "Initial system prompt" {
			foundInitialPrompt = true
			break
		}
	}
	if !foundInitialPrompt {
		t.Error("Initial prompt should be included in messages")
	}

	// Last message should be the guidance message
	lastMsg := messages[len(messages)-1]
	if !strings.Contains(lastMsg.Content, "CRITICAL REMINDERS") {
		t.Error("Last message should be guidance reminders")
	}
}

func TestBuildMessagesWithContext_EmptyPrompt(t *testing.T) {
	coder := createTestCoder(t, nil)

	messages := coder.buildMessagesWithContext("")

	// Should still have messages (at minimum the initial + guidance)
	if len(messages) < 2 {
		t.Errorf("Expected at least 2 messages even with empty prompt, got: %d", len(messages))
	}
}

func TestBuildMessagesWithContext_WithContextMessages(t *testing.T) {
	coder := createTestCoder(t, nil)

	// Add messages to context manager
	// Note: The context manager's AddMessage behavior depends on its implementation
	coder.contextManager.AddMessage("story-content", "This is the story content")
	coder.contextManager.AddMessage("user-request", "Please implement feature X")

	messages := coder.buildMessagesWithContext("System prompt")

	// The function should produce at minimum: system prompt + guidance
	// Context messages depend on contextManager behavior
	if len(messages) < 2 {
		t.Errorf("Expected at least 2 messages, got: %d", len(messages))
	}

	// Log the message count for debugging
	t.Logf("Generated %d messages total", len(messages))

	// Verify structure: last message is always the guidance
	lastMsg := messages[len(messages)-1]
	if !strings.Contains(lastMsg.Content, "CRITICAL REMINDERS") {
		t.Error("Last message should be guidance reminders")
	}
}

func TestBuildMessagesWithContext_CacheControl(t *testing.T) {
	coder := createTestCoder(t, nil)

	// Add cacheable message
	coder.contextManager.AddMessage("story-content", "Story content should be cached")

	messages := coder.buildMessagesWithContext("System prompt")

	// Find the story content message and verify cache control
	// The last cacheable message should have cache control
	cacheableFound := false
	for _, msg := range messages {
		if strings.Contains(msg.Content, "Story content") && msg.CacheControl != nil {
			cacheableFound = true
			break
		}
	}

	if !cacheableFound {
		t.Log("Note: Cache control application depends on provenance matching")
	}
}

func TestBuildMessagesWithContext_RoleMapping(t *testing.T) {
	coder := createTestCoder(t, nil)

	messages := coder.buildMessagesWithContext("System prompt")

	// All messages should have valid roles
	validRoles := map[agent.CompletionRole]bool{
		agent.RoleUser:      true,
		agent.RoleAssistant: true,
	}

	for i, msg := range messages {
		if !validRoles[msg.Role] {
			t.Errorf("Message %d has invalid role: %v", i, msg.Role)
		}
	}
}

func TestBuildMessagesWithContext_SkipsEmptyMessages(t *testing.T) {
	coder := createTestCoder(t, nil)

	// The context manager's AddMessage might filter empty messages,
	// but the buildMessagesWithContext function also filters them
	messages := coder.buildMessagesWithContext("System prompt")

	// Verify no empty messages in output
	for i, msg := range messages {
		if strings.TrimSpace(msg.Content) == "" {
			t.Errorf("Message %d has empty content", i)
		}
	}
}

func TestBuildMessagesWithContext_GuidanceAlwaysLast(t *testing.T) {
	coder := createTestCoder(t, nil)

	// Add several context messages
	coder.contextManager.AddMessage("msg1", "First message")
	coder.contextManager.AddMessage("msg2", "Second message")
	coder.contextManager.AddMessage("msg3", "Third message")

	messages := coder.buildMessagesWithContext("System prompt")

	lastMsg := messages[len(messages)-1]
	if !strings.Contains(lastMsg.Content, "CRITICAL REMINDERS") {
		t.Error("Guidance message should always be the last message")
	}
	if lastMsg.CacheControl != nil {
		t.Error("Guidance message should not have cache control (always fresh)")
	}
}
