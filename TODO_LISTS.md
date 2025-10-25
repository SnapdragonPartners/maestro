# Todo List Feature Design

## Problem Statement

Coder agents currently lack structured progress tracking, leading to:
- **Aimless refinement**: Rewrote `main.go` 11 times without clear stopping point
- **Ambiguous completion**: No clear signal when implementation is "done"
- **Poor observability**: Hard to see what the coder is working on or track progress

## Solution: Implementation Todo Lists

Add structured todo lists that break approved plans into small, atomic steps with clear completion requirements. This matches how commercial coding agents work and provides natural stopping conditions.

## User Flow

```
PLANNING
  ↓ coder creates plan, calls submit_plan
PLAN_REVIEW (architect reviews conceptual approach)
  ↓ if approved
PLAN_REVIEW prompts: "Generate implementation todo list"
  ↓ coder calls submit_todos with array of strings
  ↓ if empty/invalid → reprompt once → if still fails → ERROR state
CODING (first todo shown in prompt)
  ↓ coder implements, calls complete_step when done
  ↓ can call add_todo / update_todo for adjustments (logged)
  ↓ can call complete_step multiple times in one turn
CODING (next todo automatically presented)
  ↓ repeat until all todos complete
CODING (all complete - reflection prompt)
  ↓ coder either adds more todos or calls done
TESTING / CODE_REVIEW
```

## Architecture

### State Management

**Store in agent state** (survives restarts):
```go
type TodoList struct {
    Items     []TodoItem
    Current   int  // Index of current todo
}

type TodoItem struct {
    Description string  // "Create User model with auth fields"
    Completed   bool
}

// State keys
const (
    KeyTodoList = "todo_list"
)
```

**Rationale for minimal fields**: Keep cognitive overhead low. Just the todo text and completion status. No timestamps, no summaries - those add complexity without clear value.

### New MCP Tools

**Naming convention**: `todo_*` prefix for consistency and discoverability

#### 1. submit_todos
**When**: PLAN_REVIEW state after plan approval
**Purpose**: Submit implementation checklist
**Parameters**:
```json
{
  "todos": ["string array of atomic steps with clear completion requirements"]
}
```

**Validation**:
- Must be non-empty array
- Each todo must be non-empty string
- No programmatic enforcement of quality or quantity (trust the process)

**On failure**: Reprompt once. If still invalid → ERROR state (agent restart).

#### 2. todo_complete
**When**: CODING state
**Purpose**: Mark current todo as complete, advance to next
**Parameters**: None (operates on current todo)

**Effects**:
- Mark current todo as completed
- Advance to next todo
- If last todo: trigger "all complete" reflection prompt

**Can be called multiple times per turn** (efficient implementation).

#### 3. todo_add
**When**: CODING state
**Purpose**: Add new todo to list (mid-implementation adjustment)
**Parameters**:
```json
{
  "todo": "Description of new step",
  "add_after": 3,  // Optional: 0-based index to insert after (if omitted, appends to end)
  "reason": "Why this step is needed (e.g., 'Discovered need for database migrations')"
}
```

**Effects**:
- Insert todo after specified index (or append if no index)
- Log to events for debugging
- Continue with current todo

#### 4. todo_update
**When**: CODING state
**Purpose**: Modify or remove existing todo
**Parameters**:
```json
{
  "index": 0,  // 0-based index of todo to update
  "new_todo": "Updated description (empty string = remove)",
  "reason": "Why this change is needed"
}
```

**Effects**:
- Update todo at index (or remove if empty)
- Log to events for debugging
- Continue with current todo

**Note**: Cannot update current todo (must be upcoming todo). To modify current todo, use `todo_update` on it, then `todo_complete` will advance past it.

### Template Changes

#### PLAN_REVIEW Template (after plan approval)

Add new section:
```markdown
## Todo List Generation

Your plan has been approved. Now break it into implementation todos.

**Create approximately 3-10 atomic todos with clear completion criteria.**

Each todo should:
- Be completable in 1-3 coding turns
- Have clear completion criteria (you know when it's done)
- Start with an action verb (Create, Implement, Add, Configure, etc.)

Examples:
- "Create User model with authentication fields"
- "Set up database connection and run migrations"
- "Implement JWT token generation and validation"
- "Add HTTP handlers for login and registration"
- "Write unit tests for authentication service"

Use the `submit_todos` tool with an array of todo strings.
```

#### CODING Template Changes

**Current approach** (lines 58-64):
```markdown
**WORKFLOW**:
Start with discovery → Then implement → Finish decisively
```

