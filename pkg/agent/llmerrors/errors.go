// Package llmerrors provides structured error classification and retry configuration for LLM API interactions.
package llmerrors

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"time"
)

// ErrorType represents different categories of LLM errors for retry logic.
type ErrorType int8

const (
	// Retryable error types.

	// ErrorTypeRateLimit represents rate limiting errors (429, quota exceeded).
	ErrorTypeRateLimit ErrorType = iota
	// ErrorTypeTransient represents transient errors (5xx, EOF, connection reset, timeout).
	ErrorTypeTransient
	// ErrorTypeEmptyResponse represents HTTP 200 but no content errors.
	ErrorTypeEmptyResponse

	// Non-retryable error types.

	// ErrorTypeAuth represents authentication errors (401/403, bad API key).
	ErrorTypeAuth
	// ErrorTypeBadPrompt represents malformed request errors (too long, violates policy).
	ErrorTypeBadPrompt
	// ErrorTypeUnknown represents default for unclassified errors.
	ErrorTypeUnknown

	// Special error types for system-level handling.

	// ErrorTypeServiceUnavailable represents persistent service unavailability after retries exhausted.
	// When an agent receives this error, it should transition to SUSPEND state.
	// This is different from ErrorTypeTransient which is retried automatically.
	ErrorTypeServiceUnavailable
)

// String returns the string representation of the error type.
func (et ErrorType) String() string {
	switch et {
	case ErrorTypeRateLimit:
		return "rate_limit"
	case ErrorTypeTransient:
		return "transient"
	case ErrorTypeEmptyResponse:
		return "empty_response"
	case ErrorTypeAuth:
		return "auth"
	case ErrorTypeBadPrompt:
		return "bad_prompt"
	case ErrorTypeUnknown:
		return "unknown"
	case ErrorTypeServiceUnavailable:
		return "service_unavailable"
	default:
		return "invalid"
	}
}

// Default retry constants - eventually overridable via config.
const (
	DefaultEmptyResponseRetries = 5
	DefaultRateLimitRetries     = 6
	DefaultTransientRetries     = 4
	DefaultAuthRetries          = 0
	DefaultBadPromptRetries     = 0
	DefaultUnknownRetries       = 1
)

// RetryConfig defines exponential backoff configuration for each error type.
type RetryConfig struct {
	MaxRetries    int           // Maximum number of retry attempts
	InitialDelay  time.Duration // Initial delay for exponential backoff
	MaxDelay      time.Duration // Maximum delay between retries
	BackoffFactor float64       // Multiplier for exponential backoff
	Jitter        bool          // Add random jitter to prevent thundering herd
}

// DefaultRetryConfigs provides default retry configurations for each error type.
//
//nolint:gochecknoglobals // Configuration map - acceptable for package defaults
var DefaultRetryConfigs = map[ErrorType]RetryConfig{
	ErrorTypeEmptyResponse: {
		MaxRetries:    DefaultEmptyResponseRetries,
		InitialDelay:  2 * time.Second,
		MaxDelay:      30 * time.Second,
		BackoffFactor: 2.0,
		Jitter:        true,
	},
	ErrorTypeRateLimit: {
		MaxRetries:    DefaultRateLimitRetries,
		InitialDelay:  1 * time.Second,
		MaxDelay:      60 * time.Second,
		BackoffFactor: 2.0,
		Jitter:        true,
	},
	ErrorTypeTransient: {
		MaxRetries:    DefaultTransientRetries,
		InitialDelay:  500 * time.Millisecond,
		MaxDelay:      10 * time.Second,
		BackoffFactor: 2.0,
		Jitter:        true,
	},
	ErrorTypeAuth: {
		MaxRetries:    DefaultAuthRetries,
		InitialDelay:  0,
		MaxDelay:      0,
		BackoffFactor: 1.0,
		Jitter:        false,
	},
	ErrorTypeBadPrompt: {
		MaxRetries:    DefaultBadPromptRetries,
		InitialDelay:  0,
		MaxDelay:      0,
		BackoffFactor: 1.0,
		Jitter:        false,
	},
	ErrorTypeUnknown: {
		MaxRetries:    DefaultUnknownRetries,
		InitialDelay:  1 * time.Second,
		MaxDelay:      5 * time.Second,
		BackoffFactor: 2.0,
		Jitter:        true,
	},
	ErrorTypeServiceUnavailable: {
		MaxRetries:    0, // No retries - this error is emitted after retries exhausted
		InitialDelay:  0,
		MaxDelay:      0,
		BackoffFactor: 1.0,
		Jitter:        false,
	},
}

