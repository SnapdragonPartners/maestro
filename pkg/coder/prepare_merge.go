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
	"orchestrator/pkg/github"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
	"orchestrator/pkg/utils"
)

// handlePrepareMerge processes the PREPARE_MERGE state - commits changes, pushes branch, creates PR, and sends merge request.
//
//nolint:unparam // bool return is part of state machine interface
func (c *Coder) handlePrepareMerge(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	// Get story information and branch details from state
	storyID := utils.GetStateValueOr[string](sm, KeyStoryID, "")
	localBranch := utils.GetStateValueOr[string](sm, KeyLocalBranchName, "")
	remoteBranch := utils.GetStateValueOr[string](sm, KeyRemoteBranchName, "")

	if storyID == "" {
		return proto.StateError, false, logx.Errorf("no story ID found in PREPARE_MERGE state")
	}
	if localBranch == "" {
		return proto.StateError, false, logx.Errorf("no local branch name found in PREPARE_MERGE state")
	}
	if remoteBranch == "" {
		return proto.StateError, false, logx.Errorf("no remote branch name found in PREPARE_MERGE state")
	}

	c.logger.Info("🔀 Preparing merge for story %s: local=%s, remote=%s", storyID, localBranch, remoteBranch)

	// Get target branch from config
	targetBranch, err := c.getTargetBranch()
	if err != nil {
		c.logger.Warn("Failed to get target branch from config, using 'main': %v", err)
		targetBranch = "main"
	}

	// Verify GitHub authentication is still working (should have been set up in SETUP phase)
	if authErr := c.verifyGitHubAuthSetup(ctx); authErr != nil {
		c.logger.Error("🔀 GitHub authentication verification failed: %v", authErr)
		c.contextManager.AddMessage("system", "GitHub authentication appears to be broken. This may affect git push operations.")
		// Continue anyway - authentication issues will show up during actual git operations
	}

	// Step 1: Commit all changes
	if commitErr := c.commitChanges(ctx, storyID); commitErr != nil {
		if c.isRecoverableGitError(commitErr) {
			c.logger.Info("🔀 Git commit failed (recoverable), returning to CODING: %v", commitErr)
			commitFailureMsg := fmt.Sprintf("Git commit failed. Fix the following issues and try again: %s", commitErr.Error())
			if renderedMessage, renderErr := c.renderer.RenderSimple(templates.GitCommitFailureTemplate, commitErr.Error()); renderErr != nil {
				c.logger.Error("Failed to render git commit failure message: %v", renderErr)
				c.contextManager.AddMessage("system", commitFailureMsg)
			} else {
				c.contextManager.AddMessage("system", renderedMessage)
				commitFailureMsg = renderedMessage
			}
			// Set resume input for Claude Code mode
			sm.SetStateData(KeyResumeInput, commitFailureMsg)
			return StateCoding, false, nil
		}
		c.logger.Error("🔀 Git commit failed (unrecoverable): %v", commitErr)
		return proto.StateError, false, logx.Wrap(commitErr, "git commit failed")
	}

	// Step 2: Push branch to remote
	if pushErr := c.pushBranch(ctx, localBranch, remoteBranch); pushErr != nil {
		// Always attempt auto-rebase first on any push failure.
		// This handles non-fast-forward errors robustly without brittle string matching.
		// Worst case: rebase fails and we fall through to normal error handling.
		c.logger.Info("🔀 Push failed - attempting auto-rebase onto %s", targetBranch)
		if rebaseErr := c.attemptRebaseAndRetryPush(ctx, localBranch, remoteBranch, targetBranch); rebaseErr == nil {
			// Rebase and push succeeded - run tests to verify rebased code still works
			c.logger.Info("🔀 Auto-rebase succeeded, running post-rebase tests")

			// Run tests to verify rebased code
			if c.buildService != nil {
				passed, output, testErr := c.runTestWithBuildService(ctx, c.workDir)
				if testErr != nil {
					c.logger.Warn("🔀 Post-rebase tests failed with error: %v", testErr)
					testFailureMsg := fmt.Sprintf("Tests failed after rebase:\n\n%s\n\nPlease fix the issues and try again.", testErr.Error())
					sm.SetStateData(KeyResumeInput, testFailureMsg)
					return StateCoding, false, nil
				}
				if !passed {
					c.logger.Warn("🔀 Post-rebase tests failed")
					testFailureMsg := fmt.Sprintf("Tests failed after rebase:\n\n%s\n\nPlease fix the issues and try again.", truncateOutput(output))
					sm.SetStateData(KeyResumeInput, testFailureMsg)
					return StateCoding, false, nil
				}
				c.logger.Info("🔀 Post-rebase tests passed, continuing to PR creation")
			} else {
				c.logger.Warn("🔀 No build service available, skipping post-rebase tests")
			}
			// Rebase succeeded, continue to PR creation below
		} else {
			// Rebase didn't help - fall back to recoverable/unrecoverable error handling
			c.logger.Info("🔀 Auto-rebase failed: %v", rebaseErr)

			if c.isRecoverableGitError(pushErr) {
				c.logger.Info("🔀 Git push failed (recoverable), returning to CODING: %v", pushErr)
				pushFailureMsg := fmt.Sprintf("Git push failed. Fix the following issues and try again: %s\n\nAuto-rebase also failed: %s", pushErr.Error(), rebaseErr.Error())
				if renderedMessage, renderErr := c.renderer.RenderSimple(templates.GitPushFailureTemplate, pushErr.Error()); renderErr != nil {
					c.logger.Error("Failed to render git push failure message: %v", renderErr)
					c.contextManager.AddMessage("system", pushFailureMsg)
				} else {
					c.contextManager.AddMessage("system", renderedMessage)
					pushFailureMsg = renderedMessage
				}
				// Set resume input for Claude Code mode
				sm.SetStateData(KeyResumeInput, pushFailureMsg)
				return StateCoding, false, nil
			}
			c.logger.Error("🔀 Git push failed (unrecoverable): %v", pushErr)
			return proto.StateError, false, logx.Wrap(pushErr, "git push failed")
		}
	}

	// Step 3: Get existing PR or create new one using GitHub CLI
	prURL, err := c.getOrCreatePullRequest(ctx, storyID, remoteBranch, targetBranch)
	if err != nil {
		// Special handling for "No commits between" error - indicates work detection mismatch
		if strings.Contains(err.Error(), "No commits between") {
			c.logger.Info("🔀 No commits detected by GitHub - advising coder to verify work")
			noCommitsMsg := "GitHub reports 'No commits between branches' but work was detected earlier. " +
				"If this is incorrect and no work was actually needed, use the 'done' tool to " +
				"signal completion. Otherwise, make a small change (like adding a comment) to " +
				"ensure commits are present."
			c.contextManager.AddMessage("system", noCommitsMsg)
			// Set resume input for Claude Code mode
			sm.SetStateData(KeyResumeInput, noCommitsMsg)
			return StateCoding, false, nil
		}

		if c.isRecoverableGitError(err) {
			c.logger.Info("🔀 PR creation failed (recoverable), returning to CODING: %v", err)
			prFailureMsg := fmt.Sprintf("Pull request creation failed. Fix the following issues and try again: %s", err.Error())
			if renderedMessage, renderErr := c.renderer.RenderSimple(templates.PRCreationFailureTemplate, err.Error()); renderErr != nil {
				c.logger.Error("Failed to render PR creation failure message: %v", renderErr)
				c.contextManager.AddMessage("system", prFailureMsg)
			} else {
				c.contextManager.AddMessage("system", renderedMessage)
				prFailureMsg = renderedMessage
			}
			// Set resume input for Claude Code mode
			sm.SetStateData(KeyResumeInput, prFailureMsg)
			return StateCoding, false, nil
		}
		c.logger.Error("🔀 PR creation failed (unrecoverable): %v", err)
		return proto.StateError, false, logx.Wrap(err, "PR creation failed")
	}

	// Store PR URL in state for AWAIT_MERGE
	sm.SetStateData(KeyPRURL, prURL)
	sm.SetStateData(KeyPrepareMergeCompletedAt, time.Now().UTC())

	c.logger.Info("🔀 PR created successfully: %s", prURL)

	// Step 4: Send merge request to architect
	mergeEff := effect.NewMergeEffect(storyID, prURL, remoteBranch)

	// Execute merge effect - blocks until architect responds or times out
	result, err := c.ExecuteEffect(ctx, mergeEff)
	if err != nil {
		c.logger.Error("🔀 Merge request failed: %v", err)
		return proto.StateError, false, logx.Wrap(err, "merge request failed")
	}

	// Store the merge result for AWAIT_MERGE to process
	sm.SetStateData(KeyMergeResult, result)

	c.logger.Info("🔀 Merge request sent successfully, transitioning to AWAIT_MERGE")
	return StateAwaitMerge, false, nil
}

