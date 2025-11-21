package pm

import (
	"context"
	"fmt"
	"time"

	"orchestrator/pkg/proto"
)

// handlePreview handles the PREVIEW state where user reviews the spec.
// User actions (Continue Interview, Submit for Development) directly modify state via PreviewAction method.
// This handler just validates spec exists.
func (d *Driver) handlePreview(ctx context.Context) (proto.State, error) {
	d.logger.Debug("📋 PM in PREVIEW state - waiting for user action")

	// Verify we have a draft spec in state data
	stateData := d.GetStateData()
	draftSpec, ok := stateData["draft_spec_markdown"].(string)

	if !ok || draftSpec == "" {
		d.logger.Error("❌ No draft spec found in state data")
		return proto.StateError, fmt.Errorf("no draft spec in state data")
	}

	// Check for context cancellation (non-blocking)
	select {
	case <-ctx.Done():
		d.logger.Info("⏹️  Context canceled while in PREVIEW")
		return proto.StateDone, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
		// No state change yet - sleep briefly to avoid tight loop
		// Note: PreviewAction method modifies state directly,
		// and the Run loop will detect the change and route to the new handler
		time.Sleep(100 * time.Millisecond)
		return StatePreview, nil
	}
}
