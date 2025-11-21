
# Multi-Agent Go System Review & Refactor Plan

This document summarizes an architectural/code-quality review of the three agents (`arch`, `coder`, `pm`) plus the shared `toolloop` package, and proposes a concrete refactor plan.

The emphasis is on:

- DRYness and clarity of responsibilities
- Removing dead code and debug cruft
- Consistent, robust behavior aligned with Go idioms
- A **clean break** for `toolloop` with compiler-enforced migration
- Aligning with your operational constraints:
  - Agents run as unattended as possible
  - Failure is expensive; human escalation is preferred over failing
  - Token budgets and rate limits are enforced elsewhere (LLM middleware)
  - Iteration limits here are about preventing infinite loops and enforcing “meaningful progress”

---

## 1. How the three agents differ architecturally

### 1.1 Common foundation

Across all three agents, there is a clear shared foundation:

- All embed `*agent.BaseStateMachine` and use it as the single source of truth for state and state data.
- All use a consistent `ProcessState` / `executeState` / `processCurrentState` pattern with a `switch` over `GetCurrentState()`, returning `(nextState, done, err)`.
- All use the same pattern of **effects + runtime** (`effect.BaseRuntime`, `ExecuteEffect`, “request/result” agent messages) to interact with the dispatcher/chat service.
- All use **toolloop** with type-parameterized `Config[T]` for LLM tooling, plus `CheckTerminal` and `ExtractResult` functions.

So the migration to a unified state machine + tool-based LLM interaction is real and coherent.

---

### 1.2 Architect agent

Architect is the **approval and review hub**:

- It doesn’t own a long-running “story workspace”; instead it dynamically creates read-only tool providers pointed into coder workspaces (e.g. `createReadToolProviderForCoder`).
- It has two distinct flavors of LLM interactions:
  - **Iterative approvals** (code, completion) with rich exploration via tools rooted at `/mnt/coders/<coder>` workspaces.
  - **Single-turn reviews** (plans, budget reviews) using dedicated tools like `review_complete`, enforced with `SingleTurn: true` and small `MaxIterations`.
- It owns **spec review and question answering flows**:
  - `generatePlanPrompt` / `generateBudgetPrompt` / `generateQuestionPrompt`, each tightly coupled to specific tool sets and templates, with “you MUST call <tool>`” instructions.
  - Tool extractors like `ExtractSubmitReply`, `ExtractSpecReview`, `ExtractReviewComplete` that decode tool results into typed structs.
- It has its own **iteration-based escalation** for LLM usage (`EscalationConfig` in toolloop), plus explicit state keys for escalation tracking, e.g. `StateKeyEscalationRequestID`, `StateKeyEscalationStoryID`, and `StateKeyPattern*` iteration keys.

Conceptually: Architect is a **service-style** agent that reacts to incoming requests, runs bounded tool-based exploration, and returns typed approval/answer messages—mostly stateless between stories except for escalation tracking and some metrics.

Operationally:

- Architect is a singleton serving many coders.
- Architect context is intentionally short-lived (per-state, per-request).
- Architect failures are currently treated as fatal for the overall app, but the long-term plan is to **escalate to a human (possibly via the PM)**.

---

### 1.3 Coder agent

Coder is **stateful and lifecycle-heavy**:

- It owns the whole **implementation lifecycle**:

  ```text
  WAITING → SETUP → PLANNING → PLAN_REVIEW → CODING → TESTING → CODE_REVIEW → PREPARE_MERGE → AWAIT_MERGE → DONE/ERROR
  ```

  Plus `BUDGET_REVIEW` and `QUESTION`.

- It manages real resources:
  - Workspaces and filesystems
  - Containers and build backends (`build.Service`, `LongRunningDockerExec`)
  - Git branches and PRs via `CloneManager`
- It has its own internal **todo list abstraction** (`TodoList`, `TodoItem`, add/update/complete flows) and a separate todo-collection toolloop phase.
- It implements a **budget escalation path**:
  - Planning and coding phases maintain iteration counts and, on hitting configured limits, transition into `BUDGET_REVIEW` by storing a `BudgetReviewEffect` in state and then executing it, which sends a typed approval request to the architect.
  - `handleBudgetReview` executes this effect and then processes `BudgetReviewResult` to decide whether to return to `PLANNING`, `CODING`, or `ERROR`.
- It has rich testing behavior:
  - Distinct “DevOps” vs “App” testing strategies (`handleDevOpsStoryTesting`, `handleAppStoryTesting`, `handleLegacyTesting`), using either `build.Service` or direct `make test` via `LongRunningDockerExec`.

Conceptually: Coder is the **long-lived worker** with a deep, multi-stage FSM and a lot of operational responsibilities. It’s also where the most complexity and entropy has accumulated.

Operationally:

- Many coders can exist concurrently (target ~10).
- Each coder handles exactly one story and is torn down after completion; context, clones, and workspaces are deleted to guarantee a clean slate next time.
- If a coder fails during story development, the story is re-queued for another attempt.
- IDs (agent ID, story ID, workspace paths) are essential for tying everything together; they must not be duplicated inconsistently, but they are intentionally abundant.

---

### 1.4 PM agent

PM is **requirements- and spec-centric**, closer to a “front-of-house” agent:

- Its FSM is narrower, focused on story intake, requirement clarification, spec drafting, and hand-off:
  - States like `WAITING`, `INTERVIEW`, `SPEC_DRAFTING`, `SPEC_REVIEW`, `PREVIEW`, `AWAIT_USER`, `AWAIT_ARCHITECT`, etc.
- It uses toolloop primarily to:
  - Drive **spec creation and refinement** via tools like `spec_submit`, not filesystem/build tools.
- Its escalation path is **procedural rather than budget-driven**:
  - It doesn’t maintain complex iteration counters; escalation is about “spec needs architect review” or “waiting for user/architect response”.
  - The PM is the **only agent that directly interacts with a human user**; escalation typically means using `chat_ask_user` to provide a status update and ask for permission to continue/revise.
- It still uses the same `BaseRuntime + SendMessageEffect` pattern to talk to the dispatcher and receive replies.

Conceptually: PM is the **requirements orchestrator**—more about conversational/spec flows and less about long-running tooling or changes to the filesystem.

Operationally:

- PM is the preferred escalation surface for human involvement when possible.
- Escalation to a human is preferred over failure, but both are undesirable; the system is intended to run unattended most of the time.

---

## 2. Cross-cutting, high-priority improvements

This section summarizes high-impact changes across agents and shared infrastructure (including the updated understanding around `toolloop` and escalation).

### 2.1 DRY: unify the Runtime + SendMessageEffect pattern

Right now each agent has its own small runtime wrapper:

- `type Runtime struct { *effect.BaseRuntime; <agent> *<Agent> }`
- A `NewRuntime` that simply calls `effect.NewBaseRuntime(dispatcher, logger, agentID, "<agentType>")`.

The logic is effectively identical except for the agent type string and maybe one or two helper methods.

**Proposal**

Introduce a single reusable runtime type in the `effect` package, e.g.:

```go
type AgentRuntime struct {
    *BaseRuntime
    agentType agent.Type
}

