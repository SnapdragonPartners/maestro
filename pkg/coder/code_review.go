package coder

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/tools"
	"orchestrator/pkg/utils"
)

// handleCodeReview processes the CODE_REVIEW state - blocks waiting for architect's RESULT response.
func (c *Coder) handleCodeReview(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	// State: waiting for architect approval

	// Check if we already have approval result from previous processing.
	if approvalData, exists := sm.GetStateValue(string(stateDataKeyCodeApprovalResult)); exists {
		return c.handleCodeReviewApproval(ctx, sm, approvalData)
	}

	// Send approval request if we haven't already sent one
	if c.pendingApprovalRequest == nil {
		if err := c.sendCodeReviewRequest(ctx, sm); err != nil {
			return proto.StateError, false, logx.Wrap(err, "failed to send code review request")
		}
	}

	// Block waiting for RESULT message from architect.
	return c.handleRequestBlocking(ctx, sm, stateDataKeyCodeApprovalResult, StateCodeReview)
}

// sendCodeReviewRequest sends an approval request to the architect for code review.
func (c *Coder) sendCodeReviewRequest(ctx context.Context, sm *agent.BaseStateMachine) error {
	// Generate git diff to check if any files actually changed.
	gitDiff, err := c.generateGitDiff(ctx, sm)
	if err != nil {
		c.logger.Warn("Failed to generate git diff, proceeding with normal code review: %v", err)
		gitDiff = "" // Continue with normal flow if diff fails
	}

	// Tests passed, send REQUEST message to architect for approval.
	filesCreated, _ := sm.GetStateValue(KeyFilesCreated)

	// Check if we have any actual changes to review.
	if gitDiff == "" || strings.TrimSpace(gitDiff) == "" {
		// No changes - send completion approval instead of code approval.
		c.logger.Info("🧑‍💻 No file changes detected, requesting story completion approval")

		codeContent := fmt.Sprintf("Story completed during implementation phase: %v files processed, tests passed, no changes needed", filesCreated)

		c.pendingApprovalRequest = &ApprovalRequest{
			ID:      proto.GenerateApprovalID(),
			Content: codeContent,
			Reason:  "Story requirements already satisfied, requesting completion approval",
			Type:    proto.ApprovalTypeCompletion,
		}
	} else {
		// Normal code approval with diff included.
		c.logger.Info("🧑‍💻 File changes detected, requesting code review approval")

		// Get original story and plan from state data
		originalStory := utils.GetStateValueOr[string](sm, string(stateDataKeyTaskContent), "")
		plan := utils.GetStateValueOr[string](sm, KeyPlan, "")

		codeContent := fmt.Sprintf("Code implementation and testing completed: %v files created, tests passed\n\nOriginal Story:\n%s\n\nImplementation Plan:\n%s\n\nChanges:\n%s", filesCreated, originalStory, plan, gitDiff)

		c.pendingApprovalRequest = &ApprovalRequest{
			ID:      proto.GenerateApprovalID(),
			Content: codeContent,
			Reason:  "Code requires architect approval before completion",
			Type:    proto.ApprovalTypeCode,
		}
	}

	if c.dispatcher != nil {
		requestMsg := proto.NewAgentMsg(proto.MsgTypeREQUEST, c.GetID(), "architect")
		requestMsg.SetPayload(proto.KeyKind, string(proto.RequestKindApproval))
		requestMsg.SetPayload("approval_type", c.pendingApprovalRequest.Type.String())
		requestMsg.SetPayload("content", c.pendingApprovalRequest.Content)
		requestMsg.SetPayload("reason", c.pendingApprovalRequest.Reason)
		requestMsg.SetPayload("approval_id", c.pendingApprovalRequest.ID)

		if err := c.dispatcher.DispatchMessage(requestMsg); err != nil {
			return logx.Wrap(err, "failed to send approval request")
		}

		c.logger.Info("🧑‍💻 Sent %s approval request %s to architect during CODE_REVIEW state entry", c.pendingApprovalRequest.Type, c.pendingApprovalRequest.ID)
	} else {
		return logx.Errorf("dispatcher not set")
	}

	return nil
}

