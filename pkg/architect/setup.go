package architect

import (
	"context"
	"fmt"

	"orchestrator/pkg/proto"
	"orchestrator/pkg/workspace"
)

// handleSetup ensures the architect workspace is ready before processing requests.
// This state is idempotent - it will clone if needed, or update if clone exists.
// The pending request is preserved in StateKeyCurrentRequest and processed after setup.
func (d *Driver) handleSetup(ctx context.Context) (proto.State, error) {
	d.logger.Info("SETUP: Ensuring architect workspace is ready")

	// Ensure the architect workspace exists and is up to date
	// This is idempotent: clones if missing, updates if exists
	architectWorkspace, err := workspace.EnsureArchitectWorkspace(ctx, d.workDir)
	if err != nil {
		// Workspace setup failed - this is a critical error
		d.logger.Error("SETUP: Failed to ensure architect workspace: %v", err)
		return StateError, fmt.Errorf("failed to ensure architect workspace: %w", err)
	}

	d.logger.Info("SETUP: Architect workspace ready at: %s", architectWorkspace)

	// The pending request is already stored in StateKeyCurrentRequest by handleWaiting.
	// Transition to REQUEST to process it.
	return StateRequest, nil
}