// getTargetBranch retrieves the target branch from global config.
func (c *Coder) getTargetBranch() (string, error) {
	cfg, err := config.GetConfig()
	if err != nil {
		// Return default when config is not available
		return config.DefaultTargetBranch, fmt.Errorf("failed to get config, using default: %w", err)
	}

	targetBranch := cfg.Git.TargetBranch
	if targetBranch == "" {
		targetBranch = config.DefaultTargetBranch // "main"
	}

	return targetBranch, nil
}

// commitChanges commits all current changes to git.
func (c *Coder) commitChanges(ctx context.Context, storyID string) error {
	c.logger.Debug("🔀 Committing changes for story %s", storyID)

	opts := &execpkg.Opts{
		WorkDir: c.workDir,
		Timeout: 30 * time.Second,
	}

	// Add all changes to staging area
	result, err := c.longRunningExecutor.Run(ctx, []string{"git", "add", "-A"}, opts)
	if err != nil {
		c.logger.Error("🔀 git add failed: %v, output: %s", err, result.Stderr)
		return fmt.Errorf("git add failed: %w", err)
	}

	// Check if there are any changes to commit
	result, err = c.longRunningExecutor.Run(ctx, []string{"git", "diff", "--cached", "--exit-code"}, opts)
	if err == nil {
		// No changes staged for commit
		c.logger.Info("🔀 No changes to commit")
		return nil
	}

	// Commit with meaningful message
	commitMsg := fmt.Sprintf("Story %s: Implementation complete\n\nAutomated commit by maestro coder agent", storyID)
	result, err = c.longRunningExecutor.Run(ctx, []string{"git", "commit", "-m", commitMsg}, opts)
	if err != nil {
		c.logger.Error("🔀 git commit failed: %v, output: %s", err, result.Stderr)
		return fmt.Errorf("git commit failed: %w", err)
	}

	c.logger.Info("🔀 Changes committed successfully")
	return nil
}

