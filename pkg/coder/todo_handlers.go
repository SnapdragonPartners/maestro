package coder

import (
	"fmt"
)

// getTodoListStatus returns a formatted string showing current todo status.
// Used by templates to display current todo in CODING state.
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

// joinStrings joins strings (helper for getTodoListStatus).
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
