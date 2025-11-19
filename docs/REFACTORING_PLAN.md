# Architect Request Handler Refactoring Plan

## Overview

This document outlines a comprehensive refactoring plan for the architect's request handling system, based on external code review feedback and internal analysis. The goals are:

1. **Eliminate redundancy** - Consolidate duplicate code patterns
2. **Improve maintainability** - Clear separation of concerns
3. **Strengthen core abstractions** - Make toolloop the robust foundation for all LLM interactions
4. **Enhance type safety** - Use constants and helpers instead of magic strings

## Guiding Principles

- **Pre-release freedom**: We can make breaking changes; use the compiler to help us migrate
- **Required over optional**: Prefer required parameters with clear semantics over optional configuration
- **Test core abstractions thoroughly**: Toolloop is the heart of the system and must be rock-solid
- **Strategic over tactical**: Prioritize changes that improve the entire codebase, not just one file

---

## Phase 1: Quick Wins (Low Risk, High Value)

### 1.1 Consolidate Response Formatters

**Files**: `pkg/architect/request.go`

**Problem**: Four nearly-identical functions (`getPlanApprovalResponse`, `getCodeReviewResponse`, `getCompletionResponse`, `getBudgetReviewResponse`) with 95% duplicate code.

**Solution**: Create a single generic formatter with an enum for response types.

```go
type ResponseKind string

const (
    ResponseKindPlan       ResponseKind = "plan"
    ResponseKindCode       ResponseKind = "code"
    ResponseKindCompletion ResponseKind = "completion"
    ResponseKindBudget     ResponseKind = "budget"
)

func (d *Driver) formatApprovalResponse(
    kind ResponseKind,
    status proto.ApprovalStatus,
    feedback string,
    extra map[string]any,
) string
```

**Impact**: Removes ~80 lines of duplication, single place to maintain template logic.

**Effort**: 1-2 hours

---

### 1.2 Centralize Metadata Access

**Files**: `pkg/architect/request.go`, potentially `pkg/proto/message.go`

**Problem**:
- Inconsistent use of `proto.KeyStoryID` vs `"story_id"`
- Repeated correlation ID extraction logic (lines 79-87, 228-236)
- Manual map lookups scattered throughout

**Solution**: Create helper functions for common metadata operations.

```go
// In pkg/proto/message.go or pkg/architect/metadata.go
func GetStoryID(msg *AgentMsg) string
func GetCorrelationID(msg *AgentMsg) string // tries correlation_id, question_id, approval_id
func CopyStoryMetadata(src, dst *AgentMsg)
func CopyCorrelationMetadata(src, dst *AgentMsg)
```

**Impact**: Type-safe metadata access, consistent key usage, easier to refactor metadata structure later.

**Effort**: 2-3 hours

---

### 1.3 Add Const Keys for StateData

**Files**: `pkg/architect/driver.go` (or new `pkg/architect/state.go`)

**Problem**:
- Magic strings for stateData keys (`"current_request"`, `"work_accepted"`, etc.)
- Easy to make typos, hard to refactor
- No discoverability of what keys are used

**Solution**: Define constants for all state keys.

```go
// State data keys
const (
    StateKeyCurrentRequest        = "current_request"
    StateKeyLastResponse          = "last_response"
    StateKeyWorkAccepted          = "work_accepted"
    StateKeyAcceptedStoryID       = "accepted_story_id"
    StateKeyAcceptanceType        = "acceptance_type"
    StateKeySpecApprovedAndLoaded = "spec_approved_and_loaded"
    StateKeyCurrentStoryID        = "current_story_id"
    StateKeySubmitReplyResponse   = "submit_reply_response"
    StateKeyReviewCompleteResult  = "review_complete_result"
    StateKeyEscalationRequestID   = "escalation_request_id"
    StateKeyEscalationStoryID     = "escalation_story_id"
    // Iteration counters use dynamic keys: fmt.Sprintf("approval_iterations_%s", storyID)
)

// Optional: Add typed accessors
func (d *Driver) getCurrentRequest() (*proto.AgentMsg, bool)
func (d *Driver) setCurrentRequest(msg *proto.AgentMsg)
func (d *Driver) markWorkAccepted(storyID, acceptanceType string)
func (d *Driver) clearRequestState()
```

**Rationale**: Keep the map-based stateData (used throughout codebase, supports dynamic keys, survives across LLM calls) but add type safety at access points.

**Impact**: Compiler-enforced key names, easier refactoring, better code completion.

