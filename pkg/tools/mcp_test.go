package tools

import (
	"context"
	"testing"

	"orchestrator/pkg/exec"
)

// Tests for MCP tool system

func TestShellTool_Name(t *testing.T) {
	tool := NewShellTool(exec.NewLocalExec())
	if tool.Name() != "shell" {
		t.Errorf("Expected tool name 'shell', got '%s'", tool.Name())
	}
}

func TestShellTool_Definition(t *testing.T) {
	tool := NewShellTool(exec.NewLocalExec())
	def := tool.Definition()

	if def.Name != "shell" {
		t.Errorf("Expected definition name 'shell', got '%s'", def.Name)
	}

	if def.Description == "" {
		t.Error("Expected non-empty description")
	}

	if def.InputSchema.Type != "object" {
		t.Errorf("Expected input schema type 'object', got '%s'", def.InputSchema.Type)
	}

	if len(def.InputSchema.Required) == 0 || def.InputSchema.Required[0] != "cmd" {
		t.Error("Expected 'cmd' to be a required property")
	}

	if _, ok := def.InputSchema.Properties["cmd"]; !ok {
		t.Error("Expected 'cmd' property in schema")
	}

	if _, ok := def.InputSchema.Properties["cwd"]; !ok {
		t.Error("Expected 'cwd' property in schema")
	}
}

func TestShellTool_Exec(t *testing.T) {
	tool := NewShellTool(exec.NewLocalExec())
	ctx := context.Background()

	// Test missing cmd argument.
	args := map[string]any{}
	if _, err := tool.Exec(ctx, args); err == nil {
		t.Error("Expected error when cmd argument is missing")
	}

	// Test invalid cmd argument type.
	args = map[string]any{"cmd": 123}
	if _, err := tool.Exec(ctx, args); err == nil {
		t.Error("Expected error when cmd argument is not a string")
	}

	// Test valid execution.
	args = map[string]any{"cmd": "echo hello"}
	result, err := tool.Exec(ctx, args)
	if err != nil {
		t.Errorf("Expected no error with valid args, got %v", err)
	}

	// Check result structure.
	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("Expected result to be map[string]any, got %T", result)
	}

	if stdout, okStdout := resultMap["stdout"]; !okStdout {
		t.Error("Expected stdout in result")
	} else if stdoutStr, okStdout := stdout.(string); !okStdout {
		t.Error("Expected stdout to be string")
	} else if stdoutStr == "" {
		t.Error("Expected non-empty stdout")
	}

	if _, okStderr := resultMap["stderr"]; !okStderr {
		t.Error("Expected stderr in result")
	}

	if exitCode, okExitCode := resultMap["exit_code"]; !okExitCode {
		t.Error("Expected exit_code in result")
	} else if code, okExitCode := exitCode.(int); !okExitCode {
		t.Error("Expected exit_code to be int")
	} else if code != 0 {
		t.Errorf("Expected exit_code 0, got %d", code)
	}

	// Test with cwd argument.
	args = map[string]any{
		"cmd": "pwd",
		"cwd": "/tmp",
	}
	result, err = tool.Exec(ctx, args)
	if err != nil {
		t.Errorf("Expected no error with cwd arg, got %v", err)
	}

	resultMap, ok = result.(map[string]any)
	if !ok {
		t.Fatalf("Expected result to be map[string]any, got %T", result)
	}

	if cwd, ok := resultMap["cwd"]; !ok {
		t.Error("Expected cwd in result")
	} else if cwdStr, ok := cwd.(string); !ok {
		t.Error("Expected cwd to be string")
	} else if cwdStr != "/tmp" {
		t.Errorf("Expected cwd '/tmp', got '%s'", cwdStr)
	}
}
