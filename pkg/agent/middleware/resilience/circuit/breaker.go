// Package circuit provides circuit breaker functionality for resilient LLM calls.
package circuit

import (
	"fmt"
	"sync"
	"time"
)

// State represents the current state of a circuit breaker.
type State int

// Circuit breaker states for managing service failure patterns.
const (
	Closed   State = iota // Normal operation
	Open                  // Failing, reject requests
	HalfOpen              // Testing if service recovered
)

func (s State) String() string {
	switch s {
	case Closed:
		return "CLOSED"
	case Open:
		return "OPEN"
	case HalfOpen:
		return "HALF_OPEN"
	default:
		return "UNKNOWN"
	}
}

// Config defines configuration for circuit breaker behavior.
type Config struct {
	FailureThreshold int           `json:"failure_threshold"` // Number of failures before opening circuit
	SuccessThreshold int           `json:"success_threshold"` // Number of successes to close circuit from half-open
	Timeout          time.Duration `json:"timeout"`           // Time to wait before trying half-open
}

// DefaultConfig provides reasonable defaults for circuit breaker behavior.
//
//nolint:gochecknoglobals // Sensible default config pattern
var DefaultConfig = Config{
	FailureThreshold: 5,
	SuccessThreshold: 3,
	Timeout:          30 * time.Second,
}

// Error represents an error when circuit is open.
type Error struct {
	State State
}

func (e *Error) Error() string {
	return fmt.Sprintf("circuit breaker is %s", e.State)
}

// Breaker defines the interface for circuit breaker implementations.
type Breaker interface {
	// Allow checks if a request should be allowed based on current state.
	Allow() bool

	// Record records the result (success/failure) of a request.
	Record(success bool)

	// GetState returns the current circuit breaker state.
	GetState() State

	// Reset manually resets the circuit breaker to closed state.
	Reset()
}

// breaker implements the Breaker interface with state management.
//
//nolint:govet // Logical field grouping preferred over memory alignment
type breaker struct {
	config          Config
	mu              sync.RWMutex
	state           State
	failureCount    int
	successCount    int
	lastFailureTime time.Time
}

// New creates a new circuit breaker with the given configuration.
func New(config Config) Breaker {
	return &breaker{
		config: config,
		state:  Closed,
	}
}

// Allow checks if a request should be allowed based on current state.
func (b *breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case Closed:
		return true

	case Open:
		// Check if timeout has passed
		if time.Since(b.lastFailureTime) >= b.config.Timeout {
			b.state = HalfOpen
			b.successCount = 0
			return true
		}
		return false

	case HalfOpen:
		// Always allow in half-open (rate limiting handles concurrency)
		return true

	default:
		return false
	}
}

// Record records the success or failure of a request.
func (b *breaker) Record(success bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if success {
		b.onSuccess()
	} else {
		b.onFailure()
	}
}

// GetState returns the current circuit breaker state.
func (b *breaker) GetState() State {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.state
}

// Reset manually resets the circuit breaker to closed state.
func (b *breaker) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.state = Closed
	b.failureCount = 0
	b.successCount = 0
}

// onSuccess handles a successful request.
func (b *breaker) onSuccess() {
	switch b.state {
	case Closed:
		// Reset failure count on success
		b.failureCount = 0

	case HalfOpen:
		b.successCount++
		if b.successCount >= b.config.SuccessThreshold {
			// Close the circuit
			b.state = Closed
			b.failureCount = 0
			b.successCount = 0
		}
	}
}

// onFailure handles a failed request.
func (b *breaker) onFailure() {
	b.failureCount++
	b.lastFailureTime = time.Now()

	switch b.state {
	case Closed:
		if b.failureCount >= b.config.FailureThreshold {
			b.state = Open
		}

	case HalfOpen:
		// Any failure in half-open immediately opens the circuit
		b.state = Open
		b.successCount = 0
	}
}
