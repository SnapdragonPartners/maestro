# Todo List Feature - Implementation Status

## Overview

The todo list feature provides structured progress tracking for coder agents by breaking approved plans into small, atomic steps with clear completion criteria.

## Completed Work

### ✅ Core Data Structures (`pkg/coder/todo.go`)

Created complete `TodoList` and `TodoItem` types with methods:
- `GetCurrentTodo()` - Returns next incomplete todo
- `CompleteCurrent()` - Marks current as done, advances to next
- `AddTodo(description, addAfter)` - Insert todo at position
- `UpdateTodo(index, newDescription)` - Modify or remove (empty string = remove)
- `AllCompleted()` - Check if all todos done
- `GetCompletedCount()`, `GetTotalCount()` - Progress tracking

### ✅ State Management

- Added `todoList *TodoList` field to `Coder` struct in `pkg/coder/driver.go:66`
- Created comprehensive handlers in `pkg/coder/todo_handlers.go`:
  - `handleTodosSubmit()` - Initialize todo list from tool result
  - `handleTodoComplete()` - Mark current todo complete
  - `handleTodoAdd()` - Add new todo with insertion support
  - `handleTodoUpdate()` - Modify or remove todo by index
  - `getTodoListStatus()` - Format status for templates
  - `loadTodoListFromState()` - Restore after restart
  - `processCodingToolCallsWithTodos()` - Process todo tools in CODING state

### ✅ MCP Tools (`pkg/tools/todo_tools.go`)

Four new tools following `{object}_{verb}` naming convention:

1. **todos_submit** - Submit 3-10 atomic todos after plan approval
   - Used once in PLAN_REVIEW after architect approval
   - Validates 3-10 items, each non-empty string

2. **todo_complete** - Mark current todo complete (no parameters)
   - Advances to next incomplete todo automatically
   - Used in CODING state

3. **todo_add** - Add new todo during implementation
   - Parameters: `description` (required), `add_after` (optional, -1 = append)
   - For discovered work during coding

4. **todo_update** - Modify or remove todo by index
   - Parameters: `index` (required), `description` (required, empty = remove)
   - For adjusting plan mid-implementation

### ✅ Tool Registration

- Added tool constants to `pkg/tools/constants.go`:
  - `ToolTodosSubmit`, `ToolTodoComplete`, `ToolTodoAdd`, `ToolTodoUpdate`

- Registered in `pkg/tools/registry.go:555-577`:
  - Factory functions: `createTodosSubmitTool`, `createTodoCompleteTool`, etc.
  - Schema getters: `getTodosSubmitSchema`, `getTodoCompleteSchema`, etc.
  - Metadata with descriptions

- Added to tool lists in `pkg/tools/constants.go`:
  - `AppCodingTools`: `ToolTodoComplete`, `ToolTodoAdd`, `ToolTodoUpdate`
  - `DevOpsCodingTools`: `ToolTodoComplete`, `ToolTodoAdd`, `ToolTodoUpdate`

Note: `ToolTodosSubmit` needs to be added to PLAN_REVIEW tool lists (see Remaining Work).

## Remaining Work

### 🚧 State Machine Integration

#### 1. PLAN_REVIEW State - Todo Collection

After architect approves the plan, PLAN_REVIEW needs to:

**Current behavior**:
```go
// plan_review.go:95-121
case proto.ApprovalTypePlan:
    // Configure container and proceed directly to CODING
    return StateCoding, false, nil
```

**Required behavior**:
```go
case proto.ApprovalTypePlan:
    // Check if we already have todos
    if c.todoList != nil && len(c.todoList.Items) > 0 {
        // Already have todos, proceed to coding
        return c.transitionToCodingWithContainer(ctx, sm)
    }

    // Need to collect todos - make LLM call
    return c.requestTodosFromCoder(ctx, sm)
```

**New method needed**: `requestTodosFromCoder()`
- Renders a prompt asking coder to call `todos_submit` tool
- Makes LLM call with tools including `ToolTodosSubmit`
- Processes tool result via `handleTodosSubmit()`
- Transitions to CODING once todos received

**Key files to modify**:
- `pkg/coder/plan_review.go:95-121` (handlePlanReviewApproval)
- Create new method `requestTodosFromCoder()` in plan_review.go