func NewAgentRuntime(dispatcher Dispatcher, logger logx.Logger, id string, t agent.Type) *AgentRuntime {
    br := NewBaseRuntime(dispatcher, logger, id, t)
    return &AgentRuntime{BaseRuntime: br, agentType: t}
}
```

Each agent then embeds or holds `*effect.AgentRuntime` instead of duplicating boilerplate. Agent-specific helpers (like `SendMessageEffect` wrappers) can be small methods on their own agent struct instead of re-declaring full runtime types.

**Benefit**

- Removes near-duplicate runtime definitions and any risk of subtle divergence (e.g. logging fields, metrics tags).
- Makes it trivial to change shared runtime behavior (e.g., standardized result envelopes, tracing) in one place.

---

### 2.2 DRY: centralize “state handler switch + error semantics”

All agents follow the same general pattern:

- Read `current := sm.GetCurrentState()`.
- `switch current` and call `handleX` for each state.
- If the handler returns an error, log it, set an error message in state, and transition to `StateError` (or equivalent).

The implementations are similar but not identical, which invites unintentional differences in:

- What gets logged
- How error messages are stored
- Whether `StateDone` requires special handling

**Proposal**

Introduce a small helper in the `agent` package (or embed in `BaseStateMachine`) such as:

```go
type StateHandler func(ctx context.Context, sm *BaseStateMachine) (next proto.State, done bool, err error)

func RunStep(
    ctx context.Context,
    sm *BaseStateMachine,
    logger logx.Logger,
    handlers map[proto.State]StateHandler,
) (proto.State, bool, error) {
    cur := sm.GetCurrentState()
    handler, ok := handlers[cur]
    if !ok {
        err := fmt.Errorf("no handler for state %s", cur)
        sm.SetErrorMessage(err.Error())
        logger.Error("unhandled state", "state", cur, "err", err)
        return proto.StateError, true, err
    }

    next, done, err := handler(ctx, sm)
    if err != nil {
        sm.SetErrorMessage(err.Error())
        logger.Error("state handler error", "state", cur, "err", err)
        return proto.StateError, true, err
    }

    sm.SetCurrentState(next)
    return next, done, nil
}
```

Each agent then:

- Registers its `handlers` map once (e.g., in constructor), and
- Calls `RunStep` from its state-processing loop.

**Benefit**

- Single source of truth for error handling semantics.
- Simplifies adding cross-cutting behavior (metrics on state transitions, tracing) uniformly across agents.

---

### 2.3 DRY + correctness: standardize escalation patterns

Escalation is conceptually similar across agents but implemented differently:

- **Architect**: iteration-based escalation per story/request, stored in state keys, with `EscalationConfig` hooks.
- **Coder**: iteration → budget review; stores a `BudgetReviewEffect` and transitions to `BUDGET_REVIEW`.
- **PM**: escalation is human-centric (await user, ask for input); it’s not iteration-budget-driven.

Given your clarified goals:

- System should operate unattended whenever possible.
- Failure is expensive; when stuck, **escalate to a human** via PM or Architect as appropriate.
- Only the PM talks directly to humans (via tools like `chat_ask_user`).

**Proposal**

Rather than forcing identical escalation behavior, define a small vocabulary + helper that each agent can use:

```go
type EscalationKind string

