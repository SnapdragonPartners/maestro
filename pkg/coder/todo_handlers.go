package coder

import (
	"fmt"
	"strings"
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
		status += "**Completed**:\n" + strings.Join(completed, "\n") + "\n\n"
	}

	// Show remaining todos
	remaining := []string{}
	for i := range c.todoList.Items {
		if !c.todoList.Items[i].Completed {
			remaining = append(remaining, fmt.Sprintf("- ⏸️  %s", c.todoList.Items[i].Description))
		}
	}
	if len(remaining) > 0 {
		status += "**Remaining**:\n" + strings.Join(remaining, "\n") + "\n"
	}

	return status
}
