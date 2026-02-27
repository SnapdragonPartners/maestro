package coder

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/config"
	"orchestrator/pkg/effect"
	execpkg "orchestrator/pkg/exec"
	"orchestrator/pkg/forge"
	_ "orchestrator/pkg/forge/gitea"  // Auto-register Gitea client.
	_ "orchestrator/pkg/forge/github" // Auto-register GitHub client.
	"orchestrator/pkg/logx"
	"orchestrator/pkg/mirror"
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

	// === Phase 3 & 4: Merge attempt tracking and dirty state detection ===

	// Get current merge attempt counters from state
	attemptCount := utils.GetStateValueOr[int](sm, KeyMergeAttemptCount, 0)
	stuckAttempts := utils.GetStateValueOr[int](sm, KeyMergeStuckAttempts, 0)
	lastRemoteHEAD := utils.GetStateValueOr[string](sm, KeyLastRemoteHEAD, "")

	// Increment attempt counter
	attemptCount++
	sm.SetStateData(KeyMergeAttemptCount, attemptCount)

	// Get current remote HEAD to detect if world changed
	currentRemoteHEAD, headErr := c.getRemoteHEAD(ctx, targetBranch)
	if headErr != nil {
		c.logger.Warn("🔀 Failed to get remote HEAD: %v", headErr)
		// Continue with empty HEAD - we'll use absolute limit only
	}

	// Update stuck counter based on HEAD comparison
	if currentRemoteHEAD != "" && lastRemoteHEAD != "" {
		if currentRemoteHEAD == lastRemoteHEAD {
			// World hasn't changed - coder stuck on same problem
			stuckAttempts++
			c.logger.Info("🔀 Same remote HEAD - stuck attempt %d/%d", stuckAttempts, MaxStuckAttempts)
		} else {
			// World changed - reset stuck counter, give fresh chance
			stuckAttempts = 0
			c.logger.Info("🔀 Remote HEAD changed (%s -> %s) - resetting stuck counter", truncateSHA(lastRemoteHEAD), truncateSHA(currentRemoteHEAD))
		}
	}

	// Store updated counters
	sm.SetStateData(KeyMergeStuckAttempts, stuckAttempts)
	sm.SetStateData(KeyLastRemoteHEAD, currentRemoteHEAD)

	// Check iteration limits
	if stuckAttempts >= MaxStuckAttempts {
		c.logger.Error("🔀 Merge failed: stuck on same conflict for %d attempts", stuckAttempts)
		return proto.StateError, false, logx.Errorf("merge failed: coder stuck on same conflict for %d attempts without resolution", stuckAttempts)
	}
	if attemptCount >= MaxTotalAttempts {
		c.logger.Error("🔀 Merge failed: exceeded maximum %d total attempts", MaxTotalAttempts)
		return proto.StateError, false, logx.Errorf("merge failed: exceeded maximum %d total attempts", MaxTotalAttempts)
	}

	// === Phase 4: Detect workspace state on re-entry ===

	wsState, wsErr := c.detectGitWorkspaceState(ctx)
	if wsErr != nil {
		c.logger.Warn("🔀 Failed to detect git workspace state: %v", wsErr)
		// Continue anyway - we'll detect issues during operations
	}

	// Handle mid-operation states
	if wsState != nil {
		// Clear stale index lock if present
		if wsState.IndexLocked {
			c.clearStaleIndexLock()
		}

		// If in mid-rebase state, the coder resolved conflicts - try to continue
		if wsState.MidRebase && !wsState.HasConflicts {
			c.logger.Info("🔀 Detected mid-rebase state with no conflicts - attempting to continue")
			if continueErr := c.continueRebase(ctx); continueErr != nil {
				c.logger.Warn("🔀 Continue rebase failed: %v", continueErr)
				// Fall through to normal flow - it will detect the state
			} else {
				c.logger.Info("🔀 Rebase continued successfully")
				// Fall through to push
			}
		}

		// If there are conflicts, send coder back to resolve them
		if wsState.HasConflicts {
			c.logger.Info("🔀 Workspace has unresolved conflicts - returning to CODING")
			conflictInfo := &MergeConflictInfo{
				Kind:             FailureRebaseConflict,
				ConflictingFiles: wsState.ConflictingFiles,
				GitStatus:        wsState.GitStatusOutput,
				MidRebase:        wsState.MidRebase,
				AttemptNumber:    attemptCount,
				MaxAttempts:      MaxTotalAttempts,
			}
			msg := c.buildConflictResolutionMessage(conflictInfo)
			c.contextManager.AddMessage("system", msg)
			sm.SetStateData(KeyResumeInput, msg)
			return StateCoding, false, nil
		}
	}

	// Verify git is still available (should have been set up in SETUP phase)
	if gitErr := c.verifyGitAvailable(ctx); gitErr != nil {
		c.logger.Error("🔀 Git verification failed: %v", gitErr)
		c.contextManager.AddMessage("system", "Git appears to be unavailable. This will affect git operations.")
		// Continue anyway - git issues will show up during actual git operations
	}

	// Note: Code is already committed by the done tool at CODING exit.
	// Any uncommitted changes at this point are test artifacts and should not be committed.

	// Push branch to remote
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
			// Rebase didn't help - check if it's a conflict error with special handling
			c.logger.Info("🔀 Auto-rebase failed: %v", rebaseErr)

			// Check for RebaseConflictError - workspace left in mid-rebase state
			var conflictErr *RebaseConflictError
			if errors.As(rebaseErr, &conflictErr) {
				c.logger.Info("🔀 Rebase conflict - building resolution guidance for coder")

				conflictInfo := &MergeConflictInfo{
					Kind:             FailureRebaseConflict,
					ErrorOutput:      conflictErr.ErrorOutput,
					ConflictingFiles: conflictErr.ConflictingFiles,
					GitStatus:        conflictErr.GitStatus,
					MidRebase:        true,
					AttemptNumber:    attemptCount,
					MaxAttempts:      MaxTotalAttempts,
				}

				msg := c.buildConflictResolutionMessage(conflictInfo)
				c.contextManager.AddMessage("system", msg)
				sm.SetStateData(KeyResumeInput, msg)
				return StateCoding, false, nil
			}

			// Not a conflict - fall back to recoverable/unrecoverable error handling
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

