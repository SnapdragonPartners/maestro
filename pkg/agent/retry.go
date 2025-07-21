package agent

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
)

// RetryConfig defines configuration for retry behavior
type RetryConfig struct {
	MaxRetries    int           // Maximum number of retry attempts
	InitialDelay  time.Duration // Initial delay before first retry
	MaxDelay      time.Duration // Maximum delay between retries
	BackoffFactor float64       // Multiplier for exponential backoff
	Jitter        bool          // Add random jitter to prevent thundering herd
}

// DefaultRetryConfig provides reasonable defaults for retry behavior
var DefaultRetryConfig = RetryConfig{
	MaxRetries:    3,
	InitialDelay:  100 * time.Millisecond,
	MaxDelay:      10 * time.Second,
	BackoffFactor: 2.0,
	Jitter:        true,
}

// RetryableError interface allows errors to specify if they should be retried
type RetryableError interface {
	error
	ShouldRetry() bool
}

// TransientError represents an error that should be retried
type TransientError struct {
	Underlying error
	Retryable  bool
}

func (e *TransientError) Error() string {
	return fmt.Sprintf("transient error: %v", e.Underlying)
}

func (e *TransientError) ShouldRetry() bool {
	return e.Retryable
}

func (e *TransientError) Unwrap() error {
	return e.Underlying
}

// NewTransientError creates a new transient error
func NewTransientError(err error) *TransientError {
	return &TransientError{
		Underlying: err,
		Retryable:  true,
	}
}

// RetryableClient wraps an LLMClient with retry logic
type RetryableClient struct {
	client LLMClient
	config RetryConfig
}

// NewRetryableClient creates a new retryable LLM client
func NewRetryableClient(client LLMClient, config RetryConfig) *RetryableClient {
	return &RetryableClient{
		client: client,
		config: config,
	}
}

// Complete implements LLMClient with retry logic
func (r *RetryableClient) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	var lastErr error

	for attempt := 0; attempt <= r.config.MaxRetries; attempt++ {
		if attempt > 0 {
			// Calculate delay with exponential backoff
			delay := r.calculateDelay(attempt)

			// Check if context is still valid before sleeping
			select {
			case <-ctx.Done():
				return CompletionResponse{}, ctx.Err()
			case <-time.After(delay):
				// Continue with retry
			}
		}

		resp, err := r.client.Complete(ctx, req)
		if err == nil {
			return resp, nil
		}

		lastErr = err

		// Check if error should be retried
		if !r.shouldRetry(err) {
			break
		}

		// Check if we've reached max attempts
		if attempt == r.config.MaxRetries {
			break
		}
	}

	return CompletionResponse{}, fmt.Errorf("failed after %d retries: %w", r.config.MaxRetries, lastErr)
}

// Stream implements LLMClient with retry logic for streaming
func (r *RetryableClient) Stream(ctx context.Context, req CompletionRequest) (<-chan StreamChunk, error) {
	var lastErr error

	for attempt := 0; attempt <= r.config.MaxRetries; attempt++ {
		if attempt > 0 {
			delay := r.calculateDelay(attempt)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		ch, err := r.client.Stream(ctx, req)
		if err == nil {
			return ch, nil
		}

		lastErr = err

		if !r.shouldRetry(err) {
			break
		}

		if attempt == r.config.MaxRetries {
			break
		}
	}

	return nil, fmt.Errorf("failed to establish stream after %d retries: %w", r.config.MaxRetries, lastErr)
}

// calculateDelay computes the delay for the given retry attempt
func (r *RetryableClient) calculateDelay(attempt int) time.Duration {
	delay := time.Duration(float64(r.config.InitialDelay) * math.Pow(r.config.BackoffFactor, float64(attempt-1)))

	// Cap at maximum delay
	if delay > r.config.MaxDelay {
		delay = r.config.MaxDelay
	}

	// Add jitter if enabled
	if r.config.Jitter {
		jitterFactor := (2*time.Now().UnixNano()%2 - 1) // -1 or 1
		jitter := time.Duration(float64(delay) * 0.1 * float64(jitterFactor))
		delay += jitter
		if delay < 0 {
			delay = r.config.InitialDelay
		}
	}

	return delay
}

// shouldRetry determines if an error should be retried
func (r *RetryableClient) shouldRetry(err error) bool {
	// Check if error implements RetryableError
	if retryableErr, ok := err.(RetryableError); ok {
		return retryableErr.ShouldRetry()
	}

	// Default retry logic for common error patterns
	errStr := err.Error()

	// Retry on network/timeout errors
	if strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "connection") ||
		strings.Contains(errStr, "network") ||
		strings.Contains(errStr, "temporary") {
		return true
	}

	// Retry on rate limiting
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

	// Retry on empty responses from LLM
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

	// Default to not retrying for unknown errors
	return false
}
