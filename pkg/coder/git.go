package coder

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"orchestrator/pkg/logx"
)

// GitRunner provides an interface for running Git commands with dependency injection support
type GitRunner interface {
	// Run executes a Git command in the specified directory
	// Returns stdout+stderr combined output and any error
	Run(ctx context.Context, dir string, args ...string) ([]byte, error)
}

// DefaultGitRunner implements GitRunner using the system git command
type DefaultGitRunner struct {
	logger *logx.Logger
}

// NewDefaultGitRunner creates a new DefaultGitRunner
func NewDefaultGitRunner() *DefaultGitRunner {
	return &DefaultGitRunner{
		logger: logx.NewLogger("git"),
	}
}

// Run executes a Git command using exec.CommandContext
func (g *DefaultGitRunner) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}

	// Log the command being executed
	logDir := dir
	if logDir == "" {
		logDir = "."
	}
	g.logger.Debug("Executing Git command: cd %s && git %s", logDir, strings.Join(args, " "))

	// Combine stdout and stderr to capture all git output
	output, err := cmd.CombinedOutput()

	// Add context to error for better debugging
	if err != nil {
		g.logger.Error("Git command failed: %v", err)
		g.logger.Debug("Git command output: %s", string(output))
		return output, fmt.Errorf("git %s failed in %s: %w\nOutput: %s",
			strings.Join(args, " "), dir, err, string(output))
	}

	g.logger.Debug("Git command succeeded: %s", string(output))
	return output, nil
}

// WorkspaceManager handles Git worktree operations for coder agents
type WorkspaceManager struct {
	gitRunner       GitRunner
	projectWorkDir  string // Project work directory (shared across all agents) - contains mirrors and worktrees
	repoURL         string
	baseBranch      string
	mirrorDir       string // Mirror directory relative to projectWorkDir (e.g., ".mirrors")
	branchPattern   string // Pattern for branch names (e.g., "story-{STORY_ID}")
	worktreePattern string // Pattern for worktree paths relative to agentWorkDir (e.g., "{STORY_ID}")
	logger          *logx.Logger
}

// NewWorkspaceManager creates a new workspace manager
// projectWorkDir is the root work directory for the entire orchestrator run (shared across agents)
func NewWorkspaceManager(gitRunner GitRunner, projectWorkDir, repoURL, baseBranch, mirrorDir, branchPattern, worktreePattern string) *WorkspaceManager {
	// Convert projectWorkDir to absolute path at construction time
	absProjectWorkDir, err := filepath.Abs(projectWorkDir)
	if err != nil {
		// If we can't get absolute path, use the original (fallback)
		absProjectWorkDir = projectWorkDir
	}

	return &WorkspaceManager{
		gitRunner:       gitRunner,
		projectWorkDir:  absProjectWorkDir,
		repoURL:         repoURL,
		baseBranch:      baseBranch,
		mirrorDir:       mirrorDir,
		branchPattern:   branchPattern,
		worktreePattern: worktreePattern,
		logger:          logx.NewLogger("workspace"),
	}
}

// WorkspaceResult contains the results of workspace setup
type WorkspaceResult struct {
	WorkDir    string
	BranchName string
}

