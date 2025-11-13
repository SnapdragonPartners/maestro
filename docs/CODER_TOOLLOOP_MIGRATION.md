# Coder Toolloop Migration Plan

## Overview

Migrate the Coder agent to use the unified `toolloop` abstraction while fixing architectural issues with blocking Effects. This migration brings the Coder in line with PM and Architect agents and establishes proper state machine architecture.

## Current Architecture Problems

### Problem 1: Blocking Effects Instead of State Transitions

**Current (Wrong):**
```
PLANNING: ask_question tool called
  → Blocks via ExecuteEffect, waits for answer
  → Adds answer to context
  → Continues in PLANNING state

CODING: done tool called
  → Blocks via ExecuteEffect
  → Sets completion signal in state
  → Continues in CODING state
  → Later checks signal → transitions to TESTING
```

**Why this is wrong:**
- Violates state machine principles (states should handle state, not tools)
- Tools block the iteration loop
- Hidden control flow (signal checking after tool execution)
- Cannot leverage toolloop's terminal detection

### Problem 2: Manual Tool Iteration Loops

**Current approach:**
- `handlePlanning()` - Manual loop: build messages → call LLM → process tool calls
- `handleCoding()` → `executeMCPToolCalls()` - Manual tool execution with special cases
- Duplicate iteration logic across states
- Inconsistent with PM and Architect agents

### Problem 3: Complex Tool Execution with Special Cases

**In `processPlanningToolCalls()` and `executeMCPToolCalls()`:**
- Special handling for `ask_question` (blocking Effect)
- Special handling for `done` (blocking Effect + signal)
- Special handling for todo tools (state manipulation)
- Mixed concerns: tool execution + state transitions + context management

## Target Architecture

### Correct Architecture

**ask_question flow:**
```
PLANNING/CODING: ask_question tool called
  → CheckTerminal detects ask_question
  → Returns "QUESTION" signal
  → Toolloop exits, returns signal
  → State machine transitions to QUESTION state

QUESTION state:
  → Sends REQUEST to architect
  → Waits for RESULT (ANSWER)
  → Stores answer in state
  → Transitions back to origin state (PLANNING or CODING)

Return to PLANNING/CODING:
  → Answer available in state/context
  → Continues with new iteration
```

**done tool flow:**
```
CODING: done tool called
  → Tool validates todos are complete
  → CheckTerminal detects done tool
  → Returns "TESTING" signal
  → Toolloop exits, returns signal
  → State machine transitions to TESTING
```

**submit_plan flow (already correct):**
```
PLANNING: submit_plan tool called
  → Tool returns structured plan data
  → CheckTerminal detects submit_plan
  → Returns "PLAN_REVIEW" signal
  → State machine transitions to PLAN_REVIEW
```

### Unified Tool Loop Pattern

All agents use the same pattern:
1. Create `toolloop.Config` with ToolProvider, CheckTerminal, OnIterationLimit
2. Call `loop.Run(ctx, cfg)`
3. Handle returned signal for state transitions
4. ToolProvider executes tools normally
5. CheckTerminal examines tool calls + results for terminal conditions

## Migration Phases

### Phase 1: Install Toolloop (Simple)

**Goal:** Replace manual iteration loops with toolloop while preserving all existing behavior.

**Changes:**
- `planning.go`:
  - Import `"orchestrator/pkg/agent/toolloop"`
  - Replace manual LLM loop with `toolloop.New()` and `loop.Run()`
  - Implement `checkPlanningTerminal()` callback
  - Handle returned signals for state transitions
  - Remove `processPlanningToolCalls()` iteration - but keep tool execution logic temporarily

- `coding.go`:
  - Import `"orchestrator/pkg/agent/toolloop"`
  - Replace manual LLM loop with `toolloop.New()` and `loop.Run()`
  - Implement `checkCodingTerminal()` callback
  - Handle returned signals for state transitions
  - Remove `executeMCPToolCalls()` iteration - but keep tool execution logic temporarily

**Estimated changes:** ~150 lines (replace manual loops)

**Testing:** Existing coder functionality should work unchanged

### Phase 2: Fix ask_question Tool (Medium)