// pushBranch pushes the local branch to remote origin.
func (c *Coder) pushBranch(ctx context.Context, localBranch, remoteBranch string) error {
	c.logger.Debug("🔀 Pushing branch %s to origin as %s", localBranch, remoteBranch)

	opts := &execpkg.Opts{
		WorkDir: c.workDir,
		Timeout: 2 * time.Minute, // Give enough time for potentially large pushes
		Env:     []string{},      // Initialize environment variables slice
	}

	// Add GITHUB_TOKEN for authentication
	if config.HasGitHubToken() {
		opts.Env = append(opts.Env, "GITHUB_TOKEN")
		c.logger.Debug("🔀 Added GITHUB_TOKEN to git push environment")
	} else {
		c.logger.Warn("🔀 GITHUB_TOKEN not found - git push may fail with authentication error")
	}

	// git push -u origin localBranch:remoteBranch
	result, err := c.longRunningExecutor.Run(ctx, []string{"git", "push", "-u", "origin", fmt.Sprintf("%s:%s", localBranch, remoteBranch)}, opts)
	if err != nil {
		c.logger.Error("🔀 git push failed: %v, output: %s", err, result.Stderr)
		return fmt.Errorf("git push failed: %w", err)
	}

	c.logger.Info("🔀 Branch pushed successfully")
	return nil
}

// getOrCreatePullRequest checks if a PR exists for the branch, otherwise creates one.
func (c *Coder) getOrCreatePullRequest(ctx context.Context, storyID, headBranch, baseBranch string) (string, error) {
	c.logger.Debug("🔀 Checking for existing PR: %s -> %s", headBranch, baseBranch)

	// Create GitHub client from config (pure API calls, no workdir needed)
	cfg, err := config.GetConfig()
	if err != nil {
		return "", fmt.Errorf("failed to get config: %w", err)
	}
	if cfg.Git == nil || cfg.Git.RepoURL == "" {
		return "", fmt.Errorf("git repo_url not configured")
	}

	ghClient, err := github.NewClientFromRemote(cfg.Git.RepoURL)
	if err != nil {
		return "", fmt.Errorf("failed to create GitHub client: %w", err)
	}

	// Use longer timeout for PR operations
	ghClient = ghClient.WithTimeout(2 * time.Minute)

	// Create PR with meaningful title and body
	title := fmt.Sprintf("Story %s: Implementation", storyID)
	body := fmt.Sprintf("Automated pull request for story %s implementation.\n\nGenerated by maestro coder agent.", storyID)

	pr, err := ghClient.GetOrCreatePR(ctx, github.PRCreateOptions{
		Title: title,
		Body:  body,
		Head:  headBranch,
		Base:  baseBranch,
	})
	if err != nil {
		return "", fmt.Errorf("PR creation failed: %w", err)
	}

	if pr.URL == "" {
		return "", fmt.Errorf("PR created but no URL returned")
	}

	c.logger.Info("🔀 PR ready: %s", pr.URL)
	return pr.URL, nil
}

