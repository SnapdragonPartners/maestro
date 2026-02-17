package utils

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestCreateTempRepoClone tests temp directory creation (without actual git operations).
func TestCreateTempRepoCloneStructure(t *testing.T) {
	// This is a minimal structural test - we can't test actual git clone without a repo
	// But we can verify the function signature and error handling

	ctx := context.Background()

	// Test with invalid repo URL (should fail fast)
	_, _, err := CreateTempRepoClone(ctx, "", "")
	if err == nil {
		t.Error("expected error for empty repo URL")
	}
}

// TestBuildContainerFromDockerfileStructure tests build function structure.
func TestBuildContainerFromDockerfileStructure(t *testing.T) {
	// This is a minimal structural test - actual docker build requires docker daemon
	ctx := context.Background()

	// Test with invalid inputs (should fail)
	err := BuildContainerFromDockerfile(ctx, "", "", "")
	if err == nil {
		t.Error("expected error for empty inputs")
	}
}

// TestIsImageHealthyStructure tests health check structure.
func TestIsImageHealthyStructure(t *testing.T) {
	// This is a minimal structural test - actual health check requires docker daemon
	ctx := context.Background()

	// Test with invalid image ID (should fail)
	err := IsImageHealthy(ctx, "nonexistent-image-id-12345")
	if err == nil {
		t.Error("expected error for nonexistent image")
	}

	// Error should mention the image doesn't exist
	if err != nil && !strings.Contains(err.Error(), "nonexistent-image-id-12345") {
		t.Errorf("expected error to mention image ID, got: %v", err)
	}
}

// TestGetImageIDStructure tests image ID retrieval structure.
func TestGetImageIDStructure(t *testing.T) {
	// This is a minimal structural test - actual image lookup requires docker daemon
	ctx := context.Background()

	// Test with invalid image name (should fail)
	_, err := GetImageID(ctx, "nonexistent-image-name-12345")
	if err == nil {
		t.Error("expected error for nonexistent image")
	}

	// Error should mention failure
	if err != nil && !strings.Contains(err.Error(), "failed to get image ID") {
		t.Errorf("expected error to mention failure, got: %v", err)
	}
}

// TestContextCancellation tests that functions respect context cancellation.
func TestContextCancellation(t *testing.T) {
	// Create already-canceled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// CreateTempRepoClone should respect cancellation
	_, _, err := CreateTempRepoClone(ctx, "https://example.com/repo.git", "")
	if err == nil {
		t.Error("expected error when context is canceled")
	}
}

// TestGenerateRuntimeBootstrapDockerfile verifies the runtime Dockerfile is correctly
// derived from the embedded bootstrap Dockerfile.
func TestGenerateRuntimeBootstrapDockerfile(t *testing.T) {
	dockerfile := GenerateRuntimeBootstrapDockerfile()

	// Must start with a FROM instruction (stage 2, not the builder stage)
	if !strings.HasPrefix(strings.TrimSpace(dockerfile), "FROM alpine:") {
		t.Errorf("expected runtime Dockerfile to start with 'FROM alpine:', got: %s",
			strings.SplitN(dockerfile, "\n", 2)[0])
	}

	// Must NOT contain multi-stage builder references
	if strings.Contains(dockerfile, "COPY --from=builder") {
		t.Error("runtime Dockerfile should not contain COPY --from=builder")
	}
	if strings.Contains(dockerfile, "golang:") {
		t.Error("runtime Dockerfile should not reference golang builder image")
	}

	// Must contain the proxy COPY from build context
	if !strings.Contains(dockerfile, "COPY maestro-mcp-proxy /usr/local/bin/maestro-mcp-proxy") {
		t.Error("runtime Dockerfile should COPY maestro-mcp-proxy from build context")
	}

	// Must contain Claude Code installation
	if !strings.Contains(dockerfile, "claude-code") {
		t.Error("runtime Dockerfile should install Claude Code")
	}

	// Must contain essential packages
	for _, pkg := range []string{"docker-cli", "git", "nodejs", "npm"} {
		if !strings.Contains(dockerfile, pkg) {
			t.Errorf("runtime Dockerfile should install %s", pkg)
		}
	}

	// Must create the coder user
	if !strings.Contains(dockerfile, "adduser") {
		t.Error("runtime Dockerfile should create coder user")
	}
}

// TestContextTimeout tests that functions respect context timeout.
func TestContextTimeout(t *testing.T) {
	// Create context with very short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	// Wait for timeout
	time.Sleep(10 * time.Millisecond)

	// Operations should fail due to timeout
	_, _, err := CreateTempRepoClone(ctx, "https://example.com/repo.git", "")
	if err == nil {
		t.Error("expected error when context times out")
	}
}
