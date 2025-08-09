// Package retry provides retry logic with exponential backoff for resilient LLM calls.
package retry

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"orchestrator/pkg/agent/middleware/resilience/circuit"
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
//
//nolint:gochecknoglobals // Sensible default config pattern
var DefaultConfig = Config{
	MaxAttempts:   3,
	InitialDelay:  100 * time.Millisecond,
	MaxDelay:      10 * time.Second,
	BackoffFactor: 2.0,
	Jitter:        true,
}

// Classifier determines if an error should be retried.
type Classifier func(error) bool

// ShouldRetry is the default error classifier that determines retry behavior.
func ShouldRetry(err error) bool {
	if err == nil {
		return false
	}

	// Never retry context cancellation or deadline exceeded
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// Never retry circuit breaker errors - let the circuit breaker handle recovery
	var circuitErr *circuit.Error
	if errors.As(err, &circuitErr) {
		return false
	}

	// Check error string for retry patterns
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

	// Don't retry on client errors (4xx) except rate limiting
	if strings.Contains(errStr, "400") ||
		strings.Contains(errStr, "401") ||
		strings.Contains(errStr, "403") ||
		strings.Contains(errStr, "404") {
		return false
	}

	// Default to not retrying unknown errors
	return false
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
