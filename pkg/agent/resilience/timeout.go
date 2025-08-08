package resilience

import "time"

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

// Note: StepWithTimeout and RunWithTimeout methods have been moved to
// the runtime package to avoid import cycles. This package now contains
// only the timeout configuration types.
