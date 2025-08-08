// Package metrics provides metrics recording for LLM client operations.
package metrics

import (
	"time"
)

// Recorder defines the interface for recording LLM operation metrics.
type Recorder interface {
	// ObserveRequest records metrics for a completed LLM request.
	ObserveRequest(
		model, operation, agentType string,
		promptTokens, completionTokens int,
		success bool,
		errorType string,
		duration time.Duration,
	)

	// IncThrottle increments the throttle counter for rate limiting events.
	IncThrottle(model, reason string)

	// ObserveQueueWait records time spent waiting for rate limit availability.
	ObserveQueueWait(model string, duration time.Duration)
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
	_ bool,
	_ string,
	_ time.Duration,
) {
	// No-op
}

// IncThrottle does nothing in the no-op recorder.
func (n *NoopRecorder) IncThrottle(_, _ string) {
	// No-op
}

// ObserveQueueWait does nothing in the no-op recorder.
func (n *NoopRecorder) ObserveQueueWait(_ string, _ time.Duration) {
	// No-op
}
