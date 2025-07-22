package exec

import (
	"context"
	"strings"
	"testing"
)

func TestShellCommandAdapter_ExecuteShellCommand(t *testing.T) {
	adapter := NewShellCommandAdapter(NewLocalExec())
	ctx := context.Background()

	// Test simple command.
	result, err := adapter.ExecuteShellCommand(ctx, "echo hello", "")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("Expected result to be map[string]any, got %T", result)
	}

	if resultMap["exit_code"] != 0 {
		t.Errorf("Expected exit code 0, got %v", resultMap["exit_code"])
	}

	stdout, ok := resultMap["stdout"].(string)
	if !ok {
		t.Fatalf("Expected stdout to be string, got %T", resultMap["stdout"])
	}

	if strings.TrimSpace(stdout) != "hello" {
		t.Errorf("Expected stdout 'hello', got %s", stdout)
	}
}

func TestShellCommandAdapter_ExecuteShellCommand_WithCwd(t *testing.T) {
	adapter := NewShellCommandAdapter(NewLocalExec())
	ctx := context.Background()

	// Create temporary directory.
	tempDir := t.TempDir()

	// Test command with working directory.
	result, err := adapter.ExecuteShellCommand(ctx, "pwd", tempDir)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("Expected result to be map[string]any, got %T", result)
	}

	if resultMap["exit_code"] != 0 {
		t.Errorf("Expected exit code 0, got %v", resultMap["exit_code"])
	}

	stdout, ok := resultMap["stdout"].(string)
	if !ok {
		t.Fatalf("Expected stdout to be string, got %T", resultMap["stdout"])
	}

	if !strings.Contains(stdout, tempDir) {
		t.Errorf("Expected stdout to contain %s, got %s", tempDir, stdout)
	}
}

func TestShellCommandAdapter_ExecuteShellCommand_EmptyCommand(t *testing.T) {
	adapter := NewShellCommandAdapter(NewLocalExec())
	ctx := context.Background()

	_, err := adapter.ExecuteShellCommand(ctx, "", "")
	if err == nil {
		t.Error("Expected error for empty command")
	}
}

func TestShellCommandAdapter_ExecuteShellCommand_Failure(t *testing.T) {
	adapter := NewShellCommandAdapter(NewLocalExec())
	ctx := context.Background()

	// Test command that fails.
	result, err := adapter.ExecuteShellCommand(ctx, "false", "")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("Expected result to be map[string]any, got %T", result)
	}

	if resultMap["exit_code"] != 1 {
		t.Errorf("Expected exit code 1, got %v", resultMap["exit_code"])
	}
}

func TestShellCommandAdapter_GetExecutor(t *testing.T) {
	localExec := NewLocalExec()
	adapter := NewShellCommandAdapter(localExec)

	executor := adapter.GetExecutor()
	if executor != localExec {
		t.Error("Expected GetExecutor to return the same executor instance")
	}
}

func TestParseShellCommand(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{
			input:    "echo hello",
			expected: []string{"sh", "-c", "echo hello"},
		},
		{
			input:    "sh -c 'echo hello'",
			expected: []string{"sh", "-c", "'echo hello'"},
		},
		{
			input:    "bash -c 'echo hello'",
			expected: []string{"bash", "-c", "'echo hello'"},
		},
		{
			input:    "ls -la",
			expected: []string{"sh", "-c", "ls -la"},
		},
	}

	for _, test := range tests {
		result := parseShellCommand(test.input)
		if len(result) != len(test.expected) {
			t.Errorf("For input %s, expected %d parts, got %d", test.input, len(test.expected), len(result))
			continue
		}

		for i, part := range result {
			if part != test.expected[i] {
				t.Errorf("For input %s, expected part %d to be %s, got %s", test.input, i, test.expected[i], part)
			}
		}
	}
}
