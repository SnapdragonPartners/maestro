//go:build integration
// +build integration

package integration

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"orchestrator/pkg/config"
	dockerexec "orchestrator/pkg/exec"
	"orchestrator/pkg/tools"
)

// TestGitHubAuthenticationIntegration tests the complete GitHub authentication setup flow.
// This validates that gh auth setup-git properly configures git credential helpers.
func TestGitHubAuthenticationIntegration(t *testing.T) {
	// Skip if Docker is not available
	if !isDockerAvailable() {
		t.Skip("Docker not available, skipping GitHub auth integration test")
	}

	// Skip if GITHUB_TOKEN is not available
	if !config.HasGitHubToken() {
		t.Skip("GITHUB_TOKEN not available, skipping GitHub auth integration test")
	}

	// Setup test environment
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Create test workspace
	tempDir := t.TempDir()

	// Use maestro-bootstrap container (known to have git + gh CLI)
	executor := dockerexec.NewLongRunningDockerExec(config.BootstrapContainerTag, "github-auth-test")

	// Start container with GitHub token
	opts := &dockerexec.Opts{
		WorkDir: tempDir,
		User:    "0:0",                    // Run as root to avoid permission issues
		Env:     []string{"GITHUB_TOKEN"}, // Pass through GITHUB_TOKEN
	}

	containerName, err := executor.StartContainer(ctx, "github-auth-test", opts)
	if err != nil {
		t.Fatalf("Failed to start test container: %v", err)
	}
	defer func() {
		_ = executor.StopContainer(ctx, containerName)
	}()

	t.Logf("Started GitHub auth test container: %s", containerName)

	// Test 1: Validate container has required tools
	t.Run("validate_container_capabilities", func(t *testing.T) {
		validation := tools.ValidateContainerCapabilities(ctx, dockerexec.NewLocalExec(), config.BootstrapContainerTag)
		if !validation.Success {
			t.Fatalf("Bootstrap container validation failed: %s. Missing: %v", validation.Message, validation.MissingTools)
		}
		t.Logf("✅ Container validation passed: git=%v, gh=%v, api=%v",
			validation.GitAvailable, validation.GHAvailable, validation.GitHubAPIValid)
	})

	// Test 2: Test gh auth setup-git command
	t.Run("gh_auth_setup_git", func(t *testing.T) {
		execOpts := &dockerexec.Opts{
			WorkDir: "/workspace",
			Timeout: 30 * time.Second,
		}

		// Run gh auth setup-git
		result, err := executor.Run(ctx, []string{"gh", "auth", "setup-git"}, execOpts)
		if err != nil {
			t.Fatalf("gh auth setup-git failed: %v", err)
		}
		if result.ExitCode != 0 {
			t.Fatalf("gh auth setup-git failed with exit code %d: stdout=%s, stderr=%s",
				result.ExitCode, result.Stdout, result.Stderr)
		}

		t.Logf("✅ gh auth setup-git completed successfully")
	})

	// Test 3: Verify git credential helper is configured
	t.Run("verify_credential_helper", func(t *testing.T) {
		execOpts := &dockerexec.Opts{
			WorkDir: "/workspace",
			Timeout: 10 * time.Second,
		}

		// Check git config for credential helper
		result, err := executor.Run(ctx, []string{"git", "config", "--list"}, execOpts)
		if err != nil {
			t.Fatalf("git config --list failed: %v", err)
		}

		stdout := result.Stdout
		if !containsCredentialHelper(stdout) {
			t.Fatalf("Git credential helper not configured properly. Git config output:\n%s", stdout)
		}

		t.Logf("✅ Git credential helper configured correctly")
	})

	// Test 4: Test GitHub API access still works after setup
	t.Run("github_api_access_after_setup", func(t *testing.T) {
		execOpts := &dockerexec.Opts{
			WorkDir: "/workspace",
			Timeout: 30 * time.Second,
		}

		// Test /user endpoint
		userResult, err := executor.Run(ctx, []string{"gh", "api", "/user"}, execOpts)
		if err != nil || userResult.ExitCode != 0 {
			t.Fatalf("GitHub API /user failed after setup: %v (stdout: %s, stderr: %s)",
				err, userResult.Stdout, userResult.Stderr)
		}

		// Test repository access (using config repo URL)
		cfg, err := config.GetConfig()
		if err != nil {
			t.Fatalf("Failed to get config: %v", err)
		}

		if cfg.Git != nil && cfg.Git.RepoURL != "" {
			repoPath := extractRepoPathForTest(cfg.Git.RepoURL)
			if repoPath != "" {
				repoResult, err := executor.Run(ctx, []string{"gh", "api", "/repos/" + repoPath}, execOpts)
				if err != nil || repoResult.ExitCode != 0 {
					t.Fatalf("GitHub API repository access failed after setup: %v (stdout: %s, stderr: %s)",
						err, repoResult.Stdout, repoResult.Stderr)
				}
				t.Logf("✅ GitHub API repository access validated for: %s", repoPath)
			}
		}

		t.Logf("✅ GitHub API access working after credential setup")
	})
}

// Helper function to check if git credential helper is configured
func containsCredentialHelper(gitConfig string) bool {
	return strings.Contains(gitConfig, "credential.https://github.com.helper=") &&
		strings.Contains(gitConfig, "gh auth git-credential")
}

// Helper function to extract repo path for testing
func extractRepoPathForTest(repoURL string) string {
	url := strings.TrimSuffix(repoURL, ".git")

	if strings.HasPrefix(url, "https://github.com/") {
		path := strings.TrimPrefix(url, "https://github.com/")
		if strings.Count(path, "/") >= 1 {
			return path
		}
	}

	if strings.HasPrefix(url, "git@github.com:") {
		path := strings.TrimPrefix(url, "git@github.com:")
		if strings.Count(path, "/") >= 1 {
			return path
		}
	}

	return ""
}
