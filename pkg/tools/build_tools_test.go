//nolint:govet // Shadow variables in tests are acceptable
package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"orchestrator/pkg/build"
	"orchestrator/pkg/utils"
)

func TestBuildTools(t *testing.T) {
	// Create temporary directory for testing.
	tempDir, err := os.MkdirTemp("", "build-tools-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create minimal Go project for backend detection.
	goMod := `module test-project
go 1.21
`
	if err := os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatalf("Failed to create go.mod: %v", err)
	}

	mainGo := `package main
import "fmt"
func main() {
	fmt.Println("Hello, World!")
}`
	if err := os.WriteFile(filepath.Join(tempDir, "main.go"), []byte(mainGo), 0644); err != nil {
		t.Fatalf("Failed to create main.go: %v", err)
	}

	// Create Makefile for backend detection.
	makefile := `build:
	@echo "Building"

test:
	@echo "Testing"

lint:
	@echo "Linting"
`
	if err := os.WriteFile(filepath.Join(tempDir, "Makefile"), []byte(makefile), 0644); err != nil {
		t.Fatalf("Failed to create Makefile: %v", err)
	}

	// Create build service with mock executor.
	// We use MockExecutor because build operations should run in containers,
	// not on the host. Tests should not depend on make being installed.
	buildService := build.NewBuildService()
	mockExec := build.NewMockExecutor()
	buildService.SetExecutor(mockExec)

	// Test BackendInfoTool.
	t.Run("BackendInfoTool", func(t *testing.T) {
		tool := NewBackendInfoTool(buildService)

		// Test tool definition.
		def := tool.Definition()
		if def.Name != "backend_info" {
			t.Errorf("Expected name 'backend_info', got '%s'", def.Name)
		}

		// Test execution.
		args := map[string]any{
			"cwd": tempDir,
		}

		result, err := tool.Exec(context.Background(), args)
		if err != nil {
			t.Errorf("Tool execution failed: %v", err)
		}

		if result == nil {
			t.Fatal("Expected non-nil result")
		}

		var resultMap map[string]any
		if err := json.Unmarshal([]byte(result.Content), &resultMap); err != nil {
			t.Fatalf("Failed to unmarshal result content: %v", err)
		}

		success, err := utils.GetMapField[bool](resultMap, "success")
		if err != nil {
			t.Errorf("Expected success field: %v", err)
		}
		if !success {
			t.Error("Expected success=true")
		}

		backend, err := utils.GetMapField[string](resultMap, "backend")
		if err != nil {
			t.Errorf("Expected backend field: %v", err)
		}
		if backend != "go" {
			t.Errorf("Expected backend 'go', got '%s'", backend)
		}
	})

	// Test BuildTool.
	t.Run("BuildTool", func(t *testing.T) {
		tool := NewBuildTool(buildService)

		// Test tool definition.
		def := tool.Definition()
		if def.Name != "build" {
			t.Errorf("Expected name 'build', got '%s'", def.Name)
		}

		// Test execution.
		args := map[string]any{
			"cwd":     tempDir,
			"timeout": 30.0,
		}

		result, err := tool.Exec(context.Background(), args)
		if err != nil {
			t.Errorf("Tool execution failed: %v", err)
		}

		if result == nil {
			t.Fatal("Expected non-nil result")
		}

		var resultMap map[string]any
		if err := json.Unmarshal([]byte(result.Content), &resultMap); err != nil {
			t.Fatalf("Failed to unmarshal result content: %v", err)
		}

		if success, err := utils.GetMapField[bool](resultMap, "success"); err != nil || !success {
			if err != nil {
				t.Errorf("Expected success field: %v", err)
			} else {
				t.Errorf("Expected success=true, got error: %v", resultMap["error"])
			}
		}

		if backend, err := utils.GetMapField[string](resultMap, "backend"); err != nil || backend != "go" {
			if err != nil {
				t.Errorf("Expected backend field: %v", err)
			} else {
				t.Errorf("Expected backend 'go', got '%s'", backend)
			}
		}

		// Verify mock was called with correct command.
		if len(mockExec.Calls) == 0 {
			t.Error("Expected mock executor to be called")
		}
	})

	// Test TestTool.
	t.Run("TestTool", func(t *testing.T) {
		tool := NewTestTool(buildService)

		// Test tool definition.
		def := tool.Definition()
		if def.Name != "test" {
			t.Errorf("Expected name 'test', got '%s'", def.Name)
		}

		// Test execution.
		args := map[string]any{
			"cwd":     tempDir,
			"timeout": 30.0,
		}

		result, err := tool.Exec(context.Background(), args)
		if err != nil {
			t.Errorf("Tool execution failed: %v", err)
		}

		if result == nil {
			t.Fatal("Expected non-nil result")
		}

		var resultMap map[string]any
		if err := json.Unmarshal([]byte(result.Content), &resultMap); err != nil {
			t.Fatalf("Failed to unmarshal result content: %v", err)
		}

		if backend, err := utils.GetMapField[string](resultMap, "backend"); err != nil || backend != "go" {
			if err != nil {
				t.Errorf("Expected backend field: %v", err)
			} else {
				t.Errorf("Expected backend 'go', got '%s'", backend)
			}
		}
	})

	// Test LintTool.
	t.Run("LintTool", func(t *testing.T) {
		tool := NewLintTool(buildService)

		// Test tool definition.
		def := tool.Definition()
		if def.Name != "lint" {
			t.Errorf("Expected name 'lint', got '%s'", def.Name)
		}

		// Test execution.
		args := map[string]any{
			"cwd":     tempDir,
			"timeout": 30.0,
		}

		result, err := tool.Exec(context.Background(), args)
		if err != nil {
			t.Errorf("Tool execution failed: %v", err)
		}

		if result == nil {
			t.Fatal("Expected non-nil result")
		}

		var resultMap map[string]any
		if err := json.Unmarshal([]byte(result.Content), &resultMap); err != nil {
			t.Fatalf("Failed to unmarshal result content: %v", err)
		}

		if backend, err := utils.GetMapField[string](resultMap, "backend"); err != nil || backend != "go" {
			if err != nil {
				t.Errorf("Expected backend field: %v", err)
			} else {
				t.Errorf("Expected backend 'go', got '%s'", backend)
			}
		}
	})

	// Test error handling.
	t.Run("ErrorHandling", func(t *testing.T) {
		tool := NewBuildTool(buildService)

		// Test with non-existent directory.
		args := map[string]any{
			"cwd": "/non/existent/path",
		}

		result, err := tool.Exec(context.Background(), args)
		if err != nil {
			t.Errorf("Tool execution should not return Go error, got: %v", err)
		}

		if result == nil {
			t.Fatal("Expected non-nil result")
		}

		var resultMap map[string]any
		if err := json.Unmarshal([]byte(result.Content), &resultMap); err != nil {
			t.Fatalf("Failed to unmarshal result content: %v", err)
		}

		// Should have success=false and an error message for invalid path.
		if success, ok := resultMap["success"].(bool); !ok || success {
			t.Error("Expected success to be false for non-existent path")
		}

		if errorMsg, ok := resultMap["error"].(string); !ok || errorMsg == "" {
			t.Error("Expected error message for non-existent path")
		}

		t.Logf("Result for non-existent path: success=%v, backend=%v, error=%v",
			resultMap["success"], resultMap["backend"], resultMap["error"])
	})
}

