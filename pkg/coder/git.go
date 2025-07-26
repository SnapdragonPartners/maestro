package coder

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"orchestrator/pkg/logx"
)

// GitRunner provides an interface for running Git commands with dependency injection support.
type GitRunner interface {
	// Run executes a Git command in the specified directory.
	// Returns stdout+stderr combined output and any error.
	Run(ctx context.Context, dir string, args ...string) ([]byte, error)

	// RunQuiet executes a Git command without logging errors (for fault-tolerant operations).
	RunQuiet(ctx context.Context, dir string, args ...string) ([]byte, error)
}

// DefaultGitRunner implements GitRunner using the system git command.
type DefaultGitRunner struct {
	logger *logx.Logger
}

// NewDefaultGitRunner creates a new DefaultGitRunner.
func NewDefaultGitRunner() *DefaultGitRunner {
	return &DefaultGitRunner{
		logger: logx.NewLogger("git"),
	}
}

// Run executes a Git command using exec.CommandContext.
func (g *DefaultGitRunner) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}

	// Log the command being executed.
	logDir := dir
	if logDir == "" {
		logDir = "."
	}
	g.logger.Debug("Executing Git command: cd %s && git %s", logDir, strings.Join(args, " "))

	// Combine stdout and stderr to capture all git output.
	output, err := cmd.CombinedOutput()

	// Add context to error for better debugging.
	if err != nil {
		g.logger.Error("Git command failed: cd %s && git %s", logDir, strings.Join(args, " "))
		g.logger.Error("Git error: %v", err)
		g.logger.Error("Git output: %s", string(output))
		return output, logx.Errorf("git %s failed in %s: %w\nOutput: %s",
			strings.Join(args, " "), dir, err, string(output))
	}

	g.logger.Debug("Git command succeeded: %s", string(output))
	return output, nil
}

// RunQuiet executes a Git command without logging errors (for fault-tolerant operations).
func (g *DefaultGitRunner) RunQuiet(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}

	// Log command at debug level only.
	logDir := dir
	if logDir == "" {
		logDir = "."
	}
	g.logger.Debug("Executing Git command (quiet): cd %s && git %s", logDir, strings.Join(args, " "))

	// Combine stdout and stderr to capture all git output.
	output, err := cmd.CombinedOutput()

	// Don't log errors - caller will handle them if needed.
	if err != nil {
		return output, logx.Errorf("git %s failed in %s: %w\nOutput: %s",
			strings.Join(args, " "), dir, err, string(output))
	}

	g.logger.Debug("Git command succeeded (quiet): %s", string(output))
	return output, nil
}

// ContainerManager defines the interface for managing Docker containers.
type ContainerManager interface {
	StopContainer(ctx context.Context, containerName string) error
	Shutdown(ctx context.Context) error
}

// WorkspaceManager handles Git worktree operations and container cleanup for coder agents.
type WorkspaceManager struct {
	gitRunner        GitRunner
	containerManager ContainerManager // Optional container manager for Docker cleanup
	logger           *logx.Logger
	projectWorkDir   string // Project work directory (shared across all agents) - contains mirrors and worktrees
	repoURL          string
	baseBranch       string
	mirrorDir        string // Mirror directory relative to projectWorkDir (e.g., ".mirrors")
	branchPattern    string // Pattern for branch names (e.g., "story-{STORY_ID}")
	worktreePattern  string // Pattern for worktree paths relative to agentWorkDir (e.g., "{STORY_ID}")
}

// NewWorkspaceManager creates a new workspace manager.
// projectWorkDir is the root work directory for the entire orchestrator run (shared across agents).
func NewWorkspaceManager(gitRunner GitRunner, projectWorkDir, repoURL, baseBranch, mirrorDir, branchPattern, worktreePattern string) *WorkspaceManager {
	// Convert projectWorkDir to absolute path at construction time.
	absProjectWorkDir, err := filepath.Abs(projectWorkDir)
	if err != nil {
		// If we can't get absolute path, use the original (fallback).
		absProjectWorkDir = projectWorkDir
	}

	return &WorkspaceManager{
		gitRunner:        gitRunner,
		projectWorkDir:   absProjectWorkDir,
		repoURL:          repoURL,
		baseBranch:       baseBranch,
		mirrorDir:        mirrorDir,
		branchPattern:    branchPattern,
		worktreePattern:  worktreePattern,
		containerManager: nil, // Set via SetContainerManager if needed
		logger:           logx.NewLogger("workspace"),
	}
}

