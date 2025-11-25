# ProcessEffect Proof of Concept

## Overview

This document demonstrates the ProcessEffect pattern implementation using `ask_question` as a proof of concept.

## Design Summary

**Problem**: Tools like `ask_question` need to pause the toolloop for async operations (waiting for architect's answer) without being terminal tools.

**Solution**: Tools return `ProcessEffect` to signal "pause this loop, process async effect, then resume."

## Architecture: Tool vs State Machine Responsibilities

### Core Principle: Tools Are Stateless, State Machines Have Context

**Tools' Responsibility:**
- Extract and validate data from LLM parameters
- Return human-readable messages for LLM context (`ExecResult.Content`)
- Return structured data for state machine processing (`ProcessEffect.Data`)
- **Tools do NOT create Effects** - they don't have enough context

**State Machine's Responsibility:**
- Extract data from `ProcessEffect.Data`
- Add system context (agent ID, story ID, session info)
- **State machine creates Effects** with complete context
- Store data in state for subsequent state transitions

**Why This Separation:**
1. **Tools are pure functions** - No access to agent state, story context, session info
2. **Effects need full context** - For dispatcher routing, persistence, audit trails
3. **State machines orchestrate** - They know who, what, when, why

### Example: ask_question Flow

**Tool (Stateless):**
```go
func (a *AskQuestionTool) Exec(ctx context.Context, args map[string]any) (*ExecResult, error) {
    // Extract from LLM parameters
    question := args["question"].(string)
    context := args["context"].(string)

    return &ExecResult{
        Content: "Question submitted to architect", // For LLM context
        ProcessEffect: &ProcessEffect{
            Signal: "QUESTION",
            Data: map[string]string{  // Raw data for state machine
                "question": question,
                "context":  context,
                "urgency":  urgency,
            },
        },
    }, nil
}
```

**State Machine (Has Context):**
```go
case toolloop.OutcomeProcessEffect:
    if out.Signal == "QUESTION" {
        // Extract raw data from tool
        effectData := out.EffectData.(map[string]string)

        // State machine creates Effect with full context
        effect := effect.NewQuestionEffect(
            c.storyID,              // State machine knows this
            c.GetAgentID(),         // State machine knows this
            effectData["question"], // From tool
            effectData["context"],  // From tool
        )

        // Store and transition
        sm.SetStateData("pending_question", effect)
        return StateQuestion, false, nil
    }
```

## ExecResult Fields: Content vs ProcessEffect.Data

### Content Field - For LLM Context Only

**Purpose:** Human-readable message added to LLM conversation history

**Added to context via:** `contextManager.AddToolResult(toolCall.ID, content, isError)`

**Examples:**
- `"Question submitted to architect"`
- `"Specification accepted and ready for review"`
- `"File created successfully: src/main.go"`
- Empty string = auto-generated "Tool executed successfully"

**NOT for:** Structured data extraction by state machine

### ProcessEffect.Data Field - For State Machine Processing

**Purpose:** Structured data that state machine needs to create Effects or make decisions

**Accessed via:** `out.EffectData` in state machine after `OutcomeProcessEffect`

**Examples:**
- Question details: `{question, context, urgency}`
- Spec content: `{spec_markdown, metadata, bootstrap_params}`
- File paths: `{file_path, content_preview}`

**NOT for:** LLM context (LLM only sees Content string)

### Anti-Pattern: Returning JSON in Content

❌ **Wrong:**
```go
// Tool returns structured data as JSON string in Content
result := map[string]any{
    "spec_markdown": markdown,
    "metadata": metadata,
}
content, _ := json.Marshal(result)
return &ExecResult{Content: string(content)}, nil
```

**Problems:**
1. LLM sees ugly JSON in context instead of human-readable message
2. State machine must parse JSON from `results[]` array (brittle)
3. Violates separation: Content is for LLM, Data is for state machine

✅ **Correct:**
```go
// Tool returns human message in Content, structured data in ProcessEffect.Data
return &ExecResult{
    Content: "Specification submitted successfully",
    ProcessEffect: &ProcessEffect{
        Signal: "SPEC_PREVIEW",
        Data: map[string]any{
            "spec_markdown": markdown,
            "metadata": metadata,
        },
    },
}, nil
```

**Benefits:**
1. LLM sees clean status message
2. State machine extracts typed data from `out.EffectData`
3. Clear separation of concerns

## Key Components

### 1. ProcessEffect Type (`pkg/tools/mcp.go`)

```go
type ProcessEffect struct {
    Signal string // e.g., "QUESTION", "BUDGET_REVIEW", "SPEC_PREVIEW"
    Data   any    // Structured data for state machine (NOT added to LLM context)
}

type ExecResult struct {
    Content       string         // Human-readable message for LLM context
    ProcessEffect *ProcessEffect // nil = continue, non-nil = pause loop
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
