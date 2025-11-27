//go:build integration

package coder

import (
	"context"
	"testing"

	"orchestrator/pkg/tools"
	"orchestrator/pkg/utils"
)

// TestTodosAddToolEndToEnd tests the complete flow:
// 1. Tool receives []any from LLM, validates, returns []string in ProcessEffect
// 2. State handler extracts []string using safe generics
// 3. Todos are processed correctly
func TestTodosAddToolEndToEnd(t *testing.T) {
	tool := tools.NewTodosAddTool()
	ctx := context.Background()

	// Simulate what the LLM sends: array of strings as []any
	// This is what happens when JSON is unmarshaled - string arrays become []any
	args := map[string]any{
		"todos": []any{
			"Create main.go with basic structure",
			"Implement HTTP server setup",
			"Add error handling",
		},
	}

	// Execute the tool
	result, err := tool.Exec(ctx, args)
	if err != nil {
		t.Fatalf("Tool execution failed: %v", err)
	}

	// Verify ProcessEffect is present
	if result.ProcessEffect == nil {
		t.Fatal("Expected ProcessEffect to be present")
	}

	if result.ProcessEffect.Signal != tools.SignalCoding {
		t.Errorf("Expected signal %s, got: %s", tools.SignalCoding, result.ProcessEffect.Signal)
	}

	// Verify ProcessEffect.Data is a map
	effectData, ok := result.ProcessEffect.Data.(map[string]any)
	if !ok {
		t.Fatalf("Expected ProcessEffect.Data to be map[string]any, got: %T", result.ProcessEffect.Data)
	}

	// Test extraction using the same pattern as plan_review.go
	// This is the critical part that was breaking
	todos, err := utils.GetMapField[[]string](effectData, "todos")
	if err != nil {
		t.Fatalf("Failed to extract todos using safe generics: %v", err)
	}

	// Verify todos content
	expectedTodos := []string{
		"Create main.go with basic structure",
		"Implement HTTP server setup",
		"Add error handling",
	}

	if len(todos) != len(expectedTodos) {
		t.Errorf("Expected %d todos, got %d", len(expectedTodos), len(todos))
	}

	for i, expected := range expectedTodos {
		if i >= len(todos) {
			t.Errorf("Missing todo at index %d: %s", i, expected)
			continue
		}
		if todos[i] != expected {
			t.Errorf("Todo %d mismatch: expected %q, got %q", i, expected, todos[i])
		}
	}

	t.Logf("✅ End-to-end todos flow works correctly:")
	t.Logf("   - Tool received []any from LLM")
	t.Logf("   - Tool validated and returned []string in ProcessEffect")
	t.Logf("   - State handler extracted []string using safe generics")
	t.Logf("   - All %d todos processed successfully", len(todos))
}

// TestTodosAddToolEmptyArray tests that empty arrays are rejected
func TestTodosAddToolEmptyArray(t *testing.T) {
	tool := tools.NewTodosAddTool()
	ctx := context.Background()

	args := map[string]any{
		"todos": []any{},
	}

	_, err := tool.Exec(ctx, args)
	if err == nil {
		t.Error("Expected error for empty todos array, got nil")
	}
}

// TestTodosAddToolInvalidType tests that non-string items are rejected
func TestTodosAddToolInvalidType(t *testing.T) {
	tool := tools.NewTodosAddTool()
	ctx := context.Background()

	args := map[string]any{
		"todos": []any{
			"Valid todo",
			123, // Invalid - not a string
			"Another valid todo",
		},
	}

	_, err := tool.Exec(ctx, args)
	if err == nil {
		t.Error("Expected error for non-string todo item, got nil")
	}
}

// TestTodosAddToolEmptyString tests that empty strings are rejected
func TestTodosAddToolEmptyString(t *testing.T) {
	tool := tools.NewTodosAddTool()
	ctx := context.Background()

	args := map[string]any{
		"todos": []any{
			"Valid todo",
			"", // Invalid - empty string
			"Another valid todo",
		},
	}

	_, err := tool.Exec(ctx, args)
	if err == nil {
		t.Error("Expected error for empty string todo item, got nil")
	}
}

// TestTodosAddToolBoundaries tests min/max array size boundaries
func TestTodosAddToolBoundaries(t *testing.T) {
	tool := tools.NewTodosAddTool()
	ctx := context.Background()

	// Test minimum (1 item) - should pass
	args := map[string]any{
		"todos": []any{"Single todo"},
	}
	_, err := tool.Exec(ctx, args)
	if err != nil {
		t.Errorf("Expected single todo to be valid, got error: %v", err)
	}

	// Test maximum (20 items) - should pass
	maxTodos := make([]any, 20)
	for i := 0; i < 20; i++ {
		maxTodos[i] = "Todo item"
	}
	args = map[string]any{
		"todos": maxTodos,
	}
	_, err = tool.Exec(ctx, args)
	if err != nil {
		t.Errorf("Expected 20 todos to be valid, got error: %v", err)
	}

	// Test over maximum (21 items) - should fail
	overMaxTodos := make([]any, 21)
	for i := 0; i < 21; i++ {
		overMaxTodos[i] = "Todo item"
	}
	args = map[string]any{
		"todos": overMaxTodos,
	}
	_, err = tool.Exec(ctx, args)
	if err == nil {
		t.Error("Expected error for 21 todos (over max), got nil")
	}
}
