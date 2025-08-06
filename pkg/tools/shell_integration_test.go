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
		t.Fatalf("Shell tool execution failed: %v", err)
	}

	resultMap, err := utils.AssertMapStringAny(result)
	if err != nil {
		t.Fatalf("Expected result to be map[string]any: %v", err)
	}

	stdout, err := utils.GetMapField[string](resultMap, "stdout")
	if err != nil {
		t.Fatalf("Expected stdout field: %v", err)
	}

	if stdout == "" {
		t.Error("Expected non-empty stdout")
	}

	exitCode, err := utils.GetMapField[int](resultMap, "exit_code")
	if err != nil {
		t.Fatalf("Expected exit_code field: %v", err)
	}

	if exitCode != 0 {
		t.Errorf("Expected exit code 0, got %d", exitCode)
	}

	t.Logf("Shell tool output: %s", stdout)
}

// LEGACY TESTS - TO BE REWRITTEN FOR NEW TOOLPROVIDER SYSTEM.
func TestShellToolIntegration(t *testing.T) {
	t.Skip("Skipping legacy test - to be rewritten for new ToolProvider system")
}

func TestShellToolExecutorUpdate(t *testing.T) {
	t.Skip("Skipping legacy test - to be rewritten for new ToolProvider system")
}
