//go:build integration
// +build integration

package integration

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dockerexec "orchestrator/pkg/exec"
	"orchestrator/pkg/tools"
)

// TestContainerBuildIntegration tests the container_build tool using the container test framework.
// This test runs the real MCP tool inside a container environment to match production behavior.
func TestContainerBuildIntegration(t *testing.T) {
	// Skip if Docker is not available
	if !isDockerAvailable() {
		t.Skip("Docker not available, skipping container build integration test")
	}

	// Setup test environment
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Create temporary workspace
	tempDir := t.TempDir()

	// Create a simple test Dockerfile
	dockerfileContent := `FROM alpine:latest
RUN echo "test container built successfully"
CMD ["echo", "Hello from test container"]
`

	// Test different scenarios
	testCases := []struct {
		name           string
		dockerfilePath string
		containerName  string
		setupFunc      func(string) error
		expectSuccess  bool
	}{
		{
			name:           "dockerfile_in_workspace_root",
			dockerfilePath: "Dockerfile",
			containerName:  "maestro-test-root",
			setupFunc: func(workspaceDir string) error {
				return os.WriteFile(filepath.Join(workspaceDir, "Dockerfile"), []byte(dockerfileContent), 0644)
			},
			expectSuccess: true,
		},
		{
			name:           "dockerfile_in_subdirectory",
			dockerfilePath: "docker/Dockerfile",
			containerName:  "maestro-test-subdir",
			setupFunc: func(workspaceDir string) error {
				dockerDir := filepath.Join(workspaceDir, "docker")
				if err := os.MkdirAll(dockerDir, 0755); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(dockerDir, "Dockerfile"), []byte(dockerfileContent), 0644)
			},
			expectSuccess: true,
		},
		{
			name:           "absolute_dockerfile_path",
			dockerfilePath: "/workspace/absolute/Dockerfile",
			containerName:  "maestro-test-absolute",
			setupFunc: func(workspaceDir string) error {
				absDir := filepath.Join(workspaceDir, "absolute")
				if err := os.MkdirAll(absDir, 0755); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(absDir, "Dockerfile"), []byte(dockerfileContent), 0644)
			},
			expectSuccess: true,
		},
		{
			name:           "nonexistent_dockerfile",
			dockerfilePath: "nonexistent/Dockerfile",
			containerName:  "maestro-test-missing",
			setupFunc:      func(workspaceDir string) error { return nil }, // Don't create file
			expectSuccess:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create workspace for this test case
			workspaceDir := filepath.Join(tempDir, tc.name)
			if err := os.MkdirAll(workspaceDir, 0755); err != nil {
				t.Fatalf("Failed to create workspace dir: %v", err)
			}

			// Setup test case (create dockerfile, etc.)
			if err := tc.setupFunc(workspaceDir); err != nil {
				t.Fatalf("Failed to setup test case: %v", err)
			}

			// Create container test framework
			framework, err := NewContainerTestFramework(t, workspaceDir)
			if err != nil {
				t.Fatalf("Failed to create test framework: %v", err)
			}
			defer framework.Cleanup(ctx)

			// Start container
			if err := framework.StartContainer(ctx); err != nil {
				t.Fatalf("Failed to start container: %v", err)
			}

			// Ensure target container is cleaned up regardless of test outcome
			defer cleanupBuiltContainer(tc.containerName)

			// Prepare tool arguments
			args := map[string]interface{}{
				"container_name":  tc.containerName,
				"dockerfile_path": tc.dockerfilePath,
				"cwd":             "/workspace",
			}

			// Create container build tool with the container executor
			tool := tools.NewContainerBuildTool(framework.GetExecutor())

			// Execute the tool and verify results
			if tc.expectSuccess {
				result, err := tool.Exec(ctx, args)
				if err != nil {
					t.Fatalf("Tool execution failed: %v", err)
				}

				// Verify result structure
				resultMap, ok := result.(map[string]interface{})
				if !ok {
					t.Fatalf("Expected result to be map[string]interface{}, got %T", result)
				}

				success, ok := resultMap["success"].(bool)
				if !ok || !success {
					t.Fatalf("Expected success=true, got success=%v", resultMap["success"])
				}

				// Verify target container was actually built on host
				if !isContainerBuilt(tc.containerName) {
					t.Fatalf("Target container %s was not built on host", tc.containerName)
				}

				t.Logf("✅ Successfully built container %s with dockerfile %s", tc.containerName, tc.dockerfilePath)

			} else {
				_, err := tool.Exec(ctx, args)
				if err == nil {
					t.Fatalf("Tool was expected to fail but succeeded")
				}
				t.Logf("✅ Expected error occurred: %v", err)
			}
		})
	}
}

