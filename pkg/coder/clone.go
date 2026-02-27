package coder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/utils"
)

// CloneManager handles Git clone operations for coder agents.
// Provides complete agent isolation with self-contained repositories while maintaining network efficiency through local mirrors.
// Git config values (repoURL, baseBranch, mirrorDir, branchPattern) can be passed at construction or read from global config.
// If passed values are empty, falls back to global config (supports late binding for coders).
// If passed values are non-empty, uses those (supports bootstrap with custom config).
type CloneManager struct {
	gitRunner        GitRunner
	containerManager ContainerManager // Optional container manager for Docker cleanup
	logger           *logx.Logger
	projectWorkDir   string // Project work directory (shared across all agents) - contains mirrors and clones
	// Override values - if set, used instead of global config
	repoURLOverride       string
	baseBranchOverride    string
	mirrorDirOverride     string
	branchPatternOverride string
}

// NewCloneManager creates a new clone manager.
// projectWorkDir is the root work directory for the entire orchestrator run (shared across agents).
// repoURL, baseBranch, mirrorDir, branchPattern: if non-empty, override global config values.
// Pass empty strings to use global config (supports late binding for coders).
func NewCloneManager(gitRunner GitRunner, projectWorkDir, repoURL, baseBranch, mirrorDir, branchPattern string) *CloneManager {
	// Convert projectWorkDir to absolute path at construction time.
	absProjectWorkDir, err := filepath.Abs(projectWorkDir)
	if err != nil {
		// If we can't get absolute path, use the original (fallback).
		// This is unusual and may indicate a filesystem or path issue.
		logx.NewLogger("clone-manager").Warn("Failed to get absolute path for %q, using original: %v", projectWorkDir, err)
		absProjectWorkDir = projectWorkDir
	}

	return &CloneManager{
		gitRunner:             gitRunner,
		projectWorkDir:        absProjectWorkDir,
		containerManager:      nil, // Set via SetContainerManager if needed
		logger:                logx.NewLogger("clone-manager"),
		repoURLOverride:       repoURL,
		baseBranchOverride:    baseBranch,
		mirrorDirOverride:     mirrorDir,
		branchPatternOverride: branchPattern,
	}
}

// SetContainerManager sets the container manager for Docker cleanup operations.
func (c *CloneManager) SetContainerManager(containerManager ContainerManager) {
	c.containerManager = containerManager
}

// getRepoURL returns the repo URL to use - override if set, otherwise global config.
func (c *CloneManager) getRepoURL() string {
	if c.repoURLOverride != "" {
		return c.repoURLOverride
	}
	return config.GetGitRepoURL()
}

// getBaseBranch returns the base branch to use - override if set, otherwise global config.
func (c *CloneManager) getBaseBranch() string {
	if c.baseBranchOverride != "" {
		return c.baseBranchOverride
	}
	return config.GetGitBaseBranch()
}

// getMirrorDir returns the mirror dir to use - override if set, otherwise global config.
func (c *CloneManager) getMirrorDir() string {
	if c.mirrorDirOverride != "" {
		return c.mirrorDirOverride
	}
	return config.GetGitMirrorDir()
}

// getBranchPattern returns the branch pattern to use - override if set, otherwise global config.
func (c *CloneManager) getBranchPattern() string {
	if c.branchPatternOverride != "" {
		return c.branchPatternOverride
	}
	return config.GetGitBranchPattern()
}

// CloneResult contains the results of clone setup.
type CloneResult struct {
	WorkDir    string
	BranchName string
}

