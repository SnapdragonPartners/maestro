package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
)

// CircuitState represents the state of a circuit breaker.
type CircuitState int

// Circuit breaker states for managing service failure patterns.
const (
	CircuitClosed   CircuitState = iota // Normal operation
	CircuitOpen                         // Failing, reject requests
	CircuitHalfOpen                     // Testing if service recovered
)

func (s CircuitState) String() string {
	switch s {
	case CircuitClosed:
		return "CLOSED"
	case CircuitOpen:
		return "OPEN"
	case CircuitHalfOpen:
		return "HALF_OPEN"
	default:
		return "UNKNOWN"
	}
}

// CircuitBreakerConfig defines configuration for circuit breaker behavior.
type CircuitBreakerConfig struct {
	FailureThreshold   int           // Number of failures before opening circuit
	SuccessThreshold   int           // Number of successes to close circuit from half-open
	Timeout            time.Duration // Time to wait before trying half-open
	MaxConcurrentCalls int           // Maximum concurrent calls in half-open state
}

// DefaultCircuitBreakerConfig provides reasonable defaults.
var DefaultCircuitBreakerConfig = CircuitBreakerConfig{ //nolint:gochecknoglobals
	FailureThreshold:   5,
	SuccessThreshold:   3,
	Timeout:            30 * time.Second,
	MaxConcurrentCalls: 3,
}

// CircuitBreakerError represents an error when circuit is open.
type CircuitBreakerError struct {
	State CircuitState
}

func (e *CircuitBreakerError) Error() string {
	return fmt.Sprintf("circuit breaker is %s", e.State)
}

// CircuitBreakerClient wraps an LLMClient with circuit breaker pattern.
type CircuitBreakerClient struct {
	client          LLMClient
	lastFailureTime time.Time
	config          CircuitBreakerConfig
	mu              sync.RWMutex
	state           CircuitState
	failureCount    int
	successCount    int
	halfOpenCalls   int
}

// NewCircuitBreakerClient creates a new circuit breaker LLM client.
func NewCircuitBreakerClient(client LLMClient, config CircuitBreakerConfig) *CircuitBreakerClient {
	return &CircuitBreakerClient{
		client: client,
		config: config,
		state:  CircuitClosed,
	}
}

// Complete implements LLMClient with circuit breaker logic.
func (cb *CircuitBreakerClient) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	// Check if we can proceed.
	if err := cb.allowRequest(); err != nil {
		return CompletionResponse{}, err
	}

	// Execute the request.
	resp, err := cb.client.Complete(ctx, req)

	// Log the actual underlying error before recording the result
	if err != nil {
		_ = logx.Errorf("🔧 Circuit breaker recording FAILURE - underlying error: %T: %v", err, err)
	} else {
		logx.Infof("🔧 Circuit breaker recording SUCCESS")
	}

	// Record the result.
	cb.recordResult(err == nil)

	if err != nil {
		return resp, fmt.Errorf("LLM complete request failed: %w", err)
	}
	return resp, nil
}

// Stream implements LLMClient with circuit breaker logic.
func (cb *CircuitBreakerClient) Stream(ctx context.Context, req CompletionRequest) (<-chan StreamChunk, error) {
	// Check if we can proceed.
	if err := cb.allowRequest(); err != nil {
		return nil, err
	}

	// Execute the request.
	ch, err := cb.client.Stream(ctx, req)

	// Log the actual underlying error before recording the result
	if err != nil {
		_ = logx.Errorf("🔧 Circuit breaker recording FAILURE (stream) - underlying error: %T: %v", err, err)
	} else {
		logx.Infof("🔧 Circuit breaker recording SUCCESS (stream)")
	}

	// For streaming, we consider the initial establishment as success/failure.
	// Individual chunks are not tracked for circuit breaker state.
	cb.recordResult(err == nil)

	if err != nil {
		return ch, fmt.Errorf("LLM stream request failed: %w", err)
	}
	return ch, nil
}

// GetState returns the current circuit breaker state.
func (cb *CircuitBreakerClient) GetState() CircuitState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// GetFailureCount returns the current failure count.
func (cb *CircuitBreakerClient) GetFailureCount() int {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.failureCount
}

// Reset manually resets the circuit breaker to closed state.
func (cb *CircuitBreakerClient) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.state = CircuitClosed
	cb.failureCount = 0
	cb.successCount = 0
	cb.halfOpenCalls = 0
}