// isDockerAvailable checks if Docker is available on the system
func isDockerAvailable() bool {
	cmd := osexec.Command("docker", "version")
	return cmd.Run() == nil
}

// isContainerBuilt checks if a container image exists on the host Docker daemon
func isContainerBuilt(containerName string) bool {
	cmd := osexec.Command("docker", "images", "-q", containerName)
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(string(output))) > 0
}

// cleanupBuiltContainer removes a built container image
func cleanupBuiltContainer(imageName string) {
	if imageName == "" {
		return
	}
	// Check if image exists before trying to remove it
	if !isContainerBuilt(imageName) {
		return // Image doesn't exist, nothing to clean up
	}

	cmd := osexec.Command("docker", "rmi", "-f", imageName)
	if err := cmd.Run(); err != nil {
		// Log but don't fail test - image might be in use or already removed
		fmt.Printf("Warning: Failed to cleanup image %s: %v\n", imageName, err)
	}
}

// TestGitOperationsInContainer tests git operations inside a container to verify PREPARE_MERGE functionality
func TestGitOperationsInContainer(t *testing.T) {
	// Skip if Docker is not available
	if !isDockerAvailable() {
		t.Skip("Docker not available, skipping git operations integration test")
	}

	// Setup test environment
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Create temporary workspace directory
	tempDir := t.TempDir()

	// Create executor and start container
	executor := dockerexec.NewLongRunningDockerExec("maestro-bootstrap:latest", "git-test")

	// Configure container options
	opts := &dockerexec.Opts{
		WorkDir: tempDir, // This will be auto-mounted as /workspace in container
		User:    "0:0",   // Run as root to avoid permission issues
		Env: []string{
			"DOCKER_HOST=unix:///var/run/docker.sock",
		},
	}

	// Inject GitHub token if available
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		opts.Env = append(opts.Env, "GITHUB_TOKEN="+token)
		t.Logf("GitHub token available for authentication testing")
	} else {
		t.Logf("No GitHub token available - gh auth status will likely fail")
	}

	// Start container
	containerName, err := executor.StartContainer(ctx, "git-test", opts)
	if err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}
	defer func() {
		_ = executor.StopContainer(ctx, containerName)
	}()

	t.Logf("Started git test container: %s", containerName)

	// Test git operations in sequence
	runGitInit(ctx, t, executor)
	runGitConfig(ctx, t, executor)
	runInitialCommit(ctx, t, executor)
	runFileChangesAndCommit(ctx, t, executor)
	runGitDiffOperations(ctx, t, executor)
	runGitStatusAndLog(ctx, t, executor)
	runGitHubCLIAuth(ctx, t, executor)

	t.Logf("✅ All git operations completed successfully")
}

func runGitInit(ctx context.Context, t *testing.T, executor *dockerexec.LongRunningDockerExec) {
	result, err := executor.Run(ctx, []string{"git", "init"}, &dockerexec.Opts{
		WorkDir: "/workspace",
		Timeout: 10 * time.Second,
	})

	if err != nil || result.ExitCode != 0 {
		t.Fatalf("git init failed: %v (exit: %d)\\nStdout: %s\\nStderr: %s",
			err, result.ExitCode, result.Stdout, result.Stderr)
	}

	t.Logf("✅ git init successful: %s", strings.TrimSpace(result.Stdout))
}

