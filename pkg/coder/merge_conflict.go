package coder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	execpkg "orchestrator/pkg/exec"
)

// Merge attempt limits.
const (
	// MaxStuckAttempts is the maximum number of PREPARE_MERGE attempts allowed
	// when the remote HEAD hasn't changed (coder is stuck on the same conflict).
	MaxStuckAttempts = 2

	// MaxTotalAttempts is the absolute maximum number of PREPARE_MERGE attempts
	// allowed, regardless of HEAD changes (safety net for pathological cases).
	MaxTotalAttempts = 3
)

// State data keys for merge attempt tracking.
const (
	KeyMergeAttemptCount  = "merge_attempt_count"
	KeyMergeStuckAttempts = "merge_stuck_attempts"
	KeyLastRemoteHEAD     = "last_remote_head"
)

// MergeFailureKind categorizes the type of merge/push failure.
type MergeFailureKind string

// MergeFailureKind constants for categorizing different types of merge failures.
const (
	// FailureRebaseConflict indicates a rebase stopped due to conflict.
	FailureRebaseConflict MergeFailureKind = "rebase_conflict"
	// FailureMergeConflict indicates a merge stopped due to conflict.
	FailureMergeConflict MergeFailureKind = "merge_conflict"
	// FailurePushRejected indicates the remote rejected the push (non-fast-forward, etc).
	FailurePushRejected MergeFailureKind = "push_rejected"
	// FailureAuthError indicates an authentication/permission error - shouldn't count toward limit.
	FailureAuthError MergeFailureKind = "auth_error"
	// FailureUnknown indicates an unclassified error.
	FailureUnknown MergeFailureKind = "unknown"
)

// GitWorkspaceState represents the current state of the git workspace.
// Used to detect mid-operation states when re-entering PREPARE_MERGE.
//
// YAGNI Note: We only detect states Maestro actually uses (rebase, merge, index lock).
// Cherry-pick, revert, sequencer, detached HEAD can be added if needed in practice.
//
//nolint:govet // field order optimized for readability over minimal padding
type GitWorkspaceState struct {
	ConflictingFiles []string // List of files with conflicts
	GitStatusOutput  string   // Raw git status output for error messages
	MidRebase        bool     // .git/rebase-merge or .git/rebase-apply exists
	MidMerge         bool     // .git/MERGE_HEAD exists
	IndexLocked      bool     // .git/index.lock present (stale lock from crash)
	HasConflicts     bool     // Files with UU status in git status
	HasUncommitted   bool     // Modified/staged files (not in conflict)
	UnpushedCommits  bool     // Local commits not on remote
}

// MergeConflictInfo contains detailed information about a merge conflict
// for error messaging and coder guidance.
//
//nolint:govet // field order optimized for readability over minimal padding
type MergeConflictInfo struct {
	Kind             MergeFailureKind
	ErrorOutput      string   // Raw stderr from failed git command
	ConflictingFiles []string // Files that have conflicts
	GitStatus        string   // Current git status output
	AttemptNumber    int      // Current attempt number
	MaxAttempts      int      // Maximum attempts allowed
	MidRebase        bool     // Whether we're in mid-rebase state
}

