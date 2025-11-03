# Todo List Feature - Final Decisions & Implementation

## Architectural Decisions Made

### 1. Tool Handlers, Not Effects ✅
**Decision**: Use tool handlers for todo operations
**Rationale**:
- Effects are for inter-agent communication (architect ↔ coder)
- Tool handlers manage agent's own local state
- Todos are coder's internal checklist, not shared with architect

### 2. Todo Visibility in Budget Review ✅
**Decision**: Don't explicitly include todos in budget review payload
**Rationale**: Architect sees recent turns (which includes current todo naturally), avoiding token overhead

### 3. Missing todos_add Handling ✅
**Decision**: Reprompt once, then ERROR if still no todos
**Rationale**: Enforces structure while allowing one mistake

### 4. Flexible Todo Completion ✅
**Decision**: Allow completing any todo by index, default to current if omitted
**Implementation**:
- `todo_complete` has optional `index` parameter
- Omit index → completes current todo (most common)
- Specify index → completes that todo (for out-of-order work)

### 5. Tool Validation ✅
**Decision**: Tool-level validation is sufficient
**Rationale**: Clear error returned to LLM, which can fix and retry

### 6. Simplified Tool Set: 3 Tools Instead of 4 ✅
**Decision**: Eliminate separate `todo_add`, use `todos_add` for both initial and append
**Final Tool Set**:
1. **todos_add** - Add 1-20 todos (initial or additional)
2. **todo_complete** - Mark todo complete (current or by index)
3. **todo_update** - Modify or remove todo by index

**Rationale**:
- Reduces cognitive load (3 vs 4 tools)
- Natural pattern: "todos_add is how you add todos" (whether initial or later)
- Handler detects initial vs append automatically
- Validation: 1-20 items (flexible, with prompt guidance for 3-10)

### 7. Validation Limits ✅
**Decision**: 1-20 items (hard limit), 3-10 recommended (prompt guidance)
**Rationale**:
- Wiggle room both ways
- Single todo additions allowed (1 item)
- Bulk initial submission encouraged (3-10)
- Maximum prevents abuse (20)

### 8. Append-Only Operation ✅
**Decision**: New todos always appended to end of list
**Trade-off**: Can't insert in middle, but keeps implementation simple
**Mitigation**: Use `todo_update` to reorder if really needed
**Rationale**: Under-engineer rather than over-engineer

## Final Implementation Summary

### ✅ Completed Infrastructure

**Data Structures** (`pkg/coder/todo.go`):
- `TodoList` and `TodoItem` with full CRUD
- Methods: `GetCurrentTodo()`, `CompleteCurrent()`, `AllCompleted()`, etc.

**MCP Tools** (`pkg/tools/todo_tools.go`):
- `TodosAddTool` - 1-20 items, initial or append
- `TodoCompleteTool` - Optional index parameter
- `TodoUpdateTool` - Modify or remove by index

**Tool Registration** (`pkg/tools/registry.go`, `constants.go`):
- All 3 tools registered with proper metadata
- Added to `AppCodingTools` and `DevOpsCodingTools` lists
- Constants: `ToolTodosAdd`, `ToolTodoComplete`, `ToolTodoUpdate`

**Handlers** (`pkg/coder/todo_handlers.go`):
- `handleTodosAdd()` - Initializes or appends
- `handleTodoComplete(index)` - Completes current or specified
- `handleTodoUpdate()` - Modifies or removes
- `getTodoListStatus()` - Formats for templates
- `loadTodoListFromState()` - Restores after restart
- `processCodingToolCallsWithTodos()` - Processes todo tools

**State Management**:
- `todoList *TodoList` field in `Coder` struct
- Persisted to state data on every change
- Restored on agent restart

### 🚧 Remaining Integration Work

See `TODO_LISTS_IMPLEMENTATION_STATUS.md` for detailed breakdown:

1. **PLAN_REVIEW Integration**: Make LLM call after plan approval to collect todos via `todos_add`
2. **CODING Integration**: Wire up todo tool handlers in CODING state
3. **Templates**: Show current todo status in CODING templates
4. **Testing**: End-to-end validation

## Code Quality

**Build Status**: ✅ Clean compilation
- Only unused function warnings (expected until wired up)
- No syntax errors
- No import errors
- Linting passes (except unused warnings)

**Type Safety**: ✅ All handlers properly typed
**Error Handling**: ✅ Comprehensive validation and error messages
**Logging**: ✅ Informative log messages at all key points
**Documentation**: ✅ Comments explain all public APIs

## Key Implementation Details

### todos_add: Initial vs Append Logic

```go
if c.todoList == nil {
    // Initial submission
    c.todoList = &TodoList{Items: newTodos, Current: 0}
} else {
    // Append to existing
    c.todoList.Items = append(c.todoList.Items, newTodos...)
}
```

### todo_complete: Current vs Indexed

```go
if index == -1 {
    // Complete current (finds first incomplete)
    c.todoList.CompleteCurrent()
} else {
    // Complete specific index
    c.todoList.Items[index].Completed = true
}
```

### Tool Validation: Flexible Limits

```go
// Hard validation: 1-20 items
if len(todosArray) < 1 || len(todosArray) > 20 {
    return error
}

// Prompt guidance: "Recommended: 3-10 items for initial list"
```

## Success Criteria

The implementation will be considered successful when:

1. ✅ Foundation complete: Types, tools, handlers, registration
2. ⏳ Coder generates 3-10 todos after plan approval
3. ⏳ CODING template shows current todo clearly
4. ⏳ Coder completes todos sequentially without redundant work
5. ⏳ Main file written 1-2 times max (not 11!)
6. ⏳ Reflection prompt triggers when all todos complete
7. ⏳ Todo list survives agent restart

**Status**: 1/7 complete (foundation done, integration pending)

## Next Steps

1. Wire up PLAN_REVIEW to request todos after approval
2. Wire up CODING to process todo tool calls
3. Update templates to show todo status
4. Test end-to-end with real story

See `TODO_LISTS_IMPLEMENTATION_STATUS.md` for detailed implementation plan.
