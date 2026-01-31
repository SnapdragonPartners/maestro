//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/exec"
	"orchestrator/pkg/tools"
)

// TestContainerSwitchFallback tests that container_switch correctly falls back
// to the bootstrap container when the target container doesn't exist or fails validation.
func TestContainerSwitchFallback(t *testing.T) {
	// Skip if Docker is not available
	if !isDockerAvailable() {
		t.Skip("Docker not available, skipping container switch fallback test")
	}

	// Set up test config
	setupContainerSwitchTestConfig(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	t.Run("fallback_to_bootstrap_on_nonexistent_container", func(t *testing.T) {
		// Create container switch tool
		switchTool := tools.NewContainerSwitchTool()

		// Attempt to switch to a non-existent container
		args := map[string]any{
			"container_name": "nonexistent-container-12345-xyz",
		}

		result, err := switchTool.Exec(ctx, args)
		if err != nil {
			t.Fatalf("container_switch should not return error, got: %v", err)
		}

		// Parse the result
		var resultMap map[string]any
		if unmarshalErr := json.Unmarshal([]byte(result.Content), &resultMap); unmarshalErr != nil {
			t.Fatalf("Failed to unmarshal result: %v", unmarshalErr)
		}

		// The result should indicate fallback was used (if bootstrap is available)
		// or both failed (if bootstrap isn't available)
		t.Logf("Result: %+v", resultMap)

		// Check if fallback_used is true OR if both containers failed
		if fallbackUsed, ok := resultMap["fallback_used"].(bool); ok && fallbackUsed {
			t.Logf("✅ Fallback to bootstrap container was used")

			// Verify current_container is the bootstrap container
			if currentContainer, ok := resultMap["current_container"].(string); ok {
				if currentContainer != config.BootstrapContainerTag {
					t.Errorf("Expected current_container to be %s, got %s",
						config.BootstrapContainerTag, currentContainer)
				}
			}

			// Verify original_error contains information about the failed container
			if originalError, ok := resultMap["original_error"].(string); ok {
				t.Logf("Original error: %s", originalError)
			}
		} else {
			// Both containers failed - this is acceptable if bootstrap isn't available
			if success, ok := resultMap["success"].(bool); ok && !success {
				if errMsg, ok := resultMap["error"].(string); ok {
					t.Logf("Both containers failed (bootstrap not available): %s", errMsg)
				}
			} else {
				t.Errorf("Unexpected result state: %+v", resultMap)
			}
		}
	})

	t.Run("switch_to_valid_container", func(t *testing.T) {
		// Skip if bootstrap container isn't available
		if !isBootstrapContainerAvailable() {
			t.Skip("Bootstrap container not available, skipping valid switch test")
		}

		switchTool := tools.NewContainerSwitchTool()

		// Switch to the bootstrap container (which should always work)
		args := map[string]any{
			"container_name": config.BootstrapContainerTag,
		}

		result, err := switchTool.Exec(ctx, args)
		if err != nil {
			t.Fatalf("container_switch should not return error: %v", err)
		}

		var resultMap map[string]any
		if unmarshalErr := json.Unmarshal([]byte(result.Content), &resultMap); unmarshalErr != nil {
			t.Fatalf("Failed to unmarshal result: %v", unmarshalErr)
		}

		// Check for success
		success, ok := resultMap["success"].(bool)
		if !ok || !success {
			t.Errorf("Expected success=true when switching to bootstrap, got: %+v", resultMap)
		}

		// Verify current_container matches what we requested
		if currentContainer, ok := resultMap["current_container"].(string); ok {
			if currentContainer != config.BootstrapContainerTag {
				t.Errorf("Expected current_container=%s, got %s",
					config.BootstrapContainerTag, currentContainer)
			}
		}

		t.Logf("✅ Successfully switched to bootstrap container")
	})

	t.Run("result_structure_on_failure", func(t *testing.T) {
		switchTool := tools.NewContainerSwitchTool()

		// Use a clearly invalid container name
		args := map[string]any{
			"container_name": "invalid-container-that-does-not-exist-99999",
		}

		result, err := switchTool.Exec(ctx, args)
		if err != nil {
			t.Fatalf("container_switch should return structured result, not error: %v", err)
		}

		var resultMap map[string]any
		if unmarshalErr := json.Unmarshal([]byte(result.Content), &resultMap); unmarshalErr != nil {
			t.Fatalf("Failed to unmarshal result: %v", unmarshalErr)
		}

		// Verify the result has expected structure for LLM consumption
		expectedFields := []string{"success", "requested_container"}
		for _, field := range expectedFields {
			if _, ok := resultMap[field]; !ok {
				t.Errorf("Expected field '%s' in result, got: %+v", field, resultMap)
			}
		}

		// requested_container should match what we requested
		if requested, ok := resultMap["requested_container"].(string); ok {
			if requested != "invalid-container-that-does-not-exist-99999" {
				t.Errorf("requested_container should match input, got: %s", requested)
			}
		}

		t.Logf("✅ Result structure is correct for LLM consumption")
	})
}

// TestContainerSwitchValidation tests that container_switch validates required parameters.
func TestContainerSwitchValidation(t *testing.T) {
	setupContainerSwitchTestConfig(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Run("missing_container_name", func(t *testing.T) {
		switchTool := tools.NewContainerSwitchTool()

		// Call without container_name
		args := map[string]any{}

		_, err := switchTool.Exec(ctx, args)
		if err == nil {
			t.Error("Expected error for missing container_name")
		}

		t.Logf("✅ Missing container_name correctly returns error: %v", err)
	})

	t.Run("empty_container_name", func(t *testing.T) {
		switchTool := tools.NewContainerSwitchTool()

		// Call with empty container_name
		args := map[string]any{
			"container_name": "",
		}

		_, err := switchTool.Exec(ctx, args)
		if err == nil {
			t.Error("Expected error for empty container_name")
		}

		t.Logf("✅ Empty container_name correctly returns error: %v", err)
	})
}

// setupContainerSwitchTestConfig sets up minimal config for container switch tests.
func setupContainerSwitchTestConfig(t *testing.T) {
	t.Helper()

	// Create minimal config
	testConfig := `{
		"git": {
			"repo_url": "https://github.com/anthropics/anthropic-sdk-python.git"
		},
		"container": {
			"name": "test-container"
		},
		"pm": {
			"enabled": false
		},
		"chat": {
			"enabled": false
		},
		"demo": {
			"enabled": false
		}
	}`

	projectDir := t.TempDir()
	maestroDir := filepath.Join(projectDir, ".maestro")
	if err := os.MkdirAll(maestroDir, 0755); err != nil {
		t.Fatalf("Failed to create .maestro directory: %v", err)
	}

	configPath := filepath.Join(maestroDir, "config.json")
	if err := os.WriteFile(configPath, []byte(testConfig), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	_ = os.Setenv("CONFIG_PATH", projectDir)

	if err := config.LoadConfig(projectDir); err != nil {
		t.Fatalf("Failed to load test config: %v", err)
	}
}

// isBootstrapContainerAvailable checks if the maestro-bootstrap image exists locally.
func isBootstrapContainerAvailable() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	executor := exec.NewLocalExec()
	result := tools.ValidateContainerCapabilities(ctx, executor, config.BootstrapContainerTag)
	return result.Success
}
