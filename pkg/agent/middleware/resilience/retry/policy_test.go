package retry

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"orchestrator/pkg/agent/llmerrors"
)

// =============================================================================
// ShouldRetry classifier tests
// =============================================================================

func TestShouldRetry_NilError(t *testing.T) {
	if ShouldRetry(nil) {
		t.Error("Expected false for nil error")
	}
}

func TestShouldRetry_ContextCanceled(t *testing.T) {
	if ShouldRetry(context.Canceled) {
		t.Error("Expected false for context.Canceled")
	}
}

func TestShouldRetry_WrappedContextCanceled(t *testing.T) {
	err := fmt.Errorf("operation failed: %w", context.Canceled)
	if ShouldRetry(err) {
		t.Error("Expected false for wrapped context.Canceled")
	}
}

func TestShouldRetry_ContextDeadlineExceeded(t *testing.T) {
	// DeadlineExceeded SHOULD be retryable — per-request HTTP timeouts wrap
	// DeadlineExceeded but the parent context is still valid.
	if !ShouldRetry(context.DeadlineExceeded) {
		t.Error("Expected true for context.DeadlineExceeded (per-request timeouts should retry)")
	}
}

func TestShouldRetry_WrappedDeadlineExceeded(t *testing.T) {
	// Even when wrapped in other errors, DeadlineExceeded should still be retryable
	err := fmt.Errorf("http call failed: %w", context.DeadlineExceeded)
	if !ShouldRetry(err) {
		t.Error("Expected true for wrapped DeadlineExceeded")
	}
}

func TestShouldRetry_LLMAuthError(t *testing.T) {
	err := &llmerrors.Error{Type: llmerrors.ErrorTypeAuth, Message: "invalid api key"}
	if ShouldRetry(err) {
		t.Error("Expected false for auth error")
	}
}

func TestShouldRetry_LLMBadPromptError(t *testing.T) {
	err := &llmerrors.Error{Type: llmerrors.ErrorTypeBadPrompt, Message: "prompt too long"}
	if ShouldRetry(err) {
		t.Error("Expected false for bad prompt error")
	}
}

func TestShouldRetry_LLMServiceUnavailable(t *testing.T) {
	err := &llmerrors.Error{Type: llmerrors.ErrorTypeServiceUnavailable, Message: "all retries exhausted"}
	if ShouldRetry(err) {
		t.Error("Expected false for service unavailable (already exhausted retries)")
	}
}

func TestShouldRetry_LLMRateLimitError(t *testing.T) {
	err := &llmerrors.Error{Type: llmerrors.ErrorTypeRateLimit, Message: "rate limited"}
	if !ShouldRetry(err) {
		t.Error("Expected true for rate limit error")
	}
}

func TestShouldRetry_LLMUnknownError(t *testing.T) {
	err := &llmerrors.Error{Type: llmerrors.ErrorTypeUnknown, Message: "something went wrong"}
	if !ShouldRetry(err) {
		t.Error("Expected true for unknown LLM error")
	}
}

func TestShouldRetry_WrappedLLMAuthError(t *testing.T) {
	inner := &llmerrors.Error{Type: llmerrors.ErrorTypeAuth, Message: "invalid key"}
	err := fmt.Errorf("llm call failed: %w", inner)
	if ShouldRetry(err) {
		t.Error("Expected false for wrapped auth error")
	}
}

func TestShouldRetry_UnclassifiedAuthPatterns(t *testing.T) {
	patterns := []string{
		"HTTP 401 Unauthorized",
		"403 Forbidden",
		"unauthorized access to resource",
		"invalid api key provided",
	}
	for _, p := range patterns {
		if ShouldRetry(errors.New(p)) {
			t.Errorf("Expected false for auth pattern: %q", p)
		}
	}
}

func TestShouldRetry_UnclassifiedBadRequestPatterns(t *testing.T) {
	patterns := []string{
		"HTTP 400 Bad Request",
		"404 Not Found",
	}
	for _, p := range patterns {
		if ShouldRetry(errors.New(p)) {
			t.Errorf("Expected false for bad request pattern: %q", p)
		}
	}
}

