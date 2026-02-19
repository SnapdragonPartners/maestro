// Tests for CloneManager functions.
package coder

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/mocks"
	"orchestrator/pkg/config"
)

func setupCloneManagerTest(t *testing.T) (*CloneManager, *mocks.MockGitRunner) {
	t.Helper()
	tempDir := t.TempDir()

	// Set up config with test git values (CloneManager reads from config)
	config.SetConfigForTesting(&config.Config{
		Git: &config.GitConfig{
			RepoURL:       "https://github.com/test/myrepo.git",
			TargetBranch:  "main",
			MirrorDir:     ".mirrors",
			BranchPattern: "coder-{agent_id}-{STORY_ID}",
		},
	})
	t.Cleanup(func() { config.SetConfigForTesting(nil) })

	mockGit := mocks.NewMockGitRunner()
	cm := NewCloneManager(
		mockGit,
		tempDir,
		"", "", "", "", // These are now ignored - values come from config
	)

	return cm, mockGit
}

// =============================================================================
// BuildMirrorPath tests
// =============================================================================

func TestBuildMirrorPath(t *testing.T) {
	cm, _ := setupCloneManagerTest(t)

	mirrorPath := cm.BuildMirrorPath()

	// Should contain the mirror directory and repo name
	if mirrorPath == "" {
		t.Error("Expected non-empty mirror path")
	}
	// Should end with .git
	if mirrorPath[len(mirrorPath)-4:] != ".git" {
		t.Errorf("Expected mirror path to end with .git, got: %s", mirrorPath)
	}
	// Should contain repo name
	if !contains(mirrorPath, "myrepo.git") {
		t.Errorf("Expected mirror path to contain repo name, got: %s", mirrorPath)
	}
}

func TestBuildMirrorPath_SSHUrl(t *testing.T) {
	tempDir := t.TempDir()

	// Set up config with SSH URL
	config.SetConfigForTesting(&config.Config{
		Git: &config.GitConfig{
			RepoURL:       "git@github.com:user/another-repo.git",
			TargetBranch:  "main",
			BranchPattern: "branch-{STORY_ID}",
		},
	})
	t.Cleanup(func() { config.SetConfigForTesting(nil) })

	mockGit := mocks.NewMockGitRunner()
	cm := NewCloneManager(
		mockGit,
		tempDir,
		"", "", "", "",
	)

	mirrorPath := cm.BuildMirrorPath()

	if !contains(mirrorPath, "another-repo.git") {
		t.Errorf("Expected mirror path to contain repo name from SSH URL, got: %s", mirrorPath)
	}
}

// =============================================================================
// BuildAgentWorkDir tests
// =============================================================================

func TestBuildAgentWorkDir_RelativePath(t *testing.T) {
	cm, _ := setupCloneManagerTest(t)

	result := cm.BuildAgentWorkDir("agent-001", "relative/path/to/work")

	// Should return an absolute path
	if result == "" {
		t.Error("Expected non-empty result")
	}
	// Absolute paths start with /
	if result[0] != '/' {
		t.Errorf("Expected absolute path, got: %s", result)
	}
}

func TestBuildAgentWorkDir_AbsolutePath(t *testing.T) {
	cm, _ := setupCloneManagerTest(t)

	result := cm.BuildAgentWorkDir("agent-001", "/absolute/path/to/work")

	if result != "/absolute/path/to/work" {
		t.Errorf("Expected same absolute path, got: %s", result)
	}
}

// =============================================================================
// buildBranchName tests
// =============================================================================

func TestBuildBranchName_WithStoryID(t *testing.T) {
	cm, _ := setupCloneManagerTest(t)

	result := cm.buildBranchName("story-123")

	// Pattern is "coder-{agent_id}-{STORY_ID}"
	if !contains(result, "story-123") {
		t.Errorf("Expected branch name to contain story ID, got: %s", result)
	}
}

func TestBuildBranchName_EmptyStoryID(t *testing.T) {
	cm, _ := setupCloneManagerTest(t)

	result := cm.buildBranchName("")

	// Should still produce a result, just with empty replacement
	if result == "" {
		t.Error("Expected non-empty result even with empty story ID")
	}
}

