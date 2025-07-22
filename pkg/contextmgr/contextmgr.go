// Package contextmgr provides context management for LLM conversations including token counting and compaction.
package contextmgr

import (
	"fmt"
	"strings"

	"orchestrator/pkg/config"
)

// Message represents a single message in the conversation context.
type Message struct {
	Role    string
	Content string
}

// ContextManager manages conversation context and token counting.
type ContextManager struct {
	messages    []Message
	modelConfig *config.ModelCfg
}

// NewContextManager creates a new context manager instance.
func NewContextManager() *ContextManager {
	return &ContextManager{
		messages: make([]Message, 0),
	}
}

// NewContextManagerWithModel creates a context manager with model configuration.
func NewContextManagerWithModel(modelConfig *config.ModelCfg) *ContextManager {
	return &ContextManager{
		messages:    make([]Message, 0),
		modelConfig: modelConfig,
	}
}

// AddMessage stores a role/content pair in the context.
func (cm *ContextManager) AddMessage(role, content string) {
	// Basic validation - skip empty content to prevent context pollution.
	if strings.TrimSpace(content) == "" {
		return // Silently ignore empty messages
	}

	// Clean up role to prevent malformed context.
	role = strings.TrimSpace(role)
	if role == "" {
		role = "assistant" // Default role for empty roles
	}

	message := Message{
		Role:    role,
		Content: strings.TrimSpace(content),
	}
	cm.messages = append(cm.messages, message)
}

// CountTokens returns a simple token count based on message lengths.
// This is a stub implementation that counts characters as a proxy for tokens.
func (cm *ContextManager) CountTokens() int {
	totalLength := 0
	for _, message := range cm.messages {
		// Count both role and content characters.
		totalLength += len(message.Role) + len(message.Content)
	}
	return totalLength
}

// CompactIfNeeded performs context compaction if needed.
// Uses model configuration to determine when compaction is necessary.
func (cm *ContextManager) CompactIfNeeded() error {
	if cm.modelConfig == nil {
		// No model config available, use legacy threshold-based approach.
		return cm.compactIfNeededLegacy(10000) // Default threshold
	}

	currentTokens := cm.CountTokens()
	maxContext := cm.modelConfig.MaxContextTokens
	maxReply := cm.modelConfig.MaxReplyTokens
	buffer := cm.modelConfig.CompactionBuffer

	// Check if current + max reply + buffer > max context.
	if currentTokens+maxReply+buffer > maxContext {
		return cm.performCompaction(maxContext - maxReply - buffer)
	}

	return nil
}

// CompactIfNeededLegacy provides backward compatibility with threshold-based compaction.
func (cm *ContextManager) CompactIfNeededLegacy(threshold int) error {
	return cm.compactIfNeededLegacy(threshold)
}

// compactIfNeededLegacy implements the legacy threshold-based compaction.
func (cm *ContextManager) compactIfNeededLegacy(threshold int) error {
	currentTokens := cm.CountTokens()
	if currentTokens > threshold {
		// Simple compaction: keep only the most recent half of messages.
		targetSize := threshold / 2
		return cm.performCompaction(targetSize)
	}
	return nil
}

// performCompaction reduces context size to the target.
func (cm *ContextManager) performCompaction(targetTokens int) error {
	// Always preserve index 0 (system prompt) and ensure minimum viable context
	if len(cm.messages) <= 2 {
		// Keep system prompt + at least one exchange - can't compact further.
		return nil
	}

	// Try simple sliding window compaction first.
	originalLen := len(cm.messages)
	for cm.CountTokens() > targetTokens && len(cm.messages) > 2 {
		// Remove the second message (oldest non-system message)
		// This maintains: [system, msg3, msg4, ...] -> [system, msg4, ...].
		cm.messages = append(cm.messages[:1], cm.messages[2:]...)
	}

	// If we removed a significant amount of context (>50% of messages),
	// and we're still over target, try summarization instead.
	if len(cm.messages) < originalLen/2 && cm.CountTokens() > targetTokens {
		return cm.performSummarization(targetTokens)
	}

	return nil
}

// performSummarization uses LLM-based context compression.
func (cm *ContextManager) performSummarization(_ int) error {
	if len(cm.messages) <= 2 {
		return nil // Can't summarize minimal context
	}

	// Identify messages to summarize (everything except system + last exchange)
	systemMsg := cm.messages[0]
	var recentMsgs []Message
	var toSummarize []Message

	// Keep the last 2 messages as "recent" (preserve user-assistant exchange)
	if len(cm.messages) >= 2 {
		recentMsgs = cm.messages[len(cm.messages)-2:]
		toSummarize = cm.messages[1 : len(cm.messages)-2]
	}

	if len(toSummarize) == 0 {
		return nil // Nothing to summarize
	}

	// Create summary of the middle conversation.
	summary := cm.createConversationSummary(toSummarize)
	if summary == "" {
		return nil // Fallback to sliding window if summarization fails
	}

	// Create summary message.
	summaryMsg := Message{
		Role:    "assistant",
		Content: fmt.Sprintf("Previous conversation summary: %s", summary),
	}

	// Reconstruct messages: [system, summary, recent_exchange...].
	newMessages := []Message{systemMsg, summaryMsg}
	newMessages = append(newMessages, recentMsgs...)

	cm.messages = newMessages
	return nil
}

