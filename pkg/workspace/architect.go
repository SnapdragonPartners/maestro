// Package workspace provides workspace setup and management functionality.
package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
)

const defaultTargetBranch = "main"

// EnsureArchitectWorkspace ensures the architect workspace exists and is up to date.
// The architect workspace is a full git clone at <projectDir>/architect-001/ that provides
// read-only access to the repository for the architect agent.
//
// This function:
// 1. Creates architect-001/ directory if it doesn't exist.
// 2. Clones from the local mirror (fast, network-efficient).
// 3. Checks out the target branch (main).
// 4. Returns the workspace path.
//
//nolint:cyclop // Complexity from error handling and git operations is acceptable.
func EnsureArchitectWorkspace(ctx context.Context, projectDir string) (string, error) {
	logger := logx.NewLogger("workspace")

	// Get git configuration
	cfg, err := config.GetConfig()
	if err != nil {
		return "", fmt.Errorf("failed to get config: %w", err)
	}

	if cfg.Git == nil {
		return "", fmt.Errorf("git configuration not found")
	}

	targetBranch := cfg.Git.TargetBranch
	if targetBranch == "" {
		targetBranch = defaultTargetBranch
	}

	// Architect workspace path
	architectWorkspace := filepath.Join(projectDir, "architect-001")
	absArchitectWorkspace, absErr := filepath.Abs(architectWorkspace)
	if absErr != nil {
		return "", fmt.Errorf("failed to resolve absolute path for architect workspace: %w", absErr)
	}
	architectWorkspace = absArchitectWorkspace

	// Find the git mirror
	mirrorDir := filepath.Join(projectDir, ".mirrors")
	entries, err := os.ReadDir(mirrorDir)
	if err != nil {
		return "", fmt.Errorf("failed to read mirrors directory: %w", err)
	}

	var gitMirrorPath string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasSuffix(entry.Name(), ".git") {
			gitMirrorPath = filepath.Join(mirrorDir, entry.Name())
			break
		}
	}

	if gitMirrorPath == "" {
		return "", fmt.Errorf("no git mirror found in %s", mirrorDir)
	}

	// Check if workspace already exists
	if stat, statErr := os.Stat(architectWorkspace); statErr == nil && stat.IsDir() {
		// Workspace exists - check if it's a valid git clone
		gitDir := filepath.Join(architectWorkspace, ".git")
		if gitStat, gitStatErr := os.Stat(gitDir); gitStatErr == nil && gitStat.IsDir() {
			// Valid clone exists - update it
			logger.Info("Architect workspace exists, updating to latest %s", targetBranch)

			// Fetch latest changes
			fetchCmd := exec.CommandContext(ctx, "git", "fetch", "--all", "--prune")
			fetchCmd.Dir = architectWorkspace
			if fetchErr := fetchCmd.Run(); fetchErr != nil {
				logger.Warn("Failed to fetch updates: %v", fetchErr)
				// Don't fail - workspace might still be usable
			}

			// Reset to target branch (architect workspace is read-only, safe to hard reset)
			resetCmd := exec.CommandContext(ctx, "git", "reset", "--hard", "origin/"+targetBranch)
			resetCmd.Dir = architectWorkspace
			if resetErr := resetCmd.Run(); resetErr != nil {
				logger.Warn("Failed to reset architect workspace: %v", resetErr)
				// Don't fail - workspace might still be usable
			}

			return architectWorkspace, nil
		}

		// Directory exists but not a valid git clone - remove and recreate
		logger.Warn("Architect workspace exists but is not a valid git clone, recreating")
		if removeErr := os.RemoveAll(architectWorkspace); removeErr != nil {
			return "", fmt.Errorf("failed to remove invalid workspace: %w", removeErr)
		}
	}

	// Create new clone from mirror
	logger.Info("Creating architect workspace clone at %s", architectWorkspace)

	// Clone from local mirror (fast and network-efficient)
	cloneCmd := exec.CommandContext(ctx, "git", "clone", "--branch", targetBranch, gitMirrorPath, architectWorkspace)

	output, err := cloneCmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to clone architect workspace: %w\nOutput: %s", err, string(output))
	}

	logger.Info("Created architect workspace successfully")
	return architectWorkspace, nil
}

// UpdateArchitectWorkspace updates the architect workspace to the latest target branch.
// This should be called after successful merges to main.
func UpdateArchitectWorkspace(ctx context.Context, projectDir string) error {
	logger := logx.NewLogger("workspace")
	architectWorkspace := filepath.Join(projectDir, "architect-001")

	// Check if workspace exists
	if _, err := os.Stat(architectWorkspace); os.IsNotExist(err) {
		// Workspace doesn't exist - create it
		logger.Info("Architect workspace doesn't exist, creating it")
		_, createErr := EnsureArchitectWorkspace(ctx, projectDir)
		return createErr
	}

	// Get target branch
	cfg, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get config: %w", err)
	}

	targetBranch := defaultTargetBranch
	if cfg.Git != nil && cfg.Git.TargetBranch != "" {
		targetBranch = cfg.Git.TargetBranch
	}

	logger.Info("Updating architect workspace to latest %s", targetBranch)

	// Fetch latest changes
	fetchCmd := exec.CommandContext(ctx, "git", "fetch", "--all", "--prune")
	fetchCmd.Dir = architectWorkspace

	if fetchErr := fetchCmd.Run(); fetchErr != nil {
		logger.Warn("Failed to fetch updates: %v", fetchErr)
	}

	// Reset to target branch (architect workspace is read-only, safe to hard reset)
	resetCmd := exec.CommandContext(ctx, "git", "reset", "--hard", "origin/"+targetBranch)
	resetCmd.Dir = architectWorkspace

	output, err := resetCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to update architect workspace: %w\nOutput: %s", err, string(output))
	}

	logger.Info("Updated architect workspace successfully")
	return nil
}
