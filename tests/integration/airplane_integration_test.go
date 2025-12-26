//go:build e2e

// Package integration contains integration tests for airplane mode components.
// These tests require Docker and external services, so they use the e2e build tag.
package integration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/forge"
	"orchestrator/pkg/forge/gitea"
	"orchestrator/pkg/mirror"
	"orchestrator/pkg/sync"
)

// TestGiteaContainerLifecycle tests starting and stopping a Gitea container.
func TestGiteaContainerLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping container lifecycle test in short mode")
	}
	requireDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	projectName := fmt.Sprintf("test-%d", time.Now().UnixNano())

	// Create container manager
	mgr := gitea.NewContainerManager()

	// Create a container configuration
	cfg := gitea.ContainerConfig{
		ProjectName: projectName,
	}

	// Start container
	t.Log("Starting Gitea container...")
	container, err := mgr.EnsureContainer(ctx, cfg)
	if err != nil {
		t.Fatalf("Failed to start Gitea container: %v", err)
	}

	containerName := container.Name
	t.Logf("Container started: %s (port: %d)", containerName, container.HTTPPort)

	// Verify container is running
	if container.HTTPPort == 0 {
		t.Error("Container port should not be 0")
	}

	// Wait for Gitea to be healthy
	giteaURL := gitea.GetContainerURL(container.HTTPPort)
	t.Logf("Waiting for Gitea at %s...", giteaURL)

	if err := gitea.WaitForReady(ctx, giteaURL, 60*time.Second); err != nil {
		t.Fatalf("Gitea not ready: %v", err)
	}

	t.Log("Gitea is healthy")

	// Verify health check works
	if !gitea.IsHealthy(ctx, giteaURL) {
		t.Error("IsHealthy should return true for running container")
	}

	// Clean up - stop container
	t.Log("Stopping Gitea container...")
	if err := mgr.StopContainer(ctx, containerName); err != nil {
		t.Errorf("Failed to stop container: %v", err)
	}

	// Verify container is stopped
	time.Sleep(1 * time.Second) // Allow time for container to stop
	if gitea.IsHealthy(ctx, giteaURL) {
		t.Error("IsHealthy should return false after container stop")
	}

	t.Log("Container lifecycle test passed")
}

