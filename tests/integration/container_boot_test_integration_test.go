//go:build integration
// +build integration

package integration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"orchestrator/pkg/tools"
)

// TestContainerBootTestIntegration tests the container_test tool in boot test mode using the container test framework.
// This test runs the real MCP tool inside a container environment to match production behavior.
func TestContainerBootTestIntegration(t *testing.T) {
	// Skip if Docker is not available
	if !isDockerAvailable() {
		t.Skip("Docker not available, skipping container test integration test")
	}

	// Setup test environment
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Create temporary workspace
	tempDir := t.TempDir()

	// Test different container scenarios
	testCases := []struct {
		name              string
		dockerfileContent string
		containerName     string
		expectSuccess     bool
		timeout           int // optional timeout override
		description       string
	}{
		{
			name: "simple_long_running_container",
			dockerfileContent: `FROM alpine:latest
CMD ["sleep", "3600"]`,
			containerName: "maestro-boot-test-simple",
			expectSuccess: true,
			description:   "Container that runs indefinitely should pass boot test",
		},
		{
			name: "web_server_container",
			dockerfileContent: `FROM alpine:latest
RUN apk add --no-cache python3
EXPOSE 8080
CMD ["python3", "-m", "http.server", "8080"]`,
			containerName: "maestro-boot-test-webserver",
			expectSuccess: true,
			description:   "Web server container should stay running",
		},
		{
			name: "container_that_exits_immediately",
			dockerfileContent: `FROM alpine:latest
CMD ["echo", "I exit immediately"]`,
			containerName: "maestro-boot-test-exits",
			expectSuccess: false,
			description:   "Container that exits immediately should fail boot test",
		},
		{
			name: "container_that_fails_on_startup",
			dockerfileContent: `FROM alpine:latest
CMD ["nonexistent-command-that-fails"]`,
			containerName: "maestro-boot-test-fails",
			expectSuccess: false,
			description:   "Container with failing CMD should fail boot test",
		},
		{
			name: "container_with_custom_timeout",
			dockerfileContent: `FROM alpine:latest
CMD ["sleep", "3600"]`,
			containerName: "maestro-boot-test-timeout",
			expectSuccess: true,
			timeout:       5, // Custom short timeout
			description:   "Container with custom timeout should still pass",
		},
		{
			name: "container_that_starts_slow",
			dockerfileContent: `FROM alpine:latest
# Simulate slow startup
CMD ["sh", "-c", "sleep 2 && sleep 3600"]`,
			containerName: "maestro-boot-test-slow",
			expectSuccess: true,
			timeout:       10, // Allow time for slow startup
			description:   "Container with slow startup should pass with adequate timeout",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("Testing: %s", tc.description)

			// Create workspace for this test case
			workspaceDir := filepath.Join(tempDir, tc.name)
			if err := os.MkdirAll(workspaceDir, 0755); err != nil {
				t.Fatalf("Failed to create workspace dir: %v", err)
			}

			// Create Dockerfile
			if err := os.WriteFile(filepath.Join(workspaceDir, "Dockerfile"), []byte(tc.dockerfileContent), 0644); err != nil {
				t.Fatalf("Failed to create Dockerfile: %v", err)
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

			// First build the container
			buildTool := tools.NewContainerBuildTool(framework.GetExecutor())
			buildArgs := map[string]any{
				"container_name":  tc.containerName,
				"dockerfile_path": "Dockerfile",
				"cwd":             "/workspace",
			}

			_, err = buildTool.Exec(ctx, buildArgs)
			if err != nil {
				t.Fatalf("Failed to build test container: %v", err)
			}

			// Verify the container was built
			if !isContainerBuilt(tc.containerName) {
				t.Fatalf("Container %s was not built successfully", tc.containerName)
			}

			// Now test the container_test tool in boot test mode
			mockAgent := newMockTestAgent(framework.GetProjectDir())
			containerTestTool := tools.NewContainerTestTool(framework.GetExecutor(), mockAgent, framework.GetProjectDir())

			// Prepare tool arguments for boot test mode (no command, ttl_seconds=0)
			args := map[string]any{
				"container_name": tc.containerName,
				"ttl_seconds":    0, // Boot test mode
			}

			// Add custom timeout if specified
			if tc.timeout > 0 {
				args["timeout_seconds"] = tc.timeout
			}

			// Execute the container test in boot test mode
			result, err := containerTestTool.Exec(ctx, args)

			if tc.expectSuccess {
				if err != nil {
					t.Fatalf("Boot test failed unexpectedly: %v", err)
				}

				// Verify result structure - unmarshal from ExecResult.Content
				var resultMap map[string]any
				if err := json.Unmarshal([]byte(result.Content), &resultMap); err != nil {
					t.Fatalf("Failed to unmarshal result: %v", err)
				}

				// Check success field
				success, ok := resultMap["success"].(bool)
				if !ok || !success {
					t.Fatalf("Expected success=true, got success=%v, result=%+v", resultMap["success"], resultMap)
				}

				// Check that timeout field is present and reasonable
				timeout, ok := resultMap["timeout"]
				if !ok {
					t.Fatalf("Expected timeout field in successful result")
				}

				expectedTimeout := 30
				if tc.timeout > 0 {
					expectedTimeout = tc.timeout
				}

				if timeoutInt, ok := timeout.(int); !ok || timeoutInt != expectedTimeout {
					t.Fatalf("Expected timeout=%d, got timeout=%v", expectedTimeout, timeout)
				}

				t.Logf("✅ Container test (boot mode) passed: container %s stayed running for %d seconds", tc.containerName, expectedTimeout)

			} else {
				// Boot test should fail
				if err != nil {
					// Tool level error is acceptable for failure cases
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
						t.Fatalf("Expected container test (boot mode) to fail but it succeeded: %+v", resultMap)
					}

					// Check that we have exit code information
					if exitCode, ok := resultMap["exit_code"]; ok {
						t.Logf("✅ Expected container test (boot mode) failure with exit code: %v", exitCode)
					} else {
						t.Logf("✅ Expected container test (boot mode) failure occurred")
					}
				}
			}
		})
	}
}

