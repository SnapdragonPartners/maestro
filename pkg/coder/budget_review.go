package coder

import (
	"context"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/effect"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
	"orchestrator/pkg/utils"
)

// handleBudgetReview processes the BUDGET_REVIEW state - executes stored BudgetReviewEffect.
func (c *Coder) handleBudgetReview(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	logx.DebugState(ctx, "coder", "enter", string(StateBudgetReview))

	// Check for stored budget review effect to execute
	if effectData, exists := sm.GetStateValue("budget_review_effect"); exists {
		if budgetReviewEff, ok := effectData.(*effect.BudgetReviewEffect); ok {
			c.logger.Info("🧑‍💻 Executing stored BudgetReviewEffect")

			// Execute the effect - this will send REQUEST and wait for RESULT
			result, err := c.ExecuteEffect(ctx, budgetReviewEff)
			if err != nil {
				return proto.StateError, false, logx.Wrap(err, "failed to execute budget review effect")
			}

			// Clear the stored effect
			sm.SetStateData("budget_review_effect", nil)

			// Process the result
			if budgetReviewResult, ok := result.(*effect.BudgetReviewResult); ok {
				return c.processBudgetReviewResult(ctx, sm, budgetReviewResult)
			} else {
				return proto.StateError, false, logx.Errorf("invalid budget review result type: %T", result)
			}
		}
	}

	// No stored BudgetReviewEffect found - this indicates a bug in the calling code
	return proto.StateError, false, logx.Errorf("no BudgetReviewEffect found in state data - BUDGET_REVIEW state should only be reached via Effects pattern")
}

// processBudgetReviewResult processes BudgetReviewResult from Effects pattern.
func (c *Coder) processBudgetReviewResult(_ context.Context, sm *agent.BaseStateMachine, result *effect.BudgetReviewResult) (proto.State, bool, error) {
	// Use shared budget review processing logic
	return c.processBudgetReviewStatus(sm, result.Status, result.Feedback)
}

// processBudgetReviewStatus handles budget review status processing logic.
func (c *Coder) processBudgetReviewStatus(sm *agent.BaseStateMachine, status proto.ApprovalStatus, feedback string) (proto.State, bool, error) {
	// Store result and clear.
	sm.SetStateData(KeyBudgetReviewCompletedAt, time.Now().UTC())
	c.pendingApprovalRequest = nil // Clear since we have the result

	// Get origin state from stored data.
	originStr := utils.GetStateValueOr[string](sm, KeyOrigin, "")

	switch status {
	case proto.ApprovalStatusApproved:
		// CONTINUE/PIVOT - return to origin state and reset counter.
		c.logger.Info("🧑‍💻 Budget review approved, returning to origin state: %s", originStr)

		// Add architect's approval response to context to maintain conversation flow
		if feedback != "" {
			// Use mini-template to format the feedback message
			if c.renderer != nil {
				renderedMessage, err := c.renderer.RenderSimple(templates.BudgetReviewFeedbackTemplate, feedback)
				if err != nil {
					c.logger.Error("Failed to render budget review feedback: %v", err)
					// Fallback to simple message
					renderedMessage = "The architect has approved my request to continue. I'll proceed with the work as planned."
				}
				c.contextManager.AddAssistantMessage(renderedMessage)
				c.logger.Debug("🧑‍💻 Injected architect approval response into context")
			}
		} else {
			// Default approval message when no specific feedback provided
			c.contextManager.AddAssistantMessage("The architect has approved my request to continue. I'll proceed with the work as planned.")
			c.logger.Debug("🧑‍💻 Added default approval response to context")
		}

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
		// NEEDS_CHANGES - context-aware transition based on origin state
		if originStr == string(StateCoding) {
			// From CODING: return to CODING with guidance (not a plan issue, just execution issue)
			c.logger.Info("🧑‍💻 Budget review needs changes from CODING, continuing CODING with feedback")
			// Reset coding counter and inject feedback for coding improvements
			sm.SetStateData(string(stateDataKeyCodingIterations), 0)
			if feedback != "" {
				// Use mini-template to format the feedback message
				if c.renderer != nil {
					renderedMessage, err := c.renderer.RenderSimple(templates.BudgetReviewFeedbackTemplate, feedback)
					if err != nil {
						c.logger.Error("Failed to render budget review feedback: %v", err)
						// Fallback to simple message
						renderedMessage = "I understand the architect's guidance. Let me adjust my coding approach: " + feedback + "\n\nI'll continue with the implementation using this guidance."
					}
					c.contextManager.AddAssistantMessage(renderedMessage)
					c.logger.Debug("🧑‍💻 Injected architect feedback into context for coding")
				}
			}
			return StateCoding, false, nil
		} else {
			// From PLANNING or other states: PIVOT - return to PLANNING
			c.logger.Info("🧑‍💻 Budget review needs changes from %s, pivoting to PLANNING with feedback", originStr)
			// Reset both iteration counters since we're starting over with new guidance
			sm.SetStateData(string(stateDataKeyPlanningIterations), 0)
			sm.SetStateData(string(stateDataKeyCodingIterations), 0)
			// Inject architect feedback into context to guide next attempt
			if feedback != "" {
				// Use mini-template to format the feedback message
				if c.renderer != nil {
					renderedMessage, err := c.renderer.RenderSimple(templates.BudgetReviewFeedbackTemplate, feedback)
					if err != nil {
						c.logger.Error("Failed to render budget review feedback: %v", err)
						// Fallback to simple message
						renderedMessage = "I understand the architect's feedback. Let me correct my approach: " + feedback + "\n\nI'll now focus on the proper planning approach as guided."
					}
					c.contextManager.AddAssistantMessage(renderedMessage)
					c.logger.Debug("🧑‍💻 Injected architect feedback into context for planning")
				}
			}
			return StatePlanning, false, nil
		}
	case proto.ApprovalStatusRejected:
		// ABANDON - move to ERROR.
		c.logger.Info("🧑‍💻 Budget review rejected, abandoning task")
		return proto.StateError, false, logx.Errorf("task abandoned by architect after budget review")
	default:
		return proto.StateError, false, logx.Errorf("unknown budget review approval status: %s", status)
	}
}