// allowRequest checks if a request should be allowed based on current state.
func (cb *CircuitBreakerClient) allowRequest() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case CircuitClosed:
		return nil

	case CircuitOpen:
		// Check if timeout has passed.
		if time.Since(cb.lastFailureTime) >= cb.config.Timeout {
			cb.state = CircuitHalfOpen
			cb.halfOpenCalls = 0
			cb.successCount = 0
			return nil
		}
		return &CircuitBreakerError{State: CircuitOpen}

	case CircuitHalfOpen:
		// Allow limited concurrent calls.
		if cb.halfOpenCalls >= cb.config.MaxConcurrentCalls {
			return &CircuitBreakerError{State: CircuitHalfOpen}
		}
		cb.halfOpenCalls++
		return nil

	default:
		return &CircuitBreakerError{State: cb.state}
	}
}

// recordResult records the success or failure of a request.
func (cb *CircuitBreakerClient) recordResult(success bool) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.state == CircuitHalfOpen {
		cb.halfOpenCalls--
	}

	if success {
		cb.onSuccess()
	} else {
		cb.onFailure()
	}
}

// onSuccess handles a successful request.
func (cb *CircuitBreakerClient) onSuccess() {
	switch cb.state {
	case CircuitClosed:
		// Reset failure count on success.
		cb.failureCount = 0

	case CircuitHalfOpen:
		cb.successCount++
		if cb.successCount >= cb.config.SuccessThreshold {
			// Close the circuit.
			cb.state = CircuitClosed
			cb.failureCount = 0
			cb.successCount = 0
		}
	}
}

// onFailure handles a failed request.
func (cb *CircuitBreakerClient) onFailure() {
	cb.failureCount++
	cb.lastFailureTime = time.Now()

	// Log circuit breaker state changes for debugging
	logx.Infof("🔧 Circuit breaker failure recorded: count=%d, threshold=%d, state=%s",
		cb.failureCount, cb.config.FailureThreshold, cb.state.String())

	switch cb.state {
	case CircuitClosed:
		if cb.failureCount >= cb.config.FailureThreshold {
			cb.state = CircuitOpen
			_ = logx.Errorf("⚡ Circuit breaker OPENED after %d failures (threshold: %d)",
				cb.failureCount, cb.config.FailureThreshold)
		}

	case CircuitHalfOpen:
		// Any failure in half-open immediately opens the circuit.
		cb.state = CircuitOpen
		cb.successCount = 0
		_ = logx.Errorf("⚡ Circuit breaker OPENED immediately from HALF_OPEN state")
	}
}

// CircuitBreakerStats provides statistics about circuit breaker state.
type CircuitBreakerStats struct {
	LastFailure  *time.Time   `json:"last_failure,omitempty"`
	OpenSince    *time.Time   `json:"open_since,omitempty"`
	State        CircuitState `json:"state"`
	FailureCount int          `json:"failure_count"`
	SuccessCount int          `json:"success_count"`
}

// GetStats returns current statistics.
func (cb *CircuitBreakerClient) GetStats() CircuitBreakerStats {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	stats := CircuitBreakerStats{
		State:        cb.state,
		FailureCount: cb.failureCount,
		SuccessCount: cb.successCount,
	}

	if !cb.lastFailureTime.IsZero() {
		stats.LastFailure = &cb.lastFailureTime
	}

	if cb.state == CircuitOpen {
		stats.OpenSince = &cb.lastFailureTime
	}

	return stats
}

// GetDefaultConfig delegates to the underlying client.
func (cb *CircuitBreakerClient) GetDefaultConfig() config.Model {
	return cb.client.GetDefaultConfig()
}

// ResilientClient is a combined client that includes both retry logic and circuit breaker.
type ResilientClient struct {
	// No fields needed - this is a factory type.
}

// NewResilientClient creates a client with both retry and circuit breaker patterns.
func NewResilientClient(baseClient LLMClient) LLMClient {
	return NewResilientClientWithLogger(baseClient, nil)
}

// NewResilientClientWithLogger creates a client with retry, circuit breaker, and prompt logging.
func NewResilientClientWithLogger(baseClient LLMClient, logger *logx.Logger) LLMClient {
	// First wrap with circuit breaker (inner layer)
	cbClient := NewCircuitBreakerClient(baseClient, DefaultCircuitBreakerConfig)

	// Create prompt logger if logger is provided
	var promptLogger *PromptLogger
	if logger != nil {
		promptLogger = NewPromptLogger(DefaultPromptLogConfig, logger)
	}

	// Then wrap with retry logic (outer layer)
	// This way, circuit breaker failures won't be retried.
	retryConfig := DefaultRetryConfig
	retryConfig.MaxRetries = 2 // Reduce retries when using circuit breaker

	return NewRetryableClientWithLogger(cbClient, retryConfig, promptLogger)
}
