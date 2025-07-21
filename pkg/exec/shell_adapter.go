package exec

import (
	"context"
	"fmt"
	"strings"
)

// ShellCommandAdapter adapts the new Executor interface to work with the existing shell tool
type ShellCommandAdapter struct {
	executor Executor
}

// NewShellCommandAdapter creates a new adapter with the given executor
func NewShellCommandAdapter(executor Executor) *ShellCommandAdapter {
	return &ShellCommandAdapter{
		executor: executor,
	}
}

// ExecuteShellCommand executes a shell command string using the configured executor
// This maintains compatibility with the existing shell tool interface
func (a *ShellCommandAdapter) ExecuteShellCommand(ctx context.Context, cmdStr, cwd string) (any, error) {
	if cmdStr == "" {
		return nil, fmt.Errorf("command cannot be empty")
	}

	// Parse command string into []string
	// For now, use simple shell parsing - this can be improved later
	cmd := parseShellCommand(cmdStr)

	// Create execution options
	opts := DefaultExecOpts()
	opts.WorkDir = cwd

	// Execute the command
	result, err := a.executor.Run(ctx, cmd, opts)
	if err != nil {
		return nil, err
	}

	// Return result in the format expected by the shell tool
	return map[string]any{
		"stdout":    result.Stdout,
		"stderr":    result.Stderr,
		"exit_code": result.ExitCode,
		"cwd":       cwd,
	}, nil
}

// parseShellCommand parses a shell command string into []string
// This is a simple implementation - for production use, consider using a proper shell parser
func parseShellCommand(cmdStr string) []string {
	// Handle simple cases first
	if strings.HasPrefix(cmdStr, "sh -c ") || strings.HasPrefix(cmdStr, "bash -c ") {
		// For "sh -c 'command'" format, return as is
		parts := strings.SplitN(cmdStr, " ", 3)
		if len(parts) >= 3 {
			return parts
		}
	}

	// For other cases, wrap in sh -c
	return []string{"sh", "-c", cmdStr}
}

// GetExecutor returns the underlying executor (useful for testing)
func (a *ShellCommandAdapter) GetExecutor() Executor {
	return a.executor
}
