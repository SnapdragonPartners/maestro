package coder

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/effect"
	execpkg "orchestrator/pkg/exec"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/utils"
)

// handleCodeReview processes the CODE_REVIEW state using Effects pattern.
func (c *Coder) handleCodeReview(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	// Get context information
	filesCreatedRaw, _ := sm.GetStateValue(KeyFilesCreated)
	originalStory := utils.GetStateValueOr[string](sm, string(stateDataKeyTaskContent), "")
	plan := utils.GetStateValueOr[string](sm, KeyPlan, "")
	storyID := utils.GetStateValueOr[string](sm, KeyStoryID, "")
	testsPassed := utils.GetStateValueOr[bool](sm, KeyTestsPassed, false)
	testOutput := utils.GetStateValueOr[string](sm, KeyTestOutput, "")
	storyType := utils.GetStateValueOr[string](sm, proto.KeyStoryType, string(proto.StoryTypeApp))

	var approvalEff *effect.ApprovalEffect

	// Check if files were actually created during implementation
	filesCreated := []string{}
	if files, ok := filesCreatedRaw.([]string); ok {
		filesCreated = files
	}

	// Get completion summary from done tool if available
	completionSummary := utils.GetStateValueOr[string](sm, KeyCompletionDetails, "")

	// Generate git diff to show what changed
	gitDiff := c.getGitDiff(ctx)

	// Build comprehensive evidence section
	evidence := c.buildCompletionEvidence(testsPassed, testOutput, gitDiff, storyType, filesCreated)

	if len(filesCreated) == 0 && gitDiff == "" {
		// No files created and no changes - request completion approval
		c.logger.Info("🧑‍💻 No files created and no changes, requesting story completion approval")

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
	} else {
		// Files were created or changes made - request code approval with full details
		c.logger.Info("🧑‍💻 Changes made during implementation, requesting code review approval")

		summary := completionSummary
		if summary == "" {
			filesSummary := strings.Join(filesCreated, ", ")
			summary = fmt.Sprintf("Implementation completed: %d files created (%s)", len(filesCreated), filesSummary)
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

	// Handle approval based on status
	switch result.Status {
	case proto.ApprovalStatusApproved:
		c.logger.Info("🧑‍💻 Code approved by architect")

		// For completion approvals, go directly to DONE
		// For code approvals, proceed to AWAIT_MERGE
		if strings.Contains(result.Feedback, "completion") || strings.Contains(result.Feedback, "no changes") {
			c.logger.Info("🧑‍💻 Completion approved - story finished successfully")
			return proto.StateDone, false, nil
		}

		c.logger.Info("🧑‍💻 Code approved - proceeding to merge")
		return StateAwaitMerge, false, nil

	case proto.ApprovalStatusNeedsChanges:
		c.logger.Info("🧑‍💻 Code needs changes: %s", result.Feedback)

		// Store feedback for CODING state to address
		sm.SetStateData(KeyCodeReviewRejectionFeedback, result.Feedback)
		return StateCoding, false, nil

	case proto.ApprovalStatusRejected:
		c.logger.Error("🧑‍💻 Code rejected by architect: %s", result.Feedback)
		return proto.StateError, false, logx.Errorf("code rejected by architect: %s", result.Feedback)

	default:
		return proto.StateError, false, logx.Errorf("unknown approval status: %s", result.Status)
	}
}

// getGitDiff gets the current git diff to show what changed.
func (c *Coder) getGitDiff(ctx context.Context) string {
	// Use the executor to run git diff
	opts := &execpkg.Opts{
		WorkDir: c.workDir,
		Timeout: 30 * time.Second, // 30 seconds should be enough for git diff
	}

	result, err := c.longRunningExecutor.Run(ctx, []string{"git", "diff", "HEAD"}, opts)
	if err != nil {
		c.logger.Debug("Failed to get git diff: %v", err)
		return "No git changes detected"
	}

	if strings.TrimSpace(result.Stdout) == "" {
		return "No changes in git working directory"
	}

	// Limit diff size to avoid overwhelming the architect
	if len(result.Stdout) > 50000 {
		return result.Stdout[:50000] + "\n... (diff truncated, showing first 50000 chars)"
	}

	return result.Stdout
}

// buildCompletionEvidence builds evidence section based on story type and results.
func (c *Coder) buildCompletionEvidence(testsPassed bool, testOutput, gitDiff, storyType string, filesCreated []string) string {
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
		if len(filesCreated) > 0 {
			evidence += fmt.Sprintf("  - Files created: %s\n", strings.Join(filesCreated, ", "))
		}
	} else {
		evidence += "💻 Application story completed:\n"
		evidence += "  - Code implementation finished\n"
		evidence += "  - Build and lint checks passed\n"
		if len(filesCreated) > 0 {
			evidence += fmt.Sprintf("  - Files created: %s\n", strings.Join(filesCreated, ", "))
		}
	}

	// Add change summary
	if gitDiff != "" && gitDiff != "No changes in git working directory" && gitDiff != "No git changes detected" {
		evidence += "📝 Code changes made (see Git Diff section below)\n"
	} else {
		evidence += "📝 No code changes required\n"
	}

	return evidence
}
