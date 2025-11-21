# Budget Review Escalation Issue

**Status**: ✅ RESOLVED

## Resolution Summary

Implemented typed error approach (Option A) to make iteration limit a normal termination condition rather than a failure. This keeps toolloop generic while providing explicit control flow for escalation.

## Context

The Coder agent uses toolloop for iterative LLM interactions with iteration limits and escalation to budget review when limits are exceeded.

## Original Behavior (Before Fix)

When hitting hard iteration limit (8 iterations for coding phase), the flow is:

1. **OnHardLimit handler executes** - Creates BudgetReviewEffect and stores in state:
   ```go
   budgetEff := effect.NewBudgetReviewEffect(
       fmt.Sprintf("Maximum coding iterations (%d) reached", maxCodingIterations),
       "Coding workflow needs additional iterations to complete",
       string(StateCoding),
   )
   budgetEff.StoryID = utils.GetStateValueOr[string](sm, KeyStoryID, "")
   sm.SetStateData("budget_review_effect", budgetEff)
   return fmt.Errorf("max iterations reached - budget review required")
   ```

2. **Toolloop ALWAYS returns error** - Regardless of OnHardLimit return value (toolloop.go:351):
   ```go
   return "", zero, fmt.Errorf("hard iteration limit (%d) exceeded for key '%s'", cfg.Escalation.HardLimit, cfg.Escalation.Key)
   ```

3. **Caller receives error** - Goes to error handling path

4. **Budget review state transition never occurs** - The BudgetReviewEffect is stored but never acted upon

## Actual Log Output

```
[coder-002] ERROR: ❌ Hard iteration limit (8) reached for key 'coding_56c6d266' - escalating
[coder-002] INFO: ⚠️  Coding reached max iterations (8, key: coding_56c6d266), triggering budget review
[system] ERROR: toolloop execution failed: escalation handler failed: max iterations reached - budget review required
```

The agent logs indicate budget review should trigger, but the error prevents state transition.

## Toolloop Code (pkg/agent/toolloop/toolloop.go:343-351)

```go
if cfg.Escalation.HardLimit > 0 && currentIteration >= cfg.Escalation.HardLimit {
    tl.logger.Error("❌ Hard iteration limit (%d) reached for key '%s' - escalating", cfg.Escalation.HardLimit, cfg.Escalation.Key)
    if cfg.Escalation.OnHardLimit != nil {
        err := cfg.Escalation.OnHardLimit(ctx, cfg.Escalation.Key, currentIteration)
        if err != nil {
            return "", zero, fmt.Errorf("escalation handler failed: %w", err)
        }
    }
    return "", zero, fmt.Errorf("hard iteration limit (%d) exceeded for key '%s'", cfg.Escalation.HardLimit, cfg.Escalation.Key)
}
```

**Key observation**: Line 351 returns an error even if OnHardLimit returns nil. There's no mechanism for OnHardLimit to signal "don't treat this as an error."

## Options Considered

### Option A: Change Toolloop to Not Return Error if OnHardLimit Returns Nil

**Implementation**:
```go
if cfg.Escalation.HardLimit > 0 && currentIteration >= cfg.Escalation.HardLimit {
    tl.logger.Error("❌ Hard iteration limit (%d) reached for key '%s' - escalating", cfg.Escalation.HardLimit, cfg.Escalation.Key)
    if cfg.Escalation.OnHardLimit != nil {
        err := cfg.Escalation.OnHardLimit(ctx, cfg.Escalation.Key, currentIteration)
        if err != nil {
            return "", zero, fmt.Errorf("escalation handler failed: %w", err)
        }
        // If OnHardLimit succeeded (returned nil), don't treat as error
        // Let CheckTerminal handle state transition via signal
        break
    }
    return "", zero, fmt.Errorf("hard iteration limit (%d) exceeded for key '%s'", cfg.Escalation.HardLimit, cfg.Escalation.Key)
}
```

**Pros**:
- Clean design - allows escalation handler to control behavior
- Uses existing signal mechanism via CheckTerminal
- Escalation handler returning nil means "handled gracefully, continue to CheckTerminal"

**Cons**:
- Breaking change to toolloop contract
- Need to ensure CheckTerminal is called after escalation (currently it breaks the loop)

### Option B: Use CheckTerminal Signal After OnHardLimit

**Implementation**: Have OnHardLimit set state data, then CheckTerminal detects it and returns signal.

**Pros**:
- Uses existing signal mechanism
- No change to toolloop error handling

**Cons**:
- CheckTerminal isn't called after hard limit (early return at line 351)
- Would require changing toolloop to call CheckTerminal before returning error

### Option C: Check State in Error Handler (Currently Implemented with Constant)