// SetupWorkspace creates a self-contained git repository for complete agent isolation.
func (c *CloneManager) SetupWorkspace(ctx context.Context, agentID, storyID, agentWorkDir string) (*CloneResult, error) {
	c.logger.Debug("SetupWorkspace called with agentID=%s, storyID=%s, agentWorkDir=%s", agentID, storyID, agentWorkDir)
	c.logger.Debug("ProjectWorkDir: %s", c.projectWorkDir)

	// Step 1: Ensure bare mirror exists and is up to date.
	mirrorPath, err := c.ensureMirrorClone(ctx)
	if err != nil {
		return nil, logx.Wrap(err, "failed to setup mirror clone")
	}
	c.logger.Debug("Mirror path: %s", mirrorPath)

	// Step 2: Get agent work directory.
	agentWorkDirPath := c.BuildAgentWorkDir(agentID, agentWorkDir)
	c.logger.Debug("Agent work directory: %s", agentWorkDirPath)

	// Step 3: Create fresh lightweight clone.
	if cloneErr := c.createFreshClone(ctx, mirrorPath, agentWorkDirPath); cloneErr != nil {
		return nil, logx.Wrap(cloneErr, fmt.Sprintf("failed to create fresh clone at %s", agentWorkDirPath))
	}

	// Verify the clone exists.
	if _, statErr := os.Stat(agentWorkDirPath); statErr != nil {
		c.logger.Error("Agent work directory does not exist after clone setup: %s (error: %v)", agentWorkDirPath, statErr)
		return nil, logx.Wrap(statErr, fmt.Sprintf("agent work directory was not created at %s", agentWorkDirPath))
	}
	c.logger.Debug("Verified agent work directory exists at: %s", agentWorkDirPath)

	// Step 4: Configure git user identity on host (before any container mounts)
	if gitErr := c.configureGitIdentity(ctx, agentWorkDirPath, agentID); gitErr != nil {
		return nil, logx.Wrap(gitErr, "failed to configure git user identity")
	}

	// Step 5: Create and checkout branch.
	branchName := c.buildBranchName(storyID)
	actualBranchName, err := c.createBranch(ctx, agentWorkDirPath, branchName)
	if err != nil {
		return nil, logx.Wrap(err, "failed to create branch")
	}

	return &CloneResult{
		WorkDir:    agentWorkDirPath,
		BranchName: actualBranchName,
	}, nil
}

// CleanupWorkspace cleans the agent workspace contents after a story.
// IMPORTANT: This preserves the directory inode to maintain Docker bind mounts.
// On macOS with Docker Desktop, bind mounts track inodes, not paths. If we delete
// and recreate the directory, existing bind mounts become stale.
func (c *CloneManager) CleanupWorkspace(_ context.Context, agentID, storyID, agentWorkDir string) error {
	agentWorkDirPath := c.BuildAgentWorkDir(agentID, agentWorkDir)
	c.logger.Debug("Cleaning up workspace for story %s by clearing contents: %s", storyID, agentWorkDirPath)

	// Clean directory contents but preserve the directory itself (preserves inode).
	if _, err := os.Stat(agentWorkDirPath); err == nil {
		c.logger.Debug("Clearing agent work directory contents: %s", agentWorkDirPath)
		if err := utils.CleanDirectoryContents(agentWorkDirPath); err != nil {
			return logx.Wrap(err, "failed to clean agent work directory contents")
		}
	} else {
		c.logger.Debug("Agent work directory does not exist, nothing to clean up: %s", agentWorkDirPath)
	}

	c.logger.Debug("Completed cleanup for story %s", storyID)
	return nil
}

