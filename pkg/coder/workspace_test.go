package coder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Unit tests with mocked GitRunner
func TestWorkspaceManagerSetup_Unit(t *testing.T) {
	tests := []struct {
		name          string
		repoURL       string
		baseBranch    string
		agentID       string
		storyID       string
		mockCommands  map[string][]byte
		mockErrors    map[string]error
		expectedError bool
	}{
		{
			name:       "successful setup",
			repoURL:    "git@github.com:user/repo.git",
			baseBranch: "main",
			agentID:    "agent-1",
			storyID:    "050",
			mockCommands: map[string][]byte{
				"|clone --bare git@github.com:user/repo.git": []byte("Cloning..."),
				"|remote update --prune":                           []byte("Fetching..."),
				"|worktree add --detach":                       []byte("Adding worktree..."),
				"|switch -c story-050":                         []byte("Switched to branch"),
			},
			expectedError: false,
		},
		{
			name:       "clone failure",
			repoURL:    "git@github.com:user/repo.git",
			baseBranch: "main",
			agentID:    "agent-1",
			storyID:    "050",
			mockErrors: map[string]error{
				"|clone --bare git@github.com:user/repo.git": fmt.Errorf("clone failed"),
			},
			expectedError: true,
		},
		{
			name:       "worktree add failure",
			repoURL:    "git@github.com:user/repo.git",
			baseBranch: "main",
			agentID:    "agent-1",
			storyID:    "050",
			mockCommands: map[string][]byte{
				"|clone --bare git@github.com:user/repo.git": []byte("Cloning..."),
				"|remote update --prune":                           []byte("Fetching..."),
			},
			mockErrors: map[string]error{
				"|worktree add --detach": fmt.Errorf("worktree add failed"),
			},
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()
			
			// Setup mock GitRunner
			mockGit := NewMockGitRunner()
			for cmd, output := range tt.mockCommands {
				parts := parseMockCommand(cmd)
				if len(parts) >= 2 {
					mockGit.Commands[cmd] = output
				}
			}
			for cmd, err := range tt.mockErrors {
				mockGit.Errors[cmd] = err
			}

			// Create workspace manager
			wm := NewWorkspaceManager(
				mockGit,
				tempDir,
				tt.repoURL,
				tt.baseBranch,
				".mirrors",
				"story-{STORY_ID}",
				"{AGENT_ID}/{STORY_ID}",
			)

			// Test setup
			ctx := context.Background()
			worktreePath, err := wm.SetupWorkspace(ctx, tt.agentID, tt.storyID, "/tmp/test-agent")

			if tt.expectedError {
				if err == nil {
					t.Error("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			// Verify worktree path
			expectedPath := filepath.Join(tempDir, tt.agentID, tt.storyID)
			if worktreePath != expectedPath {
				t.Errorf("Expected worktree path %s, got %s", expectedPath, worktreePath)
			}
		})
	}
}

func TestWorkspaceManagerCleanup_Unit(t *testing.T) {
	tempDir := t.TempDir()
	mockGit := NewMockGitRunner()

	wm := NewWorkspaceManager(
		mockGit,
		tempDir,
		"git@github.com:user/repo.git",
		"main",
		".mirrors",
		"story-{STORY_ID}",
		"{AGENT_ID}/{STORY_ID}",
	)

	ctx := context.Background()
	err := wm.CleanupWorkspace(ctx, "agent-1", "050", "/tmp/test-agent")

	if err != nil {
		t.Errorf("Unexpected error during cleanup: %v", err)
	}

	// Verify cleanup commands were called
	expectedCommands := [][]string{
		{"worktree", "remove"},
		{"worktree", "prune"},
	}

	for _, expectedCmd := range expectedCommands {
		found := false
		for _, call := range mockGit.CallLog {
			if len(call.Args) >= len(expectedCmd) {
				match := true
				for i, arg := range expectedCmd {
					if call.Args[i] != arg {
						match = false
						break
					}
				}
				if match {
					found = true
					break
				}
			}
		}
		if !found {
			t.Errorf("Expected command %v was not called", expectedCmd)
		}
	}
}

func TestWorkspaceManagerPathBuilding(t *testing.T) {
	tempDir := t.TempDir()
	mockGit := NewMockGitRunner()

	wm := NewWorkspaceManager(
		mockGit,
		tempDir,
		"git@github.com:user/repo.git",
		"main",
		".mirrors",
		"story-{STORY_ID}",
		"{AGENT_ID}/{STORY_ID}",
	)

	// Test mirror path building
	mirrorPath := wm.buildMirrorPath()
	expectedMirrorPath := filepath.Join(tempDir, ".mirrors", "repo.git")
	if mirrorPath != expectedMirrorPath {
		t.Errorf("Expected mirror path %s, got %s", expectedMirrorPath, mirrorPath)
	}

	// Test story work directory path building
	storyWorkDir := wm.BuildStoryWorkDir("agent-1", "050", "/tmp/test-agent")
	expectedStoryWorkDir := filepath.Join("/tmp/test-agent", "050")
	if storyWorkDir != expectedStoryWorkDir {
		t.Errorf("Expected story work directory %s, got %s", expectedStoryWorkDir, storyWorkDir)
	}

	// Test branch name building
	branchName := wm.buildBranchName("050")
	expectedBranchName := "story-050"
	if branchName != expectedBranchName {
		t.Errorf("Expected branch name %s, got %s", expectedBranchName, branchName)
	}
}

// Functional tests with real Git (local repository only)
func TestWorkspaceManagerFunctional(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping functional test in short mode")
	}

	tempDir := t.TempDir()

	// 1. Create a fake "remote" repository (bare)
	remoteDir := filepath.Join(tempDir, "remote.git")
	err := os.MkdirAll(remoteDir, 0755)
	if err != nil {
		t.Fatal(err)
	}

	gitRunner := NewDefaultGitRunner()
	ctx := context.Background()

	// Initialize bare repository
	_, err = gitRunner.Run(ctx, remoteDir, "init", "--bare")
	if err != nil {
		t.Fatal("Failed to init bare repo:", err)
	}

	// 2. Create a seed repository to push initial content
	seedDir := filepath.Join(tempDir, "seed")
	_, err = gitRunner.Run(ctx, "", "clone", remoteDir, seedDir)
	if err != nil {
		t.Fatal("Failed to clone seed repo:", err)
	}

	// Add initial content
	readmeFile := filepath.Join(seedDir, "README.md")
	err = os.WriteFile(readmeFile, []byte("# Test Repository"), 0644)
	if err != nil {
		t.Fatal("Failed to write README:", err)
	}

	_, err = gitRunner.Run(ctx, seedDir, "add", ".")
	if err != nil {
		t.Fatal("Failed to add files:", err)
	}

	// Configure git user for commit
	_, err = gitRunner.Run(ctx, seedDir, "config", "user.name", "Test User")
	if err != nil {
		t.Fatal("Failed to set git user.name:", err)
	}
	_, err = gitRunner.Run(ctx, seedDir, "config", "user.email", "test@example.com")
	if err != nil {
		t.Fatal("Failed to set git user.email:", err)
	}

	_, err = gitRunner.Run(ctx, seedDir, "commit", "-m", "Initial commit")
	if err != nil {
		t.Fatal("Failed to commit:", err)
	}

	// Create main branch explicitly and push
	_, err = gitRunner.Run(ctx, seedDir, "branch", "-M", "main")
	if err != nil {
		t.Fatal("Failed to create main branch:", err)
	}

	_, err = gitRunner.Run(ctx, seedDir, "push", "-u", "origin", "main")
	if err != nil {
		t.Fatal("Failed to push:", err)
	}

	// 3. Test workspace manager with real Git operations
	workDir := filepath.Join(tempDir, "work")
	err = os.MkdirAll(workDir, 0755)
	if err != nil {
		t.Fatal(err)
	}

	wm := NewWorkspaceManager(
		gitRunner,
		workDir,
		remoteDir, // Use local "remote" repo
		"main",
		".mirrors",
		"story-{STORY_ID}",
		"{AGENT_ID}/{STORY_ID}",
	)


	// 4. Test workspace setup
	start := time.Now()
	agentWorkDir := filepath.Join(workDir, "test-agent")
	worktreePath, err := wm.SetupWorkspace(ctx, "test-agent", "042", agentWorkDir)
	duration := time.Since(start)

	if err != nil {
		t.Fatal("Workspace setup failed:", err)
	}

	// Verify timing (should be under 300ms on warm cache)
	if duration > 300*time.Millisecond {
		t.Logf("Warning: Setup took %v (acceptance criteria: <300ms)", duration)
	}

	// Verify worktree exists and has content
	expectedWorktreePath := filepath.Join(workDir, "test-agent", "042")
	if worktreePath != expectedWorktreePath {
		t.Errorf("Expected worktree path %s, got %s", expectedWorktreePath, worktreePath)
	}

	readmePath := filepath.Join(worktreePath, "README.md")
	if _, err := os.Stat(readmePath); os.IsNotExist(err) {
		t.Error("README.md should exist in worktree")
	}

	// Verify branch is checked out
	output, err := gitRunner.Run(ctx, worktreePath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatal("Failed to get current branch:", err)
	}
	currentBranch := string(output)
	expectedBranch := "story-042\n"
	if currentBranch != expectedBranch {
		t.Errorf("Expected branch %q, got %q", expectedBranch, currentBranch)
	}

	// 5. Test multiple agents using same mirror (shared object dir)
	agent2WorkDir := filepath.Join(workDir, "test-agent-2")
	secondWorktreePath, err := wm.SetupWorkspace(ctx, "test-agent-2", "043", agent2WorkDir)
	if err != nil {
		t.Fatal("Second workspace setup failed:", err)
	}

	// Verify both worktrees exist
	if _, err := os.Stat(worktreePath); os.IsNotExist(err) {
		t.Error("First worktree should still exist")
	}
	if _, err := os.Stat(secondWorktreePath); os.IsNotExist(err) {
		t.Error("Second worktree should exist")
	}

	// Verify worktrees are listed
	output, err = gitRunner.Run(ctx, wm.buildMirrorPath(), "worktree", "list")
	if err != nil {
		t.Fatal("Failed to list worktrees:", err)
	}
	worktreeList := string(output)
	if !contains(worktreeList, "test-agent/042") {
		t.Error("First worktree not found in list")
	}
	if !contains(worktreeList, "test-agent-2/043") {
		t.Error("Second worktree not found in list")
	}

	// 6. Test cleanup
	err = wm.CleanupWorkspace(ctx, "test-agent", "042", agentWorkDir)
	if err != nil {
		t.Fatal("Cleanup failed:", err)
	}

	// Verify worktree is removed
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Error("Worktree should be removed after cleanup")
	}

	// Verify worktree is no longer listed
	output, err = gitRunner.Run(ctx, wm.buildMirrorPath(), "worktree", "list")
	if err != nil {
		t.Fatal("Failed to list worktrees after cleanup:", err)
	}
	worktreeList = string(output)
	if contains(worktreeList, "test-agent/042") {
		t.Error("Cleaned up worktree should not be in list")
	}
	if !contains(worktreeList, "test-agent-2/043") {
		t.Error("Other worktree should still be in list")
	}
}

// Helper functions
func parseMockCommand(cmd string) []string {
	// Simple parsing for mock command signatures
	parts := []string{cmd}
	return parts
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && 
		   (s == substr || (len(s) > len(substr) && 
		   (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr ||
		   containsSubstring(s, substr))))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}