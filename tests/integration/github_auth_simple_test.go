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
	"orchestrator/pkg/exec"
)

// TestGitHubCredentialHelperSetup tests the core fix: gh auth setup-git configures git properly.
func TestGitHubCredentialHelperSetup(t *testing.T) {
	// Skip if Docker is not available
	if _, err := os.Stat("/var/run/docker.sock"); err != nil {
		t.Skip("Docker not available, skipping test")
	}

	// Skip if GITHUB_TOKEN is not available
	if !config.HasGitHubToken() {
		t.Skip("GITHUB_TOKEN not available, skipping test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	executor := exec.NewLocalExec()
	containerName := config.BootstrapContainerTag

	t.Logf("Testing GitHub credential setup with container: %s", containerName)

	// Test 1: Verify basic tools are available
	t.Run("tools_available", func(t *testing.T) {
		gitResult, err := executor.Run(ctx, []string{"docker", "run", "--rm", containerName, "git", "--version"}, &exec.Opts{Timeout: 30 * time.Second})
		if err != nil || gitResult.ExitCode != 0 {
			t.Fatalf("Git not available: %v (stdout: %s, stderr: %s)", err, gitResult.Stdout, gitResult.Stderr)
		}

		ghResult, err := executor.Run(ctx, []string{"docker", "run", "--rm", containerName, "gh", "--version"}, &exec.Opts{Timeout: 30 * time.Second})
		if err != nil || ghResult.ExitCode != 0 {
			t.Fatalf("GitHub CLI not available: %v (stdout: %s, stderr: %s)", err, ghResult.Stdout, ghResult.Stderr)
		}

		t.Logf("✅ Both git and gh CLI are available")
	})

	// Test 2: Test gh auth setup-git command
	t.Run("gh_auth_setup_git", func(t *testing.T) {
		result, err := executor.Run(ctx, []string{
			"docker", "run", "--rm", "-e", "GITHUB_TOKEN", containerName,
			"sh", "-c", "gh auth setup-git && echo 'SUCCESS'"},
			&exec.Opts{Timeout: 30 * time.Second})

		if err != nil {
			t.Fatalf("gh auth setup-git command failed: %v", err)
		}
		if result.ExitCode != 0 {
			t.Fatalf("gh auth setup-git failed with exit code %d: stdout=%s, stderr=%s",
				result.ExitCode, result.Stdout, result.Stderr)
		}
		if !strings.Contains(result.Stdout, "SUCCESS") {
			t.Fatalf("gh auth setup-git did not complete properly: %s", result.Stdout)
		}

		t.Logf("✅ gh auth setup-git completed successfully")
	})

	// Test 3: Verify credential helper configuration persists
	t.Run("credential_helper_configured", func(t *testing.T) {
		result, err := executor.Run(ctx, []string{
			"docker", "run", "--rm", "-e", "GITHUB_TOKEN", containerName,
			"sh", "-c", "gh auth setup-git && git config --global --list | grep credential"},
			&exec.Opts{Timeout: 30 * time.Second})

		if err != nil {
			t.Fatalf("Failed to check git credential config: %v", err)
		}
		if result.ExitCode != 0 {
			t.Fatalf("Git credential config check failed: stdout=%s, stderr=%s", result.Stdout, result.Stderr)
		}

		stdout := result.Stdout
		if !strings.Contains(stdout, "credential.https://github.com.helper") ||
			!strings.Contains(stdout, "gh auth git-credential") {
			t.Fatalf("Git credential helper not configured properly. Output:\n%s", stdout)
		}

		t.Logf("✅ Git credential helper configured: %s", strings.TrimSpace(stdout))
	})
}
