// Package contextmgr provides context management for LLM conversations including token counting and compaction.
package contextmgr

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
)

// Message represents a single message in the conversation context.
type Message struct {
	Role        string
	Content     string
	Provenance  string       // Source of content: "system-prompt", "tool-shell", "todo-status-update", etc.
	ToolCalls   []ToolCall   // Structured tool calls (for assistant messages)
	ToolResults []ToolResult // Structured tool results (for user messages)
}

// Fragment represents a piece of content with provenance tracking.
type Fragment struct {
	Timestamp  time.Time
	Provenance string // Source of content: "tool-shell", "architect-feedback", etc.
	Content    string
}

// ContextManagerInterface defines the new context management contract.
type ContextManagerInterface interface {
	// SystemPrompt returns the system prompt (always index 0)
	SystemPrompt() *Message
	// Conversation returns the rolling conversation window (index 1+)
	Conversation() []Message
	// ResetSystemPrompt sets a new system prompt, clearing conversation history
	ResetSystemPrompt(content string)
	// Append adds a message to the conversation with specified provenance
	Append(provenance, content string)
	// Compact performs context compaction if needed
	Compact(maxTokens int) error
	// CountTokens returns current token count
	CountTokens() int
	// Clear removes all messages
	Clear()
	// GetMessages returns all messages for backward compatibility
	GetMessages() []Message
	// FlushUserBuffer consolidates user buffer before LLM requests
	FlushUserBuffer(ctx context.Context) error
}

// LLMContextManager extends ContextManagerInterface with LLM-specific methods.
// This interface should only be used by LLM client implementations.
type LLMContextManager interface {
	ContextManagerInterface
	// AddAssistantMessage adds assistant message directly to context (LLM layer only)
	AddAssistantMessage(content string)
}

// ChatService interface defines what we need from the chat service.
type ChatService interface {
	GetNew(ctx context.Context, req *GetNewRequest) (*GetNewResponse, error)
	UpdateCursor(ctx context.Context, agentID string, newPointer int64) error
}

// GetNewRequest represents a request to get new messages for an agent.
type GetNewRequest struct {
	AgentID string
}

// GetNewResponse represents the response with new messages.
type GetNewResponse struct {
	Messages   []*ChatMessage `json:"messages"`
	NewPointer int64          `json:"newPointer"`
}

// ChatMessage represents a chat message.
//
//nolint:govet // Field alignment less important than logical grouping
type ChatMessage struct {
	ID        int64
	Author    string
	Text      string
	Channel   string
	Timestamp string
}

// ToolCall represents a structured tool call from the LLM.
//
//nolint:govet // fieldalignment: logical grouping preferred over byte savings
type ToolCall struct {
	ID         string
	Name       string
	Parameters map[string]any
}

// ToolResult represents a structured tool execution result.
type ToolResult struct {
	ToolCallID string
	Content    string
	IsError    bool
}

// ContextManager manages conversation context and token counting.
// Each instance is owned by a single agent goroutine, so no synchronization is needed.
//
//nolint:govet // Field alignment less important than logical grouping
type ContextManager struct {
	messages           []Message    // Core conversation messages
	userBuffer         []Fragment   // Buffer for user content with provenance
	modelName          string       // Model name for determining context limits
	currentTemplate    string       // Current template name for change detection
	chatService        ChatService  // Optional chat service for message injection
	agentID            string       // Agent ID for chat message fetching
	pendingToolCalls   []ToolCall   // Tool calls from last assistant message
	pendingToolResults []ToolResult // Accumulated tool results for batching
}

// NewContextManager creates a new context manager instance.
func NewContextManager() *ContextManager {
	return &ContextManager{
		messages:   make([]Message, 0),
		userBuffer: make([]Fragment, 0),
	}
}

// NewContextManagerWithModel creates a context manager with model name for context limits.
func NewContextManagerWithModel(modelName string) *ContextManager {
	return &ContextManager{
		messages:   make([]Message, 0),
		userBuffer: make([]Fragment, 0),
		modelName:  modelName,
	}
}

// SetChatService configures chat message injection for this context manager.
// If chatService is nil, chat injection is disabled.
func (cm *ContextManager) SetChatService(chatService ChatService, agentID string) {
	cm.chatService = chatService
	cm.agentID = agentID
}

