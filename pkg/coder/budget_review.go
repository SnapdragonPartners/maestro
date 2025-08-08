package coder

import (
	"context"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/utils"
)

// handleBudgetReview processes the BUDGET_REVIEW state - blocks waiting for architect's RESULT response.
func (c *Coder) handleBudgetReview(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	// State: waiting for architect guidance

	// Check if we already have approval result from previous processing.
	if approvalData, exists := sm.GetStateValue(string(stateDataKeyBudgetApprovalResult)); exists && approvalData != nil {
		return c.handleBudgetReviewApproval(ctx, sm, approvalData)
	}

	// Block waiting for RESULT message from architect.
	return c.handleRequestBlocking(ctx, sm, stateDataKeyBudgetApprovalResult, StateBudgetReview)
}

// handleBudgetReviewApproval processes budget review approval results.
func (c *Coder) handleBudgetReviewApproval(_ context.Context, sm *agent.BaseStateMachine, approvalData any) (proto.State, bool, error) {
	result, err := convertApprovalData(approvalData)
	if err != nil {
		return proto.StateError, false, logx.Wrap(err, "failed to convert budget review approval data")
	}

	// Store result and clear.
	sm.SetStateData(string(stateDataKeyBudgetApprovalResult), nil)
	sm.SetStateData(KeyBudgetReviewCompletedAt, time.Now().UTC())
	c.pendingApprovalRequest = nil // Clear since we have the result

	// Get origin state from stored data.
	originStr := utils.GetStateValueOr[string](sm, KeyOrigin, "")

	switch result.Status {
	case proto.ApprovalStatusApproved:
		// CONTINUE/PIVOT - return to origin state and reset counter.
		c.logger.Info("🧑‍💻 Budget review approved, returning to origin state: %s", originStr)

		// Reset the iteration counter for the origin state.
		switch originStr {
		case string(StatePlanning):
			sm.SetStateData(string(stateDataKeyPlanningIterations), 0)
			return StatePlanning, false, nil
		case string(StateCoding):
			sm.SetStateData(string(stateDataKeyCodingIterations), 0)
			return StateCoding, false, nil
		default:
			return StateCoding, false, nil // default fallback
		}
	case proto.ApprovalStatusNeedsChanges:
		// PIVOT - return to PLANNING and reset counter.
		c.logger.Info("🧑‍💻 Budget review needs changes, pivoting to PLANNING")
		sm.SetStateData(string(stateDataKeyPlanningIterations), 0)
		return StatePlanning, false, nil
	case proto.ApprovalStatusRejected:
		// ABANDON - move to ERROR.
		c.logger.Info("🧑‍💻 Budget review rejected, abandoning task")
		return proto.StateError, false, logx.Errorf("task abandoned by architect after budget review")
	default:
		return proto.StateError, false, logx.Errorf("unknown budget review approval status: %s", result.Status)
	}
}
