package agent

import (
	"context"
	"fmt"
	"time"
)

// TimeoutConfig defines timeouts for various operations.
type TimeoutConfig struct {
	StateTimeout    time.Duration // Timeout for individual state processing
	GlobalTimeout   time.Duration // Overall timeout for the entire run
	ShutdownTimeout time.Duration // Timeout for graceful shutdown
}

// DefaultTimeoutConfig provides reasonable default timeouts.
var DefaultTimeoutConfig = TimeoutConfig{ //nolint:gochecknoglobals
	StateTimeout:    2 * time.Minute,  // 2 minutes per state
	GlobalTimeout:   30 * time.Minute, // 30 minutes total
	ShutdownTimeout: 10 * time.Second, // 10 seconds for shutdown
}

// StepWithTimeout executes a single step with a timeout.
func (d *BaseDriver) StepWithTimeout(ctx context.Context, timeout time.Duration) (bool, error) {
	if timeout <= 0 {
		timeout = DefaultTimeoutConfig.StateTimeout
	}

	// Create a context with timeout.
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Run the step.
	done, err := d.Step(ctx)
	if err != nil {
		// Check if it was a timeout.
		if ctx.Err() == context.DeadlineExceeded {
			return false, fmt.Errorf("state processing timed out after %v", timeout)
		}
		return false, err
	}

	return done, nil
}

// RunWithTimeout executes the driver's main loop with timeouts.
func (d *BaseDriver) RunWithTimeout(ctx context.Context, cfg TimeoutConfig) error {
	if cfg.GlobalTimeout <= 0 {
		cfg.GlobalTimeout = DefaultTimeoutConfig.GlobalTimeout
	}
	if cfg.StateTimeout <= 0 {
		cfg.StateTimeout = DefaultTimeoutConfig.StateTimeout
	}

	// Create a context with global timeout.
	ctx, cancel := context.WithTimeout(ctx, cfg.GlobalTimeout)
	defer cancel()

	// Initialize if not already done.
	if err := d.Initialize(ctx); err != nil {
		return fmt.Errorf("failed to initialize driver: %w", err)
	}

	// Run the state machine loop.
	for {
		done, err := d.StepWithTimeout(ctx, cfg.StateTimeout)
		if err != nil {
			return err
		}
		if done {
			break
		}

		// Check global timeout.
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("global timeout exceeded after %v", cfg.GlobalTimeout)
		}
	}

	return nil
}