// TestGiteaClientPROperations tests PR operations with a live Gitea instance.
func TestGiteaClientPROperations(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping PR operations test in short mode")
	}
	requireDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Setup temporary project directory
	tmpDir := t.TempDir()
	projectName := fmt.Sprintf("prtest-%d", time.Now().UnixNano())

	// Create minimal config
	if err := setupTestConfig(tmpDir, "https://github.com/SnapdragonPartners/maestro-test"); err != nil {
		t.Fatalf("Failed to setup test config: %v", err)
	}

	// Create container manager
	containerMgr := gitea.NewContainerManager()
	containerName := gitea.ContainerName(projectName)

	// Defer cleanup using container name (in case EnsureContainer fails after starting)
	defer func() {
		_ = containerMgr.StopContainer(context.Background(), containerName)
	}()

	// Start Gitea container
	t.Log("Starting Gitea container...")
	container, err := containerMgr.EnsureContainer(ctx, gitea.ContainerConfig{
		ProjectName: projectName,
	})
	if err != nil {
		t.Fatalf("Failed to start Gitea: %v", err)
	}

	giteaURL := gitea.GetContainerURL(container.HTTPPort)
	if err := gitea.WaitForReady(ctx, giteaURL, 60*time.Second); err != nil {
		t.Fatalf("Gitea not ready: %v", err)
	}

	// Create test repository with initial content
	mirrorPath := filepath.Join(tmpDir, ".mirrors", "test-repo.git")
	if err := createBareTestRepo(ctx, mirrorPath); err != nil {
		t.Fatalf("Failed to create test mirror: %v", err)
	}

	// Setup repository using SetupManager
	t.Log("Setting up Gitea repository...")
	setupMgr := gitea.NewSetupManager()
	setupCfg := gitea.SetupConfig{
		Container:  container,
		RepoName:   projectName,
		MirrorPath: mirrorPath,
	}

	result, err := setupMgr.Setup(ctx, setupCfg)
	if err != nil {
		t.Fatalf("Failed to setup repository: %v", err)
	}

	t.Logf("Repository created: %s/%s", result.Owner, result.RepoName)

	// Create forge state for helper functions
	state := &forge.State{
		Provider: string(forge.ProviderGitea),
		URL:      result.URL,
		Token:    result.Token,
		Owner:    result.Owner,
		RepoName: result.RepoName,
	}

	// Create Gitea client
	client := gitea.NewClient(giteaURL, result.Token, result.Owner, result.RepoName)

	// Create a test branch with a commit
	branchName := fmt.Sprintf("test-branch-%d", time.Now().Unix())
	if err := createTestBranchInGitea(ctx, state, branchName); err != nil {
		t.Fatalf("Failed to create test branch: %v", err)
	}

	// Create a PR
	t.Log("Creating pull request...")
	pr, err := client.CreatePR(ctx, forge.PRCreateOptions{
		Title: "Test PR",
		Body:  "This is a test PR from integration tests",
		Head:  branchName,
		Base:  "main",
	})
	if err != nil {
		t.Fatalf("Failed to create PR: %v", err)
	}

	t.Logf("PR created: #%d", pr.Number)

	// List PRs for branch
	prs, err := client.ListPRsForBranch(ctx, branchName)
	if err != nil {
		t.Fatalf("Failed to list PRs: %v", err)
	}

	if len(prs) != 1 {
		t.Errorf("Expected 1 PR, got %d", len(prs))
	}

	// Get PR by number
	fetchedPR, err := client.GetPR(ctx, fmt.Sprintf("%d", pr.Number))
	if err != nil {
		t.Fatalf("Failed to get PR: %v", err)
	}

	if fetchedPR.Title != "Test PR" {
		t.Errorf("Expected title 'Test PR', got %s", fetchedPR.Title)
	}

	// Close the PR
	if err := client.ClosePR(ctx, fmt.Sprintf("%d", pr.Number)); err != nil {
		t.Fatalf("Failed to close PR: %v", err)
	}

	t.Log("PR operations test passed")
}

// TestMirrorUpstreamSwitch tests switching the mirror upstream URL.
func TestMirrorUpstreamSwitch(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping mirror switch test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	tmpDir := t.TempDir()
	gitHubURL := "https://github.com/SnapdragonPartners/maestro-test"

	// Setup config
	if err := setupTestConfig(tmpDir, gitHubURL); err != nil {
		t.Fatalf("Failed to setup test config: %v", err)
	}

	// Create initial mirror from GitHub
	mgr := mirror.NewManager(tmpDir)
	mirrorPath, err := mgr.EnsureMirror(ctx)
	if err != nil {
		t.Fatalf("Failed to create mirror: %v", err)
	}

	t.Logf("Mirror created at: %s", mirrorPath)

	// Verify initial upstream
	initialURL, err := getRemoteURL(ctx, mirrorPath)
	if err != nil {
		t.Fatalf("Failed to get remote URL: %v", err)
	}

	if initialURL != gitHubURL {
		t.Errorf("Expected initial URL %s, got %s", gitHubURL, initialURL)
	}

	// Switch to a different URL (simulating Gitea)
	giteaURL := "http://localhost:3000/maestro/test-repo.git"
	if err := mgr.SwitchUpstream(ctx, giteaURL); err != nil {
		// Note: This will fail the fetch since Gitea isn't running
		// but we can still verify the URL was changed
		t.Logf("SwitchUpstream error (expected): %v", err)
	}

	// Check URL was changed
	newURL, err := getRemoteURL(ctx, mirrorPath)
	if err != nil {
		t.Fatalf("Failed to get remote URL after switch: %v", err)
	}

	if newURL != giteaURL {
		t.Errorf("Expected switched URL %s, got %s", giteaURL, newURL)
	}

	// Switch back to GitHub
	if err := mgr.SwitchUpstream(ctx, gitHubURL); err != nil {
		t.Fatalf("Failed to switch back to GitHub: %v", err)
	}

	finalURL, err := getRemoteURL(ctx, mirrorPath)
	if err != nil {
		t.Fatalf("Failed to get remote URL after final switch: %v", err)
	}

	if finalURL != gitHubURL {
		t.Errorf("Expected final URL %s, got %s", gitHubURL, finalURL)
	}

	t.Log("Mirror upstream switch test passed")
}

