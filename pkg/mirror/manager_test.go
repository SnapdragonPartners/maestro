package mirror

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractRepoName(t *testing.T) {
	tests := []struct {
		name     string
		repoURL  string
		expected string
	}{
		{
			name:     "HTTPS URL with .git suffix",
			repoURL:  "https://github.com/user/my-project.git",
			expected: "my-project.git",
		},
		{
			name:     "HTTPS URL without .git suffix",
			repoURL:  "https://github.com/user/my-project",
			expected: "my-project.git",
		},
		{
			name:     "URL with organization",
			repoURL:  "https://github.com/my-org/my-repo",
			expected: "my-repo.git",
		},
		{
			name:     "URL with nested path",
			repoURL:  "https://github.com/org/subgroup/repo",
			expected: "repo.git",
		},
		{
			name:     "SSH URL format",
			repoURL:  "git@github.com:user/repo.git",
			expected: "repo.git",
		},
		{
			name:     "Empty URL",
			repoURL:  "",
			expected: "repo.git",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractRepoName(tt.repoURL)
			if result != tt.expected {
				t.Errorf("extractRepoName(%q) = %q, want %q", tt.repoURL, result, tt.expected)
			}
		})
	}
}

func TestMirrorExists(t *testing.T) {
	tmpDir := t.TempDir()

	// Empty directory should not be a mirror
	if mirrorExists(tmpDir) {
		t.Error("Expected mirrorExists = false for empty directory")
	}

	// Create HEAD file (bare repo indicator)
	headPath := filepath.Join(tmpDir, "HEAD")
	if err := os.WriteFile(headPath, []byte("ref: refs/heads/main"), 0644); err != nil {
		t.Fatalf("Failed to create HEAD file: %v", err)
	}

	// Now it should be detected as a mirror
	if !mirrorExists(tmpDir) {
		t.Error("Expected mirrorExists = true when HEAD file exists")
	}
}

func TestMirrorExists_NonExistentPath(t *testing.T) {
	// Non-existent path should not be a mirror
	if mirrorExists("/nonexistent/path/to/mirror") {
		t.Error("Expected mirrorExists = false for non-existent path")
	}
}

// initBareRepo creates a bare git repo at the given path for testing.
func initBareRepo(t *testing.T, path string) {
	t.Helper()
	cmd := exec.Command("git", "init", "--bare", path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare failed: %v\n%s", err, output)
	}
}

func TestValidateMirror_Healthy(t *testing.T) {
	tmpDir := t.TempDir()
	mirrorPath := filepath.Join(tmpDir, "test.git")
	initBareRepo(t, mirrorPath)

	// Add origin remote (required by validateMirror)
	cmd := exec.Command("git", "-C", mirrorPath, "remote", "add", "origin", "https://example.com/repo.git")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git remote add failed: %v\n%s", err, output)
	}

	mgr := NewManager(tmpDir)
	if err := mgr.validateMirror(context.Background(), mirrorPath); err != nil {
		t.Errorf("validateMirror returned error for healthy repo: %v", err)
	}
}

func TestValidateMirror_NoHEAD(t *testing.T) {
	tmpDir := t.TempDir()
	mirrorPath := filepath.Join(tmpDir, "test.git")
	if err := os.MkdirAll(mirrorPath, 0755); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(tmpDir)
	err := mgr.validateMirror(context.Background(), mirrorPath)
	if err == nil {
		t.Fatal("Expected error for directory without HEAD")
	}
	if !strings.Contains(err.Error(), "missing HEAD") {
		t.Errorf("Expected 'missing HEAD' in error, got: %v", err)
	}
}

func TestValidateMirror_NoOriginRemote(t *testing.T) {
	tmpDir := t.TempDir()
	mirrorPath := filepath.Join(tmpDir, "test.git")
	initBareRepo(t, mirrorPath)
	// Deliberately do NOT add origin remote

	mgr := NewManager(tmpDir)
	err := mgr.validateMirror(context.Background(), mirrorPath)
	if err == nil {
		t.Fatal("Expected error for repo without origin remote")
	}
	if !strings.Contains(err.Error(), "origin remote missing") {
		t.Errorf("Expected 'origin remote missing' in error, got: %v", err)
	}
}

func TestEnsureRemoteURL_AddWhenMissing(t *testing.T) {
	tmpDir := t.TempDir()
	mirrorPath := filepath.Join(tmpDir, "test.git")
	initBareRepo(t, mirrorPath)

	mgr := NewManager(tmpDir)
	err := mgr.ensureRemoteURL(context.Background(), mirrorPath, "https://example.com/repo.git")
	if err != nil {
		t.Fatalf("ensureRemoteURL failed: %v", err)
	}

	// Verify origin was added
	cmd := exec.Command("git", "-C", mirrorPath, "remote", "get-url", "origin")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git remote get-url failed after add: %v", err)
	}
	if got := strings.TrimSpace(string(output)); got != "https://example.com/repo.git" {
		t.Errorf("Expected URL 'https://example.com/repo.git', got '%s'", got)
	}
}

func TestEnsureRemoteURL_UpdateWhenDifferent(t *testing.T) {
	tmpDir := t.TempDir()
	mirrorPath := filepath.Join(tmpDir, "test.git")
	initBareRepo(t, mirrorPath)

	// Add origin with old URL
	cmd := exec.Command("git", "-C", mirrorPath, "remote", "add", "origin", "https://old.com/repo.git")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git remote add failed: %v\n%s", err, output)
	}

	mgr := NewManager(tmpDir)
	err := mgr.ensureRemoteURL(context.Background(), mirrorPath, "https://new.com/repo.git")
	if err != nil {
		t.Fatalf("ensureRemoteURL failed: %v", err)
	}

	// Verify URL was updated
	verifyCmd := exec.Command("git", "-C", mirrorPath, "remote", "get-url", "origin")
	output, err := verifyCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git remote get-url failed: %v", err)
	}
	if got := strings.TrimSpace(string(output)); got != "https://new.com/repo.git" {
		t.Errorf("Expected URL 'https://new.com/repo.git', got '%s'", got)
	}
}

func TestEnsureRemoteURL_NoChangeWhenSame(t *testing.T) {
	tmpDir := t.TempDir()
	mirrorPath := filepath.Join(tmpDir, "test.git")
	initBareRepo(t, mirrorPath)

	targetURL := "https://example.com/repo.git"
	cmd := exec.Command("git", "-C", mirrorPath, "remote", "add", "origin", targetURL)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git remote add failed: %v\n%s", err, output)
	}

	mgr := NewManager(tmpDir)
	err := mgr.ensureRemoteURL(context.Background(), mirrorPath, targetURL)
	if err != nil {
		t.Fatalf("ensureRemoteURL failed: %v", err)
	}

	// Verify URL is still correct
	verifyCmd := exec.Command("git", "-C", mirrorPath, "remote", "get-url", "origin")
	output, err := verifyCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git remote get-url failed: %v", err)
	}
	if got := strings.TrimSpace(string(output)); got != targetURL {
		t.Errorf("Expected URL '%s', got '%s'", targetURL, got)
	}
}
