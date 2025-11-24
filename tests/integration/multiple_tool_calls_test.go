//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orchestrator/pkg/tools"
)

// TestMultipleToolCallsExecution tests that multiple MCP tools can be executed
// and their results properly accumulated using the container test framework.
func TestMultipleToolCallsExecution(t *testing.T) {
	// Skip if Docker is not available
	if !isDockerAvailable() {
		t.Skip("Docker not available, skipping multiple tool calls integration test")
	}

	// Setup test environment
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Create temporary workspace
	tempDir := t.TempDir()
	workspaceDir := filepath.Join(tempDir, "multi_tool_test")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("Failed to create workspace dir: %v", err)
	}

	// Create test files to discover
	testFiles := map[string]string{
		"go.mod":      "module github.com/example/multi-tool-test\n\ngo 1.21\n",
		"main.go":     "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"Hello, World!\")\n}\n",
		"README.md":   "# Multi Tool Test\n\nThis is a test project for multiple tool calls.\n",
		"config.json": `{"app": {"name": "multi-tool-test", "version": "1.0.0"}}`,
	}

	for filename, content := range testFiles {
		filePath := filepath.Join(workspaceDir, filename)
		if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to create test file %s: %v", filename, err)
		}
	}

	// Create container test framework
	framework, err := NewContainerTestFramework(t, workspaceDir)
	if err != nil {
		t.Fatalf("Failed to create container test framework: %v", err)
	}

	// Start container
	if err := framework.StartContainer(ctx); err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}
	defer framework.Cleanup(ctx)

	// Test multiple shell commands that would be typical in discovery phase
	multipleCommands := []struct {
		name string
		args map[string]any
	}{
		{
			name: "shell",
			args: map[string]any{
				"cmd": "ls -la /workspace",
				"cwd": "/workspace",
			},
		},
		{
			name: "shell",
			args: map[string]any{
				"cmd": "find /workspace -name '*.go' -type f",
				"cwd": "/workspace",
			},
		},
		{
			name: "shell",
			args: map[string]any{
				"cmd": "cat /workspace/go.mod",
				"cwd": "/workspace",
			},
		},
		{
			name: "shell",
			args: map[string]any{
				"cmd": "cat /workspace/README.md",
				"cwd": "/workspace",
			},
		},
	}

	// Create shell tool directly (as the coder would)
	shellTool := tools.NewShellTool(framework.GetExecutor())

	// Execute multiple commands and collect results (simulating what happens in one LLM response)
	var results []string
	for i, cmd := range multipleCommands {
		t.Logf("Executing command %d: %s", i+1, cmd.args["cmd"])

		// Execute the shell tool directly
		result, err := shellTool.Exec(ctx, cmd.args)
		if err != nil {
			t.Fatalf("Shell tool execution failed for command %d: %v", i+1, err)
		}

		// Extract command output for verification
		var resultMap map[string]any
		if unmarshalErr := json.Unmarshal([]byte(result.Content), &resultMap); unmarshalErr == nil {
			if stdout, exists := resultMap["stdout"]; exists {
				stdoutStr := stdout.(string)
				results = append(results, stdoutStr)
				t.Logf("Command %d output: %s", i+1, stdoutStr)
			}
		}
	}

	// Verify all expected content is present in results (simulating context accumulation)
	expectedContent := []string{
		"go.mod",  // from ls command
		"main.go", // from find command
		"module github.com/example/multi-tool-test", // from cat go.mod
		"# Multi Tool Test",                         // from cat README.md
	}

	combinedResults := strings.Join(results, "\n")
	for _, expected := range expectedContent {
		if !strings.Contains(combinedResults, expected) {
			t.Errorf("Expected content %q not found in combined results", expected)
		}
	}

	t.Logf("✅ Multiple tool calls executed successfully")
	t.Logf("✅ All %d commands returned expected content", len(multipleCommands))
	t.Logf("✅ Results would be properly accumulated in context for next LLM call")
}