// =============================================================================
// branchExists tests
// =============================================================================

func TestBranchExists_Found(t *testing.T) {
	cm, _ := setupCloneManagerTest(t)

	branches := []string{"main", "develop", "feature-123"}
	result := cm.branchExists("develop", branches)

	if !result {
		t.Error("Expected branch to exist")
	}
}

func TestBranchExists_NotFound(t *testing.T) {
	cm, _ := setupCloneManagerTest(t)

	branches := []string{"main", "develop", "feature-123"}
	result := cm.branchExists("nonexistent", branches)

	if result {
		t.Error("Expected branch to not exist")
	}
}

func TestBranchExists_EmptyList(t *testing.T) {
	cm, _ := setupCloneManagerTest(t)

	result := cm.branchExists("any", []string{})

	if result {
		t.Error("Expected branch to not exist in empty list")
	}
}

// =============================================================================
// getExistingBranches tests
// =============================================================================

func TestGetExistingBranches_Success(t *testing.T) {
	cm, mockGit := setupCloneManagerTest(t)

	// Configure mock to return branch list
	mockGit.OnRun(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) > 0 {
			switch args[0] {
			case "branch":
				return []byte("* main\n  develop\n  feature-x\n"), nil
			case "ls-remote":
				return []byte("abc123\trefs/heads/main\ndef456\trefs/heads/remote-only\n"), nil
			}
		}
		return []byte{}, nil
	})

	ctx := context.Background()
	branches, err := cm.getExistingBranches(ctx, "/some/repo")

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	// Should have local branches (main, develop, feature-x) and remote branch (remote-only)
	if len(branches) < 3 {
		t.Errorf("Expected at least 3 branches, got: %d (%v)", len(branches), branches)
	}

	// Check specific branches
	foundMain := false
	foundRemoteOnly := false
	for _, b := range branches {
		if b == "main" {
			foundMain = true
		}
		if b == "remote-only" {
			foundRemoteOnly = true
		}
	}
	if !foundMain {
		t.Error("Expected to find 'main' branch")
	}
	if !foundRemoteOnly {
		t.Error("Expected to find 'remote-only' branch from ls-remote")
	}
}

func TestGetExistingBranches_LocalBranchError(t *testing.T) {
	cm, mockGit := setupCloneManagerTest(t)

	// Configure mock to fail on branch command
	mockGit.FailCommandWith("branch", errGitFailure)

	ctx := context.Background()
	_, err := cm.getExistingBranches(ctx, "/some/repo")

	if err == nil {
		t.Error("Expected error for failed branch command")
	}
}

func TestGetExistingBranches_RemoteQueryFails(t *testing.T) {
	cm, mockGit := setupCloneManagerTest(t)

	// Configure mock: branch succeeds, ls-remote fails
	mockGit.OnRun(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) > 0 {
			switch args[0] {
			case "branch":
				return []byte("* main\n  develop\n"), nil
			case "ls-remote":
				return nil, errGitFailure
			}
		}
		return []byte{}, nil
	})

	ctx := context.Background()
	branches, err := cm.getExistingBranches(ctx, "/some/repo")

	// Should not error - remote failure is logged but doesn't fail
	if err != nil {
		t.Errorf("Expected no error (remote failure is warning only), got: %v", err)
	}

	// Should have local branches only
	if len(branches) != 2 {
		t.Errorf("Expected 2 local branches, got: %d", len(branches))
	}
}

// =============================================================================
// configureGitIdentity tests
// =============================================================================

func TestConfigureGitIdentity_Success(t *testing.T) {
	cm, mockGit := setupCloneManagerTest(t)

	ctx := context.Background()
	err := cm.configureGitIdentity(ctx, "/workspace", "coder-001")

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	// Verify git config commands were called
	configCalls := mockGit.GetCallsForCommand("config")
	if len(configCalls) < 2 {
		t.Errorf("Expected at least 2 config calls (user.name and user.email), got: %d", len(configCalls))
	}
}

