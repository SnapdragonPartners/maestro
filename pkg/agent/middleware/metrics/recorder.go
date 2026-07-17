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
	// ObserveRequest records metrics for a completed LLM request. The
	// internal recorder aggregates by storyID/tokens/cost/success; agentID
	// and model feed the durable usage log (the P-1 benchmark usage
	// surface — see docs/v2/phase_1/design_adapter_v1.md).
	ObserveRequest(
		storyID, agentID, model string,
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
	_, _, _ string,
	_, _ int,
	_ float64,
	_ bool,
) {
	// No-op
}