// CleanupAgentResources performs comprehensive cleanup of all agent resources.
//
//nolint:dupl
func (c *CloneManager) CleanupAgentResources(ctx context.Context, agentID, containerName, agentWorkDir, stateDir string) error {
	c.logger.Info("Starting comprehensive cleanup for agent %s", agentID)

	var cleanupErrors []error

	// 1. Stop and cleanup Docker container if provided.
	if containerName != "" && c.containerManager != nil {
		c.logger.Debug("Stopping container: %s", containerName)
		if err := c.containerManager.StopContainer(ctx, containerName); err != nil {
			c.logger.Error("Failed to stop container %s: %v", containerName, err)
			cleanupErrors = append(cleanupErrors, logx.Wrap(err, "container cleanup failed"))
		} else {
			c.logger.Info("Successfully stopped container: %s", containerName)
		}
	}

	// 2. Clean up workspace (git clone + directory).
	// Use empty storyID since we're cleaning up entire agent workspace.
	if err := c.CleanupWorkspace(ctx, agentID, "", agentWorkDir); err != nil {
		c.logger.Error("Failed to cleanup workspace for agent %s: %v", agentID, err)
		cleanupErrors = append(cleanupErrors, logx.Wrap(err, "workspace cleanup failed"))
	}

	// 3. Remove agent state directory if provided.
	if stateDir != "" {
		c.logger.Debug("Removing agent state directory: %s", stateDir)
		if err := os.RemoveAll(stateDir); err != nil {
			c.logger.Error("Failed to remove state directory %s: %v", stateDir, err)
			cleanupErrors = append(cleanupErrors, logx.Wrap(err, "state directory cleanup failed"))
		} else {
			c.logger.Info("Removed agent state directory: %s", stateDir)
		}
	}

	// 4. Additional container manager shutdown if available.
	if c.containerManager != nil {
		c.logger.Debug("Shutting down container manager")
		if err := c.containerManager.Shutdown(ctx); err != nil {
			c.logger.Error("Failed to shutdown container manager: %v", err)
			cleanupErrors = append(cleanupErrors, logx.Wrap(err, "container manager shutdown failed"))
		}
	}

	// Return combined errors if any occurred.
	if len(cleanupErrors) > 0 {
		return logx.Errorf("cleanup completed with %d errors: %v", len(cleanupErrors), cleanupErrors)
	}

	c.logger.Info("Successfully completed comprehensive cleanup for agent %s", agentID)
	return nil
}