func TestConfigureGitIdentity_UserNameError(t *testing.T) {
	cm, mockGit := setupCloneManagerTest(t)

	// Fail on config command
	mockGit.FailCommandWith("config", errGitFailure)

	ctx := context.Background()
	err := cm.configureGitIdentity(ctx, "/workspace", "coder-001")

	if err == nil {
		t.Error("Expected error for failed git config")
	}
}

// =============================================================================
// SetContainerManager tests
// =============================================================================

func TestSetContainerManager(t *testing.T) {
	cm, _ := setupCloneManagerTest(t)

	mockContainer := mocks.NewMockContainerManager()
	cm.SetContainerManager(mockContainer)

	// No assertion needed - just verify it doesn't panic
}

// =============================================================================
// Helper functions
// =============================================================================

func contains(s, substr string) bool {
	return len(s) >= len(substr) && findSubstr(s, substr) >= 0
}

func findSubstr(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// =============================================================================
// CleanupWorkspace tests
// =============================================================================

func TestCleanupWorkspace_DirectoryExists(t *testing.T) {
	cm, _ := setupCloneManagerTest(t)

	// Create a temp directory with some content
	workDir := t.TempDir()
	testFile := workDir + "/test.txt"
	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	ctx := context.Background()
	err := cm.CleanupWorkspace(ctx, "agent-001", "story-123", workDir)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	// Directory should still exist
	if _, err := os.Stat(workDir); os.IsNotExist(err) {
		t.Error("Expected directory to still exist (inode preserved)")
	}

	// But contents should be cleaned
	entries, _ := os.ReadDir(workDir)
	if len(entries) != 0 {
		t.Errorf("Expected directory to be empty, got %d entries", len(entries))
	}
}

func TestCleanupWorkspace_DirectoryDoesNotExist(t *testing.T) {
	cm, _ := setupCloneManagerTest(t)

	ctx := context.Background()
	err := cm.CleanupWorkspace(ctx, "agent-001", "story-123", "/nonexistent/path/12345")

	// Should succeed (no-op for non-existent directory)
	if err != nil {
		t.Errorf("Expected no error for non-existent directory, got: %v", err)
	}
}

// =============================================================================
// CleanupAgentResources tests
// =============================================================================

func TestCleanupAgentResources_NoContainer(t *testing.T) {
	cm, _ := setupCloneManagerTest(t)

	// Create a work directory
	workDir := t.TempDir()

	ctx := context.Background()
	err := cm.CleanupAgentResources(ctx, "agent-001", "", workDir, "")

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
}

func TestCleanupAgentResources_WithContainerManager(t *testing.T) {
	cm, _ := setupCloneManagerTest(t)

	// Set up mock container manager
	mockContainer := mocks.NewMockContainerManager()
	cm.SetContainerManager(mockContainer)

	// Create a work directory
	workDir := t.TempDir()

	ctx := context.Background()
	err := cm.CleanupAgentResources(ctx, "agent-001", "test-container", workDir, "")

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	// Verify container was stopped
	if !mockContainer.WasContainerStopped("test-container") {
		t.Error("Expected container to be stopped")
	}

	// Verify shutdown was called
	if !mockContainer.WasShutdownCalled() {
		t.Error("Expected container manager shutdown to be called")
	}
}

func TestCleanupAgentResources_ContainerStopFailure(t *testing.T) {
	cm, _ := setupCloneManagerTest(t)

	// Set up mock container manager that fails
	mockContainer := mocks.NewMockContainerManager()
	mockContainer.FailStopContainerWith(errGitFailure)
	cm.SetContainerManager(mockContainer)

	workDir := t.TempDir()

	ctx := context.Background()
	err := cm.CleanupAgentResources(ctx, "agent-001", "test-container", workDir, "")

	// Should return error when container stop fails
	if err == nil {
		t.Error("Expected error when container stop fails")
	}
}

func TestCleanupAgentResources_WithStateDir(t *testing.T) {
	cm, _ := setupCloneManagerTest(t)

	workDir := t.TempDir()
	stateDir := t.TempDir()

	// Create a file in state dir
	testFile := stateDir + "/state.json"
	if err := os.WriteFile(testFile, []byte("{}"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	ctx := context.Background()
	err := cm.CleanupAgentResources(ctx, "agent-001", "", workDir, stateDir)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	// State directory should be removed
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Error("Expected state directory to be removed")
	}
}

// =============================================================================
// ensureMirrorClone tests
// =============================================================================

func TestEnsureMirrorClone_CreateNew(t *testing.T) {
	cm, mockGit := setupCloneManagerTest(t)

	// Configure mock for successful clone
	mockGit.OnRun(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "clone" {
			return []byte("Cloning into bare repository...\n"), nil
		}
		return []byte{}, nil
	})

	ctx := context.Background()
	mirrorPath, err := cm.ensureMirrorClone(ctx)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
	if mirrorPath == "" {
		t.Error("Expected non-empty mirror path")
	}

	// Verify clone was called
	if !mockGit.WasCommandCalled("clone") {
		t.Error("Expected git clone to be called")
	}
}

func TestEnsureMirrorClone_CloneFails(t *testing.T) {
	cm, mockGit := setupCloneManagerTest(t)

	// Configure mock to fail clone
	mockGit.FailCommandWith("clone", errGitFailure)

	ctx := context.Background()
	_, err := cm.ensureMirrorClone(ctx)

	if err == nil {
		t.Error("Expected error when clone fails")
	}
}

// =============================================================================
// createBranch tests
// =============================================================================

func TestCreateBranch_Success(t *testing.T) {
	cm, mockGit := setupCloneManagerTest(t)

	// Create actual workspace directory
	workDir := t.TempDir()

	// Configure mock for branch operations
	mockGit.OnRun(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) > 0 {
			switch args[0] {
			case "branch":
				return []byte("* main\n"), nil
			case "ls-remote":
				return []byte("abc123\trefs/heads/main\n"), nil
			case "switch":
				return []byte("Switched to new branch\n"), nil
			}
		}
		return []byte{}, nil
	})

	ctx := context.Background()
	branchName, err := cm.createBranch(ctx, workDir, "story-001")

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
	if branchName == "" {
		t.Error("Expected non-empty branch name")
	}
}

