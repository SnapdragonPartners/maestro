package coder

import (
	"os/exec"
	"strings"
	"testing"
)

// TestPrepareMergeHelpers tests the helper functions without requiring full setup.
func TestPrepareMergeHelpers(t *testing.T) {
	// Test 1: getTargetBranch should return a valid branch name
	t.Run("GetTargetBranch", func(t *testing.T) {
		coder := &Coder{}
		targetBranch, err := coder.getTargetBranch()
		if err != nil {
			t.Logf("Config not available (expected in test): %v", err)
			if targetBranch == "" {
				t.Error("Target branch should default to non-empty value when config fails")
			}
		} else {
			if targetBranch == "" {
				t.Error("Target branch should not be empty")
			}
		}

		t.Logf("Target branch: %s", targetBranch)
	})

	// Test 2: Check if git command exists
	t.Run("GitCommandAvailable", func(t *testing.T) {
		_, err := exec.LookPath("git")
		if err != nil {
			t.Skip("Git command not found in PATH")
		}
		t.Log("✅ Git command is available")
	})

	// Test 3: Check if GitHub CLI exists
	t.Run("GitHubCLICommandAvailable", func(t *testing.T) {
		_, err := exec.LookPath("gh")
		if err != nil {
			t.Skip("GitHub CLI (gh) not found in PATH")
		}
		t.Log("✅ GitHub CLI command is available")
	})

	// Test 4: Verify git is working
	t.Run("GitVersion", func(t *testing.T) {
		cmd := exec.Command("git", "--version")
		output, err := cmd.Output()
		if err != nil {
			t.Skip("Git command failed")
		}
		t.Logf("✅ Git version: %s", strings.TrimSpace(string(output)))
	})

	// Test 5: Verify GitHub CLI is working
	t.Run("GitHubCLIVersion", func(t *testing.T) {
		cmd := exec.Command("gh", "--version")
		output, err := cmd.Output()
		if err != nil {
			t.Skip("GitHub CLI command failed")
		}
		t.Logf("✅ GitHub CLI version: %s", strings.TrimSpace(string(output)))
	})
}

// TestIsRecoverableGitError tests the error categorization logic.
func TestIsRecoverableGitError(t *testing.T) {
	coder := &Coder{}

	testCases := []struct {
		name        string
		errorMsg    string
		recoverable bool
	}{
		{"NilError", "", false},
		{"NetworkError", "network timeout", true},
		{"MergeConflict", "merge conflict in file.txt", true},
		{"PermissionDenied", "permission denied", true},
		{"BranchExists", "branch already exists", true},
		{"GitNotFound", "git: command not found", false},
		{"NotGitRepo", "not a git repository", false},
		{"UnknownError", "some random error", true}, // Default to recoverable
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			if tc.errorMsg != "" {
				err = &execError{msg: tc.errorMsg}
			}

			result := coder.isRecoverableGitError(err)
			if result != tc.recoverable {
				t.Errorf("Expected recoverable=%v for error '%s', got %v",
					tc.recoverable, tc.errorMsg, result)
			}
		})
	}
}

// execError is a simple error type for testing.
type execError struct {
	msg string
}

func (e *execError) Error() string {
	return e.msg
}
