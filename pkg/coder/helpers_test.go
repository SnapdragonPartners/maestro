package coder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTruncateSHA(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "full SHA",
			input:    "abc123def456789012345678901234567890abcd",
			expected: "abc123de",
		},
		{
			name:     "exactly 8 chars",
			input:    "abc12345",
			expected: "abc12345",
		},
		{
			name:     "shorter than 8 chars",
			input:    "abc",
			expected: "abc",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "9 chars",
			input:    "123456789",
			expected: "12345678",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateSHA(tt.input)
			if result != tt.expected {
				t.Errorf("truncateSHA(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestTruncateOutput(t *testing.T) {
	tests := []struct {
		name        string
		output      string
		shouldTrunc bool
	}{
		{
			name:        "short output",
			output:      "hello",
			shouldTrunc: false,
		},
		{
			name:        "empty output",
			output:      "",
			shouldTrunc: false,
		},
		{
			name:        "at limit",
			output:      string(make([]byte, maxOutputLength)),
			shouldTrunc: false,
		},
		{
			name:        "over limit",
			output:      string(make([]byte, maxOutputLength+100)),
			shouldTrunc: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateOutput(tt.output)
			if tt.shouldTrunc {
				if len(result) >= len(tt.output) {
					t.Errorf("expected output to be truncated, but got same or longer length")
				}
				if !strings.Contains(result, "truncated") {
					t.Errorf("expected truncated output to contain 'truncated' message")
				}
			} else {
				if result != tt.output {
					t.Errorf("expected unchanged output %q, got %q", tt.output, result)
				}
			}
		})
	}
}

func TestFileExists(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()

	// Create a test file
	testFile := filepath.Join(tempDir, "testfile.txt")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	tests := []struct {
		name     string
		path     string
		expected bool
	}{
		{
			name:     "existing file",
			path:     testFile,
			expected: true,
		},
		{
			name:     "non-existent file",
			path:     filepath.Join(tempDir, "nonexistent.txt"),
			expected: false,
		},
		{
			name:     "directory exists",
			path:     tempDir,
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := fileExists(tt.path)
			if result != tt.expected {
				t.Errorf("fileExists(%q) = %v, want %v", tt.path, result, tt.expected)
			}
		})
	}
}

func TestExtractRepoPath(t *testing.T) {
	tests := []struct {
		name     string
		repoURL  string
		expected string
	}{
		{
			name:     "https URL",
			repoURL:  "https://github.com/owner/repo.git",
			expected: "owner/repo",
		},
		{
			name:     "https URL without .git",
			repoURL:  "https://github.com/owner/repo",
			expected: "owner/repo",
		},
		{
			name:     "ssh URL",
			repoURL:  "git@github.com:owner/repo.git",
			expected: "owner/repo",
		},
		{
			name:     "ssh URL without .git",
			repoURL:  "git@github.com:owner/repo",
			expected: "owner/repo",
		},
		{
			name:     "empty URL",
			repoURL:  "",
			expected: "",
		},
		{
			name:     "URL with extra slashes",
			repoURL:  "https://github.com/org/sub/repo.git",
			expected: "org/sub/repo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractRepoPath(tt.repoURL)
			if result != tt.expected {
				t.Errorf("extractRepoPath(%q) = %q, want %q", tt.repoURL, result, tt.expected)
			}
		})
	}
}
