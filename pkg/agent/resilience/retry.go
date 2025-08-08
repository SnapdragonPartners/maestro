package resilience

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/agent/llmerrors"
	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/utils"
)

// RetryConfig defines configuration for retry behavior.
type RetryConfig struct {
	MaxRetries    int           // Maximum number of retry attempts
	InitialDelay  time.Duration // Initial delay before first retry
	MaxDelay      time.Duration // Maximum delay between retries
	BackoffFactor float64       // Multiplier for exponential backoff
	Jitter        bool          // Add random jitter to prevent thundering herd
}

// DefaultRetryConfig provides reasonable defaults for retry behavior.
var DefaultRetryConfig = RetryConfig{ //nolint:gochecknoglobals
	MaxRetries:    3,
	InitialDelay:  100 * time.Millisecond,
	MaxDelay:      10 * time.Second,
	BackoffFactor: 2.0,
	Jitter:        true,
}

// RetryableError interface allows errors to specify if they should be retried.
type RetryableError interface {
	error
	ShouldRetry() bool
}

// TransientError represents an error that should be retried.
type TransientError struct {
	Underlying error
	Retryable  bool
}

func (e *TransientError) Error() string {
	return fmt.Sprintf("transient error: %v", e.Underlying)
}

// ShouldRetry returns whether this error should be retried.
func (e *TransientError) ShouldRetry() bool {
	return e.Retryable
}

func (e *TransientError) Unwrap() error {
	return e.Underlying
}

// NewTransientError creates a new transient error.
func NewTransientError(err error) *TransientError {
	return &TransientError{
		Underlying: err,
		Retryable:  true,
	}
}

// RetryableClient wraps an llm.LLMClient with retry logic.
type RetryableClient struct {
	client llm.LLMClient
	logger *logx.Logger
	config RetryConfig
}

// NewRetryableClient creates a new retryable LLM client.
func NewRetryableClient(client llm.LLMClient, config RetryConfig) *RetryableClient {
	return NewRetryableClientWithLogger(client, config, nil)
}

// NewRetryableClientWithLogger creates a new retryable LLM client with logging.
func NewRetryableClientWithLogger(client llm.LLMClient, config RetryConfig, logger *logx.Logger) *RetryableClient {
	return &RetryableClient{
		client: client,
		config: config,
		logger: logger,
	}
}

