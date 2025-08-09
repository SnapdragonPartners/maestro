package core

import "errors"

var (
	// ErrStateNotFound indicates the requested state data was not found.
	ErrStateNotFound = errors.New("state not found")

	// ErrInvalidTransition indicates an invalid state transition was attempted.
	ErrInvalidTransition = errors.New("invalid state transition")

	// ErrInvalidState indicates an invalid state was provided.
	ErrInvalidState = errors.New("invalid state")
)
