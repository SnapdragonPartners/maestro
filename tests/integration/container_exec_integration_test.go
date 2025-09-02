//go:build integration
// +build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orchestrator/pkg/proto"
	"orchestrator/pkg/tools"
)

// mockTestAgent implements tools.Agent interface for testing.
type mockTestAgent struct{}

func (m *mockTestAgent) GetCurrentState() proto.State {
	return proto.State("PLANNING") // Default to read-only state
}

func (m *mockTestAgent) GetHostWorkspacePath() string {
	return "/tmp/test-workspace"
}

// TestContainerExecIntegration tests the container_test tool in command execution mode using the container test framework.
// This test runs the real MCP tool inside a container environment to match production behavior.
func TestContainerExecIntegration(t *testing.T) {
	// Skip if Docker is not available
	if !isDockerAvailable() {
		t.Skip("Docker not available, skipping container test (command execution) integration test")
	}

	// Setup test environment
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Create temporary workspace
	tempDir := t.TempDir()

	// Create a test Dockerfile with some utilities for testing commands
	dockerfileContent := `FROM alpine:latest
RUN apk add --no-cache curl wget bash
RUN echo "test file content" > /test-file.txt
CMD ["echo", "Hello from test container"]
`

	// Build a test container first
	testContainerName := "maestro-exec-test-container"
	workspaceDir := filepath.Join(tempDir, "exec_test")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("Failed to create workspace dir: %v", err)
	}

	// Create Dockerfile
	if err := os.WriteFile(filepath.Join(workspaceDir, "Dockerfile"), []byte(dockerfileContent), 0644); err != nil {
		t.Fatalf("Failed to create Dockerfile: %v", err)
	}

	// Build container for testing
	framework, err := NewContainerTestFramework(t, workspaceDir)
	if err != nil {
		t.Fatalf("Failed to create test framework: %v", err)
	}
	defer framework.Cleanup(ctx)

	if err := framework.StartContainer(ctx); err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}

	// Build test container
	buildTool := tools.NewContainerBuildTool(framework.GetExecutor())
	buildArgs := map[string]interface{}{
		"container_name":  testContainerName,
		"dockerfile_path": "Dockerfile",
		"cwd":             "/workspace",
	}

	_, err = buildTool.Exec(ctx, buildArgs)
	if err != nil {
		t.Fatalf("Failed to build test container: %v", err)
	}

	// Ensure test container is cleaned up
	defer cleanupBuiltContainer(testContainerName)

	// Test different command scenarios
	testCases := []struct {
		name          string
		command       string
		expectSuccess bool
		expectOutput  string // substring that should be in output
		timeout       int    // optional timeout override
	}{
		{
			name:          "simple_echo_command",
			command:       "echo 'Hello World'",
			expectSuccess: true,
			expectOutput:  "Hello World",
		},
		{
			name:          "file_read_command",
			command:       "cat /test-file.txt",
			expectSuccess: true,
			expectOutput:  "test file content",
		},
		{
			name:          "list_directory",
			command:       "ls -la /",
			expectSuccess: true,
			expectOutput:  "bin",
		},
		{
			name:          "command_with_exit_code_0",
			command:       "true",
			expectSuccess: true,
			expectOutput:  "", // no output expected
		},
		{
			name:          "command_with_exit_code_1",
			command:       "false",
			expectSuccess: false,
			expectOutput:  "", // no output expected
		},
		{
			name:          "nonexistent_command",
			command:       "nonexistent-command-xyz",
			expectSuccess: false,
			expectOutput:  "not found",
		},
		{
			name:          "command_with_custom_timeout",
			command:       "sleep 2 && echo 'completed'",
			expectSuccess: true,
			expectOutput:  "completed",
			timeout:       10, // Allow enough time for sleep
		},
		{
			name:          "command_that_times_out",
			command:       "sleep 10", // This will timeout
			expectSuccess: false,
			expectOutput:  "",
			timeout:       2, // Short timeout to trigger timeout
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Prepare tool arguments
			args := map[string]interface{}{
				"container_name": testContainerName,
				"command":        tc.command,
			}

			// Add custom timeout if specified
			if tc.timeout > 0 {
				args["timeout_seconds"] = tc.timeout
			}

			// Create container test tool with mock agent context
			mockAgent := &mockTestAgent{}
			tool := tools.NewContainerTestTool(framework.GetExecutor(), mockAgent, framework.GetProjectDir())

			// Execute the tool
			result, err := tool.Exec(ctx, args)

			if tc.expectSuccess {
				if err != nil {
					t.Fatalf("Tool execution failed unexpectedly: %v", err)
				}

				// Verify result structure
				resultMap, ok := result.(map[string]interface{})
				if !ok {
					t.Fatalf("Expected result to be map[string]interface{}, got %T", result)
				}

				// Check success field
				success, ok := resultMap["success"].(bool)
				if !ok || !success {
					t.Fatalf("Expected success=true, got success=%v, result=%+v", resultMap["success"], resultMap)
				}

				// Check exit code
				exitCode, ok := resultMap["exit_code"].(int)
				if !ok || exitCode != 0 {
					t.Fatalf("Expected exit_code=0, got exit_code=%v", resultMap["exit_code"])
				}

				// Check expected output if specified
				if tc.expectOutput != "" {
					output, ok := resultMap["output"].(string)
					if !ok {
						t.Fatalf("Expected output to be string, got %T", resultMap["output"])
					}
					if !strings.Contains(output, tc.expectOutput) {
						t.Fatalf("Expected output to contain '%s', got: %s", tc.expectOutput, output)
					}
				}

				t.Logf("✅ Successfully executed command via container_test: %s", tc.command)

			} else {
				// Command should fail
				if err != nil {
					// Tool level error - this is fine for failure cases
					t.Logf("✅ Expected tool error occurred: %v", err)
				} else {
					// Check if tool returned failure in result
					resultMap, ok := result.(map[string]interface{})
					if !ok {
						t.Fatalf("Expected result to be map[string]interface{}, got %T", result)
					}

					success, ok := resultMap["success"].(bool)
					if !ok {
						t.Fatalf("Expected success field in result")
					}

					if success {
						t.Fatalf("Expected container test (command execution) to fail but it succeeded: %+v", resultMap)
					}

					// Check exit code is non-zero
					exitCode, ok := resultMap["exit_code"].(int)
					if !ok {
						t.Fatalf("Expected exit_code field in result")
					}
					if exitCode == 0 {
						t.Fatalf("Expected non-zero exit code for failed command, got %d", exitCode)
					}

					t.Logf("✅ Expected container_test command failure occurred with exit code: %d", exitCode)
				}

				// Check expected error output if specified
				if tc.expectOutput != "" {
					if err != nil {
						if !strings.Contains(err.Error(), tc.expectOutput) {
							t.Logf("Warning: Expected error to contain '%s', got: %v", tc.expectOutput, err)
						}
					} else if resultMap, ok := result.(map[string]interface{}); ok {
						if stderr, ok := resultMap["stderr"].(string); ok {
							if !strings.Contains(stderr, tc.expectOutput) {
								t.Logf("Warning: Expected stderr to contain '%s', got: %s", tc.expectOutput, stderr)
							}
						}
					}
				}
			}
		})
	}
}