// detectGitWorkspaceState examines the git workspace to determine its current state.
// This is used at the start of PREPARE_MERGE to handle re-entry scenarios.
func (c *Coder) detectGitWorkspaceState(ctx context.Context) (*GitWorkspaceState, error) {
	state := &GitWorkspaceState{}

	opts := &execpkg.Opts{
		WorkDir: c.workDir,
		Timeout: 30 * time.Second,
	}

	// Check for mid-rebase state by looking for .git/rebase-merge or .git/rebase-apply
	rebaseMergePath := filepath.Join(c.workDir, ".git", "rebase-merge")
	rebaseApplyPath := filepath.Join(c.workDir, ".git", "rebase-apply")
	if _, err := os.Stat(rebaseMergePath); err == nil {
		state.MidRebase = true
	} else if _, err := os.Stat(rebaseApplyPath); err == nil {
		state.MidRebase = true
	}

	// Check for mid-merge state by looking for .git/MERGE_HEAD
	mergeHeadPath := filepath.Join(c.workDir, ".git", "MERGE_HEAD")
	if _, err := os.Stat(mergeHeadPath); err == nil {
		state.MidMerge = true
	}

	// Check for stale index lock
	indexLockPath := filepath.Join(c.workDir, ".git", "index.lock")
	if _, err := os.Stat(indexLockPath); err == nil {
		state.IndexLocked = true
	}

	// Run git status --porcelain to get file states
	result, err := c.longRunningExecutor.Run(ctx, []string{"git", "status", "--porcelain"}, opts)
	if err != nil {
		return nil, fmt.Errorf("git status failed: %w", err)
	}

	state.GitStatusOutput = result.Stdout

	// Parse git status output
	lines := strings.Split(result.Stdout, "\n")
	for _, line := range lines {
		if len(line) < 3 {
			continue
		}
		statusCode := line[:2]
		fileName := strings.TrimSpace(line[3:])

		// UU = unmerged, both modified (conflict)
		// AA = unmerged, both added (conflict)
		// DD = unmerged, both deleted (conflict)
		// AU/UA/DU/UD = other unmerged states
		if strings.Contains(statusCode, "U") ||
			statusCode == "AA" || statusCode == "DD" {
			state.HasConflicts = true
			state.ConflictingFiles = append(state.ConflictingFiles, fileName)
		} else if statusCode != "??" && statusCode != "!!" {
			// Any other status (M, A, D, R, C) means uncommitted changes
			state.HasUncommitted = true
		}
	}

	return state, nil
}

// getRemoteHEAD fetches and returns the current SHA of the remote target branch.
func (c *Coder) getRemoteHEAD(ctx context.Context, targetBranch string) (string, error) {
	opts := &execpkg.Opts{
		WorkDir: c.workDir,
		Timeout: 30 * time.Second,
	}

	// First fetch to ensure we have latest refs
	// SECURITY: Run fetch on HOST (not in container) because containers don't have GITHUB_TOKEN.
	_ = c.fetchFromOriginOnHost(ctx, targetBranch)

	// Get the SHA of origin/targetBranch
	result, err := c.longRunningExecutor.Run(ctx, []string{
		"git", "rev-parse", fmt.Sprintf("origin/%s", targetBranch),
	}, opts)
	if err != nil {
		return "", fmt.Errorf("failed to get remote HEAD: %w", err)
	}

	return strings.TrimSpace(result.Stdout), nil
}

// getConflictingFiles returns a list of files currently in conflict state.
// Returns empty slice if no conflicts or if command fails.
func (c *Coder) getConflictingFiles(ctx context.Context) []string {
	opts := &execpkg.Opts{
		WorkDir: c.workDir,
		Timeout: 30 * time.Second,
	}

	// git diff --name-only --diff-filter=U shows unmerged (conflicting) files
	result, err := c.longRunningExecutor.Run(ctx, []string{
		"git", "diff", "--name-only", "--diff-filter=U",
	}, opts)
	if err != nil {
		// If no conflicts, this command may return empty or error
		return nil
	}

	var files []string
	for _, line := range strings.Split(result.Stdout, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}
	return files
}

// getGitStatusForError returns a formatted git status output for error messages.
func (c *Coder) getGitStatusForError(ctx context.Context) string {
	opts := &execpkg.Opts{
		WorkDir: c.workDir,
		Timeout: 30 * time.Second,
	}

	result, err := c.longRunningExecutor.Run(ctx, []string{"git", "status"}, opts)
	if err != nil {
		return fmt.Sprintf("(failed to get git status: %v)", err)
	}

	// Truncate if too long
	output := result.Stdout
	const maxLen = 2000
	if len(output) > maxLen {
		output = output[:maxLen] + "\n... (truncated)"
	}
	return output
}

// clearStaleIndexLock removes a stale .git/index.lock file if no git process is running.
// Returns true if lock was cleared, false if it should be left alone.
func (c *Coder) clearStaleIndexLock() bool {
	indexLockPath := filepath.Join(c.workDir, ".git", "index.lock")

	// Check if lock exists
	if _, err := os.Stat(indexLockPath); os.IsNotExist(err) {
		return false
	}

	// Try to remove it - if a git process is using it, this will fail
	// which is the safe behavior
	if err := os.Remove(indexLockPath); err != nil {
		c.logger.Warn("🔀 Could not remove stale index.lock: %v", err)
		return false
	}

	c.logger.Info("🔀 Cleared stale .git/index.lock")
	return true
}

