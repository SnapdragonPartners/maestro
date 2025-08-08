package resilience

import "errors"

var (
	// ErrCircuitOpen indicates the circuit breaker is open.
	ErrCircuitOpen = errors.New("circuit breaker is open")

	// ErrRetryExhausted indicates all retry attempts have been exhausted.
	ErrRetryExhausted = errors.New("retry attempts exhausted")

	// ErrTimeout indicates an operation timed out.
	ErrTimeout = errors.New("operation timed out")
)
