package coder

import (
	"context"
	"fmt"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/git"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
)

// handleAwaitMerge processes the AWAIT_MERGE state using Effects pattern
//
//nolint:unparam // bool return is part of state machine interface
func (c *Coder) handleAwaitMerge(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	// Get merge result that was stored by PREPARE_MERGE state
	mergeResultRaw, exists := sm.GetStateValue(KeyMergeResult)
	if !exists {
		return proto.StateError, false, logx.Errorf("no merge result found in AWAIT_MERGE state - should have been set by PREPARE_MERGE")
	}

	// Process the merge result
	if mergeResult, ok := mergeResultRaw.(*git.MergeResult); ok {
		return c.processMergeResult(ctx, sm, mergeResult)
	}

	return proto.StateError, false, logx.Errorf("invalid merge result type: %T", mergeResultRaw)
}

// processMergeResult processes the architect's merge response and determines next state.
func (c *Coder) processMergeResult(_ context.Context, sm *agent.BaseStateMachine, result *git.MergeResult) (proto.State, bool, error) {
	// Store completion timestamp
	sm.SetStateData(KeyMergeCompletedAt, time.Now().UTC())

	// Handle merge based on standardized approval status
	switch result.Status {
	case string(proto.ApprovalStatusApproved):
		c.logger.Info("🧑‍💻 PR merged successfully, story complete")
		return proto.StateDone, false, nil

	case string(proto.ApprovalStatusNeedsChanges):
		// Append context message with architect's feedback
		feedback := result.ConflictInfo
		if feedback == "" {
			feedback = "Unknown merge issue"
		}
		contextMessage := fmt.Sprintf("Merge requires changes. Fix and resubmit: %s", feedback)

		c.logger.Info("🧑‍💻 Merge needs changes, transitioning to CODING: %s", feedback)

		// Append context to conversation
		c.contextManager.AddMessage("architect", contextMessage)

		sm.SetStateData(KeyMergeConflictDetails, result.ConflictInfo)
		sm.SetStateData(KeyCodingMode, "merge_retry")
		return StateCoding, false, nil

	case string(proto.ApprovalStatusRejected):
		c.logger.Error("🧑‍💻 Merge rejected - unrecoverable error: %s", result.ConflictInfo)
		return proto.StateError, false, logx.Errorf("merge rejected: %s", result.ConflictInfo)

	default:
		return proto.StateError, false, logx.Errorf("unknown merge status: %s", result.Status)
	}
}
