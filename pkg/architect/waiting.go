package architect

import (
	"context"
	"fmt"

	"orchestrator/pkg/proto"
)

// handleWaiting blocks until a REQUEST message is received.
// All work (specs, questions, approvals) comes through REQUEST messages now.
// When a request is received, transitions to SETUP to ensure workspace is ready.
func (d *Driver) handleWaiting(ctx context.Context) (proto.State, error) {
	select {
	case <-ctx.Done():
		return StateError, fmt.Errorf("architect waiting cancelled: %w", ctx.Err())
	case requestMsg, ok := <-d.questionsCh:
		if !ok {
			// Channel closed by dispatcher - abnormal shutdown
			return StateError, fmt.Errorf("requests channel closed unexpectedly")
		}

		if requestMsg == nil {
			// This shouldn't happen with proper channel management, but handle gracefully
			return StateWaiting, nil
		}

		// Store the request for processing after SETUP completes
		// This handles all types: spec reviews, questions, code approvals, etc.
		d.SetStateData(StateKeyCurrentRequest, requestMsg)

		// Transition to SETUP to ensure workspace is ready before processing
		return StateSetup, nil
	}
}

// ownsSpec checks if the architect currently owns a spec.
// Returns true if there are stories in the queue (indicating active spec work).
func (d *Driver) ownsSpec() bool {
	// Check if we have stories in the queue (indicating we're working on a spec)
	if d.queue != nil && len(d.queue.GetAllStories()) > 0 {
		return true
	}

	return false
}
