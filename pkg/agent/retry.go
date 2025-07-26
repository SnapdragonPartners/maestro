package agent

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"orchestrator/pkg/agent/llmerrors"
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

// RetryableClient wraps an LLMClient with retry logic.
type RetryableClient struct {
	client       LLMClient
	promptLogger *PromptLogger
	config       RetryConfig
}

// NewRetryableClient creates a new retryable LLM client.
func NewRetryableClient(client LLMClient, config RetryConfig) *RetryableClient {
	return NewRetryableClientWithLogger(client, config, nil)
}

// NewRetryableClientWithLogger creates a new retryable LLM client with prompt logging.
func NewRetryableClientWithLogger(client LLMClient, config RetryConfig, promptLogger *PromptLogger) *RetryableClient {
	return &RetryableClient{
		client:       client,
		config:       config,
		promptLogger: promptLogger,
	}
}

// Complete implements LLMClient with retry logic.
func (r *RetryableClient) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
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
				return CompletionResponse{}, fmt.Errorf("retry cancelled: %w", ctx.Err())
			case <-time.After(delay):
				// Continue with retry.
			}
		}

		attemptStart := time.Now()
		resp, err := r.client.Complete(ctx, req)
		attemptDuration := time.Since(attemptStart)

		if err == nil {
			// Log successful request
			if r.promptLogger != nil {
				r.promptLogger.LogSuccess(ctx, req, resp, attempt, attemptDuration)
			}
			return resp, nil
		}

		lastErr = err

		// Get retry configuration based on error type
		retryConfig, errorType = r.getRetryConfigForError(err)

		// Determine if this is the final attempt
		isFinalAttempt := !r.shouldRetry(err) || attempt >= retryConfig.MaxRetries

		// Log prompt if configured to do so
		if r.promptLogger != nil {
			r.promptLogger.LogRequest(ctx, req, err, attempt, isFinalAttempt, attemptDuration)
		}

		// Check if error should be retried and we haven't exceeded max attempts
		if isFinalAttempt {
			break
		}
	}

	totalDuration := time.Since(startTime)
	return CompletionResponse{}, fmt.Errorf("failed after %d retries (%s) in %v: %w",
		retryConfig.MaxRetries, errorType.String(), totalDuration, lastErr)
}

// Stream implements LLMClient with retry logic for streaming.
func (r *RetryableClient) Stream(ctx context.Context, req CompletionRequest) (<-chan StreamChunk, error) {
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

	// Retry on empty responses from LLM.
	if strings.Contains(errStr, "empty response") {
		return true
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
