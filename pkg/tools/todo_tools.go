package tools

import (
	"context"
	"fmt"
)

// TodosAddTool adds todos to the implementation list (initial or additional).
type TodosAddTool struct{}

// NewTodosAddTool creates a new add todos tool instance.
func NewTodosAddTool() *TodosAddTool {
	return &TodosAddTool{}
}

// Definition returns the tool's definition in Claude API format.
func (t *TodosAddTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "todos_add",
		Description: "Add todos to implementation list (initial submission or additional todos discovered during work). Recommended: 3-10 items for initial list. IMPORTANT: The 'todos' parameter MUST be a non-empty array of strings.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"todos": {
					Type:        "array",
					Description: "List of todo items, where each element is a single string representing one task. Each todo should start with an action verb (e.g., 'Create', 'Implement', 'Add', 'Update') and have clear completion criteria. Example: [\"Create main.go with basic structure\", \"Implement HTTP server setup\", \"Add error handling\"]",
					Items: &Property{
						Type: "string",
					},
					MinItems: &[]int{1}[0],
					MaxItems: &[]int{20}[0],
				},
			},
			Required: []string{"todos"},
		},
	}
}

// Name returns the tool identifier.
func (t *TodosAddTool) Name() string {
	return "todos_add"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (t *TodosAddTool) PromptDocumentation() string {
	return `- **todos_add** - Add todos to implementation list
  - Parameters: todos (array of 1-20 strings, REQUIRED and must be non-empty)
  - Use after plan approval for initial list (recommended 3-10 items)
  - Use during coding to add newly discovered work
  - Each todo should start with action verb and have clear completion criteria
  - Todos are appended to existing list
  - Example: todos_add({"todos": ["Create main.go with basic structure", "Implement HTTP server setup", "Add error handling"]})`
}

// Exec executes the add todos operation.
func (t *TodosAddTool) Exec(_ context.Context, args map[string]any) (any, error) {
	todos, ok := args["todos"]
	if !ok {
		return nil, fmt.Errorf("todos parameter is required")
	}

	todosArray, ok := todos.([]any)
	if !ok {
		return nil, fmt.Errorf("todos must be an array")
	}

	if len(todosArray) < 1 || len(todosArray) > 20 {
		return nil, fmt.Errorf("todos must contain 1-20 items (got %d)", len(todosArray))
	}

	// Validate and convert todos
	validatedTodos := make([]string, len(todosArray))
	for i, todoItem := range todosArray {
		todoStr, ok := todoItem.(string)
		if !ok {
			return nil, fmt.Errorf("todo item %d must be a string", i)
		}
		if todoStr == "" {
			return nil, fmt.Errorf("todo item %d cannot be empty", i)
		}
		validatedTodos[i] = todoStr
	}

	return map[string]any{
		"success": true,
		"message": "Todos added successfully",
		"todos":   validatedTodos,
	}, nil
}

// TodoCompleteTool marks a todo as complete (current by default, or specified by index).
type TodoCompleteTool struct{}

// NewTodoCompleteTool creates a new todo complete tool instance.
func NewTodoCompleteTool() *TodoCompleteTool {
	return &TodoCompleteTool{}
}

// Definition returns the tool's definition in Claude API format.
func (t *TodoCompleteTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "todo_complete",
		Description: "Mark a todo as complete (current todo by default, or specify index for out-of-order completion)",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"index": {
					Type:        "integer",
					Description: "Optional: 0-based index of todo to complete (omit to complete current todo)",
				},
			},
			Required: []string{},
		},
	}
}

// Name returns the tool identifier.
func (t *TodoCompleteTool) Name() string {
	return "todo_complete"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (t *TodoCompleteTool) PromptDocumentation() string {
	return `- **todo_complete** - Mark todo as complete
  - Parameters: index (optional, 0-based)
  - Omit index to complete current todo (most common)
  - Specify index for out-of-order completion if needed
  - Automatically advances to next incomplete todo`
}

// Exec executes the todo complete operation.
func (t *TodoCompleteTool) Exec(_ context.Context, args map[string]any) (any, error) {
	// Extract optional index
	index := -1 // -1 means "current"
	if indexVal, hasIndex := args["index"]; hasIndex {
		switch v := indexVal.(type) {
		case int:
			index = v
		case float64:
			index = int(v)
		default:
			return nil, fmt.Errorf("index must be an integer")
		}
	}

	return map[string]any{
		"success": true,
		"message": "Todo marked complete",
		"index":   index,
	}, nil
}

// TodoUpdateTool modifies or removes an existing todo.
type TodoUpdateTool struct{}

// NewTodoUpdateTool creates a new todo update tool instance.
func NewTodoUpdateTool() *TodoUpdateTool {
	return &TodoUpdateTool{}
}

// Definition returns the tool's definition in Claude API format.
func (t *TodoUpdateTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "todo_update",
		Description: "Update or remove a todo by index",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"index": {
					Type:        "integer",
					Description: "0-based index of todo to update",
				},
				"description": {
					Type:        "string",
					Description: "New description (empty string to remove todo)",
				},
			},
			Required: []string{"index", "description"},
		},
	}
}

// Name returns the tool identifier.
func (t *TodoUpdateTool) Name() string {
	return "todo_update"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (t *TodoUpdateTool) PromptDocumentation() string {
	return `- **todo_update** - Update or remove a todo
  - Parameters: index (required), description (required, empty string removes)
  - Use to modify todos if requirements change
  - Changes are logged for debugging`
}

// Exec executes the todo update operation.
func (t *TodoUpdateTool) Exec(_ context.Context, args map[string]any) (any, error) {
	indexVal, ok := args["index"]
	if !ok {
		return nil, fmt.Errorf("index parameter is required")
	}

	var index int
	switch v := indexVal.(type) {
	case int:
		index = v
	case float64:
		index = int(v)
	default:
		return nil, fmt.Errorf("index must be an integer")
	}

	description, ok := args["description"]
	if !ok {
		return nil, fmt.Errorf("description parameter is required")
	}

	descStr, ok := description.(string)
	if !ok {
		return nil, fmt.Errorf("description must be a string")
	}

	action := "updated"
	if descStr == "" {
		action = "removed"
	}

	return map[string]any{
		"success":     true,
		"message":     fmt.Sprintf("Todo %s successfully", action),
		"index":       index,
		"description": descStr,
	}, nil
}
