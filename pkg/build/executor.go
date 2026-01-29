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
		// Context was canceled - kill the process group to ensure cleanup.
		// This kills both the docker exec client AND sends SIGKILL which Docker
		// propagates to the container process when using -i (interactive) mode.
		if cmd.Process != nil {
			// Kill the entire process group (negative PID)
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}

		// Wait for the process to exit after kill, but with a timeout to prevent
		// hanging forever if docker exec wedges (per PR review feedback).
		select {
		case <-done:
			// Process exited cleanly after kill
		case <-time.After(5 * time.Second):
			// Process didn't exit in time - return anyway to avoid hanging
			c.logger.Error("Process did not exit within 5s after SIGKILL")
		}

		if ctx.Err() == context.Canceled {
			return -1, context.Canceled
		}
		return -1, context.DeadlineExceeded

	case err := <-done:
		// Command completed normally
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
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
			return exitCode, nil // Non-zero exit is not an execution error
		}

		// Check for context cancellation
		if ctx.Err() == context.Canceled {
			return -1, context.Canceled
		}
		if ctx.Err() == context.DeadlineExceeded {
			return -1, context.DeadlineExceeded
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