// TestSyncDryRun tests the sync command in dry-run mode.
func TestSyncDryRun(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping sync dry-run test in short mode")
	}
	requireDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	tmpDir := t.TempDir()
	gitHubURL := "https://github.com/SnapdragonPartners/maestro-test"
	projectName := fmt.Sprintf("synctest-%d", time.Now().UnixNano())

	// Setup config
	if err := setupTestConfig(tmpDir, gitHubURL); err != nil {
		t.Fatalf("Failed to setup test config: %v", err)
	}

	// Create container manager
	containerMgr := gitea.NewContainerManager()
	containerName := gitea.ContainerName(projectName)

	// Defer cleanup using container name (in case EnsureContainer fails after starting)
	defer func() {
		_ = containerMgr.StopContainer(context.Background(), containerName)
	}()

	// Start Gitea container
	t.Log("Starting Gitea container...")
	container, err := containerMgr.EnsureContainer(ctx, gitea.ContainerConfig{
		ProjectName: projectName,
	})
	if err != nil {
		t.Fatalf("Failed to start Gitea: %v", err)
	}

	giteaURL := gitea.GetContainerURL(container.HTTPPort)
	if err := gitea.WaitForReady(ctx, giteaURL, 60*time.Second); err != nil {
		t.Fatalf("Gitea not ready: %v", err)
	}

	// Create mirror from GitHub
	mirrorMgr := mirror.NewManager(tmpDir)
	mirrorPath, err := mirrorMgr.EnsureMirror(ctx)
	if err != nil {
		t.Fatalf("Failed to create mirror: %v", err)
	}

	// Setup repository using SetupManager
	setupMgr := gitea.NewSetupManager()
	setupCfg := gitea.SetupConfig{
		Container:  container,
		RepoName:   projectName,
		MirrorPath: mirrorPath,
	}

	result, err := setupMgr.Setup(ctx, setupCfg)
	if err != nil {
		t.Fatalf("Failed to setup repository: %v", err)
	}

	// Create forge state for syncer
	state := &forge.State{
		Provider: string(forge.ProviderGitea),
		URL:      result.URL,
		Token:    result.Token,
		Owner:    result.Owner,
		RepoName: result.RepoName,
	}

	// Save forge state (required by syncer)
	if err := forge.SaveState(tmpDir, state); err != nil {
		t.Fatalf("Failed to save forge state: %v", err)
	}

	// Create test branch in Gitea
	branchName := "sync-test-branch"
	if err := createTestBranchInGitea(ctx, state, branchName); err != nil {
		t.Fatalf("Failed to create test branch: %v", err)
	}

	// Run syncer in dry-run mode
	t.Log("Running sync in dry-run mode...")
	syncer, err := sync.NewSyncer(tmpDir, true)
	if err != nil {
		t.Fatalf("Failed to create syncer: %v", err)
	}

	syncResult, err := syncer.SyncToGitHub(ctx)
	if err != nil {
		t.Fatalf("Sync failed: %v", err)
	}

	// Verify dry-run results
	if !syncResult.Success {
		t.Error("Sync should report success in dry-run mode")
	}

	// Check that branch was detected
	t.Logf("Branches that would be pushed: %v", syncResult.BranchesPushed)

	found := false
	for _, b := range syncResult.BranchesPushed {
		if b == branchName {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("Expected to find branch %s in dry-run results", branchName)
	}

	t.Log("Sync dry-run test passed")
}

// Helper functions

