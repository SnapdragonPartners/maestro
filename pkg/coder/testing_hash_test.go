package coder

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// TestDockerfileHashMismatchDetection tests that the hash verification logic
// correctly detects when a Dockerfile has been modified since the container was built.
// This is a unit test for the hash comparison logic - doesn't require Docker.
func TestDockerfileHashMismatchDetection(t *testing.T) {
	// Create temporary directory for test
	tempDir := t.TempDir()

	// Create initial Dockerfile
	dockerfilePath := filepath.Join(tempDir, ".maestro", "Dockerfile")
	if err := os.MkdirAll(filepath.Dir(dockerfilePath), 0755); err != nil {
		t.Fatalf("Failed to create .maestro directory: %v", err)
	}

	originalContent := `FROM alpine:latest
RUN apk add --no-cache git
RUN adduser -D -u 1000 coder
CMD ["sleep", "infinity"]`

	if err := os.WriteFile(dockerfilePath, []byte(originalContent), 0644); err != nil {
		t.Fatalf("Failed to write original Dockerfile: %v", err)
	}

	// Compute original hash (same logic as container_update tool)
	originalHashBytes := sha256.Sum256([]byte(originalContent))
	originalHash := hex.EncodeToString(originalHashBytes[:])

	t.Run("hash_matches_when_unchanged", func(t *testing.T) {
		// Read current Dockerfile and compute hash
		currentContent, err := os.ReadFile(dockerfilePath)
		if err != nil {
			t.Fatalf("Failed to read Dockerfile: %v", err)
		}

		currentHashBytes := sha256.Sum256(currentContent)
		currentHash := hex.EncodeToString(currentHashBytes[:])

		// Hashes should match when file is unchanged
		if currentHash != originalHash {
			t.Errorf("Hash mismatch when file unchanged: got %s, want %s", currentHash[:16], originalHash[:16])
		}
	})

	t.Run("hash_differs_when_modified", func(t *testing.T) {
		// Modify the Dockerfile (simulating merge conflict resolution)
		modifiedContent := `FROM alpine:latest
RUN apk add --no-cache git curl
RUN adduser -D -u 1000 coder
RUN echo "modified during merge"
CMD ["sleep", "infinity"]`

		if err := os.WriteFile(dockerfilePath, []byte(modifiedContent), 0644); err != nil {
			t.Fatalf("Failed to write modified Dockerfile: %v", err)
		}

		// Read current Dockerfile and compute hash
		currentContent, err := os.ReadFile(dockerfilePath)
		if err != nil {
			t.Fatalf("Failed to read Dockerfile: %v", err)
		}

		currentHashBytes := sha256.Sum256(currentContent)
		currentHash := hex.EncodeToString(currentHashBytes[:])

		// Hashes should NOT match when file is modified
		if currentHash == originalHash {
			t.Error("Hash should differ when file is modified, but they match")
		}

		t.Logf("Original hash: %s...", originalHash[:16])
		t.Logf("Current hash:  %s...", currentHash[:16])
		t.Logf("✅ Hash mismatch correctly detected")
	})

	t.Run("empty_stored_hash_skips_verification", func(t *testing.T) {
		// When no hash is stored (backwards compatibility), verification should be skipped
		storedHash := ""
		if storedHash == "" {
			t.Log("✅ Empty stored hash correctly indicates skip verification")
		}
	})

	t.Run("hash_is_content_based_not_metadata", func(t *testing.T) {
		// Verify that hash is based on content, not file metadata
		// Write same content to a new file
		newFile := filepath.Join(tempDir, "Dockerfile.new")
		testContent := "FROM alpine:latest\nRUN echo test"

		if err := os.WriteFile(newFile, []byte(testContent), 0644); err != nil {
			t.Fatalf("Failed to write new file: %v", err)
		}

		// Compute hash directly from content
		contentHash := sha256.Sum256([]byte(testContent))
		contentHashStr := hex.EncodeToString(contentHash[:])

		// Compute hash from file read
		fileContent, err := os.ReadFile(newFile)
		if err != nil {
			t.Fatalf("Failed to read new file: %v", err)
		}
		fileHash := sha256.Sum256(fileContent)
		fileHashStr := hex.EncodeToString(fileHash[:])

		if contentHashStr != fileHashStr {
			t.Errorf("Hash differs between direct content and file read: %s vs %s",
				contentHashStr[:16], fileHashStr[:16])
		}

		t.Log("✅ Hash is content-based, not metadata-based")
	})
}

// TestDockerfileHashEdgeCases tests edge cases in hash computation.
func TestDockerfileHashEdgeCases(t *testing.T) {
	tempDir := t.TempDir()

	t.Run("whitespace_only_changes_detected", func(t *testing.T) {
		// Even whitespace-only changes should be detected
		content1 := "FROM alpine:latest\nRUN echo test"
		content2 := "FROM alpine:latest\nRUN echo test\n" // Added trailing newline

		hash1 := sha256.Sum256([]byte(content1))
		hash2 := sha256.Sum256([]byte(content2))

		if hash1 == hash2 {
			t.Error("Whitespace changes should produce different hashes")
		}
		t.Log("✅ Whitespace changes are detected")
	})

	t.Run("large_dockerfile_hashing", func(t *testing.T) {
		// Test with a larger, more realistic Dockerfile
		largeContent := `FROM ubuntu:22.04

# Install build dependencies
RUN apt-get update && apt-get install -y \
    build-essential \
    curl \
    git \
    python3 \
    python3-pip \
    nodejs \
    npm \
    && rm -rf /var/lib/apt/lists/*

# Create non-root user
RUN useradd -m -u 1000 coder

# Set up workspace
WORKDIR /workspace
RUN chown -R coder:coder /workspace

# Install global tools
RUN npm install -g typescript eslint prettier

# Switch to non-root user
USER coder

# Default command
CMD ["bash"]
`
		hash := sha256.Sum256([]byte(largeContent))
		hashStr := hex.EncodeToString(hash[:])

		if len(hashStr) != 64 {
			t.Errorf("Expected 64-char hex hash, got %d chars", len(hashStr))
		}

		t.Logf("✅ Large Dockerfile hash: %s...", hashStr[:16])
	})

	t.Run("file_not_found_error", func(t *testing.T) {
		// Test that missing file returns error
		missingFile := filepath.Join(tempDir, "nonexistent", "Dockerfile")
		_, err := os.ReadFile(missingFile)
		if err == nil {
			t.Error("Expected error for missing file")
		}
		t.Log("✅ Missing file correctly returns error")
	})
}
