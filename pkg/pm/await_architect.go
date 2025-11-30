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

		// Parse ApprovalResponsePayload from the message
		typedPayload := msg.GetTypedPayload()
		if typedPayload == nil {
			d.logger.Error("❌ No typed payload in RESPONSE message")
			return proto.StateError, fmt.Errorf("no typed payload in RESPONSE message")
		}

		approvalResult, err := typedPayload.ExtractApprovalResponse()
		if err != nil {
			d.logger.Error("❌ Failed to parse approval response: %v", err)
			return proto.StateError, fmt.Errorf("failed to parse approval response: %w", err)
		}

		// Check approval status
		if approvalResult.Status == proto.ApprovalStatusApproved {
			// Spec approved - stay engaged for tweaks/hotfixes
			d.logger.Info("✅ Spec APPROVED by architect - staying engaged for tweaks")
			if approvalResult.Feedback != "" {
				d.logger.Info("📝 Approval feedback: %s", approvalResult.Feedback)
			}

			// Clear spec-specific data but keep conversation context
			d.SetStateData("draft_spec_markdown", nil)
			d.SetStateData("spec_metadata", nil)
			// Keep bootstrap requirements - project is bootstrapped and ready for hotfixes
			// d.SetStateData(StateKeyBootstrapRequirements, nil)
			d.SetStateData("bootstrap_params", nil)

			// Mark that we're in post-approval mode (development in progress)
			d.SetStateData(StateKeyDevelopmentInProgress, true)

			// Inject user message to inform PM of approval and prompt for response
			d.contextManager.AddMessage("user",
				"The specification has been approved by the architect and submitted for development. "+
					"Please inform the user and let them know you'll notify them when there's a demo ready "+
					"or when development completes. Also let them know they can request tweaks or changes in the meantime.")

			// Transition to WORKING so PM generates response to user
			return StateWorking, nil
		}

		// Architect provided feedback - inject as system message and transition to WORKING
		d.logger.Info("📝 Spec requires changes - feedback from architect (status=%s)", approvalResult.Status)

		if approvalResult.Feedback == "" {
			d.logger.Error("❌ Missing feedback in NEEDS_CHANGES response")
			return proto.StateError, fmt.Errorf("missing feedback in NEEDS_CHANGES response")
		}

		// Inject system message with architect feedback
		systemMessage := fmt.Sprintf(
			"The architect provided the following feedback on your spec. Address these issues and resubmit "+
				"or ask the user for any needed clarifications. The user has not seen the raw feedback. %s",
			approvalResult.Feedback,
		)
		// Add as system-level guidance message
		d.contextManager.AddMessage("architect-feedback", systemMessage)

		// Keep spec in state data for potential resubmission
		return StateWorking, nil
	}
}