const (
    EscalationKindLLMBudget        EscalationKind = "llm_budget"
    EscalationKindHumanClarify     EscalationKind = "human_clarify"
    EscalationKindPlanApproval     EscalationKind = "plan_approval"
    EscalationKindBudgetReview     EscalationKind = "budget_review"
    EscalationKindStoryRequeue     EscalationKind = "story_requeue"
    // etc.
)

func Escalate(sm *agent.BaseStateMachine, kind EscalationKind, metadata map[string]any) {
    // store in state in a consistent format, or emit a standardized effect
}
```

Use this from:

- **Architect**: in `EscalationConfig.OnHardLimit` or “no tools used twice” to record escalation intent and drive PM or user interaction.
- **Coder**: when iteration limits are hit → `EscalationKindBudgetReview`, plus a `BudgetReviewEffect`.
- **PM**: when stuck or hitting iteration limits → `EscalationKindHumanClarify`, plus `chat_ask_user` tool invocation.

**Benefit**

- Provides consistent, queryable escalation semantics for metrics, observability, and UI.
- Reduces scattered magic strings and per-agent conventions.

---

### 2.4 DRY: unify toolloop usage around a typed Outcome

Originally, each agent used `toolloop.Run` with this kind of contract:

```go
signal, result, err := toolloop.Run(loop, ctx, cfg)
```

With different call sites manually interpreting:

- `IterationLimitError` for iteration limit escalation.
- Generic errors (including “no tools used after reminder”).
- Empty `signal` vs “terminal” signal names.

With the updated understanding:

- Token budgets and rate limits are handled elsewhere (LLM middleware).
- Iteration limits here are **safety rails** to avoid infinite loops or non-progress.
- Human escalation is strongly preferred over failing whenever we’re stuck.

The suggested clean-break refactor is:

#### 2.4.1 Make `toolloop.Run` return a structured `Outcome`

Define:

```go
type OutcomeKind int

const (
    OutcomeSuccess OutcomeKind = iota
    OutcomeNoToolTwice      // LLM ignored tools 2 turns in a row (even after reminder)
    OutcomeIterationLimit   // Escalation.HardLimit reached
    OutcomeMaxIterations    // MaxIterations hit without Escalation.HardLimit
    OutcomeLLMError         // LLM client failed (network, 5xx, context errors, etc.)
    OutcomeExtractionError  // CheckTerminal signaled, but ExtractResult failed
)

type Outcome[T any] struct {
    Kind      OutcomeKind
    Signal    string // whatever CheckTerminal returned, e.g. "SUBMIT_REPLY"
    Value     T      // valid when Kind == OutcomeSuccess
    Err       error  // underlying error, where relevant
    Iteration int    // last iteration index (useful for logs/metrics)
}
```

Change the signature to:

```go
func Run[T any](tl *ToolLoop, ctx context.Context, cfg *Config[T]) Outcome[T]
```

Inside `Run`, you already know when:

- No tools were used twice (after injecting a “please use tools” reminder).
- `Escalation.HardLimit` fired (today you return `*IterationLimitError`).
- `MaxIterations` was exceeded.
- The LLM call itself failed.
- Extraction failed.

Map these events to corresponding `OutcomeKind` and populate `Outcome[T]` accordingly.

This is a deliberate **breaking change** that forces every call site to be reviewed and updated; the compiler will help ensure nothing is missed.

#### 2.4.2 Typed sentinel errors (optional, for internal clarity)

Internally, you can keep / introduce a couple of typed errors to keep `Run` implementation clean:

```go
var ErrNoToolUsageTwice = errors.New("no tools used after reminder")

type IterationLimitError struct {
    Iteration int
    msg       string
}

func (e *IterationLimitError) Error() string { return e.msg }
```

`Run` can use these to decide which `OutcomeKind` to produce, but callers don’t need to do `errors.As` anymore – they switch on `Outcome.Kind`.

#### 2.4.3 Agents interpret `Outcome` with explicit policies

Each agent’s call site now looks like this (example: Coder coding loop):

```go
out := toolloop.Run[CodingResult](loop, ctx, cfg)