// continueRebase attempts to continue a paused rebase operation.
func (c *Coder) continueRebase(ctx context.Context) error {
	opts := &execpkg.Opts{
		WorkDir: c.workDir,
		Timeout: 2 * time.Minute,
	}

	result, err := c.longRunningExecutor.Run(ctx, []string{"git", "rebase", "--continue"}, opts)
	if err != nil {
		return fmt.Errorf("git rebase --continue failed: %w (stderr: %s)", err, result.Stderr)
	}
	return nil
}

// buildConflictResolutionMessage creates a detailed message for the coder
// explaining the conflict and how to resolve it.
func (c *Coder) buildConflictResolutionMessage(info *MergeConflictInfo) string {
	var sb strings.Builder

	sb.WriteString("## Git Operation Failed\n\n")

	// Failure type
	switch info.Kind {
	case FailureRebaseConflict:
		sb.WriteString("**Type:** Rebase conflict - your changes conflict with recent changes on the target branch.\n\n")
	case FailureMergeConflict:
		sb.WriteString("**Type:** Merge conflict - your changes conflict with the target branch.\n\n")
	case FailurePushRejected:
		sb.WriteString("**Type:** Push rejected - the remote branch has changed since you started.\n\n")
	case FailureAuthError:
		sb.WriteString("**Type:** Authentication error - this is an infrastructure issue, not a code problem.\n\n")
	default:
		sb.WriteString("**Type:** Unknown git error.\n\n")
	}

	// Error output
	sb.WriteString("**Error Output:**\n```\n")
	sb.WriteString(info.ErrorOutput)
	sb.WriteString("\n```\n\n")

	// Conflicting files
	if len(info.ConflictingFiles) > 0 {
		sb.WriteString("**Conflicting Files:**\n")
		for _, f := range info.ConflictingFiles {
			sb.WriteString(fmt.Sprintf("- `%s`\n", f))
		}
		sb.WriteString("\n")
	}

	// Git status
	if info.GitStatus != "" {
		sb.WriteString("**Current Git Status:**\n```\n")
		sb.WriteString(info.GitStatus)
		sb.WriteString("\n```\n\n")
	}

	// Resolution instructions
	sb.WriteString("## How to Resolve\n\n")
	sb.WriteString("You have access to git via the `shell` tool. Use these commands:\n\n")

	if info.MidRebase {
		sb.WriteString("**You are in the middle of a rebase.** To resolve:\n\n")
		sb.WriteString("1. View conflicts: `git status`\n")
		sb.WriteString("2. For each conflicting file, edit it to resolve the conflict markers (`<<<<<<<`, `=======`, `>>>>>>>`)\n")
		sb.WriteString("3. After editing, stage resolved files: `git add <filename>`\n")
		sb.WriteString("4. Continue the rebase: `git rebase --continue`\n")
		sb.WriteString("5. If you get stuck, you can abort with: `git rebase --abort`\n\n")
		sb.WriteString("**For binary files:** Use `git checkout --ours <file>` (keep yours) or `git checkout --theirs <file>` (keep theirs)\n\n")
	} else {
		sb.WriteString("1. View current state: `git status`\n")
		sb.WriteString("2. Fetch latest: `git fetch origin main`\n")
		sb.WriteString("3. Rebase onto main: `git rebase origin/main`\n")
		sb.WriteString("4. Resolve any conflicts that appear\n")
		sb.WriteString("5. Stage resolved files: `git add <filename>`\n")
		sb.WriteString("6. Continue: `git rebase --continue`\n\n")
	}

	sb.WriteString("Once resolved, use the `done` tool to retry the merge.\n\n")

	// Attempt counter
	sb.WriteString(fmt.Sprintf("**Attempt %d of %d** - ", info.AttemptNumber, info.MaxAttempts))
	if info.AttemptNumber >= info.MaxAttempts {
		sb.WriteString("This is your last attempt. If you cannot resolve the conflict, use `ask_question` to escalate.\n")
	} else {
		sb.WriteString("If you cannot resolve the conflict after trying, use `ask_question` to escalate to the architect.\n")
	}

	return sb.String()
}
