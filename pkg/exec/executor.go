package exec

import (
	"context"
	"time"
)

// ExecutorType represents the type of executor
type ExecutorType string

// Executor type constants
const (
	ExecutorTypeLocal  ExecutorType = "local"
	ExecutorTypeDocker ExecutorType = "docker"
)

// Executor defines the interface for executing commands in different environments
type Executor interface {
	// Run executes a command with the given options and returns the result
	Run(ctx context.Context, cmd []string, opts ExecOpts) (ExecResult, error)

	// Name returns the executor type name for logging/debugging
	Name() ExecutorType

	// Available returns true if this executor can be used in the current environment
	Available() bool
}

// ExecOpts contains options for command execution
type ExecOpts struct {
	// WorkDir is the working directory for the command
	WorkDir string

	// Env contains environment variables (KEY=VALUE format)
	Env []string

	// Timeout is the maximum duration for command execution
	Timeout time.Duration

	// User is the user to run the command as (for Docker/container executors)
	User string

	// ReadOnly indicates if the filesystem should be read-only (except WorkDir)
	ReadOnly bool

	// NetworkDisabled indicates if network access should be disabled
	NetworkDisabled bool

	// ResourceLimits contains resource constraints
	ResourceLimits *ResourceLimits
}

// ResourceLimits defines resource constraints for command execution
type ResourceLimits struct {
	// CPUs is the number of CPU cores to allocate (e.g., "2" or "1.5")
	CPUs string

	// Memory is the memory limit (e.g., "2g", "512m")
	Memory string

	// PIDs is the maximum number of processes/threads
	PIDs int64
}

// ExecResult contains the result of command execution
type ExecResult struct {
	// ExitCode is the exit code of the command
	ExitCode int

	// Stdout contains the standard output
	Stdout string

	// Stderr contains the standard error output
	Stderr string

	// Duration is how long the command took to execute
	Duration time.Duration

	// ExecutorUsed indicates which executor was used (for debugging)
	ExecutorUsed string
}

// DefaultExecOpts returns default execution options
func DefaultExecOpts() ExecOpts {
	return ExecOpts{
		Timeout:         5 * time.Minute, // Default 5 minute timeout
		ReadOnly:        false,
		NetworkDisabled: false,
		ResourceLimits: &ResourceLimits{
			CPUs:   "2",
			Memory: "2g",
			PIDs:   1024,
		},
	}
}
