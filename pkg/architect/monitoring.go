package architect

import (
	"context"
	"fmt"
	"time"

	"orchestrator/pkg/chat"
	"orchestrator/pkg/proto"
)

// handleMonitoring processes the monitoring phase (waiting for coder requests).
func (d *Driver) handleMonitoring(ctx context.Context) (proto.State, error) {
	// State: waiting for coder requests and review completions

	// Track when monitoring entered idle state (for system_idle debounce)
	if d.monitoringIdleSince.IsZero() {
		d.monitoringIdleSince = time.Now()
	}

	// Check if all stories are completed.
	if d.queue.AllStoriesCompleted() {
		d.logger.Info("🚀 MONITORING → DONE: All stories completed successfully")

		// Send AllStoriesComplete notification to PM so it clears in_flight flag
		if err := d.notifyPMAllStoriesComplete(ctx); err != nil {
			d.logger.Warn("⚠️ Failed to notify PM of all stories complete: %v", err)
		}

		return StateDone, nil
	}

	// Check if all stories are terminal (some failed) — notify PM and finish
	if d.queue.AllStoriesTerminal() {
		d.logger.Info("🚀 MONITORING → DONE: All stories terminal (some failed)")

		if err := d.notifyPMAllStoriesTerminal(ctx); err != nil {
			d.logger.Warn("⚠️ Failed to notify PM of all stories terminal: %v", err)
		}

		return StateDone, nil
	}

	// Reconcile open incidents and check for system idle
	d.reconcileOpenIncidents(ctx)
	d.checkAndOpenIdleIncident(ctx)

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
		d.monitoringIdleSince = time.Time{} // Reset idle timer on coder activity
		// Store the question for processing in REQUEST state.
		d.SetStateData(StateKeyCurrentRequest, questionMsg)
		return StateRequest, nil

	case <-time.After(HeartbeatInterval):
		// Check for unread developer chat messages
		if d.devChatService != nil && d.devChatService.HaveNewMessagesForChannel(d.GetAgentID(), chat.ChannelDevelopment) {
			d.logger.Info("Dev-chat: new development messages detected, transitioning to REQUEST")
			d.SetStateData(StateKeyDevChatPending, true)
			return StateRequest, nil
		}
		return StateMonitoring, nil

	case <-ctx.Done():
		return StateError, fmt.Errorf("architect monitoring cancelled: %w", ctx.Err())
	}
}