switch out.Kind {
case toolloop.OutcomeSuccess:
    signal := out.Signal
    result := out.Value
    // normal handling based on signal/result

case toolloop.OutcomeIterationLimit:
    // For Coder: transition to BUDGET_REVIEW
    c.logger.Info("Coding iteration limit reached (%d), moving to BUDGET_REVIEW", out.Iteration)
    return StateBudgetReview, false, nil

case toolloop.OutcomeNoToolTwice:
    // Decide if this is a fatal error or a reason to escalate via PM/Architect
    return proto.StateError, false, logx.Wrap(out.Err, "coding LLM did not use tools")

case toolloop.OutcomeLLMError, toolloop.OutcomeMaxIterations, toolloop.OutcomeExtractionError:
    // Reuse your existing error handling (e.g., handleEmptyResponseError, requeue, etc.)
    return proto.StateError, false, logx.Wrap(out.Err, "toolloop execution failed")
}
```

Key point:

- **toolloop remains generic**; it doesn’t decide whether to budget review, ask user, or fail.
- Each agent encodes its own policy in a clear, compiler-checked switch.

This replaces the original “toolloop runner + escalation” idea with a simpler, more Go-ish approach: a typed result plus explicit per-agent switch logic.

---

### 2.5 Unify “no terminal tool” semantics via extractor contracts

Right now different extractors interpret “no terminal tool was called” differently:

- Planning extractors may return error if no terminal tool is seen.
- Coding extractors sometimes treat “partial activity but no terminal tool” as success, and “no activity” as error.
- Architect extractors (`ExtractSubmitReply`, `ExtractSpecReview`, etc.) generally treat missing required tools as errors.

In a tool-only interaction world, “no terminal tool” and “no meaningful activity” are central failure modes and should be treated consistently.

**Proposal**

- Define shared sentinel errors in the extractor layer:

  ```go
  var ErrNoTerminalTool = errors.New("no terminal tool was called")
  var ErrNoActivity     = errors.New("no tool calls or changes were made")
  var ErrInvalidResult  = errors.New("invalid tool result payload")
  ```

- Make each extractor follow the same contract:
  - Return `(value, nil)` when **semantics are satisfied**.
  - Return `ErrNoTerminalTool` when:
    - Required tools weren’t called, *or*
    - No terminal signal was produced.
  - Return `ErrNoActivity` when the LLM did literally nothing (e.g., no tools called, no edits).
  - Return `ErrInvalidResult` when a terminal tool was called but the payload is malformed.

- In `toolloop.Run`, classify `ErrInvalidResult` as `OutcomeExtractionError`. `ErrNoTerminalTool` and `ErrNoActivity` could be treated as “normal” (e.g., cause another iteration) or eventually mapped to `OutcomeMaxIterations` depending on where they surface.

- In agents, handle these via `Outcome.Kind` and/or checking `out.Err` when `Kind == OutcomeExtractionError` if you want finer-grained behavior.

**Benefit**

- Fewer subtle differences across agents about what “no terminal tool” means.
- Easier to add logging/metrics (“how often does the LLM fail to call a terminal tool?”) uniformly.

---

### 2.6 Dead code & debug prints

There are several pieces of code that are either dead, placeholder, or clearly debug:

- `Coder.loadTodoListFromState` and `joinStrings` are not used anywhere.
- `Coder.getExplorationHistory`, `getFilesExamined`, `getCurrentFindings` always return empty structures.
- `ExtractTodoCollectionResult` uses `fmt.Printf("DEBUG ...")` instead of structured logging.

Given that this is a **pre-release system** and you’re comfortable with breaking changes, the recommendation is:

- **Either** wire these into real behavior:
  - Actually load todo lists from state on coder startup.
  - Use exploration history and findings in prompts or logging.
- **Or** delete them now and reintroduce them when needed.

For debug prints:

- Replace `fmt.Printf` and other ad-hoc prints with structured logging via `logx.Logger` (`Debug` or `Info` level).
- If you want to keep noisy debug logs, gate them behind a configuration flag (or log level) rather than always printing.

**Benefit**

- Makes it much easier to reason about what the system *actually does*.
- Avoids surprising stdout output and keeps logs consistent.

---

### 2.7 Simplify ID/config duplication in Coder

The Coder agent currently keeps:

- A local `agentID`
- `agentConfig.ID`
- An embedded `BaseStateMachine` that also knows the agent ID

This duplication isn’t just cosmetic:

- Divergence between IDs can cause subtle bugs (mismatched metrics, logs, or dispatcher messages).

**Proposal**

- Choose a single source of truth for the agent ID:
  - Preferably `agent.Config.ID`, passed into `BaseStateMachine` at construction.
- Ensure all consumers (logs, effects, dispatcher calls) read the ID from one canonical place.

**Benefit**

- Eliminates a whole class of confusing bugs where IDs don’t line up between components.

---

### 2.8 Confirm production LLM interactions are tool-only

From the reviewed code:

- Architect prompts explicitly instruct LLMs to invoke tools and use required “terminal” tools.
- Toolloop configs are always provided with a `ToolProvider` and `CheckTerminal`/`ExtractResult`, so production flows appear to be tool-only.

There are:

- Mock/test LLMs that return text directly (fine for tests).
- Fallback prompts that are plain text but still instruct the LLM to use tools.

Recommendation:

- Do a quick sweep of the codebase (outside `arch`, `coder`, `pm`) for direct `llmClient.Complete` calls (or equivalents) that bypass `toolloop`.
- Either:
  - Migrate remaining interactions to toolloop, or
  - Mark them clearly as non-production/testing paths.

This keeps your “tool-only” invariant strong and makes the behavior more predictable under unattended operation.

---

## 3. Toolloop refactor & escalation design (clean break)

This section focuses on the redesigned `toolloop.Run` API and how it supports your unattended operation + escalation strategy.

### 3.1 Goals for the refactor

- Keep `toolloop` as the **core testable engine**:
  - It runs the LLM/tool loop.
  - It tracks iterations and detects key failure modes.
- Make toolloop return a **typed `Outcome`** instead of a `(signal, result, error)` tuple.
- Let each agent decide **how to respond** to different `OutcomeKind` values:
  - Budget review vs human escalation vs fail hard.
- Prefer **human escalation over failure**, but keep both explicit and visible.
- Avoid gradual/compat shims in favor of a **clean break** that forces call sites to be updated.

### 3.2 New `Run` signature

```go
type OutcomeKind int

