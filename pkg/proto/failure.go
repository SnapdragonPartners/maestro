// Package proto - failure taxonomy for typed agent failure classification.
//
// This file defines the failure kinds and structured failure info that flow through
// the system from coder → state machine → supervisor → requeue → architect.
//
// See docs/FAILURE_TAXONOMY_SPEC.md for full design rationale.
package proto

// FailureKind classifies the cause of an agent failure.
// Used to drive different recovery paths in the supervisor and architect.
type FailureKind string

const (
	// FailureKindTransient represents temporary external service unavailability
	// (API rate limits, network timeouts). Recovery: SUSPEND state, auto-resume.
	// Already implemented via SUSPEND + pollAPIHealth — listed for taxonomy completeness.
	FailureKindTransient FailureKind = "transient"

	// FailureKindStoryInvalid means the story requirements are unclear, contradictory,
	// or impossible to implement. Recovery: coder → ERROR, architect must rewrite story.
	// Requires LLM agency — the coder must explain why the story is invalid.
	FailureKindStoryInvalid FailureKind = "story_invalid"

	// FailureKindExternal means an infrastructure/environment issue outside the agent's
	// control prevents progress (git corruption, container filesystem, Docker issues,
	// missing build dependencies). Recovery: coder → ERROR, architect inspects and
	// either rewrites story or escalates.
	//
	// This is a v1 umbrella covering multiple sub-causes. May be split into
	// environment/dependency/workspace in a future iteration if routing needs diverge.
	FailureKindExternal FailureKind = "external"
)

// KeyFailureInfo is the metadata key used to pass FailureInfo through
// StateChangeNotification metadata and state data.
const KeyFailureInfo = "failure_info"

// FailureInfo carries structured failure context through the system.
// Stored as a value type (not pointer) in metadata maps to survive transport.
type FailureInfo struct {
	Kind        FailureKind `json:"kind"`                // Classification of the failure
	Explanation string      `json:"explanation"`         // Human-readable reason
	FailedState string      `json:"failed_state"`        // State where failure occurred (e.g., "CODING")
	ToolName    string      `json:"tool_name,omitempty"` // Tool that triggered failure (auto-detected path only)
}

// NewFailureInfo creates a FailureInfo with the given parameters.
func NewFailureInfo(kind FailureKind, explanation, failedState, toolName string) FailureInfo {
	return FailureInfo{
		Kind:        kind,
		Explanation: explanation,
		FailedState: failedState,
		ToolName:    toolName,
	}
}