// ensureMirrorClone creates or updates the bare mirror clone.
//
//nolint:dupl
func (c *CloneManager) ensureMirrorClone(ctx context.Context) (string, error) {
	mirrorPath := c.BuildMirrorPath()

	// Check if mirror already exists.
	if _, err := os.Stat(mirrorPath); os.IsNotExist(err) {
		// Create mirror directory.
		if err := os.MkdirAll(filepath.Dir(mirrorPath), 0755); err != nil {
			return "", logx.Wrap(err, "failed to create mirror directory")
		}

		// Clone bare repository as object pool (with retry for network resilience).
		_, err := c.retryGitNetworkOp(ctx, "", "clone", "--bare", c.getRepoURL(), mirrorPath)
		if err != nil {
			return "", logx.Wrap(err, fmt.Sprintf("failed to clone mirror from %s to %s", c.getRepoURL(), mirrorPath))
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

		_, err = c.retryGitNetworkOp(ctx, mirrorPath, "remote", "update", "--prune")
		if err != nil {
			return "", logx.Wrap(err, fmt.Sprintf("failed to update mirror %s", mirrorPath))
		}
	}

	return mirrorPath, nil
}

// createFreshClone creates a self-contained git repository for agent isolation.
// Uses local mirror as source for network efficiency while ensuring complete container compatibility.
//
// Two-remote strategy:
//   - origin → local mirror (host path during clone, remapped to /mirrors/<repo>.git after container starts)
//   - github → GitHub URL (used only by host-side push/fetch operations)
//
// IMPORTANT: This preserves the directory inode to maintain Docker bind mounts.
// On macOS with Docker Desktop, bind mounts track inodes, not paths. If we delete
// and recreate the directory, existing bind mounts become stale. We use git init
// instead of git clone to allow cleaning contents while preserving the directory.
func (c *CloneManager) createFreshClone(ctx context.Context, mirrorPath, agentWorkDir string) error {
	c.logger.Debug("Creating fresh self-contained clone at: %s (two-remote strategy)", agentWorkDir)

	// Handle existing directory - clean contents but preserve inode for bind mounts.
	if _, err := os.Stat(agentWorkDir); err == nil {
		c.logger.Debug("Cleaning existing agent work directory contents (preserving inode): %s", agentWorkDir)
		if err := utils.CleanDirectoryContents(agentWorkDir); err != nil {
			return logx.Wrap(err, "failed to clean existing directory contents")
		}
	} else {
		// Directory doesn't exist - create it.
		c.logger.Debug("Creating agent work directory: %s", agentWorkDir)
		if err := os.MkdirAll(agentWorkDir, 0755); err != nil {
			return logx.Wrap(err, "failed to create agent work directory")
		}
	}

	// Initialize git repository in the (now empty) directory.
	// We use git init + fetch instead of git clone to work with existing directories.
	c.logger.Debug("Initializing git repository: %s", agentWorkDir)
	_, err := c.gitRunner.Run(ctx, agentWorkDir, "init")
	if err != nil {
		return logx.Wrap(err, "git init failed")
	}

	// Add origin pointing to the local mirror (host path — works during host-side setup).
	// After the container starts, setup.go remaps origin to the container-visible /mirrors/<repo>.git path.
	c.logger.Debug("Adding origin remote (mirror): %s", mirrorPath)
	_, err = c.gitRunner.Run(ctx, agentWorkDir, "remote", "add", "origin", mirrorPath)
	if err != nil {
		return logx.Wrap(err, "failed to add origin remote (mirror)")
	}

	// Fetch all branches and tags from origin (local mirror — fast, no creds needed).
	c.logger.Debug("Fetching from origin (mirror)")
	_, err = c.gitRunner.Run(ctx, agentWorkDir, "fetch", "origin", "--tags")
	if err != nil {
		return logx.Wrap(err, "git fetch from origin (mirror) failed")
	}

	// Checkout the base branch from origin.
	c.logger.Debug("Checking out base branch: %s", c.getBaseBranch())
	_, err = c.gitRunner.Run(ctx, agentWorkDir, "checkout", "-b", c.getBaseBranch(), "origin/"+c.getBaseBranch())
	if err != nil {
		return logx.Wrap(err, fmt.Sprintf("git checkout %s failed", c.getBaseBranch()))
	}

	// Add 'github' remote for host-side push operations only.
	// Container operations use 'origin' (mirror). Host-side push/fetch uses 'github'.
	c.logger.Debug("Adding github remote for host-side push: %s", c.getRepoURL())
	_, err = c.gitRunner.Run(ctx, agentWorkDir, "remote", "add", "github", c.getRepoURL())
	if err != nil {
		return logx.Wrap(err, "failed to add github remote - agent will not be able to push branches")
	}

	c.logger.Debug("Successfully created self-contained clone at: %s (origin=mirror, github=%s)", agentWorkDir, c.getRepoURL())
	return nil
}

// createBranch creates and checks out a new branch in the agent clone directory.
func (c *CloneManager) createBranch(ctx context.Context, agentWorkDir, branchName string) (string, error) {
	c.logger.Debug("createBranch called with agentWorkDir=%s, branchName=%s", agentWorkDir, branchName)

	// Check if agent work directory exists before trying to create branch.
	if _, err := os.Stat(agentWorkDir); os.IsNotExist(err) {
		c.logger.Error("agent work directory does not exist: %s", agentWorkDir)
		return "", logx.Errorf("agent work directory does not exist: %s", agentWorkDir)
	}
	c.logger.Debug("Agent work directory exists, proceeding with branch creation")

	// Get list of existing branches to avoid collisions.
	existingBranches, err := c.getExistingBranches(ctx, agentWorkDir)
	if err != nil {
		c.logger.Warn("Failed to get existing branches, falling back to trial-and-error method: %v", err)
		return c.createBranchWithRetry(ctx, agentWorkDir, branchName)
	}

	// Find an available branch name.
	originalBranchName := branchName
	attempt := 1
	maxAttempts := 10 // Safety limit to prevent infinite loops

	for attempt <= maxAttempts {
		if !c.branchExists(branchName, existingBranches) {
			// Branch name is available, create it.
			_, err := c.gitRunner.Run(ctx, agentWorkDir, "switch", "-c", branchName)
			if err == nil {
				// Success! Log if we had to use an incremented name.
				if attempt > 1 {
					c.logger.Warn("Branch name collision detected: '%s' already exists, using '%s' instead", originalBranchName, branchName)
				}
				return branchName, nil
			}
			// If creation still failed, it's a real error.
			return "", logx.Wrap(err, fmt.Sprintf("git switch -c %s failed in %s", branchName, agentWorkDir))
		}

		// Branch exists, try next incremental name.
		attempt++
		branchName = fmt.Sprintf("%s-%d", originalBranchName, attempt)
		c.logger.Info("Branch '%s' exists, trying next name: %s", originalBranchName, branchName)
	}

	// If we've exhausted all attempts.
	return "", logx.Errorf("unable to find available branch name after %d attempts, last tried: %s", maxAttempts, branchName)
}

// getExistingBranches gets a list of all branches (local and remote) in the repository.
// Since origin now points to the local mirror, ls-remote is fast and needs no credentials.
func (c *CloneManager) getExistingBranches(ctx context.Context, agentWorkDir string) ([]string, error) {
	branches := make([]string, 0)

	// Get local branches first.
	localOutput, err := c.gitRunner.Run(ctx, agentWorkDir, "branch")
	if err != nil {
		return nil, logx.Wrap(err, "failed to list local branches")
	}

	// Parse local branch names.
	for _, line := range strings.Split(string(localOutput), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Remove "* " marker for current branch.
		line = strings.TrimPrefix(line, "* ")
		branches = append(branches, strings.TrimSpace(line))
	}

	// Query remote branches via ls-remote against origin (local mirror — fast, no creds).
	// No network retry needed since origin is a local path.
	remoteOutput, err := c.gitRunner.Run(ctx, agentWorkDir, "ls-remote", "--heads", "origin")
	if err != nil {
		// Log warning but don't fail - we can still check local branches.
		c.logger.Warn("Failed to query remote branches via ls-remote: %v", err)
		return branches, nil
	}

	// Parse remote branch names from ls-remote output.
	// Format: "<sha>\trefs/heads/<branch-name>"
	for _, line := range strings.Split(string(remoteOutput), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Split on tab to get the ref part.
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		ref := parts[1]
		// Extract branch name from refs/heads/<name>.
		if strings.HasPrefix(ref, "refs/heads/") {
			branchName := strings.TrimPrefix(ref, "refs/heads/")
			branches = append(branches, branchName)
		}
	}

	return branches, nil
}

// branchExists checks if a branch name exists in the list of existing branches.
func (c *CloneManager) branchExists(branchName string, existingBranches []string) bool {
	for _, existing := range existingBranches {
		if existing == branchName {
			return true
		}
	}
	return false
}

// createBranchWithRetry is the fallback method that uses trial-and-error.
//
//nolint:dupl
func (c *CloneManager) createBranchWithRetry(ctx context.Context, agentWorkDir, branchName string) (string, error) {
	originalBranchName := branchName
	attempt := 1
	maxAttempts := 10

	for attempt <= maxAttempts {
		_, err := c.gitRunner.Run(ctx, agentWorkDir, "switch", "-c", branchName)
		if err == nil {
			// Success! Log if we had to use an incremented name.
			if attempt > 1 {
				c.logger.Warn("Branch name collision detected: '%s' already exists, using '%s' instead", originalBranchName, branchName)
			}
			return branchName, nil
		}

		// Check if this is a "branch already exists" error.
		if strings.Contains(err.Error(), "already exists") {
			// Increment the branch name and try again.
			attempt++
			branchName = fmt.Sprintf("%s-%d", originalBranchName, attempt)
			c.logger.Debug("Branch collision detected, trying next name: %s", branchName)
			continue
		}

		// If it's not a collision error, return the original error.
		return "", logx.Wrap(err, fmt.Sprintf("git switch -c %s failed in %s", branchName, agentWorkDir))
	}

	// If we've exhausted all attempts.
	return "", logx.Errorf("unable to create branch after %d attempts, last tried: %s", maxAttempts, branchName)
}

// BuildMirrorPath constructs the mirror repository path.
func (c *CloneManager) BuildMirrorPath() string {
	// Extract repo name from URL (e.g., git@github.com:user/repo.git -> repo).
	repoName := filepath.Base(c.getRepoURL())
	repoName = strings.TrimSuffix(repoName, ".git")

	return filepath.Join(c.projectWorkDir, c.getMirrorDir(), repoName+".git")
}

// BuildAgentWorkDir returns the agent work directory as an absolute path.
func (c *CloneManager) BuildAgentWorkDir(_ /* agentID */, agentWorkDir string) string {
	// Convert agentWorkDir to absolute path.
	absAgentWorkDir, err := filepath.Abs(agentWorkDir)
	if err != nil {
		// If we can't get absolute path, use the original (fallback).
		absAgentWorkDir = agentWorkDir
	}

	return absAgentWorkDir
}

// buildBranchName constructs the branch name using the pattern.
func (c *CloneManager) buildBranchName(storyID string) string {
	return strings.ReplaceAll(c.getBranchPattern(), "{STORY_ID}", storyID)
}

// configureGitIdentity configures git user identity in the workspace on the host.
// This runs before any container mounts to avoid read-only filesystem issues.
func (c *CloneManager) configureGitIdentity(ctx context.Context, agentWorkDir, agentID string) error {
	// Get config values with agent ID substitution
	cfg, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get config: %w", err)
	}

	userName := strings.ReplaceAll(cfg.Git.GitUserName, "{AGENT_ID}", agentID)
	userEmail := strings.ReplaceAll(cfg.Git.GitUserEmail, "{AGENT_ID}", agentID)

	// Configure git user name on host
	_, err = c.gitRunner.Run(ctx, agentWorkDir, "config", "user.name", userName)
	if err != nil {
		return fmt.Errorf("failed to set git user.name: %w", err)
	}

	// Configure git user email on host
	_, err = c.gitRunner.Run(ctx, agentWorkDir, "config", "user.email", userEmail)
	if err != nil {
		return fmt.Errorf("failed to set git user.email: %w", err)
	}

	c.logger.Info("🔧 Configured git user identity on host: %s <%s>", userName, userEmail)
	return nil
}

