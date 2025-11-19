package coder

import (
	"fmt"

	"orchestrator/pkg/agent"
)

// handleTodoComplete marks a todo as complete (current by default, or specified by index).
func (c *Coder) handleTodoComplete(sm *agent.BaseStateMachine, index int) error {
	if c.todoList == nil {
		return fmt.Errorf("no todo list initialized")
	}

	if index == -1 {
		// Complete current todo - get it BEFORE marking complete
		currentTodo := c.todoList.GetCurrentTodo()
		if currentTodo == nil {
			return fmt.Errorf("no incomplete todos to complete")
		}

		// Store description before marking complete (for logging)
		todoDescription := currentTodo.Description
		todoIndex := c.todoList.Current // Store current index for logging

		// Now mark it complete
		if !c.todoList.CompleteCurrent() {
			// This should not happen since GetCurrentTodo() just returned non-nil
			return fmt.Errorf("failed to complete current todo")
		}

		c.logger.Info("📋 [TODO] ✅ Completed [%d/%d]: %s",
			c.todoList.GetCompletedCount(),
			c.todoList.GetTotalCount(),
			todoDescription)
		c.logger.Info("📋 [TODO] Completed todo at index %d (Current is now %d)", todoIndex, c.todoList.Current)
	} else {
		// Complete specific todo by index
		if index < 0 || index >= len(c.todoList.Items) {
			return fmt.Errorf("invalid todo index: %d (valid range: 0-%d)", index, len(c.todoList.Items)-1)
		}
		if c.todoList.Items[index].Completed {
			return fmt.Errorf("todo at index %d already completed", index)
		}
		c.todoList.Items[index].Completed = true
		c.logger.Info("📋 [TODO] ✅ Completed [%d]: %s", index+1, c.todoList.Items[index].Description)
	}

	// Persist updated todo list
	sm.SetStateData("todo_list", c.todoList)

	return nil
}

// handleTodoUpdate updates or removes a todo by index.
func (c *Coder) handleTodoUpdate(sm *agent.BaseStateMachine, index int, newDescription string) error {
	if c.todoList == nil {
		return fmt.Errorf("no todo list initialized")
	}

	// Store old description for logging
	oldDescription := ""
	if index >= 0 && index < len(c.todoList.Items) {
		oldDescription = c.todoList.Items[index].Description
	}

	if !c.todoList.UpdateTodo(index, newDescription) {
		return fmt.Errorf("invalid todo index: %d", index)
	}

	// Log the update/removal
	if newDescription == "" {
		c.logger.Info("📋 [TODO] ❌ Removed [%d]: %s", index+1, oldDescription)
	} else {
		c.logger.Info("📋 [TODO] ✏️  Updated [%d]: %s → %s", index+1, oldDescription, newDescription)
	}

	// Persist updated todo list
	sm.SetStateData("todo_list", c.todoList)

	return nil
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