// handleCodeReviewApproval processes code review approval results.
func (c *Coder) handleCodeReviewApproval(ctx context.Context, sm *agent.BaseStateMachine, approvalData any) (proto.State, bool, error) {
	result, err := convertApprovalData(approvalData)
	if err != nil {
		return proto.StateError, false, logx.Wrap(err, "failed to convert approval data")
	}

	// Store result and clear.
	sm.SetStateData(string(stateDataKeyCodeApprovalResult), nil)
	sm.SetStateData(KeyCodeReviewCompletedAt, time.Now().UTC())

	// Store approval type before clearing the request.
	approvalType := c.pendingApprovalRequest.Type
	c.pendingApprovalRequest = nil // Clear since we have the result

	// Handle approval based on original request type.
	switch result.Status {
	case proto.ApprovalStatusApproved:
		// Check what TYPE of approval this was.
		if approvalType == proto.ApprovalTypeCompletion {
			// Completion approved - skip directly to DONE, no merge needed.
			c.logger.Info("🧑‍💻 Story completion approved by architect")

			// Optionally: Clean up empty development branch.
			if err := c.cleanupEmptyBranch(ctx, sm); err != nil {
				c.logger.Warn("Failed to cleanup empty branch: %v", err)
			}

			return proto.StateDone, true, nil
		} else {
			// Normal code approval - proceed with merge flow.
			c.logger.Info("🧑‍💻 Code approved, pushing branch and creating PR")

			// AR-104: Push branch and open pull request.
			if err := c.pushBranchAndCreatePR(ctx, sm); err != nil {
				c.logger.Error("Failed to push branch and create PR: %v", err)
				return proto.StateError, false, err
			}

			// Send merge REQUEST to architect instead of going to DONE.
			if err := c.sendMergeRequest(ctx, sm); err != nil {
				c.logger.Error("Failed to send merge request: %v", err)
				return proto.StateError, false, err
			}

			c.logger.Info("🧑‍💻 Waiting for merge approval from architect")
			return StateAwaitMerge, false, nil
		}
	case proto.ApprovalStatusRejected, proto.ApprovalStatusNeedsChanges:
		c.logger.Info("🧑‍💻 Code rejected/needs changes, transitioning to CODING for fixes")
		// Store review feedback for CODING state to prioritize.
		sm.SetStateData(KeyCodeReviewRejectionFeedback, result.Feedback)
		sm.SetStateData(KeyCodingMode, "review_fix")
		return StateCoding, false, nil
	default:
		return proto.StateError, false, logx.Errorf("unknown approval status: %s", result.Status)
	}
}

// generateGitDiff generates a git diff showing changes made to the story branch.
func (c *Coder) generateGitDiff(ctx context.Context, _ *agent.BaseStateMachine) (string, error) {
	// Create shell tool for git operations
	tool := tools.NewShellTool(c.longRunningExecutor)

	// Get the main branch name for comparison (usually 'main' or 'master').
	mainBranch := "main" // Could make this configurable if needed

	// Generate diff between current branch and main branch.
	args := map[string]any{
		"cmd": "git diff " + mainBranch + "..HEAD",
		"cwd": c.workDir,
	}

	result, err := tool.Exec(ctx, args)
	if err != nil {
		// Try 'master' if 'main' doesn't exist.
		args["cmd"] = "git diff master..HEAD"
		result, err = tool.Exec(ctx, args)
		if err != nil {
			return "", logx.Wrap(err, "failed to generate git diff")
		}
	}

	// Extract stdout from result.
	if resultMap, ok := result.(map[string]any); ok {
		if stdout, ok := resultMap["stdout"].(string); ok {
			return stdout, nil
		}
	}

	return "", logx.Errorf("unexpected result format from shell tool")
}