func runGitConfig(ctx context.Context, t *testing.T, executor *dockerexec.LongRunningDockerExec) {
	opts := &dockerexec.Opts{
		WorkDir: "/workspace",
		Timeout: 10 * time.Second,
	}

	// Set git user email
	result, err := executor.Run(ctx, []string{"git", "config", "user.email", "test@maestro.dev"}, opts)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("git config user.email failed: %v (exit: %d)\\nStderr: %s", err, result.ExitCode, result.Stderr)
	}

	// Set git user name
	result, err = executor.Run(ctx, []string{"git", "config", "user.name", "Maestro Test"}, opts)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("git config user.name failed: %v (exit: %d)\\nStderr: %s", err, result.ExitCode, result.Stderr)
	}

	t.Logf("✅ git config successful")
}

func runInitialCommit(ctx context.Context, t *testing.T, executor *dockerexec.LongRunningDockerExec) {
	opts := &dockerexec.Opts{
		WorkDir: "/workspace",
		Timeout: 10 * time.Second,
	}

	// Create a test file
	result, err := executor.Run(ctx, []string{"sh", "-c", "echo 'Initial test file' > README.md"}, opts)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("Failed to create test file: %v (exit: %d)\\nStderr: %s", err, result.ExitCode, result.Stderr)
	}

	// Add file to git
	result, err = executor.Run(ctx, []string{"git", "add", "README.md"}, opts)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("git add failed: %v (exit: %d)\\nStderr: %s", err, result.ExitCode, result.Stderr)
	}

	// Create initial commit
	result, err = executor.Run(ctx, []string{"git", "commit", "-m", "Initial commit"}, opts)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("git commit failed: %v (exit: %d)\\nStderr: %s", err, result.ExitCode, result.Stderr)
	}

	t.Logf("✅ Initial commit successful: %s", strings.TrimSpace(result.Stdout))
}

func runFileChangesAndCommit(ctx context.Context, t *testing.T, executor *dockerexec.LongRunningDockerExec) {
	opts := &dockerexec.Opts{
		WorkDir: "/workspace",
		Timeout: 15 * time.Second,
	}

	// Create new files (simulating code changes)
	result, err := executor.Run(ctx, []string{"sh", "-c", "echo 'package main' > main.go"}, opts)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("Failed to create main.go: %v (exit: %d)", err, result.ExitCode)
	}

	result, err = executor.Run(ctx, []string{"sh", "-c", "echo 'Updated content' >> README.md"}, opts)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("Failed to update README.md: %v (exit: %d)", err, result.ExitCode)
	}

	// Test git add -A (exactly what PREPARE_MERGE does)
	result, err = executor.Run(ctx, []string{"git", "add", "-A"}, opts)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("git add -A failed: %v (exit: %d)\\nStderr: %s", err, result.ExitCode, result.Stderr)
	}

	// Check staged changes
	result, err = executor.Run(ctx, []string{"git", "diff", "--cached"}, opts)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("git diff --cached failed: %v (exit: %d)\\nStderr: %s", err, result.ExitCode, result.Stderr)
	}

	if strings.TrimSpace(result.Stdout) == "" {
		t.Fatal("Expected staged changes but git diff --cached returned empty")
	}

	// Commit changes (exactly what PREPARE_MERGE does)
	commitMsg := "Story test-123: Implementation complete\\n\\nAutomated commit by maestro coder agent"
	result, err = executor.Run(ctx, []string{"git", "commit", "-m", commitMsg}, opts)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("git commit failed: %v (exit: %d)\\nStderr: %s", err, result.ExitCode, result.Stderr)
	}

	t.Logf("✅ File changes and commit successful: %s", strings.TrimSpace(result.Stdout))
}

