package claude

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orchestrator/pkg/exec"
	"orchestrator/pkg/logx"
)

// Runner executes Claude Code and manages the lifecycle of a session.
type Runner struct {
	executor      exec.Executor
	containerName string
	installer     *Installer
	logger        *logx.Logger
}

// NewRunner creates a new Runner for executing Claude Code.
func NewRunner(executor exec.Executor, containerName string, logger *logx.Logger) *Runner {
	if logger == nil {
		logger = logx.NewLogger("claude-runner")
	}
	return &Runner{
		executor:      executor,
		containerName: containerName,
		installer:     NewInstaller(executor, containerName, logger),
		logger:        logger,
	}
}

// Run executes Claude Code with the given options and returns the result.
// This is the main entry point for running Claude Code sessions.
func (r *Runner) Run(ctx context.Context, opts *RunOptions) (Result, error) {
	startTime := time.Now()

	// Ensure Claude Code is installed
	if err := r.installer.EnsureClaudeCode(ctx); err != nil {
		return Result{
			Signal:   SignalError,
			Error:    err,
			Duration: time.Since(startTime),
		}, err
	}

	// Build the command
	cmd := r.buildCommand(opts)

	// Build execution options
	execOpts := r.buildExecOpts(opts)

	r.logger.Info("Starting Claude Code: mode=%s model=%s timeout=%s inactivity=%s",
		opts.Mode, opts.Model, opts.TotalTimeout, opts.InactivityTimeout)

	// Execute Claude Code
	execResult, err := r.executor.Run(ctx, cmd, execOpts)
	duration := time.Since(startTime)

	if err != nil {
		// Check if it was a timeout
		if ctx.Err() == context.DeadlineExceeded {
			return Result{
				Signal:   SignalTimeout,
				Error:    err,
				Duration: duration,
			}, nil
		}
		return Result{
			Signal:   SignalError,
			Error:    fmt.Errorf("claude code execution failed: %w", err),
			Duration: duration,
		}, fmt.Errorf("claude code execution failed: %w", err)
	}

	// Parse the output
	result := r.parseOutput(execResult.Stdout, execResult.Stderr)
	result.Duration = duration

	r.logger.Info("Claude Code completed: signal=%s responses=%d duration=%s",
		result.Signal, result.ResponseCount, duration)

	return result, nil
}

// buildCommand constructs the Claude Code command line.
func (r *Runner) buildCommand(opts *RunOptions) []string {
	cmd := []string{
		"claude",
		"--print",
		"--output-format", "stream-json",
		"--verbose",
	}

	// Add model if specified
	if opts.Model != "" {
		cmd = append(cmd, "--model", opts.Model)
	}

	// Add system prompt if specified
	if opts.SystemPrompt != "" {
		cmd = append(cmd, "--append-system-prompt", opts.SystemPrompt)
	}

	// Add the prompt/input
	cmd = append(cmd, "--prompt", opts.InitialInput)

	return cmd
}

// buildExecOpts creates execution options for the executor.
func (r *Runner) buildExecOpts(opts *RunOptions) *exec.Opts {
	execOpts := exec.DefaultExecOpts()

	// Set timeout
	if opts.TotalTimeout > 0 {
		execOpts.Timeout = opts.TotalTimeout
	}

	// Set working directory
	if opts.WorkDir != "" {
		execOpts.WorkDir = opts.WorkDir
	}

	// Build environment variables
	env := make([]string, 0, len(opts.EnvVars))
	for k, v := range opts.EnvVars {
		env = append(env, k+"="+v)
	}
	execOpts.Env = env

	return &execOpts
}

// parseOutput parses Claude Code's stdout and extracts the result.
func (r *Runner) parseOutput(stdout, stderr string) Result {
	var events []StreamEvent

	// Create parser that collects events
	parser := NewStreamParser(func(event StreamEvent) {
		events = append(events, event)
	}, func(err error) {
		r.logger.Debug("Stream parse error: %v", err)
	})

	// Parse stdout line by line
	for _, line := range strings.Split(stdout, "\n") {
		parser.ParseLine(line)
	}

	// Use signal detector to find maestro tool calls
	detector := NewSignalDetector()
	detector.AddEvents(events)

	signal, input := detector.DetectSignal()
	if signal == "" {
		// No signal detected - check if there was an error in the stream
		if hasErr, errMsg := HasError(events); hasErr {
			return Result{
				Signal:        SignalError,
				Error:         &streamError{message: errMsg},
				ResponseCount: CountResponses(events),
			}
		}

		// No signal and no error - unexpected completion
		r.logger.Warn("Claude Code completed without signal: lines=%d stderr=%s",
			parser.LineCount(), stderr)

		return Result{
			Signal:        SignalError,
			Error:         &streamError{message: "Claude Code completed without calling a maestro signal tool"},
			ResponseCount: CountResponses(events),
		}
	}

	// Build result from detected signal
	return BuildResult(signal, input, events)
}

// RunWithInactivityTimeout executes Claude Code with inactivity detection.
// This wraps Run() with additional monitoring for stalled sessions.
func (r *Runner) RunWithInactivityTimeout(ctx context.Context, opts *RunOptions) (Result, error) {
	// Create a timeout manager
	tm := NewTimeoutManager(opts.TotalTimeout, opts.InactivityTimeout)

	// Create a context with the total timeout
	ctx, cancel := context.WithTimeout(ctx, opts.TotalTimeout)
	defer cancel()

	// Start the timeout manager
	tm.Start()
	defer tm.Stop()

	// Run Claude Code
	result, err := r.Run(ctx, opts)

	// Check if we hit inactivity timeout
	if tm.IsInactivityExpired() {
		result.Signal = SignalInactivity
		if result.Error == nil {
			result.Error = &streamError{message: "Claude Code session stalled - no output for " + opts.InactivityTimeout.String()}
		}
	}

	return result, err
}

// GetInstaller returns the installer for manual installation operations.
func (r *Runner) GetInstaller() *Installer {
	return r.installer
}
