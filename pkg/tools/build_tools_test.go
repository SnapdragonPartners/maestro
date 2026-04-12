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
		tool := NewBackendInfoTool(buildService, tempDir)

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
		tool := NewBuildTool(buildService, tempDir)

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
		tool := NewTestTool(buildService, tempDir)

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
		tool := NewLintTool(buildService, tempDir)

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
		tool := NewBuildTool(buildService, tempDir)

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
		success, err := utils.GetMapField[bool](resultMap, "success")
		if err != nil {
			t.Errorf("Expected success field: %v", err)
		} else if success {
			t.Error("Expected success to be false for non-existent path")
		}

		errorMsg, err := utils.GetMapField[string](resultMap, "error")
		if err != nil {
			t.Errorf("Expected error field: %v", err)
		} else if errorMsg == "" {
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
		NewBuildTool(buildService, ""),
		NewTestTool(buildService, ""),
		NewLintTool(buildService, ""),
		NewBackendInfoTool(buildService, ""),
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

// TestBuildToolsDefaultWorkDir verifies that build tools use defaultWorkDir
// when cwd is omitted from the LLM's arguments, instead of falling back to os.Getwd().
// Regression test: in production, the orchestrator's cwd is different from the agent
// workspace, causing build validation to inspect the wrong directory.
//
// The test asserts on the Dir field recorded by MockExecutor to confirm the workspace
// path actually reached the executor — not just that the tool call succeeded (which
// would also pass under the broken os.Getwd fallback when the repo has its own Makefile).
func TestBuildToolsDefaultWorkDir(t *testing.T) {
	// Create a temporary workspace with a valid Go project.
	workspace, err := os.MkdirTemp("", "build-default-cwd")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(workspace)

	// Resolve symlinks so assertions match (macOS /var -> /private/var).
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatalf("Failed to resolve symlinks: %v", err)
	}

	if err := os.WriteFile(filepath.Join(workspace, "go.mod"), []byte("module test\ngo 1.21\n"), 0644); err != nil {
		t.Fatalf("Failed to create go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "Makefile"), []byte("build:\n\t@echo ok\ntest:\n\t@echo ok\nlint:\n\t@echo ok\n"), 0644); err != nil {
		t.Fatalf("Failed to create Makefile: %v", err)
	}

	// Build/test/lint tools go through the mock executor, so we can check Dir.
	// BackendInfoTool doesn't call the executor (it only detects backend), so it's
	// tested separately via the returned project_root field.
	t.Run("BuildTestLint_MockDir", func(t *testing.T) {
		tools := []struct {
			name string
			make func(*build.Service, string) Tool
		}{
			{"BuildTool", func(svc *build.Service, ws string) Tool { return NewBuildTool(svc, ws) }},
			{"TestTool", func(svc *build.Service, ws string) Tool { return NewTestTool(svc, ws) }},
			{"LintTool", func(svc *build.Service, ws string) Tool { return NewLintTool(svc, ws) }},
		}

		for _, tt := range tools {
			t.Run(tt.name, func(t *testing.T) {
				buildService := build.NewBuildService()
				mockExec := build.NewMockExecutor()
				buildService.SetExecutor(mockExec)

				tool := tt.make(buildService, workspace)

				// Deliberately omit cwd — tool should fall back to defaultWorkDir.
				result, err := tool.Exec(context.Background(), map[string]any{})
				if err != nil {
					t.Fatalf("Exec returned error: %v", err)
				}

				var resultMap map[string]any
				if err := json.Unmarshal([]byte(result.Content), &resultMap); err != nil {
					t.Fatalf("Failed to unmarshal result: %v", err)
				}

				success, _ := utils.GetMapField[bool](resultMap, "success")
				if !success {
					errMsg, _ := utils.GetMapField[string](resultMap, "error")
					t.Fatalf("Expected success=true, got error: %s", errMsg)
				}

				// Key assertion: the mock executor must have received the workspace
				// path, not the test runner's cwd.
				if len(mockExec.Calls) == 0 {
					t.Fatal("Expected mock executor to be called")
				}
				gotDir := mockExec.Calls[0].Dir
				if gotDir != workspace {
					t.Errorf("Executor received Dir=%q, want %q (defaultWorkDir)", gotDir, workspace)
				}
			})
		}
	})

	// BackendInfoTool doesn't call the executor — verify via project_root in output.
	t.Run("BackendInfoTool_ProjectRoot", func(t *testing.T) {
		buildService := build.NewBuildService()
		mockExec := build.NewMockExecutor()
		buildService.SetExecutor(mockExec)

		tool := NewBackendInfoTool(buildService, workspace)

		result, err := tool.Exec(context.Background(), map[string]any{})
		if err != nil {
			t.Fatalf("Exec returned error: %v", err)
		}

		var resultMap map[string]any
		if err := json.Unmarshal([]byte(result.Content), &resultMap); err != nil {
			t.Fatalf("Failed to unmarshal result: %v", err)
		}

		success, _ := utils.GetMapField[bool](resultMap, "success")
		if !success {
			errMsg, _ := utils.GetMapField[string](resultMap, "error")
			t.Fatalf("Expected success=true, got error: %s", errMsg)
		}

		projectRoot, err := utils.GetMapField[string](resultMap, "project_root")
		if err != nil {
			t.Fatalf("Expected project_root field: %v", err)
		}
		if projectRoot != workspace {
			t.Errorf("project_root=%q, want %q (defaultWorkDir)", projectRoot, workspace)
		}
	})
}

func TestBuildServiceRequiresExecutor(t *testing.T) {
	// Verify that build service fails gracefully without executor.
	buildService := build.NewBuildService()
	// Don't set an executor

	tool := NewBuildTool(buildService, "")

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
