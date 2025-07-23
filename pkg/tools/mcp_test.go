package tools

import (
	"context"
	"testing"

	"orchestrator/pkg/exec"
)

func TestRegistry_Register(t *testing.T) {
	registry := &Registry{tools: make(map[string]ToolChannel)}

	tool := NewShellTool(exec.NewLocalExec())

	// Test successful registration.
	if err := registry.Register(tool); err != nil {
		t.Errorf("Expected no error registering tool, got %v", err)
	}

	// Test duplicate registration.
	if err := registry.Register(tool); err == nil {
		t.Error("Expected error when registering duplicate tool")
	}

	// Test nil tool registration.
	if err := registry.Register(nil); err == nil {
		t.Error("Expected error when registering nil tool")
	}
}

func TestRegistry_Get(t *testing.T) {
	registry := &Registry{tools: make(map[string]ToolChannel)}
	tool := NewShellTool(exec.NewLocalExec())

	// Test getting non-existent tool.
	if _, err := registry.Get("nonexistent"); err == nil {
		t.Error("Expected error when getting non-existent tool")
	}

	// Register and test getting existing tool.
	if err := registry.Register(tool); err != nil {
		t.Fatalf("Failed to register tool: %v", err)
	}

	retrieved, err := registry.Get("shell")
	if err != nil {
		t.Errorf("Expected no error getting registered tool, got %v", err)
	}

	if retrieved != tool {
		t.Error("Retrieved tool is not the same as registered tool")
	}
}

func TestRegistry_GetAll(t *testing.T) {
	registry := &Registry{tools: make(map[string]ToolChannel)}

	// Test empty registry.
	all := registry.GetAll()
	if len(all) != 0 {
		t.Errorf("Expected empty registry, got %d tools", len(all))
	}

	// Add tools and test.
	tool1 := NewShellTool(exec.NewLocalExec())
	registry.Register(tool1)

	all = registry.GetAll()
	if len(all) != 1 {
		t.Errorf("Expected 1 tool, got %d", len(all))
	}

	if all["shell"] != tool1 {
		t.Error("GetAll did not return correct tool")
	}
}

func TestGlobalRegistry(t *testing.T) {
	// Clear global registry for clean test.
	globalRegistry.Clear()

	// Test InitializeShellTool.
	if err := InitializeShellTool(exec.NewLocalExec()); err != nil {
		t.Errorf("Expected no error with InitializeShellTool, got %v", err)
	}

	// Test global Get.
	retrieved, err := Get("shell")
	if err != nil {
		t.Errorf("Expected no error with global Get, got %v", err)
	}

	if retrieved.Name() != "shell" {
		t.Errorf("Expected tool name 'shell', got '%s'", retrieved.Name())
	}

	// Test global GetAll.
	all := GetAll()
	if len(all) != 1 {
		t.Errorf("Expected 1 tool in global registry, got %d", len(all))
	}

	// Test GetToolDefinitions.
	defs := GetToolDefinitions()
	if len(defs) != 1 {
		t.Errorf("Expected 1 tool definition, got %d", len(defs))
	}

	if defs[0].Name != "shell" {
		t.Errorf("Expected tool definition name 'shell', got '%s'", defs[0].Name)
	}
}

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