**Goal:** Remove blocking Effect from ask_question, use proper QUESTION state.

**Current behavior:**
```go
// In processPlanningToolCalls / executeMCPToolCalls
if toolCall.Name == tools.ToolAskQuestion {
    // Execute blocking Effect
    eff := effect.NewQuestionEffect(...)
    result, err := c.ExecuteEffect(ctx, eff) // BLOCKS HERE

    // Add answer to context
    qaContent := fmt.Sprintf("Question: %s\nAnswer: %s", question, answer)
    c.contextManager.AddMessage("architect-answer", qaContent)
    continue
}
```

**New behavior:**

1. **Remove blocking from tool execution:**
   - Tools execute normally via ToolProvider
   - ask_question tool just validates parameters and returns success

2. **CheckTerminal detects ask_question:**
```go
func (c *Coder) checkPlanningTerminal(ctx context.Context, sm *agent.BaseStateMachine, calls []agent.ToolCall, results []any) string {
    for i := range calls {
        if calls[i].Name == tools.ToolAskQuestion {
            // Extract question from tool call parameters
            question := calls[i].Parameters["question"].(string)
            context := calls[i].Parameters["context"].(string)
            urgency := calls[i].Parameters["urgency"].(string)

            // Store question data in state for QUESTION state to use
            sm.SetStateData("pending_question", map[string]any{
                "question": question,
                "context": context,
                "urgency": urgency,
                "origin": "PLANNING", // or "CODING"
            })

            return "QUESTION" // Signal state transition
        }

        // Check other terminal tools...
    }
    return "" // Continue loop
}
```

3. **Create/fix QUESTION state handler:**
```go
// In coder_fsm.go or question.go
func (c *Coder) handleQuestion(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
    // Get pending question from state
    questionData := sm.GetStateValue("pending_question")

    // Send REQUEST to architect
    eff := effect.NewQuestionEffect(question, context, urgency, origin)
    result, err := c.ExecuteEffect(ctx, eff)

    if err != nil {
        return proto.StateError, false, err
    }

    // Process answer
    if questionResult, ok := result.(*effect.QuestionResult); ok {
        // Add Q&A to context so LLM can see it
        qaContent := fmt.Sprintf("Question: %s\nAnswer: %s", question, questionResult.Answer)
        c.contextManager.AddMessage("architect-answer", qaContent)
    }

    // Transition back to origin state
    origin := questionData["origin"].(string)
    if origin == "PLANNING" {
        return StatePlanning, false, nil
    }
    return StateCoding, false, nil
}
```

4. **Register QUESTION state in FSM:**
```go
// In coder_fsm.go
StateQuestion: func(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
    return c.handleQuestion(ctx, sm)
},
```

**Estimated changes:** ~150 lines (new state handler + CheckTerminal logic)

**Testing:**
- ask_question in PLANNING → transitions to QUESTION → gets answer → back to PLANNING
- ask_question in CODING → transitions to QUESTION → gets answer → back to CODING

### Phase 3: Fix done Tool (Easy)

**Goal:** Remove blocking Effect from done tool, use CheckTerminal for immediate transition.

**Current behavior:**
```go
// In executeMCPToolCalls
if toolCall.Name == tools.ToolDone {
    // Validate todos complete
    if incompleteTodos > 0 {
        c.contextManager.AddMessage("error", "Complete todos first")
        continue
    }

    // Execute blocking Effect
    completionEff := effect.NewCompletionEffect(...)
    result, err := c.ExecuteEffect(ctx, completionEff)

    // Store signal for later checking
    sm.SetStateData(KeyCompletionSignaled, completionResult)
}

// Later in handleCoding:
if completionData, exists := sm.GetStateValue(KeyCompletionSignaled); exists {
    return completionResult.TargetState, false, nil
}
```

**New behavior:**

1. **done tool validates and returns:**
```go
// In tools/done_tool.go
func (d *DoneTool) Exec(ctx context.Context, args map[string]any) (any, error) {
    summary := args["summary"].(string)

    // Validate summary provided
    if summary == "" {
        return nil, fmt.Errorf("summary is required")
    }

    // Return success - CheckTerminal will handle transition
    return map[string]any{
        "success": true,
        "summary": summary,
        "next_state": "TESTING",
    }, nil
}
```