// convertApprovalData converts various approval data formats to ApprovalResult.
func convertApprovalData(data any) (*proto.ApprovalResult, error) {
	// If data is nil or empty, return error indicating no approval data.
	if data == nil {
		return nil, logx.Errorf("no approval data available")
	}

	// If it's already the correct type, return it.
	if result, ok := data.(*proto.ApprovalResult); ok {
		return result, nil
	}

	// If it's a map (from JSON deserialization), convert it.
	if dataMap, ok := data.(map[string]any); ok {
		// Convert map to JSON and then to struct.
		jsonData, err := json.Marshal(dataMap)
		if err != nil {
			return nil, logx.Wrap(err, "failed to marshal approval data")
		}

		var result proto.ApprovalResult
		if err := json.Unmarshal(jsonData, &result); err != nil {
			return nil, logx.Wrap(err, "failed to unmarshal approval data")
		}

		return &result, nil
	}

	// If it's a string (from cleanup or serialization), handle appropriately.
	if str, ok := data.(string); ok {
		// Empty string means no approval result (from cleanup).
		if str == "" {
			return nil, logx.Errorf("no approval data available")
		}
		// Non-empty string might be JSON-serialized approval result.
		var result proto.ApprovalResult
		if err := json.Unmarshal([]byte(str), &result); err != nil {
			return nil, logx.Wrap(err, "failed to unmarshal approval data from string")
		}
		return &result, nil
	}

	return nil, logx.Errorf("unsupported approval data type: %T", data)
}

// cleanupEmptyBranch cleans up an empty development branch after completion approval.
func (c *Coder) cleanupEmptyBranch(ctx context.Context, sm *agent.BaseStateMachine) error {
	// Create shell tool for git operations
	tool := tools.NewShellTool(c.longRunningExecutor)

	// Get the current branch name.
	branchName, exists := sm.GetStateValue(KeyBranchName)
	if !exists {
		c.logger.Debug("No branch name stored, skipping branch cleanup")
		return nil
	}

	branchNameStr, ok := branchName.(string)
	if !ok || branchNameStr == "" {
		c.logger.Debug("Invalid branch name, skipping branch cleanup")
		return nil
	}

	c.logger.Info("🧹 Cleaning up empty development branch: %s", branchNameStr)

	// Switch back to main branch first.
	args := map[string]any{
		"cmd": "git checkout main",
		"cwd": c.workDir,
	}

	_, err := tool.Exec(ctx, args)
	if err != nil {
		// Try master if main doesn't exist.
		args["cmd"] = "git checkout master"
		_, err = tool.Exec(ctx, args)
		if err != nil {
			return logx.Wrap(err, "failed to checkout main/master branch")
		}
	}

	// Delete the development branch.
	args["cmd"] = "git branch -D " + branchNameStr
	_, err = tool.Exec(ctx, args)
	if err != nil {
		return logx.Wrap(err, fmt.Sprintf("failed to delete branch %s", branchNameStr))
	}

	c.logger.Info("🧹 Successfully cleaned up empty branch: %s", branchNameStr)
	return nil
}

