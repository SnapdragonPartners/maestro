package exec

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// LocalExec executes commands directly on the local system without sandboxing
type LocalExec struct{}

// NewLocalExec creates a new LocalExec executor
func NewLocalExec() *LocalExec {
	return &LocalExec{}
}

// Name returns the executor type name
func (e *LocalExec) Name() string {
	return "local"
}

// Available returns true since local execution is always available
func (e *LocalExec) Available() bool {
	return true
}

// Run executes a command locally with the given options
func (e *LocalExec) Run(ctx context.Context, cmd []string, opts ExecOpts) (ExecResult, error) {
	if len(cmd) == 0 {
		return ExecResult{}, fmt.Errorf("command cannot be empty")
	}

	startTime := time.Now()

	// Create context with timeout if specified
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	// Create the command
	execCmd := exec.CommandContext(ctx, cmd[0], cmd[1:]...)

	// Set working directory if specified
	if opts.WorkDir != "" {
		// Validate that the directory exists
		if _, err := os.Stat(opts.WorkDir); os.IsNotExist(err) {
			return ExecResult{}, fmt.Errorf("working directory does not exist: %s", opts.WorkDir)
		}
		execCmd.Dir = opts.WorkDir
	}

	// Set environment variables
	if len(opts.Env) > 0 {
		// Start with current environment
		execCmd.Env = os.Environ()
		// Add/override with provided environment
		execCmd.Env = append(execCmd.Env, opts.Env...)
	}

	// Execute the command and capture output
	stdout, stderr, exitCode, err := e.executeCommand(execCmd)

	duration := time.Since(startTime)

	result := ExecResult{
		ExitCode:     exitCode,
		Stdout:       stdout,
		Stderr:       stderr,
		Duration:     duration,
		ExecutorUsed: e.Name(),
	}

	// Return the result even if the command failed (non-zero exit code)
	// The caller can check ExitCode to determine success/failure
	return result, err
}

// executeCommand runs the command and captures output
func (e *LocalExec) executeCommand(cmd *exec.Cmd) (stdout, stderr string, exitCode int, err error) {
	// Capture stdout and stderr
	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	// Execute the command
	err = cmd.Run()

	stdout = stdoutBuf.String()
	stderr = stderrBuf.String()
	exitCode = 0

	if err != nil {
		// Extract exit code from error
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
			// Don't return error for non-zero exit codes - caller should check ExitCode
			err = nil
		} else {
			// Command failed to start or other error
			exitCode = -1
		}
	}

	return stdout, stderr, exitCode, err
}