// pushBranch pushes the local branch to the 'github' remote.
// SECURITY: This runs on the HOST (not in container) to prevent coders from pushing unapproved code.
// The container has no git credentials - only the host can push.
// Uses the 'github' remote (GitHub URL), not 'origin' (local mirror).
func (c *Coder) pushBranch(ctx context.Context, localBranch, remoteBranch string) error {
	c.logger.Debug("🔀 Pushing branch %s to github as %s (host-side)", localBranch, remoteBranch)

	// GITHUB_TOKEN is required for push authentication
	if !config.HasGitHubToken() {
		return fmt.Errorf("GITHUB_TOKEN not found - cannot push without authentication")
	}

	// Log push invariants for forensic debugging
	c.logPushInvariants(ctx, localBranch)

	// Create context with timeout for the push operation
	pushCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	// Run git push on the HOST (not in container) using the workspace path.
	// Push to 'github' remote (GitHub URL). 'origin' points to local mirror (RO).
	cmd := exec.CommandContext(pushCtx, "git", "push", "-u", "github", fmt.Sprintf("%s:%s", localBranch, remoteBranch))
	cmd.Dir = c.workDir

	// Inherit environment and ensure GITHUB_TOKEN is available for git credential helper
	cmd.Env = os.Environ()

	output, err := cmd.CombinedOutput()
	if err != nil {
		c.logger.Error("🔀 git push failed: %v, output: %s", err, string(output))
		return fmt.Errorf("git push failed: %w (output: %s)", err, string(output))
	}

	c.logger.Info("🔀 Branch pushed successfully (host-side, github remote)")
	return nil
}