#### 2. Create PLAN_REVIEW Tool Provider

PLAN_REVIEW currently doesn't have tools. Need to:

**Add tool lists** in `pkg/tools/constants.go`:
```go
// New constants for PLAN_REVIEW tools
PlanReviewTools = []string{
    ToolTodosSubmit,  // Only tool needed in this state
}
```

**Create tool provider** in `pkg/coder/plan_review.go`:
- Similar to how CODING creates `c.codingToolProvider`
- Store as `c.planReviewToolProvider` (add field to Coder struct)

#### 3. CODING State - Todo Tool Integration

Wire up todo handlers in CODING state:

**Modify** `pkg/coder/coding.go` to check for todo tools:
```go
// In processCodingToolCalls(), before standard tool processing:
switch toolCall.Name {
case tools.ToolTodoComplete:
    if err := c.handleTodoComplete(sm); err != nil {
        // Add error to context
    }
    continue
case tools.ToolTodoAdd:
    description := utils.GetMapFieldOr[string](toolCall.Parameters, "description", "")
    addAfter := utils.GetMapFieldOr[int](toolCall.Parameters, "add_after", -1)
    c.handleTodoAdd(sm, description, addAfter)
    continue
case tools.ToolTodoUpdate:
    index := utils.GetMapFieldOr[int](toolCall.Parameters, "index", -1)
    description := utils.GetMapFieldOr[string](toolCall.Parameters, "description", "")
    if err := c.handleTodoUpdate(sm, index, description); err != nil {
        // Add error to context
    }
    continue
}
```

**Alternatively**, replace `processCodingToolCalls` with `processCodingToolCallsWithTodos` (already created in todo_handlers.go, but needs completion for non-todo tools).

### 🚧 Template Updates

#### 1. PLAN_REVIEW Todo Collection Prompt

Create new template or template section for requesting todos.

**Option A**: New template file `pkg/templates/plan_review_todos.tpl.md`
```markdown
# Todo List Generation

Your implementation plan has been approved by the architect. Now break it into atomic implementation steps.

## Requirements

Generate approximately 3-10 atomic todos with clear completion criteria.

Each todo should:
- Be completable in 1-3 coding turns
- Have clear completion criteria (you know when it's done)
- Start with an action verb (Create, Implement, Add, Configure, etc.)

## Examples

Good todos:
- "Create User model with authentication fields"
- "Set up database connection and run migrations"
- "Implement JWT token generation and validation"
- "Add error handling middleware to Express app"

Bad todos:
- "Work on the backend" (too vague)
- "Fix bugs" (no clear completion)
- "Update everything" (not atomic)

Use the `todos_submit` tool with an array of todo descriptions.
```

**Option B**: Add section to existing `budget_review_coding.tpl.md` (simpler)

#### 2. CODING Templates - Show Todo Status

Update `pkg/templates/app_coding.tpl.md` and `devops_coding.tpl.md` to show current todo.

**Add near top of template** (after "Your role" section):
```markdown
## Implementation Progress

{{if .TodoList}}
**Current Todo** ({{.CurrentTodoIndex}}/{{.TotalTodos}}): {{.CurrentTodoDescription}}

**Completed** ({{.CompletedCount}}):
{{range .CompletedTodos}}- ✅ {{.}}
{{end}}

**Remaining** ({{.RemainingCount}}):
{{range .RemainingTodos}}- ⏸️  {{.}}
{{end}}

Use `todo_complete` when you finish the current todo. Use `todo_add` to add newly discovered work. Use `todo_update` to modify the plan if needed.
{{else}}
No todo list available - using freeform implementation.
{{end}}
```

**Wire up template data** in `pkg/coder/coding.go`:
- Call `c.getTodoListStatus()` to get formatted status
- Parse into template variables
- Pass to template renderer

#### 3. All-Complete Reflection Prompt

When `todoList.AllCompleted() == true`, add reflection prompt:

```markdown
## All Todos Complete

You've completed all todos in your implementation plan:
{{range .AllTodos}}- ✅ {{.}}
{{end}}

Before calling `done`:
1. Did you discover any additional work needed? If yes, use `todo_add` to add it.
2. Are all requirements from the original story satisfied?
3. Is the implementation ready for testing?

If everything is complete, call the `done` tool. Otherwise, add remaining todos.
```

