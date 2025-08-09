package architect

import (
	"context"

	"orchestrator/pkg/proto"
)

// handleMerging processes the merging phase (merging approved code).
func (d *Driver) handleMerging(_ context.Context) (proto.State, error) {
	// State: processing completed stories

	// TODO: Implement proper merging logic without RequestWorker
	// For now, immediately return to dispatching to check for new ready stories.
	d.logger.Info("🏗️ Merging completed, returning to dispatching")
	return StateDispatching, nil
}