**New approach**:
```markdown
**CURRENT TODO ({current_index + 1} of {total_count})**:
{current_todo_text}

**Completed ({completed_count})**:
{{range .CompletedTodos}}
✓ {{.Description}} - {{.Summary}}
{{end}}

**Remaining ({remaining_count})**:
{{range .RemainingTodos}}
- {{.Description}}
{{end}}

**Workflow**:
1. **Discover**: Check what files exist related to this todo
2. **Implement**: Complete the specific requirements of this todo using tool calls
3. **Verify**: Ensure this todo's requirements compile/run correctly
4. **Mark complete**: Call `complete_step` tool with brief summary

**Todo Completion Criteria**: When the specific requirements of this todo are implemented and compile/run correctly, call `complete_step`.

**Efficiency**: You can complete multiple todos in one turn by calling `complete_step` multiple times.

**Flexibility**: If this todo needs adjustment, use:
- `add_todo`: Add new step discovered during implementation
- `update_todo`: Modify or remove existing todo

**Progress, not perfection**: Complete the todo requirements and move on. Don't refine or polish beyond the todo's scope.
```

**All todos complete prompt**:
```markdown
✓ **ALL TODOS COMPLETE**

All implementation steps are marked complete. Before finishing:

1. **Review original plan**: Is it fully implemented?
2. **Run final verification**: Do builds/tests pass?
3. **Decide**:
   - If more work needed to fulfill plan: use `add_todo`
   - If implementation is complete: call `done` to submit for review

**Do not make cosmetic changes or refinements**. If requirements are met, call `done`.
```

### PLAN_REVIEW State Logic

**Current flow**:
```
PLAN_REVIEW → architect approves → CODING
```

**New flow**:
```
PLAN_REVIEW → architect approves plan
  ↓
PLAN_REVIEW (same state, new phase) → prompt for todos
  ↓ coder calls submit_todos
  ↓ validate todos
  ↓ if invalid: reprompt once
  ↓ if still invalid: ERROR state
  ↓ if valid: store in state
CODING (with first todo)
```

**Implementation**: Add `todo_list_requested` flag to track phase within PLAN_REVIEW.

### CODING State Logic

**Current**: Freeform implementation with vague completion criteria

**New**:
```go
func (c *Coder) handleCoding(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
    // Get todo list from state
    todoList := getTodoList(sm)

    if todoList == nil {
        return proto.StateError, false, fmt.Errorf("no todo list found - should have been created in PLAN_REVIEW")
    }

    // Check if all todos complete
    if todoList.AllComplete() {
        // Render "all complete" reflection prompt
        // Wait for either add_todo or done tool call
    }

    // Get current todo
    currentTodo := todoList.Current()

    // Render coding template with current todo context
    prompt := renderCodingTemplateWithTodo(currentTodo, todoList)

    // ... existing LLM call logic ...

    // Tool execution handles complete_step, add_todo, update_todo
}
```

### Tool Implementation

**Location**: `pkg/tools/` (new files or extend existing MCP tools)