func TestCreateBranch_CollisionRetry(t *testing.T) {
	cm, mockGit := setupCloneManagerTest(t)

	// Create actual workspace directory
	workDir := t.TempDir()

	// Configure mock so that "feature-branch" exists but "feature-branch-2" doesn't
	mockGit.OnRun(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) > 0 {
			switch args[0] {
			case "branch":
				// Return that feature-branch exists locally
				return []byte("* main\n  feature-branch\n"), nil
			case "ls-remote":
				return []byte("abc123\trefs/heads/main\n"), nil
			case "switch":
				// Allow creation of the incremented name
				return []byte("Switched to new branch\n"), nil
			}
		}
		return []byte{}, nil
	})

	ctx := context.Background()
	branchName, err := cm.createBranch(ctx, workDir, "feature-branch")

	if err != nil {
		t.Errorf("Expected no error after finding available name, got: %v", err)
	}
	// Should have a suffix like -2 since feature-branch exists
	if branchName != "feature-branch-2" {
		t.Errorf("Expected branch name 'feature-branch-2', got: %s", branchName)
	}
}

// =============================================================================
// createBranchWithRetry tests
// =============================================================================

func TestCreateBranchWithRetry_Success(t *testing.T) {
	cm, mockGit := setupCloneManagerTest(t)

	mockGit.OnRun(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "switch" {
			return []byte("Switched to new branch\n"), nil
		}
		return []byte{}, nil
	})

	ctx := context.Background()
	branchName, err := cm.createBranchWithRetry(ctx, "/workspace", "feature-branch")

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
	if branchName != "feature-branch" {
		t.Errorf("Expected branch name 'feature-branch', got: %s", branchName)
	}
}

func TestCreateBranchWithRetry_AllAttemptsFail(t *testing.T) {
	cm, mockGit := setupCloneManagerTest(t)

	// All attempts fail with "already exists"
	mockGit.OnRun(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "switch" {
			return nil, &gitError{msg: "already exists"}
		}
		return []byte{}, nil
	})

	ctx := context.Background()
	_, err := cm.createBranchWithRetry(ctx, "/workspace", "feature-branch")

	if err == nil {
		t.Error("Expected error when all attempts fail")
	}
}

