# Toolloop Refactor: Compile-Time Terminal Tool Enforcement

## Executive Summary

This document outlines the migration plan for refactoring the toolloop system to enforce **exactly one terminal tool per toolloop invocation at compile time**. This is a **breaking change** with no backwards compatibility - the compiler will catch every call site that needs updating.

## Problem Statement

The current toolloop design allows multiple terminal tools to be provided via the `ToolProvider` interface, leading to bugs:

1. **Undefined Behavior**: If LLM calls multiple terminal tools in one response, behavior depends on tool ordering in `CheckTerminal` vs `ExtractResult`
2. **Implicit Semantics**: Terminal tool selection implies the decision (e.g., `submit_stories` = approval, `spec_feedback` = rejection) rather than explicit parameters
3. **Runtime-Only Detection**: Violations only caught at runtime, not during development

### Current Architecture

```go
type Config[T any] struct {
    ToolProvider   ToolProvider              // Can contain multiple terminal tools
    CheckTerminal  func(calls, results) string
    ExtractResult  func(calls, results) (T, error)
}
```

## Solution: Type-Safe Terminal Tools

Use Go generics to enforce exactly one terminal tool at compile time:

```go
type TerminalTool[TResult any] interface {
    tools.Tool
    ExtractResult(calls []agent.ToolCall, results []any) (TResult, error)
}

type Config[TResult any] struct {
    ContextManager *contextmgr.ContextManager
    GeneralTools   []tools.Tool           // Non-terminal tools only
    TerminalTool   TerminalTool[TResult]  // Exactly one, compiler enforces
    MaxIterations  int
    MaxTokens      int
    SingleTurn     bool
    DebugLogging   bool
    AgentID        string
    Escalation     *EscalationConfig
}

func Run[TResult any](tl *ToolLoop, ctx context.Context, cfg *Config[TResult]) Outcome[TResult]
```

**Key changes:**
- `ToolProvider` removed from `Config` (breaking change)
- `CheckTerminal` callback removed - terminal tool detection is implicit (tool was called)
- `ExtractResult` moved to terminal tool interface (each terminal tool extracts its own result)
- Compiler guarantees exactly one terminal tool value provided

## Migration Plan

### Phase 1: Core Toolloop Infrastructure

**Goal**: Update toolloop package with new API, breaking all existing call sites.

**IMPORTANT**: We update Coder and PM **before** Architect to validate the new architecture works for all cases, not just the ones we've analyzed.