const (
    OutcomeSuccess OutcomeKind = iota
    OutcomeNoToolTwice
    OutcomeIterationLimit
    OutcomeMaxIterations
    OutcomeLLMError
    OutcomeExtractionError
)

type Outcome[T any] struct {
    Kind      OutcomeKind
    Signal    string
    Value     T
    Err       error
    Iteration int
}

func Run[T any](tl *ToolLoop, ctx context.Context, cfg *Config[T]) Outcome[T]
```

#### Behavior mapping inside `Run`

Conceptually:

- If `CheckTerminal` signaled and `ExtractResult` succeeded:
  - Return `OutcomeSuccess` with `Signal` and `Value`.
- If the LLM ignored tools twice (even after the “please use tools” reminder):
  - Return `OutcomeNoToolTwice` with an underlying `ErrNoToolUsageTwice` (or equivalent).
- If `Escalation.HardLimit` is reached:
  - Return `OutcomeIterationLimit` with `IterationLimitError` (or at least `Iteration`).
- If `MaxIterations` is exceeded without `Escalation.HardLimit` firing:
  - Return `OutcomeMaxIterations` with a descriptive error.
- If the LLM call itself fails persistently:
  - Return `OutcomeLLMError` with the underlying error.
- If `CheckTerminal` signaled but `ExtractResult` fails:
  - Return `OutcomeExtractionError`.

This provides a single, explicit classification of what happened in the loop.

### 3.3 Using `Outcome` in agents (examples)

#### 3.3.1 Coder – coding loop with budget review

**Before** (simplified):

- Calls `toolloop.Run`.
- Checks `error` for `IterationLimitError` to transition to `BUDGET_REVIEW`.
- Treats other errors based on type/message.

**After**:

```go
out := toolloop.Run[CodingResult](loop, ctx, cfg)

switch out.Kind {
case toolloop.OutcomeSuccess:
    signal := out.Signal
    result := out.Value

    // Process result, then switch on signal:
    switch signal {
    case string(StateBudgetReview):
        return StateBudgetReview, false, nil
    case string(StateQuestion):
        return StateQuestion, false, nil
    case string(proto.StateError):
        return proto.StateError, false, logx.Errorf("coding returned error signal")
    // ...
    }

case toolloop.OutcomeIterationLimit:
    // Budget review escalation
    c.logger.Info("📊 Coding iteration limit reached (%d iterations), transitioning to BUDGET_REVIEW", out.Iteration)
    return StateBudgetReview, false, nil

case toolloop.OutcomeNoToolTwice:
    // Policy choice: treat as error or escalate via PM/Architect
    c.logger.Error("❌ LLM failed to use tools in coding state")
    return proto.StateError, false, logx.Wrap(out.Err, "coding LLM did not use tools")

case toolloop.OutcomeLLMError, toolloop.OutcomeMaxIterations, toolloop.OutcomeExtractionError:
    if isEmptyResponseError(out.Err) {
        req := agent.CompletionRequest{MaxTokens: 8192}
        return c.handleEmptyResponseError(sm, prompt, req, StateCoding)
    }
    return proto.StateError, false, logx.Wrap(out.Err, "toolloop execution failed")
}
```

- `OutcomeIterationLimit` is the only place that triggers budget review.
- `OutcomeNoToolTwice` is clearly visible; you can later change policy to human-escalate instead of fail.
- All `errors.As` and string-based classification disappear from call sites.

#### 3.3.2 Architect – question/approval flows

Similar pattern:

```go
out := toolloop.Run[SubmitReplyResult](qh.driver.toolLoop, ctx, cfg)

switch out.Kind {
case toolloop.OutcomeSuccess:
    if out.Signal == "" {
        return fmt.Errorf("no terminal signal (submit_reply) received")
    }
    // Handle out.Value and signal normally

case toolloop.OutcomeIterationLimit:
    // Architect is a singleton; failure is expensive, escalation is preferable
    qh.driver.logger.Error("❌ Architect Q&A iteration limit reached (%d)", out.Iteration)
    // TODO: future: escalate via PM/human
    return fmt.Errorf("architect Q&A iteration limit hit: %w", out.Err)

case toolloop.OutcomeNoToolTwice:
    return fmt.Errorf("architect Q&A did not use tools: %w", out.Err)

default:
    return fmt.Errorf("failed to get LLM response for question: %w", out.Err)
}
```

Here the policy is:

- Architect failures are considered fatal today but should eventually cause human escalation (likely via PM).
- The `Outcome` split makes both failure and escalation triggers explicit.

#### 3.3.3 PM – working loop and `chat_ask_user`

PM’s semantics around iteration limits are special: if an iteration limit is hit, it **must** have called something like `await_user` to provide a status update before hitting the limit.

```go
out := toolloop.Run[WorkingResult](loop, ctx, cfg)