// Complete implements llm.LLMClient with retry logic.
func (r *RetryableClient) Complete(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
	var lastErr error
	var retryConfig llmerrors.RetryConfig
	var errorType llmerrors.ErrorType
	startTime := time.Now()

	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			// Calculate delay with error-type-specific exponential backoff
			delay := r.calculateDelayForError(attempt, retryConfig)

			// Check if context is still valid before sleeping.
			select {
			case <-ctx.Done():
				return llm.CompletionResponse{}, fmt.Errorf("retry cancelled: %w", ctx.Err())
			case <-time.After(delay):
				// Continue with retry.
			}
		}

		attemptStart := time.Now()
		resp, err := r.client.Complete(ctx, req)
		attemptDuration := time.Since(attemptStart)

		if err == nil {
			// Log successful request
			if r.logger != nil {
				r.logger.Debug("Retry client: successful completion after %d attempts in %v", attempt, attemptDuration)
			}
			return resp, nil
		}

		lastErr = err

		// Get retry configuration based on error type
		retryConfig, errorType = r.getRetryConfigForError(err)

		// Determine if this is the final attempt
		isFinalAttempt := !r.shouldRetry(err) || attempt >= retryConfig.MaxRetries

		// Handle empty response errors by panicking with full context (fail-fast debugging)
		if errorType == llmerrors.ErrorTypeEmptyResponse {
			// Build complete prompt content for analysis
			promptContent := ""
			totalChars := 0
			for i := range req.Messages {
				if i > 0 {
					promptContent += "\n---\n"
				}
				msgContent := fmt.Sprintf("Role: %s\nContent: %s", req.Messages[i].Role, req.Messages[i].Content)
				promptContent += msgContent
				totalChars += len(req.Messages[i].Content)
			}

			// Log with detailed debugging information before panicking
			fmt.Printf("\n=== [FATAL] EMPTY RESPONSE ERROR - Panic for debugging ===\n")
			fmt.Printf("Error Type: %T\n", err)
			fmt.Printf("Error Message: %s\n", err.Error())
			fmt.Printf("Classified As: %s\n", errorType.String())
			fmt.Printf("Attempt: %d/%d\n", attempt+1, retryConfig.MaxRetries)
			fmt.Printf("Request MaxTokens: %d\n", req.MaxTokens)
			fmt.Printf("Request Temperature: %.2f\n", req.Temperature)
			fmt.Printf("Messages Count: %d\n", len(req.Messages))
			fmt.Printf("Total Content Length: %d chars\n", totalChars)
			fmt.Printf("Full Prompt Length: %d chars\n", len(promptContent))

			// Add token count estimation
			estimatedTokens := utils.CountTokensSimple(promptContent)
			fmt.Printf("Estimated Tokens: %d\n", estimatedTokens)

			// Show complete prompt for debugging
			fmt.Printf("Complete Prompt for Debugging:\n%s\n", promptContent)
			fmt.Printf("Full Error Details: %+v\n", err)
			fmt.Printf("=========================================================================\n\n")

			// Panic with detailed context to force immediate debugging
			panic(fmt.Sprintf("Empty response from Claude API - full context logged above. Error: %v", err))
		}

		// Add aggressive logging for unknown errors to help debug circuit breaker issues
		if errorType == llmerrors.ErrorTypeUnknown {
			// Build complete prompt content for analysis
			promptContent := ""
			totalChars := 0
			for i := range req.Messages {
				if i > 0 {
					promptContent += "\n---\n"
				}
				msgContent := fmt.Sprintf("Role: %s\nContent: %s", req.Messages[i].Role, req.Messages[i].Content)
				promptContent += msgContent
				totalChars += len(req.Messages[i].Content)
			}

			// Log aggressively to understand what's failing
			logLevel := "WARN"
			errorTypeDesc := "UNKNOWN ERROR"
			if errorType == llmerrors.ErrorTypeEmptyResponse {
				errorTypeDesc = "EMPTY RESPONSE ERROR"
			}
			if isFinalAttempt {
				logLevel = "ERROR"
			}

			// Log with detailed debugging information
			fmt.Printf("\n=== [%s] %s - Circuit breaker debugging ===\n", logLevel, errorTypeDesc)
			fmt.Printf("Error Type: %T\n", err)
			fmt.Printf("Error Message: %s\n", err.Error())
			fmt.Printf("Classified As: %s\n", errorType.String())
			fmt.Printf("Attempt: %d/%d\n", attempt+1, retryConfig.MaxRetries)
			fmt.Printf("Is Final: %v\n", isFinalAttempt)
			fmt.Printf("Request MaxTokens: %d\n", req.MaxTokens)
			fmt.Printf("Request Temperature: %.2f\n", req.Temperature)
			fmt.Printf("Messages Count: %d\n", len(req.Messages))
			fmt.Printf("Total Content Length: %d chars\n", totalChars)
			fmt.Printf("Full Prompt Length: %d chars\n", len(promptContent))

			// Add token count estimation for better debugging
			estimatedTokens := utils.CountTokensSimple(promptContent)
			fmt.Printf("Estimated Tokens: %d\n", estimatedTokens)

			// Show prompt content with better truncation
			const maxDisplayChars = 2000
			if len(promptContent) <= maxDisplayChars {
				fmt.Printf("Full Prompt:\n%s\n", promptContent)
			} else {
				// Show first and last portions for better debugging
				firstChars := maxDisplayChars / 2
				lastChars := maxDisplayChars / 2
				fmt.Printf("Prompt (first %d chars):\n%s\n", firstChars, promptContent[:firstChars])
				fmt.Printf("... [TRUNCATED %d chars] ...\n", len(promptContent)-maxDisplayChars)
				fmt.Printf("Prompt (last %d chars):\n%s\n", lastChars, promptContent[len(promptContent)-lastChars:])
			}

			fmt.Printf("Full Error Details: %+v\n", err)
			fmt.Printf("=========================================================================\n\n")
		}

		// Log request if configured to do so
		if r.logger != nil {
			r.logger.Debug("Retry client: attempt %d failed in %v, error: %v, final: %v", attempt, attemptDuration, err, isFinalAttempt)
		}

		// Check if error should be retried and we haven't exceeded max attempts
		if isFinalAttempt {
			break
		}
	}

	totalDuration := time.Since(startTime)
	return llm.CompletionResponse{}, fmt.Errorf("failed after %d retries (%s) in %v: %w",
		retryConfig.MaxRetries, errorType.String(), totalDuration, lastErr)
}