// =============================================================================
// createFreshClone tests
// =============================================================================

func TestCreateFreshClone_Success(t *testing.T) {
	cm, mockGit := setupCloneManagerTest(t)
	mirrorPath := t.TempDir()
	agentWorkDir := filepath.Join(t.TempDir(), "agent")

	// Mock git commands for fresh clone
	mockGit.OnRun(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) == 0 {
			return []byte{}, nil
		}
		switch args[0] {
		case "init":
			return []byte("Initialized empty Git repository\n"), nil
		case "remote":
			return []byte{}, nil
		case "fetch":
			return []byte{}, nil
		case "checkout":
			return []byte("Switched to a new branch 'main'\n"), nil
		case "reset":
			return []byte{}, nil
		}
		return []byte{}, nil
	})

	ctx := context.Background()
	err := cm.createFreshClone(ctx, mirrorPath, agentWorkDir)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	// Verify directory was created
	if _, statErr := os.Stat(agentWorkDir); os.IsNotExist(statErr) {
		t.Error("Expected agent work directory to be created")
	}
}

func TestCreateFreshClone_CleansExistingDirectory(t *testing.T) {
	cm, mockGit := setupCloneManagerTest(t)
	mirrorPath := t.TempDir()
	agentWorkDir := t.TempDir()

	// Create a file in the existing directory
	testFile := filepath.Join(agentWorkDir, "existing.txt")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Mock git commands
	mockGit.OnRun(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) == 0 {
			return []byte{}, nil
		}
		switch args[0] {
		case "init":
			return []byte("Reinitialized existing Git repository\n"), nil
		case "remote", "fetch", "checkout", "reset":
			return []byte{}, nil
		}
		return []byte{}, nil
	})

	ctx := context.Background()
	err := cm.createFreshClone(ctx, mirrorPath, agentWorkDir)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	// Verify old file was cleaned
	if _, statErr := os.Stat(testFile); !os.IsNotExist(statErr) {
		t.Error("Expected existing file to be cleaned")
	}
}

func TestCreateFreshClone_GitInitFails(t *testing.T) {
	cm, mockGit := setupCloneManagerTest(t)
	mirrorPath := t.TempDir()
	agentWorkDir := t.TempDir()

	// Mock git init to fail
	mockGit.OnRun(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "init" {
			return nil, &gitError{msg: "git init failed"}
		}
		return []byte{}, nil
	})

	ctx := context.Background()
	err := cm.createFreshClone(ctx, mirrorPath, agentWorkDir)

	if err == nil {
		t.Error("Expected error when git init fails")
	}
	if !strings.Contains(err.Error(), "git init failed") {
		t.Errorf("Expected error about git init, got: %v", err)
	}
}

func TestCreateFreshClone_AddRemoteFails(t *testing.T) {
	cm, mockGit := setupCloneManagerTest(t)
	mirrorPath := t.TempDir()
	agentWorkDir := t.TempDir()

	// Mock git commands - remote add fails
	mockGit.OnRun(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) >= 2 && args[0] == "remote" && args[1] == "add" {
			return nil, &gitError{msg: "remote add failed"}
		}
		return []byte{}, nil
	})

	ctx := context.Background()
	err := cm.createFreshClone(ctx, mirrorPath, agentWorkDir)

	if err == nil {
		t.Error("Expected error when remote add fails")
	}
}

func TestCreateFreshClone_FetchFails(t *testing.T) {
	cm, mockGit := setupCloneManagerTest(t)
	mirrorPath := t.TempDir()
	agentWorkDir := t.TempDir()

	// Mock git commands - fetch fails
	mockGit.OnRun(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "fetch" {
			return nil, &gitError{msg: "fetch failed"}
		}
		return []byte{}, nil
	})

	ctx := context.Background()
	err := cm.createFreshClone(ctx, mirrorPath, agentWorkDir)

	if err == nil {
		t.Error("Expected error when fetch fails")
	}
}