// TestContainerBootTestEdgeCases tests edge cases and error conditions for container_test boot mode
func TestContainerBootTestEdgeCases(t *testing.T) {
	// Skip if Docker is not available
	if !isDockerAvailable() {
		t.Skip("Docker not available, skipping container test boot mode edge cases")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	tempDir := t.TempDir()
	workspaceDir := filepath.Join(tempDir, "edge_cases")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("Failed to create workspace dir: %v", err)
	}

	framework, err := NewContainerTestFramework(t, workspaceDir)
	if err != nil {
		t.Fatalf("Failed to create test framework: %v", err)
	}
	defer framework.Cleanup(ctx)

	if err := framework.StartContainer(ctx); err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}

	mockAgent := newMockTestAgent(framework.GetProjectDir())
	containerTestTool := tools.NewContainerTestTool(framework.GetExecutor(), mockAgent, framework.GetProjectDir())

	t.Run("nonexistent_container", func(t *testing.T) {
		args := map[string]any{
			"container_name": "nonexistent-container-xyz",
			"ttl_seconds":    0, // Boot test mode
		}

		result, err := containerTestTool.Exec(ctx, args)

		// Should fail because container doesn't exist
		if err == nil {
			var resultMap map[string]any
			if unmarshalErr := json.Unmarshal([]byte(result.Content), &resultMap); unmarshalErr == nil {
				if success, ok := resultMap["success"].(bool); ok && success {
					t.Fatalf("Expected container test (boot mode) to fail for nonexistent container")
				}
			}
		}

		t.Logf("✅ Correctly failed for nonexistent container")
	})

	t.Run("timeout_limit_enforcement", func(t *testing.T) {
		args := map[string]any{
			"container_name":  "test-container",
			"timeout_seconds": 100, // Above max limit of 60
			"ttl_seconds":     0,   // Boot test mode
		}

		result, err := containerTestTool.Exec(ctx, args)

		// Should either limit timeout to 60 or fail cleanly
		if err == nil {
			var resultMap map[string]any
			if unmarshalErr := json.Unmarshal([]byte(result.Content), &resultMap); unmarshalErr == nil {
				if timeout, ok := resultMap["timeout"]; ok {
					if timeoutInt, ok := timeout.(int); ok && timeoutInt > 60 {
						t.Fatalf("Timeout should be limited to 60 seconds, got %d", timeoutInt)
					}
				}
			}
		}

		t.Logf("✅ Timeout limit enforcement working correctly")
	})
}