```go
// submit_todos
func (t *SubmitTodosTool) Execute(ctx context.Context, params map[string]any) (map[string]any, error) {
    todos, ok := params["todos"].([]any)
    if !ok || len(todos) == 0 {
        return nil, fmt.Errorf("todos must be non-empty array")
    }

    // Convert to []string, validate
    todoStrings := make([]string, 0, len(todos))
    for _, t := range todos {
        if s, ok := t.(string); ok && strings.TrimSpace(s) != "" {
            todoStrings = append(todoStrings, strings.TrimSpace(s))
        }
    }

    if len(todoStrings) == 0 {
        return nil, fmt.Errorf("all todos were empty")
    }

    // Warn if unusual count (but don't fail)
    if len(todoStrings) < 3 {
        logger.Warn("Todo list has only %d items - might be too coarse", len(todoStrings))
    } else if len(todoStrings) > 20 {
        logger.Warn("Todo list has %d items - might be too fine-grained", len(todoStrings))
    }

    // Store in agent state
    todoList := NewTodoList(todoStrings)
    sm.SetStateData(KeyTodoList, todoList)

    return map[string]any{
        "success": true,
        "count": len(todoStrings),
        "message": fmt.Sprintf("Todo list created with %d items", len(todoStrings)),
    }, nil
}

// complete_step
func (t *CompleteStepTool) Execute(ctx context.Context, params map[string]any) (map[string]any, error) {
    summary := params["summary"].(string)

    todoList := getTodoList(sm)
    if todoList == nil {
        return nil, fmt.Errorf("no todo list found")
    }

    current := todoList.Current()
    if current == nil {
        return nil, fmt.Errorf("no current todo - all todos already complete")
    }

    // Mark current as complete
    todoList.MarkComplete(summary)
    sm.SetStateData(KeyTodoList, todoList)

    // Log event
    logger.Info("✓ Todo %d/%d complete: %s", todoList.CurrentIndex, todoList.Total(), current.Description)

    return map[string]any{
        "success": true,
        "completed": current.Description,
        "remaining": todoList.RemainingCount(),
        "all_complete": todoList.AllComplete(),
    }, nil
}

// add_todo
func (t *AddTodoTool) Execute(ctx context.Context, params map[string]any) (map[string]any, error) {
    todo := params["todo"].(string)
    reason := params["reason"].(string)

    todoList := getTodoList(sm)
    todoList.Add(todo)
    sm.SetStateData(KeyTodoList, todoList)

    // Log for debugging
    logger.Info("+ Added todo: %s (reason: %s)", todo, reason)
    eventlog.Log("todo_added", map[string]any{
        "todo": todo,
        "reason": reason,
        "total_count": todoList.Total(),
    })

    return map[string]any{"success": true}, nil
}

// update_todo
func (t *UpdateTodoTool) Execute(ctx context.Context, params map[string]any) (map[string]any, error) {
    index := int(params["index"].(float64))
    newTodo := params["new_todo"].(string)
    reason := params["reason"].(string)

    todoList := getTodoList(sm)

    if newTodo == "" {
        // Remove
        removed := todoList.Remove(index)
        logger.Info("- Removed todo %d: %s (reason: %s)", index, removed, reason)
    } else {
        // Update
        old := todoList.Update(index, newTodo)
        logger.Info("~ Updated todo %d: %s → %s (reason: %s)", index, old, newTodo, reason)
    }

    sm.SetStateData(KeyTodoList, todoList)

    eventlog.Log("todo_updated", map[string]any{
        "index": index,
        "new_todo": newTodo,
        "reason": reason,
    })

    return map[string]any{"success": true}, nil
}
```

## Benefits

### 1. Clear Progress Tracking
- Each turn has explicit objective
- Easy to see what's done, what's remaining
- Natural stopping condition: empty todo list

### 2. Prevents Aimless Refinement
- Hard to justify rewriting main.go 11 times when todo was "Create main.go handler" (singular)
- Forces forward progress through discrete steps
- "All complete" prompt requires explicit decision: add more work or call done

### 3. Better Observability
- Logs show exactly which todo was being worked on
- Budget review context includes progress through list
- Easy to debug: "It failed on todo 7 of 12"

### 4. Matches Industry Practice
- Commercial coding agents (Devin, Cursor, etc.) use todo lists
- Familiar mental model for developers
- Proven pattern for structured coding

### 5. Flexibility Without Chaos
- Coder can adapt todos mid-stream (add_todo, update_todo)
- Changes are logged for debugging
- Still maintains structure and progress tracking

## Edge Cases

### 1. Agent Restart Mid-Implementation
**Scenario**: Agent crashes while working on todo 5 of 10

**Handling**:
- Todo list stored in agent state (persisted)
- On restart, load state and resume from todo 5
- Prompt includes completed todos for context

### 2. Empty/Invalid Todo List Submission
**Scenario**: Coder calls `submit_todos` with empty array or refuses

**Handling**:
1. First attempt: Validation fails, reprompt with clearer instructions
2. Second attempt: If still invalid, transition to ERROR state
3. ERROR state triggers agent restart with fresh context

### 3. All Todos Complete But Work Isn't Done
**Scenario**: Coder marks all todos complete but original plan isn't fully implemented

**Handling**:
- "All complete" prompt asks: "Is original plan fully implemented?"
- If no: use `add_todo` to add missing work
- Forces explicit decision rather than premature `done` call

### 4. Todo Too Broad or Too Narrow
**Scenario**: Todo is "Implement entire backend" or "Add semicolon to line 47"

**Handling**:
- Template warns about granularity but doesn't enforce
- Coder can use `update_todo` to split/combine during implementation
- If pattern of poor todos emerges, architect feedback in CODE_REVIEW

### 5. Multiple Todos Completed in One Turn
**Scenario**: Coder efficiently completes 3 small todos in one response

**Handling**:
- Completely supported - call `complete_step` three times
- Each call advances to next todo
- Efficient execution is encouraged, not penalized

## Migration Path

