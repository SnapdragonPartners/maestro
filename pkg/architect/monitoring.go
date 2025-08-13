package architect

import (
	"context"
	"fmt"
	"time"

	"orchestrator/pkg/proto"
)

// handleMonitoring processes the monitoring phase (waiting for coder requests).
func (d *Driver) handleMonitoring(ctx context.Context) (proto.State, error) {
	// State: waiting for coder requests and review completions

	// Check if all stories are completed.
	if d.queue.AllStoriesCompleted() {
		return StateDone, nil
	}

	// In monitoring state, we wait for either:
	// 1. Coder questions/requests (transition to REQUEST).
	// 2. Heartbeat to check for new ready stories.
	select {
	case questionMsg, ok := <-d.questionsCh:
		if !ok {
			// Channel closed by dispatcher - abnormal shutdown
			return StateError, fmt.Errorf("questions channel closed unexpectedly")
		}
		if questionMsg == nil {
			return StateMonitoring, nil
		}
		// Store the question for processing in REQUEST state.
		d.stateData["current_request"] = questionMsg
		return StateRequest, nil

	case <-time.After(HeartbeatInterval):
		// Heartbeat debug logging.
		return StateMonitoring, nil

	case <-ctx.Done():
		return StateError, fmt.Errorf("architect dispatching cancelled: %w", ctx.Err())
	}
}
