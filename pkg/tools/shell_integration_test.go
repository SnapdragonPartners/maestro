package tools

import (
	"context"
	"encoding/json"
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
		t.Fatalf("Shell tool execution failed: %v", err)
	}

	if result == nil {
		t.Fatal("Expected non-nil result")
	}

	var resultMap map[string]any
	if err := json.Unmarshal([]byte(result.Content), &resultMap); err != nil {
		t.Fatalf("Failed to unmarshal result content: %v", err)
	}

	stdout, err := utils.GetMapField[string](resultMap, "stdout")
	if err != nil {
		t.Fatalf("Expected stdout field: %v", err)
	}

	if stdout == "" {
		t.Error("Expected non-empty stdout")
	}

	// JSON unmarshals numbers as float64, convert to int
	exitCodeFloat, err := utils.GetMapField[float64](resultMap, "exit_code")
	if err != nil {
		t.Fatalf("Expected exit_code field: %v", err)
	}

	exitCode := int(exitCodeFloat)
	if exitCode != 0 {
		t.Errorf("Expected exit code 0, got %d", exitCode)
	}

	t.Logf("Shell tool output: %s", stdout)
}
