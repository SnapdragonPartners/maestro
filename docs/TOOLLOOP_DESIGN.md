# Toolloop Design

This document describes the architecture and design principles of the toolloop system, which manages LLM interactions with tool calling.

## Overview

The toolloop system provides a generic, type-safe abstraction for LLM tool-calling loops used by all agents (Architect, Coder, PM). It handles:
- Iterative LLM interactions with tool execution
- Terminal condition detection (when to stop iterating)
- Type-safe result extraction
- Escalation management (iteration limits)
- Error handling and retry logic

## Core Principle: One Goal, One Exit

**A toolloop represents accomplishing one specific goal. That goal should have exactly one exit condition.**

This principle is enforced at compile time through the type system:
- Each toolloop invocation provides exactly one **terminal tool**
- Terminal tools signal loop completion and extract the typed result
- Multiple exit paths require multiple sequential toollloops

## Architecture

### Type-Safe Terminal Tools

```go
// Terminal tools must implement this interface
type TerminalTool[TResult any] interface {
    tools.Tool
    ExtractResult(calls []agent.ToolCall, results []any) (TResult, error)
}

// Toolloop configuration with compile-time enforcement
type Config[TResult any] struct {
    ContextManager *contextmgr.ContextManager
    GeneralTools   []tools.Tool           // Non-terminal tools (exploration, analysis)
    TerminalTool   TerminalTool[TResult]  // Exactly one terminal tool (compiler enforces)
    MaxIterations  int
    MaxTokens      int
    SingleTurn     bool                   // Expect completion in first iteration
    DebugLogging   bool
    AgentID        string
    Escalation     *EscalationConfig      // Soft/hard iteration limits
}

// Run executes the toolloop with type-safe result extraction
func Run[TResult any](tl *ToolLoop, ctx context.Context, cfg *Config[TResult]) Outcome[TResult]
```

### Key Design Decisions

#### 1. Exactly One Terminal Tool (Compile-Time Enforced)

**Why**: Multiple terminal tools lead to:
- Ambiguous semantics (which tool = which outcome?)
- Ordering bugs (CheckTerminal vs ExtractResult check in different orders)
- Implicit coupling (terminal tool choice implies decision)

**How**: Go's type system enforces this:
- `TerminalTool` is a single field, not a slice
- Compiler error if you try to pass multiple values
- Cannot construct `Config` without exactly one terminal tool

**Example - Good:**
```go
cfg := &toolloop.Config[ReviewResult]{
    GeneralTools: []tools.Tool{readFile, listFiles},
    TerminalTool: &ReviewCompleteTool{},  // Exactly one
}
```

**Example - Won't Compile:**
```go
cfg := &toolloop.Config[ReviewResult]{
    GeneralTools: []tools.Tool{readFile, listFiles},
    TerminalTool: &ReviewCompleteTool{},
    // Can't add a second terminal tool - compiler error
}
```

#### 2. Terminal Tools Extract Their Own Results

**Why**: Encapsulation and type safety
- Each terminal tool knows its own result structure
- No separate `ExtractResult` callback that can get out of sync
- Type parameter `TResult` ensures terminal tool and config agree on result type

**How**: `TerminalTool[T]` interface includes `ExtractResult()` method
- Tool validates its own output structure
- Returns typed result directly
- Toolloop calls `cfg.TerminalTool.ExtractResult()` when tool is invoked

#### 3. Implicit Terminal Detection

**Old way**: Explicit `CheckTerminal` callback scans tool calls
**New way**: Toolloop checks if `cfg.TerminalTool.Name()` was called

**Why**: Simpler and less error-prone
- No callback that can check tools in wrong order
- Terminal condition is: "was the terminal tool called?"
- Single source of truth

#### 4. No Signal Field in Outcome

**Old**: `Outcome[T]` had `Signal string` field for state machine transitions
**New**: `Outcome[T]` only has `Value T` - the result type identifies the outcome

**Why**: Type-based dispatch is cleaner
- Caller switches on `out.Kind` (Success/Error/IterationLimit)
- For Success, caller uses `out.Value` (typed result from terminal tool)
- State machine transitions based on result type, not string signal

## Toolloop Modes

### Single-Turn Mode

**Usage**: Reviews and decisions that should complete in one LLM call