**Effort**: 3-4 hours (finding all usages, updating, testing)

---

### 1.4 Extract Persistence Mappers

**Files**: `pkg/architect/request.go` → new `pkg/architect/persistence.go`

**Problem**:
- Request persistence logic (lines 40-98) embedded in state machine
- Response persistence logic (lines 166-243) embedded in state machine
- Hard to test, hard to modify
- Mixes concerns (routing + persistence)

**Solution**: Create pure functions for mapping messages to persistence structs.

```go
// In pkg/architect/persistence.go
func buildAgentRequestFromMsg(msg *proto.AgentMsg) *persistence.AgentRequest
func buildAgentResponseFromMsg(request, response *proto.AgentMsg) *persistence.AgentResponse
```

**Impact**:
- Testable persistence logic (pure functions)
- Cleaner state machine code
- Single place to change persistence schema

**Effort**: 4-5 hours (extraction, testing)

---

## Phase 2: Toolloop Enhancement (Strategic Core)

### 2.1 Enhanced Toolloop API Design

**Files**: `pkg/agent/toolloop/toolloop.go`

**Problem**:
- Agents manually manage result extraction via stateData
- Escalation logic duplicated in each handler
- No standard pattern for iteration limits
- Optional features make it easy to skip best practices

**Solution**: Make result extraction and escalation handling first-class features of toolloop.

#### New API Design with Generics

**Key Decision: Use Go generics for type-safe result extraction**

Feedback from OpenAI confirms this is an excellent use case for generics:
- Result type varies per call (type parameter, not value parameter)
- Only 3-4 distinct result types across the system
- Call-site knows the desired type at compile-time
- Eliminates runtime type assertions in critical infrastructure

```go
// EscalationHandler is called when hard iteration limit reached
type EscalationHandler func(ctx context.Context, key string, count int) error

// ExtractFunc extracts typed result from tool calls
type ExtractFunc[T any] func(calls []agent.ToolCall, results []any) (T, error)

// Config for toolloop execution - GENERIC over result type T
type Config[T any] struct {
    // Core requirements
    ContextManager *contextmgr.ContextManager
    ToolProvider   ToolProvider

    // Terminal detection (required)
    CheckTerminal func(calls []agent.ToolCall, results []any) string

    // Result extraction (required) - type-safe!
    ExtractResult ExtractFunc[T]

    // Escalation handling (required)
    Escalation *EscalationConfig

    // Limits
    MaxIterations int
    MaxTokens     int

    // Single-turn mode
    SingleTurn bool

    // Optional initial prompt
    InitialPrompt string

    // Agent identification
    AgentID string

    // Debug logging
    DebugLogging bool
}

// EscalationConfig defines iteration limits and escalation behavior
type EscalationConfig struct {
    Key         string            // Unique key for tracking iterations (e.g., "approval_story-123")
    SoftLimit   int              // Warning threshold (e.g., 8)
    HardLimit   int              // Escalation threshold (e.g., 16)
    OnSoftLimit func(count int)  // Optional: called at soft limit
    OnHardLimit EscalationHandler // Required: called at hard limit
}

// ToolLoop remains NON-GENERIC (important!)
type ToolLoop struct {
    llmClient agent.LLMClient
    logger    *logx.Logger
}

// Run is a GENERIC METHOD that returns type-safe results
// The method is generic, not the struct, so ToolLoop can handle any result type
func (tl *ToolLoop) Run[T any](ctx context.Context, cfg Config[T]) (signal string, result T, err error)
```

**Why this shape?**
- ToolLoop struct is not tied to a single result type for its lifetime
- Avoids proliferation of `ToolLoop[string]`, `ToolLoop[Review]`, etc.
- Generic receiver types cannot satisfy interfaces (breaks mocking/testing)
- Type inference works: compiler infers `T` from `cfg`, no need to write `Run[Foo](...)`
- This is the idiomatic Go 1.22-1.24 pattern per OpenAI guidance

#### Migration Strategy

1. Make Config generic: `type Config[T any] struct { ... }`
2. Update Run signature: `func (tl *ToolLoop) Run[T any](ctx context.Context, cfg Config[T]) (string, T, error)`
3. Define result types for each use case (see examples below)
4. Update all call sites (compiler will catch them)
5. Remove all manual type assertions like `result.(string)`

**Breaking Changes**: Yes, but pre-release and compiler-enforced

**Impact**:
- ✅ Toolloop becomes the robust foundation for all LLM interactions
- ✅ Escalation handling tested once, applied everywhere
- ✅ No more manual stateData management for results
- ✅ Patterns enforced by framework
- ✅ Easy to add telemetry/metrics centrally

