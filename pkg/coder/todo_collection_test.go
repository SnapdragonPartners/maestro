package coder

import (
	"context"
	"testing"

	"orchestrator/pkg/tools"
)

// NOTE: Integration test for handleTodosAdd with real LLM calls can be added later.
// For now, we rely on the tool validation test below to ensure the tool returns []string,
// and manual testing to verify the handler properly handles []string from tool results.

// TestTodoToolValidation tests the todos_add tool validation logic directly.
// This validates that the tool properly converts inputs and validates constraints.
func TestTodoToolValidation(t *testing.T) {
	tool := tools.NewTodosAddTool()

	//nolint:govet // Test struct, optimization not critical
	tests := []struct {
		name      string
		args      map[string]any
		wantError bool
	}{
		{
			name: "valid_todos",
			args: map[string]any{
				"todos": []any{"Create file", "Add function", "Test code"},
			},
			wantError: false,
		},
		{
			name: "empty_array",
			args: map[string]any{
				"todos": []any{},
			},
			wantError: true,
		},
		{
			name: "too_many_todos",
			args: func() map[string]any {
				todos := make([]any, 25) // More than MaxItems (20)
				for i := range todos {
					todos[i] = "todo item"
				}
				return map[string]any{"todos": todos}
			}(),
			wantError: true,
		},
		{
			name:      "missing_todos",
			args:      map[string]any{},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tool.Exec(context.Background(), tt.args)

			if tt.wantError {
				if err == nil {
					t.Errorf("Expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			// Verify result structure
			resultMap, ok := result.(map[string]any)
			if !ok {
				t.Fatalf("Expected result to be map[string]any, got %T", result)
			}

			if success, ok := resultMap["success"].(bool); !ok || !success {
				t.Error("Expected success=true in result")
			}

			// Verify tool returns []string (this is what the handler must handle)
			if validatedTodos, ok := resultMap["todos"].([]string); !ok {
				t.Errorf("Expected validated todos as []string, got %T", resultMap["todos"])
			} else {
				originalTodos, ok := tt.args["todos"].([]any)
				if !ok {
					t.Fatalf("Test setup error: todos should be []any")
				}
				if len(validatedTodos) != len(originalTodos) {
					t.Errorf("Expected %d validated todos, got %d", len(originalTodos), len(validatedTodos))
				}
			}
		})
	}
}

// TestTodoListCompleteCurrent is a unit test for the TodoList.CompleteCurrent method.
// This is a regression test for the bug where completing the first todo caused a panic
// due to incorrect index calculation.
func TestTodoListCompleteCurrent(t *testing.T) {
	// Create a todo list with 3 items
	todoList := &TodoList{
		Items: []TodoItem{
			{Description: "First todo", Completed: false},
			{Description: "Second todo", Completed: false},
			{Description: "Third todo", Completed: false},
		},
		Current: 0,
	}

	// Test 1: Complete the first todo
	t.Run("complete_first_todo", func(t *testing.T) {
		// Get current todo before completing (this is what the fix does)
		currentTodo := todoList.GetCurrentTodo()
		if currentTodo == nil {
			t.Fatal("GetCurrentTodo returned nil")
		}
		if currentTodo.Description != "First todo" {
			t.Errorf("Expected 'First todo', got '%s'", currentTodo.Description)
		}

		// Complete current
		if !todoList.CompleteCurrent() {
			t.Error("CompleteCurrent returned false")
		}

		// Verify first item is marked complete
		if !todoList.Items[0].Completed {
			t.Error("First todo should be marked complete")
		}

		// Verify completed count
		if todoList.GetCompletedCount() != 1 {
			t.Errorf("Expected 1 completed, got %d", todoList.GetCompletedCount())
		}
	})

	// Test 2: Complete the second todo
	t.Run("complete_second_todo", func(t *testing.T) {
		// Get current todo (should now be second)
		currentTodo := todoList.GetCurrentTodo()
		if currentTodo == nil {
			t.Fatal("GetCurrentTodo returned nil")
		}
		if currentTodo.Description != "Second todo" {
			t.Errorf("Expected 'Second todo', got '%s'", currentTodo.Description)
		}

		// Complete current
		if !todoList.CompleteCurrent() {
			t.Error("CompleteCurrent returned false")
		}

		// Verify second item is marked complete
		if !todoList.Items[1].Completed {
			t.Error("Second todo should be marked complete")
		}
	})

	// Test 3: Complete the third todo
	t.Run("complete_third_todo", func(t *testing.T) {
		if !todoList.CompleteCurrent() {
			t.Error("CompleteCurrent returned false")
		}
		if !todoList.Items[2].Completed {
			t.Error("Third todo should be marked complete")
		}
	})

	// Test 4: Try to complete when all are done
	t.Run("complete_when_all_done", func(t *testing.T) {
		if todoList.CompleteCurrent() {
			t.Error("CompleteCurrent should return false when all todos are complete")
		}
	})
}
