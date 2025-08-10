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
				"container_name":   tc.containerName,
				"dockerfile_path":  tc.dockerfilePath,
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