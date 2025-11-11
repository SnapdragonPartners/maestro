package pm

import (
	"context"
	"fmt"

	"orchestrator/pkg/proto"
)

// handleAwaitArchitect handles the AWAIT_ARCHITECT state where PM blocks waiting for architect's response.
// Blocks on the response channel waiting for a RESULT message from architect.
//
// Two possible outcomes:
// - Feedback (approved=false): inject architect feedback as system message → WORKING.
// - Approval (approved=true): spec approved → WAITING.
func (d *Driver) handleAwaitArchitect(ctx context.Context) (proto.State, error) {
	d.logger.Info("⏳ PM in AWAIT_ARCHITECT state - waiting for architect response")

	// Block on response channel waiting for RESULT message
	select {
	case <-ctx.Done():
		d.logger.Info("⏹️  Context canceled while awaiting architect")
		return proto.StateDone, fmt.Errorf("context canceled: %w", ctx.Err())

	case msg := <-d.replyCh:
		d.logger.Info("📨 Received RESPONSE message from architect: %s", msg.ID)

		// Parse the response
		if msg.Type != proto.MsgTypeRESPONSE {
			d.logger.Error("❌ Expected RESPONSE message, got %s", msg.Type)
			return proto.StateError, fmt.Errorf("expected RESPONSE message, got %s", msg.Type)
		}

		// Check if approved (metadata is map[string]string)
		approvedStr := msg.Metadata["approved"]
		if approvedStr == "" {
			d.logger.Error("❌ Missing 'approved' field in RESPONSE message")
			return proto.StateError, fmt.Errorf("missing 'approved' field")
		}

		if approvedStr == "true" {
			// Spec approved - transition to WAITING for next interview
			d.logger.Info("✅ Spec APPROVED by architect")
			// Clear draft spec from state data
			delete(d.stateData, "draft_spec_markdown")
			delete(d.stateData, "spec_metadata")
			return StateWaiting, nil
		}

		// Architect provided feedback - inject as system message and transition to WORKING
		feedback := msg.Metadata["feedback"]
		if feedback == "" {
			d.logger.Error("❌ Missing 'feedback' field in RESPONSE message")
			return proto.StateError, fmt.Errorf("missing 'feedback' field")
		}

		d.logger.Info("📝 Spec requires changes - feedback from architect")

		// Inject system message with architect feedback
		systemMessage := fmt.Sprintf(
			"The architect provided the following feedback on your spec. Address these issues and resubmit "+
				"or ask the user for any needed clarifications. The user has not seen the raw feedback. %s",
			feedback,
		)
		// Add as system-level guidance message
		d.contextManager.AddMessage("architect-feedback", systemMessage)

		// Keep spec in state data for potential resubmission
		return StateWorking, nil
	}
}
