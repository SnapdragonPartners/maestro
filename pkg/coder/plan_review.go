package coder

import (
	"context"
	"fmt"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/effect"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
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
		planContent := c.getPlanApprovalContent(sm)
		storyID := c.GetStoryID()                                               // Use the getter method I created
		eff = effect.NewPlanApprovalEffectWithStoryID(planContent, "", storyID) // Task content is now in planContent template
		// Note: Don't add assistant message here - would violate alternation after submit_plan tool result

	case proto.ApprovalTypeCompletion:
		completionContent := c.getCompletionContent(sm)
		storyID := c.GetStoryID()                                                           // Use the getter method I created
		eff = effect.NewCompletionApprovalEffectWithStoryID(completionContent, "", storyID) // Files created is now in completionContent template
		// Note: Don't add assistant message here - would violate alternation after done tool result

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
		c.logger.Info("🧑‍💻 %s needs changes, returning to appropriate state with feedback", approvalType)

		// Add feedback to context for visibility (as user role for proper alternation)
		if approvalResult.Feedback != "" {
			c.contextManager.AddMessage("user", fmt.Sprintf("Architect feedback: %s", approvalResult.Feedback))
		}

		// Return to appropriate state based on approval type
		if approvalType == proto.ApprovalTypeCompletion {
			// Completion claim rejected - return to PLANNING to re-analyze
			// Going to CODING would be a dead end since the coder determined no work was needed
			if approvalResult.Feedback != "" {
				sm.SetStateData(KeyResumeInput, fmt.Sprintf("Completion claim feedback - changes requested:\n\n%s\n\nPlease re-analyze the story and either submit a revised completion claim with better evidence, or create an implementation plan.", approvalResult.Feedback))
			}
			return StatePlanning, false, nil // Completion needs changes, go back to planning
		}
		// Plan needs changes - set resume input for planning session resume (if using Claude Code mode)
		// Note: We reuse KeyResumeInput since planning and coding never run simultaneously
		if approvalResult.Feedback != "" {
			sm.SetStateData(KeyResumeInput, fmt.Sprintf("Plan review feedback - changes requested:\n\n%s\n\nPlease revise your plan and resubmit.", approvalResult.Feedback))
		}
		return StatePlanning, false, nil // Plan needs changes, go back to planning

	case proto.ApprovalStatusRejected:
		if approvalType == proto.ApprovalTypeCompletion {
			c.logger.Error("🧑‍💻 Completion request rejected by architect: %s", approvalResult.Feedback)
			return proto.StateError, false, logx.Errorf("completion rejected by architect: %s", approvalResult.Feedback)
		} else {
			c.logger.Info("🧑‍💻 %s rejected, returning to PLANNING with feedback", approvalType)
			if approvalResult.Feedback != "" {
				c.contextManager.AddMessage("user", fmt.Sprintf("Architect feedback: %s", approvalResult.Feedback))
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
		// Regular plan approved
		c.logger.Info("🧑‍💻 Development plan approved")

		// Skip todo collection in Claude Code mode - it manages its own todos
		var nextState proto.State
		var completed bool
		if c.isClaudeCodeMode(ctx) {
			c.logger.Info("📋 Skipping todo collection in Claude Code mode (Claude Code manages its own todos)")
			nextState = StateCoding
			completed = false
		} else {
			// Standard mode - collect todos FIRST (fail-fast principle)
			c.logger.Info("📋 [TODO] Requesting todo list from LLM after plan approval")
			var err error
			nextState, completed, err = c.requestTodoList(ctx, sm)
			if err != nil {
				return proto.StateError, false, logx.Wrap(err, "failed to collect todo list")
			}
		}

		// NOW reconfigure container for coding
		c.logger.Info("🧑‍💻 Reconfiguring container for coding")

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

		return nextState, completed, nil

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

// getPlanApprovalContent generates plan approval request content using templates.
func (c *Coder) getPlanApprovalContent(sm *agent.BaseStateMachine) string {
	// Get plan content from state
	planContent := utils.GetStateValueOr[string](sm, KeyPlan, "")
	if planContent == "" {
		// Fallback to context if plan content not in state
		planContent = c.getLastAssistantMessage()
	}

	// Get task content
	taskContent := utils.GetStateValueOr[string](sm, string(stateDataKeyTaskContent), "")

	// Get knowledge pack from state
	knowledgePack := utils.GetStateValueOr[string](sm, string(stateDataKeyKnowledgePack), "")

	// Build template data
	templateData := &templates.TemplateData{
		Extra: map[string]any{
			"TaskContent":   taskContent,
			"PlanContent":   planContent,
			"KnowledgePack": knowledgePack,
		},
	}

	// Render template
	if c.renderer == nil {
		return fmt.Sprintf("## Implementation Plan\n\n%s\n\n## Task Requirements\n\n%s", planContent, taskContent)
	}

	content, err := c.renderer.Render(templates.PlanApprovalRequestTemplate, templateData)
	if err != nil {
		c.logger.Warn("Failed to render plan approval template: %v", err)
		return fmt.Sprintf("## Implementation Plan\n\n%s\n\n## Task Requirements\n\n%s", planContent, taskContent)
	}

	return content
}

// getCompletionContent generates completion request content using templates.
func (c *Coder) getCompletionContent(sm *agent.BaseStateMachine) string {
	// Get completion evidence from state (stored by processStoryCompleteDataFromEffect)
	evidence := utils.GetStateValueOr[string](sm, KeyCompletionDetails, "")

	// Get completion summary from context
	summary := c.getLastAssistantMessage()
	if summary == "" {
		summary = "Story requirements already satisfied during analysis"
	}

	// Get original story for architect convenience
	originalStory := utils.GetStateValueOr[string](sm, string(stateDataKeyTaskContent), "")

	// Build template data
	templateData := &templates.TemplateData{
		Extra: map[string]any{
			"Summary":       summary,
			"Evidence":      evidence,
			"OriginalStory": originalStory,
		},
	}

	fallback := fmt.Sprintf("## Completion Summary\n\n%s\n\n## Evidence\n\n%s\n\n## Original Story\n\n%s", summary, evidence, originalStory)

	// Render template
	if c.renderer == nil {
		return fallback
	}

	content, err := c.renderer.Render(templates.CompletionRequestTemplate, templateData)
	if err != nil {
		c.logger.Warn("Failed to render completion request template: %v", err)
		return fallback
	}

	return content
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

// Note: Todo collection functions (requestTodoList, createTodoCollectionToolProvider,
// processTodosFromEffect) have been moved to todo_collection.go for better separation of concerns.
