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

// TestContainerBuildIntegration tests the container_build tool using the container test framework.
func TestContainerBuildIntegration(t *testing.T) {
	// Skip if Docker is not available
	if !isDockerAvailable() {
		t.Skip("Docker not available, skipping container build integration test")
		// Setup test config for container validation (uses public repo)
		SetupTestConfig(t)
	}

	// Setup test environment
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Create temporary workspace
	tempDir := t.TempDir()

	// Test building a container from Dockerfile based on maestro-bootstrap
	testCases := []struct {
		name              string
		dockerfileContent string
		containerName     string
		dockerfilePath    string
	}{
		{
			name: "dockerfile_in_workspace_root",
			dockerfileContent: `FROM maestro-bootstrap:latest
RUN echo "test" > /test.txt
CMD ["sleep", "infinity"]`,
			containerName:  "maestro-test-root",
			dockerfilePath: "Dockerfile",
		},
		{
			name: "dockerfile_in_subdirectory",
			dockerfileContent: `FROM maestro-bootstrap:latest
RUN echo "subdir test" > /subdir.txt
CMD ["sleep", "infinity"]`,
			containerName:  "maestro-test-subdir",
			dockerfilePath: "docker/Dockerfile",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			workspaceDir := filepath.Join(tempDir, tc.name)
			if err := os.MkdirAll(workspaceDir, 0755); err != nil {
				t.Fatalf("Failed to create workspace dir: %v", err)
			}

			// Create Dockerfile (and subdirectory if needed)
			dockerfilePath := filepath.Join(workspaceDir, tc.dockerfilePath)
			if err := os.MkdirAll(filepath.Dir(dockerfilePath), 0755); err != nil {
				t.Fatalf("Failed to create dockerfile directory: %v", err)
			}
			if err := os.WriteFile(dockerfilePath, []byte(tc.dockerfileContent), 0644); err != nil {
				t.Fatalf("Failed to create Dockerfile: %v", err)
			}

			// Create container test framework
			framework, err := NewContainerTestFramework(t, workspaceDir)
			if err != nil {
				t.Fatalf("Failed to create test framework: %v", err)
			}
			defer framework.Cleanup(ctx)

			if err := framework.StartContainer(ctx); err != nil {
				t.Fatalf("Failed to start container: %v", err)
			}

			// Ensure target container is cleaned up
			defer cleanupBuiltContainer(tc.containerName)

			// Build the container
			buildTool := tools.NewContainerBuildTool(framework.GetExecutor())
			args := map[string]any{
				"container_name":  tc.containerName,
				"dockerfile_path": tc.dockerfilePath,
				"cwd":             "/workspace",
			}

			result, err := buildTool.Exec(ctx, args)
			if err != nil {
				t.Fatalf("Container build failed: %v", err)
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

			// Verify the container was built
			if !isContainerBuilt(tc.containerName) {
				t.Fatalf("Container %s was not built successfully", tc.containerName)
			}

			t.Logf("✅ Successfully built container: %s", tc.containerName)
		})
	}
}
