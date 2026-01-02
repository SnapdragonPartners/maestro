//go:build e2e

// Package e2e contains end-to-end tests for airplane mode workflows.
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

// TestE2E_AirplaneMode_FullStoryCycle tests a complete story cycle in airplane mode.
// This tests:
// - Starting Gitea from scratch
// - Mirroring a GitHub repository
// - Creating a branch in Gitea
// - Creating a PR in Gitea
// - Verifying the PR workflow works
func TestE2E_AirplaneMode_FullStoryCycle(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	RequireDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// Create test harness
	h := NewAirplaneHarness(t,
		WithGitHubRepo("https://github.com/SnapdragonPartners/maestro-test"),
		WithCleanup(true),
	)
	defer h.Cleanup()

	// Step 1: Full setup
	t.Log("Step 1: Setting up airplane mode environment")
	if err := h.SetupFull(ctx); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Step 2: Verify Gitea is healthy
	t.Log("Step 2: Verifying Gitea health")
	h.AssertGiteaHealthy(ctx)

	// Step 3: Verify forge state
	t.Log("Step 3: Verifying forge state")
	h.AssertForgeStateProvider(string(forge.ProviderGitea))

	// Step 4: Create a story branch
	t.Log("Step 4: Creating story branch")
	branchName := fmt.Sprintf("story/test-%d", time.Now().Unix())
	if err := h.CreateTestBranch(ctx, branchName, "Implement test feature"); err != nil {
		t.Fatalf("Failed to create story branch: %v", err)
	}

	// Step 5: Create a PR for the story
	t.Log("Step 5: Creating pull request")
	client, err := h.GetGiteaClient()
	if err != nil {
		t.Fatalf("Failed to get Gitea client: %v", err)
	}

	t.Logf("Creating PR with client for repo: %s, ForgeState: URL=%s Owner=%s Repo=%s",
		client.RepoPath(), h.ForgeState.URL, h.ForgeState.Owner, h.ForgeState.RepoName)

	pr, err := client.CreatePR(ctx, forge.PRCreateOptions{
		Title: "Story: Test Feature Implementation",
		Body:  "This PR implements the test feature as part of the E2E test.",
		Head:  branchName,
		Base:  "main",
	})
	if err != nil {
		t.Fatalf("Failed to create PR: %v", err)
	}

	t.Logf("PR created: #%d at %s", pr.Number, pr.URL)

	// Step 6: Verify PR exists
	t.Log("Step 6: Verifying PR exists")
	h.AssertPRExists(ctx, branchName)

	// Step 7: Verify PR is mergeable
	t.Log("Step 7: Checking PR mergeability")
	fetchedPR, err := client.GetPR(ctx, fmt.Sprintf("%d", pr.Number))
	if err != nil {
		t.Fatalf("Failed to fetch PR: %v", err)
	}

	if !fetchedPR.Mergeable {
		t.Logf("Warning: PR is not marked as mergeable (this may be due to timing)")
	}

	// Step 8: Merge the PR
	t.Log("Step 8: Merging PR")
	result, err := client.MergePRWithResult(ctx, fmt.Sprintf("%d", pr.Number), forge.PRMergeOptions{
		Method:       "merge",
		DeleteBranch: true,
	})
	if err != nil {
		t.Fatalf("Failed to merge PR: %v", err)
	}

	if !result.Merged {
		if result.HasConflicts {
			t.Fatal("PR merge failed due to conflicts")
		}
		t.Fatalf("PR merge failed: %s", result.Message)
	}

	t.Log("PR merged successfully")

	// Step 9: Verify story cycle completed
	t.Log("Step 9: Story cycle complete - verifying final state")

	// Get syncer to verify mirror state
	syncer, err := h.GetSyncer(true) // dry-run
	if err != nil {
		t.Fatalf("Failed to get syncer: %v", err)
	}

	// Verify we can still sync (connection works)
	_, err = syncer.SyncToGitHub(ctx)
	if err != nil {
		t.Fatalf("Sync verification failed: %v", err)
	}

	t.Log("E2E full story cycle test PASSED")
}

