package architect

import (
	"context"
	"fmt"
	"time"

	"orchestrator/pkg/proto"
)

// handleEscalated processes the escalated phase (waiting for human intervention).
func (d *Driver) handleEscalated(ctx context.Context) (proto.State, error) {
	// State: waiting for human intervention

	// Check escalation timeout (2 hours).
	if escalatedAt, exists := d.stateData["escalated_at"].(time.Time); exists {
		timeSinceEscalation := time.Since(escalatedAt)
		if timeSinceEscalation > EscalationTimeout {
			d.logger.Warn("escalation timeout exceeded (%v > %v), sending ABANDON review and re-queuing",
				timeSinceEscalation.Truncate(time.Minute), EscalationTimeout)

			// Log timeout event for monitoring.
			if d.escalationHandler != nil {
				if logErr := d.escalationHandler.LogTimeout(escalatedAt, timeSinceEscalation); logErr != nil {
					d.logger.Error("Failed to log timeout event: %v", logErr)
				}
			}

			// Send ABANDON review and re-queue story.
			if err := d.sendAbandonAndRequeue(ctx); err != nil {
				d.logger.Error("failed to send ABANDON review and re-queue: %v", err)
				return StateError, fmt.Errorf("failed to handle escalation timeout: %w", err)
			}

			return StateDispatching, nil
		}

		// Log remaining time periodically (every hour in actual usage, but for demo we'll be more verbose).
		timeRemaining := EscalationTimeout - timeSinceEscalation
		d.logger.Debug("escalation timeout: %v remaining (escalated %v ago)",
			timeRemaining.Truncate(time.Minute), timeSinceEscalation.Truncate(time.Minute))
	} else {
		// If we don't have an escalation timestamp, this is an error - we should always record when we escalate.
		d.logger.Warn("in ESCALATED state but no escalation timestamp found")
		return StateError, fmt.Errorf("invalid escalated state: no escalation timestamp")
	}

	// Check for pending escalations.
	if d.escalationHandler != nil {
		summary := d.escalationHandler.GetEscalationSummary()
		if summary.PendingEscalations > 0 {
			// Still have pending escalations, stay in escalated state.
			return StateEscalated, nil
		}
		// No more pending escalations, return to request handling.
		return StateRequest, nil
	}

	// No escalation handler, return to request.
	return StateRequest, nil
}

// sendAbandonAndRequeue sends an ABANDON review response and re-queues the story.
func (d *Driver) sendAbandonAndRequeue(ctx context.Context) error {
	// Get the escalated story ID from escalation handler.
	if d.escalationHandler == nil {
		return fmt.Errorf("no escalation handler available")
	}

	summary := d.escalationHandler.GetEscalationSummary()
	if len(summary.Escalations) == 0 {
		return fmt.Errorf("no escalations found to abandon")
	}

	// Find the most recent pending escalation.
	var latestEscalation *EscalationEntry
	for _, escalation := range summary.Escalations {
		if escalation.Status == string(StatusPending) {
			if latestEscalation == nil || escalation.EscalatedAt.After(latestEscalation.EscalatedAt) {
				latestEscalation = escalation
			}
		}
	}

	if latestEscalation == nil {
		return fmt.Errorf("no pending escalations found to abandon")
	}

	storyID := latestEscalation.StoryID
	agentID := latestEscalation.AgentID

	// Create ABANDON review message.
	abandonMsg := proto.NewAgentMsg(proto.MsgTypeRESPONSE, d.architectID, agentID)
	abandonMsg.SetPayload("story_id", storyID)
	abandonMsg.SetPayload("review_result", "ABANDON")
	abandonMsg.SetPayload("review_notes", "Escalation timeout exceeded - abandoning current submission")
	abandonMsg.SetPayload("reviewed_at", time.Now().UTC().Format(time.RFC3339))
	abandonMsg.SetPayload("timeout_reason", "escalation_timeout")

	// Send abandon message using Effects pattern.
	sendEffect := &SendMessageEffect{Message: abandonMsg}
	if err := d.ExecuteEffect(ctx, sendEffect); err != nil {
		return fmt.Errorf("failed to send ABANDON message: %w", err)
	}

	// Re-queue the story by resetting it to pending status.
	story, exists := d.queue.GetStory(storyID)
	if !exists {
		return fmt.Errorf("story %s not found in queue", storyID)
	}

	// Reset to pending status so it can be picked up again.
	story.Status = StatusPending
	story.AssignedAgent = ""
	story.StartedAt = nil
	story.CompletedAt = nil
	story.LastUpdated = time.Now().UTC()

	// Trigger ready notification if dependencies are met.
	d.queue.checkAndNotifyReady()

	d.logger.Info("abandoned story %s due to escalation timeout and re-queued", storyID)
	return nil
}
