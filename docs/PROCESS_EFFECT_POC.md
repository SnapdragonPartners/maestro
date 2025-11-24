# ProcessEffect Proof of Concept

## Overview

This document demonstrates the ProcessEffect pattern implementation using `ask_question` as a proof of concept.

## Design Summary

**Problem**: Tools like `ask_question` need to pause the toolloop for async operations (waiting for architect's answer) without being terminal tools.

**Solution**: Tools return `ProcessEffect` to signal "pause this loop, process async effect, then resume."

## Key Components

### 1. ProcessEffect Type (`pkg/tools/mcp.go`)

```go
type ProcessEffect struct {
    Signal string // e.g., "QUESTION", "BUDGET_REVIEW"
    Data   any    // Question data, budget info, etc.
}

type ExecResult struct {
    Result        any            // Optional rich output for LLM (nil = auto-ack)
    ProcessEffect *ProcessEffect // nil = continue, non-nil = pause
}
```

### 2. Tool Implementation (`pkg/tools/planning_tools.go`)

**ask_question** tool now returns ProcessEffect:

```go
func (a *AskQuestionTool) Exec(_ context.Context, args map[string]any) (*ExecResult, error) {
    // ... validate and extract params ...

    return &ExecResult{
        ProcessEffect: &ProcessEffect{
            Signal: "QUESTION",
            Data: map[string]string{
                "question": questionStr,
                "context":  context,
                "urgency":  urgency,
            },
        },
    }, nil
}
```

### 3. Toolloop Handling (`pkg/agent/toolloop/toolloop.go`)

After each tool execution:

```go
// Execute tool
execResult, err := tool.Exec(toolCtx, toolCall.Parameters)

// Check for ProcessEffect
if execResult.ProcessEffect != nil && execResult.ProcessEffect.Signal != "" {
    logger.Info("Tool returned ProcessEffect with signal: %s", execResult.ProcessEffect.Signal)
    pendingEffect = execResult.ProcessEffect
}

// After all tools in iteration execute:
if pendingEffect != nil {
    return Outcome[T]{
        Kind:   OutcomeProcessEffect,
        Signal: pendingEffect.Signal,  // "QUESTION"
        // Value is zero - no terminal result
    }
}
```

### 4. State Machine Handling (`pkg/coder/coding.go` - TODO)

```go
func (c *Coder) handleCoding(ctx context.Context) (proto.State, bool, error) {
    // Run toolloop
    out := toolloop.Run[CodingResult](tl, ctx, cfg)

    switch out.Kind {
    case toolloop.OutcomeProcessEffect:
        // Handle pause signals
        switch out.Signal {
        case "QUESTION":
            // Extract question from ProcessEffect.Data
            // Create Effect for architect communication
            // Transition to QUESTION state
            return proto.StateQuestion, false, nil
        }

    case toolloop.OutcomeSuccess:
        // Terminal tool called - extract result
        result := out.Value
        if result.TestingRequest {
            return proto.StateTesting, false, nil
        }
    }
}
```

### 5. Question State Handler (`pkg/coder/question.go` - TODO)

```go
func (c *Coder) handleQuestion(ctx context.Context) (proto.State, bool, error) {
    // Wait for Effect resolution (architect's answer)
    // Effect system adds answer to context manager
    // ...answer arrives...

    // Transition back to CODING
    return proto.StateCoding, false, nil
}

// State machine calls handleCoding again automatically
// Toolloop resumes with answer in context
```

## Flow Diagram

```
CODING state:
1. toolloop.Run() executes LLM with tools
2. LLM calls ask_question
3. ask_question returns ProcessEffect{Signal: "QUESTION"}
4. toolloop detects ProcessEffect, returns OutcomeProcessEffect
5. State machine receives OutcomeProcessEffect
6. Creates Effect for architect communication
7. Transitions to QUESTION state

QUESTION state:
1. Waits for Effect resolution
2. Effect system adds architect's answer to context manager
3. Transitions back to CODING state

CODING state (resumed):
1. toolloop.Run() called again (same context manager)
2. Context now has: [previous messages] → [ask_question] → [answer]
3. LLM sees answer and continues work
4. Eventually calls done (terminal tool)
5. toolloop returns OutcomeSuccess
6. State machine transitions to TESTING
```

## Key Benefits

1. **Clean Separation**:
   - ProcessEffect = pause (temporary, will resume)
   - Terminal tools = completion (work done, extract result)

2. **No Concurrency**:
   - Loop exits via return, no goroutines needed
   - State machine handles async wait
   - Resume by calling Run() again

3. **Type Safety**:
   - Terminal tools have strongly-typed results
   - ProcessEffect has untyped data (flexible for different effects)

4. **Extensible**:
   - Same pattern for budget_review, chat_ask_user, etc.
   - Each effect type has its own Signal string
   - State machine routes based on Signal

## Current Status

✅ **Completed:**
- ProcessEffect/ExecResult types defined
- Toolloop ProcessEffect detection
- OutcomeProcessEffect enum value
- ask_question tool updated

⏸️ **TODO (Phase 3d):**
- Update coder state machine to handle OutcomeProcessEffect
- Implement QUESTION state handler
- Extract question data from ProcessEffect.Data
- Create Effect for architect communication
- Test full round-trip (question → answer → resume)

⏸️ **Deferred (Phase 3c):**
- Update remaining tools to new Exec signature
- Can be done incrementally after POC validation
- Most tools just return `&ExecResult{}` (no ProcessEffect)

## Next Steps

1. Validate this approach with user
2. Implement coder state machine changes (Phase 3d)
3. Test with actual question/answer flow
4. Document any issues or refinements needed
5. Apply pattern to other pause tools (budget_review, chat_ask_user)
6. Update remaining tools incrementally
