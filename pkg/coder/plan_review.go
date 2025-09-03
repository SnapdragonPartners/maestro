package coder

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/effect"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/utils"
)

// handlePlanReview processes the PLAN_REVIEW state using the Effects pattern.
func (c *Coder) handlePlanReview(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	// Determine approval type based on pending request
	var approvalType proto.ApprovalType = proto.ApprovalTypePlan // default

	if c.pendingApprovalRequest != nil {
		approvalType = c.pendingApprovalRequest.Type
	}

	// Create the appropriate approval effect based on type
	var eff effect.Effect
	switch approvalType {
	case proto.ApprovalTypePlan:
		planContent := c.getPlanContentForReview(sm)
		taskContent := c.getTaskContentForReview(sm)
		storyID := c.GetStoryID() // Use the getter method I created
		eff = effect.NewPlanApprovalEffectWithStoryID(planContent, taskContent, storyID)
		c.contextManager.AddAssistantMessage("Plan review phase: requesting architect approval")

	case proto.ApprovalTypeCompletion:
		summary := c.getCompletionSummaryForReview(sm)
		filesCreated := c.getFilesCreatedForReview(sm)
		storyID := c.GetStoryID() // Use the getter method I created
		eff = effect.NewCompletionApprovalEffectWithStoryID(summary, filesCreated, storyID)
		c.contextManager.AddAssistantMessage("Completion review phase: requesting architect approval")

	default:
		return proto.StateError, false, logx.Errorf("unsupported approval type: %s", approvalType)
	}

	// Execute the approval effect
	c.logger.Info("🧑‍💻 Requesting %s approval from architect", approvalType)
	result, err := c.ExecuteEffect(ctx, eff)
	if err != nil {
		c.logger.Error("🧑‍💻 Failed to get %s approval: %v", approvalType, err)
		return proto.StateError, false, logx.Wrap(err, fmt.Sprintf("failed to get %s approval", approvalType))
	}

	// Convert result to ApprovalResult
	approvalResult, ok := result.(*effect.ApprovalResult)
	if !ok {
		return proto.StateError, false, logx.Errorf("unexpected result type from approval effect: %T", result)
	}

	// Clear pending request since we have the result
	c.pendingApprovalRequest = nil
	sm.SetStateData(KeyPlanReviewCompletedAt, time.Now().UTC())

	// Process the approval result
	switch approvalResult.Status {
	case proto.ApprovalStatusApproved:
		c.logger.Info("🧑‍💻 %s approved by architect", approvalType)
		return c.handlePlanReviewApproval(ctx, sm, approvalType)

	case proto.ApprovalStatusNeedsChanges:
		c.logger.Info("🧑‍💻 %s needs changes, returning to PLANNING with feedback", approvalType)
		if approvalResult.Feedback != "" {
			c.contextManager.AddMessage("architect", fmt.Sprintf("Feedback: %s", approvalResult.Feedback))
		}
		return StatePlanning, false, nil

	case proto.ApprovalStatusRejected:
		if approvalType == proto.ApprovalTypeCompletion {
			c.logger.Error("🧑‍💻 Completion request rejected by architect: %s", approvalResult.Feedback)
			return proto.StateError, false, logx.Errorf("completion rejected by architect: %s", approvalResult.Feedback)
		} else {
			c.logger.Info("🧑‍💻 %s rejected, returning to PLANNING with feedback", approvalType)
			if approvalResult.Feedback != "" {
				c.contextManager.AddMessage("architect", fmt.Sprintf("Feedback: %s", approvalResult.Feedback))
			}
			return StatePlanning, false, nil
		}

	default:
		return proto.StateError, false, logx.Errorf("unknown %s approval status: %s", approvalType, approvalResult.Status)
	}
}

// handlePlanReviewApproval handles approved plan review based on approval type.
func (c *Coder) handlePlanReviewApproval(ctx context.Context, sm *agent.BaseStateMachine, approvalType proto.ApprovalType) (proto.State, bool, error) {
	switch approvalType {
	case proto.ApprovalTypePlan:
		// Regular plan approved - configure container and proceed to coding
		c.logger.Info("🧑‍💻 Development plan approved, reconfiguring container for coding")

		// Reconfigure container with read-write workspace for coding phase
		// Note: configureWorkspaceMount(readonly=false) creates a new coding container with GitHub auth
		if c.longRunningExecutor != nil {
			if err := c.configureWorkspaceMount(ctx, false, "coding"); err != nil {
				return proto.StateError, false, logx.Wrap(err, "failed to configure coding container")
			}
		}

		// Update story status to CODING via dispatcher (non-blocking)
		if c.dispatcher != nil {
			storyID := c.GetStoryID() // Get story ID for status update
			if err := c.dispatcher.UpdateStoryStatus(storyID, "coding"); err != nil {
				c.logger.Warn("Failed to update story status to coding: %v", err)
				// Continue anyway - status update failure shouldn't block the workflow
			} else {
				c.logger.Info("✅ Story %s status updated to CODING", storyID)
			}
		}

		c.logger.Info("🧑‍💻 Container reconfigured, transitioning to CODING")
		return StateCoding, false, nil

	case proto.ApprovalTypeCompletion:
		// Completion request approved - story is complete
		c.logger.Info("🧑‍💻 Story completion approved by architect, transitioning to DONE")

		// Mark story as completed
		sm.SetStateData(KeyStoryCompletedAt, time.Now().UTC())
		sm.SetStateData(KeyCompletionStatus, "APPROVED")

		return proto.StateDone, true, nil

	default:
		return proto.StateError, false, logx.Errorf("unsupported approval type in plan review: %s", approvalType)
	}
}

// Helper methods to extract data for approval requests

// getPlanContentForReview extracts plan content from state for review.
func (c *Coder) getPlanContentForReview(sm *agent.BaseStateMachine) string {
	planContent := utils.GetStateValueOr[string](sm, string(stateDataKeyPlan), "")
	if planContent == "" {
		// Fallback to context if plan content not in state
		planContent = c.getLastAssistantMessage()
	}
	return planContent
}

// getTaskContentForReview extracts task content from state for review.
func (c *Coder) getTaskContentForReview(sm *agent.BaseStateMachine) string {
	return utils.GetStateValueOr[string](sm, string(stateDataKeyTaskContent), "")
}

// getCompletionSummaryForReview extracts completion summary from state for review.
func (c *Coder) getCompletionSummaryForReview(_ *agent.BaseStateMachine) string {
	// Since there's no specific completion summary in state, generate one from context
	summary := c.getLastAssistantMessage()
	if summary == "" {
		summary = "Story implementation completed. Ready for final review."
	}
	return summary
}

// getFilesCreatedForReview extracts files created information from state for review.
func (c *Coder) getFilesCreatedForReview(sm *agent.BaseStateMachine) string {
	filesCreated := utils.GetStateValueOr[[]string](sm, KeyFilesCreated, []string{})
	if len(filesCreated) == 0 {
		return "No files created information available"
	}
	return strings.Join(filesCreated, ", ")
}

// getLastAssistantMessage gets the last assistant message from context.
func (c *Coder) getLastAssistantMessage() string {
	messages := c.contextManager.GetMessages()
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			return messages[i].Content
		}
	}
	return ""
}