2. **CheckTerminal detects done and validates todos:**
```go
func (c *Coder) checkCodingTerminal(ctx context.Context, sm *agent.BaseStateMachine, calls []agent.ToolCall, results []any) string {
    for i := range calls {
        if calls[i].Name == tools.ToolDone {
            // Check if all todos are complete before allowing transition
            if c.todoList != nil {
                incompleteTodos := []TodoItem{}
                for _, todo := range c.todoList.Items {
                    if !todo.Completed {
                        incompleteTodos = append(incompleteTodos, todo)
                    }
                }

                if len(incompleteTodos) > 0 {
                    // Block completion - add error to context
                    errorMsg := fmt.Sprintf("Cannot mark story as done: %d todos not complete", len(incompleteTodos))
                    c.contextManager.AddMessage("tool-error", errorMsg)
                    continue // Don't signal transition, continue loop
                }
            }

            // All todos complete - store summary and signal transition
            if resultMap, ok := results[i].(map[string]any); ok {
                if summary, ok := resultMap["summary"].(string); ok {
                    sm.SetStateData(KeyCompletionDetails, summary)
                }
            }

            return "TESTING" // Signal transition to TESTING
        }

        // Check other terminal tools...
    }
    return "" // Continue loop
}
```

3. **Remove signal checking from handleCoding:**
```go
// DELETE this code:
if completionData, exists := sm.GetStateValue(KeyCompletionSignaled); exists {
    if completionResult, ok := completionData.(*effect.CompletionResult); ok {
        return completionResult.TargetState, false, nil
    }
}
```

**Estimated changes:** ~50 lines removed, ~30 lines added to CheckTerminal

**Testing:**
- done tool with incomplete todos → error message, stays in CODING
- done tool with all todos complete → transitions to TESTING
- Completion summary stored correctly

### Phase 4: Cleanup and Simplification

**Goal:** Remove obsolete code and consolidate logic.

**Changes:**
1. Remove `processPlanningToolCalls()` if no longer needed
2. Remove `executeMCPToolCalls()` if no longer needed
3. Remove `KeyCompletionSignaled` and related signal checking
4. Remove blocking Effect execution from tool paths
5. Consolidate CheckTerminal logic

**Estimated changes:** ~200 lines removed

## Detailed Implementation Steps

### Phase 1 Implementation

#### Step 1.1: Update planning.go

```go
// Add import
import (
    // ... existing imports
    "orchestrator/pkg/agent/toolloop"
)

func (c *Coder) handlePlanning(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
    // ... existing setup (budget check, knowledge pack, template rendering) ...

    // Use toolloop for LLM iteration
    loop := toolloop.New(c.llmClient, c.logger)

    cfg := &toolloop.Config{
        ContextManager: c.contextManager,
        InitialPrompt:  "", // Prompt already in context via ResetForNewTemplate
        ToolProvider:   c.planningToolProvider,
        MaxIterations:  maxPlanningIterations,
        MaxTokens:      8192,
        AgentID:        c.agentID,
        DebugLogging:   false,
        CheckTerminal: func(calls []agent.ToolCall, results []any) string {
            return c.checkPlanningTerminal(ctx, sm, calls, results)
        },
        OnIterationLimit: func(ctx context.Context) (string, error) {
            c.logger.Info("⚠️  Planning reached max iterations, triggering budget review")
            sm.SetStateData("budget_review_effect", effect.NewBudgetReviewEffect(string(StatePlanning), maxPlanningIterations))
            return "BUDGET_REVIEW", nil
        },
    }

    signal, err := loop.Run(ctx, cfg)
    if err != nil {
        if c.isEmptyResponseError(err) {
            req := agent.CompletionRequest{MaxTokens: 8192}
            return c.handleEmptyResponseError(sm, prompt, req, StatePlanning)
        }
        return proto.StateError, false, logx.Wrap(err, "toolloop execution failed")
    }

    // Handle terminal signals
    switch signal {
    case "BUDGET_REVIEW":
        return StateBudgetReview, false, nil
    case "PLAN_REVIEW":
        return StatePlanReview, false, nil
    case "QUESTION":
        return StateQuestion, false, nil // Phase 2 addition
    case "":
        // No signal, continue planning
        return StatePlanning, false, nil
    default:
        c.logger.Warn("Unknown signal from toolloop: %s", signal)
        return StatePlanning, false, nil
    }
}

// Implement CheckTerminal callback
func (c *Coder) checkPlanningTerminal(ctx context.Context, sm *agent.BaseStateMachine, calls []agent.ToolCall, results []any) string {
    for i := range calls {
        toolCall := &calls[i]

        // Phase 1: Only check submit_plan and mark_story_complete
        // These already work correctly via handleToolStateTransition

        resultMap, ok := results[i].(map[string]any)
        if !ok {
            continue
        }

        // Check for next_state signal
        if nextState, hasNextState := resultMap["next_state"]; hasNextState {
            if nextStateStr, ok := nextState.(string); ok {
                // Process via existing handler
                newState, _, err := c.handleToolStateTransition(ctx, sm, toolCall.Name, nextStateStr, resultMap)
                if err != nil {
                    c.logger.Error("Error handling tool state transition: %v", err)
                    continue
                }

                // Map state to signal
                switch newState {
                case StatePlanReview:
                    return "PLAN_REVIEW"
                default:
                    return string(newState)
                }
            }
        }

        // Phase 2: Add ask_question detection here
        // Phase 3: Not applicable to planning
    }

    return "" // Continue loop
}
```

