package agent

import "errors"

var (
	// ErrStateNotFound indicates the requested state data was not found
	ErrStateNotFound = errors.New("state not found")

	// ErrInvalidTransition indicates an invalid state transition was attempted
	ErrInvalidTransition = errors.New("invalid state transition")

	// ErrMaxRetriesExceeded indicates the maximum number of retries has been exceeded
	ErrMaxRetriesExceeded = errors.New("maximum retries exceeded")

	// ErrInvalidState indicates an invalid state was provided
	ErrInvalidState = errors.New("invalid state")

	// ErrInvalidConfig indicates an invalid configuration was provided
	ErrInvalidConfig = errors.New("invalid configuration")
)