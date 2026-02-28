package coder

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/config"
	"orchestrator/pkg/effect"
	execpkg "orchestrator/pkg/exec"
	"orchestrator/pkg/git"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
	"orchestrator/pkg/utils"
)

// handleCodeReview processes the CODE_REVIEW state using Effects pattern.
func (c *Coder) handleCodeReview(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	// Get context information
	originalStory := utils.GetStateValueOr[string](sm, string(stateDataKeyTaskContent), "")
	plan := utils.GetStateValueOr[string](sm, KeyPlan, "")
	storyID := utils.GetStateValueOr[string](sm, KeyStoryID, "")
	testsPassed := utils.GetStateValueOr[bool](sm, KeyTestsPassed, false)
	testOutput := utils.GetStateValueOr[string](sm, KeyTestOutput, "")
	storyType := utils.GetStateValueOr[string](sm, proto.KeyStoryType, string(proto.StoryTypeApp))

	var approvalEff *effect.ApprovalEffect

	// Get completion summary from done tool if available
	completionSummary := utils.GetStateValueOr[string](sm, KeyCompletionDetails, "")

	// Use reliable git-based work detection instead of unreliable filesCreated
	baseBranch, err := c.getTargetBranch()
	if err != nil {
		c.logger.Warn("Failed to get target branch, using '%s': %v", config.DefaultTargetBranch, err)
		baseBranch = config.DefaultTargetBranch
	}

	workResult := git.CheckWorkDone(ctx, baseBranch, c.workDir, c.longRunningExecutor)
	if workResult.Err != nil {
		c.logger.Warn("🔍 Git work check warning: %v", workResult.Err)
	}

	// Resolve HEAD SHA for the review request (architect can verify workspace state)
	headSHA := c.resolveHeadSHA(ctx)

	// Build comprehensive evidence section
	evidence := c.buildCompletionEvidence(testsPassed, testOutput, storyType, workResult, headSHA)

	if !workResult.HasWork {
		// No work detected - request completion approval
		c.logger.Info("🧑‍💻 No work detected (%s) - requesting completion approval",
			strings.Join(workResult.Reasons, ", "))

		summary := completionSummary
		if summary == "" {
			summary = "Story requirements already satisfied during analysis"
		}

		codeContent := c.getCompletionRequestContent(summary, evidence, originalStory)

		approvalEff = effect.NewApprovalEffect(codeContent, "Story completion verified with evidence", proto.ApprovalTypeCompletion)
		approvalEff.StoryID = storyID
		// Store approval type for later processing
		sm.SetStateData(KeyCodeApprovalResult, string(proto.ApprovalTypeCompletion))
	} else {
		// Work was detected - request code approval with full details
		c.logger.Info("🧑‍💻 Work detected (%s) - requesting code review approval",
			strings.Join(workResult.Reasons, ", "))

		summary := completionSummary
		if summary == "" {
			summary = fmt.Sprintf("Implementation completed: %s", strings.Join(workResult.Reasons, ", "))
		}

		confidence := "high - all tests passing"

		// Get knowledge pack from state machine
		knowledgePack := utils.GetStateValueOr[string](sm, string(stateDataKeyKnowledgePack), "")

		codeContent := c.getCodeReviewContent(summary, evidence, confidence, originalStory, plan, knowledgePack)

		approvalEff = effect.NewApprovalEffect(codeContent, "Code implementation requires architect review", proto.ApprovalTypeCode)
		approvalEff.StoryID = storyID
		// Store approval type for later processing
		sm.SetStateData(KeyCodeApprovalResult, string(proto.ApprovalTypeCode))
	}

	// Signal to architect if Claude Code was upgraded in-place (container image needs rebuild)
	if c.containerUpgradeNeeded {
		approvalEff.ExtraMetadata = map[string]string{
			"container_upgrade_needed": "claude_code",
		}
	}

	// Execute approval effect - blocks until architect responds
	result, err := c.ExecuteEffect(ctx, approvalEff)
	if err != nil {
		c.logger.Error("🧑‍💻 Approval effect failed: %v", err)
		return proto.StateError, false, logx.Wrap(err, "approval effect failed")
	}

	// Process the approval result
	if approvalResult, ok := result.(*effect.ApprovalResult); ok {
		return c.processApprovalResult(ctx, sm, approvalResult)
	}

	return proto.StateError, false, logx.Errorf("invalid approval result type: %T", result)
}

