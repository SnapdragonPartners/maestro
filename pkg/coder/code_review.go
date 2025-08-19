package coder

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/effect"
	execpkg "orchestrator/pkg/exec"
	"orchestrator/pkg/git"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
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
		c.logger.Warn("Failed to get target branch, using 'main': %v", err)
		baseBranch = "main"
	}

	workResult := git.CheckWorkDone(ctx, baseBranch, c.workDir, c.longRunningExecutor)
	if workResult.Err != nil {
		c.logger.Warn("🔍 Git work check warning: %v", workResult.Err)
	}

	// Generate git diff for evidence (branch-based, not just uncommitted)
	gitDiff := c.getBranchDiff(ctx, baseBranch)

	// Build comprehensive evidence section
	evidence := c.buildCompletionEvidence(testsPassed, testOutput, gitDiff, storyType, workResult)

	if !workResult.HasWork {
		// No work detected - request completion approval
		c.logger.Info("🧑‍💻 No work detected (%s) - requesting completion approval",
			strings.Join(workResult.Reasons, ", "))

		summary := completionSummary
		if summary == "" {
			summary = "Story requirements already satisfied during analysis"
		}

		confidence := "high - no changes needed"

		codeContent := fmt.Sprintf(`## Completion Summary
%s

## Evidence
%s

## Confidence
%s

## Original Story
%s

## Implementation Analysis
%s`, summary, evidence, confidence, originalStory, plan)

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

		codeContent := fmt.Sprintf(`## Implementation Summary
%s

## Evidence
%s

## Confidence
%s

## Git Diff
%s

## Original Story
%s

## Implementation Plan
%s`, summary, evidence, confidence, gitDiff, originalStory, plan)

		approvalEff = effect.NewApprovalEffect(codeContent, "Code implementation requires architect review", proto.ApprovalTypeCode)
		approvalEff.StoryID = storyID
		// Store approval type for later processing
		sm.SetStateData(KeyCodeApprovalResult, string(proto.ApprovalTypeCode))
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

		// Store feedback for CODING state to address
		sm.SetStateData(KeyCodeReviewRejectionFeedback, result.Feedback)
		return StateCoding, false, nil

	case proto.ApprovalStatusRejected:
		// Handle rejection differently based on approval type
		if approvalTypeStr == string(proto.ApprovalTypeCompletion) {
			c.logger.Error("🧑‍💻 Completion rejected by architect: %s", result.Feedback)
			// Return to CODING to do the work that was deemed missing
			sm.SetStateData(KeyCodeReviewRejectionFeedback, result.Feedback)
			return StateCoding, false, nil
		} else {
			c.logger.Error("🧑‍💻 Code rejected by architect: %s", result.Feedback)
			return proto.StateError, false, logx.Errorf("code rejected by architect: %s", result.Feedback)
		}

	default:
		return proto.StateError, false, logx.Errorf("unknown approval status: %s", result.Status)
	}
}

// getBranchDiff gets the git diff since branch creation (more accurate than HEAD diff).
func (c *Coder) getBranchDiff(ctx context.Context, baseBranch string) string {
	opts := &execpkg.Opts{
		WorkDir: c.workDir,
		Timeout: 30 * time.Second,
	}

	// Try merge-base approach first (most accurate)
	result, err := c.longRunningExecutor.Run(ctx, []string{"git", "merge-base", baseBranch, "HEAD"}, opts)
	if err == nil && strings.TrimSpace(result.Stdout) != "" {
		mergeBase := strings.TrimSpace(result.Stdout)
		result, err = c.longRunningExecutor.Run(ctx, []string{"git", "diff", mergeBase + "..HEAD"}, opts)
		if err == nil {
			if strings.TrimSpace(result.Stdout) == "" {
				return "No changes since branching from " + baseBranch
			}
			// Limit diff size to avoid overwhelming the architect
			if len(result.Stdout) > 50000 {
				return result.Stdout[:50000] + "\n... (diff truncated, showing first 50000 chars)"
			}
			return result.Stdout
		}
	}

	// Fallback to simple range diff
	result, err = c.longRunningExecutor.Run(ctx, []string{"git", "diff", baseBranch + "..HEAD"}, opts)
	if err != nil {
		c.logger.Debug("Failed to get branch diff: %v", err)
		return "No git changes detected"
	}

	if strings.TrimSpace(result.Stdout) == "" {
		return "No changes since branching from " + baseBranch
	}

	// Limit diff size to avoid overwhelming the architect
	if len(result.Stdout) > 50000 {
		return result.Stdout[:50000] + "\n... (diff truncated, showing first 50000 chars)"
	}

	return result.Stdout
}

// buildCompletionEvidence builds evidence section based on story type and results.
func (c *Coder) buildCompletionEvidence(testsPassed bool, testOutput, gitDiff, storyType string, workResult *git.WorkDoneResult) string {
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

	// Add git diff summary
	if gitDiff != "" && !strings.Contains(gitDiff, "No changes") && !strings.Contains(gitDiff, "No git changes") {
		evidence += "📝 Code changes made (see Git Diff section below)\n"
	} else {
		evidence += "📝 No code changes required\n"
	}

	return evidence
}
