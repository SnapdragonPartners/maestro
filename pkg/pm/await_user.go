package pm

import (
	"context"
	"fmt"
	"time"

	"orchestrator/pkg/proto"
)

// handleAwaitUser implements the AWAIT_USER state - PM is blocked waiting for user's chat response
// OR architect notifications (story completions, all-stories-complete, escalations).
//
// This dual-channel approach ensures PM receives architect notifications even while waiting for user input.
//
//nolint:revive,unparam // ctx parameter kept for consistency with other state handlers
func (d *Driver) handleAwaitUser(ctx context.Context) (proto.State, error) {
	d.logger.Debug("⏳ PM awaiting user response or architect notification")

	// Use select with timeout to check both channels
	select {
	case <-ctx.Done():
		d.logger.Info("⏹️  Context canceled while awaiting user")
		return proto.StateDone, fmt.Errorf("context canceled: %w", ctx.Err())

	case msg := <-d.replyCh:
		// Architect notification received
		if msg == nil {
			d.logger.Warn("⚠️ Received nil message from architect reply channel")
			return StateAwaitUser, nil
		}
		//nolint:contextcheck // Handler uses context.Background() for quick local bootstrap detection
		return d.handleArchitectNotification(msg)

	case <-time.After(awaitUserPollTimeout):
		// Timeout - check for user messages
		if d.chatService.HaveNewMessages(d.GetAgentID()) {
			d.logger.Info("📬 New user messages received, PM resuming work")
			return StateWorking, nil
		}
		// No messages on either channel, stay in AWAIT_USER
		return StateAwaitUser, nil
	}
}

// handleArchitectNotification processes notifications from the architect.
// Handles story completions, all-stories-complete, and escalations.
func (d *Driver) handleArchitectNotification(msg *proto.AgentMsg) (proto.State, error) {
	d.logger.Info("📨 Received architect notification: %s (type: %s)", msg.ID, msg.Type)

	// Parse the typed payload
	typedPayload := msg.GetTypedPayload()
	if typedPayload == nil {
		d.logger.Warn("⚠️ No typed payload in architect notification")
		return StateAwaitUser, nil
	}

	switch typedPayload.Kind {
	case proto.PayloadKindStoryComplete:
		// Individual story completion - log and inform user
		storyComplete, err := typedPayload.ExtractStoryComplete()
		if err != nil {
			d.logger.Error("❌ Failed to parse story_complete payload: %v", err)
			return StateAwaitUser, nil
		}

		if storyComplete.IsHotfix {
			d.logger.Info("🔧 Hotfix completed: %s - %s", storyComplete.StoryID, storyComplete.Title)
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

		// Inject message so PM can inform user
		completionMsg := fmt.Sprintf(
			"A story has been completed by the development team. Story: %q (ID: %s). ",
			storyComplete.Title, storyComplete.StoryID)
		if storyComplete.IsHotfix {
			completionMsg += "This was a hotfix request. "
		}
		if storyComplete.Summary != "" {
			completionMsg += fmt.Sprintf("Summary: %s ", storyComplete.Summary)
		}
		completionMsg += "Use chat_ask_user to inform the user about this progress."

		d.contextManager.AddMessage("user", completionMsg)
		return StateWorking, nil

	case proto.PayloadKindAllStoriesComplete:
		// All stories complete - set in_flight to false and inform user
		allComplete, err := typedPayload.ExtractAllStoriesComplete()
		if err != nil {
			d.logger.Error("❌ Failed to parse all_stories_complete payload: %v", err)
			return StateAwaitUser, nil
		}

		d.logger.Info("🎉 All stories complete! Spec: %s, Total: %d stories", allComplete.SpecID, allComplete.TotalStories)

		// Clear in_flight flag - PM can now accept full specs again
		d.SetStateData(StateKeyInFlight, false)

		// Inject message so PM can inform user
		completionMsg := fmt.Sprintf(
			"Great news! All development work has been completed. "+
				"Total of %d stories were implemented successfully. ",
			allComplete.TotalStories)
		if allComplete.DemoReady {
			completionMsg += "The demo is now ready - the user can access it from the Demo tab. "
		}
		if allComplete.Message != "" {
			completionMsg += allComplete.Message + " "
		}
		completionMsg += "Use chat_ask_user to inform the user about this exciting milestone and ask if they'd like to try the demo or request any changes."

		d.contextManager.AddMessage("user", completionMsg)
		return StateWorking, nil

	default:
		d.logger.Warn("⚠️ Unhandled architect notification kind: %s", typedPayload.Kind)
		return StateAwaitUser, nil
	}
}
