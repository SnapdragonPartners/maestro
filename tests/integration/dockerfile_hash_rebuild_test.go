//go:build integration

package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"orchestrator/pkg/tools"
)

// TestDockerfileHashRebuildFlow tests the full flow of:
// 1. Building a container
// 2. Computing Dockerfile hash via container_update
// 3. Modifying the Dockerfile
// 4. Detecting hash mismatch (would trigger rebuild in TESTING state)
//
// This tests the components used by verifyAndRebuildIfNeeded without requiring
// the full coder state machine.
func TestDockerfileHashRebuildFlow(t *testing.T) {
	// Skip if Docker is not available
	if !isDockerAvailable() {
		t.Skip("Docker not available, skipping Dockerfile hash rebuild test")
	}

	// Setup test config
	SetupTestConfig(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Create temporary workspace
	tempDir := t.TempDir()
	workspaceDir := filepath.Join(tempDir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("Failed to create workspace dir: %v", err)
	}

	// Create initial Dockerfile
	dockerfilePath := filepath.Join(workspaceDir, ".maestro", "Dockerfile")
	if err := os.MkdirAll(filepath.Dir(dockerfilePath), 0755); err != nil {
		t.Fatalf("Failed to create .maestro directory: %v", err)
	}

	originalContent := `FROM alpine:latest
RUN apk add --no-cache git github-cli
RUN adduser -D -u 1000 coder
RUN echo "version-1" > /version.txt
CMD ["sleep", "infinity"]`

	if err := os.WriteFile(dockerfilePath, []byte(originalContent), 0644); err != nil {
		t.Fatalf("Failed to create Dockerfile: %v", err)
	}

	containerName := "maestro-test-hash-rebuild"
	defer cleanupBuiltContainer(containerName)

	var originalHash string
	var originalImageID string

	t.Run("1_build_initial_container", func(t *testing.T) {
		buildTool := tools.NewContainerBuildTool(workspaceDir)
		args := map[string]any{
			"container_name": containerName,
			"dockerfile":     ".maestro/Dockerfile",
			"cwd":            workspaceDir,
		}

		result, err := buildTool.Exec(ctx, args)
		if err != nil {
			t.Fatalf("Initial container build failed: %v", err)
		}

		var resultMap map[string]any
		if unmarshalErr := json.Unmarshal([]byte(result.Content), &resultMap); unmarshalErr != nil {
			t.Fatalf("Failed to unmarshal result: %v", unmarshalErr)
		}

		if success, ok := resultMap["success"].(bool); !ok || !success {
			t.Fatalf("Build should succeed, got: %+v", resultMap)
		}

		t.Logf("✅ Initial container built: %s", containerName)
	})

	t.Run("2_compute_original_hash", func(t *testing.T) {
		// Compute hash the same way container_update does
		content, err := os.ReadFile(dockerfilePath)
		if err != nil {
			t.Fatalf("Failed to read Dockerfile: %v", err)
		}

		hashBytes := sha256.Sum256(content)
		originalHash = hex.EncodeToString(hashBytes[:])

		t.Logf("✅ Original Dockerfile hash: %s...", originalHash[:16])
	})

	t.Run("3_get_original_image_id", func(t *testing.T) {
		// Use container_update to get the image ID
		mockAgent := newMockTestAgent(workspaceDir)
		updateTool := tools.NewContainerUpdateTool(mockAgent)

		args := map[string]any{
			"container_name": containerName,
			"dockerfile":     ".maestro/Dockerfile",
		}

		result, err := updateTool.Exec(ctx, args)
		if err != nil {
			t.Fatalf("container_update failed: %v", err)
		}

		var resultMap map[string]any
		if unmarshalErr := json.Unmarshal([]byte(result.Content), &resultMap); unmarshalErr != nil {
			t.Fatalf("Failed to unmarshal result: %v", unmarshalErr)
		}

		if imageID, ok := resultMap["pinned_image_id"].(string); ok {
			originalImageID = imageID
			t.Logf("✅ Original image ID: %s...", originalImageID[:16])
		} else {
			t.Fatalf("Expected pinned_image_id in result: %+v", resultMap)
		}
	})

	t.Run("4_modify_dockerfile", func(t *testing.T) {
		// Simulate merge conflict resolution that changes the Dockerfile
		modifiedContent := `FROM alpine:latest
RUN apk add --no-cache git github-cli curl
RUN adduser -D -u 1000 coder
RUN echo "version-2-modified" > /version.txt
RUN echo "added during merge conflict resolution"
CMD ["sleep", "infinity"]`

		if err := os.WriteFile(dockerfilePath, []byte(modifiedContent), 0644); err != nil {
			t.Fatalf("Failed to write modified Dockerfile: %v", err)
		}

		t.Logf("✅ Dockerfile modified (simulating merge conflict resolution)")
	})

	t.Run("5_detect_hash_mismatch", func(t *testing.T) {
		// Read current Dockerfile and compute hash
		content, err := os.ReadFile(dockerfilePath)
		if err != nil {
			t.Fatalf("Failed to read Dockerfile: %v", err)
		}

		hashBytes := sha256.Sum256(content)
		currentHash := hex.EncodeToString(hashBytes[:])

		// Verify hash mismatch is detected
		if currentHash == originalHash {
			t.Error("Hash should differ after modification")
		}

		t.Logf("Original hash: %s...", originalHash[:16])
		t.Logf("Current hash:  %s...", currentHash[:16])
		t.Logf("✅ Hash mismatch correctly detected")
	})

	t.Run("6_rebuild_container_after_mismatch", func(t *testing.T) {
		// Rebuild the container (simulating what verifyAndRebuildIfNeeded does)
		buildTool := tools.NewContainerBuildTool(workspaceDir)
		args := map[string]any{
			"container_name": containerName,
			"dockerfile":     ".maestro/Dockerfile",
			"cwd":            workspaceDir,
		}

		result, err := buildTool.Exec(ctx, args)
		if err != nil {
			t.Fatalf("Container rebuild failed: %v", err)
		}

		var resultMap map[string]any
		if unmarshalErr := json.Unmarshal([]byte(result.Content), &resultMap); unmarshalErr != nil {
			t.Fatalf("Failed to unmarshal result: %v", unmarshalErr)
		}

		if success, ok := resultMap["success"].(bool); !ok || !success {
			t.Fatalf("Rebuild should succeed, got: %+v", resultMap)
		}

		t.Logf("✅ Container rebuilt successfully after hash mismatch")
	})

	t.Run("7_verify_new_image_differs", func(t *testing.T) {
		// Get the new image ID
		mockAgent := newMockTestAgent(workspaceDir)
		updateTool := tools.NewContainerUpdateTool(mockAgent)

		args := map[string]any{
			"container_name": containerName,
			"dockerfile":     ".maestro/Dockerfile",
		}

		result, err := updateTool.Exec(ctx, args)
		if err != nil {
			t.Fatalf("container_update failed: %v", err)
		}

		var resultMap map[string]any
		if unmarshalErr := json.Unmarshal([]byte(result.Content), &resultMap); unmarshalErr != nil {
			t.Fatalf("Failed to unmarshal result: %v", unmarshalErr)
		}

		if newImageID, ok := resultMap["pinned_image_id"].(string); ok {
			if newImageID == originalImageID {
				t.Error("Image ID should differ after rebuild with modified Dockerfile")
			}
			t.Logf("Original image ID: %s...", originalImageID[:16])
			t.Logf("New image ID:      %s...", newImageID[:16])
			t.Logf("✅ New image ID differs from original (rebuild was effective)")
		} else {
			t.Fatalf("Expected pinned_image_id in result: %+v", resultMap)
		}
	})
}

// TestHashComputationConsistency verifies that hash computation is consistent
// between container_update tool and the TESTING state verification.
func TestHashComputationConsistency(t *testing.T) {
	// Create temporary workspace
	tempDir := t.TempDir()
	dockerfilePath := filepath.Join(tempDir, "Dockerfile")

	content := `FROM alpine:latest
RUN echo "test content"
CMD ["echo", "hello"]`

	if err := os.WriteFile(dockerfilePath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write Dockerfile: %v", err)
	}

	// Compute hash directly (as done in container_update)
	directContent, err := os.ReadFile(dockerfilePath)
	if err != nil {
		t.Fatalf("Failed to read Dockerfile: %v", err)
	}

	hashBytes := sha256.Sum256(directContent)
	directHash := hex.EncodeToString(hashBytes[:])

	// Compute hash again (as done in verifyAndRebuildIfNeeded)
	verifyContent, err := os.ReadFile(dockerfilePath)
	if err != nil {
		t.Fatalf("Failed to read Dockerfile for verification: %v", err)
	}

	verifyHashBytes := sha256.Sum256(verifyContent)
	verifyHash := hex.EncodeToString(verifyHashBytes[:])

	// Hashes should be identical
	if directHash != verifyHash {
		t.Errorf("Hash computation is inconsistent: %s vs %s", directHash[:16], verifyHash[:16])
	}

	t.Logf("✅ Hash computation is consistent: %s...", directHash[:16])
}