// TestE2E_StandardToAirplane tests transitioning from standard mode to airplane mode.
// This simulates:
// - Having a GitHub repository
// - Starting airplane mode with existing mirror
// - Verifying Gitea is set up correctly from the mirror
func TestE2E_StandardToAirplane(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	RequireDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	tmpDir := t.TempDir()
	gitHubURL := "https://github.com/SnapdragonPartners/maestro-test"
	projectName := fmt.Sprintf("std2air-%d", time.Now().UnixNano())

	// Step 1: Setup as if in standard mode (GitHub mirror)
	t.Log("Step 1: Setting up standard mode environment")
	if err := setupProjectConfig(tmpDir, gitHubURL, "standard"); err != nil {
		t.Fatalf("Failed to setup config: %v", err)
	}

	mgr := mirror.NewManager(tmpDir)
	mirrorPath, err := mgr.EnsureMirror(ctx)
	if err != nil {
		t.Fatalf("Failed to create mirror: %v", err)
	}

	t.Logf("Mirror created at: %s", mirrorPath)

	// Verify mirror points to GitHub (accept both HTTPS and SSH URL formats)
	remoteURL, err := getRemoteURL(ctx, mirrorPath)
	if err != nil {
		t.Fatalf("Failed to get remote URL: %v", err)
	}

	isGitHubURL := strings.Contains(remoteURL, "github.com") &&
		strings.Contains(remoteURL, "SnapdragonPartners/maestro-test")
	if !isGitHubURL {
		t.Errorf("Expected mirror to point to GitHub repo, got %s", remoteURL)
	}

	// Step 2: Switch to airplane mode
	t.Log("Step 2: Switching to airplane mode")

	// Update config to airplane mode
	if err := updateConfigMode(tmpDir, "airplane"); err != nil {
		t.Fatalf("Failed to update config: %v", err)
	}

	// Create container manager
	containerMgr := gitea.NewContainerManager()
	containerName := gitea.ContainerName(projectName)

	// Defer cleanup using container name (in case EnsureContainer fails after starting)
	defer func() {
		_ = containerMgr.StopContainer(context.Background(), containerName)
	}()

	// Start Gitea container
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

	// Create forge state
	state := &forge.State{
		Provider: string(forge.ProviderGitea),
		URL:      result.URL,
		Token:    result.Token,
		Owner:    result.Owner,
		RepoName: result.RepoName,
	}

	// Save forge state
	if err := forge.SaveState(tmpDir, state); err != nil {
		t.Fatalf("Failed to save forge state: %v", err)
	}

	// Wait for branches to be indexed after setup
	client := gitea.NewClient(giteaURL, state.Token, state.Owner, state.RepoName)
	if err := client.WaitForBranches(ctx, 30*time.Second); err != nil {
		t.Fatalf("Failed waiting for branches: %v", err)
	}

	t.Logf("Repository setup in Gitea: %s/%s", state.Owner, state.RepoName)

	// Step 3: Verify mirror can switch to Gitea
	t.Log("Step 3: Switching mirror to Gitea")
	giteaRepoURL := fmt.Sprintf("%s/%s/%s.git", giteaURL, state.Owner, state.RepoName)
	if err := mgr.SwitchUpstream(ctx, giteaRepoURL); err != nil {
		t.Fatalf("Failed to switch mirror to Gitea: %v", err)
	}

	// Verify mirror now points to Gitea
	newRemoteURL, err := getRemoteURL(ctx, mirrorPath)
	if err != nil {
		t.Fatalf("Failed to get remote URL: %v", err)
	}

	if newRemoteURL != giteaRepoURL {
		t.Errorf("Expected Gitea URL %s, got %s", giteaRepoURL, newRemoteURL)
	}

	// Step 4: Verify we can work in airplane mode
	t.Log("Step 4: Verifying airplane mode operations")

	// List existing PRs (should be empty or have some)
	_, err = client.ListPRsForBranch(ctx, "main")
	if err != nil {
		t.Fatalf("Failed to list PRs in airplane mode: %v", err)
	}

	t.Log("E2E standard to airplane mode test PASSED")
}

