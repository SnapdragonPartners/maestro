package coder

// TodoList tracks implementation progress through a checklist of atomic tasks.
type TodoList struct {
	Items   []TodoItem `json:"items"`   // List of todo items
	Current int        `json:"current"` // Index of current todo (0-based)
}

// TodoItem represents a single task in the todo list.
type TodoItem struct {
	Description string `json:"description"` // Task description (e.g., "Create User model with auth fields")
	Completed   bool   `json:"completed"`   // Whether this task is complete
}

// GetCurrentTodo returns the current (next incomplete) todo item, or nil if all complete.
func (tl *TodoList) GetCurrentTodo() *TodoItem {
	if tl == nil || len(tl.Items) == 0 {
		return nil
	}

	// Find the first incomplete todo
	for i := range tl.Items {
		if !tl.Items[i].Completed {
			tl.Current = i
			return &tl.Items[i]
		}
	}

	// All items completed
	return nil
}

// CompleteCurrent marks the current todo as complete and advances to next.
func (tl *TodoList) CompleteCurrent() bool {
	if tl == nil || len(tl.Items) == 0 {
		return false
	}

	current := tl.GetCurrentTodo()
	if current == nil {
		return false // Nothing to complete
	}

	tl.Items[tl.Current].Completed = true
	return true
}

// AddTodo adds a new todo item at the specified position.
// If addAfter is -1, appends to the end.
// If addAfter is >= 0, inserts after that index.
func (tl *TodoList) AddTodo(description string, addAfter int) {
	if tl == nil {
		return
	}

	newItem := TodoItem{
		Description: description,
		Completed:   false,
	}

	// Append to end if addAfter is -1 or out of bounds
	if addAfter < 0 || addAfter >= len(tl.Items) {
		tl.Items = append(tl.Items, newItem)
		return
	}

	// Insert after addAfter index
	insertPos := addAfter + 1
	tl.Items = append(tl.Items[:insertPos], append([]TodoItem{newItem}, tl.Items[insertPos:]...)...)
}

// UpdateTodo modifies or removes a todo by index.
// If newDescription is empty, removes the todo.
func (tl *TodoList) UpdateTodo(index int, newDescription string) bool {
	if tl == nil || index < 0 || index >= len(tl.Items) {
		return false
	}

	if newDescription == "" {
		// Remove the todo
		tl.Items = append(tl.Items[:index], tl.Items[index+1:]...)
		// Adjust current index if needed
		if tl.Current >= len(tl.Items) {
			tl.Current = len(tl.Items) - 1
		}
		if tl.Current < 0 {
			tl.Current = 0
		}
		return true
	}

	// Update the description
	tl.Items[index].Description = newDescription
	return true
}

// AllCompleted returns true if all todos are marked complete.
func (tl *TodoList) AllCompleted() bool {
	if tl == nil || len(tl.Items) == 0 {
		return false
	}

	for i := range tl.Items {
		if !tl.Items[i].Completed {
			return false
		}
	}

	return true
}

// GetCompletedCount returns the number of completed todos.
func (tl *TodoList) GetCompletedCount() int {
	if tl == nil {
		return 0
	}

	count := 0
	for i := range tl.Items {
		if tl.Items[i].Completed {
			count++
		}
	}

	return count
}

// GetTotalCount returns the total number of todos.
func (tl *TodoList) GetTotalCount() int {
	if tl == nil {
		return 0
	}
	return len(tl.Items)
}
