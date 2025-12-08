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

// TestContainerBuildIntegration tests the container_build tool.
// The tool uses local executor to run docker build on the host.
func TestContainerBuildIntegration(t *testing.T) {
	// Setup test config for container validation
	SetupTestConfig(t)

	// Skip if Docker is not available
	if !isDockerAvailable() {
		t.Skip("Docker not available, skipping container build integration test")
	}

	// Setup test environment
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Create temporary workspace
	tempDir := t.TempDir()

	// Test building a container from Dockerfile using a public base image
	// The Dockerfile must include git and gh for maestro validation to pass
	testCases := []struct {
		name              string
		dockerfileContent string
		containerName     string
		dockerfilePath    string
	}{
		{
			name: "dockerfile_in_workspace_root",
			dockerfileContent: `FROM alpine:latest
RUN apk add --no-cache git github-cli
RUN echo "test" > /test.txt
CMD ["sleep", "infinity"]`,
			containerName:  "maestro-test-root",
			dockerfilePath: "Dockerfile",
		},
		{
			name: "dockerfile_in_subdirectory",
			dockerfileContent: `FROM alpine:latest
RUN apk add --no-cache git github-cli
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

			// Ensure target container image is cleaned up
			defer cleanupBuiltContainer(tc.containerName)

			// Build the container (uses local executor internally, so we pass host path)
			buildTool := tools.NewContainerBuildTool(workspaceDir)
			args := map[string]any{
				"container_name":  tc.containerName,
				"dockerfile_path": tc.dockerfilePath,
				"cwd":             workspaceDir, // Host path - container_build runs on host
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