// TestE2E_AirplaneToStandard tests syncing from airplane mode back to standard mode.
// This is the critical sync operation when coming back online.
func TestE2E_AirplaneToStandard(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	RequireDocker(t)

	// Skip if GITHUB_TOKEN is not set (needed for actual push)
	if os.Getenv("GITHUB_TOKEN") == "" {
		t.Skip("GITHUB_TOKEN not set - skipping sync test that pushes to GitHub")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	tmpDir := t.TempDir()
	gitHubURL := "https://github.com/SnapdragonPartners/maestro-test"
	projectName := fmt.Sprintf("air2std-%d", time.Now().UnixNano())

	// Step 1: Setup airplane mode environment
	t.Log("Step 1: Setting up airplane mode environment")
	if err := setupProjectConfig(tmpDir, gitHubURL, "airplane"); err != nil {
		t.Fatalf("Failed to setup config: %v", err)
	}

	// Create mirror from GitHub first
	mgr := mirror.NewManager(tmpDir)
	mirrorPath, err := mgr.EnsureMirror(ctx)
	if err != nil {
		t.Fatalf("Failed to create mirror: %v", err)
	}

	// Create container manager
	containerMgr := gitea.NewContainerManager()
	containerName := gitea.ContainerName(projectName)

	// Defer cleanup using container name (in case EnsureContainer fails after starting)
	defer func() {
		_ = containerMgr.StopContainer(context.Background(), containerName)
	}()

	// Start Gitea
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

	// Setup repository
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

	state := &forge.State{
		Provider: string(forge.ProviderGitea),
		URL:      result.URL,
		Token:    result.Token,
		Owner:    result.Owner,
		RepoName: result.RepoName,
	}

	if err := forge.SaveState(tmpDir, state); err != nil {
		t.Fatalf("Failed to save forge state: %v", err)
	}

	// Wait for branches to be indexed after setup
	client := gitea.NewClient(giteaURL, state.Token, state.Owner, state.RepoName)
	if err := client.WaitForBranches(ctx, 30*time.Second); err != nil {
		t.Fatalf("Failed waiting for branches: %v", err)
	}

	// Step 2: Create work in airplane mode
	t.Log("Step 2: Creating work in airplane mode")

	// Create a unique branch to avoid conflicts with other test runs
	branchName := fmt.Sprintf("e2e-sync-test-%d", time.Now().UnixNano())
	if err := createBranchInGitea(ctx, state, branchName, "E2E sync test commit"); err != nil {
		t.Fatalf("Failed to create branch in Gitea: %v", err)
	}

	// Wait for the new branch to be indexed
	if err := client.WaitForBranch(ctx, branchName, 10*time.Second); err != nil {
		t.Fatalf("Failed waiting for branch %s to be indexed: %v", branchName, err)
	}

	t.Logf("Created branch: %s", branchName)

	// Step 3: Sync to GitHub (dry-run first)
	t.Log("Step 3: Running sync dry-run")
	syncer, err := sync.NewSyncer(tmpDir, true) // dry-run
	if err != nil {
		t.Fatalf("Failed to create syncer: %v", err)
	}

	dryResult, err := syncer.SyncToGitHub(ctx)
	if err != nil {
		t.Fatalf("Sync dry-run failed: %v", err)
	}

	if !dryResult.Success {
		t.Fatal("Dry-run sync should succeed")
	}

	// Verify branch would be pushed
	found := false
	for _, b := range dryResult.BranchesPushed {
		if b == branchName {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("Expected branch %s in dry-run results", branchName)
	}

	t.Logf("Dry-run shows %d branches would be pushed", len(dryResult.BranchesPushed))

	// Step 4: Actual sync to GitHub
	t.Log("Step 4: Running actual sync to GitHub")
	realSyncer, err := sync.NewSyncer(tmpDir, false) // real sync
	if err != nil {
		t.Fatalf("Failed to create real syncer: %v", err)
	}

	syncResult, err := realSyncer.SyncToGitHub(ctx)
	if err != nil {
		t.Fatalf("Sync failed: %v", err)
	}

	if !syncResult.Success {
		t.Fatal("Sync should succeed")
	}

	t.Logf("Synced branches: %v", syncResult.BranchesPushed)

	// Step 5: Verify mirror is back to GitHub
	t.Log("Step 5: Verifying mirror switched back to GitHub")
	finalRemoteURL, err := getRemoteURL(ctx, mirrorPath)
	if err != nil {
		t.Fatalf("Failed to get remote URL: %v", err)
	}

	// Accept both HTTPS and SSH URL formats for GitHub
	isGitHubURL := strings.Contains(finalRemoteURL, "github.com") &&
		strings.Contains(finalRemoteURL, "SnapdragonPartners/maestro-test")
	if !isGitHubURL {
		t.Errorf("Expected mirror to point to GitHub repo, got %s", finalRemoteURL)
	}

	// Step 6: Cleanup - delete the test branch from GitHub
	t.Log("Step 6: Cleaning up test branch from GitHub")
	if err := deleteGitHubBranch(ctx, gitHubURL, branchName); err != nil {
		t.Logf("Warning: Failed to cleanup test branch: %v", err)
		// Not fatal - branch might not exist or cleanup might fail
	}

	t.Log("E2E airplane to standard mode test PASSED")
}

// Helper functions

func setupProjectConfig(projectDir, repoURL, mode string) error {
	maestroDir := filepath.Join(projectDir, ".maestro")
	if err := os.MkdirAll(maestroDir, 0755); err != nil {
		return err
	}

	// Create a more complete config to avoid nil pointer issues
	configContent := fmt.Sprintf(`{
  "default_mode": "%s",
  "git": {
    "repo_url": "%s",
    "target_branch": "main"
  },
  "project": {
    "name": "e2e-test",
    "description": "E2E test project"
  },
  "agents": {
    "pm_model": "claude-sonnet-4-5",
    "architect_model": "claude-sonnet-4-5",
    "coder_model": "claude-sonnet-4-5"
  }
}`, mode, repoURL)

	configPath := filepath.Join(maestroDir, "config.json")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		return err
	}

	return config.LoadConfig(projectDir)
}

func updateConfigMode(projectDir, mode string) error {
	cfg, err := config.GetConfig()
	if err != nil {
		return err
	}
	cfg.DefaultMode = mode
	return config.SaveConfig(&cfg, projectDir)
}

func getRemoteURL(ctx context.Context, repoPath string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "remote", "get-url", "origin")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	url := string(output)
	if len(url) > 0 && url[len(url)-1] == '\n' {
		url = url[:len(url)-1]
	}
	return url, nil
}

