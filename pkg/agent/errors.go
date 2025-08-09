package agent

import (
	"orchestrator/pkg/agent/config"
	"orchestrator/pkg/agent/internal/core"
	"orchestrator/pkg/agent/internal/runtime"
)

// Re-exported public errors from domain packages.
var (
	// ErrStateNotFound indicates the requested state data was not found.
	ErrStateNotFound = core.ErrStateNotFound

	// ErrInvalidTransition indicates an invalid state transition was attempted.
	ErrInvalidTransition = core.ErrInvalidTransition

	// ErrInvalidState indicates an invalid state was provided.
	ErrInvalidState = core.ErrInvalidState

	// ErrMaxRetriesExceeded indicates the maximum number of retries has been exceeded.
	ErrMaxRetriesExceeded = runtime.ErrMaxRetriesExceeded

	// ErrInvalidConfig indicates an invalid configuration was provided.
	ErrInvalidConfig = config.ErrInvalidConfig
)