**Effort**: 8-10 hours
- Design: 2 hours
- Implementation: 4 hours
- Migration (architect, coder, PM): 3 hours
- Testing: 1 hour

---

### 2.2 Migrate Architect Handlers to Enhanced Toolloop

**Files**: `pkg/architect/request.go`

**Before** (handleIterativeApproval):
```go
func (d *Driver) handleIterativeApproval(...) (*proto.AgentMsg, error) {
    // 1. Build prompt
    prompt := d.generateCodePrompt(...)

    // 2. Reset context
    d.contextManager.ResetForNewTemplate(templateName, prompt)

    // 3. Define CheckTerminal with manual result storage
    checkTerminal := func(calls []agent.ToolCall, _ []any) string {
        for i := range calls {
            if calls[i].Name == tools.ToolSubmitReply {
                if response, ok := calls[i].Parameters["response"].(string); ok {
                    d.stateData["submit_reply_response"] = response
                    return "SUBMIT_REPLY"
                }
            }
        }
        return ""
    }

    // 4. Define OnIterationLimit with manual escalation
    onIterationLimit := func(_ context.Context) (string, error) {
        iterationKey := fmt.Sprintf("approval_iterations_%s", storyID)
        if d.checkIterationLimit(iterationKey, StateRequest) {
            d.stateData["escalation_story_id"] = storyID
            return "", ErrEscalationTriggered
        }
        return "", fmt.Errorf("max iterations exceeded")
    }

    // 5. Run toolloop
    signal, err := d.toolLoop.Run(ctx, &toolloop.Config{...})

    // 6. Extract result from stateData
    submitResponse, ok := d.stateData["submit_reply_response"]
    // ... type assertions, error handling ...

    // 7. Clean up stateData
    delete(d.stateData, "submit_reply_response")

    // 8. Build response
    return d.buildApprovalResponseFromSubmit(...)
}
```

**After (with generics)**:
```go
// Define result type for submit_reply tool (once, at package level)
type SubmitReplyResult struct {
    Response string
}

func (d *Driver) handleIterativeApproval(...) (*proto.AgentMsg, error) {
    // 1. Build prompt
    prompt := d.generateCodePrompt(...)

    // 2. Run toolloop with type-safe extraction and escalation
    signal, result, err := d.toolLoop.Run(ctx, toolloop.Config[SubmitReplyResult]{
        ContextManager: d.contextManager,
        ToolProvider:   toolProvider,
        InitialPrompt:  prompt,

        CheckTerminal: func(calls []agent.ToolCall, _ []any) string {
            for _, call := range calls {
                if call.Name == tools.ToolSubmitReply {
                    return "SUBMIT_REPLY"
                }
            }
            return ""
        },

        // Type-safe extraction - returns SubmitReplyResult, not any
        ExtractResult: func(calls []agent.ToolCall, _ []any) (SubmitReplyResult, error) {
            for _, call := range calls {
                if call.Name == tools.ToolSubmitReply {
                    if response, ok := call.Parameters["response"].(string); ok && response != "" {
                        return SubmitReplyResult{Response: response}, nil
                    }
                }
            }
            return SubmitReplyResult{}, fmt.Errorf("submit_reply response not found")
        },

        Escalation: &toolloop.EscalationConfig{
            Key:       fmt.Sprintf("approval_%s", storyID),
            SoftLimit: 8,
            HardLimit: 16,
            OnHardLimit: func(ctx context.Context, key string, count int) error {
                d.stateData[StateKeyEscalationStoryID] = storyID
                return ErrEscalationTriggered
            },
        },

        MaxIterations: 20,
        MaxTokens:     agent.ArchitectMaxTokens,
        AgentID:       d.architectID,
    })

    if err != nil {
        return nil, fmt.Errorf("iterative approval failed: %w", err)
    }

    // 3. Build response - NO TYPE ASSERTION NEEDED!
    //    result is already SubmitReplyResult, fully type-safe
    return d.buildApprovalResponseFromSubmit(ctx, requestMsg, approvalPayload, result.Response)
}
```

**Result Types to Define:**

```go
// In pkg/architect/results.go (new file)

// SubmitReplyResult is returned from submit_reply tool calls (code/completion review)
type SubmitReplyResult struct {
    Response string
}

// ReviewCompleteResult is returned from review_complete tool calls (plan/budget review)
type ReviewCompleteResult struct {
    Status   string
    Feedback string
}

// SpecFeedbackResult is returned from spec review operations
type SpecFeedbackResult struct {
    Approved bool
    Feedback string
    Stories  []string
}
```

