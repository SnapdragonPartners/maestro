package architect

import (
	"context"
	"fmt"

	"orchestrator/pkg/proto"
)

// handleWaiting blocks until a spec message or question is received.
func (d *Driver) handleWaiting(ctx context.Context) (proto.State, error) {
	d.logger.Info("🏗️ Architect waiting for spec or question...")

	select {
	case <-ctx.Done():
		d.logger.Info("🏗️ Architect WAITING state context cancelled")
		return StateError, fmt.Errorf("architect waiting cancelled: %w", ctx.Err())
	case specMsg, ok := <-d.specCh:
		if !ok {
			// Channel closed by dispatcher - abnormal shutdown
			d.logger.Info("🏗️ Spec channel closed, transitioning to ERROR")
			return StateError, fmt.Errorf("spec channel closed unexpectedly")
		}

		if specMsg == nil {
			// This shouldn't happen with proper channel management, but handle gracefully
			d.logger.Warn("🏗️ Received nil spec message on open channel")
			return StateWaiting, nil
		}
		d.logger.Info("🏗️ Architect received spec message %s, transitioning to SCOPING", specMsg.ID)

		// Store the spec message for processing in SCOPING state.
		d.stateData["spec_message"] = specMsg

		return StateScoping, nil
	case questionMsg, ok := <-d.questionsCh:
		if !ok {
			// Channel closed by dispatcher - abnormal shutdown
			d.logger.Info("🏗️ Questions channel closed, transitioning to ERROR")
			return StateError, fmt.Errorf("questions channel closed unexpectedly")
		}

		if questionMsg == nil {
			// This shouldn't happen with proper channel management, but handle gracefully
			d.logger.Warn("🏗️ Received nil question message on open channel")
			return StateWaiting, nil
		}
		d.logger.Info("🏗️ Architect received question message %s in WAITING state, transitioning to REQUEST", questionMsg.ID)

		// Store the question for processing in REQUEST state.
		d.stateData["current_request"] = questionMsg

		return StateRequest, nil
	}
}

// ownsSpec checks if the architect currently owns a spec.
func (d *Driver) ownsSpec() bool {
	// Check if we have a spec message in state data.
	if _, hasSpec := d.stateData["spec_message"]; hasSpec {
		return true
	}

	// Check if we have stories in the queue (indicating we're working on a spec).
	if d.queue != nil && len(d.queue.GetAllStories()) > 0 {
		return true
	}

	return false
}
