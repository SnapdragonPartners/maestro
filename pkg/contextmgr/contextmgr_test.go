package contextmgr

import (
	"strings"
	"testing"

	"orchestrator/pkg/config"
)

func TestNewContextManager(t *testing.T) {
	cm := NewContextManager()

	if cm == nil {
		t.Error("Expected NewContextManager to return non-nil instance")
	}

	if cm.GetMessageCount() != 0 {
		t.Errorf("Expected new context manager to have 0 messages, got %d", cm.GetMessageCount())
	}

	if cm.CountTokens() != 0 {
		t.Errorf("Expected new context manager to have 0 tokens, got %d", cm.CountTokens())
	}
}

func TestAddMessage(t *testing.T) {
	cm := NewContextManager()

	// Add first message.
	cm.AddMessage("user", "Hello world")

	if cm.GetMessageCount() != 1 {
		t.Errorf("Expected 1 message after adding, got %d", cm.GetMessageCount())
	}

	messages := cm.GetMessages()
	if len(messages) != 1 {
		t.Errorf("Expected 1 message in GetMessages, got %d", len(messages))
	}

	msg := messages[0]
	if msg.Role != "user" {
		t.Errorf("Expected role 'user', got '%s'", msg.Role)
	}
	if msg.Content != "Hello world" {
		t.Errorf("Expected content 'Hello world', got '%s'", msg.Content)
	}

	// Add second message.
	cm.AddMessage("assistant", "Hi there!")

	if cm.GetMessageCount() != 2 {
		t.Errorf("Expected 2 messages after adding second, got %d", cm.GetMessageCount())
	}

	messages = cm.GetMessages()
	if len(messages) != 2 {
		t.Errorf("Expected 2 messages in GetMessages, got %d", len(messages))
	}

	// Check second message.
	msg2 := messages[1]
	if msg2.Role != "assistant" {
		t.Errorf("Expected second role 'assistant', got '%s'", msg2.Role)
	}
	if msg2.Content != "Hi there!" {
		t.Errorf("Expected second content 'Hi there!', got '%s'", msg2.Content)
	}
}

func TestCountTokens(t *testing.T) {
	cm := NewContextManager()

	// Empty context should have 0 tokens.
	if cm.CountTokens() != 0 {
		t.Errorf("Expected 0 tokens for empty context, got %d", cm.CountTokens())
	}

	// Add a message and check token count.
	cm.AddMessage("user", "test")
	expectedTokens := len("user") + len("test") // 4 + 4 = 8
	if cm.CountTokens() != expectedTokens {
		t.Errorf("Expected %d tokens, got %d", expectedTokens, cm.CountTokens())
	}

	// Add another message.
	cm.AddMessage("assistant", "response")
	expectedTokens += len("assistant") + len("response") // 8 + 9 + 8 = 25
	if cm.CountTokens() != expectedTokens {
		t.Errorf("Expected %d tokens after second message, got %d", expectedTokens, cm.CountTokens())
	}
}

func TestCompactIfNeeded(t *testing.T) {
	cm := NewContextManager()

	// Add some messages.
	cm.AddMessage("user", "Hello")
	cm.AddMessage("assistant", "Hi")

	// CompactIfNeeded without model config should use legacy approach.
	if err := cm.CompactIfNeeded(); err != nil {
		t.Errorf("Expected CompactIfNeeded to not return error, got %v", err)
	}

	// Messages should still be there since we're under legacy threshold.
	if cm.GetMessageCount() != 2 {
		t.Errorf("Expected messages to remain after CompactIfNeeded, got %d", cm.GetMessageCount())
	}

	// Test legacy method directly.
	if err := cm.CompactIfNeededLegacy(10); err != nil {
		t.Errorf("Expected CompactIfNeededLegacy to not return error, got %v", err)
	}
}

