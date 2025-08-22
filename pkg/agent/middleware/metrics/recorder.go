// Package metrics provides metrics recording for LLM client operations.
package metrics

import (
	"orchestrator/pkg/proto"
)

// StateProvider provides access to agent state for metrics collection.
type StateProvider interface {
	// GetCurrentState returns the agent's current state (PLANNING, CODING, etc).
	GetCurrentState() proto.State
	// GetStoryID returns the current story ID being worked on.
	GetStoryID() string
	// GetID returns the agent ID.
	GetID() string
}

// Recorder defines the interface for recording LLM operation metrics.
type Recorder interface {
	// ObserveRequest records metrics for a completed LLM request.
	// Only storyID, tokens, cost, and success are used by internal recorder.
	ObserveRequest(
		storyID string,
		promptTokens, completionTokens int,
		cost float64,
		success bool,
	)
}

// NoopRecorder implements Recorder with no-op behavior for when metrics are disabled.
type NoopRecorder struct{}

// Nop returns a no-op metrics recorder that discards all metrics.
func Nop() Recorder {
	return &NoopRecorder{}
}

// ObserveRequest does nothing in the no-op recorder.
func (n *NoopRecorder) ObserveRequest(
	_ string,
	_, _ int,
	_ float64,
	_ bool,
) {
	// No-op
}