// isRecoverableGitError determines if a git error is recoverable (should return to CODING) or unrecoverable (ERROR).
func (c *Coder) isRecoverableGitError(err error) bool {
	if err == nil {
		return false
	}

	errorStr := strings.ToLower(err.Error())

	// Check unrecoverable errors first - fundamental system issues
	unrecoverablePatterns := []string{
		"not a git repository",
		"gh: command not found",
		"git: command not found",
		"fatal: not a git repository",
		"no such file or directory",
	}

	for _, pattern := range unrecoverablePatterns {
		if strings.Contains(errorStr, pattern) {
			return false
		}
	}

	// Recoverable errors - issues the coder can potentially fix
	recoverablePatterns := []string{
		"nothing to commit",
		"working tree clean",
		"merge conflict",
		"conflict",
		"permission denied",
		"authentication failed",
		"network",
		"timeout",
		"connection",
		"already exists",
		"branch",
		"not found", // This should be checked after "command not found"
		"rejected",
		"non-fast-forward",
		"pull request already exists",
		"required status check",
		"not mergeable",
	}

	for _, pattern := range recoverablePatterns {
		if strings.Contains(errorStr, pattern) {
			return true
		}
	}

	// Default to recoverable for unknown errors - safer to allow retry
	return true
}

// attemptRebaseAndRetryPush attempts to rebase the current branch onto the target branch
// and retry the push with --force-with-lease. This handles the common case in parallel
// story development where another story was merged to main while this story was being developed.
//
// Returns nil on success (rebase + push succeeded).
// Returns error if rebase has conflicts or push still fails.
func (c *Coder) attemptRebaseAndRetryPush(ctx context.Context, localBranch, remoteBranch, targetBranch string) error {
	opts := &execpkg.Opts{
		WorkDir: c.workDir,
		Timeout: 2 * time.Minute,
		Env:     []string{},
	}

	// Add GITHUB_TOKEN for fetch operations
	if config.HasGitHubToken() {
		opts.Env = append(opts.Env, "GITHUB_TOKEN")
	}

	// Step 1: Fetch latest from origin (both target branch and our remote branch)
	// We need to fetch the remote branch too so --force-with-lease has an up-to-date reference.
	// Without this, --force-with-lease fails with "stale info" because the local tracking ref
	// doesn't match what's actually on the remote.
	c.logger.Debug("🔀 Fetching latest from origin/%s and origin/%s", targetBranch, remoteBranch)
	result, err := c.longRunningExecutor.Run(ctx, []string{"git", "fetch", "origin", targetBranch, remoteBranch}, opts)
	if err != nil {
		// If the remote branch doesn't exist yet, that's OK - just fetch target branch
		c.logger.Debug("🔀 Fetch of both branches failed, trying just target branch: %v", err)
		result, err = c.longRunningExecutor.Run(ctx, []string{"git", "fetch", "origin", targetBranch}, opts)
		if err != nil {
			return fmt.Errorf("git fetch failed: %w (stderr: %s)", err, result.Stderr)
		}
	}

	// Step 2: Attempt rebase onto origin/targetBranch
	c.logger.Debug("🔀 Rebasing onto origin/%s", targetBranch)
	result, err = c.longRunningExecutor.Run(ctx, []string{"git", "rebase", fmt.Sprintf("origin/%s", targetBranch)}, opts)
	if err != nil {
		// Check if this is a conflict
		if strings.Contains(strings.ToLower(result.Stderr), "conflict") ||
			strings.Contains(strings.ToLower(result.Stdout), "conflict") {
			// Abort the rebase to leave workspace in clean state
			c.logger.Debug("🔀 Rebase has conflicts, aborting")
			_, _ = c.longRunningExecutor.Run(ctx, []string{"git", "rebase", "--abort"}, opts)
			return fmt.Errorf("rebase has conflicts that require manual resolution: %s", result.Stderr)
		}
		// Other rebase failure - also abort
		c.logger.Debug("🔀 Rebase failed, aborting")
		_, _ = c.longRunningExecutor.Run(ctx, []string{"git", "rebase", "--abort"}, opts)
		return fmt.Errorf("git rebase failed: %w (stderr: %s)", err, result.Stderr)
	}

	c.logger.Info("🔀 Rebase successful, pushing with --force-with-lease")

	// Step 3: Push with --force-with-lease (safer than --force, prevents overwriting others' work)
	result, err = c.longRunningExecutor.Run(ctx, []string{
		"git", "push", "--force-with-lease", "-u", "origin",
		fmt.Sprintf("%s:%s", localBranch, remoteBranch),
	}, opts)
	if err != nil {
		return fmt.Errorf("git push --force-with-lease failed: %w (stderr: %s)", err, result.Stderr)
	}

	c.logger.Info("🔀 Push with --force-with-lease succeeded")
	return nil
}
