// Package retry provides retry logic with exponential backoff for resilient LLM calls.
package retry

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"orchestrator/pkg/agent/llmerrors"
)

// Config defines configuration for retry behavior.
type Config struct {
	MaxAttempts   int           `json:"max_attempts"`   // Maximum number of attempts (including initial)
	InitialDelay  time.Duration `json:"initial_delay"`  // Initial delay before first retry
	MaxDelay      time.Duration `json:"max_delay"`      // Maximum delay between retries
	BackoffFactor float64       `json:"backoff_factor"` // Multiplier for exponential backoff
	Jitter        bool          `json:"jitter"`         // Add random jitter to prevent thundering herd
}

// DefaultConfig provides reasonable defaults for retry behavior.
// Timing: 0ms -> ~1s -> ~2s -> ~4s -> ~8s (±10% jitter)
//
//nolint:gochecknoglobals // Sensible default config pattern
var DefaultConfig = Config{
	MaxAttempts:   5,
	InitialDelay:  1 * time.Second,
	MaxDelay:      30 * time.Second,
	BackoffFactor: 2.0,
	Jitter:        true,
}

// Classifier determines if an error should be retried.
type Classifier func(error) bool

// ShouldRetry is the default error classifier that determines retry behavior.
// Uses a blocklist approach: everything is retryable UNLESS explicitly non-retryable.
// This ensures unknown/unclassified errors (like network timeouts classified as "unknown")
// are retried, eventually producing ServiceUnavailableError to trigger SUSPEND.
func ShouldRetry(err error) bool {
	if err == nil {
		return false
	}

	// Never retry context cancellation (real shutdown).
	// Note: Do NOT check context.DeadlineExceeded here — per-request HTTP timeouts
	// wrap DeadlineExceeded but should be retried so the retry middleware can exhaust
	// attempts and emit ServiceUnavailableError to trigger SUSPEND.
	if errors.Is(err, context.Canceled) {
		return false
	}

	// Check if this is a classified LLM error with explicit non-retryable type
	var llmErr *llmerrors.Error
	if errors.As(err, &llmErr) {
		switch llmErr.Type {
		case llmerrors.ErrorTypeAuth, llmerrors.ErrorTypeBadPrompt:
			return false // Explicitly non-retryable
		case llmerrors.ErrorTypeServiceUnavailable:
			return false // Already exhausted retries
		default:
			return true // Everything else: retry
		}
	}

	// For unclassified errors, check for non-retryable patterns
	errStr := strings.ToLower(err.Error())

	// Don't retry auth errors
	if strings.Contains(errStr, "401") || strings.Contains(errStr, "403") ||
		strings.Contains(errStr, "unauthorized") || strings.Contains(errStr, "invalid api key") {
		return false
	}

	// Don't retry bad request errors
	if strings.Contains(errStr, "400") || strings.Contains(errStr, "404") {
		return false
	}

	// Default: retry everything else (including unknown errors, circuit breaker errors, etc.)
	return true
}

// Policy encapsulates retry configuration and logic.
//
//nolint:govet // Simple struct, logical grouping preferred
type Policy struct {
	Config     Config
	Classifier Classifier
}

// NewPolicy creates a new retry policy with the given configuration and classifier.
func NewPolicy(config Config, classifier Classifier) *Policy {
	if classifier == nil {
		classifier = ShouldRetry
	}
	return &Policy{
		Config:     config,
		Classifier: classifier,
	}
}

// CalculateDelay computes the delay for the given attempt number.
func (p *Policy) CalculateDelay(attempt int) time.Duration {
	if attempt <= 1 {
		return 0
	}

	delay := time.Duration(float64(p.Config.InitialDelay) * math.Pow(p.Config.BackoffFactor, float64(attempt-2)))

	// Cap at maximum delay
	if delay > p.Config.MaxDelay {
		delay = p.Config.MaxDelay
	}

	// Add jitter if enabled
	if p.Config.Jitter && delay > 0 {
		jitterFactor := (2*time.Now().UnixNano()%2 - 1) // -1 or 1
		jitter := time.Duration(float64(delay) * 0.1 * float64(jitterFactor))
		delay += jitter
		if delay < 0 {
			delay = p.Config.InitialDelay
		}
	}

	return delay
}

// ShouldRetry determines if an error should be retried based on the configured classifier.
func (p *Policy) ShouldRetry(err error) bool {
	return p.Classifier(err)
}
