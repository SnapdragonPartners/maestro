// Package mirror provides git mirror repository management.
package mirror

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/forge"
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

// GetFetchURL returns the upstream URL based on operating mode.
// In airplane mode, reads from runtime state (forge_state.json).
// In standard mode, returns the configured GitHub URL.
func (m *Manager) GetFetchURL() (string, error) {
	if config.IsAirplaneMode() {
		// Load forge state to get Gitea URL
		state, err := forge.LoadState(m.projectDir)
		if err != nil {
			// Gitea not yet configured - fall back to GitHub URL
			// This can happen during initial setup before Gitea is ready
			m.logger.Debug("Forge state not found, using GitHub URL: %v", err)
			return m.getGitHubURL()
		}
		// Return Gitea clone URL
		return fmt.Sprintf("%s/%s/%s.git", state.URL, state.Owner, state.RepoName), nil
	}
	return m.getGitHubURL()
}

// getGitHubURL returns the GitHub repository URL from config.
func (m *Manager) getGitHubURL() (string, error) {
	cfg, err := config.GetConfig()
	if err != nil {
		return "", fmt.Errorf("failed to get config: %w", err)
	}
	if cfg.Git == nil || cfg.Git.RepoURL == "" {
		return "", fmt.Errorf("no git repository configured")
	}
	return cfg.Git.RepoURL, nil
}

// RefreshFromForge updates the mirror from the current forge (GitHub or Gitea).
// This should be called after PR merges to ensure the mirror has the latest changes.
func (m *Manager) RefreshFromForge(ctx context.Context) error {
	mirrorPath, err := m.GetMirrorPath()
	if err != nil {
		return fmt.Errorf("failed to get mirror path: %w", err)
	}

	if !mirrorExists(mirrorPath) {
		return fmt.Errorf("mirror does not exist at %s", mirrorPath)
	}

	// Get the current fetch URL
	fetchURL, err := m.GetFetchURL()
	if err != nil {
		return fmt.Errorf("failed to get fetch URL: %w", err)
	}

	// Ensure the remote URL is correct
	if err := m.ensureRemoteURL(ctx, mirrorPath, fetchURL); err != nil {
		return fmt.Errorf("failed to update remote URL: %w", err)
	}

	// Fetch updates
	m.logger.Info("📥 Refreshing mirror from %s...", fetchURL)
	if err := updateGitMirror(ctx, mirrorPath); err != nil {
		return fmt.Errorf("failed to update mirror: %w", err)
	}

	m.logger.Info("✅ Mirror refreshed successfully")
	return nil
}

// SwitchUpstream changes the mirror's upstream to a new URL.
// This is used when switching between standard and airplane modes.
func (m *Manager) SwitchUpstream(ctx context.Context, newURL string) error {
	mirrorPath, err := m.GetMirrorPath()
	if err != nil {
		return fmt.Errorf("failed to get mirror path: %w", err)
	}

	if !mirrorExists(mirrorPath) {
		return fmt.Errorf("mirror does not exist at %s", mirrorPath)
	}

	m.logger.Info("🔄 Switching mirror upstream to %s", newURL)

	// Update the remote URL
	if err := m.ensureRemoteURL(ctx, mirrorPath, newURL); err != nil {
		return fmt.Errorf("failed to update remote URL: %w", err)
	}

	// Fetch from new upstream
	if err := updateGitMirror(ctx, mirrorPath); err != nil {
		return fmt.Errorf("failed to fetch from new upstream: %w", err)
	}

	m.logger.Info("✅ Mirror upstream switched successfully")
	return nil
}

// ensureRemoteURL ensures the mirror's origin remote points to the given URL.
func (m *Manager) ensureRemoteURL(ctx context.Context, mirrorPath, targetURL string) error {
	// Get current remote URL
	cmd := exec.CommandContext(ctx, "git", "remote", "get-url", "origin")
	cmd.Dir = mirrorPath
	output, err := cmd.CombinedOutput()

	currentURL := strings.TrimSpace(string(output))

	if err != nil || currentURL != targetURL {
		// Update remote URL
		m.logger.Debug("Updating remote URL from %s to %s", currentURL, targetURL)
		setCmd := exec.CommandContext(ctx, "git", "remote", "set-url", "origin", targetURL)
		setCmd.Dir = mirrorPath
		if setOutput, setErr := setCmd.CombinedOutput(); setErr != nil {
			return fmt.Errorf("git remote set-url failed: %w\nOutput: %s", setErr, string(setOutput))
		}
	}

	return nil
}

// GetMirrorPath returns the path to the mirror directory.
// This extracts the repository name from the configured URL.
func (m *Manager) GetMirrorPath() (string, error) {
	cfg, err := config.GetConfig()
	if err != nil {
		return "", fmt.Errorf("failed to get config: %w", err)
	}

	if cfg.Git == nil || cfg.Git.RepoURL == "" {
		return "", fmt.Errorf("no git repository configured")
	}

	repoName := extractRepoName(cfg.Git.RepoURL)
	return filepath.Join(m.projectDir, ".mirrors", repoName), nil
}

