//go:build integration

package tools

import (
	"context"
	"testing"
	"time"

	"orchestrator/pkg/exec"
)

// TestValidateContainerCapabilities_SafeContainer tests validation against the bootstrap container.
// This is an integration test because it requires Docker and the maestro-bootstrap image.
func TestValidateContainerCapabilities_SafeContainer(t *testing.T) {
	// Test against the safe bootstrap container which should always pass
	// Skip if the bootstrap image isn't available (e.g., in CI without pre-built images)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	executor := exec.NewLocalExec()

	// Check if maestro-bootstrap image exists locally
	checkResult, err := executor.Run(ctx, []string{"docker", "image", "inspect", "maestro-bootstrap"}, &exec.Opts{Timeout: 10 * time.Second})
	if err != nil || checkResult.ExitCode != 0 {
		t.Skip("maestro-bootstrap image not available locally - skipping test. Build with: docker build -t maestro-bootstrap -f pkg/dockerfiles/bootstrap.dockerfile .")
	}

	result := ValidateContainerCapabilities(ctx, executor, "maestro-bootstrap")

	// Safe container must pass all checks
	if !result.Success {
		t.Errorf("Safe container validation failed: %s", result.Message)
		for key, detail := range result.ErrorDetails {
			t.Errorf("  %s: %s", key, detail)
		}
	}

	// Verify git is available
	if !result.GitAvailable {
		t.Error("Safe container should have git available")
	}

	// Verify UID 1000 user exists
	if !result.UserUID1000 {
		t.Error("Safe container should have user with UID 1000")
	}

	// Verify /tmp is writable
	if !result.TmpWritable {
		t.Error("Safe container should have writable /tmp")
	}

	// Verify no missing tools
	if len(result.MissingTools) > 0 {
		t.Errorf("Safe container should have no missing tools, got: %v", result.MissingTools)
	}

	t.Logf("Validation passed: %s", result.Message)
}

func TestValidateContainerCapabilities_NonExistentContainer(t *testing.T) {
	// Test validation fails gracefully for non-existent container
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	executor := exec.NewLocalExec()
	result := ValidateContainerCapabilities(ctx, executor, "nonexistent-container-12345")

	// Should fail validation
	if result.Success {
		t.Error("Non-existent container should fail validation")
	}

	// Should report missing tools
	if len(result.MissingTools) == 0 {
		t.Error("Should report missing tools for non-existent container")
	}

	// Git should not be available
	if result.GitAvailable {
		t.Error("Git should not be available in non-existent container")
	}

	// UID 1000 should not exist
	if result.UserUID1000 {
		t.Error("UID 1000 should not exist in non-existent container")
	}

	// /tmp should not be writable (container doesn't exist)
	if result.TmpWritable {
		t.Error("/tmp should not be writable in non-existent container")
	}

	t.Logf("Validation failed as expected: %s", result.Message)
}
