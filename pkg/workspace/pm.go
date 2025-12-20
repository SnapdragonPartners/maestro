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

// UpdatePMWorkspace updates the PM workspace after a merge.
// This pulls the latest changes from the mirror into the PM's read-only clone.
//
// The PM workspace is located at <projectDir>/pm-001/ and should already exist
// (created during startup). This function updates it to the latest commit.
func UpdatePMWorkspace(ctx context.Context, projectDir string) error {
	logger := logx.NewLogger("workspace-pm")

	// PM workspace path
	pmWorkspace := filepath.Join(projectDir, "pm-001")

	// Check if PM workspace exists
	if _, err := os.Stat(pmWorkspace); os.IsNotExist(err) {
		// PM workspace doesn't exist yet - this is not an error, just log it
		logger.Debug("PM workspace does not exist yet: %s", pmWorkspace)
		return fmt.Errorf("PM workspace not found at %s (will be created when PM agent is implemented)", pmWorkspace)
	}

	logger.Info("Updating PM workspace at %s", pmWorkspace)

	// Get config for target branch
	cfg, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get config: %w", err)
	}

	targetBranch := cfg.Git.TargetBranch
	if targetBranch == "" {
		targetBranch = config.DefaultTargetBranch
	}

	// Fetch all refs from origin
	fetchCmd := exec.CommandContext(ctx, "git", "-C", pmWorkspace, "fetch", "--all", "--prune")
	if output, err := fetchCmd.CombinedOutput(); err != nil {
		logger.Error("Failed to fetch in PM workspace: %s", string(output))
		return fmt.Errorf("git fetch failed in PM workspace: %w", err)
	}

	// Reset to latest commit on target branch
	resetCmd := exec.CommandContext(ctx, "git", "-C", pmWorkspace, "reset", "--hard", fmt.Sprintf("origin/%s", targetBranch))
	if output, err := resetCmd.CombinedOutput(); err != nil {
		logger.Error("Failed to reset PM workspace: %s", string(output))
		return fmt.Errorf("git reset failed in PM workspace: %w", err)
	}

	logger.Info("✅ PM workspace updated successfully to latest %s", targetBranch)
	return nil
}

// EnsurePMWorkspace ensures the PM workspace exists and is up to date.
// The PM workspace can operate in two modes:
//
//  1. With Repository: A full git clone at <projectDir>/pm-001/ that provides
//     read-only access to the repository for the PM agent during interviews.
//  2. Without Repository: A minimal empty directory for conducting interviews
//     when no git repository has been configured yet (bootstrap mode).
//
// This function:
// 1. Creates pm-001/ directory if it doesn't exist.
// 2. If git is configured: Clones from the local mirror (fast, network-efficient).
// 3. If no git: Returns empty workspace for interview-only mode.
// 4. Returns the workspace path.
//
//nolint:cyclop,dupl // Complexity and duplication acceptable - mirrors architect workspace pattern.
func EnsurePMWorkspace(ctx context.Context, projectDir string) (string, error) {
	logger := logx.NewLogger("workspace-pm")

	// Get git configuration
	cfg, err := config.GetConfig()
	if err != nil {
		return "", fmt.Errorf("failed to get config: %w", err)
	}

	// PM workspace path (always the same location)
	pmWorkspace := filepath.Join(projectDir, "pm-001")
	absPMWorkspace, absErr := filepath.Abs(pmWorkspace)
	if absErr != nil {
		return "", fmt.Errorf("failed to resolve absolute path for PM workspace: %w", absErr)
	}
	pmWorkspace = absPMWorkspace

	// Check if git is configured with a repository URL
	hasGitRepo := cfg.Git != nil && cfg.Git.RepoURL != ""

	if !hasGitRepo {
		// No git repository configured - create minimal workspace for interview-only mode
		logger.Info("No git repository configured, creating minimal PM workspace for interviews")

		// Create workspace directory if it doesn't exist
		if mkdirErr := os.MkdirAll(pmWorkspace, 0755); mkdirErr != nil {
			return "", fmt.Errorf("failed to create PM workspace directory: %w", mkdirErr)
		}

		logger.Info("✅ Created minimal PM workspace (no-repo mode) at %s", pmWorkspace)
		return pmWorkspace, nil
	}

	// Git is configured - proceed with repository clone
	targetBranch := cfg.Git.TargetBranch
	if targetBranch == "" {
		targetBranch = config.DefaultTargetBranch
	}

	// Find the git mirror
	mirrorDir := filepath.Join(projectDir, ".mirrors")

	// Check if mirrors directory exists - if not, bootstrap hasn't run yet.
	// Create minimal workspace for now; PM can conduct interviews without a clone.
	// RefreshMirrorAndWorkspaces will populate the workspace after bootstrap creates the mirror.
	if _, statErr := os.Stat(mirrorDir); os.IsNotExist(statErr) {
		logger.Info("Git configured but mirror not yet created - creating minimal PM workspace")

		// Create workspace directory if it doesn't exist
		if mkdirErr := os.MkdirAll(pmWorkspace, 0755); mkdirErr != nil {
			return "", fmt.Errorf("failed to create PM workspace directory: %w", mkdirErr)
		}

		logger.Info("✅ Created minimal PM workspace (pre-bootstrap mode) at %s", pmWorkspace)
		return pmWorkspace, nil
	}

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
	if stat, statErr := os.Stat(pmWorkspace); statErr == nil && stat.IsDir() {
		// Workspace exists - check if it's a valid git clone
		gitDir := filepath.Join(pmWorkspace, ".git")
		if gitStat, gitStatErr := os.Stat(gitDir); gitStatErr == nil && gitStat.IsDir() {
			// Valid clone exists - update it
			logger.Info("PM workspace exists, updating to latest %s", targetBranch)

			// Fetch latest changes
			fetchCmd := exec.CommandContext(ctx, "git", "fetch", "--all", "--prune")
			fetchCmd.Dir = pmWorkspace
			if fetchErr := fetchCmd.Run(); fetchErr != nil {
				logger.Warn("Failed to fetch updates: %v", fetchErr)
				// Don't fail - workspace might still be usable
			}

			// Reset to target branch (PM workspace is read-only, safe to hard reset)
			resetCmd := exec.CommandContext(ctx, "git", "reset", "--hard", "origin/"+targetBranch)
			resetCmd.Dir = pmWorkspace
			if resetErr := resetCmd.Run(); resetErr != nil {
				logger.Warn("Failed to reset PM workspace: %v", resetErr)
				// Don't fail - workspace might still be usable
			}

			return pmWorkspace, nil
		}

		// Directory exists but not a valid git clone - remove and recreate
		logger.Warn("PM workspace exists but is not a valid git clone, recreating")
		if removeErr := os.RemoveAll(pmWorkspace); removeErr != nil {
			return "", fmt.Errorf("failed to remove invalid workspace: %w", removeErr)
		}
	}

	// Create new clone from mirror
	logger.Info("Creating PM workspace clone at %s", pmWorkspace)

	// Clone from local mirror (fast and network-efficient)
	cloneCmd := exec.CommandContext(ctx, "git", "clone", "--branch", targetBranch, gitMirrorPath, pmWorkspace)

	output, err := cloneCmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to clone PM workspace: %w\nOutput: %s", err, string(output))
	}

	logger.Info("✅ Created PM workspace successfully")
	return pmWorkspace, nil
}
