package tools

import (
	"context"
	"testing"

	"orchestrator/pkg/exec"
	"orchestrator/pkg/utils"
)

func TestShellTool_WithLocalExecutor(t *testing.T) {
	localExec := exec.NewLocalExec()
	tool := NewShellTool(localExec)

	ctx := context.Background()
	args := map[string]any{
		"cmd": "echo 'Hello from local executor'",
	}

	result, err := tool.Exec(ctx, args)
	if err != nil {
		t.Fatalf("Failed to execute command with local executor: %v", err)
	}

	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("Expected result to be map[string]any, got %T", result)
	}

	stdout, okStdout := resultMap["stdout"].(string)
	if !okStdout {
		t.Fatal("Expected stdout to be string")
	}

	if stdout != "Hello from local executor\n" {
		t.Errorf("Expected 'Hello from local executor\\n', got '%s'", stdout)
	}

	if resultMap["exit_code"] != 0 {
		t.Errorf("Expected exit_code 0, got %v", resultMap["exit_code"])
	}
}

func TestShellTool_WithDockerExecutor(t *testing.T) {
	// This test requires Docker to be available.
	dockerExec := exec.NewLongRunningDockerExec("golang:1.24-alpine", "")
	if !dockerExec.Available() {
		t.Skip("Docker not available, skipping Docker executor test")
	}

	tool := NewShellTool(dockerExec)

	ctx := context.Background()
	args := map[string]any{
		"cmd": "echo 'Hello from docker executor'",
	}

	result, err := tool.Exec(ctx, args)
	if err != nil {
		t.Fatalf("Failed to execute command with docker executor: %v", err)
	}

	resultMap, okResult := result.(map[string]any)
	if !okResult {
		t.Fatalf("Expected result to be map[string]any, got %T", result)
	}

	stdout, okStdout := resultMap["stdout"].(string)
	if !okStdout {
		t.Fatal("Expected stdout to be string")
	}

	if stdout != "Hello from docker executor\n" {
		t.Errorf("Expected 'Hello from docker executor\\n', got '%s'", stdout)
	}

	if resultMap["exit_code"] != 0 {
		t.Errorf("Expected exit_code 0, got %v", resultMap["exit_code"])
	}
}

func TestUpdateShellToolExecutor(t *testing.T) {
	// Clear registry for clean test.
	globalRegistry.Clear()

	// Start with local executor.
	localExec := exec.NewLocalExec()
	if err := InitializeShellTool(localExec); err != nil {
		t.Fatalf("Failed to initialize shell tool executor: %v", err)
	}

	// Get the tool and verify it's using local executor.
	tool, err := Get("shell")
	if err != nil {
		t.Fatalf("Failed to get shell tool: %v", err)
	}

	shellTool, okShellTool := tool.(*ShellTool)
	if !okShellTool {
		t.Fatalf("Expected *ShellTool, got %T", tool)
	}

	// Test with local executor.
	ctx := context.Background()
	args := map[string]any{
		"cmd": "echo 'local test'",
	}

	result, err := shellTool.Exec(ctx, args)
	if err != nil {
		t.Fatalf("Failed to execute with local executor: %v", err)
	}

	resultMap, err := utils.AssertMapStringAny(result)
	if err != nil {
		t.Fatalf("Result assertion failed: %v", err)
	}
	if resultMap["exit_code"] != 0 {
		t.Errorf("Expected exit_code 0, got %v", resultMap["exit_code"])
	}

	// Now switch to docker executor if available.
	dockerExec := exec.NewLongRunningDockerExec("golang:1.24-alpine", "")
	if dockerExec.Available() {
		if err := UpdateShellToolExecutor(dockerExec); err != nil { //nolint:govet // Shadow variable acceptable in test context
			t.Fatalf("Failed to update to docker executor: %v", err)
		}

		// Get the tool again and verify it's using docker executor.
		tool, err = Get("shell")
		if err != nil {
			t.Fatalf("Failed to get shell tool after docker update: %v", err)
		}

		shellTool, okShellTool = tool.(*ShellTool)
		if !okShellTool {
			t.Fatalf("Expected *ShellTool after docker update, got %T", tool)
		}

		// Test with docker executor.
		result, err = shellTool.Exec(ctx, args)
		if err != nil {
			t.Fatalf("Failed to execute with docker executor: %v", err)
		}

		resultMap, err = utils.AssertMapStringAny(result)
		if err != nil {
			t.Fatalf("Docker result assertion failed: %v", err)
		}
		if resultMap["exit_code"] != 0 {
			t.Errorf("Expected exit_code 0 with docker, got %v", resultMap["exit_code"])
		}
	} else {
		t.Log("Docker not available, skipping docker executor switch test")
	}
}