// SetContainerManager sets the container manager for Docker cleanup operations.
func (w *WorkspaceManager) SetContainerManager(containerManager ContainerManager) {
	w.containerManager = containerManager
}

// WorkspaceResult contains the results of workspace setup.
type WorkspaceResult struct {
	WorkDir    string
	BranchName string
}

// SetupWorkspace implements the AR-102 workspace initialization logic.
func (w *WorkspaceManager) SetupWorkspace(ctx context.Context, agentID, storyID, agentWorkDir string) (*WorkspaceResult, error) {
	w.logger.Debug("SetupWorkspace called with agentID=%s, storyID=%s, agentWorkDir=%s", agentID, storyID, agentWorkDir)
	w.logger.Debug("ProjectWorkDir: %s", w.projectWorkDir)

	// Step 1: Ensure mirror clone exists and is up to date.
	mirrorPath, err := w.ensureMirrorClone(ctx)
	if err != nil {
		return nil, logx.Wrap(err, "failed to setup mirror clone")
	}
	w.logger.Debug("Mirror path: %s", mirrorPath)

	// Step 1.5: Occasionally clean up old worktrees (housekeeping).
	// Run cleanup every 10th story to avoid overhead.
	if strings.HasSuffix(storyID, "0") {
		w.logger.Debug("Running worktree cleanup for story %s", storyID)
		if cleanupErr := w.cleanupWorktrees(ctx, mirrorPath); cleanupErr != nil {
			w.logger.Warn("Worktree cleanup failed: %v", cleanupErr)
		}
	}

	// Step 2: Get agent work directory (simplified - work directly in agent directory).
	agentWorkDirPath := w.BuildAgentWorkDir(agentID, agentWorkDir)
	w.logger.Debug("Agent work directory: %s", agentWorkDirPath)

	// Step 3: Create fresh agent work directory (remove existing if present).
	if freshErr := w.createFreshWorktree(ctx, mirrorPath, agentWorkDirPath); freshErr != nil {
		return nil, logx.Wrap(freshErr, fmt.Sprintf("failed to create fresh worktree at %s", agentWorkDirPath))
	}

	// Verify the worktree exists.
	if _, statErr := os.Stat(agentWorkDirPath); statErr != nil {
		w.logger.Error("Agent work directory does not exist after worktree setup: %s (error: %v)", agentWorkDirPath, statErr)
		return nil, logx.Wrap(err, fmt.Sprintf("agent work directory was not created at %s", agentWorkDirPath))
	}
	w.logger.Debug("Verified agent work directory exists at: %s", agentWorkDirPath)

	// Step 4: Create and checkout branch.
	branchName := w.buildBranchName(storyID)
	actualBranchName, err := w.createBranch(ctx, agentWorkDirPath, branchName)
	if err != nil {
		return nil, logx.Wrap(err, "failed to create branch")
	}

	return &WorkspaceResult{
		WorkDir:    agentWorkDirPath,
		BranchName: actualBranchName,
	}, nil
}

// CleanupWorkspace removes the agent work directory completely after a story.
// This is much simpler now since we recreate the directory fresh for each story.
func (w *WorkspaceManager) CleanupWorkspace(ctx context.Context, agentID, storyID, agentWorkDir string) error {
	agentWorkDirPath := w.BuildAgentWorkDir(agentID, agentWorkDir)
	w.logger.Debug("Cleaning up workspace for story %s by removing directory: %s", storyID, agentWorkDirPath)

	// Remove the entire agent work directory.
	if _, err := os.Stat(agentWorkDirPath); err == nil {
		w.logger.Debug("Removing agent work directory: %s", agentWorkDirPath)
		if err := os.RemoveAll(agentWorkDirPath); err != nil {
			return logx.Wrap(err, "failed to remove agent work directory")
		}
	} else {
		w.logger.Debug("Agent work directory does not exist, nothing to clean up: %s", agentWorkDirPath)
	}

	// Remove worktree registration from git (ignore errors since directory is gone).
	mirrorPath := w.BuildMirrorPath()
	if _, err := w.gitRunner.RunQuiet(ctx, mirrorPath, "worktree", "remove", "--force", agentWorkDirPath); err != nil {
		w.logger.Debug("Worktree remove failed (expected): %v", err)
	}

	w.logger.Debug("Completed cleanup for story %s", storyID)
	return nil
}

