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
		// Guard against nil message from closed channel
		if msg == nil {
			d.logger.Warn("⚠️ Reply channel closed unexpectedly in AWAIT_ARCHITECT")
			return proto.StateError, fmt.Errorf("reply channel closed unexpectedly")
		}

		d.logger.Info("📨 Received RESPONSE message from architect: %s", msg.ID)

		// Parse the response
		if msg.Type != proto.MsgTypeRESPONSE {
			d.logger.Error("❌ Expected RESPONSE message, got %s", msg.Type)
			return proto.StateError, fmt.Errorf("expected RESPONSE message, got %s", msg.Type)
		}

		// Parse the typed payload - could be approval response or story completion
		typedPayload := msg.GetTypedPayload()
		if typedPayload == nil {
			d.logger.Error("❌ No typed payload in RESPONSE message")
			return proto.StateError, fmt.Errorf("no typed payload in RESPONSE message")
		}

		// Check payload kind to determine how to handle
		switch typedPayload.Kind {
		case proto.PayloadKindStoryComplete:
			// Story completion notification - handle and stay in current state
			//nolint:contextcheck // Handler uses context.Background() for quick local bootstrap detection
			return d.handleStoryCompleteNotification(typedPayload)

		case proto.PayloadKindApprovalResponse:
			// Continue with approval handling below
		default:
			d.logger.Warn("⚠️ Unexpected payload kind in AWAIT_ARCHITECT: %s", typedPayload.Kind)
			return proto.StateError, fmt.Errorf("unexpected payload kind: %s", typedPayload.Kind)
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

			// Clear user spec data from state - the spec has been submitted and we
			// don't want stale data prepended to future hotfixes.
			// The conversation context still has the spec history for PM reference.
			d.SetStateData(StateKeyUserSpecMd, nil)
			d.SetStateData(StateKeySpecMetadata, nil)
			d.SetStateData(StateKeySpecUploaded, nil)
			d.SetStateData(StateKeyBootstrapParams, nil)
			d.SetStateData(StateKeyIsHotfix, nil)
			d.SetStateData(StateKeyTurnCount, nil)

			// Re-run bootstrap detection to refresh bootstrap state.
			// This will either regenerate bootstrap spec (if something's still missing)
			// or clear it (if bootstrap is complete). More robust than manual clearing.
			//nolint:contextcheck // Bootstrap detection is a quick local operation
			d.detectAndStoreBootstrapRequirements(context.Background())

			// Mark that development is in flight - only hotfixes allowed now
			d.SetStateData(StateKeyInFlight, true)

			// Inject user message to inform PM of approval and prompt for response
			// Use chat_ask_user to post the message AND wait for user input (for tweaks/hotfixes)
			d.contextManager.AddMessage("user",
				"The specification has been approved by the architect and submitted for development. "+
					"Use the chat_ask_user tool to inform the user of this good news. Let them know you'll notify them "+
					"when there's a demo ready or when development completes. Also let them know they can request "+
					"tweaks or quick changes in the meantime. IMPORTANT: You MUST call chat_ask_user to post this message.")

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

// handleStoryCompleteNotification processes a story completion notification from architect.
// This injects a message into the PM's context so it can inform the user.
func (d *Driver) handleStoryCompleteNotification(payload *proto.MessagePayload) (proto.State, error) {
	storyComplete, err := payload.ExtractStoryComplete()
	if err != nil {
		d.logger.Error("❌ Failed to parse story_complete payload: %v", err)
		return proto.StateError, fmt.Errorf("failed to parse story_complete payload: %w", err)
	}

	// Log the completion
	if storyComplete.IsHotfix {
		d.logger.Info("🔧 Hotfix story completed: %s - %s", storyComplete.StoryID, storyComplete.Title)
	} else {
		d.logger.Info("✅ Story completed: %s - %s", storyComplete.StoryID, storyComplete.Title)
	}

	// Check if demo should become available after this story
	// Stories may create bootstrap components (Dockerfile, Makefile, etc.)
	if !d.demoAvailable {
		d.logger.Debug("Story completed, checking if bootstrap is now complete...")
		//nolint:contextcheck // Bootstrap detection is a quick local operation, context.Background() is appropriate
		d.detectAndStoreBootstrapRequirements(context.Background())
	}

	// Inject a user message so PM can inform the user about the completion
	completionMsg := fmt.Sprintf(
		"A story has been completed by the development team. Story: %q (ID: %s). ",
		storyComplete.Title, storyComplete.StoryID)
	if storyComplete.IsHotfix {
		completionMsg += "This was a hotfix request. "
	}
	if storyComplete.Summary != "" {
		completionMsg += fmt.Sprintf("Summary: %s ", storyComplete.Summary)
	}
	completionMsg += "Use chat_ask_user to inform the user about this progress and ask if they need anything else."

	d.contextManager.AddMessage("user", completionMsg)

	// Transition to WORKING so PM can generate a response to inform the user
	return StateWorking, nil
}