func TestShouldRetry_UnknownErrors(t *testing.T) {
	// Unknown errors should be retryable (blocklist approach)
	unknowns := []string{
		"connection reset by peer",
		"timeout exceeded",
		"EOF",
		"something completely unexpected",
	}
	for _, msg := range unknowns {
		if !ShouldRetry(errors.New(msg)) {
			t.Errorf("Expected true for unknown error: %q", msg)
		}
	}
}

// =============================================================================
// DeadlineExceeded in LLM error chain test
// =============================================================================

func TestShouldRetry_DeadlineExceededWrappedInLLMError(t *testing.T) {
	// Simulates per-request HTTP timeout: DeadlineExceeded wrapped in an llmerrors.Error
	inner := fmt.Errorf("http request failed: %w", context.DeadlineExceeded)
	llmErr := &llmerrors.Error{
		Type:    llmerrors.ErrorTypeUnknown,
		Err:     inner,
		Message: "request timed out",
	}
	if !ShouldRetry(llmErr) {
		t.Error("Expected true: per-request timeout wrapped in LLM error should be retryable")
	}
}

// =============================================================================
// Policy tests
// =============================================================================

func TestNewPolicy_DefaultClassifier(t *testing.T) {
	p := NewPolicy(DefaultConfig, nil)
	if p.Classifier == nil {
		t.Error("Expected default classifier when nil passed")
	}
	// Verify it uses ShouldRetry behavior
	if p.ShouldRetry(nil) {
		t.Error("Expected false for nil error with default classifier")
	}
}

func TestNewPolicy_CustomClassifier(t *testing.T) {
	alwaysRetry := func(err error) bool { return err != nil }
	p := NewPolicy(DefaultConfig, alwaysRetry)

	if !p.ShouldRetry(errors.New("anything")) {
		t.Error("Expected custom classifier to be used")
	}
}

func TestCalculateDelay_FirstAttempt(t *testing.T) {
	p := NewPolicy(Config{
		InitialDelay:  time.Second,
		MaxDelay:      30 * time.Second,
		BackoffFactor: 2.0,
		Jitter:        false,
	}, nil)

	delay := p.CalculateDelay(1)
	if delay != 0 {
		t.Errorf("Expected 0 delay for first attempt, got: %v", delay)
	}
}

func TestCalculateDelay_ExponentialBackoff(t *testing.T) {
	p := NewPolicy(Config{
		InitialDelay:  time.Second,
		MaxDelay:      30 * time.Second,
		BackoffFactor: 2.0,
		Jitter:        false,
	}, nil)

	// Attempt 2: 1s * 2^0 = 1s
	delay2 := p.CalculateDelay(2)
	if delay2 != time.Second {
		t.Errorf("Expected 1s for attempt 2, got: %v", delay2)
	}

	// Attempt 3: 1s * 2^1 = 2s
	delay3 := p.CalculateDelay(3)
	if delay3 != 2*time.Second {
		t.Errorf("Expected 2s for attempt 3, got: %v", delay3)
	}

	// Attempt 4: 1s * 2^2 = 4s
	delay4 := p.CalculateDelay(4)
	if delay4 != 4*time.Second {
		t.Errorf("Expected 4s for attempt 4, got: %v", delay4)
	}
}

func TestCalculateDelay_MaxDelayCap(t *testing.T) {
	p := NewPolicy(Config{
		InitialDelay:  time.Second,
		MaxDelay:      5 * time.Second,
		BackoffFactor: 2.0,
		Jitter:        false,
	}, nil)

	// Attempt 10: 1s * 2^8 = 256s, but capped at 5s
	delay := p.CalculateDelay(10)
	if delay != 5*time.Second {
		t.Errorf("Expected 5s (max delay cap) for attempt 10, got: %v", delay)
	}
}

func TestCalculateDelay_WithJitter(t *testing.T) {
	p := NewPolicy(Config{
		InitialDelay:  time.Second,
		MaxDelay:      30 * time.Second,
		BackoffFactor: 2.0,
		Jitter:        true,
	}, nil)

	// With jitter, delay should be within ±10% of base delay
	delay := p.CalculateDelay(2)
	baseDelay := time.Second
	minDelay := baseDelay - time.Duration(float64(baseDelay)*0.1)
	maxDelay := baseDelay + time.Duration(float64(baseDelay)*0.1)

	if delay < minDelay || delay > maxDelay {
		t.Errorf("Expected delay within ±10%% of %v, got: %v", baseDelay, delay)
	}
}