// SetupWorkspace implements the AR-102 workspace initialization logic
func (w *WorkspaceManager) SetupWorkspace(ctx context.Context, agentID, storyID, agentWorkDir string) (*WorkspaceResult, error) {
	w.logger.Debug("SetupWorkspace called with agentID=%s, storyID=%s, agentWorkDir=%s", agentID, storyID, agentWorkDir)
	w.logger.Debug("ProjectWorkDir: %s", w.projectWorkDir)

	// Step 1: Ensure mirror clone exists and is up to date
	mirrorPath, err := w.ensureMirrorClone(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to setup mirror clone: %w", err)
	}
	w.logger.Debug("Mirror path: %s", mirrorPath)

	// Step 1.5: Occasionally clean up old worktrees (housekeeping)
	// Run cleanup every 10th story to avoid overhead
	if strings.HasSuffix(storyID, "0") {
		w.logger.Debug("Running worktree cleanup for story %s", storyID)
		w.cleanupWorktrees(ctx, mirrorPath)
	}

	// Step 2: Create story work directory path
	storyWorkDir := w.BuildStoryWorkDir(agentID, storyID, agentWorkDir)
	w.logger.Debug("Story work directory: %s", storyWorkDir)
	if err := os.MkdirAll(filepath.Dir(storyWorkDir), 0755); err != nil {
		return nil, fmt.Errorf("failed to create story work directory parent: %w", err)
	}

	// Step 3: Add worktree
	if err := w.addWorktree(ctx, mirrorPath, storyWorkDir); err != nil {
		return nil, fmt.Errorf("failed to add worktree from mirror %s to %s: %w", mirrorPath, storyWorkDir, err)
	}

	// Verify the worktree was created
	if _, err := os.Stat(storyWorkDir); err != nil {
		w.logger.Error("Story work directory does not exist after git worktree add: %s (error: %v)", storyWorkDir, err)
		return nil, fmt.Errorf("story work directory was not created at %s: %w", storyWorkDir, err)
	}
	w.logger.Debug("Verified story work directory exists at: %s", storyWorkDir)

	// Step 4: Create and checkout branch
	branchName := w.buildBranchName(storyID)
	actualBranchName, err := w.createBranch(ctx, storyWorkDir, branchName)
	if err != nil {
		return nil, fmt.Errorf("failed to create branch: %w", err)
	}

	return &WorkspaceResult{
		WorkDir:    storyWorkDir,
		BranchName: actualBranchName,
	}, nil
}

// CleanupWorkspace removes a worktree and cleans up the workspace
func (w *WorkspaceManager) CleanupWorkspace(ctx context.Context, agentID, storyID, agentWorkDir string) error {
	storyWorkDir := w.BuildStoryWorkDir(agentID, storyID, agentWorkDir)
	mirrorPath := w.BuildMirrorPath()

	// Remove worktree (must be run from the mirror directory)
	_, err := w.gitRunner.Run(ctx, mirrorPath, "worktree", "remove", storyWorkDir)
	if err != nil {
		return fmt.Errorf("failed to remove worktree: %w", err)
	}

	// Prune worktrees
	_, err = w.gitRunner.Run(ctx, mirrorPath, "worktree", "prune")
	if err != nil {
		return fmt.Errorf("failed to prune worktrees: %w", err)
	}

	// Remove agent directory if empty
	if isEmpty, _ := w.isDirectoryEmpty(agentWorkDir); isEmpty {
		os.RemoveAll(agentWorkDir)
	}

	return nil
}

// ensureMirrorClone creates or updates the mirror clone
func (w *WorkspaceManager) ensureMirrorClone(ctx context.Context) (string, error) {
	mirrorPath := w.BuildMirrorPath()

	// Check if mirror already exists
	if _, err := os.Stat(mirrorPath); os.IsNotExist(err) {
		// Create mirror directory
		if err := os.MkdirAll(filepath.Dir(mirrorPath), 0755); err != nil {
			return "", fmt.Errorf("failed to create mirror directory: %w", err)
		}

		// Clone bare repository (not mirror to avoid push conflicts)
		_, err := w.gitRunner.Run(ctx, "", "clone", "--bare", w.repoURL, mirrorPath)
		if err != nil {
			return "", fmt.Errorf("failed to clone mirror from %s to %s: %w", w.repoURL, mirrorPath, err)
		}
	} else {
		// Update existing mirror - fetch all branches and tags with pruning
		_, err := w.gitRunner.Run(ctx, mirrorPath, "remote", "update", "--prune")
		if err != nil {
			return "", fmt.Errorf("failed to update mirror %s: %w", mirrorPath, err)
		}
	}

	return mirrorPath, nil
}

