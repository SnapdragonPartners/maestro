// Package mirror provides git mirror repository management.
package mirror

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

// Manager handles git mirror repository operations.
type Manager struct {
	logger     *logx.Logger
	projectDir string
}

// NewManager creates a new mirror manager for the given project directory.
func NewManager(projectDir string) *Manager {
	return &Manager{
		logger:     logx.NewLogger("mirror"),
		projectDir: projectDir,
	}
}

// EnsureMirror creates or updates a git mirror.
// Reads repository URL from config.GetConfig().Git.RepoURL.
// Returns the path to the mirror directory.
func (m *Manager) EnsureMirror(ctx context.Context) (string, error) {
	// Get git configuration
	cfg, err := config.GetConfig()
	if err != nil {
		return "", fmt.Errorf("failed to get config: %w", err)
	}

	if cfg.Git == nil || cfg.Git.RepoURL == "" {
		return "", fmt.Errorf("no git repository configured")
	}

	repoURL := cfg.Git.RepoURL

	// Create .mirrors directory
	mirrorDir := filepath.Join(m.projectDir, ".mirrors")
	if err := os.MkdirAll(mirrorDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create .mirrors directory: %w", err)
	}

	// Extract repo name from URL for mirror directory
	repoName := extractRepoName(repoURL)
	repoMirrorPath := filepath.Join(mirrorDir, repoName)

	// Check if mirror already exists by looking for HEAD file (bare repos don't have .git subdir)
	if mirrorExists(repoMirrorPath) {
		// Mirror exists - update it
		m.logger.Info("📂 Git mirror exists at %s, updating...", repoMirrorPath)
		if err := updateGitMirror(ctx, repoMirrorPath); err != nil {
			return "", fmt.Errorf("failed to update git mirror: %w", err)
		}
		m.logger.Info("✅ Git mirror updated successfully")
	} else {
		// Clone as bare mirror
		m.logger.Info("📥 Creating git mirror for %s...", repoURL)
		if err := cloneGitMirror(ctx, repoURL, repoMirrorPath); err != nil {
			return "", fmt.Errorf("failed to create git mirror: %w", err)
		}
		m.logger.Info("✅ Git mirror created at %s", repoMirrorPath)
	}

	return repoMirrorPath, nil
}

// extractRepoName extracts the repository name from a git URL.
func extractRepoName(repoURL string) string {
	// Remove .git suffix if present
	repoURL = strings.TrimSuffix(repoURL, ".git")

	// Extract the last path component
	parts := strings.Split(repoURL, "/")
	if len(parts) == 0 {
		return "repo.git"
	}

	repoName := parts[len(parts)-1]
	if repoName == "" {
		return "repo.git"
	}

	// Add .git suffix for mirror directory name
	return repoName + ".git"
}

// mirrorExists checks if a git mirror exists at the given path.
func mirrorExists(mirrorPath string) bool {
	// Bare repos have HEAD file at the root
	_, err := os.Stat(filepath.Join(mirrorPath, "HEAD"))
	return err == nil
}

// cloneGitMirror creates a bare git mirror clone of the repository.
func cloneGitMirror(ctx context.Context, repoURL, mirrorPath string) error {
	cmd := exec.CommandContext(ctx, "git", "clone", "--mirror", repoURL, mirrorPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone --mirror failed: %w\nOutput: %s", err, string(output))
	}
	return nil
}

// updateGitMirror updates an existing bare mirror repository.
func updateGitMirror(ctx context.Context, mirrorPath string) error {
	cmd := exec.CommandContext(ctx, "git", "remote", "update")
	cmd.Dir = mirrorPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git remote update failed: %w\nOutput: %s", err, string(output))
	}
	return nil
}