// EnsureMirror creates or updates a git mirror.
// Reads repository URL from config.GetConfig().Git.RepoURL.
// In airplane mode with existing mirror, updates from Gitea instead of GitHub.
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
	err = os.MkdirAll(mirrorDir, 0755)
	if err != nil {
		return "", fmt.Errorf("failed to create .mirrors directory: %w", err)
	}

	// Extract repo name from URL for mirror directory
	repoName := extractRepoName(repoURL)
	repoMirrorPath := filepath.Join(mirrorDir, repoName)

	// Check if mirror already exists by looking for HEAD file (bare repos don't have .git subdir)
	if mirrorExists(repoMirrorPath) {
		// Mirror exists - update it from current forge (mode-aware)
		fetchURL, fetchErr := m.GetFetchURL()
		if fetchErr != nil {
			// Fall back to configured URL if we can't determine fetch URL
			fetchURL = repoURL
		}

		m.logger.Info("📂 Git mirror exists at %s, updating from %s...", repoMirrorPath, fetchURL)

		// Ensure remote URL is correct for current mode
		if updateErr := m.ensureRemoteURL(ctx, repoMirrorPath, fetchURL); updateErr != nil {
			return "", fmt.Errorf("failed to update remote URL: %w", updateErr)
		}

		err = updateGitMirror(ctx, repoMirrorPath)
		if err != nil {
			return "", fmt.Errorf("failed to update git mirror: %w", err)
		}
		m.logger.Info("✅ Git mirror updated successfully")
	} else {
		// Clone as bare mirror
		m.logger.Info("📥 Creating git mirror for %s...", repoURL)
		err = cloneGitMirror(ctx, repoURL, repoMirrorPath)
		if err != nil {
			return "", fmt.Errorf("failed to create git mirror: %w", err)
		}
		m.logger.Info("✅ Git mirror created at %s", repoMirrorPath)
	}

	// Check if mirror is empty (no commits)
	isEmpty, err := m.isEmptyMirror(repoMirrorPath)
	if err != nil {
		return "", fmt.Errorf("failed to check if mirror is empty: %w", err)
	}

	if isEmpty {
		m.logger.Info("📝 Empty repository detected, creating initial commit...")
		err = m.initializeEmptyRepository(ctx, repoMirrorPath)
		if err != nil {
			return "", fmt.Errorf("failed to initialize empty repository: %w", err)
		}
		m.logger.Info("✅ Initial commit created and pushed to GitHub")
	}

	// Detect and update the default branch in config if not already set correctly
	err = m.updateDefaultBranch(ctx, repoMirrorPath)
	if err != nil {
		m.logger.Warn("Failed to detect default branch: %v (will use config value)", err)
	}

	return repoMirrorPath, nil
}

// updateDefaultBranch detects the default branch from the mirror and updates config if needed.
func (m *Manager) updateDefaultBranch(ctx context.Context, mirrorPath string) error {
	// For bare mirror repos, read HEAD directly
	cmd := exec.CommandContext(ctx, "git", "symbolic-ref", "HEAD")
	cmd.Dir = mirrorPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to get default branch: %w\nOutput: %s", err, string(output))
	}

	// Parse output: "refs/heads/main" -> "main" or "refs/heads/master" -> "master"
	defaultBranch := strings.TrimSpace(string(output))
	defaultBranch = strings.TrimPrefix(defaultBranch, "refs/heads/")

	if defaultBranch == "" {
		return fmt.Errorf("could not parse default branch from: %s", string(output))
	}

	m.logger.Info("📍 Detected default branch: %s", defaultBranch)

	// Update config if the target branch doesn't match
	cfg, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get config: %w", err)
	}

	if cfg.Git != nil && cfg.Git.TargetBranch != defaultBranch {
		m.logger.Info("Updating config target branch from %s to %s", cfg.Git.TargetBranch, defaultBranch)
		cfg.Git.TargetBranch = defaultBranch
		if err := config.UpdateGit(cfg.Git); err != nil {
			return fmt.Errorf("failed to update target branch: %w", err)
		}
	}

	return nil
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

// isEmptyMirror checks if a bare mirror repository has any commits.
// Returns true if the repository has no refs in refs/heads/.
func (m *Manager) isEmptyMirror(mirrorPath string) (bool, error) {
	// Use git rev-list to check if any commits exist
	// This is more reliable than checking refs since it directly checks for commit history
	cmd := exec.Command("git", "rev-list", "--all", "--count")
	cmd.Dir = mirrorPath
	output, err := cmd.CombinedOutput()

	if err != nil {
		// Check if this is a "does not have any commits yet" error (exit code 0, output "0")
		// vs an actual git failure. For truly empty repos, git rev-list still succeeds.
		return false, fmt.Errorf("git rev-list failed: %w\nOutput: %s", err, string(output))
	}

	// Parse commit count
	commitCountStr := strings.TrimSpace(string(output))
	if commitCountStr == "" || commitCountStr == "0" {
		return true, nil
	}

	// Repository has commits, not empty
	return false, nil
}

