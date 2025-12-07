package integration

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"orchestrator/pkg/exec"
	"orchestrator/pkg/tools"
)

// TestNonRootExecution verifies that containers run as non-root user (UID 1000).
// This test validates the security configuration that ensures all coder operations
// run as unprivileged user, regardless of story type or execution mode.
func TestNonRootExecution(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Create a long-running docker executor
	dockerExec := exec.NewLongRunningDockerExec("alpine:latest", "test-nonroot")

	ctx := context.Background()
	workDir := t.TempDir()

	// Start container with non-root user (1000:1000) - same as production
	opts := &exec.Opts{
		WorkDir: workDir,
		User:    "1000:1000", // Same as coder production config
		Timeout: 30 * time.Second,
	}

	containerName, err := dockerExec.StartContainer(ctx, "test-nonroot", opts)
	if err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}
	defer func() {
		_ = dockerExec.StopContainer(ctx, containerName)
	}()

	// Test 1: Verify container process runs as UID 1000
	t.Run("ContainerStartedAsNonRoot", func(t *testing.T) {
		result, err := dockerExec.Run(ctx, []string{"id", "-u"}, opts)
		if err != nil {
			t.Fatalf("Failed to run id command: %v", err)
		}

		uid := strings.TrimSpace(result.Stdout)
		if uid != "1000" {
			t.Errorf("Expected UID 1000, got: %s", uid)
		}
		t.Logf("Container running as UID: %s", uid)
	})

	// Test 2: Verify docker exec uses the --user flag
	t.Run("DockerExecUsesUserFlag", func(t *testing.T) {
		// Run with explicit user option
		execOpts := &exec.Opts{
			WorkDir: workDir,
			User:    "1000:1000",
			Timeout: 30 * time.Second,
		}

		result, err := dockerExec.Run(ctx, []string{"id"}, execOpts)
		if err != nil {
			t.Fatalf("Failed to run id command: %v", err)
		}

		output := result.Stdout
		t.Logf("id output: %s", output)

		// Should show uid=1000 and gid=1000
		if !strings.Contains(output, "uid=1000") {
			t.Errorf("Expected uid=1000 in output, got: %s", output)
		}
		if !strings.Contains(output, "gid=1000") {
			t.Errorf("Expected gid=1000 in output, got: %s", output)
		}
	})

	// Test 3: Verify we can't write to root-owned directories
	t.Run("CannotWriteToRootDirs", func(t *testing.T) {
		result, err := dockerExec.Run(ctx, []string{"sh", "-c", "touch /root/testfile 2>&1 || echo 'permission denied'"}, opts)
		if err != nil {
			// Error is expected for permission denied
			t.Logf("Got expected error: %v", err)
		}

		output := result.Stdout + result.Stderr
		t.Logf("Write to /root output: %s", output)

		// Should fail with permission denied
		if !strings.Contains(strings.ToLower(output), "permission denied") &&
			!strings.Contains(strings.ToLower(output), "operation not permitted") &&
			result.ExitCode == 0 {
			t.Errorf("Expected permission denied when writing to /root, got: %s", output)
		}
	})

	// Test 4: Verify we CAN write to /tmp (always writable)
	t.Run("CanWriteToTmp", func(t *testing.T) {
		result, err := dockerExec.Run(ctx, []string{"sh", "-c", "echo 'test' > /tmp/testfile && cat /tmp/testfile"}, opts)
		if err != nil {
			t.Fatalf("Failed to write to /tmp: %v", err)
		}

		if strings.TrimSpace(result.Stdout) != "test" {
			t.Errorf("Expected 'test' output, got: %s", result.Stdout)
		}
		t.Logf("Successfully wrote to /tmp")
	})
}

