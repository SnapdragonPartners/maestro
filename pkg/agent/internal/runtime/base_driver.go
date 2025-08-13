package runtime

import (
	"context"
	"fmt"

	"orchestrator/pkg/agent/internal/core"
	"orchestrator/pkg/proto"
)

// BaseDriver provides common functionality for agent drivers.

// BaseDriver provides common functionality for agent drivers.
type BaseDriver struct {
	core.StateMachine
	config *Config
}

// NewBaseDriver creates a new base driver.
func NewBaseDriver(config *Config, initialState proto.State) (*BaseDriver, error) {
	// Validate config.
	// Removed config validation - should be done at higher level

	// Initialize state machine.
	sm := core.NewBaseStateMachine(config.ID, initialState, config.Context.Store, nil)

	return &BaseDriver{
		StateMachine: sm,
		config:       config,
	}, nil
}

// Run executes the driver's main loop.
func (d *BaseDriver) Run(ctx context.Context) error {
	// Initialize if not already done.
	if err := d.Initialize(ctx); err != nil {
		return fmt.Errorf("failed to initialize driver: %w", err)
	}

	// Run the state machine loop.
	for {
		done, err := d.Step(ctx)
		if err != nil {
			return err
		}
		if done {
			break
		}
	}

	return nil
}

// Step executes a single step of the state machine.
func (d *BaseDriver) Step(ctx context.Context) (bool, error) {
	// Check context cancellation.
	select {
	case <-ctx.Done():
		return false, fmt.Errorf("driver step cancelled: %w", ctx.Err())
	default:
	}

	// Process current state.
	nextState, done, err := d.ProcessState(ctx)
	if err != nil {
		// Transition to error state.
		if transErr := d.TransitionTo(ctx, nextState, map[string]any{
			"error":        err.Error(),
			"failed_state": d.GetCurrentState().String(),
		}); transErr != nil {
			// Log transition error but return original error.
			d.config.Context.Logger.Printf("Failed to transition to error state: %v", transErr)
		}
		return false, fmt.Errorf("state machine processing failed: %w", err)
	}

	// If we're done, no need to transition.
	if done {
		return true, nil
	}

	// Transition to next state.
	if err := d.TransitionTo(ctx, nextState, nil); err != nil {
		return false, fmt.Errorf("failed to transition to state %s: %w", nextState, err)
	}

	// Truncate state transition history if needed (simple memory management).
	if err := d.TruncateTransitionHistoryIfNeeded(); err != nil {
		// Log warning but don't fail.
		d.config.Context.Logger.Printf("Warning: state transition history truncation failed: %v", err)
	}

	return false, nil
}

// Shutdown performs cleanup when the driver is stopping.
func (d *BaseDriver) Shutdown(_ context.Context) error {
	// Persist final state.
	if err := d.Persist(); err != nil {
		return fmt.Errorf("failed to persist state during shutdown: %w", err)
	}
	return nil
}
