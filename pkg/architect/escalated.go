package architect

import (
	"context"
	"fmt"
	"time"

	"orchestrator/pkg/proto"
)

// handleEscalated processes the escalated phase using chat-based escalation.
// This handler is reached when iteration limits are exceeded during request processing.
// It posts an escalation message to chat, waits for human reply, and returns to the origin state.
func (d *Driver) handleEscalated(ctx context.Context) (proto.State, error) {
	d.logger.Info("🚨 Entered ESCALATED state - waiting for human guidance")

	// Get state data
	stateData := d.GetStateData()

	// Get escalation context from state data
	originState, ok := stateData["escalation_origin_state"].(string)
	if !ok || originState == "" {
		return StateError, fmt.Errorf("escalation_origin_state not found in state data")
	}

	iterationCount, ok := stateData["escalation_iteration_count"].(int)
	if !ok {
		iterationCount = 0 // Default if not found
	}

	requestID, _ := stateData["escalation_request_id"].(string)
	storyID, _ := stateData["escalation_story_id"].(string)
	agentID, _ := stateData["escalation_agent_id"].(string)

	// Check if chat service is available
	if d.chatService == nil {
		d.logger.Error("Chat service not available for escalation - transitioning to ERROR")
		return StateError, fmt.Errorf("chat service not available for escalation")
	}

	// Check if we've already posted an escalation message
	escalationMsgID, hasPosted := stateData["escalation_message_id"].(int64)

	if !hasPosted {
		// First time in ESCALATED state - post escalation message
		escalationText := d.buildEscalationMessage(originState, iterationCount, requestID, storyID)

		resp, err := d.chatService.Post(ctx, &ChatPostRequest{
			Author:   d.GetAgentID(),
			Text:     escalationText,
			Channel:  "development", // Escalation goes to development channel
			PostType: "escalate",
		})

		if err != nil {
			d.logger.Error("Failed to post escalation message: %v", err)
			return StateError, fmt.Errorf("failed to post escalation: %w", err)
		}

		escalationMsgID = resp.ID
		d.SetStateData("escalation_message_id", escalationMsgID)
		d.SetStateData("escalated_at", time.Now())

		d.logger.Info("📢 Posted escalation message (id=%d) - waiting for human reply", escalationMsgID)
	}

	// Check escalation timeout (2 hours)
	escalatedAt, _ := stateData["escalated_at"].(time.Time)
	if !escalatedAt.IsZero() {
		timeSinceEscalation := time.Since(escalatedAt)
		if timeSinceEscalation > EscalationTimeout {
			d.logger.Warn("Escalation timeout exceeded (%v > %v) - transitioning to ERROR",
				timeSinceEscalation.Truncate(time.Minute), EscalationTimeout)
			return StateError, fmt.Errorf("escalation timeout exceeded (%v)", timeSinceEscalation)
		}

		// Log time remaining periodically
		timeRemaining := EscalationTimeout - timeSinceEscalation
		d.logger.Debug("Escalation timeout: %v remaining (escalated %v ago)",
			timeRemaining.Truncate(time.Minute), timeSinceEscalation.Truncate(time.Minute))
	}

	// Wait for human reply with timeout
	pollInterval := 5 * time.Second
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second) // Short timeout for this poll cycle
	defer cancel()

	reply, err := d.chatService.WaitForReply(waitCtx, escalationMsgID, pollInterval)
	if err != nil {
		// Check if it's a timeout (expected during polling loop)
		if waitCtx.Err() == context.DeadlineExceeded {
			// No reply yet - stay in ESCALATED state and poll again next cycle
			return StateEscalated, nil
		}

		// Other errors
		d.logger.Warn("Error waiting for escalation reply: %v", err)
		return StateEscalated, nil // Continue waiting
	}

	// Got a reply!
	d.logger.Info("✅ Received human reply (id=%d) to escalation", reply.ID)

	// Add human guidance to agent-specific context
	if agentID != "" {
		cm := d.getContextForAgent(agentID)
		cm.AddMessage("system", fmt.Sprintf(
			"🧑 HUMAN GUIDANCE: %s\n\nPlease incorporate this guidance and continue with your task.",
			reply.Text,
		))
	} else {
		d.logger.Warn("No agent_id found in escalation context - cannot add human guidance to specific context")
	}

	// Reset iteration counter for the origin state
	originStateIterationKey := fmt.Sprintf("%s_iterations", originState)
	d.SetStateData(originStateIterationKey, 0)

	// Clear escalation state data (set to nil to clear)
	d.SetStateData("escalation_message_id", nil)
	d.SetStateData("escalation_origin_state", nil)
	d.SetStateData("escalation_iteration_count", nil)
	d.SetStateData("escalation_request_id", nil)
	d.SetStateData("escalation_story_id", nil)
	d.SetStateData("escalation_agent_id", nil)
	d.SetStateData("escalated_at", nil)

	// Return to REQUEST state (where escalations originate from)
	d.logger.Info("↩️  Returning to REQUEST state with human guidance")
	return StateRequest, nil
}

// buildEscalationMessage creates the escalation message text for human review.
func (d *Driver) buildEscalationMessage(originState string, iterationCount int, requestID, storyID string) string {
	msg := fmt.Sprintf(`🚨 ESCALATION: Iteration limit exceeded

I have reached my iteration limit (%d iterations) while processing a request and need human guidance.

**Context:**
- Origin State: %s
- Request ID: %s`, iterationCount, originState, requestID)

	if storyID != "" {
		msg += fmt.Sprintf("\n- Story ID: %s", storyID)
	}

	msg += `

**What I was trying to do:**
I was using MCP read tools to explore code and answer a request, but exceeded my iteration budget without completing the task.

**What I need:**
Please provide guidance on how to proceed. Your reply will be added to my context and I'll continue with your guidance.`

	return msg
}
