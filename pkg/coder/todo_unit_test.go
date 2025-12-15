package coder

import (
	"testing"
)

func TestTodoList_GetCurrentTodo(t *testing.T) {
	tests := []struct {
		name     string
		todoList *TodoList
		wantNil  bool
		wantDesc string
	}{
		{
			name:     "nil list",
			todoList: nil,
			wantNil:  true,
		},
		{
			name:     "empty list",
			todoList: &TodoList{Items: []TodoItem{}},
			wantNil:  true,
		},
		{
			name: "first incomplete",
			todoList: &TodoList{Items: []TodoItem{
				{Description: "Task 1", Completed: false},
				{Description: "Task 2", Completed: false},
			}},
			wantNil:  false,
			wantDesc: "Task 1",
		},
		{
			name: "second incomplete",
			todoList: &TodoList{Items: []TodoItem{
				{Description: "Task 1", Completed: true},
				{Description: "Task 2", Completed: false},
			}},
			wantNil:  false,
			wantDesc: "Task 2",
		},
		{
			name: "all complete",
			todoList: &TodoList{Items: []TodoItem{
				{Description: "Task 1", Completed: true},
				{Description: "Task 2", Completed: true},
			}},
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.todoList.GetCurrentTodo()
			if tt.wantNil {
				if got != nil {
					t.Errorf("GetCurrentTodo() = %v, want nil", got)
				}
			} else {
				if got == nil {
					t.Errorf("GetCurrentTodo() = nil, want %q", tt.wantDesc)
				} else if got.Description != tt.wantDesc {
					t.Errorf("GetCurrentTodo().Description = %q, want %q", got.Description, tt.wantDesc)
				}
			}
		})
	}
}

func TestTodoList_CompleteCurrent(t *testing.T) {
	tests := []struct {
		name       string
		todoList   *TodoList
		wantResult bool
		wantCount  int // completed count after
	}{
		{
			name:       "nil list",
			todoList:   nil,
			wantResult: false,
			wantCount:  0,
		},
		{
			name:       "empty list",
			todoList:   &TodoList{Items: []TodoItem{}},
			wantResult: false,
			wantCount:  0,
		},
		{
			name: "complete first",
			todoList: &TodoList{Items: []TodoItem{
				{Description: "Task 1", Completed: false},
				{Description: "Task 2", Completed: false},
			}},
			wantResult: true,
			wantCount:  1,
		},
		{
			name: "all already complete",
			todoList: &TodoList{Items: []TodoItem{
				{Description: "Task 1", Completed: true},
			}},
			wantResult: false,
			wantCount:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.todoList.CompleteCurrent()
			if got != tt.wantResult {
				t.Errorf("CompleteCurrent() = %v, want %v", got, tt.wantResult)
			}
			if tt.todoList != nil {
				completedCount := tt.todoList.GetCompletedCount()
				if completedCount != tt.wantCount {
					t.Errorf("GetCompletedCount() = %d, want %d", completedCount, tt.wantCount)
				}
			}
		})
	}
}

func TestTodoList_AddTodo(t *testing.T) {
	tests := []struct {
		name      string
		todoList  *TodoList
		desc      string
		addAfter  int
		wantLen   int
		wantOrder []string
	}{
		{
			name:      "append to end (-1)",
			todoList:  &TodoList{Items: []TodoItem{{Description: "Task 1"}}},
			desc:      "Task 2",
			addAfter:  -1,
			wantLen:   2,
			wantOrder: []string{"Task 1", "Task 2"},
		},
		{
			name:      "insert after first",
			todoList:  &TodoList{Items: []TodoItem{{Description: "Task 1"}, {Description: "Task 3"}}},
			desc:      "Task 2",
			addAfter:  0,
			wantLen:   3,
			wantOrder: []string{"Task 1", "Task 2", "Task 3"},
		},
		{
			name:      "insert to empty list",
			todoList:  &TodoList{Items: []TodoItem{}},
			desc:      "Task 1",
			addAfter:  -1,
			wantLen:   1,
			wantOrder: []string{"Task 1"},
		},
		{
			name:      "out of bounds uses append",
			todoList:  &TodoList{Items: []TodoItem{{Description: "Task 1"}}},
			desc:      "Task 2",
			addAfter:  100,
			wantLen:   2,
			wantOrder: []string{"Task 1", "Task 2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.todoList.AddTodo(tt.desc, tt.addAfter)
			if len(tt.todoList.Items) != tt.wantLen {
				t.Errorf("len(Items) = %d, want %d", len(tt.todoList.Items), tt.wantLen)
			}
			for i, want := range tt.wantOrder {
				if tt.todoList.Items[i].Description != want {
					t.Errorf("Items[%d].Description = %q, want %q", i, tt.todoList.Items[i].Description, want)
				}
			}
		})
	}
}

func TestTodoList_AddTodo_Nil(_ *testing.T) {
	var tl *TodoList
	// Should not panic
	tl.AddTodo("test", -1)
}

