package toolloop

import "fmt"

// OutcomeKind categorizes the result of a toolloop execution.
// These are loop-level outcomes describing what happened during iteration.
type OutcomeKind int

const (
	// OutcomeSuccess indicates the loop completed successfully with a terminal signal.
	// Signal field contains the state transition signal (e.g., "PLAN_REVIEW", "TESTING").
	// Value field contains the extracted result from ExtractResult.
	OutcomeSuccess OutcomeKind = iota

	// OutcomeProcessEffect indicates a tool returned ProcessEffect to pause the loop.
	// Signal field contains the effect signal (e.g., "QUESTION", "BUDGET_REVIEW").
	// The state machine should process the async effect and then transition back to continue the loop.
	OutcomeProcessEffect

	// OutcomeNoToolTwice indicates the LLM failed to use tools twice in a row.
	// This is a loop-level guard that prevents infinite loops when LLM ignores tools.
	OutcomeNoToolTwice

	// OutcomeIterationLimit indicates Escalation.HardLimit was reached.
	// This is a normal termination condition for work that needs human intervention.
	// Iteration field contains the 1-indexed iteration count at which limit was hit.
	OutcomeIterationLimit

	// OutcomeMaxIterations indicates MaxIterations was exceeded without hitting HardLimit.
	// This occurs when no escalation config is provided or limits haven't been reached.
	OutcomeMaxIterations

	// OutcomeLLMError indicates the LLM client failed (network, API error, etc.).
	// Err field contains the underlying error from the LLM client.
	OutcomeLLMError

	// OutcomeExtractionError indicates CheckTerminal signaled completion but ExtractResult failed.
	// This happens when terminal tools were called but results couldn't be parsed.
	// Use errors.Is(out.Err, ErrNoTerminalTool) for fine-grained handling of extraction failures.
	OutcomeExtractionError
)

// String returns human-readable name for OutcomeKind.
func (k OutcomeKind) String() string {
	switch k {
	case OutcomeSuccess:
		return "Success"
	case OutcomeProcessEffect:
		return "ProcessEffect"
	case OutcomeNoToolTwice:
		return "NoToolTwice"
	case OutcomeIterationLimit:
		return "IterationLimit"
	case OutcomeMaxIterations:
		return "MaxIterations"
	case OutcomeLLMError:
		return "LLMError"
	case OutcomeExtractionError:
		return "ExtractionError"
	default:
		return fmt.Sprintf("OutcomeKind(%d)", k)
	}
}

// Outcome represents the result of a toolloop execution with typed result extraction.
// Generic over result type T for type-safe extraction of terminal tool data.
//
//nolint:govet // Field order optimized for readability over memory alignment
type Outcome[T any] struct {
	// Kind categorizes what happened during the loop (success, error, limit, etc.).
	Kind OutcomeKind

	// Signal is the terminal signal or ProcessEffect signal.
	// For OutcomeSuccess: terminal tool name (e.g., "submit_plan", "done")
	// For OutcomeProcessEffect: state to transition to (e.g., "QUESTION")
	// Empty string means normal completion with no state transition.
	Signal string

	// EffectData contains data from ProcessEffect when Kind == OutcomeProcessEffect.
	// Only valid when Kind == OutcomeProcessEffect.
	// Contains effect-specific data (e.g., question text, budget info).
	EffectData any

	// Value is the extracted result from ExtractResult.
	// Only valid when Kind == OutcomeSuccess.
	// Zero value of T for all other outcomes.
	Value T

	// Err is the underlying error (non-nil for all non-Success outcomes).
	// For OutcomeExtractionError, check with errors.Is for sentinel errors:
	//   - errors.Is(out.Err, ErrNoTerminalTool)
	//   - errors.Is(out.Err, ErrNoActivity)
	//   - errors.Is(out.Err, ErrInvalidResult)
	Err error

	// Iteration is the 1-indexed iteration count when the outcome occurred.
	// Useful for logging, metrics, and debugging.
	// Example: "reached iteration limit at iteration 16"
	Iteration int
}