switch out.Kind {
case toolloop.OutcomeSuccess:
    if err := d.processPMResult(out.Value); err != nil {
        return "", fmt.Errorf("failed to process PM result: %w", err)
    }
    return out.Signal, nil

case toolloop.OutcomeIterationLimit:
    if out.Signal == SignalAwaitUser || out.Value.AwaitUser {
        d.logger.Info("✅ PM reached iteration limit but used await_user to update human")
        return SignalAwaitUser, nil
    }

    d.logger.Error("❌ PM reached iteration limit (%d) without calling await_user", out.Iteration)
    return "", fmt.Errorf("PM must call await_user with status update before iteration limit: %w", out.Err)

case toolloop.OutcomeNoToolTwice:
    d.logger.Error("❌ PM did not call any tools in working loop (id: %s)", d.pmID)
    return "", fmt.Errorf("PM working loop did not use tools: %w", out.Err)

default:
    return "", fmt.Errorf("PM toolloop execution failed: %w", out.Err)
}
```

This preserves your current business rule and makes it crystal clear in code.

### 3.4 Implementation plan (engineer-facing)

1. **Modify `toolloop.Run` to return `Outcome[T]`.**
   - Implement `OutcomeKind` and `Outcome[T]`.
   - Add internal sentinel errors (`ErrNoToolUsageTwice`, `IterationLimitError`) as needed to keep `Run` clean.

2. **Update all call sites in `arch`, `coder`, `pm`:**
   - Replace `signal, result, err := toolloop.Run(...)` with `out := toolloop.Run(...)`.
   - Replace `if err != nil` branches with `switch out.Kind`.
   - For each `OutcomeKind`, explicitly decide:
     - **Coder**: budget review vs story requeue vs fatal error.
     - **Architect**: fatal now, escalated later.
     - **PM**: `chat_ask_user` vs error.

3. **Standardize extractor error contracts (optional but recommended):**
   - Introduce `ErrNoTerminalTool`, `ErrNoActivity`, `ErrInvalidResult`.
   - Map them to `OutcomeExtractionError` or `OutcomeMaxIterations` as appropriate.

4. **Refactor runtimes and state-machine loops:**
   - Introduce `effect.AgentRuntime` and migrate agents.
   - Introduce `agent.RunStep` (or similar) to centralize state handler logic.

5. **Clean up dead code & logging:**
   - Remove or complete unused helpers (`loadTodoListFromState`, exploration history helpers).
   - Replace `fmt.Printf` and similar debug prints with `logx.Logger` calls.

---

## 4. Summary

Taken together, these changes will:

- Tighten up cross-agent consistency (runtime, state-machine loop, escalation semantics).
- Make `toolloop` behavior explicit, testable, and easier to reason about.
- Allow each agent to encode its own escalation policy in a small, readable switch statement rather than ad-hoc error handling.
- Let the compiler guide the migration via a deliberate breaking change to `toolloop.Run`.
- Clean out dead/debug code and reduce sources of subtle bugs (ID duplication, divergent error handling).

The system remains strongly aligned with your operational goals:

- Mostly unattended operation.
- Avoiding failure where possible.
- Preferring human escalation when the LLM cannot make meaningful progress using tools.

---

## 5. Implementation Review & Refinements (2025-01-21)

This section captures decisions from detailed code review and clarifications on implementation approach.

### 5.1 Prioritization & Phasing

**Immediate Scope (High Value):**
1. **Runtime Unification (§2.1)** - One PR
2. **ID Unification (§2.7)** - One PR
3. **Outcome[T] Refactor (§2.4)** - One focused PR (~12 callsites)
4. **Extractor Sentinel Errors (§2.5)** - One PR
5. **Cleanup (§2.6)** - One PR

**Deferred/Optional:**
- **State Handler Centralization (§2.2)** - Wait and see if patterns emerge naturally after Outcome refactor
- **Escalation Standardization (§2.3)** - Focus on observability (log fields, metrics) rather than forcing unified control flow

### 5.2 Runtime Unification Details

**AgentRuntime scope:**
- Common: dispatching effects, logging, metadata (agent type, IDs)
- Agent-specific: `ReceiveMessage` overrides remain in each agent
- Rationale: Coder's `ReceiveMessage` logic is tied to its lifecycle (story state, todo list) which Architect/PM don't share

**Optional helper pattern:**
```go
func (r *AgentRuntime) WrapReceive(fn func(msg agent.Message) error) func(msg agent.Message) error {
    return func(msg agent.Message) error {
        // common logging/tracing
        return fn(msg)
    }
}
```

### 5.3 ID Unification Strategy

**Canonical source:** `agent.Config.ID`
- Pass into `BaseStateMachine` on construction
- Expose via `sm.ID()` or `runtime.ID()` method
- Remove duplicate `agentID` fields in driver structs
- Let compiler identify remaining references

### 5.4 Outcome[T] Design Decisions

#### OutcomeKind enum (loop-focused)

```go
type OutcomeKind int