// cleanupWorktrees prunes obsolete worktrees and runs garbage collection
func (w *WorkspaceManager) cleanupWorktrees(ctx context.Context, mirrorPath string) error {
	w.logger.Debug("Cleaning up worktrees in mirror: %s", mirrorPath)

	// Prune worktrees that are no longer needed
	_, err := w.gitRunner.Run(ctx, mirrorPath, "worktree", "prune", "--expire", "1.day.ago")
	if err != nil {
		w.logger.Warn("Failed to prune worktrees: %v", err)
		// Don't fail - just warn since this is housekeeping
	}

	// Optional: Run garbage collection to clean up unreachable objects
	_, err = w.gitRunner.Run(ctx, mirrorPath, "gc", "--prune=now")
	if err != nil {
		w.logger.Warn("Failed to run garbage collection: %v", err)
		// Don't fail - just warn since this is housekeeping
	}

	return nil
}

// addWorktree adds a new worktree from the mirror
func (w *WorkspaceManager) addWorktree(ctx context.Context, mirrorPath, storyWorkDir string) error {
	w.logger.Debug("Adding worktree from mirror=%s to path=%s with branch=%s", mirrorPath, storyWorkDir, w.baseBranch)

	// storyWorkDir is guaranteed to be absolute from BuildStoryWorkDir
	// For bare repositories, we need to use the branch name directly
	_, err := w.gitRunner.Run(ctx, mirrorPath, "worktree", "add", "--detach", storyWorkDir, w.baseBranch)
	if err != nil {
		return fmt.Errorf("git worktree add --detach %s %s failed from %s: %w", storyWorkDir, w.baseBranch, mirrorPath, err)
	}

	w.logger.Debug("Successfully added worktree at path=%s", storyWorkDir)
	return nil
}

// createBranch creates and checks out a new branch in the story work directory
// If the branch already exists, it will try incremental names (e.g., story-050-2, story-050-3, etc.)
// Returns the actual branch name that was created
func (w *WorkspaceManager) createBranch(ctx context.Context, storyWorkDir, branchName string) (string, error) {
	w.logger.Debug("createBranch called with storyWorkDir=%s, branchName=%s", storyWorkDir, branchName)

	// Check if story work directory exists before trying to create branch
	if _, err := os.Stat(storyWorkDir); os.IsNotExist(err) {
		w.logger.Error("story work directory does not exist: %s", storyWorkDir)
		return "", fmt.Errorf("story work directory does not exist: %s", storyWorkDir)
	}
	w.logger.Debug("Story work directory exists, proceeding with branch creation")

	// Get list of existing branches to avoid collisions
	existingBranches, err := w.getExistingBranches(ctx, storyWorkDir)
	if err != nil {
		w.logger.Warn("Failed to get existing branches, falling back to trial-and-error method: %v", err)
		return w.createBranchWithRetry(ctx, storyWorkDir, branchName)
	}

	// Find an available branch name
	originalBranchName := branchName
	attempt := 1
	maxAttempts := 10 // Safety limit to prevent infinite loops

	for attempt <= maxAttempts {
		if !w.branchExists(branchName, existingBranches) {
			// Branch name is available, create it
			_, err := w.gitRunner.Run(ctx, storyWorkDir, "switch", "-c", branchName)
			if err == nil {
				// Success! Log if we had to use an incremented name
				if attempt > 1 {
					w.logger.Warn("Branch name collision detected: '%s' already exists, using '%s' instead", originalBranchName, branchName)
				}
				return branchName, nil
			}
			// If creation still failed, it's a real error
			return "", fmt.Errorf("git switch -c %s failed in %s: %w", branchName, storyWorkDir, err)
		}

		// Branch exists, try next incremental name
		attempt++
		branchName = fmt.Sprintf("%s-%d", originalBranchName, attempt)
		w.logger.Info("Branch '%s' exists, trying next name: %s", originalBranchName, branchName)
	}

	// If we've exhausted all attempts
	return "", fmt.Errorf("unable to find available branch name after %d attempts, last tried: %s", maxAttempts, branchName)
}

