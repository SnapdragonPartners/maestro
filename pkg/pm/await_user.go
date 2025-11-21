package pm

import (
	"context"
	"time"

	"orchestrator/pkg/proto"
)

// handleAwaitUser implements the AWAIT_USER state - PM is blocked waiting for user's chat response.
// This state polls for new chat messages and transitions back to WORKING when messages arrive.
//
//nolint:revive,unparam // ctx parameter kept for consistency with other state handlers
func (d *Driver) handleAwaitUser(ctx context.Context) (proto.State, error) {
	d.logger.Debug("⏳ PM awaiting user response")

	// Check for new messages without blocking
	if !d.chatService.HaveNewMessages(d.GetAgentID()) {
		// No new messages yet - sleep briefly to avoid tight polling
		time.Sleep(500 * time.Millisecond)
		return StateAwaitUser, nil
	}

	// New messages arrived! Transition back to WORKING to process them
	d.logger.Info("📬 New messages received, PM resuming work")
	return StateWorking, nil
}