// getOrCreatePullRequest checks if a PR exists for the branch, otherwise creates one.
// This function is mode-aware: in airplane mode it uses Gitea, otherwise GitHub.
func (c *Coder) getOrCreatePullRequest(ctx context.Context, storyID, headBranch, baseBranch string) (string, error) {
	c.logger.Debug("🔀 Checking for existing PR: %s -> %s", headBranch, baseBranch)

	// Create forge client (mode-aware: GitHub or Gitea)
	forgeClient, err := forge.NewClient(c.workDir)
	if err != nil {
		return "", fmt.Errorf("failed to create forge client: %w", err)
	}

	c.logger.Debug("🔀 Using forge provider: %s", forgeClient.Provider())

	// Create PR with meaningful title and body
	title := fmt.Sprintf("Story %s: Implementation", storyID)
	body := fmt.Sprintf("Automated pull request for story %s implementation.\n\nGenerated by maestro coder agent.", storyID)

	pr, err := forgeClient.GetOrCreatePR(ctx, forge.PRCreateOptions{
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

// RebaseConflictError represents a rebase that stopped due to conflicts.
// The workspace is left in mid-rebase state for the coder to resolve.
//
//nolint:govet // field order optimized for readability over minimal padding
type RebaseConflictError struct {
	ErrorOutput      string
	ConflictingFiles []string
	GitStatus        string
}

func (e *RebaseConflictError) Error() string {
	return fmt.Sprintf("rebase has conflicts that require manual resolution: %s", e.ErrorOutput)
}

// attemptRebaseAndRetryPush attempts to rebase the current branch onto the target branch
// and retry the push with --force-with-lease. This handles the common case in parallel
// story development where another story was merged to main while this story was being developed.
//
// Returns nil on success (rebase + push succeeded).
// Returns *RebaseConflictError if rebase has conflicts (workspace left in mid-rebase state).
// Returns other error if rebase/push fails for non-conflict reasons.
//
// IMPORTANT: On conflict, this function does NOT abort the rebase. The workspace is left
// in mid-rebase state so the coder can resolve conflicts using git commands, then use
// 'git rebase --continue' to finish. The caller should return to CODING state with
// detailed resolution instructions.
func (c *Coder) attemptRebaseAndRetryPush(ctx context.Context, localBranch, remoteBranch, targetBranch string) error {
	opts := &execpkg.Opts{
		WorkDir: c.workDir,
		Timeout: 2 * time.Minute,
		Env:     []string{},
	}

	// Step 1: Refresh mirror from GitHub (host-side) to get latest changes.
	c.logger.Debug("🔀 Refreshing mirror from GitHub before rebase")
	if err := c.refreshMirrorOnHost(ctx); err != nil {
		c.logger.Warn("🔀 Mirror refresh failed (continuing with stale mirror): %v", err)
		// Continue anyway — mirror may still have sufficient data
	}

	// Step 2: Fetch latest from origin (mirror) inside the container.
	// Now that mirror is refreshed, container can pick up changes via the RO mount.
	c.logger.Debug("🔀 Fetching latest from origin (mirror) in container")
	if _, err := c.longRunningExecutor.Run(ctx, []string{"git", "fetch", "origin", targetBranch}, opts); err != nil {
		c.logger.Warn("🔀 Container fetch from origin (mirror) failed: %v", err)
		// Continue anyway — rebase will use whatever refs are available
	}

	// Step 3: Attempt rebase onto origin/targetBranch
	c.logger.Debug("🔀 Rebasing onto origin/%s", targetBranch)
	result, err := c.longRunningExecutor.Run(ctx, []string{"git", "rebase", fmt.Sprintf("origin/%s", targetBranch)}, opts)
	if err != nil {
		// Check if this is a conflict
		if strings.Contains(strings.ToLower(result.Stderr), "conflict") ||
			strings.Contains(strings.ToLower(result.Stdout), "conflict") {
			// DO NOT abort - leave workspace in mid-rebase state for coder to resolve
			c.logger.Info("🔀 Rebase has conflicts - leaving workspace in mid-rebase state for resolution")

			// Get current conflict state for error message
			conflictFiles := c.getConflictingFiles(ctx)
			gitStatus := c.getGitStatusForError(ctx)

			return &RebaseConflictError{
				ErrorOutput:      result.Stderr,
				ConflictingFiles: conflictFiles,
				GitStatus:        gitStatus,
			}
		}
		// Other rebase failure (not conflict) - abort to leave clean state
		c.logger.Debug("🔀 Rebase failed (non-conflict), aborting")
		_, _ = c.longRunningExecutor.Run(ctx, []string{"git", "rebase", "--abort"}, opts)
		return fmt.Errorf("git rebase failed: %w (stderr: %s)", err, result.Stderr)
	}

	c.logger.Info("🔀 Rebase successful, pushing with --force-with-lease")

	// Step 4: Fetch from GitHub to get fresh tracking refs for --force-with-lease safety.
	// Without this, --force-with-lease fails with "stale info" because the local 'github'
	// tracking ref doesn't match what's actually on GitHub.
	c.logger.Debug("🔀 Fetching github/%s and github/%s for --force-with-lease safety", targetBranch, remoteBranch)
	if err := c.fetchFromGitHubOnHost(ctx, targetBranch, remoteBranch); err != nil {
		// If the remote branch doesn't exist yet, that's OK - just fetch target branch
		c.logger.Debug("🔀 Fetch of both branches failed, trying just target branch: %v", err)
		if err := c.fetchFromGitHubOnHost(ctx, targetBranch); err != nil {
			c.logger.Warn("🔀 GitHub fetch failed (continuing anyway): %v", err)
		}
	}

	// Step 5: Push with --force-with-lease (safer than --force, prevents overwriting others' work)
	// SECURITY: Run on HOST (not in container) to prevent coders from pushing unapproved code
	if err := c.pushBranchForceWithLease(ctx, localBranch, remoteBranch); err != nil {
		return err
	}

	c.logger.Info("🔀 Push with --force-with-lease succeeded")
	return nil
}

// fetchFromGitHubOnHost fetches from the 'github' remote on the HOST (not in container).
// This is required because containers don't have GITHUB_TOKEN for private repo authentication.
// Runs fetch on host where git credential helper has access to GITHUB_TOKEN.
// Used to keep 'github' tracking refs fresh for --force-with-lease safety.
func (c *Coder) fetchFromGitHubOnHost(ctx context.Context, branches ...string) error {
	c.logger.Debug("🔀 Fetching from github remote (host-side): %v", branches)

	// GITHUB_TOKEN is required for fetch authentication on private repos
	if !config.HasGitHubToken() {
		return fmt.Errorf("GITHUB_TOKEN not found - cannot fetch without authentication")
	}

	// Create context with timeout for the fetch operation
	fetchCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	// Build git fetch command with branches — uses 'github' remote (GitHub URL)
	args := []string{"fetch", "github"}
	args = append(args, branches...)

	// Run git fetch on the HOST (not in container)
	cmd := exec.CommandContext(fetchCtx, "git", args...)
	cmd.Dir = c.workDir

	// Inherit environment and ensure GITHUB_TOKEN is available for git credential helper
	cmd.Env = os.Environ()

	output, err := cmd.CombinedOutput()
	if err != nil {
		c.logger.Error("🔀 git fetch github failed: %v, output: %s", err, string(output))
		return fmt.Errorf("git fetch github failed: %w (output: %s)", err, string(output))
	}

	return nil
}

// pushBranchForceWithLease pushes the local branch to 'github' remote with --force-with-lease.
// SECURITY: This runs on the HOST (not in container) to prevent coders from pushing unapproved code.
// Uses the 'github' remote (GitHub URL), not 'origin' (local mirror).
func (c *Coder) pushBranchForceWithLease(ctx context.Context, localBranch, remoteBranch string) error {
	c.logger.Debug("🔀 Pushing branch %s with --force-with-lease to github (host-side)", localBranch)

	// GITHUB_TOKEN is required for push authentication
	if !config.HasGitHubToken() {
		return fmt.Errorf("GITHUB_TOKEN not found - cannot push without authentication")
	}

	// Log push invariants for forensic debugging
	c.logPushInvariants(ctx, localBranch)

	// Create context with timeout for the push operation
	pushCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	// Run git push on the HOST (not in container).
	// Push to 'github' remote (GitHub URL). 'origin' points to local mirror (RO).
	cmd := exec.CommandContext(pushCtx, "git", "push", "--force-with-lease", "-u", "github",
		fmt.Sprintf("%s:%s", localBranch, remoteBranch))
	cmd.Dir = c.workDir

	// Inherit environment and ensure GITHUB_TOKEN is available for git credential helper
	cmd.Env = os.Environ()

	output, err := cmd.CombinedOutput()
	if err != nil {
		c.logger.Error("🔀 git push --force-with-lease failed: %v, output: %s", err, string(output))
		return fmt.Errorf("git push --force-with-lease failed: %w (output: %s)", err, string(output))
	}

	return nil
}

// logPushInvariants logs the local HEAD, origin/<target>, and github/<target> SHAs
// before a push operation. Provides a forensic trail for debugging push safety issues.
func (c *Coder) logPushInvariants(ctx context.Context, localBranch string) {
	targetBranch, _ := c.getTargetBranch()

	// Helper to get SHA quietly
	getSHA := func(ref string) string {
		cmd := exec.CommandContext(ctx, "git", "rev-parse", "--short", ref)
		cmd.Dir = c.workDir
		out, err := cmd.Output()
		if err != nil {
			return "<unknown>"
		}
		return strings.TrimSpace(string(out))
	}

	localHEAD := getSHA("HEAD")
	originTarget := getSHA(fmt.Sprintf("origin/%s", targetBranch))
	githubTarget := getSHA(fmt.Sprintf("github/%s", targetBranch))
	githubBranch := getSHA(fmt.Sprintf("github/%s", localBranch))

	c.logger.Info("🔀 Push invariants: HEAD=%s origin/%s=%s github/%s=%s github/%s=%s",
		localHEAD, targetBranch, originTarget, targetBranch, githubTarget, localBranch, githubBranch)
}

// refreshMirrorOnHost refreshes the local mirror from GitHub (host-side).
// This ensures the mirror has the latest changes before any rebase operation.
// Includes recovery-on-failure: if refresh fails, deletes and reclones the mirror.
func (c *Coder) refreshMirrorOnHost(ctx context.Context) error {
	projectDir := config.GetProjectDir()
	if projectDir == "" {
		return fmt.Errorf("project directory not configured")
	}

	mirrorMgr := mirror.NewManager(projectDir)
	if err := mirrorMgr.RefreshFromForge(ctx); err != nil {
		c.logger.Warn("Mirror refresh failed, attempting reclone: %v", err)
		mirrorPath, pathErr := mirrorMgr.GetMirrorPath()
		if pathErr != nil {
			return fmt.Errorf("mirror recovery failed (cannot get path): %w", pathErr)
		}
		if removeErr := os.RemoveAll(mirrorPath); removeErr != nil {
			return fmt.Errorf("mirror recovery failed (cannot remove): %w", removeErr)
		}
		if _, recloneErr := mirrorMgr.EnsureMirror(ctx); recloneErr != nil {
			return fmt.Errorf("mirror recovery failed (reclone): %w", recloneErr)
		}
		c.logger.Info("Mirror recovered via reclone")
	}
	return nil
}

// truncateSHA safely truncates a git SHA to 8 characters for display.
// Returns the full string if shorter than 8 characters.
func truncateSHA(sha string) string {
	if len(sha) <= 8 {
		return sha
	}
	return sha[:8]
}
