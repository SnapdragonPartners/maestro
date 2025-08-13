package coder

import (
	"context"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/effect"
	"orchestrator/pkg/git"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/utils"
)

// handleAwaitMerge processes the AWAIT_MERGE state using Effects pattern
//
//nolint:unparam // bool return is part of state machine interface
func (c *Coder) handleAwaitMerge(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	// Get story information and merge details from state
	storyID := utils.GetStateValueOr[string](sm, KeyStoryID, "")
	prURL := utils.GetStateValueOr[string](sm, KeyPRURL, "")
	branchName := utils.GetStateValueOr[string](sm, KeyPushedBranch, "")

	if storyID == "" {
		return proto.StateError, false, logx.Errorf("no story ID found in AWAIT_MERGE state")
	}

	// Create merge effect
	mergeEff := effect.NewMergeEffect(storyID, prURL, branchName)

	// Execute merge effect - blocks until architect responds
	result, err := c.ExecuteEffect(ctx, mergeEff)
	if err != nil {
		c.logger.Error("🧑‍💻 Merge effect failed: %v", err)
		return proto.StateError, false, logx.Wrap(err, "merge effect failed")
	}

	// Process the merge result
	if mergeResult, ok := result.(*git.MergeResult); ok {
		return c.processMergeResult(ctx, sm, mergeResult)
	}

	return proto.StateError, false, logx.Errorf("invalid merge result type: %T", result)
}

// processMergeResult processes the architect's merge response and determines next state.
func (c *Coder) processMergeResult(_ context.Context, sm *agent.BaseStateMachine, result *git.MergeResult) (proto.State, bool, error) {
	// Store completion timestamp
	sm.SetStateData(KeyMergeCompletedAt, time.Now().UTC())

	// Handle merge based on status
	switch result.Status {
	case "merged":
		c.logger.Info("🧑‍💻 PR merged successfully, story complete")
		return proto.StateDone, false, nil

	case "merge_conflict":
		c.logger.Info("🧑‍💻 Merge conflict detected, transitioning to CODING for resolution")
		sm.SetStateData(KeyMergeConflictDetails, result.ConflictInfo)
		sm.SetStateData(KeyCodingMode, "merge_fix")
		return StateCoding, false, nil

	default:
		return proto.StateError, false, logx.Errorf("unknown merge status: %s", result.Status)
	}
}
