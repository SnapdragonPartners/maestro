package contextmgr

import (
	"fmt"
	"strings"

	"orchestrator/pkg/config"
)

// Message represents a single message in the conversation context
type Message struct {
	Role    string
	Content string
}

// ContextManager manages conversation context and token counting
type ContextManager struct {
	messages    []Message
	modelConfig *config.ModelCfg
}

// NewContextManager creates a new context manager instance
func NewContextManager() *ContextManager {
	return &ContextManager{
		messages: make([]Message, 0),
	}
}

// NewContextManagerWithModel creates a context manager with model configuration
func NewContextManagerWithModel(modelConfig *config.ModelCfg) *ContextManager {
	return &ContextManager{
		messages:    make([]Message, 0),
		modelConfig: modelConfig,
	}
}

// AddMessage stores a role/content pair in the context
func (cm *ContextManager) AddMessage(role string, content string) {
	message := Message{
		Role:    role,
		Content: content,
	}
	cm.messages = append(cm.messages, message)
}

// CountTokens returns a simple token count based on message lengths
// This is a stub implementation that counts characters as a proxy for tokens
func (cm *ContextManager) CountTokens() int {
	totalLength := 0
	for _, message := range cm.messages {
		// Count both role and content characters
		totalLength += len(message.Role) + len(message.Content)
	}
	return totalLength
}

// CompactIfNeeded performs context compaction if needed
// Uses model configuration to determine when compaction is necessary
func (cm *ContextManager) CompactIfNeeded() error {
	if cm.modelConfig == nil {
		// No model config available, use legacy threshold-based approach
		return cm.compactIfNeededLegacy(10000) // Default threshold
	}

	currentTokens := cm.CountTokens()
	maxContext := cm.modelConfig.MaxContextTokens
	maxReply := cm.modelConfig.MaxReplyTokens
	buffer := cm.modelConfig.CompactionBuffer

	// Check if current + max reply + buffer > max context
	if currentTokens+maxReply+buffer > maxContext {
		return cm.performCompaction(maxContext - maxReply - buffer)
	}

	return nil
}

// CompactIfNeededLegacy provides backward compatibility with threshold-based compaction
func (cm *ContextManager) CompactIfNeededLegacy(threshold int) error {
	return cm.compactIfNeededLegacy(threshold)
}

// compactIfNeededLegacy implements the legacy threshold-based compaction
func (cm *ContextManager) compactIfNeededLegacy(threshold int) error {
	currentTokens := cm.CountTokens()
	if currentTokens > threshold {
		// Simple compaction: keep only the most recent half of messages
		targetSize := threshold / 2
		return cm.performCompaction(targetSize)
	}
	return nil
}

// performCompaction reduces context size to the target
func (cm *ContextManager) performCompaction(targetTokens int) error {
	if len(cm.messages) <= 1 {
		// Can't compact if we have 1 or fewer messages
		return nil
	}

	// Simple strategy: remove oldest messages until we're under target
	for cm.CountTokens() > targetTokens && len(cm.messages) > 1 {
		// Keep the first message (usually system prompt) and remove the second
		if len(cm.messages) > 2 {
			cm.messages = append(cm.messages[:1], cm.messages[2:]...)
		} else {
			// If only 2 messages, remove the first one
			cm.messages = cm.messages[1:]
		}
	}

	return nil
}

// GetMessages returns a copy of all messages in the context
func (cm *ContextManager) GetMessages() []Message {
	// Return a copy to prevent external modification
	result := make([]Message, len(cm.messages))
	copy(result, cm.messages)
	return result
}

// Clear removes all messages from the context
func (cm *ContextManager) Clear() {
	cm.messages = cm.messages[:0]
}

// GetMessageCount returns the number of messages in the context
func (cm *ContextManager) GetMessageCount() int {
	return len(cm.messages)
}

// GetContextSummary returns a brief summary of the context state
func (cm *ContextManager) GetContextSummary() string {
	messageCount := len(cm.messages)
	tokenCount := cm.CountTokens()

	if messageCount == 0 {
		return "Empty context"
	}

	var roleBreakdown []string
	roleCounts := make(map[string]int)

	for _, message := range cm.messages {
		roleCounts[message.Role]++
	}

	for role, count := range roleCounts {
		roleBreakdown = append(roleBreakdown, fmt.Sprintf("%s: %d", role, count))
	}

	return fmt.Sprintf("%d messages (%d tokens) - %s",
		messageCount, tokenCount, strings.Join(roleBreakdown, ", "))
}

// GetMaxReplyTokens returns the maximum reply tokens for this model
func (cm *ContextManager) GetMaxReplyTokens() int {
	if cm.modelConfig == nil {
		return 4096 // Default fallback
	}
	return cm.modelConfig.MaxReplyTokens
}

// GetMaxContextTokens returns the maximum context tokens for this model
func (cm *ContextManager) GetMaxContextTokens() int {
	if cm.modelConfig == nil {
		return 32000 // Default fallback
	}
	return cm.modelConfig.MaxContextTokens
}

// ShouldCompact checks if compaction is needed without performing it
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

// GetCompactionInfo returns information about context state and compaction thresholds
func (cm *ContextManager) GetCompactionInfo() map[string]interface{} {
	info := map[string]interface{}{
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