// CleanupAgentResources performs comprehensive cleanup of all agent resources.
// This is the DRY method shared between orchestrator and agent SETUP state.
func (w *WorkspaceManager) CleanupAgentResources(ctx context.Context, agentID, containerName, agentWorkDir, stateDir string) error {
	w.logger.Info("Starting comprehensive cleanup for agent %s", agentID)

	var cleanupErrors []error

	// 1. Stop and cleanup Docker container if provided.
	if containerName != "" && w.containerManager != nil {
		w.logger.Debug("Stopping container: %s", containerName)
		if err := w.containerManager.StopContainer(ctx, containerName); err != nil {
			w.logger.Error("Failed to stop container %s: %v", containerName, err)
			cleanupErrors = append(cleanupErrors, logx.Wrap(err, "container cleanup failed"))
		} else {
			w.logger.Info("Successfully stopped container: %s", containerName)
		}
	}

	// 2. Clean up workspace (git worktree + directory).
	// Use empty storyID since we're cleaning up entire agent workspace.
	if err := w.CleanupWorkspace(ctx, agentID, "", agentWorkDir); err != nil {
		w.logger.Error("Failed to cleanup workspace for agent %s: %v", agentID, err)
		cleanupErrors = append(cleanupErrors, logx.Wrap(err, "workspace cleanup failed"))
	}

	// 3. Remove agent state directory if provided.
	if stateDir != "" {
		w.logger.Debug("Removing agent state directory: %s", stateDir)
		if err := os.RemoveAll(stateDir); err != nil {
			w.logger.Error("Failed to remove state directory %s: %v", stateDir, err)
			cleanupErrors = append(cleanupErrors, logx.Wrap(err, "state directory cleanup failed"))
		} else {
			w.logger.Info("Removed agent state directory: %s", stateDir)
		}
	}

	// 4. Additional container manager shutdown if available.
	if w.containerManager != nil {
		w.logger.Debug("Shutting down container manager")
		if err := w.containerManager.Shutdown(ctx); err != nil {
			w.logger.Error("Failed to shutdown container manager: %v", err)
			cleanupErrors = append(cleanupErrors, logx.Wrap(err, "container manager shutdown failed"))
		}
	}

	// Return combined errors if any occurred.
	if len(cleanupErrors) > 0 {
		return logx.Errorf("cleanup completed with %d errors: %v", len(cleanupErrors), cleanupErrors)
	}

	w.logger.Info("Successfully completed comprehensive cleanup for agent %s", agentID)
	return nil
}

// ensureMirrorClone creates or updates the mirror clone.
func (w *WorkspaceManager) ensureMirrorClone(ctx context.Context) (string, error) {
	mirrorPath := w.BuildMirrorPath()

	// Check if mirror already exists.
	if _, err := os.Stat(mirrorPath); os.IsNotExist(err) {
		// Create mirror directory.
		if err := os.MkdirAll(filepath.Dir(mirrorPath), 0755); err != nil {
			return "", logx.Wrap(err, "failed to create mirror directory")
		}

		// Clone bare repository (not mirror to avoid push conflicts).
		_, err := w.gitRunner.Run(ctx, "", "clone", "--bare", w.repoURL, mirrorPath)
		if err != nil {
			return "", logx.Wrap(err, fmt.Sprintf("failed to clone mirror from %s to %s", w.repoURL, mirrorPath))
		}
	} else {
		// Update existing mirror - fetch all branches and tags with pruning.
		// Use file locking to prevent concurrent git remote update operations.
		lockPath := filepath.Join(mirrorPath, ".update.lock")
		lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return "", logx.Wrap(err, fmt.Sprintf("failed to create lock file %s", lockPath))
		}
		defer func() { _ = lockFile.Close() }()
		defer func() { _ = os.Remove(lockPath) }() // Clean up lock file

		// Acquire exclusive lock (blocks until available).
		err = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX)
		if err != nil {
			return "", logx.Wrap(err, "failed to acquire exclusive lock for git remote update")
		}
		defer func() { _ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN) }() // Release lock

		_, err = w.gitRunner.Run(ctx, mirrorPath, "remote", "update", "--prune")
		if err != nil {
			return "", logx.Wrap(err, fmt.Sprintf("failed to update mirror %s", mirrorPath))
		}
	}

	return mirrorPath, nil
}