**Key Benefits:**
- ✅ Zero runtime type assertions at call sites
- ✅ Compiler catches mismatched types
- ✅ Clear documentation of what each handler produces
- ✅ Better IDE autocomplete (knows `result.Response` exists)
- ✅ Safe refactoring (rename `Response` field, compiler finds all uses)
```

**Handlers to migrate**:
- `handleIterativeApproval` (code + completion review)
- `handleSingleTurnReview` (plan + budget review)
- `handleIterativeQuestion`
- Potentially `handleQuestionRequest` (fallback mode)

**Impact**:
- ~50 lines removed per handler
- No more manual stateData management
- Consistent escalation handling
- Easier to understand flow

**Effort**: 4-5 hours (after toolloop enhancement is complete)

---

## Phase 3: Bigger Structural Refactorings

### 3.1 Split handleRequest into Pipeline

**Files**: `pkg/architect/request.go`

**Problem**: `handleRequest` does too much (routing, persistence, side effects, state transitions).

**Solution**: Break into smaller functions:

```go
// Main orchestrator
func (d *Driver) handleRequest(ctx context.Context) (proto.State, error) {
    // 1. Load state
    requestMsg, err := d.loadCurrentRequest()

    // 2. Persist incoming request (fire-and-forget)
    d.persistIncomingRequest(requestMsg)

    // 3. Route and handle
    response, err := d.routeRequest(ctx, requestMsg)

    // 4. Handle escalation
    if errors.Is(err, ErrEscalationTriggered) {
        return StateEscalated, nil
    }
    if err != nil {
        return StateError, err
    }

    // 5. Send and persist response
    if response != nil {
        d.sendAndPersistResponse(ctx, requestMsg, response)
    }

    // 6. Compute next state
    return d.computeNextState()
}

// Pipeline steps (mostly pure functions)
func (d *Driver) loadCurrentRequest() (*proto.AgentMsg, error)
func (d *Driver) persistIncomingRequest(msg *proto.AgentMsg)
func (d *Driver) routeRequest(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error)
func (d *Driver) sendAndPersistResponse(ctx context.Context, request, response *proto.AgentMsg)
func (d *Driver) computeNextState() (proto.State, error)
```

**Impact**:
- Clear separation of concerns
- Testable individual steps
- Easier to modify routing or persistence independently

**Effort**: 6-8 hours

---

### 3.2 Unify Build Approval Response Functions

**Files**: `pkg/architect/request.go`

**Problem**:
- `buildApprovalResponseFromReviewComplete` (lines 631-688)
- `buildApprovalResponseFromSubmit` (lines 1073-1135)
- Share ~60% of code (ApprovalResult creation, work acceptance, message building)

**Solution**: Create shared helper that takes parsed status and feedback.

```go
// Shared response builder
func (d *Driver) buildApprovalResponse(
    ctx context.Context,
    requestMsg *proto.AgentMsg,
    payload *proto.ApprovalRequestPayload,
    status proto.ApprovalStatus,
    feedback string,
    source string, // "review_complete" or "submit_reply"
) (*proto.AgentMsg, error)

// Thin wrappers that parse and delegate
func (d *Driver) buildApprovalResponseFromReviewComplete(...) (*proto.AgentMsg, error) {
    status := parseStatusString(statusStr)
    return d.buildApprovalResponse(ctx, requestMsg, payload, status, feedback, "review_complete")
}

