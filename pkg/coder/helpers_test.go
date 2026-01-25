package coder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/pkg/logx"
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

func TestValidateMakefileTargets(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name            string
		makefileContent string
		expectError     bool
		errorContains   string
	}{
		{
			name:            "valid makefile with targets",
			makefileContent: "build:\n\tgo build ./...\n\ntest:\n\tgo test ./...\n",
			expectError:     false,
		},
		{
			name:            "minimal makefile",
			makefileContent: "all: build",
			expectError:     false,
		},
		{
			name:            "empty makefile",
			makefileContent: "",
			expectError:     true,
			errorContains:   "empty",
		},
		{
			name:            "whitespace only makefile",
			makefileContent: "   \n\t\n   ",
			expectError:     true,
			errorContains:   "empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test workspace with Makefile
			testWorkspace := filepath.Join(tmpDir, strings.ReplaceAll(tt.name, " ", "_"))
			if err := os.MkdirAll(testWorkspace, 0755); err != nil {
				t.Fatalf("Failed to create test workspace: %v", err)
			}
			makefilePath := filepath.Join(testWorkspace, "Makefile")
			if err := os.WriteFile(makefilePath, []byte(tt.makefileContent), 0644); err != nil {
				t.Fatalf("Failed to create Makefile: %v", err)
			}

			// Create a minimal coder for testing
			c := &Coder{
				logger: logx.NewLogger("test"),
			}

			err := c.validateMakefileTargets(testWorkspace)
			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got nil")
				} else if tt.errorContains != "" && !strings.Contains(err.Error(), tt.errorContains) {
					t.Errorf("expected error containing %q, got %q", tt.errorContains, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestValidateMakefileTargets_NoMakefile(t *testing.T) {
	tmpDir := t.TempDir()

	c := &Coder{
		logger: logx.NewLogger("test"),
	}

	err := c.validateMakefileTargets(tmpDir)
	if err == nil {
		t.Error("expected error for missing Makefile")
	}
	if !strings.Contains(err.Error(), "failed to read Makefile") {
		t.Errorf("expected error about failing to read Makefile, got: %v", err)
	}
}