// cleanupWorktrees prunes obsolete worktrees and runs garbage collection.
//
//nolint:unparam // error return kept for future extensibility
func (w *WorkspaceManager) cleanupWorktrees(ctx context.Context, mirrorPath string) error {
	w.logger.Debug("Cleaning up worktrees in mirror: %s", mirrorPath)

	// Prune worktrees that are no longer needed.
	_, err := w.gitRunner.Run(ctx, mirrorPath, "worktree", "prune", "--expire", "1.day.ago")
	if err != nil {
		w.logger.Warn("Failed to prune worktrees: %v", err)
		// Don't fail - just warn since this is housekeeping.
	}

	// Optional: Run garbage collection to clean up unreachable objects.
	_, err = w.gitRunner.Run(ctx, mirrorPath, "gc", "--prune=now")
	if err != nil {
		w.logger.Warn("Failed to run garbage collection: %v", err)
		// Don't fail - just warn since this is housekeeping.
	}

	return nil
}

// createFreshWorktree creates a completely fresh worktree by removing any existing directory.
// and creating a new one. This ensures a perfectly clean state for each story.
func (w *WorkspaceManager) createFreshWorktree(ctx context.Context, mirrorPath, agentWorkDir string) error {
	w.logger.Debug("Creating fresh worktree at: %s", agentWorkDir)

	// Step 1: Remove existing directory completely if it exists.
	if _, err := os.Stat(agentWorkDir); err == nil {
		w.logger.Debug("Removing existing agent work directory: %s", agentWorkDir)
		if err := os.RemoveAll(agentWorkDir); err != nil {
			return logx.Wrap(err, "failed to remove existing directory")
		}
	}

	// Step 2: Remove any lingering worktree registration (ignore errors).
	// This command is expected to fail if no worktree exists - that's fine.
	_, err := w.gitRunner.RunQuiet(ctx, mirrorPath, "worktree", "remove", "--force", agentWorkDir)
	if err != nil {
		w.logger.Debug("Worktree remove failed (expected): %v", err)
	}

	// Step 3: Create parent directory if needed.
	if mkdirErr := os.MkdirAll(filepath.Dir(agentWorkDir), 0755); mkdirErr != nil {
		return logx.Wrap(mkdirErr, "failed to create parent directory")
	}

	// Step 4: Create the fresh worktree.
	w.logger.Debug("Creating fresh worktree at: %s", agentWorkDir)
	_, err = w.gitRunner.Run(ctx, mirrorPath, "worktree", "add", "--detach", agentWorkDir, w.baseBranch)
	if err != nil {
		return logx.Wrap(err, fmt.Sprintf("git worktree add --detach %s %s failed from %s", agentWorkDir, w.baseBranch, mirrorPath))
	}

	w.logger.Debug("Successfully created fresh worktree at: %s", agentWorkDir)
	return nil
}

// createBranch creates and checks out a new branch in the agent work directory.
// If the branch already exists, it will try incremental names (e.g., story-050-2, story-050-3, etc.).
// Returns the actual branch name that was created.
func (w *WorkspaceManager) createBranch(ctx context.Context, agentWorkDir, branchName string) (string, error) {
	w.logger.Debug("createBranch called with agentWorkDir=%s, branchName=%s", agentWorkDir, branchName)

	// Check if agent work directory exists before trying to create branch.
	if _, err := os.Stat(agentWorkDir); os.IsNotExist(err) {
		w.logger.Error("agent work directory does not exist: %s", agentWorkDir)
		return "", logx.Errorf("agent work directory does not exist: %s", agentWorkDir)
	}
	w.logger.Debug("Agent work directory exists, proceeding with branch creation")

	// Get list of existing branches to avoid collisions.
	existingBranches, err := w.getExistingBranches(ctx, agentWorkDir)
	if err != nil {
		w.logger.Warn("Failed to get existing branches, falling back to trial-and-error method: %v", err)
		return w.createBranchWithRetry(ctx, agentWorkDir, branchName)
	}

	// Find an available branch name.
	originalBranchName := branchName
	attempt := 1
	maxAttempts := 10 // Safety limit to prevent infinite loops

	for attempt <= maxAttempts {
		if !w.branchExists(branchName, existingBranches) {
			// Branch name is available, create it.
			_, err := w.gitRunner.Run(ctx, agentWorkDir, "switch", "-c", branchName)
			if err == nil {
				// Success! Log if we had to use an incremented name.
				if attempt > 1 {
					w.logger.Warn("Branch name collision detected: '%s' already exists, using '%s' instead", originalBranchName, branchName)
				}
				return branchName, nil
			}
			// If creation still failed, it's a real error.
			return "", logx.Wrap(err, fmt.Sprintf("git switch -c %s failed in %s", branchName, agentWorkDir))
		}

		// Branch exists, try next incremental name.
		attempt++
		branchName = fmt.Sprintf("%s-%d", originalBranchName, attempt)
		w.logger.Info("Branch '%s' exists, trying next name: %s", originalBranchName, branchName)
	}

	// If we've exhausted all attempts.
	return "", logx.Errorf("unable to find available branch name after %d attempts, last tried: %s", maxAttempts, branchName)
}