// Stream implements llm.LLMClient with retry logic for streaming.
func (r *RetryableClient) Stream(ctx context.Context, req llm.CompletionRequest) (<-chan llm.StreamChunk, error) {
	var lastErr error
	var retryConfig llmerrors.RetryConfig
	var errorType llmerrors.ErrorType

	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			delay := r.calculateDelayForError(attempt, retryConfig)
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("stream retry cancelled: %w", ctx.Err())
			case <-time.After(delay):
			}
		}

		ch, err := r.client.Stream(ctx, req)
		if err == nil {
			return ch, nil
		}

		lastErr = err

		// Get retry configuration based on error type
		retryConfig, errorType = r.getRetryConfigForError(err)

		// Check if error should be retried and we haven't exceeded max attempts
		if !r.shouldRetry(err) || attempt >= retryConfig.MaxRetries {
			break
		}
	}

	return nil, fmt.Errorf("failed to establish stream after %d retries (%s): %w", retryConfig.MaxRetries, errorType.String(), lastErr)
}

// shouldRetry determines if an error should be retried based on its classified type.
func (r *RetryableClient) shouldRetry(err error) bool {
	// Check if error implements RetryableError interface first
	var retryableErr RetryableError
	if errors.As(err, &retryableErr) {
		return retryableErr.ShouldRetry()
	}

	// Check if it's our classified LLM error
	var llmErr *llmerrors.Error
	if errors.As(err, &llmErr) {
		return llmErr.IsRetryable()
	}

	// Fallback to old string-based logic for unclassified errors
	errStr := err.Error()

	// Retry on network/timeout errors.
	if strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "connection") ||
		strings.Contains(errStr, "network") ||
		strings.Contains(errStr, "temporary") {
		return true
	}

	// Retry on rate limiting.
	if strings.Contains(errStr, "rate") || strings.Contains(errStr, "429") {
		return true
	}

	// Retry on server errors (5xx)
	if strings.Contains(errStr, "500") ||
		strings.Contains(errStr, "502") ||
		strings.Contains(errStr, "503") ||
		strings.Contains(errStr, "504") {
		return true
	}

	// Don't retry on empty responses - panic for immediate debugging instead
	if strings.Contains(errStr, "empty response") {
		return false
	}

	// Don't retry on client errors (4xx) except rate limiting
	if strings.Contains(errStr, "400") ||
		strings.Contains(errStr, "401") ||
		strings.Contains(errStr, "403") ||
		strings.Contains(errStr, "404") {
		return false
	}

	// Default to not retrying for unknown errors.
	return false
}

// getRetryConfigForError returns the appropriate retry configuration for an error.
func (r *RetryableClient) getRetryConfigForError(err error) (llmerrors.RetryConfig, llmerrors.ErrorType) {
	// Check if it's our classified LLM error
	var llmErr *llmerrors.Error
	if errors.As(err, &llmErr) {
		return llmErr.GetRetryConfig(), llmErr.Type
	}

	// Log unknown errors aggressively for debugging - we'll add better logging where the error occurs

	// Convert our legacy config to the new format for unclassified errors
	legacyConfig := llmerrors.RetryConfig{
		MaxRetries:    r.config.MaxRetries,
		InitialDelay:  r.config.InitialDelay,
		MaxDelay:      r.config.MaxDelay,
		BackoffFactor: r.config.BackoffFactor,
		Jitter:        r.config.Jitter,
	}
	return legacyConfig, llmerrors.ErrorTypeUnknown
}

// calculateDelayForError computes the delay for the given retry attempt using error-specific config.
func (r *RetryableClient) calculateDelayForError(attempt int, config llmerrors.RetryConfig) time.Duration {
	if attempt == 0 {
		return 0
	}

	delay := time.Duration(float64(config.InitialDelay) * math.Pow(config.BackoffFactor, float64(attempt-1)))

	// Cap at maximum delay.
	if delay > config.MaxDelay {
		delay = config.MaxDelay
	}

	// Add jitter if enabled.
	if config.Jitter {
		jitterFactor := (2*time.Now().UnixNano()%2 - 1) // -1 or 1
		jitter := time.Duration(float64(delay) * 0.1 * float64(jitterFactor))
		delay += jitter
		if delay < 0 {
			delay = config.InitialDelay
		}
	}

	return delay
}

// GetDefaultConfig delegates to the underlying client.
func (r *RetryableClient) GetDefaultConfig() config.Model {
	return r.client.GetDefaultConfig()
}