#### Step 1.2: Update coding.go

```go
// Add import
import (
    // ... existing imports
    "orchestrator/pkg/agent/toolloop"
)

func (c *Coder) handleInitialCoding(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
    // ... existing setup (budget check, template rendering) ...

    // Use toolloop for LLM iteration
    loop := toolloop.New(c.llmClient, c.logger)

    cfg := &toolloop.Config{
        ContextManager: c.contextManager,
        InitialPrompt:  "", // Prompt already in context
        ToolProvider:   c.codingToolProvider,
        MaxIterations:  8, // maxCodingIterations
        MaxTokens:      8192,
        AgentID:        c.agentID,
        DebugLogging:   false,
        ToolChoice:     "any", // Force tool use
        CheckTerminal: func(calls []agent.ToolCall, results []any) string {
            return c.checkCodingTerminal(ctx, sm, calls, results)
        },
        OnIterationLimit: func(ctx context.Context) (string, error) {
            c.logger.Info("⚠️  Coding reached max iterations, triggering budget review")
            sm.SetStateData("budget_review_effect", effect.NewBudgetReviewEffect(string(StateCoding), 8))
            return "BUDGET_REVIEW", nil
        },
    }

    signal, err := loop.Run(ctx, cfg)
    if err != nil {
        if c.isEmptyResponseError(err) {
            req := agent.CompletionRequest{MaxTokens: 8192}
            return c.handleEmptyResponseError(sm, prompt, req, StateCoding)
        }
        return proto.StateError, false, logx.Wrap(err, "toolloop execution failed")
    }

    // Handle terminal signals
    switch signal {
    case "BUDGET_REVIEW":
        return StateBudgetReview, false, nil
    case "TESTING":
        return StateTesting, false, nil
    case "QUESTION":
        return StateQuestion, false, nil // Phase 2 addition
    case "":
        // No signal, continue coding
        return StateCoding, false, nil
    default:
        c.logger.Warn("Unknown signal from toolloop: %s", signal)
        return StateCoding, false, nil
    }
}

// Implement CheckTerminal callback
func (c *Coder) checkCodingTerminal(ctx context.Context, sm *agent.BaseStateMachine, calls []agent.ToolCall, results []any) string {
    for i := range calls {
        toolCall := &calls[i]

        // Phase 1: No special detection yet
        // Phase 2: Add ask_question detection
        // Phase 3: Add done tool detection with todo validation
    }

    return "" // Continue loop
}
```

**Note:** In Phase 1, toolloop will execute tools via ToolProvider, including ask_question and done. These will still block temporarily. We'll fix this in Phase 2 and 3.