func (d *Driver) buildApprovalResponseFromSubmit(...) (*proto.AgentMsg, error) {
    status, feedback := parseSubmitReply(submitResponse)
    return d.buildApprovalResponse(ctx, requestMsg, payload, status, feedback, "submit_reply")
}
```

**Impact**:
- DRY (Don't Repeat Yourself)
- Single place for ApprovalResult logic
- Consistent work acceptance handling

**Effort**: 3-4 hours

---

### 3.3 Normalize Approval Status Parsing

**Files**: `pkg/architect/request.go` or `pkg/proto/approval.go`

**Problem**: Status parsing (`APPROVED`, `NEEDS_CHANGES`, `REJECTED`) happens in multiple places with slight variations.

**Solution**: Centralize parsing logic.

```go
// In pkg/proto/approval.go
func ParseApprovalStatus(raw string) (status ApprovalStatus, feedback string) {
    responseUpper := strings.ToUpper(raw)

    if strings.HasPrefix(responseUpper, "APPROVED") {
        status = ApprovalStatusApproved
        feedback = strings.TrimSpace(strings.TrimPrefix(raw, "APPROVED"))
    } else if strings.HasPrefix(responseUpper, "NEEDS_CHANGES") {
        status = ApprovalStatusNeedsChanges
        feedback = strings.TrimSpace(strings.TrimPrefix(raw, "NEEDS_CHANGES"))
    } else if strings.HasPrefix(responseUpper, "REJECTED") {
        status = ApprovalStatusRejected
        feedback = strings.TrimSpace(strings.TrimPrefix(raw, "REJECTED"))
    } else {
        status = ApprovalStatusNeedsChanges
        feedback = raw
    }

    feedback = strings.TrimPrefix(feedback, ":")
    feedback = strings.TrimSpace(feedback)
    return
}
```

**Impact**: Consistent status interpretation across all handlers.

**Effort**: 1-2 hours

---

## Phase 4: Optional Future Improvements

### 4.1 Approval Strategy Pattern

**Context**: Only consider if `handleApprovalRequest` remains complex after Phase 3.

**Solution**: Create per-type approval handlers:
- `PlanApprovalHandler`
- `CodeApprovalHandler`
- `CompletionApprovalHandler`
- `BudgetReviewHandler`

**Decision**: Defer until after Phase 3 to see if still needed.

---

## Testing Strategy

### Phase 1 Tests
- Unit tests for metadata helpers
- Unit tests for response formatters
- Unit tests for persistence mappers
- Integration test: full request flow still works

### Phase 2 Tests
- **Extensive unit tests for enhanced toolloop** (this is critical infrastructure)
- Test escalation at soft/hard limits
- Test result extraction for various tool outputs
- Integration tests for each migrated handler
- End-to-end test: architect handles question, code review, plan review

### Phase 3 Tests
- Unit tests for pipeline functions
- Integration test: full request pipeline
- Regression tests: all existing scenarios still work

---

## Success Metrics

**Code Quality**:
- Reduce `request.go` from 1314 lines to <800 lines
- Eliminate >90% of code duplication
- Zero magic strings for metadata/state keys

**Maintainability**:
- New LLM interactions take <30 min to implement (just plug into toolloop)
- Persistence changes only require touching mapper functions
- State transitions clearly separated from business logic

**Robustness**:
- Toolloop tested thoroughly with >90% coverage
- All LLM interactions have consistent escalation handling
- No more manual result extraction bugs

---

## Timeline Estimate

| Phase | Description | Effort | Dependencies |
|-------|-------------|--------|--------------|
| 1.1 | Response formatters | 1-2h | None |
| 1.2 | Metadata helpers | 2-3h | None |
| 1.3 | Const keys | 3-4h | None |
| 1.4 | Persistence mappers | 4-5h | 1.2 (metadata helpers) |
| **Phase 1 Total** | **Quick Wins** | **10-14h** | |
| 2.1 | Toolloop enhancement | 8-10h | None (parallel with Phase 1) |
| 2.2 | Migrate handlers | 4-5h | 2.1 |
| **Phase 2 Total** | **Toolloop Strategic** | **12-15h** | |
| 3.1 | Pipeline refactor | 6-8h | 1.4, 2.2 |
| 3.2 | Unify build response | 3-4h | 2.2 |
| 3.3 | Normalize parsing | 1-2h | None |
| **Phase 3 Total** | **Structural** | **10-14h** | |
| **Grand Total** | | **32-43h** | |

---

## Open Questions

1. **Toolloop state management**: Should toolloop own iteration counting internally, or continue to use external state?
   - **Answer**: Toolloop should own it via EscalationConfig.Key - this makes it fully encapsulated

2. **ExtractResult nil handling**: Should nil result be valid (for fire-and-forget) or should we require a NoResult sentinel?
   - **Answer**: nil is valid and clear; document this in godoc

3. **Metadata helpers location**: `pkg/proto` or `pkg/architect`?
   - **Answer**: Start in `pkg/proto` if they're generic, move if architect-specific patterns emerge

4. **Breaking changes communication**: How do we track and communicate toolloop API changes?
   - **Answer**: This document + commit messages + update CHANGELOG.md

---

## Notes

- This plan represents the consensus view from external reviewer feedback and internal analysis
- We're pre-release so breaking changes are acceptable
- Priority is robustness and maintainability over backwards compatibility
- Compiler-enforced migrations are better than optional features
