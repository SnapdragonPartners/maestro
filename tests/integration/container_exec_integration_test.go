//go:build integration
// +build integration

package integration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orchestrator/pkg/tools"
)

// TestContainerExecIntegration tests the container_test tool in command execution mode using maestro-bootstrap.
func TestContainerExecIntegration(t *testing.T) {
	// Skip if Docker is not available
	if !isDockerAvailable() {
		// Setup test config for container validation (uses public repo)
		SetupTestConfig(t)
		t.Skip("Docker not available, skipping container test (command execution) integration test")
	}

	// Setup test environment
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Create temporary workspace
	tempDir := t.TempDir()
	workspaceDir := filepath.Join(tempDir, "exec_test")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("Failed to create workspace dir: %v", err)
	}

	// Use maestro-bootstrap container which has all required tools
	framework, err := NewContainerTestFramework(t, workspaceDir)
	if err != nil {
		t.Fatalf("Failed to create test framework: %v", err)
	}
	defer framework.Cleanup(ctx)

	if err := framework.StartContainer(ctx); err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}

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
			name:          "list_directory",
			command:       "ls -la /workspace",
			expectSuccess: true,
			expectOutput:  "drwx", // Check for directory listing format
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
			command:       "sleep 1 && echo 'completed'",
			expectSuccess: true,
			expectOutput:  "completed",
			timeout:       10, // Allow enough time for sleep
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Prepare tool arguments
			args := map[string]any{
				"container_name": "maestro-bootstrap:latest",
				"command":        tc.command,
			}

			// Add custom timeout if specified
			if tc.timeout > 0 {
				args["timeout_seconds"] = tc.timeout
			}

			// Create container test tool with mock agent context
			mockAgent := newMockTestAgent(framework.GetProjectDir())
			tool := tools.NewContainerTestTool(framework.GetExecutor(), mockAgent, framework.GetProjectDir())

			// Execute the tool
			result, err := tool.Exec(ctx, args)

			if tc.expectSuccess {
				if err != nil {
					t.Fatalf("Tool execution failed unexpectedly: %v", err)
				}

				// Verify result structure
				var resultMap map[string]any
				if unmarshalErr := json.Unmarshal([]byte(result.Content), &resultMap); unmarshalErr != nil {
					t.Fatalf("Failed to unmarshal result: %v", unmarshalErr)
				}

				// Check success field
				success, ok := resultMap["success"].(bool)
				if !ok || !success {
					t.Fatalf("Expected success=true, got success=%v, result=%+v", resultMap["success"], resultMap)
				}

				// Check exit code
				exitCode, ok := resultMap["exit_code"].(int)
				if !ok || exitCode != 0 {
					// exit_code might be float64 from JSON
					if exitCodeFloat, ok := resultMap["exit_code"].(float64); ok {
						exitCode = int(exitCodeFloat)
					}
					if exitCode != 0 {
						t.Fatalf("Expected exit_code=0, got exit_code=%v", resultMap["exit_code"])
					}
				}

				// Check expected output if specified
				if tc.expectOutput != "" {
					// Check if we have stdout or output field
					var output string
					if stdout, ok := resultMap["stdout"].(string); ok {
						output = stdout
					} else if out, ok := resultMap["output"].(string); ok {
						output = out
					} else {
						t.Fatalf("Expected stdout or output field, got fields: %v", resultMap)
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
					var resultMap map[string]any
					if unmarshalErr := json.Unmarshal([]byte(result.Content), &resultMap); unmarshalErr != nil {
						t.Fatalf("Failed to unmarshal result: %v", unmarshalErr)
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
						// Try float64
						if exitCodeFloat, ok := resultMap["exit_code"].(float64); ok {
							exitCode = int(exitCodeFloat)
						} else {
							t.Fatalf("Expected exit_code field in result")
						}
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
					} else {
						var resultMap map[string]any
						if unmarshalErr := json.Unmarshal([]byte(result.Content), &resultMap); unmarshalErr == nil {
							if stderr, ok := resultMap["stderr"].(string); ok {
								if !strings.Contains(stderr, tc.expectOutput) {
									t.Logf("Warning: Expected stderr to contain '%s', got: %s", tc.expectOutput, stderr)
								}
							}
						}
					}
				}
			}
		})
	}
}
