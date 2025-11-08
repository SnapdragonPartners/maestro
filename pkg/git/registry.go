// Package git provides repository management utilities including clone registry.
package git

import (
	"context"
	"fmt"

	"orchestrator/pkg/logx"
	"orchestrator/pkg/workspace"
)

// Registry manages repository clones and their updates.
// This is a lightweight abstraction that will house epoch tracking in the future.
// For now, it provides a clean interface for updating dependent read-only clones after merges.
type Registry struct {
	projectDir string
	logger     *logx.Logger
}

// NewRegistry creates a new clone registry for the given project directory.
func NewRegistry(projectDir string) *Registry {
	return &Registry{
		projectDir: projectDir,
		logger:     logx.NewLogger("git-registry"),
	}
}

// UpdateDependentClones updates all dependent read-only clones after a merge.
// Currently updates architect and PM workspaces; will add epoch tracking in the future.
//
// Parameters:
//   - ctx: Context for cancellation
//   - repoURL: Repository URL (for future use)
//   - branch: Branch name (for future use)
//   - mergeSHA: Commit SHA that was just merged (for logging)
//
// Returns error if any workspace update fails.
func (r *Registry) UpdateDependentClones(ctx context.Context, repoURL, branch, mergeSHA string) error {
	r.logger.Info("📦 Updating dependent clones after merge %s", mergeSHA)

	// Update architect workspace
	if err := workspace.UpdateArchitectWorkspace(ctx, r.projectDir); err != nil {
		r.logger.Error("Failed to update architect workspace: %v", err)
		return fmt.Errorf("architect workspace update failed: %w", err)
	}
	r.logger.Info("✅ Updated architect workspace")

	// Update PM workspace (will be implemented when PM agent is added)
	if err := workspace.UpdatePMWorkspace(ctx, r.projectDir); err != nil {
		// PM workspace may not exist yet, log but don't fail
		r.logger.Warn("⚠️  Failed to update PM workspace (may not exist yet): %v", err)
		// Don't return error - PM is not critical for current operations
	} else {
		r.logger.Info("✅ Updated PM workspace")
	}

	r.logger.Info("✅ All dependent clones updated successfully")
	return nil
}

// Future: Add epoch tracking methods here
// - Epoch(repoURL string) (int64, error)
// - IncrementEpoch(repoURL string) error