// GitNetworkError is a sentinel error returned when git network operations
// exhaust all retry attempts. Used by setup.go to trigger SUSPEND.
type GitNetworkError struct {
	Err      error
	Attempts int
}

func (e *GitNetworkError) Error() string {
	return fmt.Sprintf("git network unavailable after %d attempts: %v", e.Attempts, e.Err)
}

func (e *GitNetworkError) Unwrap() error { return e.Err }

// retryGitNetworkOp retries a git operation that may fail due to network issues.
// Uses backoff delays: 0s, 5s, 15s, 30s between attempts.
// Returns GitNetworkError when all network retries are exhausted.
func (c *CloneManager) retryGitNetworkOp(ctx context.Context, dir string, args ...string) ([]byte, error) {
	delays := []time.Duration{0, 5 * time.Second, 15 * time.Second, 30 * time.Second}
	var lastErr error

	for attempt, delay := range delays {
		if delay > 0 {
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("git retry cancelled: %w", ctx.Err())
			case <-time.After(delay):
			}
		}

		result, err := c.gitRunner.Run(ctx, dir, args...)
		if err == nil {
			return result, nil
		}
		lastErr = err

		if !isGitNetworkError(err) {
			return nil, fmt.Errorf("git %s failed: %w", args[0], err)
		}

		c.logger.Warn("Git network error (attempt %d/%d): %v", attempt+1, len(delays), err)
	}

	return nil, &GitNetworkError{Attempts: len(delays), Err: lastErr}
}

// isGitNetworkError checks if a git error is caused by network/connectivity issues.
// Uses positive network patterns with negative exclusions for non-network failures
// that share similar error strings (e.g., "unable to access" in 404/auth errors).
func isGitNetworkError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())

	// Exclude non-network errors that contain network-like substrings
	nonNetworkPatterns := []string{
		"repository not found", "authentication failed", "permission denied",
		"invalid username", "could not find remote branch",
	}
	for _, p := range nonNetworkPatterns {
		if strings.Contains(errStr, p) {
			return false
		}
	}

	networkPatterns := []string{
		"could not read from remote", "connection refused", "connection reset",
		"connection timed out", "no route to host", "operation timed out",
		"name or service not known", "couldn't resolve host",
		"unable to access", "network is unreachable",
		"ssh_exchange_identification", "broken pipe",
	}
	for _, p := range networkPatterns {
		if strings.Contains(errStr, p) {
			return true
		}
	}
	return false
}