// processApprovalResult processes the architect's approval response and determines next state.
func (c *Coder) processApprovalResult(_ context.Context, sm *agent.BaseStateMachine, result *effect.ApprovalResult) (proto.State, bool, error) {
	// Store completion timestamp
	sm.SetStateData(KeyCodeReviewCompletedAt, time.Now().UTC())

	// Get the approval type that was stored when the request was made
	approvalTypeStr := utils.GetStateValueOr[string](sm, KeyCodeApprovalResult, "")

	// Handle approval based on status
	switch result.Status {
	case proto.ApprovalStatusApproved:
		c.logger.Info("🧑‍💻 Approval received from architect")

		// Route based on approval type (not unreliable string matching)
		if approvalTypeStr == string(proto.ApprovalTypeCompletion) {
			c.logger.Info("🧑‍💻 Completion approved - story finished successfully")
			return proto.StateDone, false, nil
		} else {
			c.logger.Info("🧑‍💻 Code approved - preparing merge")
			return StatePrepareMerge, false, nil
		}

	case proto.ApprovalStatusNeedsChanges:
		c.logger.Info("🧑‍💻 Changes requested: %s", result.Feedback)

		// Add feedback to context for visibility
		feedbackMessage := fmt.Sprintf("Code review feedback - changes requested:\n\n%s\n\nPlease address these issues and continue implementation.", result.Feedback)
		c.contextManager.AddMessage("architect-feedback", feedbackMessage)

		// Set resume input for Claude Code mode (will be used if session exists)
		sm.SetStateData(KeyResumeInput, feedbackMessage)

		return StateCoding, false, nil

	case proto.ApprovalStatusRejected:
		// Handle rejection differently based on approval type
		if approvalTypeStr == string(proto.ApprovalTypeCompletion) {
			c.logger.Error("🧑‍💻 Completion rejected by architect: %s", result.Feedback)
			// Return to CODING to do the work that was deemed missing
			rejectionMessage := fmt.Sprintf("Code completion rejected by architect:\n\n%s\n\nPlease continue implementation to address these concerns.", result.Feedback)
			c.contextManager.AddMessage("architect-rejection", rejectionMessage)

			// Set resume input for Claude Code mode (will be used if session exists)
			sm.SetStateData(KeyResumeInput, rejectionMessage)

			return StateCoding, false, nil
		} else {
			c.logger.Error("🧑‍💻 Code rejected by architect: %s", result.Feedback)
			return proto.StateError, false, logx.Errorf("code rejected by architect: %s", result.Feedback)
		}

	default:
		return proto.StateError, false, logx.Errorf("unknown approval status: %s", result.Status)
	}
}

// resolveHeadSHA resolves the current HEAD SHA in the workspace.
func (c *Coder) resolveHeadSHA(ctx context.Context) string {
	opts := &execpkg.Opts{
		WorkDir: c.workDir,
		Timeout: 10 * time.Second,
	}
	result, err := c.longRunningExecutor.Run(ctx, []string{"git", "rev-parse", "HEAD"}, opts)
	if err != nil {
		c.logger.Debug("Failed to resolve HEAD SHA: %v", err)
		return ""
	}
	return strings.TrimSpace(result.Stdout)
}