**Tasks:**
1. ✅ Create `TerminalTool[T]` interface in `pkg/agent/toolloop/toolloop.go`
2. ✅ Update `Config[T]` struct to remove `ToolProvider`, `CheckTerminal`, `ExtractResult`
3. ✅ Add `GeneralTools []tools.Tool` and `TerminalTool TerminalTool[TResult]` fields
4. ✅ Update `Run[T]()` function to build internal ToolProvider from general + terminal tools
5. ✅ Update `Run[T]()` to detect terminal tool call by checking if `TerminalTool.Name()` was called
6. ✅ Call `TerminalTool.ExtractResult()` when terminal tool detected
7. ✅ Update `Outcome[T]` to remove `Signal` field (no longer needed - terminal tool type identifies the outcome)
8. ✅ Add optional `NewConfig()` constructor with runtime validation (general tools can't be terminal tools)

**Files to modify:**
- `pkg/agent/toolloop/toolloop.go`

**Compilation will break at:** All `toolloop.Run()` call sites (architect, coder, PM)

### Phase 2: Create Terminal Tool Implementations

**Goal**: Convert existing terminal tool combinations into proper `TerminalTool[T]` implementations.

#### Architect Terminal Tools

| Tool | Result Type | Used By | Notes |
|------|-------------|---------|-------|
| `review_complete` | `ReviewCompleteResult` | Plan reviews, Budget reviews, Code reviews, Completion reviews | Replaces both `review_complete` AND `submit_reply` for consistency |
| `submit_reply` | `SubmitReplyResult` | Questions | Only used for Q&A (not a "review") |
| `submit_stories` | `SubmitStoriesResult` | Spec review (phase 2) | Used in second toolloop after `review_complete` approves spec |
| `spec_feedback` | ❌ **REMOVE** | ~~Spec review~~ | Merged into `review_complete` for spec review phase 1 |

**Key Decision**: Use `review_complete` for ALL reviews (plan, budget, code, completion, spec phase 1) since they all need structured decisions. Only questions use `submit_reply` for open-ended answers.

**Tasks:**
1. ✅ Create `ReviewCompleteTool` implementing `TerminalTool[ReviewCompleteResult]`
   - Moves `ExtractReviewComplete()` logic into the tool
   - Used by: Plan, Budget, Code, Completion, Spec (phase 1)
2. ✅ Create `SubmitReplyTool` implementing `TerminalTool[SubmitReplyResult]`
   - Moves `ExtractSubmitReply()` logic into the tool
   - Used by: Questions only
3. ✅ Create `SubmitStoriesTool` implementing `TerminalTool[SubmitStoriesResult]`
   - Moves `ExtractSpecReview()` logic for submit_stories into the tool
   - Used by: Spec review phase 2 only
4. ❌ Remove `SpecFeedbackTool` (no longer a terminal tool - merged into review_complete)

**Files to create/modify:**
- `pkg/tools/review_complete.go` - Add `TerminalTool[ReviewCompleteResult]` implementation
- `pkg/tools/submit_reply.go` - Add `TerminalTool[SubmitReplyResult]` implementation
- `pkg/tools/submit_stories.go` - Add `TerminalTool[SubmitStoriesResult]` implementation
- `pkg/tools/spec_feedback.go` - Remove or convert to non-terminal helper tool
- `pkg/architect/toolloop_results.go` - Move extraction logic into terminal tools

#### Coder Terminal Tools

| Tool | Result Type | Used By | Notes |
|------|-------------|---------|-------|
| `plan_submit` | `PlanSubmitResult` | Planning state | Single-turn |
| `todo_complete` | `TodoCompleteResult` | Coding state | Iterative - marks todos as done |
| `request_testing` | `RequestTestingResult` | Coding state | Signals ready for testing |

**Tasks:**
1. ✅ Create `PlanSubmitTool` implementing `TerminalTool[PlanSubmitResult]`
2. ✅ Update existing `TodoCompleteTool` to implement `TerminalTool[TodoCompleteResult]`
3. ✅ Update existing `RequestTestingTool` to implement `TerminalTool[RequestTestingResult]`

**Files to modify:**
- `pkg/tools/plan_submit.go`
- `pkg/tools/todo_complete.go`
- `pkg/tools/request_testing.go`
- `pkg/coder/toolloop_results.go`

#### PM Terminal Tools

| Tool | Result Type | Used By | Notes |
|------|-------------|---------|-------|
| `spec_submit` | `SpecSubmitResult` | Working state | Submits spec to architect |

**Tasks:**
1. ✅ Update `SpecSubmitTool` to implement `TerminalTool[SpecSubmitResult]`

**Files to modify:**
- `pkg/tools/spec_submit.go`
- `pkg/pm/toolloop_results.go`

### Phase 3: Update Coder Toolloop Calls

**Goal**: Update all coder toolloop calls to use new API **FIRST** to validate architecture.

**Why before Architect**: We've thoroughly analyzed the architect, but need to ensure the new design works for coder use cases (plan submission, todo completion, test requests).

**States Using Toolloop:**
- `PLANNING`: Uses `plan_submit` terminal tool
- `CODING`: Uses `todo_complete` or `request_testing` terminal tools
- `TESTING`: May use toolloop for error analysis (check if terminal tool needed)

**Tasks:**
1. ✅ Update `pkg/coder/planning.go` toolloop call
2. ✅ Update `pkg/coder/coding.go` toolloop calls
3. ✅ Review `pkg/coder/testing.go` for toolloop usage
4. ✅ Remove coder `CheckTerminal` functions
5. ✅ Move extraction logic into terminal tools

**Files to modify:**
- `pkg/coder/planning.go`
- `pkg/coder/coding.go`
- `pkg/coder/testing.go`
- `pkg/coder/toolloop_results.go`

### Phase 4: Update PM Toolloop Calls

**Goal**: Update PM toolloop calls to use new API and handle non-terminal state transitions.

**Why before Architect**: Validate that `chat_ask_user` tool (which transitions to AWAIT_USER state) works correctly with new architecture.

**Special Case: `chat_ask_user` Tool**
- **Not a terminal tool** (work isn't finished, just paused waiting for user)
- Returns `await_user: true` flag in result
- PM's `callLLMWithTools()` checks this flag and returns "AWAIT_USER" signal
- This is a **state transition signal, not a terminal tool** - the toolloop continues normally, but PM's state handler transitions to AWAIT_USER based on the signal

**Validation Points:**
1. ✅ `chat_ask_user` remains a general tool (not terminal)
2. ✅ PM working state can return "AWAIT_USER" signal without terminal tool being called
3. ✅ `spec_submit` is the only terminal tool for PM's WORKING state
4. ✅ Confirm this pattern (general tool returning signal) is compatible with new architecture

**Tasks:**
1. ✅ Update `pkg/pm/working.go` toolloop call (uses `spec_submit`)
2. ✅ Verify `chat_ask_user` works as general tool with state signal
3. ✅ Remove PM `CheckTerminal` functions
4. ✅ Move extraction logic into terminal tools

**Files to modify:**
- `pkg/pm/working.go`
- `pkg/pm/toolloop_results.go`
- `pkg/pm/await_user.go` (validate unchanged)

### Phase 5: Update Architect Request Handlers

**Goal**: Refactor architect to use new toolloop API and regularize tool usage.

**Why after Coder/PM**: We've validated the new architecture works for all agent types. Now apply it to the most complex agent (architect) with confidence.

**Additional Validation:**
1. ✅ Confirm `review_complete` can route responses to PM (not just coders)
   - Architect uses `requestMsg.FromAgent` for routing (works for both coder and PM)
   - Spec review (phase 1) sends `review_complete` result to PM
2. ✅ Test that general tools returning signals (like PM's `chat_ask_user`) work correctly

#### 5.1: Regular Reviews (Single Terminal Tool)

**Plan Reviews** (currently single-turn with `review_complete`):
- ✅ Already uses single terminal tool
- ✅ Update to new API: pass `ReviewCompleteTool` as terminal tool
- ✅ Remove `CheckTerminal` and `ExtractResult` callbacks

**Budget Reviews** (currently single-turn with `review_complete`):
- ✅ Already uses single terminal tool
- ✅ Same changes as plan reviews

**Code Reviews** (currently iterative with `submit_reply`):
- ⚠️ **Change terminal tool** from `submit_reply` to `review_complete`
- ✅ Parse APPROVED/NEEDS_CHANGES/REJECTED from structured result instead of free text
- ✅ Update to new API

**Completion Reviews** (currently iterative with `submit_reply`):
- ⚠️ **Change terminal tool** from `submit_reply` to `review_complete`
- ✅ Same changes as code reviews

**Questions** (currently iterative with `submit_reply`):
- ✅ Already uses single terminal tool (`submit_reply`)
- ✅ Update to new API

**Tasks:**
1. ✅ Update `handleSingleTurnReview()` for plan/budget reviews
2. ✅ Update `handleIterativeApproval()` to use `ReviewCompleteTool` instead of `SubmitReplyTool`
3. ✅ Update `handleIterativeQuestion()` to use `SubmitReplyTool` with new API
4. ✅ Remove `checkTerminal` functions (logic moved into terminal tools)
5. ✅ Remove extraction functions from `toolloop_results.go` (moved into tools)
6. ✅ Update prompts to use `review_complete` for all reviews

**Files to modify:**
- `pkg/architect/request.go`
- `pkg/architect/request_code.go`
- `pkg/architect/request_completion.go`
- `pkg/architect/request_plan.go`
- `pkg/architect/request_question.go`
- `pkg/architect/driver.go` (remove `checkTerminalTools()`)
- `pkg/architect/toolloop_results.go`

#### 5.2: Spec Review (Two-Phase Approach)

**Current Flow** (broken - multiple terminal tools):
```
handleSpecReview() {
    Tools: [read_file, list_files, submit_stories, spec_feedback]
    Terminal: submit_stories (approval) OR spec_feedback (rejection)
    Problem: Multiple terminal tools cause ordering bugs
}
```

**New Flow** (two phases, one terminal tool each):
```
Phase 1 - Review Decision:
    State: REQUEST (spec approval type)
    Tools: [read_file, list_files] + review_complete
    Terminal: review_complete(status=APPROVED/NEEDS_CHANGES/REJECTED)
    Outcome:
        APPROVED → transition to StateStoryGeneration
        NEEDS_CHANGES/REJECTED → send rejection to PM, transition to StateDispatching

Phase 2 - Story Generation:
    State: STORY_GENERATION (new state)
    Tools: [] + submit_stories
    Terminal: submit_stories(analysis, platform, requirements)
    Outcome: Load stories, send approval to PM, transition to StateDispatching
```

**Tasks:**
1. ✅ Add `StateStoryGeneration` state to architect state machine
2. ✅ Refactor `handleSpecReview()` to phase 1 (review decision only)
3. ✅ Create `handleStoryGeneration()` for phase 2
4. ✅ Update state transitions: REQUEST(spec) → STORY_GENERATION → DISPATCHING
5. ✅ Remove `spec_feedback` tool (replaced by `review_complete(status=NEEDS_CHANGES)`)
6. ✅ Update spec review templates to use `review_complete`

**Files to modify:**
- `pkg/architect/request_spec.go`
- `pkg/architect/states.go` (add StateStoryGeneration)
- `pkg/architect/driver.go` (add ProcessStoryGeneration handler)
- `pkg/templates/architect/spec_analysis.tpl.md`


### Phase 6: Update Tests

**Goal**: Fix all broken tests to use new toolloop API.

**Tasks:**
1. ✅ Update toolloop unit tests (`pkg/agent/toolloop/*_test.go`)
2. ✅ Update architect tests (`pkg/architect/*_test.go`)
3. ✅ Update coder tests (`pkg/coder/*_test.go`)
4. ✅ Update PM tests (`pkg/pm/*_test.go`)
5. ✅ Add new tests for compile-time enforcement (negative tests that shouldn't compile)

**Files to modify:**
- All `*_test.go` files that use toolloop

### Phase 7: Update Documentation

**Goal**: Document new architecture and migration.

**Tasks:**
1. ✅ Update `docs/TOOLLOOP_DESIGN.md` with new architecture
2. ✅ Update `docs/ARCHITECT_TOOL_CONFIGURATION.md` with regularized tool usage
3. ✅ Add migration notes to `CHANGELOG.md`
4. ✅ Update code comments in toolloop package

**Files to modify:**
- `docs/TOOLLOOP_DESIGN.md`
- `docs/ARCHITECT_TOOL_CONFIGURATION.md`
- `CHANGELOG.md`

## Implementation Strategy

### Approach: Clean Break (No Backwards Compatibility)

We will make all breaking changes at once, letting the compiler catch every call site:

1. **Phase 1**: Break the API (update toolloop package)
   - Compilation fails at all call sites
   - Compiler errors guide us to every location that needs updating
2. **Phase 2-5**: Fix all call sites systematically
   - Work through compiler errors one by one
   - No legacy code paths or compatibility shims
3. **Phase 6-7**: Verify and document
   - All tests pass with new API
   - Documentation reflects new architecture

### Migration Timeline

**Estimated effort**: 1-2 days
- Phase 1 (Toolloop): 2 hours
- Phase 2 (Terminal Tools): 3 hours
- Phase 3 (Coder): 2 hours ← MOVED UP
- Phase 4 (PM): 1 hour ← MOVED UP (validate chat_ask_user pattern)
- Phase 5 (Architect): 4 hours (spec review refactor is complex)
- Phase 6 (Tests): 3 hours
- Phase 7 (Docs): 1 hour

### Risk Mitigation

**Risk**: Breaking changes might introduce subtle bugs

**Mitigation:**
- Comprehensive test coverage before starting
- Work on a feature branch (`refactor/toolloop-terminal-enforcement`)
- Run full test suite after each phase
- Manual testing of key workflows (spec review, code review, planning)

**Risk**: Spec review two-phase approach might have state management issues

**Mitigation:**
- Architect already has state persistence for interrupted work
- Store "spec_pending_story_generation" flag for recovery
- Test crash recovery between phases

## Success Criteria

- ✅ All code compiles with new toolloop API
- ✅ Zero `CheckTerminal` callback functions remain (logic in terminal tools)
- ✅ Zero `ExtractResult` callback functions remain (methods on terminal tools)
- ✅ All tests pass
- ✅ Architect uses `review_complete` for all reviews (plan, budget, code, completion, spec)
- ✅ Architect uses `submit_reply` only for questions
- ✅ Spec review is two-phase (review decision → story generation)
- ✅ Compiler error if anyone tries to add multiple terminal tools
- ✅ Documentation updated to reflect new architecture

## Post-Migration Benefits

1. **Compile-Time Safety**: Impossible to have multiple terminal tools
2. **Clear Semantics**: Terminal tool type + parameters = explicit outcome
3. **Simpler Code**: No `CheckTerminal` ordering bugs
4. **Better Encapsulation**: Each terminal tool knows how to extract its own result
5. **Consistent Architecture**: All reviews use same tool (`review_complete`)
6. **Maintainability**: Compiler enforces correct usage, not runtime checks

## Appendix: Terminal Tool Summary

### Post-Migration Terminal Tools

| Tool Name | Result Type | Used By | Purpose |
|-----------|-------------|---------|---------|
| `review_complete` | `ReviewCompleteResult{Status, Feedback}` | Architect: Plan, Budget, Code, Completion, Spec (phase 1) | Structured review decision with status enum |
| `submit_reply` | `SubmitReplyResult{Response}` | Architect: Questions | Open-ended text response |
| `submit_stories` | `SubmitStoriesResult{Analysis, Platform, Requirements}` | Architect: Spec (phase 2) | Story generation after approval |
| `plan_submit` | `PlanSubmitResult{Plan, Confidence, ...}` | Coder: Planning | Submit implementation plan |
| `todo_complete` | `TodoCompleteResult{CompletedIDs, ...}` | Coder: Coding | Mark todos as complete |
| `request_testing` | `RequestTestingResult{ReadyForTest}` | Coder: Coding | Signal ready for test phase |
| `spec_submit` | `SpecSubmitResult{Spec}` | PM: Working | Submit spec to architect |

### Removed Tools

| Tool Name | Reason |
|-----------|--------|
| `spec_feedback` | Merged into `review_complete(status=NEEDS_CHANGES)` for consistency |