// getExistingBranches gets a list of all branches (local and remote) in the repository
func (w *WorkspaceManager) getExistingBranches(ctx context.Context, storyWorkDir string) ([]string, error) {
	// Get all branches (local and remote)
	output, err := w.gitRunner.Run(ctx, storyWorkDir, "branch", "-a")
	if err != nil {
		return nil, fmt.Errorf("failed to list branches: %w", err)
	}

	// Parse branch names from output
	lines := strings.Split(string(output), "\n")
	var branches []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Remove markers like "* " for current branch and "remotes/" prefix
		line = strings.TrimPrefix(line, "* ")
		line = strings.TrimPrefix(line, "remotes/origin/")
		// Skip HEAD references
		if strings.Contains(line, "HEAD ->") {
			continue
		}
		branches = append(branches, strings.TrimSpace(line))
	}

	return branches, nil
}

// branchExists checks if a branch name exists in the list of existing branches
func (w *WorkspaceManager) branchExists(branchName string, existingBranches []string) bool {
	for _, existing := range existingBranches {
		if existing == branchName {
			return true
		}
	}
	return false
}

// createBranchWithRetry is the fallback method that uses trial-and-error (original approach)
func (w *WorkspaceManager) createBranchWithRetry(ctx context.Context, storyWorkDir, branchName string) (string, error) {
	originalBranchName := branchName
	attempt := 1
	maxAttempts := 10

	for attempt <= maxAttempts {
		_, err := w.gitRunner.Run(ctx, storyWorkDir, "switch", "-c", branchName)
		if err == nil {
			// Success! Log if we had to use an incremented name
			if attempt > 1 {
				w.logger.Warn("Branch name collision detected: '%s' already exists, using '%s' instead", originalBranchName, branchName)
			}
			return branchName, nil
		}

		// Check if this is a "branch already exists" error
		if strings.Contains(err.Error(), "already exists") {
			// Increment the branch name and try again
			attempt++
			branchName = fmt.Sprintf("%s-%d", originalBranchName, attempt)
			w.logger.Debug("Branch collision detected, trying next name: %s", branchName)
			continue
		}

		// If it's not a collision error, return the original error
		return "", fmt.Errorf("git switch -c %s failed in %s: %w", branchName, storyWorkDir, err)
	}

	// If we've exhausted all attempts
	return "", fmt.Errorf("unable to create branch after %d attempts, last tried: %s", maxAttempts, branchName)
}

// BuildMirrorPath constructs the mirror repository path
func (w *WorkspaceManager) BuildMirrorPath() string {
	// Extract repo name from URL (e.g., git@github.com:user/repo.git -> repo)
	repoName := filepath.Base(w.repoURL)
	repoName = strings.TrimSuffix(repoName, ".git")

	return filepath.Join(w.projectWorkDir, w.mirrorDir, repoName+".git")
}

// BuildStoryWorkDir constructs the story work directory path using the pattern
func (w *WorkspaceManager) BuildStoryWorkDir(agentID, storyID, agentWorkDir string) string {
	// Convert agentWorkDir to absolute path
	absAgentWorkDir, err := filepath.Abs(agentWorkDir)
	if err != nil {
		// If we can't get absolute path, use the original (fallback)
		absAgentWorkDir = agentWorkDir
	}

	path := w.worktreePattern
	path = strings.ReplaceAll(path, "{AGENT_ID}", agentID)
	path = strings.ReplaceAll(path, "{STORY_ID}", storyID)

	// Make absolute if relative - use agent work directory as base
	if !filepath.IsAbs(path) {
		path = filepath.Join(absAgentWorkDir, path)
	}

	// Ensure the final path is absolute
	absPath, err := filepath.Abs(path)
	if err != nil {
		// If we can't get absolute path, use the constructed path (fallback)
		absPath = path
	}

	return absPath
}

// buildBranchName constructs the branch name using the pattern
func (w *WorkspaceManager) buildBranchName(storyID string) string {
	return strings.ReplaceAll(w.branchPattern, "{STORY_ID}", storyID)
}

// isDirectoryEmpty checks if a directory is empty
func (w *WorkspaceManager) isDirectoryEmpty(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	return len(entries) == 0, nil
}
