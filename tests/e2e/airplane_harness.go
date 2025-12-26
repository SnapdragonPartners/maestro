//go:build e2e

// Airplane test harness provides reusable setup/teardown for airplane mode tests.
package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/forge"
	"orchestrator/pkg/forge/gitea"
	"orchestrator/pkg/mirror"
	"orchestrator/pkg/sync"
)

// AirplaneTestHarness provides reusable setup/teardown for airplane mode tests.
type AirplaneTestHarness struct {
	T              *testing.T
	ProjectDir     string
	GiteaContainer *gitea.ContainerInfo
	ForgeState     *forge.State
	MirrorPath     string

	// Test configuration.
	GitHubTestRepo string // e.g., "https://github.com/SnapdragonPartners/maestro-test"
	CleanupOnDone  bool   // Whether to cleanup resources on teardown.

	// Internal state.
	tempDirs          []string
	containerManager  *gitea.ContainerManager
	containerNames    []string // Track container names for cleanup even if setup fails
}

// HarnessOption configures the test harness.
type HarnessOption func(*AirplaneTestHarness)

// WithGitHubRepo sets the GitHub test repository URL.
func WithGitHubRepo(url string) HarnessOption {
	return func(h *AirplaneTestHarness) {
		h.GitHubTestRepo = url
	}
}

// WithCleanup enables cleanup on teardown.
func WithCleanup(cleanup bool) HarnessOption {
	return func(h *AirplaneTestHarness) {
		h.CleanupOnDone = cleanup
	}
}

// RequireDocker checks if Docker is available and skips the test if not.
// This should be called at the start of any test that requires Docker.
func RequireDocker(t *testing.T) {
	t.Helper()

	cmd := exec.Command("docker", "info")
	if err := cmd.Run(); err != nil {
		t.Skipf("Docker not available, skipping test: %v", err)
	}
}

// NewAirplaneHarness creates a new test harness for airplane mode tests.
func NewAirplaneHarness(t *testing.T, opts ...HarnessOption) *AirplaneTestHarness {
	h := &AirplaneTestHarness{
		T:              t,
		GitHubTestRepo: "https://github.com/SnapdragonPartners/maestro-test",
		CleanupOnDone:  true,
		tempDirs:       make([]string, 0),
	}

	for _, opt := range opts {
		opt(h)
	}

	return h
}