func runGitDiffOperations(ctx context.Context, t *testing.T, executor *dockerexec.LongRunningDockerExec) {
	opts := &dockerexec.Opts{
		WorkDir: "/workspace",
		Timeout: 10 * time.Second,
	}

	// Test the exact command that was failing: git diff HEAD
	result, err := executor.Run(ctx, []string{"git", "diff", "HEAD"}, opts)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("git diff HEAD failed: %v (exit: %d)\\nStderr: %s", err, result.ExitCode, result.Stderr)
	}

	// Should be empty since we just committed everything
	if strings.TrimSpace(result.Stdout) != "" {
		t.Logf("Note: git diff HEAD returned content (might be expected): %s", result.Stdout)
	}

	// Create a new uncommitted change
	result, err = executor.Run(ctx, []string{"sh", "-c", "echo '// New change' >> main.go"}, opts)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("Failed to create uncommitted change: %v", err)
	}

	// Test git diff HEAD with changes
	result, err = executor.Run(ctx, []string{"git", "diff", "HEAD"}, opts)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("git diff HEAD (with changes) failed: %v (exit: %d)\\nStderr: %s", err, result.ExitCode, result.Stderr)
	}

	// Should show the new change
	if !strings.Contains(result.Stdout, "New change") {
		t.Fatalf("Expected git diff to show 'New change', got: %s", result.Stdout)
	}

	t.Logf("✅ git diff HEAD operations successful")
}

func runGitStatusAndLog(ctx context.Context, t *testing.T, executor *dockerexec.LongRunningDockerExec) {
	opts := &dockerexec.Opts{
		WorkDir: "/workspace",
		Timeout: 10 * time.Second,
	}

	// Test git status
	result, err := executor.Run(ctx, []string{"git", "status", "--porcelain"}, opts)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("git status failed: %v (exit: %d)\\nStderr: %s", err, result.ExitCode, result.Stderr)
	}

	// Should show modified main.go from previous test
	statusOutput := strings.TrimSpace(result.Stdout)
	if !strings.Contains(statusOutput, "main.go") {
		t.Fatalf("Expected git status to show modified main.go, got: %s", statusOutput)
	}

	// Test git log
	result, err = executor.Run(ctx, []string{"git", "log", "--oneline", "-n", "5"}, opts)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("git log failed: %v (exit: %d)\\nStderr: %s", err, result.ExitCode, result.Stderr)
	}

	// Should show our commits
	logOutput := strings.TrimSpace(result.Stdout)
	if !strings.Contains(logOutput, "Initial commit") || !strings.Contains(logOutput, "Implementation complete") {
		t.Fatalf("Expected git log to show our commits, got: %s", logOutput)
	}

	t.Logf("✅ git status and log successful")
}

func runGitHubCLIAuth(ctx context.Context, t *testing.T, executor *dockerexec.LongRunningDockerExec) {
	opts := &dockerexec.Opts{
		WorkDir: "/workspace",
		Timeout: 15 * time.Second,
	}

	// First check if gh is available
	result, err := executor.Run(ctx, []string{"gh", "--version"}, opts)
	if err != nil || result.ExitCode != 0 {
		t.Logf("GitHub CLI not available in container (expected): %v (exit: %d)", err, result.ExitCode)
		return
	}

	t.Logf("GitHub CLI version: %s", strings.TrimSpace(result.Stdout))

	// Test gh auth status
	result, err = executor.Run(ctx, []string{"gh", "auth", "status"}, opts)

	// Log the result regardless of success/failure for debugging
	t.Logf("gh auth status result: exit_code=%d", result.ExitCode)
	if result.Stdout != "" {
		t.Logf("gh auth stdout: %s", result.Stdout)
	}
	if result.Stderr != "" {
		t.Logf("gh auth stderr: %s", result.Stderr)
	}

	// Don't fail the test if auth fails - this is expected without GitHub token
	if err != nil || result.ExitCode != 0 {
		t.Logf("GitHub CLI authentication failed (expected without token): %v (exit: %d)", err, result.ExitCode)
		return
	}

	// If we get here, authentication is working
	t.Logf("✅ GitHub CLI authentication successful")
}