func TestCreateFreshClone_CheckoutFails(t *testing.T) {
	cm, mockGit := setupCloneManagerTest(t)
	mirrorPath := t.TempDir()
	agentWorkDir := t.TempDir()

	fetchCount := 0
	// Mock git commands - checkout fails
	mockGit.OnRun(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) == 0 {
			return []byte{}, nil
		}
		switch args[0] {
		case "fetch":
			fetchCount++
			if fetchCount == 1 {
				// First fetch (from mirror) succeeds
				return []byte{}, nil
			}
			return []byte{}, nil
		case "checkout":
			return nil, &gitError{msg: "checkout failed"}
		}
		return []byte{}, nil
	})

	ctx := context.Background()
	err := cm.createFreshClone(ctx, mirrorPath, agentWorkDir)

	if err == nil {
		t.Error("Expected error when checkout fails")
	}
}

// =============================================================================
// SetupWorkspace tests
// =============================================================================

func TestSetupWorkspace_InvalidProjectDir(t *testing.T) {
	// Set up config with test git values (CloneManager reads from config)
	config.SetConfigForTesting(&config.Config{
		Git: &config.GitConfig{
			RepoURL:       "https://github.com/test/myrepo.git",
			TargetBranch:  "main",
			BranchPattern: "coder-{agent_id}-{STORY_ID}",
		},
	})
	t.Cleanup(func() { config.SetConfigForTesting(nil) })

	mockGit := mocks.NewMockGitRunner()
	// Create a CloneManager with a non-existent project directory
	cm := NewCloneManager(
		mockGit,
		"/nonexistent/path/that/does/not/exist",
		"", "", "", "",
	)

	ctx := context.Background()
	_, err := cm.SetupWorkspace(ctx, "test-agent", "test-story", "/workspace")

	if err == nil {
		t.Error("Expected error for non-existent project directory")
	}
}

// =============================================================================
// isGitNetworkError tests
// =============================================================================

func TestIsGitNetworkError_NilError(t *testing.T) {
	if isGitNetworkError(nil) {
		t.Error("Expected false for nil error")
	}
}

func TestIsGitNetworkError_NetworkErrors(t *testing.T) {
	networkErrors := []string{
		"fatal: Could not read from remote repository",
		"Connection refused",
		"Connection reset by peer",
		"Connection timed out",
		"No route to host",
		"Operation timed out",
		"Name or service not known",
		"Couldn't resolve host 'github.com'",
		"fatal: unable to access 'https://github.com/user/repo.git/'",
		"Network is unreachable",
		"ssh_exchange_identification: Connection closed",
		"Broken pipe",
	}

	for _, msg := range networkErrors {
		if !isGitNetworkError(&gitError{msg: msg}) {
			t.Errorf("Expected true for network error: %q", msg)
		}
	}
}

func TestIsGitNetworkError_NonNetworkErrors(t *testing.T) {
	nonNetworkErrors := []string{
		"repository not found",
		"authentication failed for 'https://github.com/user/repo.git/'",
		"Permission denied (publickey)",
		"invalid username or password",
		"could not find remote branch 'feature-xyz'",
		"not a git repository",
		"already exists",
		"merge conflict in file.go",
	}

	for _, msg := range nonNetworkErrors {
		if isGitNetworkError(&gitError{msg: msg}) {
			t.Errorf("Expected false for non-network error: %q", msg)
		}
	}
}

func TestIsGitNetworkError_NegativeExclusionPrecedence(t *testing.T) {
	// "unable to access" is a network pattern, but "repository not found"
	// should take precedence as a negative exclusion
	err := &gitError{msg: "unable to access: repository not found"}
	if isGitNetworkError(err) {
		t.Error("Expected false: negative exclusion should take precedence over network patterns")
	}
}

// =============================================================================
// retryGitNetworkOp tests
// =============================================================================

func TestRetryGitNetworkOp_SuccessOnFirstAttempt(t *testing.T) {
	cm, mockGit := setupCloneManagerTest(t)

	mockGit.OnRun(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte("success"), nil
	})

	ctx := context.Background()
	result, err := cm.retryGitNetworkOp(ctx, "/workspace", "fetch", "origin")

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
	if string(result) != "success" {
		t.Errorf("Expected 'success', got: %s", string(result))
	}
}

