package coder

import (
	"context"
	"fmt"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/git"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/utils"
)

// handleAwaitMerge processes the AWAIT_MERGE state, waiting for merge results from architect.
//
//nolint:unparam // bool return is part of state machine interface
func (c *Coder) handleAwaitMerge(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	// State: waiting for architect merge result

	// Check if we already have merge result from previous processing.
	if result, exists := agent.GetTyped[git.MergeResult](sm, KeyMergeResult); exists {
		sm.SetStateData(KeyMergeCompletedAt, time.Now().UTC())

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

	// Block waiting for RESULT message from architect.
	c.logger.Debug("🧑‍💻 Blocking in AWAIT_MERGE, waiting for architect merge result...")
	select {
	case <-ctx.Done():
		return proto.StateError, false, fmt.Errorf("coder await merge cancelled: %w", ctx.Err())
	case resultMsg, ok := <-c.replyCh:
		if !ok {
			// Channel closed by dispatcher - abnormal shutdown
			c.logger.Info("🧑‍💻 Reply channel closed in AWAIT_MERGE, transitioning to ERROR")
			return proto.StateError, true, fmt.Errorf("reply channel closed unexpectedly during merge")
		}
		if resultMsg == nil {
			c.logger.Warn("🧑‍💻 Received nil RESULT message")
			return StateAwaitMerge, false, nil
		}

		if resultMsg.Type == proto.MsgTypeRESPONSE {
			c.logger.Info("🧑‍💻 Received RESPONSE message %s for merge", resultMsg.ID)

			// Extract merge result and store it.
			if status, exists := resultMsg.GetPayload("status"); exists {
				if statusStr, ok := utils.SafeAssert[string](status); ok {
					mergeResult := git.MergeResult{
						Status: statusStr,
					}
					if conflictInfo, exists := resultMsg.GetPayload("conflict_details"); exists {
						if conflictInfoStr, ok := utils.SafeAssert[string](conflictInfo); ok {
							mergeResult.ConflictInfo = conflictInfoStr
						}
					}
					if mergeCommit, exists := resultMsg.GetPayload("merge_commit"); exists {
						if mergeCommitStr, ok := utils.SafeAssert[string](mergeCommit); ok {
							mergeResult.MergeCommit = mergeCommitStr
						}
					}

					agent.SetTyped(sm, KeyMergeResult, mergeResult)
					c.logger.Info("🧑‍💻 Merge result received and stored")
					// Return same state to re-process with the new merge data.
					return StateAwaitMerge, false, nil
				}
			} else {
				c.logger.Error("🧑‍💻 RESULT message missing status payload")
				return proto.StateError, false, logx.Errorf("RESULT message missing status")
			}
		} else {
			c.logger.Warn("🧑‍💻 Received unexpected message type: %s", resultMsg.Type)
			return StateAwaitMerge, false, nil
		}
	}

	// This should not be reached, but add for completeness.
	return StateAwaitMerge, false, nil
}
