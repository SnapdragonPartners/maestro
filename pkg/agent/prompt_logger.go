package agent

import (
	"context"
	"errors"
	"time"

	"orchestrator/pkg/agent/llmerrors"
	"orchestrator/pkg/logx"
)

// PromptLogMode defines when prompts should be logged.
type PromptLogMode string

const (
	// PromptLogOff disables prompt logging completely.
	PromptLogOff PromptLogMode = "off" // Never log prompts
	// PromptLogOnFailure logs prompts on any failure.
	PromptLogOnFailure PromptLogMode = "on_failure" // Log prompts on any failure
	// PromptLogFinalOnly logs prompts only on final failure after all retries.
	PromptLogFinalOnly PromptLogMode = "final_only" // Log prompts only on final failure after all retries
)

// PromptLogConfig configures prompt logging behavior.
type PromptLogConfig struct {
	Mode        PromptLogMode // When to log prompts
	MaxChars    int           // Maximum characters to log (truncate with hash if larger)
	IncludeHash bool          // Include hash of full prompt for correlation
}

// DefaultPromptLogConfig provides sensible defaults.
//
//nolint:gochecknoglobals // Configuration struct - acceptable for package defaults
var DefaultPromptLogConfig = PromptLogConfig{
	Mode:        PromptLogFinalOnly,
	MaxChars:    4000,
	IncludeHash: true,
}

// PromptLogger handles conditional logging of prompts based on configuration.
type PromptLogger struct {
	logger *logx.Logger
	config PromptLogConfig
}

// NewPromptLogger creates a new prompt logger with the given configuration.
func NewPromptLogger(config PromptLogConfig, logger *logx.Logger) *PromptLogger {
	return &PromptLogger{
		config: config,
		logger: logger,
	}
}

// LogRequest logs a prompt request if conditions are met.
func (pl *PromptLogger) LogRequest(
	_ context.Context,
	req CompletionRequest,
	err error,
	attempt int,
	isFinalAttempt bool,
	duration time.Duration,
) {
	if pl.config.Mode == PromptLogOff {
		return
	}

	// Determine if we should log based on mode and conditions
	shouldLog := false
	switch pl.config.Mode {
	case PromptLogOnFailure:
		shouldLog = err != nil
	case PromptLogFinalOnly:
		shouldLog = err != nil && isFinalAttempt
	}

	if !shouldLog {
		return
	}

	// Extract prompt content from messages
	promptContent := pl.extractPromptContent(req)

	// Sanitize prompt for logging
	sanitizedPrompt := llmerrors.SanitizePrompt(promptContent, pl.config.MaxChars)

	// Get error type information
	errorType := llmerrors.TypeOf(err)
	var statusCode int
	var llmErr *llmerrors.Error
	if errors.As(err, &llmErr) {
		statusCode = llmErr.StatusCode
	}

	// Calculate approximate token count (rough estimate: 4 chars per token)
	approxTokens := len(promptContent) / 4

	// Log with structured information
	pl.logger.Warn("LLM request failed - prompt logged for debugging",
		"error_type", errorType.String(),
		"status_code", statusCode,
		"attempt", attempt,
		"final_attempt", isFinalAttempt,
		"duration_ms", duration.Milliseconds(),
		"prompt_chars", len(promptContent),
		"approx_tokens", approxTokens,
		"max_tokens", req.MaxTokens,
		"tools_count", len(req.Tools),
		"messages_count", len(req.Messages),
		"error", err.Error(),
		"prompt", sanitizedPrompt,
	)
}

// LogSuccess logs successful requests at debug level for metrics.
func (pl *PromptLogger) LogSuccess(
	_ context.Context,
	req CompletionRequest,
	resp CompletionResponse,
	attempt int,
	duration time.Duration,
) {
	// Only log success metrics, not the full prompt
	promptLength := pl.calculatePromptLength(req)
	approxTokens := promptLength / 4

	pl.logger.Debug("LLM request succeeded",
		"attempt", attempt,
		"duration_ms", duration.Milliseconds(),
		"prompt_chars", promptLength,
		"approx_tokens", approxTokens,
		"response_chars", len(resp.Content),
		"tool_calls", len(resp.ToolCalls),
		"max_tokens", req.MaxTokens,
	)
}

// extractPromptContent extracts the full prompt content from a completion request.
func (pl *PromptLogger) extractPromptContent(req CompletionRequest) string {
	var content string

	for i := range req.Messages {
		msg := &req.Messages[i]
		if i > 0 {
			content += "\n\n"
		}
		content += "[" + string(msg.Role) + "]: " + msg.Content
	}

	return content
}

// calculatePromptLength calculates the total character length of all messages.
func (pl *PromptLogger) calculatePromptLength(req CompletionRequest) int {
	total := 0
	for i := range req.Messages {
		total += len(req.Messages[i].Content)
	}
	return total
}
