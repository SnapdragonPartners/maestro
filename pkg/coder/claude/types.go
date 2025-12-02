// Package claude provides Claude Code integration for the coder agent.
// It enables running Claude Code as a subprocess for PLANNING and CODING states,
// leveraging Anthropic's optimized toolsets and prompts.
package claude

import (
	"time"
)

// Mode represents the Claude Code execution mode.
type Mode string

const (
	// ModePlanning is used during the PLANNING state (read-only workspace).
	ModePlanning Mode = "PLANNING"
	// ModeCoding is used during the CODING state (read-write workspace).
	ModeCoding Mode = "CODING"
)

// Signal represents a completion signal from Claude Code.
type Signal string

const (
	// SignalPlanComplete indicates Claude Code has submitted a plan.
	SignalPlanComplete Signal = "PLAN_COMPLETE"
	// SignalDone indicates Claude Code has completed implementation.
	SignalDone Signal = "DONE"
	// SignalQuestion indicates Claude Code needs to ask the architect a question.
	SignalQuestion Signal = "QUESTION"
	// SignalStoryComplete indicates the story was already implemented.
	SignalStoryComplete Signal = "STORY_COMPLETE"
	// SignalError indicates an error occurred.
	SignalError Signal = "ERROR"
	// SignalTimeout indicates execution timed out.
	SignalTimeout Signal = "TIMEOUT"
	// SignalInactivity indicates no output for too long.
	SignalInactivity Signal = "INACTIVITY"
)

// RunOptions contains options for running Claude Code.
type RunOptions struct {
	// Mode is the execution mode (PLANNING or CODING).
	Mode Mode

	// WorkDir is the container workspace path.
	WorkDir string

	// Model is the Anthropic model to use (from coder_model config).
	Model string

	// SystemPrompt is the prompt appended to Claude Code's defaults.
	SystemPrompt string

	// InitialInput is the story content (planning) or approved plan (coding).
	InitialInput string

	// EnvVars contains environment variables (ANTHROPIC_API_KEY, etc.).
	EnvVars map[string]string

	// TotalTimeout is the maximum time for the entire run (default: 5m).
	TotalTimeout time.Duration

	// InactivityTimeout is the maximum time without output (default: 1m).
	InactivityTimeout time.Duration

	// ContainerName is the name of the container to execute in.
	ContainerName string
}

// DefaultRunOptions returns RunOptions with default timeout values.
func DefaultRunOptions() RunOptions {
	return RunOptions{
		TotalTimeout:      5 * time.Minute,
		InactivityTimeout: 1 * time.Minute,
	}
}

// Result contains the result of a Claude Code execution.
type Result struct {
	// Signal is the completion signal detected.
	Signal Signal

	// Plan is the submitted plan (for SignalPlanComplete).
	Plan string

	// Summary is the completion summary (for SignalDone).
	Summary string

	// Reason is the completion reason (for SignalStoryComplete).
	Reason string

	// Question contains question details (for SignalQuestion).
	Question *Question

	// Error contains error details (for SignalError).
	Error error

	// ResponseCount is the number of assistant responses received.
	ResponseCount int

	// Duration is how long the execution took.
	Duration time.Duration
}

// Question represents a question to be asked to the architect.
type Question struct {
	// Question is the question text.
	Question string

	// Context is additional context about why the question is being asked.
	Context string
}
