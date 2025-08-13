package coder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"orchestrator/pkg/logx"
)

// CloneManager handles Git lightweight clone operations for coder agents.
// Provides perfect container isolation while maintaining object sharing through git clone --shared --reference.
type CloneManager struct {
	gitRunner        GitRunner
	containerManager ContainerManager // Optional container manager for Docker cleanup
	logger           *logx.Logger
	projectWorkDir   string // Project work directory (shared across all agents) - contains mirrors and clones
	repoURL          string
	baseBranch       string
	mirrorDir        string // Mirror directory relative to projectWorkDir (e.g., ".mirrors")
	branchPattern    string // Pattern for branch names (e.g., "story-{STORY_ID}")
}

// NewCloneManager creates a new clone manager.
// projectWorkDir is the root work directory for the entire orchestrator run (shared across agents).
func NewCloneManager(gitRunner GitRunner, projectWorkDir, repoURL, baseBranch, mirrorDir, branchPattern string) *CloneManager {
	// Convert projectWorkDir to absolute path at construction time.
	absProjectWorkDir, err := filepath.Abs(projectWorkDir)
	if err != nil {
		// If we can't get absolute path, use the original (fallback).
		absProjectWorkDir = projectWorkDir
	}

	return &CloneManager{
		gitRunner:        gitRunner,
		projectWorkDir:   absProjectWorkDir,
		repoURL:          repoURL,
		baseBranch:       baseBranch,
		mirrorDir:        mirrorDir,
		branchPattern:    branchPattern,
		containerManager: nil, // Set via SetContainerManager if needed
		logger:           logx.NewLogger("clone-manager"),
	}
}

// SetContainerManager sets the container manager for Docker cleanup operations.
func (c *CloneManager) SetContainerManager(containerManager ContainerManager) {
	c.containerManager = containerManager
}

// CloneResult contains the results of clone setup.
type CloneResult struct {
	WorkDir    string
	BranchName string
}

// SetupWorkspace creates a lightweight git clone with shared objects for container isolation.
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

	// Step 4: Create and checkout branch.
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

// CleanupWorkspace removes the agent clone directory completely after a story.
func (c *CloneManager) CleanupWorkspace(_ context.Context, agentID, storyID, agentWorkDir string) error {
	agentWorkDirPath := c.BuildAgentWorkDir(agentID, agentWorkDir)
	c.logger.Debug("Cleaning up workspace for story %s by removing directory: %s", storyID, agentWorkDirPath)

	// Remove the entire agent work directory.
	if _, err := os.Stat(agentWorkDirPath); err == nil {
		c.logger.Debug("Removing agent work directory: %s", agentWorkDirPath)
		if err := os.RemoveAll(agentWorkDirPath); err != nil {
			return logx.Wrap(err, "failed to remove agent work directory")
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

		// Clone bare repository as object pool.
		_, err := c.gitRunner.Run(ctx, "", "clone", "--bare", c.repoURL, mirrorPath)
		if err != nil {
			return "", logx.Wrap(err, fmt.Sprintf("failed to clone mirror from %s to %s", c.repoURL, mirrorPath))
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

		_, err = c.gitRunner.Run(ctx, mirrorPath, "remote", "update", "--prune")
		if err != nil {
			return "", logx.Wrap(err, fmt.Sprintf("failed to update mirror %s", mirrorPath))
		}
	}

	return mirrorPath, nil
}

// createFreshClone creates a lightweight clone with shared objects for isolation.
func (c *CloneManager) createFreshClone(ctx context.Context, mirrorPath, agentWorkDir string) error {
	c.logger.Debug("Creating fresh lightweight clone at: %s", agentWorkDir)

	// Step 1: Remove existing directory completely if it exists.
	if _, err := os.Stat(agentWorkDir); err == nil {
		c.logger.Debug("Removing existing agent work directory: %s", agentWorkDir)
		if err := os.RemoveAll(agentWorkDir); err != nil {
			return logx.Wrap(err, "failed to remove existing directory")
		}
	}

	// Step 2: Create parent directory if needed.
	if err := os.MkdirAll(filepath.Dir(agentWorkDir), 0755); err != nil {
		return logx.Wrap(err, "failed to create parent directory")
	}

	// Step 3: Create lightweight clone with shared objects.
	// Use --shared and --reference to share objects with the mirror for efficiency.
	c.logger.Debug("Creating lightweight clone from mirror: %s", mirrorPath)
	_, err := c.gitRunner.Run(ctx, "", "clone", "--shared", "--reference", mirrorPath, mirrorPath, agentWorkDir)
	if err != nil {
		return logx.Wrap(err, fmt.Sprintf("git clone --shared --reference %s %s %s failed", mirrorPath, mirrorPath, agentWorkDir))
	}

	// Step 4: Set up remote URL for pushing (clone inherits from bare repo).
	c.logger.Debug("Configuring origin remote for clone: %s", c.repoURL)
	_, err = c.gitRunner.Run(ctx, agentWorkDir, "remote", "set-url", "origin", c.repoURL)
	if err != nil {
		return logx.Wrap(err, "failed to configure origin remote for clone - agent will not be able to push branches")
	}

	c.logger.Debug("Successfully created lightweight clone at: %s", agentWorkDir)
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
func (c *CloneManager) getExistingBranches(ctx context.Context, agentWorkDir string) ([]string, error) {
	// Get all branches (local and remote).
	output, err := c.gitRunner.Run(ctx, agentWorkDir, "branch", "-a")
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
	repoName := filepath.Base(c.repoURL)
	repoName = strings.TrimSuffix(repoName, ".git")

	return filepath.Join(c.projectWorkDir, c.mirrorDir, repoName+".git")
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
	return strings.ReplaceAll(c.branchPattern, "{STORY_ID}", storyID)
}