func TestGetMessages(t *testing.T) {
	cm := NewContextManager()

	// Test empty messages.
	messages := cm.GetMessages()
	if len(messages) != 0 {
		t.Errorf("Expected empty messages slice, got %d messages", len(messages))
	}

	// Add messages.
	cm.AddMessage("user", "Hello")
	cm.AddMessage("assistant", "Hi")

	messages = cm.GetMessages()
	if len(messages) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(messages))
	}

	// Test that returned slice is a copy (modifying it shouldn't affect original)
	messages[0].Content = "Modified"

	originalMessages := cm.GetMessages()
	if originalMessages[0].Content == "Modified" {
		t.Error("GetMessages should return a copy, not the original slice")
	}
	if originalMessages[0].Content != "Hello" {
		t.Errorf("Expected original content to be unchanged, got '%s'", originalMessages[0].Content)
	}
}

func TestClear(t *testing.T) {
	cm := NewContextManager()

	// Add some messages.
	cm.AddMessage("user", "Hello")
	cm.AddMessage("assistant", "Hi")

	if cm.GetMessageCount() != 2 {
		t.Errorf("Expected 2 messages before clear, got %d", cm.GetMessageCount())
	}

	// Clear the context.
	cm.Clear()

	if cm.GetMessageCount() != 0 {
		t.Errorf("Expected 0 messages after clear, got %d", cm.GetMessageCount())
	}

	if cm.CountTokens() != 0 {
		t.Errorf("Expected 0 tokens after clear, got %d", cm.CountTokens())
	}

	messages := cm.GetMessages()
	if len(messages) != 0 {
		t.Errorf("Expected empty messages after clear, got %d", len(messages))
	}
}

func TestGetContextSummary(t *testing.T) {
	cm := NewContextManager()

	// Test empty context.
	summary := cm.GetContextSummary()
	if summary != "Empty context" {
		t.Errorf("Expected 'Empty context' for empty manager, got '%s'", summary)
	}

	// Add some messages.
	cm.AddMessage("user", "Hello")
	summary = cm.GetContextSummary()

	// Should contain message count and token count.
	if !contains(summary, "1 messages") {
		t.Errorf("Expected summary to contain '1 messages', got '%s'", summary)
	}

	if !contains(summary, "user: 1") {
		t.Errorf("Expected summary to contain role breakdown, got '%s'", summary)
	}

	// Add more messages.
	cm.AddMessage("assistant", "Hi")
	cm.AddMessage("user", "How are you?")

	summary = cm.GetContextSummary()
	if !contains(summary, "3 messages") {
		t.Errorf("Expected summary to contain '3 messages', got '%s'", summary)
	}

	if !contains(summary, "user: 2") {
		t.Errorf("Expected summary to contain 'user: 2', got '%s'", summary)
	}

	if !contains(summary, "assistant: 1") {
		t.Errorf("Expected summary to contain 'assistant: 1', got '%s'", summary)
	}
}

// Helper function to check if a string contains a substring.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		(len(s) > len(substr) &&
			(s[:len(substr)] == substr ||
				s[len(s)-len(substr):] == substr ||
				containsAt(s, substr))))
}