// pushBranchAndCreatePR implements AR-104: Push branch & open pull request.
func (c *Coder) pushBranchAndCreatePR(ctx context.Context, sm *agent.BaseStateMachine) error {
	// Get worktree path and story ID.
	worktreePath, exists := sm.GetStateValue(KeyWorktreePath)
	if !exists || worktreePath == "" {
		c.logger.Warn("No worktree path found, skipping branch push and PR creation")
		return nil // Not an error - just skip for backward compatibility
	}

	worktreePathStr, ok := worktreePath.(string)
	if !ok {
		return logx.Errorf("worktree_path is not a string: %v", worktreePath)
	}

	storyID, exists := sm.GetStateValue(KeyStoryID)
	if !exists || storyID == nil {
		return logx.Errorf("no story_id found in state data")
	}

	storyIDStr, ok := storyID.(string)
	if !ok {
		return logx.Errorf("story_id is not a string in pushBranchAndCreatePR: %v (type: %T)", storyID, storyID)
	}

	// Use the actual branch name that was created (which may be different due to collisions).
	actualBranchName, exists := sm.GetStateValue(KeyActualBranchName)
	if !exists || actualBranchName == "" {
		// Fallback to generating the branch name if not found.
		actualBranchName = fmt.Sprintf("story-%s", storyIDStr)
		c.logger.Warn("actual_branch_name not found in state, using fallback: %s", actualBranchName)
	}

	branchName, ok := actualBranchName.(string)
	if !ok {
		branchName = fmt.Sprintf("story-%s", storyIDStr)
		c.logger.Warn("actual_branch_name is not a string, using fallback: %s", branchName)
	}

	agentID := c.BaseStateMachine.GetAgentID()

	c.logger.Info("Pushing branch %s for story %s", branchName, storyIDStr)

	// Step 1: Commit all changes before pushing.
	commitCtx, commitCancel := context.WithTimeout(ctx, 1*time.Minute)
	defer commitCancel()

	// Add all files to staging.
	addCmd := exec.CommandContext(commitCtx, "git", "add", ".")
	addCmd.Dir = worktreePathStr
	addOutput, err := addCmd.CombinedOutput()
	if err != nil {
		return logx.Errorf("failed to stage changes: %w\nOutput: %s", err, string(addOutput))
	}
	c.logger.Info("Staged all changes for commit")

	// Check if there are any changes to commit.
	statusCmd := exec.CommandContext(commitCtx, "git", "status", "--porcelain")
	statusCmd.Dir = worktreePathStr
	statusOutput, err := statusCmd.CombinedOutput()
	if err != nil {
		return logx.Errorf("failed to check git status: %w\nOutput: %s", err, string(statusOutput))
	}

	if strings.TrimSpace(string(statusOutput)) == "" {
		c.logger.Info("No changes to commit for story %s", storyIDStr)
		return nil // No changes, skip push and PR creation
	}

	// Commit changes.
	commitMsg := fmt.Sprintf("Implement story %s\n\n🤖 Generated by Maestro AI", storyIDStr)
	commitCmd := exec.CommandContext(commitCtx, "git", "commit", "-m", commitMsg)
	commitCmd.Dir = worktreePathStr
	commitOutput, err := commitCmd.CombinedOutput()
	if err != nil {
		return logx.Errorf("failed to commit changes: %w\nOutput: %s", err, string(commitOutput))
	}
	c.logger.Info("Committed changes for story %s", storyIDStr)

	// Step 2: Push branch via SSH.
	pushCtx, pushCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer pushCancel()

	pushCmd := exec.CommandContext(pushCtx, "git", "push", "-u", KeyOrigin, branchName)
	pushCmd.Dir = worktreePathStr

	pushOutput, err := pushCmd.CombinedOutput()
	if err != nil {
		return logx.Errorf("failed to push branch %s: %w\nOutput: %s", branchName, err, string(pushOutput))
	}

	c.logger.Info("Successfully pushed branch %s", branchName)
	sm.SetStateData(KeyBranchPushed, true)
	sm.SetStateData(KeyPushedBranch, branchName)

	// Step 3: Create PR if GITHUB_TOKEN is available.
	if githubToken := os.Getenv("GITHUB_TOKEN"); githubToken != "" {
		c.logger.Info("GITHUB_TOKEN found, creating pull request")

		prURL, err := c.createPullRequest(ctx, worktreePathStr, branchName, storyIDStr, agentID)
		if err != nil {
			// Log error but don't fail the push - PR creation is optional.
			c.logger.Error("Failed to create pull request: %v", err)
			sm.SetStateData(KeyPRCreationError, err.Error())
		} else {
			c.logger.Info("Successfully created pull request: %s", prURL)
			sm.SetStateData(KeyPRURL, prURL)
			sm.SetStateData(KeyPRCreated, true)

			// TODO: Post PR URL back to architect agent via message
			c.logger.Info("🧑‍💻 Pull request created for story %s: %s", storyIDStr, prURL)
		}
	} else {
		c.logger.Info("No GITHUB_TOKEN found, skipping automatic PR creation")
		sm.SetStateData(KeyPRSkipped, "no_github_token")
	}

	return nil
}