### 🚧 State Persistence

Todo list is already saved to state data (`sm.SetStateData("todo_list", c.todoList)`), but needs:

1. **Restore on restart**: Call `c.loadTodoListFromState(sm)` in:
   - `handleSetup()` or `Initialize()` when restoring agent state
   - After state machine loads persisted data

2. **Test restart behavior**: Verify todo list survives agent restart

### 🧪 Testing

1. **Unit tests** for todo operations:
   - `pkg/coder/todo_test.go` - Test TodoList methods
   - `pkg/coder/todo_handlers_test.go` - Test handlers

2. **Integration test**:
   - Create simple test story
   - Verify plan approval → todo collection → coding with todos → completion
   - Check that files only written when needed (not 11 times!)

3. **Edge cases**:
   - Empty todo list (should reprompt once, then ERROR)
   - Invalid todo list (< 3 or > 10 items)
   - Agent restart mid-implementation (todos persist)
   - All todos complete but story not done (reflection prompt works)

## Architecture Decisions

### Why Tool Handlers, Not Effects?

- **Effects** are for inter-agent communication (architect ↔ coder)
- **Tool handlers** are for agent's own state management
- Todos are coder's local state, not shared with architect
- Therefore: tool handlers, not effects

### Why todos_submit in PLAN_REVIEW, not PLANNING?

- PLANNING submits the conceptual approach (`submit_plan`)
- Architect reviews and approves the approach
- PLAN_REVIEW collects implementation todos AFTER approval
- This separates "what to build" (plan) from "how to build it step-by-step" (todos)

### Why Not Put Todos in submit_plan?

Original `submit_plan` tool had a `todos` field, but we removed it because:
1. The architect reviews the plan, not the todos
2. Todos are implementation details, not conceptual design
3. Separating them allows the coder to adjust todos based on architect feedback without resubmitting the plan

## Open Questions

1. **Should todos be visible to architect during budget review?**
   - Pro: Helps architect understand where coder is stuck
   - Con: Adds noise to budget review context
   - Current spec: Not included in budget review payload

2. **What if coder never calls todos_submit?**
   - Current spec: Reprompt once, if still no todos → ERROR state
   - Implementation: Track attempt count in state data

3. **Should we enforce todo completion order?**
   - Current: No enforcement, coder can complete in any order
   - Alternative: Only allow completing current todo
   - Decision: Current approach is more flexible

## Next Steps

Recommended implementation order:

1. **Add PLAN_REVIEW tool support** (infrastructure)
   - Create tool list constant
   - Add tool provider field to Coder
   - Create `requestTodosFromCoder()` method

2. **Wire up PLAN_REVIEW todo collection** (critical path)
   - Modify `handlePlanReviewApproval()`
   - Create todo collection prompt
   - Handle `todos_submit` tool result

3. **Wire up CODING todo handlers** (critical path)
   - Modify `processCodingToolCalls()` to handle todo tools
   - Add tool results to context

4. **Update templates** (UX improvement)
   - Add todo status to CODING templates
   - Add reflection prompt for all-complete case

5. **Test end-to-end** (validation)
   - Simple story test
   - Verify no redundant file writes
   - Check restart behavior

## Files to Modify

### Critical Path (must do):
- `pkg/coder/plan_review.go` - Todo collection flow
- `pkg/coder/coding.go` - Todo tool handling
- `pkg/tools/constants.go` - PLAN_REVIEW tool list
- `pkg/templates/app_coding.tpl.md` - Show todo status
- `pkg/templates/devops_coding.tpl.md` - Show todo status

### Nice to Have:
- Create `pkg/templates/plan_review_todos.tpl.md` - Dedicated prompt
- Create `pkg/coder/todo_test.go` - Unit tests
- Update `pkg/templates/budget_review_coding.tpl.md` - Include todos optionally

## Success Metrics

The feature will be successful when:
1. ✅ Coder generates 3-10 todos after plan approval
2. ✅ CODING template shows current todo clearly
3. ✅ Coder completes todos sequentially without redundant work
4. ✅ Main file (e.g., main.go) written 1-2 times max (not 11!)
5. ✅ Reflection prompt triggers when all todos complete
6. ✅ Todo list survives agent restart