const (
    OutcomeSuccess OutcomeKind = iota  // Terminal signal + successful extraction
    OutcomeNoToolTwice                 // LLM ignored tools 2x (loop gave up)
    OutcomeIterationLimit              // Escalation.HardLimit reached
    OutcomeMaxIterations               // MaxIterations without escalation
    OutcomeLLMError                    // LLM client failure
    OutcomeExtractionError             // CheckTerminal signaled but ExtractResult failed
)

type Outcome[T any] struct {
    Kind      OutcomeKind
    Signal    string  // CheckTerminal result (e.g., "PLAN_REVIEW", "TESTING")
    Value     T       // Extracted result (valid when Kind == OutcomeSuccess)
    Err       error   // Underlying error (non-nil for all non-Success kinds)
    Iteration int     // Last iteration count (1-indexed, useful for logs/metrics)
}
```

#### Key design principles

**OutcomeKind stays loop-focused:**
- "LLM didn't call ANY tools twice" → `OutcomeNoToolTwice` (loop-level behavior)
- "LLM called tools, but no terminal tool" → extractor layer (`ErrNoTerminalTool`)
- Don't add `OutcomeNoActivity` or `OutcomeSingleTurnFailed` - keep enum small

**Error handling:**
- `Outcome.Err` is always non-nil for non-Success kinds
- `toolloop` returns raw errors (no wrapping inside toolloop)
- Agents wrap with context at callsites: `logx.Wrap(out.Err, "coding toolloop failed")`
- Extractor errors surface as `OutcomeExtractionError` + `errors.Is()` checks for nuance

**SingleTurn mode:**
- No separate `OutcomeSingleTurnFailed` kind
- Failures map to existing kinds based on semantics:
  - No tools → `OutcomeNoToolTwice`
  - Tools used but no terminal → `OutcomeExtractionError` with `ErrNoTerminalTool`
  - Terminal tool but invalid result → `OutcomeExtractionError` with `ErrInvalidResult`

#### Agent switch pattern

```go
switch out.Kind {
case toolloop.OutcomeSuccess:
    // Process result, then switch on signal
    switch out.Signal {
    case string(StateBudgetReview):
        return StateBudgetReview, false, nil
    case string(StateQuestion):
        return StateQuestion, false, nil
    case "PLAN_REVIEW":
        return StatePlanReview, false, nil
    }

case toolloop.OutcomeIterationLimit:
    // Budget review escalation for Coder
    c.logger.Info("Iteration limit reached (%d), transitioning to BUDGET_REVIEW", out.Iteration)
    return StateBudgetReview, false, nil

case toolloop.OutcomeNoToolTwice:
    // Policy decision: error vs escalate via PM/Architect
    return proto.StateError, false, logx.Wrap(out.Err, "LLM did not use tools")

case toolloop.OutcomeLLMError, toolloop.OutcomeMaxIterations, toolloop.OutcomeExtractionError:
    // Reuse existing error handling (empty response recovery, etc.)
    if isEmptyResponseError(out.Err) {
        req := agent.CompletionRequest{MaxTokens: 8192}
        return c.handleEmptyResponseError(sm, prompt, req, StatePlanning)
    }
    return proto.StateError, false, logx.Wrap(out.Err, "toolloop execution failed")
}
```

**Pattern benefits:**
- Switch on `Kind` first (loop outcome)
- Inside `OutcomeSuccess`, switch on `Signal` (state transition)
- Prevents treating error outcomes as if they had meaningful "next state" signals

### 5.5 Extractor Sentinel Errors

Define shared vocabulary in extractor layer:

```go
var (
    ErrNoTerminalTool = errors.New("no terminal tool was called")
    ErrNoActivity     = errors.New("no tool calls or changes were made")
    ErrInvalidResult  = errors.New("invalid tool result payload")
)
```

**Contract:**
- Return `(value, nil)` when semantics satisfied
- Return `ErrNoTerminalTool` when required tools weren't called
- Return `ErrNoActivity` when LLM did literally nothing
- Return `ErrInvalidResult` when terminal tool called but payload malformed

**Mapping to OutcomeKind:**
- All three → `OutcomeExtractionError` (with `Outcome.Err` set to sentinel)
- Agents can use `errors.Is(out.Err, ErrNoActivity)` for fine-grained handling

**Nuance distinction:**
- `OutcomeNoToolTwice`: Loop-level guard (consecutiveNoToolTurns == 2)
- `ErrNoActivity`: Semantic failure at extractor level
- Both indicate "no meaningful progress", but different layers

### 5.6 State Handler Centralization (§2.2) - Deferred

**Rationale for deferral:**
- This is preventative, not fixing a known bug
- Current switch statements are readable and debuggable
- Abstraction adds indirection that may not pay off

**Approach:**
- Complete Runtime and Outcome refactors first
- If repeated boilerplate emerges naturally, add small optional helper
- Don't force a framework - make it a tiny convenience if needed

### 5.7 Escalation Standardization (§2.3) - Observability Focus

**Goal:** Unified observability/queryability, NOT identical code paths

**Approach:**
- Define shared vocabulary (string constants or small enum) for escalation kinds
- Use consistently in logging/metrics: `logger.Info("escalation", "kind", "budget_review", "agent", "coder", ...)`
- Standard metrics tags: `escalation_kind=budget_review`, `agent=coder`, `reason=iteration_limit`

**Avoid:**
- Heavy `Escalate()` helper that tries to be clever across all agents
- Forcing unified control flow when PM/Architect/Coder have legitimately different semantics

**Optional later:**
```go
logEscalation(logger, EscalationKindBudgetReview, metadata)
```

### 5.8 Critical Implementation Details

#### Breaking change scope
- **One focused PR** for Outcome[T] refactor
- ~12 callsites to update
- Keep other refactors separate to keep review tight
- No backward-compat shims needed (internal API, pre-release)

#### Error wrapping location
- `toolloop.Run` returns raw underlying errors in `Outcome.Err`
- Agents wrap at callsites where business context exists:
  ```go
  return proto.StateError, false, logx.Wrap(out.Err, "coding toolloop failed")
  ```

#### PM's `await_user` rule preservation
```go
case toolloop.OutcomeIterationLimit:
    if out.Signal == SignalAwaitUser || out.Value.AwaitUser {
        d.logger.Info("PM reached iteration limit but used await_user to update human")
        return SignalAwaitUser, nil
    }

    d.logger.Error("PM reached iteration limit (%d) without calling await_user", out.Iteration)
    return "", fmt.Errorf("PM must call await_user with status update before iteration limit: %w", out.Err)
