package coder

import (
	"context"
	"fmt"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/git"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
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
		// Get merge feedback
		feedback := result.ConflictInfo
		if feedback == "" {
			feedback = "Unknown merge issue"
		}

		c.logger.Info("🧑‍💻 Merge needs changes, transitioning to CODING: %s", feedback)

		// If all todos were complete (which allowed done to be called), add feedback as new todo
		if c.todoList != nil {
			allComplete := c.todoList.GetCurrentTodo() == nil && c.todoList.GetCompletedCount() == c.todoList.GetTotalCount()
			if allComplete && feedback != "" {
				feedbackTodo := fmt.Sprintf("Address merge issue: %s", feedback)
				c.todoList.AddTodo(feedbackTodo, -1) // -1 means append to end
				c.logger.Info("📋 Added merge feedback as new todo")
				sm.SetStateData("todo_list", c.todoList)
			}
		}

		// Use mini-template to format the merge failure message
		if c.renderer != nil {
			renderedMessage, err := c.renderer.RenderSimple(templates.MergeFailureFeedbackTemplate, feedback)
			if err != nil {
				c.logger.Error("Failed to render merge failure feedback: %v", err)
				// Fallback to simple message
				renderedMessage = fmt.Sprintf("Merge requires changes. Fix and resubmit: %s", feedback)
			}
			c.contextManager.AddMessage("architect", renderedMessage)
		}

		return StateCoding, false, nil

	case string(proto.ApprovalStatusRejected):
		c.logger.Error("🧑‍💻 Merge rejected - unrecoverable error: %s", result.ConflictInfo)
		return proto.StateError, false, logx.Errorf("merge rejected: %s", result.ConflictInfo)

	default:
		return proto.StateError, false, logx.Errorf("unknown merge status: %s", result.Status)
	}
}