// AddMessage stores a provenance/content pair in the user buffer.
// This replaces the old role-based API - all content goes to user buffer for later flushing.
// NOTE: User messages use context-aware truncation (no hard limit) to preserve important content.
func (cm *ContextManager) AddMessage(provenance, content string) {
	// Basic validation - skip empty content to prevent context pollution
	if strings.TrimSpace(content) == "" {
		return // Silently ignore empty messages
	}

	// Clean up provenance to prevent malformed context
	provenance = strings.TrimSpace(provenance)
	if provenance == "" {
		provenance = "unknown" // Default provenance for empty provenance
	}

	// Apply context-aware truncation for user messages
	content = cm.truncateOutputIfNeeded(strings.TrimSpace(content))

	// Add to user buffer with provenance tracking
	fragment := Fragment{
		Provenance: provenance,
		Content:    content,
		Timestamp:  time.Now(),
	}
	cm.userBuffer = append(cm.userBuffer, fragment)
}

// SystemPrompt returns the system prompt (always index 0).
func (cm *ContextManager) SystemPrompt() *Message {
	if len(cm.messages) == 0 {
		return nil
	}
	return &cm.messages[0]
}

// Conversation returns the rolling conversation window (index 1+).
func (cm *ContextManager) Conversation() []Message {
	if len(cm.messages) <= 1 {
		return []Message{}
	}
	// Return a copy to prevent external modification
	conversation := make([]Message, len(cm.messages)-1)
	copy(conversation, cm.messages[1:])
	return conversation
}

// ResetSystemPrompt sets a new system prompt, clearing conversation history.
func (cm *ContextManager) ResetSystemPrompt(content string) {
	// Clear all messages and buffer, set new system prompt
	cm.messages = []Message{{
		Role:       "system",
		Content:    strings.TrimSpace(content),
		Provenance: "system-prompt",
	}}
	cm.userBuffer = cm.userBuffer[:0]
}

// Append adds a message to the conversation with specified provenance.
func (cm *ContextManager) Append(provenance, content string) {
	// Use existing AddMessage logic for validation, cleanup, and middleware
	cm.AddMessage(provenance, content)
}

// Compact performs context compaction if needed.
func (cm *ContextManager) Compact(maxTokens int) error {
	return cm.performCompaction(maxTokens)
}

// CountTokens returns a simple token count based on message lengths.
// This is a stub implementation that counts characters as a proxy for tokens.
func (cm *ContextManager) CountTokens() int {
	totalLength := 0
	for i := range cm.messages {
		message := &cm.messages[i]
		// Count both role and content characters.
		totalLength += len(message.Role) + len(message.Content)
	}

	// Also count buffered user content
	for i := range cm.userBuffer {
		fragment := &cm.userBuffer[i]
		totalLength += len(fragment.Content)
	}

	return totalLength
}

