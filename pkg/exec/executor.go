// Package exec provides command execution abstractions with support for local and Docker-based execution.
// It includes resource management, container registry, and execution patterns for agent operations.
package exec

import (
	"context"
	"fmt"
	"os"
	"time"
)

// ExecutorType represents the type of executor.
type ExecutorType string

// Executor type constants.
const (
	ExecutorTypeLocal  ExecutorType = "local"
	ExecutorTypeDocker ExecutorType = "docker"
)

// Executor defines the interface for executing commands in different environments.
type Executor interface {
	// Run executes a command with the given options and returns the result.
	Run(ctx context.Context, cmd []string, opts *Opts) (Result, error)

	// Name returns the executor type name for logging/debugging.
	Name() ExecutorType

	// Available returns true if this executor can be used in the current environment.
	Available() bool
}

// StreamingExecutor extends Executor with line-by-line output streaming.
// This is used by Claude Code integration to enable real-time activity
// tracking and inactivity detection during long-running subprocess execution.
type StreamingExecutor interface {
	Executor

	// RunStreaming executes a command and streams output line-by-line via callbacks.
	// onStdout is called for each line of stdout, onStderr for each line of stderr.
	// Both callbacks may be nil (falls back to buffered capture).
	// The Result still contains the full accumulated stdout/stderr.
	RunStreaming(ctx context.Context, cmd []string, opts *Opts, onStdout, onStderr func(line string)) (Result, error)
}

// Mount represents a volume mount for container execution.
type Mount struct {
	// Source is the host path to mount.
	Source string

	// Destination is the path inside the container.
	Destination string

	// ReadOnly indicates if the mount should be read-only.
	ReadOnly bool
}

// Opts contains options for command execution.
//
//nolint:govet // Configuration struct, logical grouping preferred
type Opts struct {
	// Env contains environment variables (KEY=VALUE format)
	Env []string

	// ResourceLimits contains resource constraints.
	ResourceLimits *ResourceLimits

	// Timeout is the maximum duration for command execution.
	Timeout time.Duration

	// WorkDir is the working directory for the command.
	WorkDir string

	// User is the user to run the command as (for Docker/container executors)
	User string

	// ReadOnly indicates if the filesystem should be read-only (except WorkDir)
	ReadOnly bool

	// NetworkDisabled indicates if network access should be disabled.
	NetworkDisabled bool

	// ClaudeCodeMode indicates that this container will run Claude Code.
	// When true, a Docker named volume is mounted at /tmp/.claude to persist
	// Claude Code session state across container restarts.
	ClaudeCodeMode bool

	// ExtraMounts contains additional volume mounts for the container.
	// These are added to any default mounts configured by the executor.
	ExtraMounts []Mount
}

// ResourceLimits defines resource constraints for command execution.
type ResourceLimits struct {
	// CPUs is the number of CPU cores to allocate (e.g., "2" or "1.5")
	CPUs string

	// Memory is the memory limit (e.g., "2g", "512m")
	Memory string

	// PIDs is the maximum number of processes/threads.
	PIDs int64
}

// Result contains the result of command execution.
type Result struct {
	// Stdout contains the standard output.
	Stdout string

	// Stderr contains the standard error output.
	Stderr string

	// ExecutorUsed indicates which executor was used (for debugging)
	ExecutorUsed string

	// Duration is how long the command took to execute.
	Duration time.Duration

	// ExitCode is the exit code of the command.
	ExitCode int
}

// WriteEnvFile writes KEY=VALUE pairs to a temporary file suitable for docker --env-file.
// The file is created with 0600 permissions. The caller is responsible for removing the file
// after the docker command has started (Docker reads it at container creation time only).
// Returns the file path, or empty string if envVars is empty.
func WriteEnvFile(envVars []string) (string, error) {
	if len(envVars) == 0 {
		return "", nil
	}

	f, err := os.CreateTemp("", "maestro-env-*")
	if err != nil {
		return "", fmt.Errorf("failed to create env file: %w", err)
	}

	for _, env := range envVars {
		if _, wErr := fmt.Fprintln(f, env); wErr != nil {
			_ = f.Close()
			_ = os.Remove(f.Name())
			return "", fmt.Errorf("failed to write env file: %w", wErr)
		}
	}

	if cErr := f.Close(); cErr != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("failed to close env file: %w", cErr)
	}

	if chErr := os.Chmod(f.Name(), 0600); chErr != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("failed to chmod env file: %w", chErr)
	}

	return f.Name(), nil
}

// DefaultExecOpts returns default execution options.
func DefaultExecOpts() Opts {
	return Opts{
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