func TestTodoList_UpdateTodo(t *testing.T) {
	tests := []struct {
		name       string
		todoList   *TodoList
		index      int
		newDesc    string
		wantResult bool
		wantLen    int
		wantDescs  []string
	}{
		{
			name:       "nil list",
			todoList:   nil,
			index:      0,
			newDesc:    "Updated",
			wantResult: false,
		},
		{
			name:       "invalid index negative",
			todoList:   &TodoList{Items: []TodoItem{{Description: "Task 1"}}},
			index:      -1,
			newDesc:    "Updated",
			wantResult: false,
			wantLen:    1,
			wantDescs:  []string{"Task 1"},
		},
		{
			name:       "invalid index too high",
			todoList:   &TodoList{Items: []TodoItem{{Description: "Task 1"}}},
			index:      5,
			newDesc:    "Updated",
			wantResult: false,
			wantLen:    1,
			wantDescs:  []string{"Task 1"},
		},
		{
			name:       "update description",
			todoList:   &TodoList{Items: []TodoItem{{Description: "Task 1"}}},
			index:      0,
			newDesc:    "Updated Task",
			wantResult: true,
			wantLen:    1,
			wantDescs:  []string{"Updated Task"},
		},
		{
			name:       "remove by empty string",
			todoList:   &TodoList{Items: []TodoItem{{Description: "Task 1"}, {Description: "Task 2"}}},
			index:      0,
			newDesc:    "",
			wantResult: true,
			wantLen:    1,
			wantDescs:  []string{"Task 2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.todoList.UpdateTodo(tt.index, tt.newDesc)
			if got != tt.wantResult {
				t.Errorf("UpdateTodo() = %v, want %v", got, tt.wantResult)
			}
			if tt.todoList != nil && tt.wantLen > 0 {
				if len(tt.todoList.Items) != tt.wantLen {
					t.Errorf("len(Items) = %d, want %d", len(tt.todoList.Items), tt.wantLen)
				}
				for i, want := range tt.wantDescs {
					if tt.todoList.Items[i].Description != want {
						t.Errorf("Items[%d].Description = %q, want %q", i, tt.todoList.Items[i].Description, want)
					}
				}
			}
		})
	}
}

func TestTodoList_AllCompleted(t *testing.T) {
	tests := []struct {
		name     string
		todoList *TodoList
		want     bool
	}{
		{
			name:     "nil list",
			todoList: nil,
			want:     false,
		},
		{
			name:     "empty list",
			todoList: &TodoList{Items: []TodoItem{}},
			want:     false,
		},
		{
			name: "all complete",
			todoList: &TodoList{Items: []TodoItem{
				{Description: "Task 1", Completed: true},
				{Description: "Task 2", Completed: true},
			}},
			want: true,
		},
		{
			name: "some incomplete",
			todoList: &TodoList{Items: []TodoItem{
				{Description: "Task 1", Completed: true},
				{Description: "Task 2", Completed: false},
			}},
			want: false,
		},
		{
			name: "none complete",
			todoList: &TodoList{Items: []TodoItem{
				{Description: "Task 1", Completed: false},
				{Description: "Task 2", Completed: false},
			}},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.todoList.AllCompleted(); got != tt.want {
				t.Errorf("AllCompleted() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTodoList_GetTotalCount(t *testing.T) {
	tests := []struct {
		name     string
		todoList *TodoList
		want     int
	}{
		{
			name:     "nil list",
			todoList: nil,
			want:     0,
		},
		{
			name:     "empty list",
			todoList: &TodoList{Items: []TodoItem{}},
			want:     0,
		},
		{
			name: "two items",
			todoList: &TodoList{Items: []TodoItem{
				{Description: "Task 1"},
				{Description: "Task 2"},
			}},
			want: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.todoList.GetTotalCount(); got != tt.want {
				t.Errorf("GetTotalCount() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTodoList_GetCompletedCount(t *testing.T) {
	tests := []struct {
		name     string
		todoList *TodoList
		want     int
	}{
		{
			name:     "nil list",
			todoList: nil,
			want:     0,
		},
		{
			name: "mixed completion",
			todoList: &TodoList{Items: []TodoItem{
				{Description: "Task 1", Completed: true},
				{Description: "Task 2", Completed: false},
				{Description: "Task 3", Completed: true},
			}},
			want: 2,
		},
		{
			name: "none complete",
			todoList: &TodoList{Items: []TodoItem{
				{Description: "Task 1", Completed: false},
			}},
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.todoList.GetCompletedCount(); got != tt.want {
				t.Errorf("GetCompletedCount() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetTodoListStatus(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(c *Coder)
		contains []string
	}{
		{
			name: "nil todo list",
			setup: func(c *Coder) {
				c.todoList = nil
			},
			contains: []string{"No todo list available"},
		},
		{
			name: "empty todo list",
			setup: func(c *Coder) {
				c.todoList = &TodoList{Items: []TodoItem{}}
			},
			contains: []string{"No todo list available"},
		},
		{
			name: "with current and completed",
			setup: func(c *Coder) {
				c.todoList = &TodoList{
					Items: []TodoItem{
						{Description: "Done task", Completed: true},
						{Description: "Current task", Completed: false},
						{Description: "Future task", Completed: false},
					},
					Current: 1,
				}
			},
			contains: []string{"Current Todo", "Done task", "Current task", "Remaining"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Coder{}
			tt.setup(c)
			result := c.getTodoListStatus()
			for _, want := range tt.contains {
				if !containsString(result, want) {
					t.Errorf("getTodoListStatus() missing %q in result: %s", want, result)
				}
			}
		})
	}
}

// containsString is a simple substring check for test assertions.
func containsString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