// TestCanWriteToWorkspace verifies that non-root user can write to /workspace mount.
func TestCanWriteToWorkspace(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Create a long-running docker executor
	dockerExec := exec.NewLongRunningDockerExec("alpine:latest", "test-workspace-write")

	ctx := context.Background()
	workDir := t.TempDir()

	// Make workspace writable by non-root user (UID 1000) in container.
	// The host temp directory is created by the test runner (different UID),
	// so we need to open permissions for the container's non-root user.
	if err := os.Chmod(workDir, 0777); err != nil {
		t.Fatalf("Failed to chmod workspace: %v", err)
	}

	// Start container with non-root user (1000:1000)
	opts := &exec.Opts{
		WorkDir: workDir,
		User:    "1000:1000",
		Timeout: 30 * time.Second,
	}

	containerName, err := dockerExec.StartContainer(ctx, "test-workspace-write", opts)
	if err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}
	defer func() {
		_ = dockerExec.StopContainer(ctx, containerName)
	}()

	// Test: Write a file to /workspace
	t.Run("WriteFileToWorkspace", func(t *testing.T) {
		result, err := dockerExec.Run(ctx, []string{"sh", "-c", "echo 'hello from non-root' > /workspace/test.txt && cat /workspace/test.txt"}, opts)
		if err != nil {
			t.Fatalf("Failed to write to workspace: %v (stderr: %s)", err, result.Stderr)
		}

		output := strings.TrimSpace(result.Stdout)
		if output != "hello from non-root" {
			t.Errorf("Expected 'hello from non-root', got: %s", output)
		}
		t.Logf("Successfully wrote to /workspace: %s", output)
	})

	// Test: Create a directory in /workspace
	t.Run("CreateDirInWorkspace", func(t *testing.T) {
		result, err := dockerExec.Run(ctx, []string{"sh", "-c", "mkdir -p /workspace/subdir && touch /workspace/subdir/file.txt && ls /workspace/subdir/"}, opts)
		if err != nil {
			t.Fatalf("Failed to create dir in workspace: %v (stderr: %s)", err, result.Stderr)
		}

		if !strings.Contains(result.Stdout, "file.txt") {
			t.Errorf("Expected file.txt in output, got: %s", result.Stdout)
		}
		t.Logf("Successfully created directory in /workspace")
	})

	// Test: Verify files exist on host
	t.Run("VerifyFilesOnHost", func(t *testing.T) {
		// Check that files created in container are visible on host
		content, err := os.ReadFile(workDir + "/test.txt")
		if err != nil {
			t.Fatalf("Failed to read test.txt on host: %v", err)
		}
		if strings.TrimSpace(string(content)) != "hello from non-root" {
			t.Errorf("Unexpected content on host: %s", content)
		}
		t.Logf("Files correctly visible on host filesystem")
	})
}

// TestUserFlagPassedToDockerExec verifies that the User option is properly
// passed through to docker exec commands.
func TestUserFlagPassedToDockerExec(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Create a long-running docker executor
	dockerExec := exec.NewLongRunningDockerExec("alpine:latest", "test-nonroot")

	ctx := context.Background()
	workDir := t.TempDir()

	// Start container as ROOT (0:0) to test that exec overrides with user
	startOpts := &exec.Opts{
		WorkDir: workDir,
		User:    "0:0", // Start as root
		Timeout: 30 * time.Second,
	}

	containerName, err := dockerExec.StartContainer(ctx, "test-user-override", startOpts)
	if err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}
	defer func() {
		_ = dockerExec.StopContainer(ctx, containerName)
	}()

	// Now run command with User override to 1000:1000
	execOpts := &exec.Opts{
		WorkDir: workDir,
		User:    "1000:1000", // Override to non-root for exec
		Timeout: 30 * time.Second,
	}

	result, err := dockerExec.Run(ctx, []string{"id", "-u"}, execOpts)
	if err != nil {
		t.Fatalf("Failed to run id command: %v", err)
	}

	uid := strings.TrimSpace(result.Stdout)
	if uid != "1000" {
		t.Errorf("docker exec should use --user flag. Expected UID 1000, got: %s", uid)
	}
	t.Logf("docker exec correctly used --user flag, running as UID: %s", uid)
}

// TestContainerToolsWithLocalExecutor verifies that container tools work with local executor.
// These tools now run docker commands directly on the host, not inside containers.
func TestContainerToolsWithLocalExecutor(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()

	// Test 1: container_list tool works
	t.Run("ContainerListTool", func(t *testing.T) {
		listTool := tools.NewContainerListTool()

		result, err := listTool.Exec(ctx, map[string]any{
			"show_all": false,
		})
		if err != nil {
			t.Fatalf("container_list failed: %v", err)
		}

		// Should return valid JSON with success field
		if !strings.Contains(result.Content, "success") {
			t.Errorf("Expected success in output, got: %s", result.Content)
		}
		t.Logf("container_list output: %s", result.Content[:min(200, len(result.Content))])
	})

	// Test 2: container_build tool can be instantiated (actual build would need a Dockerfile)
	t.Run("ContainerBuildToolInstantiation", func(t *testing.T) {
		buildTool := tools.NewContainerBuildTool()

		def := buildTool.Definition()
		if def.Name != "container_build" {
			t.Errorf("Expected tool name 'container_build', got: %s", def.Name)
		}
		t.Logf("container_build tool instantiated successfully")
	})

	// Test 3: container_switch tool can be instantiated
	t.Run("ContainerSwitchToolInstantiation", func(t *testing.T) {
		switchTool := tools.NewContainerSwitchTool()

		def := switchTool.Definition()
		if def.Name != "container_switch" {
			t.Errorf("Expected tool name 'container_switch', got: %s", def.Name)
		}
		t.Logf("container_switch tool instantiated successfully")
	})
}