func createBranchInGitea(ctx context.Context, state *forge.State, branchName, commitMsg string) error {
	cloneDir, err := os.MkdirTemp("", "e2e-clone-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(cloneDir)

	// Use authenticated URL for clone and push
	giteaCloneURL := strings.Replace(
		fmt.Sprintf("%s/%s/%s.git", state.URL, state.Owner, state.RepoName),
		"://",
		fmt.Sprintf("://%s:%s@", gitea.DefaultAdminUser, state.Token),
		1,
	)

	cmd := exec.CommandContext(ctx, "git", "clone", giteaCloneURL, cloneDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone failed: %w\n%s", err, string(output))
	}

	cmd = exec.CommandContext(ctx, "git", "checkout", "-b", branchName)
	cmd.Dir = cloneDir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout -b failed: %w\n%s", err, string(output))
	}

	testFile := filepath.Join(cloneDir, fmt.Sprintf("test-%s.txt", branchName))
	if err := os.WriteFile(testFile, []byte("E2E test content\n"), 0644); err != nil {
		return err
	}

	cmd = exec.CommandContext(ctx, "git", "add", ".")
	cmd.Dir = cloneDir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git add failed: %w\n%s", err, string(output))
	}

	cmd = exec.CommandContext(ctx, "git", "commit", "-m", commitMsg)
	cmd.Dir = cloneDir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=E2E Test",
		"GIT_AUTHOR_EMAIL=e2e@test.local",
		"GIT_COMMITTER_NAME=E2E Test",
		"GIT_COMMITTER_EMAIL=e2e@test.local",
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit failed: %w\n%s", err, string(output))
	}

	cmd = exec.CommandContext(ctx, "git", "push", "-u", "origin", branchName)
	cmd.Dir = cloneDir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git push failed: %w\n%s", err, string(output))
	}

	return nil
}

func deleteGitHubBranch(ctx context.Context, repoURL, branchName string) error {
	// Clone, delete remote branch
	cloneDir, err := os.MkdirTemp("", "gh-cleanup-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(cloneDir)

	cmd := exec.CommandContext(ctx, "git", "clone", "--depth=1", repoURL, cloneDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone failed: %w\n%s", err, string(output))
	}

	cmd = exec.CommandContext(ctx, "git", "push", "origin", "--delete", branchName)
	cmd.Dir = cloneDir
	if output, err := cmd.CombinedOutput(); err != nil {
		// Branch might not exist, which is fine
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() != 0 {
			return nil // Likely branch doesn't exist
		}
		return fmt.Errorf("git push --delete failed: %w\n%s", err, string(output))
	}

	return nil
}