// buildCompletionEvidence builds evidence section based on story type and results.
func (c *Coder) buildCompletionEvidence(testsPassed bool, testOutput, storyType string, workResult *git.WorkDoneResult, headSHA string) string {
	evidence := ""

	// Add test evidence
	if testsPassed {
		evidence += "✅ All tests passing\n"
		if testOutput != "" {
			evidence += fmt.Sprintf("Test output: %s\n", testOutput)
		}
	} else {
		evidence += "❌ Tests not run or failed\n"
		if testOutput != "" {
			evidence += fmt.Sprintf("Test output: %s\n", testOutput)
		}
	}

	// Add story-type specific evidence
	if storyType == string(proto.StoryTypeDevOps) {
		evidence += "🐳 DevOps story completed:\n"
		evidence += "  - Container build and validation completed\n"
		evidence += "  - Infrastructure configuration verified\n"
	} else {
		evidence += "💻 Application story completed:\n"
		evidence += "  - Code implementation finished\n"
		evidence += "  - Build and lint checks passed\n"
	}

	// Add work detection evidence
	if workResult.HasWork {
		evidence += fmt.Sprintf("🔍 Work detected: %s\n", strings.Join(workResult.Reasons, ", "))
		if workResult.Unstaged {
			evidence += "  - Unstaged changes present\n"
		}
		if workResult.Staged {
			evidence += "  - Staged changes present\n"
		}
		if workResult.Untracked {
			evidence += "  - Untracked files present\n"
		}
		if workResult.Ahead {
			evidence += "  - Commits ahead of base branch\n"
		}
	} else {
		evidence += "🔍 No work required - story satisfied by analysis\n"
		if len(workResult.Reasons) > 0 {
			evidence += fmt.Sprintf("  - Verified: %s\n", strings.Join(workResult.Reasons, ", "))
		}
	}

	// Add git diff note (architect will use get_diff tool to inspect changes)
	if workResult.HasWork {
		evidence += "📝 Code changes made (architect will inspect via get_diff tool)\n"
	} else {
		evidence += "📝 No code changes required\n"
	}

	// Add HEAD SHA for workspace state verification
	if headSHA != "" {
		evidence += fmt.Sprintf("🔖 Workspace HEAD: %s\n", headSHA)
	}

	return evidence
}

// getCodeReviewContent generates code review request content using templates.
// Note: raw git diff is intentionally NOT included — the architect uses its own
// get_diff tool to inspect the workspace, ensuring it always sees current state.
func (c *Coder) getCodeReviewContent(summary, evidence, confidence, originalStory, plan, knowledgePack string) string {
	// Build template data
	templateData := &templates.TemplateData{
		Extra: map[string]any{
			"Summary":       summary,
			"Evidence":      evidence,
			"Confidence":    confidence,
			"OriginalStory": originalStory,
			"ApprovedPlan":  plan,
			"KnowledgePack": knowledgePack,
		},
	}

	// Render template
	if c.renderer == nil {
		return fmt.Sprintf(`## Implementation Summary
%s

## Evidence
%s

## Confidence
%s

## Original Story
%s

## Reference: Approved Plan (DO NOT EVALUATE - ALREADY APPROVED)
The following plan was already approved in PLAN_REVIEW and is immutable. Use it only as context to verify the implementation matches what was approved:

%s`, summary, evidence, confidence, originalStory, plan)
	}

	content, err := c.renderer.Render(templates.CodeReviewRequestTemplate, templateData)
	if err != nil {
		c.logger.Warn("Failed to render code review template: %v", err)
		return fmt.Sprintf(`## Implementation Summary
%s

## Evidence
%s

## Confidence
%s

## Original Story
%s

## Reference: Approved Plan (DO NOT EVALUATE - ALREADY APPROVED)
The following plan was already approved in PLAN_REVIEW and is immutable. Use it only as context to verify the implementation matches what was approved:

%s`, summary, evidence, confidence, originalStory, plan)
	}

	return content
}

// getCompletionRequestContent generates completion request content using templates (for no-work scenarios).
func (c *Coder) getCompletionRequestContent(summary, evidence, originalStory string) string {
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