// SetupProjectDir creates a temporary project directory with config.
func (h *AirplaneTestHarness) SetupProjectDir(ctx context.Context) error {
	h.T.Helper()

	// Create temp directory.
	tmpDir, err := os.MkdirTemp("", "airplane-test-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	h.ProjectDir = tmpDir
	h.tempDirs = append(h.tempDirs, tmpDir)

	// Create .maestro directory.
	maestroDir := filepath.Join(tmpDir, ".maestro")
	if err := os.MkdirAll(maestroDir, 0755); err != nil {
		return fmt.Errorf("failed to create .maestro dir: %w", err)
	}

	// Create minimal config.
	configContent := fmt.Sprintf(`{
  "default_mode": "airplane",
  "git": {
    "repo_url": "%s",
    "target_branch": "main"
  },
  "project": {
    "name": "airplane-test"
  }
}`, h.GitHubTestRepo)

	configPath := filepath.Join(maestroDir, "config.json")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	// Load config.
	if err := config.LoadConfig(tmpDir); err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	h.T.Logf("Created project dir: %s", tmpDir)
	return nil
}

// SetupGitea starts a Gitea container for testing.
func (h *AirplaneTestHarness) SetupGitea(ctx context.Context) error {
	h.T.Helper()

	if h.ProjectDir == "" {
		return fmt.Errorf("project dir not set - call SetupProjectDir first")
	}

	// Create container manager.
	h.containerManager = gitea.NewContainerManager()

	// Use the gitea package to ensure container.
	containerCfg := gitea.ContainerConfig{
		ProjectName: "airplane-test",
	}

	// Track container name for cleanup even if EnsureContainer fails after starting the container.
	containerName := gitea.ContainerName(containerCfg.ProjectName)
	h.containerNames = append(h.containerNames, containerName)

	// Clean up any existing container and volume to ensure fresh state.
	// This is needed because reusing a container with stale credentials fails.
	_ = h.containerManager.RemoveContainer(ctx, containerName, true)

	container, err := h.containerManager.EnsureContainer(ctx, containerCfg)
	if err != nil {
		return fmt.Errorf("failed to ensure Gitea container: %w", err)
	}
	h.GiteaContainer = container

	// Wait for Gitea to be ready.
	giteaURL := fmt.Sprintf("http://localhost:%d", container.HTTPPort)
	if err := gitea.WaitForReady(ctx, giteaURL, gitea.DefaultReadyTimeout); err != nil {
		return fmt.Errorf("Gitea not ready: %w", err)
	}

	h.T.Logf("Gitea running at %s (container: %s)", giteaURL, container.Name)
	return nil
}

// SetupRepository creates a repository in Gitea from the mirror.
func (h *AirplaneTestHarness) SetupRepository(ctx context.Context) error {
	h.T.Helper()

	if h.GiteaContainer == nil {
		return fmt.Errorf("Gitea not started - call SetupGitea first")
	}

	// First, create a mirror from GitHub.
	mgr := mirror.NewManager(h.ProjectDir)
	mirrorPath, err := mgr.EnsureMirror(ctx)
	if err != nil {
		return fmt.Errorf("failed to create mirror: %w", err)
	}
	h.MirrorPath = mirrorPath

	// Setup repository in Gitea using SetupManager.
	setupMgr := gitea.NewSetupManager()
	setupCfg := gitea.SetupConfig{
		Container:  h.GiteaContainer,
		RepoName:   "airplane-test",
		MirrorPath: mirrorPath,
	}

	result, err := setupMgr.Setup(ctx, setupCfg)
	if err != nil {
		return fmt.Errorf("failed to setup repository: %w", err)
	}

	// Convert result to forge.State.
	h.ForgeState = &forge.State{
		Provider: string(forge.ProviderGitea),
		URL:      result.URL,
		Token:    result.Token,
		Owner:    result.Owner,
		RepoName: result.RepoName,
	}

	// Save forge state.
	if err := forge.SaveState(h.ProjectDir, h.ForgeState); err != nil {
		return fmt.Errorf("failed to save forge state: %w", err)
	}

	// Wait for Gitea to index the branches after push.
	// This can take a moment after mirroring content.
	client := gitea.NewClient(result.URL, result.Token, result.Owner, result.RepoName)
	if err := client.WaitForBranches(ctx, 30*time.Second); err != nil {
		return fmt.Errorf("failed waiting for branches to be indexed: %w", err)
	}

	h.T.Logf("Repository setup complete: %s/%s", result.Owner, result.RepoName)
	return nil
}

// SetupFull performs complete setup: project dir, Gitea, and repository.
func (h *AirplaneTestHarness) SetupFull(ctx context.Context) error {
	h.T.Helper()

	if err := h.SetupProjectDir(ctx); err != nil {
		return err
	}
	if err := h.SetupGitea(ctx); err != nil {
		return err
	}
	if err := h.SetupRepository(ctx); err != nil {
		return err
	}
	return nil
}

// GetGiteaClient returns a Gitea API client.
func (h *AirplaneTestHarness) GetGiteaClient() (*gitea.Client, error) {
	if h.ForgeState == nil {
		return nil, fmt.Errorf("forge state not set - call SetupRepository first")
	}
	return gitea.NewClient(
		h.ForgeState.URL,
		h.ForgeState.Token,
		h.ForgeState.Owner,
		h.ForgeState.RepoName,
	), nil
}

// GetSyncer returns a configured syncer for testing sync operations.
func (h *AirplaneTestHarness) GetSyncer(dryRun bool) (*sync.Syncer, error) {
	if h.ProjectDir == "" {
		return nil, fmt.Errorf("project dir not set")
	}
	return sync.NewSyncer(h.ProjectDir, dryRun)
}

// CreateTestBranch creates a test branch with a commit.
func (h *AirplaneTestHarness) CreateTestBranch(ctx context.Context, branchName, commitMsg string) error {
	h.T.Helper()

	// Clone from Gitea to a temp dir.
	cloneDir, err := os.MkdirTemp("", "test-clone-*")
	if err != nil {
		return fmt.Errorf("failed to create clone dir: %w", err)
	}
	h.tempDirs = append(h.tempDirs, cloneDir)

	// Use authenticated URL for clone and push (embed token in URL).
	// Format: http://user:token@host/owner/repo.git
	giteaCloneURL := strings.Replace(
		fmt.Sprintf("%s/%s/%s.git", h.ForgeState.URL, h.ForgeState.Owner, h.ForgeState.RepoName),
		"://",
		fmt.Sprintf("://%s:%s@", gitea.DefaultAdminUser, h.ForgeState.Token),
		1,
	)

	// Clone.
	cmd := exec.CommandContext(ctx, "git", "clone", giteaCloneURL, cloneDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone failed: %w\n%s", err, string(out))
	}

	// Create branch.
	cmd = exec.CommandContext(ctx, "git", "checkout", "-b", branchName)
	cmd.Dir = cloneDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout -b failed: %w\n%s", err, string(out))
	}

	// Create a test file.
	// Replace slashes in branch name to avoid creating directories.
	safeFileName := strings.ReplaceAll(branchName, "/", "-")
	testFile := filepath.Join(cloneDir, "test-"+safeFileName+".txt")
	if err := os.WriteFile(testFile, []byte("test content for "+branchName), 0644); err != nil {
		return fmt.Errorf("failed to write test file: %w", err)
	}

	// Add and commit.
	cmd = exec.CommandContext(ctx, "git", "add", ".")
	cmd.Dir = cloneDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git add failed: %w\n%s", err, string(out))
	}

	cmd = exec.CommandContext(ctx, "git", "commit", "-m", commitMsg)
	cmd.Dir = cloneDir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit failed: %w\n%s", err, string(out))
	}

	// Push.
	cmd = exec.CommandContext(ctx, "git", "push", "-u", "origin", branchName)
	cmd.Dir = cloneDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git push failed: %w\n%s", err, string(out))
	}

	// Wait for Gitea to index the new branch.
	client, err := h.GetGiteaClient()
	if err != nil {
		return fmt.Errorf("failed to get client for branch wait: %w", err)
	}
	if err := client.WaitForBranch(ctx, branchName, 10*time.Second); err != nil {
		return fmt.Errorf("branch not indexed: %w", err)
	}

	h.T.Logf("Created test branch: %s", branchName)
	return nil
}