**Characteristics:**
- `SingleTurn: true` in config
- Only the terminal tool is provided (no general tools)
- LLM must call terminal tool in first iteration
- If terminal tool not called, toolloop nudges LLM to retry
- Maximum 3 iterations (original + 2 nudges)

**Example**: Plan reviews
```go
cfg := &toolloop.Config[ReviewResult]{
    GeneralTools: []tools.Tool{},  // Empty - single-turn
    TerminalTool: &ReviewCompleteTool{},
    SingleTurn:   true,
    MaxIterations: 3,  // Allow retry/nudge
}
```

### Iterative Mode

**Usage**: Exploratory analysis requiring multiple LLM interactions

**Characteristics:**
- `SingleTurn: false` (default)
- General tools provided for exploration (read_file, list_files, etc.)
- LLM can iterate multiple times before calling terminal tool
- MaxIterations typically 10-20

**Example**: Code reviews
```go
cfg := &toolloop.Config[ReviewResult]{
    GeneralTools: []tools.Tool{readFile, listFiles, getDiff},
    TerminalTool: &ReviewCompleteTool{},
    SingleTurn:   false,
    MaxIterations: 20,
}
```

## Terminal Tool Categories

### Review Tools (Structured Decisions)

**Tool**: `review_complete`
**Result**: `ReviewCompleteResult{Status, Feedback}`
**Parameters**:
- `status`: enum (APPROVED, NEEDS_CHANGES, REJECTED)
- `feedback`: string

**Used By**:
- Architect: Plan reviews, Budget reviews, Code reviews, Completion reviews, Spec reviews (phase 1)

**Why**: All reviews need structured decisions with explicit status

### Reply Tools (Open-Ended Responses)

**Tool**: `submit_reply`
**Result**: `SubmitReplyResult{Response}`
**Parameters**:
- `response`: string (free-form text)

**Used By**:
- Architect: Questions (Q&A with coders)

**Why**: Questions don't have APPROVED/REJECTED status - just answers

### Submission Tools (Structured Data)

**Tool**: `submit_stories`, `plan_submit`, `spec_submit`
**Result**: Varies by tool
**Parameters**: Complex structured data (requirements, plan details, etc.)

**Used By**:
- Architect: Story generation (after spec approval)
- Coder: Plan submission
- PM: Spec submission

**Why**: Submitting structured artifacts requires validation

## Multi-Phase Patterns

### When One Toolloop Isn't Enough

If you need multiple exit paths, use **sequential toollloops** instead of multiple terminal tools:

**Bad (multiple terminal tools - won't compile):**
```go
cfg := &toolloop.Config[SpecResult]{
    TerminalTool: ???,  // Which one? submit_stories OR spec_feedback?
}
```

**Good (two sequential toollloops):**
```go
// Phase 1: Review decision
cfg1 := &toolloop.Config[ReviewResult]{
    GeneralTools: []tools.Tool{readFile, listFiles},
    TerminalTool: &ReviewCompleteTool{},
}
out1 := toolloop.Run(tl, ctx, cfg1)

if out1.Value.Status == APPROVED {
    // Phase 2: Story generation
    cfg2 := &toolloop.Config[StoriesResult]{
        GeneralTools: []tools.Tool{},
        TerminalTool: &SubmitStoriesTool{},
        SingleTurn:   true,
    }
    out2 := toolloop.Run(tl, ctx, cfg2)
}
```

### Example: Spec Review (Two-Phase)

**Phase 1 - Review Decision:**
- **Goal**: Decide if spec is acceptable
- **Tools**: `read_file`, `list_files` + `review_complete`
- **Outcome**: Status (APPROVED/NEEDS_CHANGES/REJECTED)
- **Next**: If approved → Phase 2, else send feedback to PM

**Phase 2 - Story Generation:**
- **Goal**: Break spec into implementable stories
- **Tools**: `submit_stories` only
- **Outcome**: Story list with requirements
- **Next**: Load stories, send approval to PM

**Why two phases**:
- Different goals: "is it good?" vs "break it down"
- Different tools: exploration vs generation
- Clean state management: approval decision persists, story generation can retry

## Escalation Management

Toolloop supports soft and hard iteration limits with callbacks:

```go
cfg := &toolloop.Config[ReviewResult]{
    // ... tools ...
    MaxIterations: 20,
    Escalation: &toolloop.EscalationConfig{
        Key:       "approval_story123",     // Unique key for tracking
        SoftLimit: 8,                        // Warning at 8 iterations
        HardLimit: 16,                       // Stop at 16 iterations
        OnSoftLimit: func(count int) {
            // Log warning, inject message to LLM context
        },
        OnHardLimit: func(ctx, key string, count int) error {
            // Post to chat for human intervention
            // Return error to stop toolloop
        },
    },
}
```

**Iteration Counting**: 1-indexed for user-facing logs

## Testing Terminal Tools

Each terminal tool should have unit tests covering:
1. **Successful extraction**: Tool called with valid parameters
2. **Missing tool**: Terminal tool not called → `ErrNoTerminalTool`
3. **Invalid parameters**: Tool called with malformed data → `ErrInvalidResult`
4. **Type safety**: Result type matches expected type

Example test:
```go
func TestReviewCompleteTool_ExtractResult(t *testing.T) {
    tool := &ReviewCompleteTool{}

    calls := []agent.ToolCall{{
        Name: "review_complete",
        Parameters: map[string]any{
            "status": "APPROVED",
            "feedback": "Looks good",
        },
    }}

    results := []any{
        map[string]any{
            "success": true,
            "status": "APPROVED",
            "feedback": "Looks good",
        },
    }

    result, err := tool.ExtractResult(calls, results)
    assert.NoError(t, err)
    assert.Equal(t, "APPROVED", result.Status)
    assert.Equal(t, "Looks good", result.Feedback)
}
```

## Best Practices

### 1. One Terminal Tool Per Toolloop

✅ **Do**: Each toolloop has exactly one terminal tool
```go
cfg := &toolloop.Config[PlanResult]{
    TerminalTool: &PlanSubmitTool{},
}
```

❌ **Don't**: Try to use multiple terminal tools (won't compile)