// requireDocker checks if Docker is available and skips the test if not.
func requireDocker(t *testing.T) {
	t.Helper()
	cmd := exec.Command("docker", "info")
	if err := cmd.Run(); err != nil {
		t.Skipf("Docker not available, skipping test: %v", err)
	}
}

func setupTestConfig(projectDir, repoURL string) error {
	maestroDir := filepath.Join(projectDir, ".maestro")
	if err := os.MkdirAll(maestroDir, 0755); err != nil {
		return err
	}

	// Create a more complete config to avoid nil pointer issues in applyDefaults
	configContent := fmt.Sprintf(`{
  "default_mode": "airplane",
  "git": {
    "repo_url": "%s",
    "target_branch": "main"
  },
  "project": {
    "name": "test-project",
    "description": "Integration test project"
  },
  "agents": {
    "pm_model": "claude-sonnet-4-5",
    "architect_model": "claude-sonnet-4-5",
    "coder_model": "claude-sonnet-4-5"
  }
}`, repoURL)

	configPath := filepath.Join(maestroDir, "config.json")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		return err
	}

	return config.LoadConfig(projectDir)
}

func getRemoteURL(ctx context.Context, repoPath string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "remote", "get-url", "origin")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	// Trim newline
	url := string(output)
	if len(url) > 0 && url[len(url)-1] == '\n' {
		url = url[:len(url)-1]
	}
	return url, nil
}

func createBareTestRepo(ctx context.Context, repoPath string) error {
	// Create parent directory
	if err := os.MkdirAll(filepath.Dir(repoPath), 0755); err != nil {
		return err
	}

	// Create temp working directory
	tmpDir, err := os.MkdirTemp("", "test-repo-init-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	// Initialize normal repo
	cmd := exec.CommandContext(ctx, "git", "init")
	cmd.Dir = tmpDir
	if _, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git init failed: %w", err)
	}

	// Create initial commit
	readmePath := filepath.Join(tmpDir, "README.md")
	if err := os.WriteFile(readmePath, []byte("# Test Repository\n"), 0644); err != nil {
		return err
	}

	cmd = exec.CommandContext(ctx, "git", "add", ".")
	cmd.Dir = tmpDir
	if _, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git add failed: %w", err)
	}

	cmd = exec.CommandContext(ctx, "git", "commit", "-m", "Initial commit")
	cmd.Dir = tmpDir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	if _, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit failed: %w", err)
	}

	// Clone as bare
	cmd = exec.CommandContext(ctx, "git", "clone", "--bare", tmpDir, repoPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone --bare failed: %w\nOutput: %s", err, string(output))
	}

	return nil
}

func createTestBranchInGitea(ctx context.Context, state *forge.State, branchName string) error {
	// Clone from Gitea
	cloneDir, err := os.MkdirTemp("", "test-clone-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(cloneDir)

	giteaCloneURL := fmt.Sprintf("%s/%s/%s.git", state.URL, state.Owner, state.RepoName)

	cmd := exec.CommandContext(ctx, "git", "clone", giteaCloneURL, cloneDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone failed: %w\n%s", err, string(output))
	}

	// Create branch
	cmd = exec.CommandContext(ctx, "git", "checkout", "-b", branchName)
	cmd.Dir = cloneDir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout -b failed: %w\n%s", err, string(output))
	}

	// Create test file
	testFile := filepath.Join(cloneDir, "test-"+branchName+".txt")
	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		return err
	}

	// Add and commit
	cmd = exec.CommandContext(ctx, "git", "add", ".")
	cmd.Dir = cloneDir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git add failed: %w\n%s", err, string(output))
	}

	cmd = exec.CommandContext(ctx, "git", "commit", "-m", "Add test file")
	cmd.Dir = cloneDir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit failed: %w\n%s", err, string(output))
	}

	// Push
	cmd = exec.CommandContext(ctx, "git", "push", "-u", "origin", branchName)
	cmd.Dir = cloneDir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git push failed: %w\n%s", err, string(output))
	}

	return nil
}