// CompactIfNeeded performs context compaction if needed.
// Uses model name to determine when compaction is necessary.
func (cm *ContextManager) CompactIfNeeded() error {
	if cm.modelName == "" {
		// No model name available, use legacy threshold-based approach.
		return cm.compactIfNeededLegacy(10000) // Default threshold
	}

	currentTokens := cm.CountTokens()
	maxContext, maxReply := cm.getContextLimits()
	buffer := 2000 // Fixed buffer size

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

//nolint:cyclop // Complex summarization logic, acceptable for this use case
func (cm *ContextManager) createConversationSummary(messages []Message) string {
	if len(messages) == 0 {
		return ""
	}

	// Simple text-based summarization for now.
	// This could be enhanced to use an actual LLM call in the future.
	var topics []string
	var codeActions []string
	var issues []string

	for i := range messages {
		msg := &messages[i]
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

// GetModelName returns the model name for this context manager.
func (cm *ContextManager) GetModelName() string {
	return cm.modelName
}

// getContextLimits returns context management limits based on model name.
// Uses ModelInfo from config if available, otherwise falls back to conservative defaults.
func (cm *ContextManager) getContextLimits() (maxContext, maxReply int) {
	if cm.modelName == "" {
		return 32000, 4096 // Conservative defaults for empty model name
	}

	// Get model info from config (includes KnownModels and pattern matching)
	modelInfo, _ := config.GetModelInfo(cm.modelName)

	// Use the values from ModelInfo (will be defaults if unknown model)
	return modelInfo.MaxContextTokens, modelInfo.MaxOutputTokens
}

// Clear removes all messages from the context.
func (cm *ContextManager) Clear() {
	cm.messages = cm.messages[:0]
	cm.userBuffer = cm.userBuffer[:0]
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

	for i := range cm.messages {
		message := &cm.messages[i]
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
	_, maxReply := cm.getContextLimits()
	return maxReply
}

// GetMaxContextTokens returns the maximum context tokens for this model.
func (cm *ContextManager) GetMaxContextTokens() int {
	maxContext, _ := cm.getContextLimits()
	return maxContext
}

// ShouldCompact checks if compaction is needed without performing it.
func (cm *ContextManager) ShouldCompact() bool {
	if cm.modelName == "" {
		return cm.CountTokens() > 10000 // Legacy threshold
	}

	currentTokens := cm.CountTokens()
	maxContext, maxReply := cm.getContextLimits()
	buffer := 2000 // Fixed buffer size

	return currentTokens+maxReply+buffer > maxContext
}

// GetCompactionInfo returns information about context state and compaction thresholds.
func (cm *ContextManager) GetCompactionInfo() map[string]any {
	info := map[string]any{
		"current_tokens": cm.CountTokens(),
		"message_count":  len(cm.messages),
		"should_compact": cm.ShouldCompact(),
	}

	if cm.modelName != "" {
		maxContext, maxReply := cm.getContextLimits()
		buffer := 2000 // Fixed buffer size
		info["max_context_tokens"] = maxContext
		info["max_reply_tokens"] = maxReply
		info["compaction_buffer"] = buffer

		currentTokens := cm.CountTokens()
		info["available_for_reply"] = maxContext - currentTokens
		info["compaction_threshold"] = maxContext - maxReply - buffer
		info["tokens_over_threshold"] = currentTokens - (maxContext - maxReply - buffer)
	}

	return info
}

// ResetForNewTemplate resets the context and buffer when loading a new template.
// This should be called when switching between template types (e.g., PLANNING ↔ CODING).
func (cm *ContextManager) ResetForNewTemplate(templateName, systemPrompt string) {
	// Only reset if this is actually a different template
	if cm.currentTemplate == templateName {
		return // Same template, preserve context
	}

	// Clear all messages and buffer, set new system prompt
	cm.messages = []Message{{
		Role:       "system",
		Content:    strings.TrimSpace(systemPrompt),
		Provenance: "system-prompt",
	}}
	cm.userBuffer = cm.userBuffer[:0]

	// Clear pending tool state to prevent stale tool_use_id references
	cm.pendingToolCalls = nil
	cm.pendingToolResults = nil

	cm.currentTemplate = templateName
}

// MaxToolOutputChars is the hard limit for tool output before context-aware truncation.
const MaxToolOutputChars = 2000

// truncateToolOutput applies both hard limit and context-aware truncation to tool output.
// Tool output gets aggressive truncation to prevent verbose logs from consuming context.
func (cm *ContextManager) truncateToolOutput(content string) string {
	// First apply hard limit for tool output
	if len(content) > MaxToolOutputChars {
		content = content[:MaxToolOutputChars] + fmt.Sprintf("\n\n[... tool output truncated: %d chars exceeded hard limit of %d chars ...]",
			len(content), MaxToolOutputChars)
	}

	// Then apply context-aware truncation
	return cm.truncateOutputIfNeeded(content)
}

// truncateOutputIfNeeded truncates content based on available context space.
// Used for both user messages and tool output (after hard limit check for tools).
func (cm *ContextManager) truncateOutputIfNeeded(content string) string {
	// Get context limits for this model
	maxContext, _ := cm.getContextLimits()

	// Reserve 20% of context for response and buffer
	const reserveRatio = 0.20
	buffer := int(float64(maxContext) * reserveRatio)
	maxSafeContent := maxContext - buffer

	// Calculate current context usage (existing messages + buffered content)
	currentTokens := cm.CountTokens()

	// Check if this single message is larger than the entire safe context limit
	// (This catches pathologically large inputs like massive log files)
	if len(content) > maxSafeContent {
		truncated := content[:maxSafeContent]
		return truncated + fmt.Sprintf("\n\n[... content truncated: original size %d chars exceeded safe context limit of %d chars ...]",
			len(content), maxSafeContent)
	}

	// Check if adding this content would overflow the safe context limit
	projectedTotal := currentTokens + len(content)
	if projectedTotal > maxSafeContent {
		// Calculate how much we can safely add
		available := maxSafeContent - currentTokens
		if available <= 0 {
			// Context is already at or over limit - this should trigger compaction
			// But return a minimal truncated message to prevent complete failure
			const minSize = 1000
			if len(content) > minSize {
				return content[:minSize] + fmt.Sprintf("\n\n[... content truncated: context at capacity (%d/%d tokens) ...]",
					currentTokens, maxSafeContent)
			}
		}

		// Truncate to fit available space
		if len(content) > available {
			return content[:available] + fmt.Sprintf("\n\n[... content truncated to fit context: %d chars of %d shown ...]",
				available, len(content))
		}
	}

	// Content fits comfortably, no truncation needed
	return content
}

// FlushUserBuffer consolidates accumulated user messages into a single context message.
// This should be called before each LLM request to ensure proper alternation.
// Returns error if context compaction fails (indicating imminent token limit overflow).
//
// Chat Injection: If chat service is configured, fetches and injects new chat messages
// as late as possible (right before flushing), then updates cursor so messages aren't
// re-injected on next turn.
//
//nolint:cyclop // Complex logic for message batching and provenance tracking
func (cm *ContextManager) FlushUserBuffer(ctx context.Context) error {
	// STEP 1: Inject chat messages if chat service is configured
	if cm.chatService != nil && cm.agentID != "" {
		if err := cm.injectChatMessages(ctx); err != nil {
			// Log but don't fail - chat injection is best-effort
			logger := logx.NewLogger("contextmgr")
			logger.Warn("Chat injection failed for %s: %v (continuing without chat)", cm.agentID, err)
		}
	}

	// STEP 2: Combine pending tool results + userBuffer into ONE user message
	// This maintains proper user/assistant alternation
	if len(cm.pendingToolResults) > 0 || len(cm.userBuffer) > 0 {
		// Build combined content from userBuffer fragments
		var combinedContent string
		if len(cm.userBuffer) > 0 {
			contentParts := make([]string, 0, len(cm.userBuffer))
			for i := range cm.userBuffer {
				fragment := &cm.userBuffer[i]
				contentParts = append(contentParts, fragment.Content)
			}
			combinedContent = strings.Join(contentParts, "\n\n")
		} else if len(cm.pendingToolResults) > 0 {
			// Add minimal content when we have tool results but no buffer content
			// Anthropic API requires non-empty content field even with ToolResults
			combinedContent = "Tool results:"
		}

		// Determine provenance based on what we're including
		var provenance string
		if len(cm.pendingToolResults) > 0 && combinedContent != "" {
			provenance = "tool-results-and-content"
		} else if len(cm.pendingToolResults) > 0 {
			provenance = "tool-results-only"
		} else if len(cm.userBuffer) > 0 {
			// Use original provenance tracking logic for content-only
			firstProvenance := cm.userBuffer[0].Provenance
			allSameProvenance := true
			for i := range cm.userBuffer {
				if cm.userBuffer[i].Provenance != firstProvenance {
					allSameProvenance = false
					break
				}
			}
			if allSameProvenance {
				provenance = firstProvenance
			} else {
				provenance = "mixed"
			}
		}

		// Create single user message with both tool results and content
		// ToolResults will be used by API clients for proper formatting
		cm.messages = append(cm.messages, Message{
			Role:        "user",
			Content:     combinedContent, // Can be empty if only tool results
			Provenance:  provenance,
			ToolResults: cm.pendingToolResults, // Include structured tool results
		})

		// Clear pending state
		cm.pendingToolResults = nil
		cm.userBuffer = cm.userBuffer[:0]
	} else if len(cm.messages) == 0 || cm.messages[len(cm.messages)-1].Role != "user" {
		// STEP 3: Handle empty buffer - only add fallback if we need a user message
		// (i.e., no recent user message exists)
		cm.messages = append(cm.messages, Message{
			Role:       "user",
			Content:    "No response from user, please try something else",
			Provenance: "empty-buffer-fallback",
		})
	}

	// STEP 4: Perform compaction after flushing but before LLM request
	// Compaction failures indicate we would exceed LLM token limits, so fail fast
	if err := cm.CompactIfNeeded(); err != nil {
		return fmt.Errorf("context compaction failed before LLM request: %w", err)
	}

	return nil
}

// injectChatMessages fetches new chat messages and adds them directly to conversation.
// Messages are persisted immediately (not buffered) and cursor is updated to prevent re-injection.
func (cm *ContextManager) injectChatMessages(ctx context.Context) error {
	logger := logx.NewLogger("contextmgr")

	// Get configuration for chat limits
	cfg, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get config: %w", err)
	}

	// Check if chat is enabled
	if cfg.Chat == nil || !cfg.Chat.Enabled {
		return nil // Chat disabled, nothing to inject
	}

	// Fetch new messages
	resp, err := cm.chatService.GetNew(ctx, &GetNewRequest{AgentID: cm.agentID})
	if err != nil {
		return fmt.Errorf("failed to fetch new messages: %w", err)
	}

	// No new messages - nothing to do
	if len(resp.Messages) == 0 {
		return nil
	}

	// Limit to MaxNewMessages (most recent)
	maxMessages := cfg.Chat.MaxNewMessages
	if maxMessages <= 0 {
		maxMessages = 100 // Default
	}
	newMessages := resp.Messages
	if len(newMessages) > maxMessages {
		newMessages = newMessages[len(newMessages)-maxMessages:]
	}

	// Add chat messages as individual conversation turns with proper roles
	// This maintains natural conversation flow for the LLM
	expectedAgentAuthor := fmt.Sprintf("@%s", cm.agentID)

	for _, msg := range newMessages {
		var role string
		if msg.Author == "@human" {
			role = "user"
		} else if msg.Author == expectedAgentAuthor {
			role = "assistant"
		} else {
			// Messages from other agents - buffer as user content for batching
			cm.userBuffer = append(cm.userBuffer, Fragment{
				Timestamp:  time.Now(),
				Provenance: "chat-injection-other",
				Content:    fmt.Sprintf("[Chat from %s]: %s", msg.Author, msg.Text),
			})
			continue
		}

		// Add message based on role:
		// - Assistant messages: add directly (can't be batched)
		// - User messages: buffer for batching with tool results
		if role == "assistant" {
			cm.messages = append(cm.messages, Message{
				Role:       role,
				Content:    msg.Text,
				Provenance: "chat-injection",
			})
		} else {
			// User message - buffer for batching with tool results and other user content
			cm.userBuffer = append(cm.userBuffer, Fragment{
				Timestamp:  time.Now(),
				Provenance: "chat-injection",
				Content:    msg.Text,
			})
		}
	}

	logger.Info("💬 Injected %d chat messages into context for %s", len(newMessages), cm.agentID)

	// Update cursor so these messages won't be injected again
	if err := cm.chatService.UpdateCursor(ctx, cm.agentID, resp.NewPointer); err != nil {
		logger.Warn("Failed to update chat cursor for %s: %v", cm.agentID, err)
		// Continue anyway - message was injected successfully
	}

	return nil
}

// AddAssistantMessage adds an assistant message directly to context.
// This method should only be called by LLM client implementations.
func (cm *ContextManager) AddAssistantMessage(content string) {
	// Assistant messages go directly to context (no mutex needed - single threaded per agent)
	cm.messages = append(cm.messages, Message{
		Role:       "assistant",
		Content:    strings.TrimSpace(content),
		Provenance: "llm-response",
	})
	// Note: Compaction will be handled before the next LLM request, not here
}

// AddAssistantMessageWithTools adds an assistant message with structured tool calls.
// This preserves tool call information for proper API formatting.
func (cm *ContextManager) AddAssistantMessageWithTools(content string, toolCalls []ToolCall) {
	// Store tool calls for linking with results
	cm.pendingToolCalls = toolCalls

	// Add assistant message to context with structured tool calls
	cm.messages = append(cm.messages, Message{
		Role:       "assistant",
		Content:    strings.TrimSpace(content),
		Provenance: "llm-response-with-tools",
		ToolCalls:  toolCalls, // Include structured tool calls
	})
}

// AddToolResult adds a tool execution result to the pending batch.
// Tool results are accumulated and combined with human input in FlushUserBuffer.
// Tool output is truncated with both hard limit (2000 chars) and context-aware checks.
func (cm *ContextManager) AddToolResult(toolCallID, content string, isError bool) {
	// Apply aggressive truncation to tool output (hard limit + context-aware)
	truncatedContent := cm.truncateToolOutput(content)

	cm.pendingToolResults = append(cm.pendingToolResults, ToolResult{
		ToolCallID: toolCallID,
		Content:    truncatedContent,
		IsError:    isError,
	})
}

// AddUserMessageDirect adds a user message directly to context, bypassing the buffer.
// This is used by middleware that needs to persist messages across turns without buffering.
func (cm *ContextManager) AddUserMessageDirect(provenance, content string) {
	// Skip empty content
	if strings.TrimSpace(content) == "" {
		return
	}

	// Add directly to messages array (bypassing user buffer)
	cm.messages = append(cm.messages, Message{
		Role:       "user",
		Content:    strings.TrimSpace(content),
		Provenance: provenance,
	})
}

// GetUserBufferInfo returns information about the current user buffer state.
func (cm *ContextManager) GetUserBufferInfo() map[string]any {
	info := map[string]any{
		"fragment_count": len(cm.userBuffer),
		"is_empty":       len(cm.userBuffer) == 0,
	}

	if len(cm.userBuffer) > 0 {
		provenanceCounts := make(map[string]int)
		totalLength := 0
		for i := range cm.userBuffer {
			fragment := &cm.userBuffer[i]
			provenanceCounts[fragment.Provenance]++
			totalLength += len(fragment.Content)
		}
		info["provenance_breakdown"] = provenanceCounts
		info["total_buffer_length"] = totalLength
	}

	return info
}
