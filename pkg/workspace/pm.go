package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

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

// EnsurePMWorkspace creates or verifies the PM workspace clone.
// This will be implemented when the PM agent is added to the system.
// For now, this is a placeholder that returns an error if called.
func EnsurePMWorkspace(_ context.Context, projectDir string) (string, error) {
	// TODO: Implement PM workspace creation when PM agent is added
	// This should:
	// 1. Clone from mirror to <projectDir>/pm-001/
	// 2. Checkout target branch
	// 3. Return workspace path

	pmWorkspace := filepath.Join(projectDir, "pm-001")
	return pmWorkspace, fmt.Errorf("PM workspace creation not yet implemented (PM agent coming in future phase)")
}
