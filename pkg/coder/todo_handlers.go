package coder

import (
	"context"
	"fmt"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/proto"
)

// handleTodosAdd processes todos_add tool results (initial or append).
//
//nolint:unparam // bool parameter follows handler signature convention
func (c *Coder) handleTodosAdd(_ context.Context, sm *agent.BaseStateMachine, resultMap map[string]any) (proto.State, bool, error) {
	// The tool returns map[string]any{"todos": []string{...}} from validation
	// We need to handle both []string (from tool validation) and []any (from raw parameters)
	todosRaw, ok := resultMap["todos"]
	if !ok {
		return proto.StateError, false, fmt.Errorf("todos parameter missing from result")
	}

	// Try to extract as []string first (from tool validation), fallback to []any
	var todoStrings []string
	switch v := todosRaw.(type) {
	case []string:
		todoStrings = v
	case []any:
		// Fallback to []any and convert
		todoStrings = make([]string, 0, len(v))
		for i, todoAny := range v {
			todoStr, ok := todoAny.(string)
			if !ok {
				return proto.StateError, false, fmt.Errorf("todo item %d is not a string", i)
			}
			todoStrings = append(todoStrings, todoStr)
		}
	default:
		return proto.StateError, false, fmt.Errorf("todos must be []string or []any, got %T", todosRaw)
	}

	if len(todoStrings) == 0 {
		return proto.StateError, false, fmt.Errorf("todos_add returned empty list")
	}

	// Convert string todos to TodoItem structs
	newTodos := make([]TodoItem, len(todoStrings))
	for i, todoStr := range todoStrings {
		newTodos[i] = TodoItem{
			Description: todoStr,
			Completed:   false,
		}
	}

	if c.todoList == nil {
		// Initial submission
		c.todoList = &TodoList{
			Items:   newTodos,
			Current: 0,
		}
		c.logger.Info("📋 [TODO] Initialized todo list with %d items", len(newTodos))
		// Log each todo for easy grepping
		for i, todo := range newTodos {
			c.logger.Info("📋 [TODO] [%d/%d] %s", i+1, len(newTodos), todo.Description)
		}
	} else {
		// Append to existing list
		oldCount := len(c.todoList.Items)
		c.todoList.Items = append(c.todoList.Items, newTodos...)
		c.logger.Info("📋 [TODO] Added %d todos (now %d total)", len(newTodos), len(c.todoList.Items))
		// Log each new todo
		for i, todo := range newTodos {
			c.logger.Info("📋 [TODO] [%d/%d] %s", oldCount+i+1, len(c.todoList.Items), todo.Description)
		}
	}

	// Store in state data for persistence
	sm.SetStateData("todo_list", c.todoList)

	// Continue from where we left off (plan was already approved)
	return StateCoding, false, nil
}

// getTodoListStatus returns a formatted string showing current todo status.
// Used by templates to display current todo in CODING state.
//
//nolint:unused // Will be used in templates next
func (c *Coder) getTodoListStatus() string {
	if c.todoList == nil || len(c.todoList.Items) == 0 {
		return "No todo list available"
	}

	var status string
	current := c.todoList.GetCurrentTodo()

	if current != nil {
		status = fmt.Sprintf("**Current Todo** (%d/%d): %s\n\n",
			c.todoList.GetCompletedCount()+1,
			c.todoList.GetTotalCount(),
			current.Description)
	}

	// Show completed todos
	completed := []string{}
	for i := range c.todoList.Items {
		if c.todoList.Items[i].Completed {
			completed = append(completed, fmt.Sprintf("- ✅ %s", c.todoList.Items[i].Description))
		}
	}
	if len(completed) > 0 {
		status += "**Completed**:\n" + joinStrings(completed, "\n") + "\n\n"
	}

	// Show remaining todos
	remaining := []string{}
	for i := range c.todoList.Items {
		if !c.todoList.Items[i].Completed {
			remaining = append(remaining, fmt.Sprintf("- ⏸️  %s", c.todoList.Items[i].Description))
		}
	}
	if len(remaining) > 0 {
		status += "**Remaining**:\n" + joinStrings(remaining, "\n") + "\n"
	}

	return status
}

// loadTodoListFromState loads the todo list from state data (for restarts).
// Called during agent initialization to restore todo list after restart.
//
//nolint:unused // Will be used in agent initialization next
func (c *Coder) loadTodoListFromState(sm *agent.BaseStateMachine) {
	if todoData, exists := sm.GetStateValue("todo_list"); exists {
		if todoList, ok := todoData.(*TodoList); ok {
			c.todoList = todoList
			c.logger.Info("📋 [TODO] Restored todo list from state: %d items, %d completed",
				c.todoList.GetTotalCount(),
				c.todoList.GetCompletedCount())
		}
	}
}

// joinStrings joins strings (helper for getTodoListStatus).
//
//nolint:unused // Used by getTodoListStatus
func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	result := strs[0]
	for i := 1; i < len(strs); i++ {
		result += sep + strs[i]
	}
	return result
}
