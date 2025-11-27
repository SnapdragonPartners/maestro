// Package integration provides container-based testing infrastructure for MCP tools.
package integration

import (
	"context"
	"encoding/json"
	"fmt"
	osexec "os/exec"
	"testing"
	"time"

	"orchestrator/pkg/config"
	dockerexec "orchestrator/pkg/exec"
	"orchestrator/pkg/logx"
)

// ContainerTestFramework provides infrastructure for testing MCP tools inside containers.
//
//nolint:govet // fieldalignment: prefer logical grouping over memory optimization
type ContainerTestFramework struct {
	workspaceDir  string // Host directory mounted as /workspace
	executor      *dockerexec.LongRunningDockerExec
	containerName string
	logger        *logx.Logger
	t             *testing.T
}

// HarnessResult represents the result from the MCP tool harness.
//
//nolint:govet // fieldalignment: prefer logical grouping over memory optimization
type HarnessResult struct {
	Success   bool   `json:"success"`
	ToolName  string `json:"tool_name"`
	Duration  string `json:"duration"`
	Result    any    `json:"result,omitempty"`
	Error     string `json:"error,omitempty"`
	Arguments any    `json:"arguments"`
}

// NewContainerTestFramework creates a new container-based testing framework.
func NewContainerTestFramework(t *testing.T, workspaceDir string) (*ContainerTestFramework, error) {
	logger := logx.NewLogger("test")

	framework := &ContainerTestFramework{
		workspaceDir: workspaceDir,
		logger:       logger,
		t:            t,
	}

	return framework, nil
}

// GetExecutor returns the executor for creating tools with the same execution context as production.
func (f *ContainerTestFramework) GetExecutor() *dockerexec.LongRunningDockerExec {
	return f.executor
}

// GetProjectDir returns the workspace directory path.
func (f *ContainerTestFramework) GetProjectDir() string {
	return f.workspaceDir
}

// StartContainer starts the maestro-bootstrap container with proper mounts.
func (f *ContainerTestFramework) StartContainer(ctx context.Context) error {
	// Create executor
	f.executor = dockerexec.NewLongRunningDockerExec(config.BootstrapContainerTag, "test-tool-harness")

	// Configure container options
	opts := &dockerexec.Opts{
		WorkDir: f.workspaceDir,
		User:    "0:0", // Run as root for Docker access
		Env: []string{
			"DOCKER_HOST=unix:///var/run/docker.sock",
		},
	}

	// Start container
	containerName, err := f.executor.StartContainer(ctx, "test-harness", opts)
	if err != nil {
		return fmt.Errorf("failed to start container: %w", err)
	}

	f.containerName = containerName
	f.t.Logf("Started test container: %s", containerName)

	// Wait for container to be ready
	time.Sleep(2 * time.Second)

	// Verify Docker is available inside container
	result, err := f.executor.Run(ctx, []string{"docker", "version"}, &dockerexec.Opts{})
	if err != nil || result.ExitCode != 0 {
		return fmt.Errorf("docker not available in container: %w (stdout: %s, stderr: %s)",
			err, result.Stdout, result.Stderr)
	}

	f.t.Logf("Container framework ready")
	return nil
}

// ExecuteTool executes an MCP tool inside the container using the harness.
func (f *ContainerTestFramework) ExecuteTool(ctx context.Context, toolName string, args map[string]any) (*HarnessResult, error) {
	// Marshal arguments to JSON
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal args: %w", err)
	}

	// Execute harness inside container
	cmd := []string{"/usr/local/bin/harness", toolName, string(argsJSON)}
	result, _ := f.executor.Run(ctx, cmd, &dockerexec.Opts{
		WorkDir: "/workspace",
	})

	// Parse harness output
	var harnessResult HarnessResult
	if err := json.Unmarshal([]byte(result.Stdout), &harnessResult); err != nil {
		// If JSON parsing fails, create error result
		return &HarnessResult{
			Success: false,
			Error: fmt.Sprintf("harness execution failed: exit_code=%d, stdout=%s, stderr=%s, json_error=%v",
				result.ExitCode, result.Stdout, result.Stderr, err),
			ToolName:  toolName,
			Arguments: args,
		}, nil
	}

	return &harnessResult, nil
}

// ExecuteToolExpectSuccess executes a tool and expects it to succeed.
func (f *ContainerTestFramework) ExecuteToolExpectSuccess(ctx context.Context, toolName string, args map[string]any) any {
	result, err := f.ExecuteTool(ctx, toolName, args)
	if err != nil {
		f.t.Fatalf("Tool execution failed: %v", err)
	}

	if !result.Success {
		f.t.Fatalf("Tool %s failed: %s", toolName, result.Error)
	}

	return result.Result
}

// ExecuteToolExpectFailure executes a tool and expects it to fail.
func (f *ContainerTestFramework) ExecuteToolExpectFailure(ctx context.Context, toolName string, args map[string]any) string {
	result, err := f.ExecuteTool(ctx, toolName, args)
	if err != nil {
		f.t.Fatalf("Tool execution failed: %v", err)
	}

	if result.Success {
		f.t.Fatalf("Tool %s was expected to fail but succeeded: %+v", toolName, result.Result)
	}

	return result.Error
}

// Cleanup cleans up the container.
func (f *ContainerTestFramework) Cleanup(ctx context.Context) {
	if f.containerName != "" {
		_ = f.executor.StopContainer(ctx, f.containerName)
	}
}

// cleanupBuiltContainer removes a Docker container image by name.
//
//nolint:unused // Used by integration tests with build tags
func cleanupBuiltContainer(containerName string) {
	cmd := osexec.Command("docker", "rmi", "-f", containerName)
	_ = cmd.Run() // Ignore errors - container might not exist
}

// isContainerBuilt checks if a Docker container image exists.
//
//nolint:unused // Used by integration tests with build tags
func isContainerBuilt(containerName string) bool {
	cmd := osexec.Command("docker", "image", "inspect", containerName)
	return cmd.Run() == nil
}
