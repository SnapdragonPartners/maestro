package effect

import (
	"context"

	"orchestrator/pkg/proto"
)

// CompletionEffect represents an immediate task completion effect.
// Unlike other effects that involve network communication, this effect
// executes immediately and signals that the task is complete.
type CompletionEffect struct {
	Metadata    map[string]any // Optional completion metadata
	Message     string         // Optional completion message
	TargetState proto.State    // State to transition to (e.g., StateTesting)
}

// Execute immediately processes the completion signal.
// This effect doesn't involve network communication - it just signals completion.
func (e *CompletionEffect) Execute(_ context.Context, runtime Runtime) (any, error) {
	runtime.Info("✅ Task completion signaled: %s", e.Message)

	if e.Metadata != nil {
		runtime.Debug("📊 Completion metadata: %+v", e.Metadata)
	}

	result := &CompletionResult{
		Metadata:    e.Metadata,
		Message:     e.Message,
		TargetState: e.TargetState,
	}

	return result, nil
}

// Type returns the effect type identifier.
func (e *CompletionEffect) Type() string {
	return "completion"
}

// CompletionResult represents the result of a completion effect.
type CompletionResult struct {
	Metadata    map[string]any `json:"metadata,omitempty"`
	Message     string         `json:"message"`
	TargetState proto.State    `json:"target_state"`
}

// NewCompletionEffect creates an effect for immediate task completion.
func NewCompletionEffect(message string, targetState proto.State) *CompletionEffect {
	return &CompletionEffect{
		Metadata:    make(map[string]any),
		Message:     message,
		TargetState: targetState,
	}
}

// NewCompletionEffectWithMetadata creates an effect with completion metadata.
func NewCompletionEffectWithMetadata(message string, targetState proto.State, metadata map[string]any) *CompletionEffect {
	return &CompletionEffect{
		Metadata:    metadata,
		Message:     message,
		TargetState: targetState,
	}
}
