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
		d.logger.Info("🏗️ All stories completed, transitioning to DONE")
		return StateDone, nil
	}

	// In monitoring state, we wait for either:
	// 1. Coder questions/requests (transition to REQUEST).
	// 2. Heartbeat to check for new ready stories.
	select {
	case questionMsg, ok := <-d.questionsCh:
		if !ok {
			// Channel closed by dispatcher - abnormal shutdown
			d.logger.Info("🏗️ Questions channel closed in MONITORING, transitioning to ERROR")
			return StateError, fmt.Errorf("questions channel closed unexpectedly")
		}
		if questionMsg == nil {
			d.logger.Warn("🏗️ Received nil question message in MONITORING")
			return StateMonitoring, nil
		}
		d.logger.Info("🏗️ Architect received question in MONITORING state, transitioning to REQUEST")
		// Store the question for processing in REQUEST state.
		d.stateData["current_request"] = questionMsg
		return StateRequest, nil

	case <-time.After(HeartbeatInterval):
		// Heartbeat debug logging.
		d.logger.Debug("🏗️ Monitoring heartbeat: checking for ready stories")
		return StateMonitoring, nil

	case <-ctx.Done():
		return StateError, fmt.Errorf("architect dispatching cancelled: %w", ctx.Err())
	}
}