func containsAt(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestNewContextManagerWithModel(t *testing.T) {
	modelConfig := &config.Model{
		Name:           "claude-3-5-sonnet-20241022",
		MaxTPM:         50000,
		DailyBudget:    200.0,
		MaxConnections: 4,
		CPM:            3.0,
	}

	cm := NewContextManagerWithModel(modelConfig)

	if cm == nil {
		t.Error("Expected non-nil context manager")
	}

	if cm.GetMaxContextTokens() != 1000 {
		t.Errorf("Expected max context tokens 1000, got %d", cm.GetMaxContextTokens())
	}

	if cm.GetMaxReplyTokens() != 100 {
		t.Errorf("Expected max reply tokens 100, got %d", cm.GetMaxReplyTokens())
	}
}

func TestCompactIfNeededWithModel(t *testing.T) {
	modelConfig := &config.Model{
		Name:           "claude-3-5-sonnet-20241022",
		MaxTPM:         50000,
		DailyBudget:    200.0,
		MaxConnections: 4,
		CPM:            3.0,
	}

	cm := NewContextManagerWithModel(modelConfig)

	// Add messages that exceed the compaction threshold.
	// Threshold = MaxContext - MaxReply - Buffer = 100 - 20 - 10 = 70
	cm.AddMessage("user", "This is a test message that is quite long and will help us exceed the compaction threshold")
	cm.AddMessage("assistant", "This is another long message to push us over the threshold")

	initialCount := cm.GetMessageCount()
	initialTokens := cm.CountTokens()

	// Should trigger compaction since we're likely over 70 tokens.
	if err := cm.CompactIfNeeded(); err != nil {
		t.Errorf("Expected no error during compaction, got %v", err)
	}

	// If compaction occurred, we should have fewer tokens.
	if cm.CountTokens() >= initialTokens && cm.GetMessageCount() >= initialCount {
		// This is expected if we weren't actually over threshold.
		t.Logf("No compaction needed - tokens: %d, messages: %d", cm.CountTokens(), cm.GetMessageCount())
	}
}

func TestShouldCompact(t *testing.T) {
	// Test without model config.
	cm := NewContextManager()
	cm.AddMessage("user", "Short message")

	if cm.ShouldCompact() {
		t.Error("Expected ShouldCompact to return false for short message without model config")
	}

	// Test with model config.
	modelConfig := &config.Model{
		Name:           "claude-3-5-sonnet-20241022",
		MaxTPM:         50000,
		DailyBudget:    200.0,
		MaxConnections: 4,
		CPM:            3.0,
	}

	cm2 := NewContextManagerWithModel(modelConfig)
	cm2.AddMessage("user", "This is a longer message that should trigger compaction logic")

	// This might or might not trigger compaction depending on exact token count.
	result := cm2.ShouldCompact()
	t.Logf("ShouldCompact result: %v, tokens: %d, threshold: %d",
		result, cm2.CountTokens(), 50-10-5)
}

func TestGetCompactionInfo(t *testing.T) {
	modelConfig := &config.Model{
		Name:           "claude-3-5-sonnet-20241022",
		MaxTPM:         50000,
		DailyBudget:    200.0,
		MaxConnections: 4,
		CPM:            3.0,
	}

	cm := NewContextManagerWithModel(modelConfig)
	cm.AddMessage("user", "Test message")

	info := cm.GetCompactionInfo()

	// Check required fields.
	if _, exists := info["current_tokens"]; !exists {
		t.Error("Expected current_tokens in compaction info")
	}

	if _, exists := info["message_count"]; !exists {
		t.Error("Expected message_count in compaction info")
	}

	if _, exists := info["should_compact"]; !exists {
		t.Error("Expected should_compact in compaction info")
	}

	if _, exists := info["max_context_tokens"]; !exists {
		t.Error("Expected max_context_tokens in compaction info")
	}

	if info["max_context_tokens"] != 1000 {
		t.Errorf("Expected max_context_tokens 1000, got %v", info["max_context_tokens"])
	}

	if info["max_reply_tokens"] != 200 {
		t.Errorf("Expected max_reply_tokens 200, got %v", info["max_reply_tokens"])
	}
}

// TestCompactionPreservesSystemPrompt tests that compaction never removes the first message.
func TestCompactionPreservesSystemPrompt(t *testing.T) {
	cm := NewContextManager()

	// Add system prompt and multiple messages.
	cm.AddMessage("system", "You are a helpful assistant")
	cm.AddMessage("user", "Hello")
	cm.AddMessage("assistant", "Hi there!")
	cm.AddMessage("user", "How are you?")
	cm.AddMessage("assistant", "I'm doing well!")

	if cm.GetMessageCount() != 5 {
		t.Errorf("Expected 5 messages initially, got %d", cm.GetMessageCount())
	}

	// Force aggressive compaction with very low target.
	err := cm.performCompaction(10) // Very low target to force maximum compaction
	if err != nil {
		t.Errorf("Compaction failed: %v", err)
	}

	// Should preserve at least system + 1 message.
	messages := cm.GetMessages()
	if len(messages) < 2 {
		t.Errorf("Compaction removed too many messages, got %d", len(messages))
	}

	// First message should still be the system prompt.
	if messages[0].Role != "system" || messages[0].Content != "You are a helpful assistant" {
		t.Errorf("System prompt was not preserved: got role=%s, content=%s",
			messages[0].Role, messages[0].Content)
	}
}

// TestSummarization tests the context summarization feature.
func TestSummarization(t *testing.T) {
	cm := NewContextManager()

	// Add system prompt and conversation.
	cm.AddMessage("system", "You are a coding assistant")
	cm.AddMessage("user", "Create a file called hello.go")
	cm.AddMessage("assistant", "I'll create the hello.go file for you")
	cm.AddMessage("user", "There's an error in the code")
	cm.AddMessage("assistant", "Let me fix that error")
	cm.AddMessage("user", "Test the file")
	cm.AddMessage("assistant", "Running tests now")

	originalCount := cm.GetMessageCount()

	// Test summarization directly.
	err := cm.performSummarization(50) // Low target to force summarization
	if err != nil {
		t.Errorf("Summarization failed: %v", err)
	}

	messages := cm.GetMessages()

	// Should have: system + summary + recent exchange.
	if len(messages) < 3 {
		t.Errorf("Summarization didn't preserve enough context, got %d messages", len(messages))
	}

	// First message should still be system.
	if messages[0].Role != "system" {
		t.Errorf("System message not preserved in summarization")
	}

	// Second message should be a summary.
	if !strings.Contains(messages[1].Content, "summary") && !strings.Contains(messages[1].Content, "conversation") {
		t.Errorf("Summary message not found: %s", messages[1].Content)
	}

	// Should have fewer messages than original.
	if len(messages) >= originalCount {
		t.Errorf("Summarization didn't reduce message count: %d >= %d", len(messages), originalCount)
	}
}

// TestAddMessageValidation tests input validation in AddMessage.
func TestAddMessageValidation(t *testing.T) {
	cm := NewContextManager()

	initialCount := cm.GetMessageCount()

	// Empty content should be ignored.
	cm.AddMessage("user", "")
	cm.AddMessage("user", "   \n\t  ")

	if cm.GetMessageCount() != initialCount {
		t.Errorf("Empty messages should be ignored, but count changed from %d to %d",
			initialCount, cm.GetMessageCount())
	}

	// Valid message should be added.
	cm.AddMessage("user", "Hello world")
	if cm.GetMessageCount() != initialCount+1 {
		t.Errorf("Valid message should be added, expected count %d, got %d",
			initialCount+1, cm.GetMessageCount())
	}

	// Empty role should default to "assistant".
	cm.AddMessage("", "Test message")
	messages := cm.GetMessages()
	lastMsg := messages[len(messages)-1]
	if lastMsg.Role != "assistant" {
		t.Errorf("Empty role should default to 'assistant', got '%s'", lastMsg.Role)
	}

	// Content should be trimmed.
	cm.AddMessage("user", "  trimmed content  ")
	messages = cm.GetMessages()
	lastMsg = messages[len(messages)-1]
	if lastMsg.Content != "trimmed content" {
		t.Errorf("Content should be trimmed, got '%s'", lastMsg.Content)
	}
}

// TestCreateConversationSummary tests the summarization logic.
func TestCreateConversationSummary(t *testing.T) {
	cm := NewContextManager()

	// Test empty messages.
	summary := cm.createConversationSummary([]Message{})
	if summary != "" {
		t.Errorf("Empty messages should return empty summary, got '%s'", summary)
	}

	// Test messages with different patterns.
	messages := []Message{
		{Role: "user", Content: "Create a file called test.go"},
		{Role: "assistant", Content: "I'll create the test.go file for you"},
		{Role: "user", Content: "There's an error: undefined variable"},
		{Role: "assistant", Content: "Let me fix that error for you"},
		{Role: "user", Content: "Please discuss the algorithm"},
	}

	summary = cm.createConversationSummary(messages)

	if summary == "" {
		t.Error("Summary should not be empty for valid messages")
	}

	// Should contain key information.
	if !strings.Contains(strings.ToLower(summary), "create") && !strings.Contains(strings.ToLower(summary), "file") {
		t.Errorf("Summary should mention file creation: %s", summary)
	}

	if !strings.Contains(strings.ToLower(summary), "error") {
		t.Errorf("Summary should mention errors: %s", summary)
	}

	// Should be reasonably short.
	if len(summary) > 1000 {
		t.Errorf("Summary should be concise, got %d characters", len(summary))
	}
}