// AssertGiteaHealthy verifies Gitea is responding.
func (h *AirplaneTestHarness) AssertGiteaHealthy(ctx context.Context) {
	h.T.Helper()

	if h.GiteaContainer == nil {
		h.T.Fatal("Gitea container not started")
	}

	giteaURL := fmt.Sprintf("http://localhost:%d", h.GiteaContainer.HTTPPort)
	if err := gitea.WaitForReady(ctx, giteaURL, 10*time.Second); err != nil {
		h.T.Fatalf("Gitea health check failed: %v", err)
	}
}

// AssertPRExists verifies a PR exists for the given branch.
func (h *AirplaneTestHarness) AssertPRExists(ctx context.Context, branch string) {
	h.T.Helper()

	client, err := h.GetGiteaClient()
	if err != nil {
		h.T.Fatalf("Failed to get Gitea client: %v", err)
	}

	prs, err := client.ListPRsForBranch(ctx, branch)
	if err != nil {
		h.T.Fatalf("Failed to list PRs: %v", err)
	}

	if len(prs) == 0 {
		h.T.Fatalf("Expected PR for branch %s, but none found", branch)
	}

	h.T.Logf("Found PR #%d for branch %s", prs[0].Number, branch)
}

// AssertMirrorUpstream verifies the mirror points to the expected URL.
func (h *AirplaneTestHarness) AssertMirrorUpstream(ctx context.Context, expectedURL string) {
	h.T.Helper()

	if h.MirrorPath == "" {
		h.T.Fatal("Mirror path not set")
	}

	cmd := exec.CommandContext(ctx, "git", "remote", "get-url", "origin")
	cmd.Dir = h.MirrorPath
	out, err := cmd.Output()
	if err != nil {
		h.T.Fatalf("Failed to get mirror remote URL: %v", err)
	}

	actualURL := string(out)
	// Trim whitespace.
	actualURL = actualURL[:len(actualURL)-1]

	if actualURL != expectedURL {
		h.T.Fatalf("Mirror upstream mismatch: expected %q, got %q", expectedURL, actualURL)
	}

	h.T.Logf("Mirror upstream verified: %s", expectedURL)
}