### Phase 1: Infrastructure (No Behavior Change)
- Create TodoList types and state management
- Implement new MCP tools (submit_todos, complete_step, add_todo, update_todo)
- Add tools to planning and coding tool sets
- No template changes yet

### Phase 2: PLAN_REVIEW Integration
- Update PLAN_REVIEW template to request todos after plan approval
- Add todo list validation logic
- Test with synthetic data

### Phase 3: CODING Integration
- Update CODING template to show current todo
- Render completed/remaining lists
- Test complete_step advancement

### Phase 4: Refinement
- Add "all complete" reflection prompt
- Tune template language based on observed behavior
- Add logging and metrics

### Phase 5: Enable by Default
- Remove any feature flags
- Document new flow
- Update architect templates to expect todo-structured implementation

## Success Metrics

**Before** (current behavior):
- Wrote main.go 11 times
- Unclear when to stop
- Budget exhausted without completion

**After** (expected with todo lists):
- Each file written once per todo that requires it
- Clear completion: when todo list empty and reflection passed
- Budget used efficiently: focused work on specific todos

**Measurable**:
- File rewrite count per story: target <3 rewrites per file
- Completion rate: % of stories that call `done` vs hit error/budget
- Todo list quality: avg # of todos, % that need mid-flight updates

## Open Questions

1. **Should architect see todos in CODE_REVIEW?**
   - Current answer: No, architect reviews result not process
   - They implicitly see todos if reviewing context during budget review
   - Could add "show todo history" option to CODE_REVIEW template if useful

2. **Tool availability across states?**
   - `submit_todos`: Only in PLAN_REVIEW
   - `complete_step`, `add_todo`, `update_todo`: Only in CODING
   - `done`: Available in CODING (when appropriate)

3. **Budget review with todos?**
   - Current: Architect doesn't need to see current todo to detect redundant work
   - Budget review focuses on "is approach working?" not "is todo well-defined?"
   - Architect sees full context including tool calls, can infer progress

4. **Persist todo changes to database?**
   - Yes - log add_todo and update_todo to events table for audit trail
   - Helps debug: "Why did the coder add 5 extra todos?"

## Alternative Considered: Automatic Todo Generation

**Idea**: System automatically generates todos from approved plan

**Pros**: Less cognitive load on coder, ensures todos exist

**Cons**:
- Adds LLM call (cost, latency)
- Removes coder ownership of breakdown
- Hard to get granularity right automatically

**Decision**: Let coder generate todos. They understand the implementation details best.

## Risks

### 1. Cognitive Overhead
**Risk**: Todo generation adds complexity to planning phase

**Mitigation**:
- Separate turn from plan submission (not same response)
- Clear examples and guidelines
- Failure mode: restart agent (fresh attempt)

### 2. Too Rigid
**Risk**: Todo list becomes straitjacket preventing adaptive implementation

**Mitigation**:
- add_todo and update_todo provide flexibility
- Todos are guidelines, not prison
- Coder can complete multiple todos in one turn if efficient

### 3. Template Bloat
**Risk**: CODING template becomes too long with todo rendering

**Mitigation**:
- Keep todo display concise (limit shown completed todos to last 3)
- Focus on current todo (most important context)
- Remaining todos list for orientation

### 4. Adoption Resistance
**Risk**: LLM ignores todo structure, reverts to freeform

**Mitigation**:
- Tool availability enforces structure (complete_step required to advance)
- "All complete" prompt blocks premature done
- Budget review provides correction opportunity

## Implementation Estimate

**Complexity**: Medium

**Files to Create/Modify**:
- `pkg/coder/todolist.go` - TodoList types and methods
- `pkg/tools/submit_todos.go` - New tool
- `pkg/tools/complete_step.go` - New tool
- `pkg/tools/add_todo.go` - New tool
- `pkg/tools/update_todo.go` - New tool
- `pkg/coder/plan_review.go` - Add todo generation phase
- `pkg/coder/coding.go` - Integrate todo display and advancement
- `pkg/templates/plan_review.tpl.md` - Add todo generation section
- `pkg/templates/app_coding.tpl.md` - Replace workflow section
- `pkg/templates/devops_coding.tpl.md` - Replace workflow section

**Estimated Effort**:
- Infrastructure: 2-3 hours
- Integration: 2-3 hours
- Testing: 1-2 hours
- Refinement: 1-2 hours
- **Total**: 6-10 hours

## Next Steps

1. Get external feedback on this design
2. Create feature branch
3. Implement Phase 1 (infrastructure)
4. Test with synthetic data
5. Implement Phases 2-3 (integration)
6. Run with real story (hello world quiz)
7. Measure and refine