### 2. Explicit Status in Parameters

✅ **Do**: Terminal tool parameters include explicit status/decision
```go
review_complete(status="APPROVED", feedback="...")
```

❌ **Don't**: Hide decision in which tool was called
```go
// Bad: approval implicit in tool choice
submit_stories(...)  // means approved
spec_feedback(...)   // means rejected
```

### 3. Use Sequential Toollloops for Multiple Phases

✅ **Do**: Break complex workflows into phases
```go
phase1 := toolloop.Run(cfg1)  // Review
if phase1.Value.Approved {
    phase2 := toolloop.Run(cfg2)  // Generate
}
```

❌ **Don't**: Try to cram multiple goals into one loop

### 4. Terminal Tools Should Be Simple

✅ **Do**: Terminal tools validate and extract data
```go
func (t *ReviewCompleteTool) ExtractResult(calls, results) (ReviewResult, error) {
    // Find tool call, validate parameters, return typed result
}
```

❌ **Don't**: Terminal tools perform side effects (logging is OK, state changes are not)

### 5. SingleTurn for Decisions, Iterative for Exploration

✅ **Do**: Use single-turn when LLM has all information
```go
// Plan text is in the request - no exploration needed
cfg := &Config[ReviewResult]{
    SingleTurn: true,
    TerminalTool: &ReviewCompleteTool{},
}
```

✅ **Do**: Use iterative when LLM needs to explore
```go
// Code review requires reading files
cfg := &Config[ReviewResult]{
    SingleTurn: false,
    GeneralTools: []tools.Tool{readFile, listFiles, getDiff},
    TerminalTool: &ReviewCompleteTool{},
}
```

## Migration from Old API

See `docs/TOOLLOOP_REFACTOR_PLAN.md` for detailed migration guide.

**Key Changes:**
- ✅ Remove `ToolProvider` from config
- ✅ Remove `CheckTerminal` callback (implicit in terminal tool)
- ✅ Remove `ExtractResult` callback (method on terminal tool)
- ✅ Split tools into `GeneralTools` and `TerminalTool`
- ✅ Update result handling (no `Signal` field)

**Benefits:**
- Compile-time enforcement of one terminal tool
- Simpler code (no callbacks to maintain)
- Type-safe result extraction
- Impossible to have ordering bugs

## Further Reading

- `docs/ARCHITECT_TOOL_CONFIGURATION.md` - Architect tool sets by request type
- `docs/TOOLLOOP_REFACTOR_PLAN.md` - Migration plan for refactor
- `pkg/agent/toolloop/toolloop.go` - Implementation
