package pm

import (
	"context"
	"fmt"

	"orchestrator/pkg/proto"
)

// handlePreview handles the PREVIEW state where user reviews the spec.
// User can choose to:
// - Continue Interview: inject "What changes would you like to make?" → AWAIT_USER
// - Submit for Development: send REQUEST to architect → AWAIT_ARCHITECT
//
// This function blocks waiting for user action via the interviewRequestCh channel.
func (d *Driver) handlePreview(ctx context.Context) (proto.State, error) {
	d.logger.Info("📋 PM in PREVIEW state - user reviewing spec")

	// Verify we have a draft spec in state data
	draftSpec, ok := d.stateData["draft_spec_markdown"].(string)
	if !ok || draftSpec == "" {
		d.logger.Error("❌ No draft spec found in state data")
		return proto.StateError, fmt.Errorf("no draft spec in state data")
	}

	// Log spec size for debugging
	d.logger.Info("📋 Draft spec ready (%d bytes)", len(draftSpec))

	// Block waiting for user action (Continue Interview or Submit for Development)
	// The WebUI will send a message via interviewRequestCh with the user's choice
	select {
	case <-ctx.Done():
		d.logger.Info("⏹️  Context canceled while in PREVIEW")
		return proto.StateDone, fmt.Errorf("context canceled: %w", ctx.Err())

	case msg := <-d.interviewRequestCh:
		if msg == nil {
			d.logger.Info("📭 Interview request channel closed")
			return proto.StateDone, nil
		}

		d.logger.Info("📨 Received message in PREVIEW state: %v (type: %s)", msg.ID, msg.Type)

		// Extract action from payload
		var action string
		if typedPayload := msg.GetTypedPayload(); typedPayload != nil {
			if payloadData, err := typedPayload.ExtractGeneric(); err == nil {
				action, _ = payloadData["action"].(string)
				d.logger.Info("📋 Extracted action from payload: '%s'", action)
			}
		}

		if action == "" {
			d.logger.Warn("⚠️  Received message without valid 'action' field in PREVIEW state - ignoring and staying in PREVIEW")
			// Stay in PREVIEW state and wait for a valid action
			return StatePreview, nil
		}

		switch action {
		case "continue_interview":
			d.logger.Info("🔄 User chose to continue interview")
			// Inject question to context
			d.contextManager.AddMessage("user-action", "What changes would you like to make?")
			return StateAwaitUser, nil

		case "submit_to_architect":
			d.logger.Info("📤 User chose to submit spec to architect")

			// Copy draft_spec_markdown to spec_markdown for sendSpecApprovalRequest
			if draftSpec, ok := d.stateData["draft_spec_markdown"].(string); ok {
				d.stateData["spec_markdown"] = draftSpec
			}

			// Send REQUEST to architect
			err := d.sendSpecApprovalRequest(ctx)
			if err != nil {
				d.logger.Error("❌ Failed to send spec approval request: %v", err)
				return proto.StateError, fmt.Errorf("failed to send approval request: %w", err)
			}

			d.logger.Info("✅ Spec submitted to architect, transitioning to AWAIT_ARCHITECT")
			return StateAwaitArchitect, nil

		default:
			d.logger.Error("❌ Unknown preview action: %s", action)
			return proto.StateError, fmt.Errorf("unknown preview action: %s", action)
		}
	}
}
