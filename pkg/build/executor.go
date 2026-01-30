// Package build provides a service for executing build commands across different backend types.
package build

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"orchestrator/pkg/logx"
)

// contextError returns the appropriate context error (Canceled or DeadlineExceeded).
// Returns nil if the context has not been canceled.
func contextError(ctx context.Context) error {
	if ctx.Err() == context.Canceled {
		return context.Canceled
	}
	if ctx.Err() == context.DeadlineExceeded {
		return context.DeadlineExceeded
	}
	return nil
}

// ExecOpts configures command execution.
//
//nolint:govet // Field order chosen for readability over memory alignment.
type ExecOpts struct {
	// Dir is the working directory for command execution.
	// For ContainerExecutor, this is a container path (e.g., "/workspace").
	// For HostExecutor, this is a host path.
	// Required.
	Dir string

	// Env contains environment variable overrides as "KEY=VALUE" strings.
	// These are merged with the execution environment's existing variables.
	// Optional.
	Env []string

	// Stdout receives standard output. Required.
	Stdout io.Writer

	// Stderr receives standard error. Can be same as Stdout for combined output.
	// Required.
	Stderr io.Writer
}

// Executor runs commands and returns results.
type Executor interface {
	// Run executes a command with the given arguments.
	//
	// argv is the command and arguments as a string slice (NOT a shell string).
	// This prevents shell injection vulnerabilities.
	//
	// Returns the exit code and any execution error.
	// Exit code is valid even when err != nil (e.g., command ran but returned non-zero).
	//
	// Context cancellation must terminate the running command and return
	// context.Canceled or context.DeadlineExceeded as appropriate.
	Run(ctx context.Context, argv []string, opts ExecOpts) (exitCode int, err error)

	// Name returns the executor name for logging.
	Name() string
}

// ContainerExecutor runs commands inside a Docker container via docker exec.
type ContainerExecutor struct {
	logger        *logx.Logger
	ContainerName string
	DockerCmd     string // "docker" or "podman"
}

// NewContainerExecutor creates a new container executor.
func NewContainerExecutor(containerName string) *ContainerExecutor {
	// Auto-detect Docker command
	dockerCmd := "docker"
	if _, err := exec.LookPath("podman"); err == nil {
		if _, err := exec.LookPath("docker"); err != nil {
			dockerCmd = "podman"
		}
	}

	return &ContainerExecutor{
		ContainerName: containerName,
		DockerCmd:     dockerCmd,
		logger:        logx.NewLogger("container-executor"),
	}
}

// Name returns the executor name.
func (c *ContainerExecutor) Name() string {
	return "container:" + c.ContainerName
}

// killContainerProcess attempts to kill any in-container processes matching the command.
// This is a best-effort cleanup for orphaned processes after docker exec client is killed.
// Uses pkill to match processes by command name. Ignores errors since the process may
// have already exited or pkill may not be available in the container.
//
// handleCancellation handles context cancellation by killing the docker exec process
// and any orphaned in-container processes. Returns the appropriate context error.
func (c *ContainerExecutor) handleCancellation(ctx context.Context, cmd *exec.Cmd, done <-chan error, argv []string) error {
	// Kill the docker exec client process group.
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	// Wait for the docker exec client to exit after kill.
	select {
	case <-done:
		// Process exited cleanly after kill
	case <-time.After(5 * time.Second):
		// Process didn't exit in time - return anyway to avoid hanging
		c.logger.Error("Process did not exit within 5s after SIGKILL")
	}

	// Kill any orphaned in-container processes matching our command.
	if len(argv) > 0 {
		c.killContainerProcess(argv[0])
	}

	return contextError(ctx)
}

// killContainerProcess attempts to kill any in-container processes matching the command.
// This is a best-effort cleanup for orphaned processes after docker exec client is killed.
// Uses pkill to match processes by command name. Ignores errors since the process may
// have already exited or pkill may not be available in the container.
//
//nolint:contextcheck // Intentionally creates new context for cleanup after original context is canceled
func (c *ContainerExecutor) killContainerProcess(cmdName string) {
	// Create a short timeout context for the cleanup command.
	// We use Background() because the original context is already canceled.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Use pkill to kill processes matching the command name.
	// The -9 sends SIGKILL, -f matches the full command line.
	// This is best-effort - we ignore errors since the process may have already exited.
	killCmd := exec.CommandContext(ctx, c.DockerCmd, "exec", c.ContainerName,
		"pkill", "-9", "-f", cmdName)
	_ = killCmd.Run()
}