```

**Enforcement:** Add unit test for this rule to prevent regression during refactor

### 5.9 Testing Strategy

**Unit tests (toolloop package):**
- Fake `ContextManager` and `ToolProvider`
- Force each condition:
  - No tools twice → `OutcomeNoToolTwice`
  - Hard limit → `OutcomeIterationLimit`
  - MaxIterations fallback → `OutcomeMaxIterations`
  - Extraction error → `OutcomeExtractionError`
- Assert `Outcome.Kind`, `Outcome.Err`, `Outcome.Signal` as expected

**Agent-facing tests:**
- **Coder planning/coding:**
  - Simulate `OutcomeIterationLimit` → ensure state transitions to `BUDGET_REVIEW`
- **PM:**
  - Simulate `OutcomeIterationLimit` with/without `SignalAwaitUser` → ensure "must call await_user" rule preserved
- **Architect:**
  - Simulate `OutcomeNoToolTwice` and `OutcomeIterationLimit` → ensure treated as error/fatal

**Not needed:** Full end-to-end system tests (unit + targeted agent tests sufficient)

### 5.10 Migration Order

Execute in sequence:

1. **Runtime unification (§2.1)**
   - Implement `AgentRuntime` in `pkg/effect/`
   - Switch agents over one at a time
   - Remove duplicate runtime boilerplate

2. **ID unification (§2.7)**
   - Make `agent.Config.ID` canonical
   - Pass to `BaseStateMachine` on construction
   - Remove duplicate `agentID` fields
   - Add `sm.ID()` or `runtime.ID()` accessor

3. **Outcome[T] refactor (§2.4)**
   - Define `Outcome[T]` struct and `OutcomeKind` enum
   - Change `toolloop.Run` signature
   - Update all ~12 callsites with new switch pattern
   - Add unit tests for each `OutcomeKind`

4. **Extractor sentinel errors (§2.5)**
   - Define `ErrNoTerminalTool`, `ErrNoActivity`, `ErrInvalidResult`
   - Update extractor functions to return these
   - Wire into `OutcomeExtractionError` in toolloop
   - Add agent-level `errors.Is()` checks where needed

5. **Cleanup (§2.6)**
   - Search for usage of `loadTodoListFromState`, `getExplorationHistory`, etc.
   - Remove truly dead code
   - Replace `fmt.Printf("DEBUG...")` with `logger.Debug()`
   - Move test-only code to `_test.go` files

Each phase is a separate PR for focused review.

### 5.11 Risk Mitigation

**Loss of error context:**
- Always set `Outcome.Err` when `Kind != OutcomeSuccess`
- Never wrap inside toolloop - only at callsites
- Log both: `logger.Error("toolloop failed", "kind", out.Kind, "err", out.Err)`

**Signal vs Outcome.Kind confusion:**
- Always switch on `Kind` first
- Only examine `Signal` inside `OutcomeSuccess` branch
- Prevents treating error outcomes as if they have meaningful state transitions

**Business rule preservation (e.g., PM's await_user):**
- Preserve exact logic in new switch structure
- Add unit tests to prevent regression
- Example callsites in §3.3.3 and §5.8 show literal translation

### 5.12 Example Callsite (Coder Planning)

See proposal §3.3.1 for detailed before/after example showing:
- Budget review escalation
- Empty response handling
- Signal-based state transitions
- Result extraction and processing

Representative callsite: `pkg/coder/planning.go:176-214`
