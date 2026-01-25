//go:build integration
// +build integration

package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/exec"
	"orchestrator/pkg/tools"
)

// TestContainerSecurityModel tests the security model where containers cannot push to remote.
// This validates:
// - Containers have git for local commits
// - Containers do NOT need gh CLI
// - Containers do NOT need GitHub credentials
// - Git push operations should run on host (not tested here - would require host git setup)
func TestContainerSecurityModel(t *testing.T) {
	// Skip if Docker is not available
	if _, err := os.Stat("/var/run/docker.sock"); err != nil {
		t.Skip("Docker not available, skipping test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	executor := exec.NewLocalExec()
	containerName := config.BootstrapContainerTag

	t.Logf("Testing container security model with container: %s", containerName)

	// Test 1: Verify container has required tools (git, UID 1000, writable /tmp)
	t.Run("container_validation", func(t *testing.T) {
		validation := tools.ValidateContainerCapabilities(ctx, executor, containerName)
		if !validation.Success {
			t.Fatalf("Container validation failed: %s. Missing: %v", validation.Message, validation.MissingTools)
		}
		t.Logf("Container validation passed: git=%v, uid_1000=%v, tmp_writable=%v",
			validation.GitAvailable, validation.UserUID1000, validation.TmpWritable)
	})

	// Test 2: Verify git is available
	t.Run("git_available", func(t *testing.T) {
		gitResult, err := executor.Run(ctx, []string{"docker", "run", "--rm", containerName, "git", "--version"}, &exec.Opts{Timeout: 30 * time.Second})
		if err != nil || gitResult.ExitCode != 0 {
			t.Fatalf("Git not available: %v (stdout: %s, stderr: %s)", err, gitResult.Stdout, gitResult.Stderr)
		}
		t.Logf("Git available: %s", gitResult.Stdout)
	})

	// Test 3: gh CLI is no longer required (PR operations run on host)
	t.Run("gh_cli_not_required", func(t *testing.T) {
		ghResult, err := executor.Run(ctx, []string{"docker", "run", "--rm", containerName, "which", "gh"}, &exec.Opts{Timeout: 30 * time.Second})
		if err == nil && ghResult.ExitCode == 0 {
			t.Logf("Note: gh CLI is present but not required (PR operations run on host)")
		} else {
			t.Logf("gh CLI not present - this is expected (PR operations run on host)")
		}
		// This test always passes - gh is optional
	})

	// Test 4: Verify container can do local git operations
	t.Run("local_git_operations", func(t *testing.T) {
		// Create a temp directory for git operations
		tempDir := t.TempDir()

		result, err := executor.Run(ctx, []string{
			"docker", "run", "--rm",
			"-v", tempDir + ":/workspace:rw",
			"-w", "/workspace",
			"-e", "HOME=/tmp",
			containerName, "sh", "-c", `
git init
git config user.name "Test User"
git config user.email "test@test.com"
echo "test" > test.txt
git add test.txt
git commit -m "Test commit"
echo "SUCCESS"
`}, &exec.Opts{Timeout: 30 * time.Second})

		if err != nil {
			t.Fatalf("Local git operations failed: %v", err)
		}
		if result.ExitCode != 0 {
			t.Fatalf("Local git operations failed with exit code %d: stdout=%s, stderr=%s",
				result.ExitCode, result.Stdout, result.Stderr)
		}

		t.Logf("Local git operations (init, commit) work correctly")
	})
}