// Run executes a command inside the container.
func (c *ContainerExecutor) Run(ctx context.Context, argv []string, opts ExecOpts) (int, error) {
	if len(argv) == 0 {
		return -1, fmt.Errorf("command cannot be empty")
	}

	if opts.Stdout == nil || opts.Stderr == nil {
		return -1, fmt.Errorf("stdout and stderr writers are required")
	}

	// Build docker exec command with argv-style execution (no shell)
	execArgs := []string{"exec", "-i"}

	// Set working directory if specified
	if opts.Dir != "" {
		execArgs = append(execArgs, "--workdir", opts.Dir)
	}

	// Add environment variables
	for _, env := range opts.Env {
		execArgs = append(execArgs, "-e", env)
	}

	// Add container name and command (argv-style, no shell wrapping)
	execArgs = append(execArgs, c.ContainerName)
	execArgs = append(execArgs, argv...)

	// Log the command for debugging
	_, _ = fmt.Fprintf(opts.Stdout, "$ %s\n", strings.Join(argv, " "))

	// Execute command
	cmd := exec.CommandContext(ctx, c.DockerCmd, execArgs...)
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr

	// Set process group for proper cleanup on cancellation.
	// This allows us to kill the entire process group (docker exec + children).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	c.logger.Debug("Executing in container %s: %s %s", c.ContainerName, c.DockerCmd, strings.Join(execArgs, " "))

	// Start the command (don't block)
	if err := cmd.Start(); err != nil {
		return -1, fmt.Errorf("failed to start command in container: %w", err)
	}

	// Wait for command completion or context cancellation
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		// Context was canceled - handle cleanup and return context error.
		return -1, c.handleCancellation(ctx, cmd, done, argv)

	case err := <-done:
		// Check if context was canceled while we were waiting.
		// Context cancellation can race with command completion - prioritize
		// returning context errors to maintain the Executor contract.
		if ctxErr := contextError(ctx); ctxErr != nil {
			return -1, ctxErr
		}

		// Command completed normally (context still valid)
		exitCode := 0
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				exitCode = exitErr.ExitCode()
				return exitCode, nil // Non-zero exit is not an execution error
			}
			// Other execution error
			return -1, fmt.Errorf("failed to execute command in container: %w", err)
		}
		return exitCode, nil
	}
}

// HostExecutor runs commands directly on the host.
// Deprecated: Only for use in tests and migration. Must not be used in production.
// This executor exists to allow gradual migration from host to container execution.
type HostExecutor struct {
	logger *logx.Logger
}

// NewHostExecutor creates a new host executor.
// Deprecated: Only for use in tests and migration.
func NewHostExecutor() *HostExecutor {
	return &HostExecutor{
		logger: logx.NewLogger("host-executor"),
	}
}

// Name returns the executor name.
func (h *HostExecutor) Name() string {
	return "host"
}

// Run executes a command on the host.
// Deprecated: This runs commands directly on the host, bypassing container isolation.
func (h *HostExecutor) Run(ctx context.Context, argv []string, opts ExecOpts) (int, error) {
	if len(argv) == 0 {
		return -1, fmt.Errorf("command cannot be empty")
	}

	if opts.Stdout == nil || opts.Stderr == nil {
		return -1, fmt.Errorf("stdout and stderr writers are required")
	}

	// Log the command for debugging
	_, _ = fmt.Fprintf(opts.Stdout, "$ %s\n", strings.Join(argv, " "))

	// Execute command directly on host
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = opts.Dir
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr

	// Only set Env if we have overrides - otherwise inherit parent environment.
	// Note: Setting cmd.Env to ANY value (even empty) replaces the entire environment,
	// which would remove PATH and break command lookup.
	if len(opts.Env) > 0 {
		cmd.Env = append(os.Environ(), opts.Env...)
	}

	// Set process group for proper cleanup on cancellation
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	h.logger.Debug("Executing on host: %s", strings.Join(argv, " "))

	err := cmd.Run()

	// Extract exit code
	exitCode := 0
	if err != nil {
		// Check for context cancellation FIRST - this takes priority over exit codes.
		// When context is canceled, exec.CommandContext kills the process which
		// results in an ExitError, but we should return the context error to
		// maintain the Executor contract.
		if ctxErr := contextError(ctx); ctxErr != nil {
			return -1, ctxErr
		}

		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
			return exitCode, nil // Non-zero exit is not an execution error
		}

		// Other execution error
		return -1, fmt.Errorf("failed to execute command: %w", err)
	}

	return exitCode, nil
}

// MockExecutor is a test executor that simulates command execution without running anything.
// Use this in tests to avoid dependencies on make, containers, or other external tools.
//
//nolint:govet // Field order chosen for readability over memory alignment.
type MockExecutor struct {
	// ExitCode is the exit code to return from Run. Default is 0 (success).
	ExitCode int
	// Error is the error to return from Run. Default is nil.
	Error error
	// Output is written to stdout when Run is called.
	Output string
	// Calls records all calls to Run for assertions.
	Calls []MockExecCall
}

// MockExecCall records a single call to the mock executor.
//
//nolint:govet // Field order chosen for readability over memory alignment.
type MockExecCall struct {
	Argv []string
	Dir  string
}

// NewMockExecutor creates a new mock executor that returns success.
func NewMockExecutor() *MockExecutor {
	return &MockExecutor{
		ExitCode: 0,
		Output:   "mock execution successful\n",
	}
}

// Name returns the executor name.
func (m *MockExecutor) Name() string {
	return "mock"
}

// Run simulates command execution without actually running anything.
func (m *MockExecutor) Run(_ context.Context, argv []string, opts ExecOpts) (int, error) {
	// Record the call.
	m.Calls = append(m.Calls, MockExecCall{
		Argv: argv,
		Dir:  opts.Dir,
	})

	// Write output if configured.
	if m.Output != "" && opts.Stdout != nil {
		_, _ = fmt.Fprintf(opts.Stdout, "%s", m.Output)
	}

	return m.ExitCode, m.Error
}