### Phase 2 Implementation

Add to checkPlanningTerminal and checkCodingTerminal:

```go
// Detect ask_question before tool execution completes
if toolCall.Name == tools.ToolAskQuestion {
    question := utils.GetMapFieldOr[string](toolCall.Parameters, "question", "")
    context := utils.GetMapFieldOr[string](toolCall.Parameters, "context", "")
    urgency := utils.GetMapFieldOr[string](toolCall.Parameters, "urgency", "medium")

    // Store question for QUESTION state
    sm.SetStateData("pending_question", map[string]any{
        "question": question,
        "context":  context,
        "urgency":  urgency,
        "origin":   "PLANNING", // or "CODING"
    })

    return "QUESTION"
}
```

Create handleQuestion() and register in FSM.

### Phase 3 Implementation

Add to checkCodingTerminal:

```go
if toolCall.Name == tools.ToolDone {
    // Validate todos complete
    if c.todoList != nil && !c.allTodosComplete() {
        errorMsg := "Cannot mark story as done: incomplete todos"
        c.contextManager.AddMessage("tool-error", errorMsg)
        continue // Don't transition, stay in CODING
    }

    // Store completion summary
    if resultMap, ok := results[i].(map[string]any); ok {
        if summary, ok := resultMap["summary"].(string); ok {
            sm.SetStateData(KeyCompletionDetails, summary)
        }
    }

    return "TESTING"
}
```

## Testing Strategy

### Phase 1 Testing
- Run existing coder test suite
- Manual test: PLANNING → uses tools → submits plan
- Manual test: CODING → uses tools → calls done → goes to TESTING
- Verify no regressions in tool execution

### Phase 2 Testing
- Unit test: ask_question in PLANNING → QUESTION state
- Unit test: ask_question in CODING → QUESTION state
- Unit test: QUESTION state sends REQUEST, receives ANSWER
- Manual test: Full question flow end-to-end

### Phase 3 Testing
- Unit test: done with incomplete todos → stays in CODING
- Unit test: done with complete todos → goes to TESTING
- Unit test: Completion summary stored correctly
- Manual test: Full coding flow with done tool

### Integration Testing
- Run full story: PLANNING → ask question → CODING → done → TESTING
- Verify all state transitions work correctly
- Verify context contains expected messages
- Verify architect receives and responds to questions

## Risks and Mitigation

### Risk 1: Breaking Existing Coder Functionality
**Mitigation:**
- Implement in phases with testing at each step
- Keep existing tool execution logic in Phase 1
- Only remove after verifying toolloop works

### Risk 2: ask_question Blocking Behavior Changes
**Mitigation:**
- QUESTION state preserves blocking behavior (waits for ANSWER)
- Test question flow thoroughly before deploying
- Can rollback to Phase 1 if issues

### Risk 3: Todo State Management Breaks
**Mitigation:**
- Todo tools don't change in Phase 1
- Phase 3 only moves validation logic, doesn't change behavior
- Test todo completion thoroughly

### Risk 4: Unforeseen Tool Interactions
**Mitigation:**
- Comprehensive testing at each phase
- Manual testing with real stories
- Keep git history clean for easy rollback

## Success Criteria

- [ ] Coder uses toolloop for iteration (consistent with PM and Architect)
- [ ] ask_question uses QUESTION state (no blocking in tool execution)
- [ ] done tool triggers immediate transition via CheckTerminal
- [ ] All existing coder functionality works correctly
- [ ] State machine architecture is clean and maintainable
- [ ] Code is simpler than before (net reduction in lines)

## Timeline Estimate

- **Phase 1:** 2-3 hours (implementation + testing)
- **Phase 2:** 2-3 hours (QUESTION state + testing)
- **Phase 3:** 1-2 hours (done tool + testing)
- **Phase 4:** 1 hour (cleanup)
- **Total:** 6-9 hours

## References

- `docs/TOOL_LOOP.md` - Toolloop architecture and usage
- `pkg/agent/toolloop/toolloop.go` - Toolloop implementation
- `pkg/pm/working.go` - PM toolloop usage example
- `pkg/architect/driver.go` - Architect toolloop usage example