func TestBuildToolsDefinitions(t *testing.T) {
	buildService := build.NewBuildService()
	mockExec := build.NewMockExecutor()
	buildService.SetExecutor(mockExec)

	// Test all tool definitions.
	tools := []Tool{
		NewBuildTool(buildService),
		NewTestTool(buildService),
		NewLintTool(buildService),
		NewBackendInfoTool(buildService),
	}

	expectedNames := []string{"build", "test", "lint", "backend_info"}

	for i, tool := range tools {
		def := tool.Definition()

		if def.Name != expectedNames[i] {
			t.Errorf("Expected tool name '%s', got '%s'", expectedNames[i], def.Name)
		}

		if def.Description == "" {
			t.Errorf("Tool %s missing description", def.Name)
		}

		if def.InputSchema.Type != "object" {
			t.Errorf("Tool %s should have object input schema", def.Name)
		}

		// All tools should have optional cwd parameter.
		if _, hasCwd := def.InputSchema.Properties["cwd"]; !hasCwd {
			t.Errorf("Tool %s missing 'cwd' property", def.Name)
		}
	}
}

func TestBuildServiceRequiresExecutor(t *testing.T) {
	// Verify that build service fails gracefully without executor.
	buildService := build.NewBuildService()
	// Don't set an executor

	tool := NewBuildTool(buildService)

	args := map[string]any{
		"cwd": "/tmp",
	}

	result, err := tool.Exec(context.Background(), args)
	if err != nil {
		t.Errorf("Tool execution should not return Go error, got: %v", err)
	}

	var resultMap map[string]any
	if err := json.Unmarshal([]byte(result.Content), &resultMap); err != nil {
		t.Fatalf("Failed to unmarshal result content: %v", err)
	}

	// Should fail because no executor is configured.
	success, err := utils.GetMapField[bool](resultMap, "success")
	if err != nil {
		t.Errorf("Expected success field: %v", err)
	} else if success {
		t.Error("Expected success to be false when executor not configured")
	}

	errorMsg, err := utils.GetMapField[string](resultMap, "error")
	if err != nil {
		t.Errorf("Expected error field: %v", err)
	} else if errorMsg == "" {
		t.Error("Expected non-empty error message when executor not configured")
	}
}