// createConversationSummary generates a concise summary of conversation messages.
func (cm *ContextManager) createConversationSummary(messages []Message) string {
	if len(messages) == 0 {
		return ""
	}

	// Simple text-based summarization for now.
	// This could be enhanced to use an actual LLM call in the future.
	var topics []string
	var codeActions []string
	var issues []string

	for _, msg := range messages {
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}

		// Extract key information based on content patterns.
		if strings.Contains(strings.ToLower(content), "error") ||
			strings.Contains(strings.ToLower(content), "failed") ||
			strings.Contains(strings.ToLower(content), "issue") {
			// Truncate long error messages.
			if len(content) > 100 {
				content = content[:100] + "..."
			}
			issues = append(issues, content)
		} else if strings.Contains(content, "file") &&
			(strings.Contains(content, "create") || strings.Contains(content, "edit")) {
			// Code-related actions.
			if len(content) > 80 {
				content = content[:80] + "..."
			}
			codeActions = append(codeActions, content)
		} else {
			// General topics/discussions.
			if len(content) > 60 {
				content = content[:60] + "..."
			}
			topics = append(topics, content)
		}
	}

	// Build summary.
	var summaryParts []string

	if len(topics) > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("Topics discussed: %s",
			strings.Join(deduplicateStrings(topics), "; ")))
	}

	if len(codeActions) > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("Code actions: %s",
			strings.Join(deduplicateStrings(codeActions), "; ")))
	}

	if len(issues) > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("Issues encountered: %s",
			strings.Join(deduplicateStrings(issues), "; ")))
	}

	if len(summaryParts) == 0 {
		return fmt.Sprintf("Previous conversation with %d messages", len(messages))
	}

	summary := strings.Join(summaryParts, ". ")

	// Ensure summary itself isn't too long.
	if len(summary) > 500 {
		summary = summary[:500] + "..."
	}

	return summary
}

// deduplicateStrings removes duplicate strings from a slice.
func deduplicateStrings(slice []string) []string {
	seen := make(map[string]bool)
	var result []string

	for _, item := range slice {
		if !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}

	return result
}

// GetMessages returns a copy of all messages in the context.
func (cm *ContextManager) GetMessages() []Message {
	// Return a copy to prevent external modification.
	result := make([]Message, len(cm.messages))
	copy(result, cm.messages)
	return result
}

// Clear removes all messages from the context.
func (cm *ContextManager) Clear() {
	cm.messages = cm.messages[:0]
}

// GetMessageCount returns the number of messages in the context.
func (cm *ContextManager) GetMessageCount() int {
	return len(cm.messages)
}

// GetContextSummary returns a brief summary of the context state.
func (cm *ContextManager) GetContextSummary() string {
	messageCount := len(cm.messages)
	tokenCount := cm.CountTokens()

	if messageCount == 0 {
		return "Empty context"
	}

	roleCounts := make(map[string]int)

	for _, message := range cm.messages {
		roleCounts[message.Role]++
	}

	roleBreakdown := make([]string, 0, len(roleCounts))

	for role, count := range roleCounts {
		roleBreakdown = append(roleBreakdown, fmt.Sprintf("%s: %d", role, count))
	}

	return fmt.Sprintf("%d messages (%d tokens) - %s",
		messageCount, tokenCount, strings.Join(roleBreakdown, ", "))
}

// GetMaxReplyTokens returns the maximum reply tokens for this model.
func (cm *ContextManager) GetMaxReplyTokens() int {
	if cm.modelConfig == nil {
		return 4096 // Default fallback
	}
	return cm.modelConfig.MaxReplyTokens
}

// GetMaxContextTokens returns the maximum context tokens for this model.
func (cm *ContextManager) GetMaxContextTokens() int {
	if cm.modelConfig == nil {
		return 32000 // Default fallback
	}
	return cm.modelConfig.MaxContextTokens
}

// ShouldCompact checks if compaction is needed without performing it.
func (cm *ContextManager) ShouldCompact() bool {
	if cm.modelConfig == nil {
		return cm.CountTokens() > 10000 // Legacy threshold
	}

	currentTokens := cm.CountTokens()
	maxContext := cm.modelConfig.MaxContextTokens
	maxReply := cm.modelConfig.MaxReplyTokens
	buffer := cm.modelConfig.CompactionBuffer

	return currentTokens+maxReply+buffer > maxContext
}

// GetCompactionInfo returns information about context state and compaction thresholds.
func (cm *ContextManager) GetCompactionInfo() map[string]any {
	info := map[string]any{
		"current_tokens": cm.CountTokens(),
		"message_count":  len(cm.messages),
		"should_compact": cm.ShouldCompact(),
	}

	if cm.modelConfig != nil {
		info["max_context_tokens"] = cm.modelConfig.MaxContextTokens
		info["max_reply_tokens"] = cm.modelConfig.MaxReplyTokens
		info["compaction_buffer"] = cm.modelConfig.CompactionBuffer

		currentTokens := cm.CountTokens()
		maxContext := cm.modelConfig.MaxContextTokens
		maxReply := cm.modelConfig.MaxReplyTokens
		buffer := cm.modelConfig.CompactionBuffer

		info["available_for_reply"] = maxContext - currentTokens
		info["compaction_threshold"] = maxContext - maxReply - buffer
		info["tokens_over_threshold"] = currentTokens - (maxContext - maxReply - buffer)
	}

	return info
}
