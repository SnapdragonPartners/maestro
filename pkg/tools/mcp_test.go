package tools

import (
	"context"
	"testing"
)

func TestRegistry_Register(t *testing.T) {
	registry := &Registry{tools: make(map[string]ToolChannel)}
	
	tool := NewShellTool()
	
	// Test successful registration
	if err := registry.Register(tool); err != nil {
		t.Errorf("Expected no error registering tool, got %v", err)
	}
	
	// Test duplicate registration
	if err := registry.Register(tool); err == nil {
		t.Error("Expected error when registering duplicate tool")
	}
	
	// Test nil tool registration
	if err := registry.Register(nil); err == nil {
		t.Error("Expected error when registering nil tool")
	}
}

func TestRegistry_Get(t *testing.T) {
	registry := &Registry{tools: make(map[string]ToolChannel)}
	tool := NewShellTool()
	
	// Test getting non-existent tool
	if _, err := registry.Get("nonexistent"); err == nil {
		t.Error("Expected error when getting non-existent tool")
	}
	
	// Register and test getting existing tool
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
	
	// Test empty registry
	all := registry.GetAll()
	if len(all) != 0 {
		t.Errorf("Expected empty registry, got %d tools", len(all))
	}
	
	// Add tools and test
	tool1 := NewShellTool()
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
	// Clear global registry for clean test
	globalRegistry.Clear()
	
	tool := NewShellTool()
	
	// Test global Register
	if err := Register(tool); err != nil {
		t.Errorf("Expected no error with global Register, got %v", err)
	}
	
	// Test global Get
	retrieved, err := Get("shell")
	if err != nil {
		t.Errorf("Expected no error with global Get, got %v", err)
	}
	
	if retrieved.Name() != "shell" {
		t.Errorf("Expected tool name 'shell', got '%s'", retrieved.Name())
	}
	
	// Test global GetAll
	all := GetAll()
	if len(all) != 1 {
		t.Errorf("Expected 1 tool in global registry, got %d", len(all))
	}
}

func TestShellTool_Name(t *testing.T) {
	tool := NewShellTool()
	if tool.Name() != "shell" {
		t.Errorf("Expected tool name 'shell', got '%s'", tool.Name())
	}
}

func TestShellTool_Exec(t *testing.T) {
	tool := NewShellTool()
	ctx := context.Background()
	
	// Test missing cmd argument
	args := map[string]any{}
	if _, err := tool.Exec(ctx, args); err == nil {
		t.Error("Expected error when cmd argument is missing")
	}
	
	// Test invalid cmd argument type
	args = map[string]any{"cmd": 123}
	if _, err := tool.Exec(ctx, args); err == nil {
		t.Error("Expected error when cmd argument is not a string")
	}
	
	// Test valid execution
	args = map[string]any{"cmd": "echo hello"}
	result, err := tool.Exec(ctx, args)
	if err != nil {
		t.Errorf("Expected no error with valid args, got %v", err)
	}
	
	// Check result structure
	if stdout, ok := result["stdout"]; !ok {
		t.Error("Expected stdout in result")
	} else if stdoutStr, ok := stdout.(string); !ok {
		t.Error("Expected stdout to be string")
	} else if stdoutStr == "" {
		t.Error("Expected non-empty stdout")
	}
	
	if _, ok := result["stderr"]; !ok {
		t.Error("Expected stderr in result")
	}
	
	if exitCode, ok := result["exit_code"]; !ok {
		t.Error("Expected exit_code in result")
	} else if code, ok := exitCode.(int); !ok {
		t.Error("Expected exit_code to be int")
	} else if code != 0 {
		t.Errorf("Expected exit_code 0, got %d", code)
	}
	
	// Test with cwd argument
	args = map[string]any{
		"cmd": "pwd",
		"cwd": "/tmp",
	}
	result, err = tool.Exec(ctx, args)
	if err != nil {
		t.Errorf("Expected no error with cwd arg, got %v", err)
	}
	
	if cwd, ok := result["cwd"]; !ok {
		t.Error("Expected cwd in result")
	} else if cwdStr, ok := cwd.(string); !ok {
		t.Error("Expected cwd to be string")
	} else if cwdStr != "/tmp" {
		t.Errorf("Expected cwd '/tmp', got '%s'", cwdStr)
	}
}