**Implementation**:
```go
signal, result, err := toolloop.Run(loop, ctx, cfg)
if err != nil {
    // Check if budget review was triggered
    if budgetEff, exists := sm.GetStateValue(KeyBudgetReviewEffect); exists && budgetEff != nil {
        c.logger.Info("📊 Budget review triggered, transitioning to BUDGET_REVIEW")
        return StateBudgetReview, false, nil
    }
    // Check if this is an empty response error
    if c.isEmptyResponseError(err) {
        req := agent.CompletionRequest{MaxTokens: 8192}
        return c.handleEmptyResponseError(sm, prompt, req, StateCoding)
    }
    return proto.StateError, false, logx.Wrap(err, "toolloop execution failed")
}
```

**Pros**:
- Non-breaking change
- Works with current toolloop design
- Only requires adding constant `KeyBudgetReviewEffect` to avoid magic strings

**Cons**:
- Requires checking state key in multiple places (both coding.go and planning.go)
- Feels like working around toolloop design rather than using it properly
- Tight coupling between toolloop error handling and state data keys

## Question for Expert

**What's the intended design for escalation handlers?**

Should they be able to signal "don't fail, transition to different state" or are they meant only for side effects with the caller always treating hard limits as errors?

The current implementation suggests escalation is always an error (line 351 always returns error), but the OnHardLimit callback pattern suggests handlers might want to handle escalation gracefully without failing the operation.

## Affected Files

- `pkg/coder/coding.go` - executeCodingWithTemplate() handles toolloop.Run() errors
- `pkg/coder/planning.go` - executePlanningWithTemplate() handles toolloop.Run() errors
- `pkg/agent/toolloop/toolloop.go` - Hard limit enforcement at lines 343-351
- `pkg/coder/coder_fsm.go` - Added `KeyBudgetReviewEffect = "budget_review_effect"` constant

## Implementation (Option A - Adopted)

### Changes Made

1. **Added IterationLimitError type** (`pkg/agent/toolloop/toolloop.go`):
   ```go
   type IterationLimitError struct {
       Key       string
       Limit     int
       Iteration int
   }

   func (e *IterationLimitError) Error() string {
       return fmt.Sprintf("iteration limit (%d) exceeded for key %q at iteration %d",
           e.Limit, e.Key, e.Iteration)
   }
   ```

2. **Updated toolloop to return typed error** (line 366):
   ```go
   // Return typed error for explicit control flow
   return "", zero, &IterationLimitError{
       Key:       cfg.Escalation.Key,
       Limit:     cfg.Escalation.HardLimit,
       Iteration: currentIteration,
   }
   ```

3. **Updated callers to handle IterationLimitError** (`pkg/coder/coding.go` and `pkg/coder/planning.go`):
   ```go
   signal, result, err := toolloop.Run(loop, ctx, cfg)
   if err != nil {
       // Check if this is an iteration limit error (normal escalation path)
       var iterErr *toolloop.IterationLimitError
       if errors.As(err, &iterErr) {
           // OnHardLimit already stored BudgetReviewEffect in state
           c.logger.Info("📊 Iteration limit reached (%d iterations), transitioning to BUDGET_REVIEW", iterErr.Iteration)
           return StateBudgetReview, false, nil
       }

       // Other error handling...
   }
   ```

### Benefits

- **Explicit API**: Iteration limits are treated like `io.EOF` - a normal but special termination
- **Type-Safe**: Uses idiomatic Go `errors.As()` for control flow branching
- **Generic Toolloop**: Toolloop remains reusable across all agents without budget review knowledge
- **Maintainable**: State transitions are visible in FSM code, not hidden in error handling
- **No Magic Strings**: No need to check state keys like `KeyBudgetReviewEffect`

### Test Results

All toolloop tests pass, including escalation tests:
- `TestIterationLimit` - Verifies hard limit triggers IterationLimitError
- `TestEscalationSoftLimit` - Verifies soft limit warnings work
- `TestEscalationHardLimit` - Verifies hard limit with escalation handler

### PM Working State (Added Later)

9. **`pkg/pm/working.go`** ✅ - Added escalation configuration
   - Added `errors` import
   - Configured escalation: soft limit (8), hard limit (10)
   - **Special behavior**: PM must call `await_user` with status update before hitting limit
   - On IterationLimitError:
     - If `await_user` was called: Returns `AWAIT_USER` signal (valid completion with status)
     - If `await_user` not called: Returns error (PM must provide status before limit)
   - This ensures PM always provides status updates to user when approaching iteration limits

## Original Status (Historical)

- Added `KeyBudgetReviewEffect` constant to replace magic string
- Received expert feedback recommending typed error approach
- Implemented Option A per expert recommendation