// AssertForgeStateProvider verifies the forge state has the expected provider.
func (h *AirplaneTestHarness) AssertForgeStateProvider(expected string) {
	h.T.Helper()

	state, err := forge.LoadState(h.ProjectDir)
	if err != nil {
		h.T.Fatalf("Failed to load forge state: %v", err)
	}

	if state.Provider != expected {
		h.T.Fatalf("Forge provider mismatch: expected %q, got %q", expected, state.Provider)
	}

	h.T.Logf("Forge provider verified: %s", expected)
}

// Cleanup removes all temporary resources.
func (h *AirplaneTestHarness) Cleanup() {
	h.T.Helper()

	if !h.CleanupOnDone {
		h.T.Logf("Skipping cleanup (CleanupOnDone=false)")
		return
	}

	// Stop Gitea containers - use containerNames list to catch containers that failed during setup.
	if h.containerManager != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		for _, containerName := range h.containerNames {
			if err := h.containerManager.StopContainer(ctx, containerName); err != nil {
				h.T.Logf("Warning: failed to stop Gitea container %s: %v", containerName, err)
			} else {
				h.T.Logf("Stopped Gitea container: %s", containerName)
			}
		}
	}

	// Remove temp directories.
	for _, dir := range h.tempDirs {
		if err := os.RemoveAll(dir); err != nil {
			h.T.Logf("Warning: failed to remove temp dir %s: %v", dir, err)
		}
	}

	// Delete forge state.
	if h.ProjectDir != "" {
		_ = forge.DeleteState(h.ProjectDir)
	}

	h.T.Logf("Cleanup complete")
}

// ResetGitHubTestRepo resets the GitHub test repository to a clean state.
// This should be called at the end of tests that modify the repo.
func (h *AirplaneTestHarness) ResetGitHubTestRepo(ctx context.Context) error {
	h.T.Helper()

	// Clone the GitHub repo.
	tmpDir, err := os.MkdirTemp("", "github-reset-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Clone.
	cmd := exec.CommandContext(ctx, "git", "clone", h.GitHubTestRepo, tmpDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone failed: %w\n%s", err, string(out))
	}

	// Get all remote branches.
	cmd = exec.CommandContext(ctx, "git", "branch", "-r")
	cmd.Dir = tmpDir
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("git branch -r failed: %w", err)
	}

	// Delete all branches except main/master.
	// This is a simplified reset - in practice you might want to force-push main to a known state.
	h.T.Logf("Remote branches: %s", string(out))
	h.T.Logf("GitHub test repo reset skipped - implement full reset if needed")

	return nil
}
