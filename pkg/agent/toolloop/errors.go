package toolloop

import "errors"

// Extractor sentinel errors define a shared vocabulary for extraction failures.
// These are returned by ExtractResult functions when terminal conditions aren't met.

var (
	// ErrNoTerminalTool indicates that required terminal tools weren't called.
	// Example: Planning loop expects "plan_complete" tool but LLM didn't call it.
	ErrNoTerminalTool = errors.New("no terminal tool was called")

	// ErrNoActivity indicates the LLM did nothing meaningful (no tools, no changes).
	// This is distinct from OutcomeNoToolTwice which is a loop-level guard.
	// ErrNoActivity is an extractor-level semantic failure.
	ErrNoActivity = errors.New("no tool calls or changes were made")

	// ErrInvalidResult indicates a terminal tool was called but the payload is malformed.
	// Example: "done" tool called but "summary" field is missing or empty.
	ErrInvalidResult = errors.New("invalid tool result payload")

	// ErrGracefulShutdown indicates the toolloop was interrupted by context cancellation.
	// This is a normal termination condition for graceful shutdown, not an error.
	// Callers should serialize their state and exit cleanly when receiving this.
	ErrGracefulShutdown = errors.New("graceful shutdown requested")
)