// initializeEmptyRepository creates an initial commit in an empty repository.
// This creates a temporary workspace, initializes git, commits MAESTRO.md,
// pushes to GitHub, and updates the mirror.
//
//nolint:cyclop // Sequential git operations for repository initialization
func (m *Manager) initializeEmptyRepository(ctx context.Context, mirrorPath string) error {
	// Get configuration for repository URL, default branch, and project info
	cfg, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get config: %w", err)
	}

	repoURL := cfg.Git.RepoURL
	defaultBranch := cfg.Git.TargetBranch
	if defaultBranch == "" {
		defaultBranch = "main" // Default to main if not set
	}

	// Get project name and description for MAESTRO.md
	projectName := "Project"
	projectDescription := ""
	if cfg.Project != nil {
		if cfg.Project.Name != "" {
			projectName = cfg.Project.Name
		}
		projectDescription = cfg.Project.Description
	}

	// Create temporary directory in <projectDir>/.tmp/
	tempDir := filepath.Join(m.projectDir, ".tmp", fmt.Sprintf("init-%d", time.Now().UnixNano()))
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}

	// Ensure cleanup on exit
	defer func() {
		m.logger.Debug("Cleaning up temp init directory: %s", tempDir)
		if err := os.RemoveAll(tempDir); err != nil {
			m.logger.Warn("Failed to clean up temp init directory: %v", err)
		}
	}()

	// Initialize fresh git repository
	m.logger.Debug("Initializing git repository in %s", tempDir)
	initCmd := exec.CommandContext(ctx, "git", "init")
	initCmd.Dir = tempDir
	if output, err := initCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git init failed: %w\nOutput: %s", err, string(output))
	}

	// Checkout default branch
	m.logger.Debug("Creating branch: %s", defaultBranch)
	checkoutCmd := exec.CommandContext(ctx, "git", "checkout", "-b", defaultBranch)
	checkoutCmd.Dir = tempDir
	if output, err := checkoutCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout -b %s failed: %w\nOutput: %s", defaultBranch, err, string(output))
	}

	// Create .maestro directory and MAESTRO.md
	maestroDir := filepath.Join(tempDir, ".maestro")
	if err := os.MkdirAll(maestroDir, 0755); err != nil {
		return fmt.Errorf("failed to create .maestro directory: %w", err)
	}

	// Build MAESTRO.md content with project info
	var descriptionSection string
	if projectDescription != "" {
		descriptionSection = "\n" + projectDescription + "\n"
	} else {
		descriptionSection = "\nThis project is managed by Maestro, an AI-powered development orchestrator.\n"
	}

	maestroContent := fmt.Sprintf(`# %s
%s
## Development

- Architecture: Managed by architect agent
- Implementation: Executed by coder agents
- Quality: Enforced through automated review and testing

For more information about Maestro, visit: https://github.com/anthropics/maestro
`, projectName, descriptionSection)
	maestroPath := filepath.Join(maestroDir, "MAESTRO.md")
	if err := os.WriteFile(maestroPath, []byte(maestroContent), 0644); err != nil {
		return fmt.Errorf("failed to create MAESTRO.md: %w", err)
	}

	// Stage and commit the file
	m.logger.Debug("Committing .maestro/MAESTRO.md")
	addCmd := exec.CommandContext(ctx, "git", "add", ".maestro/MAESTRO.md")
	addCmd.Dir = tempDir
	if output, err := addCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git add failed: %w\nOutput: %s", err, string(output))
	}

	commitCmd := exec.CommandContext(ctx, "git", "commit", "-m", "Initial Maestro project setup")
	commitCmd.Dir = tempDir
	if output, err := commitCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit failed: %w\nOutput: %s", err, string(output))
	}

	// Add remote and push to GitHub
	m.logger.Debug("Pushing to GitHub: %s", repoURL)
	remoteCmd := exec.CommandContext(ctx, "git", "remote", "add", "origin", repoURL)
	remoteCmd.Dir = tempDir
	if output, err := remoteCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git remote add failed: %w\nOutput: %s", err, string(output))
	}

	pushCmd := exec.CommandContext(ctx, "git", "push", "-u", "origin", defaultBranch)
	pushCmd.Dir = tempDir
	if output, err := pushCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git push failed (check GITHUB_TOKEN): %w\nOutput: %s", err, string(output))
	}

	// Update mirror to fetch the new commit
	m.logger.Debug("Updating mirror with new commit")
	updateCmd := exec.CommandContext(ctx, "git", "remote", "update")
	updateCmd.Dir = mirrorPath
	if output, err := updateCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git remote update failed: %w\nOutput: %s", err, string(output))
	}

	return nil
}