// createPullRequest uses gh CLI to create a pull request.
func (c *Coder) createPullRequest(ctx context.Context, worktreePath, branchName, storyID, agentID string) (string, error) {
	prCtx, cancel := context.WithTimeout(ctx, 1*time.Minute)
	defer cancel()

	// Build PR title and body.
	title := fmt.Sprintf("Story #%s: generated by agent %s", storyID, agentID)

	// Get base branch from config (default: main).
	baseBranch := "main" // TODO: Get from workspace manager config

	// Check if gh is available.
	if _, err := exec.LookPath("gh"); err != nil {
		return "", logx.Wrap(err, "gh (GitHub CLI) is not available in PATH")
	}

	// Check if GITHUB_TOKEN is set.
	if os.Getenv("GITHUB_TOKEN") == "" {
		return "", logx.Errorf("GITHUB_TOKEN environment variable is not set")
	}

	// Create PR using gh CLI.
	prCmd := exec.CommandContext(prCtx, "gh", "pr", "create",
		"--title", title,
		"--body", fmt.Sprintf("Automated pull request for story %s generated by agent %s", storyID, agentID),
		"--base", baseBranch,
		"--head", branchName)
	prCmd.Dir = worktreePath

	prOutput, err := prCmd.CombinedOutput()
	if err != nil {
		return "", logx.Errorf("gh pr create failed: %w\nOutput: %s", err, string(prOutput))
	}

	// Extract PR URL from output (gh returns the PR URL).
	prURL := strings.TrimSpace(string(prOutput))
	return prURL, nil
}

// sendMergeRequest sends a merge request to the architect for PR merging.
func (c *Coder) sendMergeRequest(_ context.Context, sm *agent.BaseStateMachine) error {
	storyID, _ := sm.GetStateValue(KeyStoryID)
	prURL, _ := sm.GetStateValue(KeyPRURL)
	branchName, _ := sm.GetStateValue(KeyPushedBranch)

	// Convert to strings safely.
	storyIDStr, _ := storyID.(string)
	prURLStr, _ := prURL.(string)
	branchNameStr, _ := branchName.(string)

	// Log the state of PR creation for debugging.
	if prCreated := utils.GetStateValueOr[bool](sm, KeyPRCreated, false); prCreated {
		c.logger.Info("🧑‍💻 Sending merge request to architect for story %s with PR: %s", storyIDStr, prURLStr)
	} else {
		c.logger.Info("🧑‍💻 Sending merge request to architect for story %s with branch: %s (PR creation failed or skipped)", storyIDStr, branchNameStr)
		if prError, exists := sm.GetStateValue(KeyPRCreationError); exists {
			c.logger.Warn("🧑‍💻 PR creation error: %v", prError)
		}
	}

	requestMsg := proto.NewAgentMsg(proto.MsgTypeREQUEST, c.GetID(), "architect")
	requestMsg.SetPayload("request_type", "merge")
	requestMsg.SetPayload(KeyPRURL, prURLStr)
	requestMsg.SetPayload(KeyBranchName, branchNameStr)
	requestMsg.SetPayload(KeyStoryID, storyIDStr)

	if err := c.dispatcher.DispatchMessage(requestMsg); err != nil {
		return fmt.Errorf("failed to dispatch merge request: %w", err)
	}
	return nil
}
