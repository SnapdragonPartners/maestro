// Package agent provides timeout management for state machine operations.
package agent

import (
	"context"
	"fmt"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/proto"
)

// StateTimeoutWrapper wraps state processing with a global timeout.
type StateTimeoutWrapper struct {
	timeout time.Duration
}

// NewStateTimeoutWrapper creates a new timeout wrapper with the given timeout.
func NewStateTimeoutWrapper(timeout time.Duration) *StateTimeoutWrapper {
	return &StateTimeoutWrapper{
		timeout: timeout,
	}
}

// NewStateTimeoutWrapperFromConfig creates a timeout wrapper from the global config.
func NewStateTimeoutWrapperFromConfig() (*StateTimeoutWrapper, error) {
	cfg, err := config.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get config for timeout wrapper: %w", err)
	}

	if cfg.Agents == nil {
		return nil, fmt.Errorf("agent config not found")
	}

	return NewStateTimeoutWrapper(cfg.Agents.StateTimeout), nil
}

// ProcessWithTimeout wraps a state processing function with timeout handling.
// It returns the next state, done flag, and any error (including timeout errors).
func (w *StateTimeoutWrapper) ProcessWithTimeout(
	ctx context.Context,
	currentState proto.State,
	processor func(context.Context) (proto.State, bool, error),
) (proto.State, bool, error) {
	// Create a context with timeout
	timeoutCtx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()

	// Channel to receive the result from the processor
	type result struct {
		err       error
		nextState proto.State
		done      bool
	}

	resultCh := make(chan result, 1)

	// Run the processor in a goroutine
	go func() {
		defer func() {
			// Recover from any panics in the processor
			if r := recover(); r != nil {
				resultCh <- result{
					err:       fmt.Errorf("state processor panicked: %v", r),
					nextState: proto.StateError,
					done:      false,
				}
			}
		}()

		nextState, done, err := processor(timeoutCtx)
		resultCh <- result{
			err:       err,
			nextState: nextState,
			done:      done,
		}
	}()

	// Wait for either the result or timeout
	select {
	case res := <-resultCh:
		return res.nextState, res.done, res.err
	case <-timeoutCtx.Done():
		// Check if it was the parent context that was cancelled
		select {
		case <-ctx.Done():
			return proto.StateError, false, fmt.Errorf("state processing cancelled: %w", ctx.Err())
		default:
			return proto.StateError, false, fmt.Errorf("state %s processing timed out after %v", currentState, w.timeout)
		}
	}
}

// SetTimeout sets the global timeout.
func (w *StateTimeoutWrapper) SetTimeout(timeout time.Duration) {
	w.timeout = timeout
}

// GetTimeout returns the current timeout.
func (w *StateTimeoutWrapper) GetTimeout() time.Duration {
	return w.timeout
}

// ProcessStateWithGlobalTimeout provides a global function that any agent can use to wrap
// state processing with timeout handling, regardless of their internal architecture.
func ProcessStateWithGlobalTimeout(
	ctx context.Context,
	currentState proto.State,
	processor func(context.Context) (proto.State, bool, error),
) (proto.State, bool, error) {
	wrapper, err := NewStateTimeoutWrapperFromConfig()
	if err != nil {
		// If config is not available, use default timeout of 10 minutes
		wrapper = NewStateTimeoutWrapper(10 * time.Minute)
	}

	return wrapper.ProcessWithTimeout(ctx, currentState, processor)
}

// ProcessStateWithGlobalTimeoutSimple provides timeout wrapping for state processors that
// return (proto.State, error) instead of (proto.State, bool, error).
func ProcessStateWithGlobalTimeoutSimple(
	ctx context.Context,
	currentState proto.State,
	processor func(context.Context) (proto.State, error),
) (proto.State, error) {
	wrapper, err := NewStateTimeoutWrapperFromConfig()
	if err != nil {
		// If config is not available, use default timeout of 10 minutes
		wrapper = NewStateTimeoutWrapper(10 * time.Minute)
	}

	// Wrap the simple processor to match the expected signature
	nextState, _, wrapErr := wrapper.ProcessWithTimeout(ctx, currentState, func(ctx context.Context) (proto.State, bool, error) {
		state, err := processor(ctx)
		// We don't use the 'done' flag for simple processors
		return state, false, err
	})

	return nextState, wrapErr
}
