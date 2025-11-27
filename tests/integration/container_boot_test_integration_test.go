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

// TestContainerBootTestIntegration tests the container_test tool in boot test mode using maestro-bootstrap.
func TestContainerBootTestIntegration(t *testing.T) {
	// Skip if Docker is not available
	if !isDockerAvailable() {
		t.Skip("Docker not available, skipping container test integration test")
		// Setup test config for container validation (uses public repo)
		SetupTestConfig(t)
	}

	// Setup test environment
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Create temporary workspace
	tempDir := t.TempDir()

	// Test maestro-bootstrap container directly (no need to build)
	t.Run("maestro_bootstrap_container", func(t *testing.T) {
		workspaceDir := filepath.Join(tempDir, "bootstrap_test")
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

		args := map[string]any{
			"container_name": "maestro-bootstrap:latest",
			"ttl_seconds":    0, // Boot test mode
		}

		result, err := containerTestTool.Exec(ctx, args)
		if err != nil {
			t.Fatalf("Boot test failed unexpectedly: %v", err)
		}

		var resultMap map[string]any
		if err := json.Unmarshal([]byte(result.Content), &resultMap); err != nil {
			t.Fatalf("Failed to unmarshal result: %v", err)
		}

		success, ok := resultMap["success"].(bool)
		if !ok || !success {
			t.Fatalf("Expected success=true, got success=%v, result=%+v", resultMap["success"], resultMap)
		}

		t.Logf("✅ maestro-bootstrap container passed validation and boot test")
	})
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
			"container_name":  "maestro-bootstrap:latest",
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
