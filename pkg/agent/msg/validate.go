// Package msg provides message validation utilities for agent communication.
package msg

import (
	"fmt"
	"strings"

	"orchestrator/pkg/agent/llm"
)

// MessageValidationError represents a validation error for completion messages.
type MessageValidationError struct {
	Field  string
	Value  string
	Reason string
}

func (e MessageValidationError) Error() string {
	return fmt.Sprintf("message validation error - %s: '%s' (%s)", e.Field, e.Value, e.Reason)
}

// ValidateMessages validates a slice of completion messages.
func ValidateMessages(messages []llm.CompletionMessage) error {
	if len(messages) == 0 {
		return MessageValidationError{
			Field:  "messages",
			Value:  "[]",
			Reason: "at least one message is required",
		}
	}

	for i := range messages {
		msg := &messages[i]
		if err := ValidateMessage(msg); err != nil {
			return fmt.Errorf("message %d: %w", i, err)
		}
	}

	return nil
}

// ValidateMessage validates a single completion message.
func ValidateMessage(msg *llm.CompletionMessage) error {
	// Validate role.
	if err := ValidateRole(msg.Role); err != nil {
		return err
	}

	// Validate content.
	if err := ValidateContent(msg.Content); err != nil {
		return err
	}

	return nil
}

// ValidateRole validates that a role is valid and non-empty.
func ValidateRole(role llm.CompletionRole) error {
	roleStr := string(role)
	if strings.TrimSpace(roleStr) == "" {
		return MessageValidationError{
			Field:  "role",
			Value:  roleStr,
			Reason: "role cannot be empty",
		}
	}

	// Check against known valid roles.
	if role != llm.RoleUser && role != llm.RoleAssistant && role != llm.RoleSystem {
		return MessageValidationError{
			Field:  "role",
			Value:  roleStr,
			Reason: "role must be one of: user, assistant, system",
		}
	}

	return nil
}

// ValidateContent validates that content is meaningful and not empty.
func ValidateContent(content string) error {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return MessageValidationError{
			Field:  "content",
			Value:  content,
			Reason: "content cannot be empty or whitespace-only",
		}
	}

	// Check for common malformed patterns.
	if strings.HasPrefix(trimmed, "[]") {
		return MessageValidationError{
			Field:  "content",
			Value:  content,
			Reason: "content appears to have malformed role prefix '[]'",
		}
	}

	// Soft limit check - warn about very long content.
	const softLimit = 50000 // ~50KB
	if len(content) > softLimit {
		// This is not an error, just a warning condition.
		// Callers can check the length if they need to handle this.
		_ = softLimit // Mark as used for warning threshold
	}

	return nil
}

// ValidateTokenCount provides a rough validation of token limits.
func ValidateTokenCount(messages []llm.CompletionMessage, maxTokens int) error {
	if maxTokens <= 0 {
		return nil // Skip validation if no limit specified
	}

	totalLength := 0
	for i := range messages {
		msg := &messages[i]
		// Rough approximation: 1 token ≈ 4 characters.
		totalLength += len(msg.Role) + len(msg.Content)
	}

	estimatedTokens := totalLength / 4

	if estimatedTokens > maxTokens {
		return MessageValidationError{
			Field:  "token_count",
			Value:  fmt.Sprintf("%d estimated", estimatedTokens),
			Reason: fmt.Sprintf("estimated tokens exceed limit of %d", maxTokens),
		}
	}

	return nil
}

// SanitizeMessage cleans up a message to make it valid.
func SanitizeMessage(msg *llm.CompletionMessage) llm.CompletionMessage {
	// Clean up role.
	roleStr := strings.TrimSpace(string(msg.Role))
	role := llm.CompletionRole(roleStr)
	if roleStr == "" || (role != llm.RoleUser && role != llm.RoleAssistant && role != llm.RoleSystem) {
		role = llm.RoleUser // Default to user role
	}

	// Clean up content.
	content := strings.TrimSpace(msg.Content)

	// Remove malformed role prefixes.
	if strings.HasPrefix(content, "[]") {
		content = strings.TrimSpace(content[2:])
	}

	// Remove other common bracket prefixes like "[assistant]", "[user]", etc.
	for _, prefix := range []string{"[assistant]", "[user]", "[system]", "[tool]"} {
		if strings.HasPrefix(strings.ToLower(content), prefix) {
			content = strings.TrimSpace(content[len(prefix):])
		}
	}

	// Ensure we have some content.
	if content == "" {
		content = "(empty message)"
	}

	return llm.CompletionMessage{
		Role:        role,
		Content:     content,
		ToolCalls:   msg.ToolCalls,
		ToolResults: msg.ToolResults,
	}
}

// ValidateAndSanitizeMessages validates and cleans up a slice of messages.
func ValidateAndSanitizeMessages(messages []llm.CompletionMessage) ([]llm.CompletionMessage, error) {
	if len(messages) == 0 {
		return nil, MessageValidationError{
			Field:  "messages",
			Value:  "[]",
			Reason: "at least one message is required",
		}
	}

	sanitized := make([]llm.CompletionMessage, 0, len(messages))

	for i := range messages {
		msg := &messages[i]
		cleaned := SanitizeMessage(msg)

		// Validate the cleaned message.
		if err := ValidateMessage(&cleaned); err != nil {
			return nil, fmt.Errorf("failed to sanitize message: %w", err)
		}

		sanitized = append(sanitized, cleaned)
	}

	return sanitized, nil
}
