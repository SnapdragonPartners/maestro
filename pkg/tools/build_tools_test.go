package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"orchestrator/pkg/build"
)

func TestBuildTools(t *testing.T) {
	// Create temporary directory for testing
	tempDir, err := os.MkdirTemp("", "build-tools-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create minimal Go project
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

	// Create build service
	buildService := build.NewBuildService()

	// Test BackendInfoTool
	t.Run("BackendInfoTool", func(t *testing.T) {
		tool := NewBackendInfoTool(buildService)

		// Test tool definition
		def := tool.Definition()
		if def.Name != "backend_info" {
			t.Errorf("Expected name 'backend_info', got '%s'", def.Name)
		}

		// Test execution
		args := map[string]any{
			"cwd": tempDir,
		}

		result, err := tool.Exec(context.Background(), args)
		if err != nil {
			t.Errorf("Tool execution failed: %v", err)
		}

		resultMap, ok := result.(map[string]any)
		if !ok {
			t.Error("Expected result to be map[string]any")
		}

		if !resultMap["success"].(bool) {
			t.Error("Expected success=true")
		}

		if resultMap["backend"].(string) != "go" {
			t.Errorf("Expected backend 'go', got '%s'", resultMap["backend"])
		}
	})

	// Test BuildTool
	t.Run("BuildTool", func(t *testing.T) {
		tool := NewBuildTool(buildService)

		// Test tool definition
		def := tool.Definition()
		if def.Name != "build" {
			t.Errorf("Expected name 'build', got '%s'", def.Name)
		}

		// Test execution
		args := map[string]any{
			"cwd":     tempDir,
			"timeout": 30.0,
		}

		result, err := tool.Exec(context.Background(), args)
		if err != nil {
			t.Errorf("Tool execution failed: %v", err)
		}

		resultMap, ok := result.(map[string]any)
		if !ok {
			t.Error("Expected result to be map[string]any")
		}

		if !resultMap["success"].(bool) {
			t.Errorf("Expected success=true, got error: %v", resultMap["error"])
		}

		if resultMap["backend"].(string) != "go" {
			t.Errorf("Expected backend 'go', got '%s'", resultMap["backend"])
		}

		if resultMap["output"].(string) == "" {
			t.Error("Expected non-empty output")
		}
	})

	// Test TestTool
	t.Run("TestTool", func(t *testing.T) {
		tool := NewTestTool(buildService)

		// Test tool definition
		def := tool.Definition()
		if def.Name != "test" {
			t.Errorf("Expected name 'test', got '%s'", def.Name)
		}

		// Test execution
		args := map[string]any{
			"cwd":     tempDir,
			"timeout": 30.0,
		}

		result, err := tool.Exec(context.Background(), args)
		if err != nil {
			t.Errorf("Tool execution failed: %v", err)
		}

		resultMap, ok := result.(map[string]any)
		if !ok {
			t.Error("Expected result to be map[string]any")
		}

		// Tests might pass or fail, but tool execution should succeed
		if resultMap["backend"].(string) != "go" {
			t.Errorf("Expected backend 'go', got '%s'", resultMap["backend"])
		}

		if resultMap["output"].(string) == "" {
			t.Error("Expected non-empty output")
		}
	})

	// Test LintTool
	t.Run("LintTool", func(t *testing.T) {
		tool := NewLintTool(buildService)

		// Test tool definition
		def := tool.Definition()
		if def.Name != "lint" {
			t.Errorf("Expected name 'lint', got '%s'", def.Name)
		}

		// Test execution
		args := map[string]any{
			"cwd":     tempDir,
			"timeout": 30.0,
		}

		result, err := tool.Exec(context.Background(), args)
		if err != nil {
			t.Errorf("Tool execution failed: %v", err)
		}

		resultMap, ok := result.(map[string]any)
		if !ok {
			t.Error("Expected result to be map[string]any")
		}

		// Lint might pass or fail, but tool execution should succeed
		if resultMap["backend"].(string) != "go" {
			t.Errorf("Expected backend 'go', got '%s'", resultMap["backend"])
		}

		if resultMap["output"].(string) == "" {
			t.Error("Expected non-empty output")
		}
	})

	// Test error handling
	t.Run("ErrorHandling", func(t *testing.T) {
		tool := NewBuildTool(buildService)

		// Test with non-existent directory
		args := map[string]any{
			"cwd": "/non/existent/path",
		}

		result, err := tool.Exec(context.Background(), args)
		if err != nil {
			t.Errorf("Tool execution should not return error, got: %v", err)
		}

		resultMap, ok := result.(map[string]any)
		if !ok {
			t.Error("Expected result to be map[string]any")
		}

		// The null backend might succeed, so let's just check that we got a valid response
		if resultMap["backend"] == nil {
			t.Error("Expected backend to be set")
		}

		t.Logf("Result for non-existent path: success=%v, backend=%v, error=%v",
			resultMap["success"], resultMap["backend"], resultMap["error"])
	})
}

func TestBuildToolsDefinitions(t *testing.T) {
	buildService := build.NewBuildService()

	// Test all tool definitions
	tools := []ToolChannel{
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

		// All tools should have optional cwd parameter
		if _, hasCwd := def.InputSchema.Properties["cwd"]; !hasCwd {
			t.Errorf("Tool %s missing 'cwd' property", def.Name)
		}
	}
}