// getExistingBranches gets a list of all branches (local and remote) in the repository.
func (w *WorkspaceManager) getExistingBranches(ctx context.Context, agentWorkDir string) ([]string, error) {
	// Get all branches (local and remote).
	output, err := w.gitRunner.Run(ctx, agentWorkDir, "branch", "-a")
	if err != nil {
		return nil, logx.Wrap(err, "failed to list branches")
	}

	// Parse branch names from output.
	lines := strings.Split(string(output), "\n")
	branches := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Remove markers like "* " for current branch and "remotes/" prefix.
		line = strings.TrimPrefix(line, "* ")
		line = strings.TrimPrefix(line, "remotes/origin/")
		// Skip HEAD references.
		if strings.Contains(line, "HEAD ->") {
			continue
		}
		branches = append(branches, strings.TrimSpace(line))
	}

	return branches, nil
}

// branchExists checks if a branch name exists in the list of existing branches.
func (w *WorkspaceManager) branchExists(branchName string, existingBranches []string) bool {
	for _, existing := range existingBranches {
		if existing == branchName {
			return true
		}
	}
	return false
}

// createBranchWithRetry is the fallback method that uses trial-and-error (original approach).
func (w *WorkspaceManager) createBranchWithRetry(ctx context.Context, agentWorkDir, branchName string) (string, error) {
	originalBranchName := branchName
	attempt := 1
	maxAttempts := 10

	for attempt <= maxAttempts {
		_, err := w.gitRunner.Run(ctx, agentWorkDir, "switch", "-c", branchName)
		if err == nil {
			// Success! Log if we had to use an incremented name.
			if attempt > 1 {
				w.logger.Warn("Branch name collision detected: '%s' already exists, using '%s' instead", originalBranchName, branchName)
			}
			return branchName, nil
		}

		// Check if this is a "branch already exists" error.
		if strings.Contains(err.Error(), "already exists") {
			// Increment the branch name and try again.
			attempt++
			branchName = fmt.Sprintf("%s-%d", originalBranchName, attempt)
			w.logger.Debug("Branch collision detected, trying next name: %s", branchName)
			continue
		}

		// If it's not a collision error, return the original error.
		return "", logx.Wrap(err, fmt.Sprintf("git switch -c %s failed in %s", branchName, agentWorkDir))
	}

	// If we've exhausted all attempts.
	return "", logx.Errorf("unable to create branch after %d attempts, last tried: %s", maxAttempts, branchName)
}

// BuildMirrorPath constructs the mirror repository path.
func (w *WorkspaceManager) BuildMirrorPath() string {
	// Extract repo name from URL (e.g., git@github.com:user/repo.git -> repo).
	repoName := filepath.Base(w.repoURL)
	repoName = strings.TrimSuffix(repoName, ".git")

	return filepath.Join(w.projectWorkDir, w.mirrorDir, repoName+".git")
}

// BuildAgentWorkDir returns the agent work directory as an absolute path.
func (w *WorkspaceManager) BuildAgentWorkDir(_ /* agentID */, agentWorkDir string) string {
	// Convert agentWorkDir to absolute path.
	absAgentWorkDir, err := filepath.Abs(agentWorkDir)
	if err != nil {
		// If we can't get absolute path, use the original (fallback).
		absAgentWorkDir = agentWorkDir
	}

	return absAgentWorkDir
}

// buildBranchName constructs the branch name using the pattern.
func (w *WorkspaceManager) buildBranchName(storyID string) string {
	return strings.ReplaceAll(w.branchPattern, "{STORY_ID}", storyID)
}
