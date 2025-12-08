package mirror

import (
	"os"
	"path/filepath"
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