// Error represents a classified LLM error with retry metadata.
type Error struct {
	Err        error     // Wrapped underlying error
	Message    string    // Human-readable error message
	BodyStub   string    // First portion of response body (guards PII)
	Type       ErrorType // Classified error type
	StatusCode int       // HTTP status code if applicable
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("LLM error (%s): %s", e.Type.String(), e.Message)
	}
	if e.Err != nil {
		return fmt.Sprintf("LLM error (%s): %v", e.Type.String(), e.Err)
	}
	return fmt.Sprintf("LLM error (%s): status %d", e.Type.String(), e.StatusCode)
}

// Unwrap returns the underlying error for error unwrapping.
func (e *Error) Unwrap() error {
	return e.Err
}

// IsRetryable returns whether this error type should be retried.
// Uses blocklist approach: everything is retryable UNLESS explicitly non-retryable.
func (e *Error) IsRetryable() bool {
	switch e.Type {
	case ErrorTypeAuth, ErrorTypeBadPrompt, ErrorTypeServiceUnavailable:
		return false
	default:
		return true
	}
}

// GetRetryConfig returns the retry configuration for this error type.
func (e *Error) GetRetryConfig() RetryConfig {
	if config, exists := DefaultRetryConfigs[e.Type]; exists {
		return config
	}
	return DefaultRetryConfigs[ErrorTypeUnknown]
}

// Helper functions for error classification and checking

// Is checks if an error is of a specific type.
func Is(err error, errorType ErrorType) bool {
	var llmErr *Error
	if errors.As(err, &llmErr) {
		return llmErr.Type == errorType
	}
	return false
}

// TypeOf returns the error type of an error, or ErrorTypeUnknown if not classified.
func TypeOf(err error) ErrorType {
	var llmErr *Error
	if errors.As(err, &llmErr) {
		return llmErr.Type
	}
	return ErrorTypeUnknown
}

// NewError creates a new classified LLM error.
func NewError(errorType ErrorType, message string) *Error {
	return &Error{
		Type:    errorType,
		Message: message,
	}
}

// NewErrorWithStatus creates a new classified LLM error with HTTP status.
func NewErrorWithStatus(errorType ErrorType, statusCode int, message string) *Error {
	return &Error{
		Type:       errorType,
		StatusCode: statusCode,
		Message:    message,
	}
}

// NewErrorWithCause creates a new classified LLM error wrapping another error.
func NewErrorWithCause(errorType ErrorType, cause error, message string) *Error {
	return &Error{
		Type:    errorType,
		Err:     cause,
		Message: message,
	}
}

// SanitizePrompt creates a safe representation of a prompt for logging.
// For large prompts, it returns first/last portions plus a hash of the full content.
func SanitizePrompt(prompt string, maxChars int) string {
	if len(prompt) <= maxChars {
		return prompt
	}

	// For large prompts, show first/last portions plus hash
	halfMax := maxChars / 2
	if halfMax < 100 {
		halfMax = 100
	}

	first := prompt[:halfMax]
	last := prompt[len(prompt)-halfMax:]

	// Create hash of full prompt for correlation
	hash := sha256.Sum256([]byte(prompt))
	hashStr := fmt.Sprintf("%x", hash)[:16] // First 16 chars of hash

	return fmt.Sprintf("%s...[%d chars, hash:%s]...%s",
		first, len(prompt), hashStr, last)
}

// IsServiceUnavailable checks if the error indicates persistent service unavailability.
// When this returns true, the agent should transition to SUSPEND state.
func IsServiceUnavailable(err error) bool {
	return Is(err, ErrorTypeServiceUnavailable)
}

// NewServiceUnavailableError creates a ServiceUnavailable error from a transient error
// after retries have been exhausted. This signals the agent to enter SUSPEND state.
func NewServiceUnavailableError(cause error, attempts int) *Error {
	return &Error{
		Type:    ErrorTypeServiceUnavailable,
		Err:     cause,
		Message: fmt.Sprintf("service unavailable after %d retry attempts", attempts),
	}
}