func TestRetryGitNetworkOp_NonNetworkErrorNoRetry(t *testing.T) {
	cm, mockGit := setupCloneManagerTest(t)

	callCount := 0
	mockGit.OnRun(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		callCount++
		return nil, &gitError{msg: "repository not found"}
	})

	ctx := context.Background()
	_, err := cm.retryGitNetworkOp(ctx, "/workspace", "clone", "https://example.com/repo.git")

	if err == nil {
		t.Error("Expected error for non-network failure")
	}
	if callCount != 1 {
		t.Errorf("Expected exactly 1 attempt for non-network error, got: %d", callCount)
	}
	// Should NOT be a GitNetworkError
	var gitNetErr *GitNetworkError
	if errors.As(err, &gitNetErr) {
		t.Error("Expected non-GitNetworkError for non-network failure")
	}
}

func TestRetryGitNetworkOp_NetworkErrorExhaustsRetries(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping slow retry test in short mode")
	}

	cm, mockGit := setupCloneManagerTest(t)

	callCount := 0
	mockGit.OnRun(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		callCount++
		return nil, &gitError{msg: "connection refused"}
	})

	ctx := context.Background()
	_, err := cm.retryGitNetworkOp(ctx, "/workspace", "fetch", "origin")

	if err == nil {
		t.Error("Expected error after exhausting retries")
	}
	// Should exhaust all 4 retry slots (0s, 5s, 15s, 30s)
	if callCount != 4 {
		t.Errorf("Expected 4 attempts, got: %d", callCount)
	}
	// Should be a GitNetworkError sentinel
	var gitNetErr *GitNetworkError
	if !errors.As(err, &gitNetErr) {
		t.Errorf("Expected GitNetworkError, got: %T: %v", err, err)
	}
	if gitNetErr.Attempts != 4 {
		t.Errorf("Expected 4 attempts in GitNetworkError, got: %d", gitNetErr.Attempts)
	}
}

func TestRetryGitNetworkOp_SuccessOnSecondAttempt(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping slow retry test in short mode")
	}

	cm, mockGit := setupCloneManagerTest(t)

	callCount := 0
	mockGit.OnRun(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		callCount++
		if callCount < 2 {
			return nil, &gitError{msg: "connection refused"}
		}
		return []byte("recovered"), nil
	})

	ctx := context.Background()
	result, err := cm.retryGitNetworkOp(ctx, "/workspace", "fetch", "origin")

	if err != nil {
		t.Errorf("Expected no error after recovery, got: %v", err)
	}
	if string(result) != "recovered" {
		t.Errorf("Expected 'recovered', got: %s", string(result))
	}
	if callCount != 2 {
		t.Errorf("Expected 2 attempts before success, got: %d", callCount)
	}
}

func TestRetryGitNetworkOp_CancelledContext(t *testing.T) {
	cm, mockGit := setupCloneManagerTest(t)

	mockGit.OnRun(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return nil, &gitError{msg: "connection refused"}
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := cm.retryGitNetworkOp(ctx, "/workspace", "fetch", "origin")

	if err == nil {
		t.Error("Expected error for cancelled context")
	}
}

// =============================================================================
// GitNetworkError tests
// =============================================================================

func TestGitNetworkError_ErrorMessage(t *testing.T) {
	inner := &gitError{msg: "connection refused"}
	netErr := &GitNetworkError{Err: inner, Attempts: 4}

	msg := netErr.Error()
	if !strings.Contains(msg, "4 attempts") {
		t.Errorf("Expected error message to contain attempt count, got: %s", msg)
	}
	if !strings.Contains(msg, "connection refused") {
		t.Errorf("Expected error message to contain inner error, got: %s", msg)
	}
}

func TestGitNetworkError_Unwrap(t *testing.T) {
	inner := &gitError{msg: "connection refused"}
	netErr := &GitNetworkError{Err: inner, Attempts: 4}

	unwrapped := netErr.Unwrap()
	if !errors.Is(unwrapped, inner) {
		t.Error("Expected Unwrap to return inner error")
	}
}

// =============================================================================
// Helper imports
// =============================================================================

var errGitFailure = &gitError{msg: "git operation failed"}

type gitError struct {
	msg string
}

func (e *gitError) Error() string {
	return e.msg
}
