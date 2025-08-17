package coder

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"orchestrator/pkg/logx"
)

// GitRunner provides an interface for running Git commands with dependency injection support.
type GitRunner interface {
	// Run executes a Git command in the specified directory.
	// Returns stdout+stderr combined output and any error.
	Run(ctx context.Context, dir string, args ...string) ([]byte, error)

	// RunQuiet executes a Git command without logging errors (for fault-tolerant operations).
	RunQuiet(ctx context.Context, dir string, args ...string) ([]byte, error)
}

// ContainerManager defines the interface for managing Docker containers.
type ContainerManager interface {
	StopContainer(ctx context.Context, containerName string) error
	Shutdown(ctx context.Context) error
}

// DefaultGitRunner implements GitRunner using the system git command.
type DefaultGitRunner struct {
	logger *logx.Logger
}

// NewDefaultGitRunner creates a new DefaultGitRunner.
func NewDefaultGitRunner() *DefaultGitRunner {
	return &DefaultGitRunner{
		logger: logx.NewLogger("git"),
	}
}

// Run executes a Git command using exec.CommandContext.
func (g *DefaultGitRunner) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return g.runGitCommand(ctx, dir, false, args...)
}

// RunQuiet executes a Git command without logging errors (for fault-tolerant operations like cleanup).
func (g *DefaultGitRunner) RunQuiet(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return g.runGitCommand(ctx, dir, true, args...)
}

// runGitCommand is the shared implementation for running git commands.
func (g *DefaultGitRunner) runGitCommand(ctx context.Context, dir string, quiet bool, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}

	// Log the command being executed.
	logDir := dir
	if logDir == "" {
		logDir = "."
	}

	cmdDesc := strings.Join(args, " ")
	if quiet {
		g.logger.Debug("Executing Git command (quiet): cd %s && git %s", logDir, cmdDesc)
	} else {
		g.logger.Debug("Executing Git command: cd %s && git %s", logDir, cmdDesc)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		if quiet {
			// For quiet operations, only log at debug level to avoid noise during cleanup.
			g.logger.Debug("Git command failed (expected): %s (exit status: %v)", cmdDesc, err)
			g.logger.Debug("Git output: %s", string(output))
		} else {
			g.logger.Error("Git command failed: %s (exit status: %v)", cmdDesc, err)
			g.logger.Error("Git output: %s", string(output))
		}
		return output, fmt.Errorf("git %s failed: %w", cmdDesc, err)
	}

	g.logger.Debug("Git command succeeded: %s", cmdDesc)
	return output, nil
}
