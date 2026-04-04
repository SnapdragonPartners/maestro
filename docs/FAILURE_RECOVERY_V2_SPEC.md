# Failure Recovery and Blocked Work v2

## Status

**Phase 1 implemented** (2026-03-31). **Phase 2 implemented** (2026-03-31). **Phase 2.5 implemented** (2026-03-31). Phase 3 is future work.

This document defines the desired end state for how Maestro handles blocked work, recovery, human clarification, and failure analytics. It is intentionally architecture-first. It does not prescribe an implementation sequence.

This document supersedes the conceptual recovery model in `docs/FAILURE_TAXONOMY_SPEC.md` and the retry-limit portion of `docs/RESILIENCE_IMPROVEMENTS.md`.

### Phase 1 Implementation Status

All 7 Phase 1 tasks are complete:

| Task | Description | Status |
| --- | --- | --- |
| 1 | FailureInfo expansion (Tier 1 fields, enums, evidence) | Done |
| 2 | DB migration — `failures` table, `on_hold`/`failed` story statuses | Done |
| 3 | `on_hold` queue status, hold/release, `AllStoriesTerminal()` | Done |
| 4 | Circuit breaker fix — failed story no longer kills architect | Done |
| 5 | Separate retry budgets (attempt + rewrite, per-class tracking) | Done |
| 6 | Hold/release mechanism wired into requeue flow | Done |
| 7 | `report_blocked` handling in PLANNING state | Done |

**Key decisions made during implementation:**

- `report_blocked` was NOT added to TESTING tools because TESTING runs procedurally (no toolloop). Phase 2 will classify test failures procedurally rather than adding a toolloop to TESTING.
- `report_blocked` signal handling was added to PLANNING state (`pkg/coder/planning.go`) — was previously missing from the outcome switch.
- Failure records are persisted for ALL requeue events (retry_attempt, rewrite_story, mark_failed), not just terminal events. This enables budget reconstruction on resume.
- Budget exhaustion for rewrites is checked BEFORE `handleBlockedRequeue()` runs, preventing infinite bypass via successful edits.
- Hold metadata (reason, since, owner, note, blocked_by_failure_id) is persisted to DB and loaded on resume via COALESCE wrappers for NULL safety.
- `AllStoriesCompleted()` remains success-only (drives PM "all work done" notifications). `AllStoriesTerminal()` (done OR failed) drives the circuit breaker.
- Budget reconstruction is wired into `RestoreState()` — queries `CountFailuresByStoryAndActionForSession()` for each story and calls `ReconstructBudgetsFromFailures()` to restore in-memory counters from the durable failures table.

## Summary

Maestro needs a single recovery architecture that can handle:

- bad or contradictory stories
- broader spec or epoch-level problems
- broken local workspaces or shared environment issues
- missing or invalid prerequisites such as credentials or external services
- rare but necessary human clarification
- future mid-flight requirements changes without destabilizing in-flight work

The design in this document is built around four decisions:

1. `blocked` is an event, not an agent state.
2. `kind` remains a small enum; richer routing comes from `scope` and structured failure metadata.
3. Only PM may talk to the human.
4. The system must preserve enough structured failure data to analyze recurring failure patterns and improve Maestro over time.

The result is a design with minimal agent FSM changes, one new durable story state, and a richer `FailureInfo` envelope that supports both recovery routing and later analysis.

## Terminology

These terms are specific to Maestro and are normative in this document.

- `requirements`: the user-approved problem statement and intent owned by PM
- `spec`: the architect-refined technical interpretation of the requirements
- `story`: a unit of execution assigned to a coder
- `epoch`: one approved cycle of requirements from PM to architect
- `blocked`: a structured report that a unit of work cannot proceed autonomously
- `failure`: the broader recovery record produced from a blocked report or an automatically detected error
- `attempt`: one coder execution attempt for a story

## Design Goals

1. Maestro should remain autonomous by default after requirements are approved.
2. A blocked story should not stall unrelated work unless the blast radius truly requires it.
3. Only PM should communicate with the human.
4. Deterministic repair work should be executed by the orchestrator, not improvised by the architect.
5. Story-level, epoch-level, and system-level problems must follow different recovery paths.
6. Failure handling must produce durable, queryable data for analysis and product improvement.
7. The design should support future mid-flight requirements changes without requiring a second recovery architecture.

## Non-Goals

This document does not require:

- a full epoch versioning system before blocked-work recovery ships
- progress-quality scoring or semantic stall detection
- enterprise-grade security hardening for failure storage
- a specific UI design for displaying failure analytics

## Core Principles

### 1. Blocked is not a state

When a coder is blocked, the coder should report the block and terminate that attempt. The coder does not remain alive in a `BLOCKED` state.

The blocked condition is represented as:

- a structured failure record
- optional story hold metadata
- orchestrator, architect, and PM actions

This keeps the coder FSM simple and avoids long-lived blocked agents.

### 2. Keep `kind` small

`kind` should classify what went wrong, not encode routing, ownership, or every possible symptom.

Routing should primarily depend on:

- `kind`
- `scope`
- whether human input is actually required

### 3. Preserve autonomy

The system should exhaust deterministic repair paths before asking a human for help.

Examples:

- refresh a workspace before asking a human to fix the repo
- verify or re-check a prerequisite before asking a human about a key
- rewrite or split a story before asking a human to clarify requirements

### 4. Preserve parallelism

Affected work should pause; unaffected work should continue.

Architect should not enter a global human-wait state for a story-specific block. Human clarification must be delegated to PM while architect remains able to monitor and dispatch unrelated work.

### 5. Failures are product data

A blocked report is not just a control signal. It is also evidence about where Maestro is weak. Failure data must therefore be durable, structured, and analyzable.

## Recovery Model

### Failure Kinds

`FailureKind` is the primary classification of what went wrong.

```go
type FailureKind string

const (
    FailureKindTransient    FailureKind = "transient"
    FailureKindStoryInvalid FailureKind = "story_invalid"
    FailureKindEnvironment  FailureKind = "environment"
    FailureKindPrerequisite FailureKind = "prerequisite"
)
```

Definitions:

- `transient`: temporary service or network unavailability already handled by `SUSPEND`. This remains part of the taxonomy for consistency and analytics, but it is not a normal `report_blocked` path.
- `story_invalid`: the work definition is bad. This includes contradictory, ambiguous, incomplete, or impossible work definitions at the story, spec, or epoch level.
- `environment`: the local or shared execution environment is broken or inconsistent. Examples include corrupted clone state, broken toolchain, invalid workspace state, or unrecoverable local container issues.
- `prerequisite`: progress depends on an external prerequisite that is missing, invalid, expired, or unavailable. Examples include invalid API credentials, revoked access, unavailable third-party service, or missing human-provided configuration.

### Failure Scope

`FailureScope` describes the blast radius.

```go
type FailureScope string

const (
    FailureScopeAttempt FailureScope = "attempt"
    FailureScopeStory   FailureScope = "story"
    FailureScopeEpoch   FailureScope = "epoch"
    FailureScopeSystem  FailureScope = "system"
)
```

Definitions:

- `attempt`: isolated to one agent attempt or one local workspace
- `story`: affects only the current story
- `epoch`: affects multiple stories in the current requirements epoch
- `system`: affects the shared execution environment or otherwise blocks work across epochs or across most of the system

Scope is the main routing input for recovery planning.

### Failure Source

Failures may be reported by:

- coder via `report_blocked`
- automatic classification of tool failures
- architect during review or impact analysis
- orchestrator when it detects repair failure or shared-environment failure

The system should preserve the reporting source separately from the resolved classification.

Suggested enum:

```go
type FailureSource string

const (
    FailureSourceLLMReport      FailureSource = "llm_report"
    FailureSourceAutoClassifier FailureSource = "auto_classifier"
    FailureSourceArchitect      FailureSource = "architect"
    FailureSourceOrchestrator   FailureSource = "orchestrator"
)
```

### Failure Owner, Action, and Resolution Status

These fields track who is currently responsible for recovery and what the system is trying to do.

Suggested enums:

```go
type FailureOwner string

const (
    FailureOwnerOrchestrator FailureOwner = "orchestrator"
    FailureOwnerArchitect    FailureOwner = "architect"
    FailureOwnerPM           FailureOwner = "pm"
    FailureOwnerHuman        FailureOwner = "human"
)

type FailureAction string

const (
    FailureActionRetryAttempt      FailureAction = "retry_attempt"
    FailureActionRewriteStory      FailureAction = "rewrite_story"
    FailureActionRewriteEpoch      FailureAction = "rewrite_epoch"
    FailureActionRepairEnvironment FailureAction = "repair_environment"
    FailureActionValidatePrereq    FailureAction = "validate_prerequisite"
    FailureActionAskHuman          FailureAction = "ask_human"
    FailureActionMarkFailed        FailureAction = "mark_failed"
)

type FailureResolutionStatus string

const (
    FailureResolutionPending   FailureResolutionStatus = "pending"
    FailureResolutionRunning   FailureResolutionStatus = "running"
    FailureResolutionSucceeded FailureResolutionStatus = "succeeded"
    FailureResolutionFailed    FailureResolutionStatus = "failed"
    FailureResolutionEscalated FailureResolutionStatus = "escalated"
)
```

## Blocked Reporting Contract

The blocked-reporting interface should collect enough structured signal to support routing without forcing the coder to make final policy decisions.

Suggested logical contract:

```go
report_blocked(
    kind,
    scope_guess,
    explanation,
    human_needed_guess?,
    evidence?
)
```

Rules:

- `kind` is required and must exclude `transient`
- `scope_guess` is required but advisory
- `explanation` is required and should be concise
- `human_needed_guess` is optional and advisory
- `evidence` is optional but strongly encouraged when there is a concrete error or diagnostic artifact

Automatic failure classifiers should populate the same logical fields when possible.

## Transient Failure Adapter

`transient` failures follow the existing `SUSPEND` path rather than the normal `report_blocked` path.

However, if Maestro wants unified failure analytics, `SUSPEND` events must still be representable in the same logical failure store.

This may be done by creating synthetic or adapted failure records with:

- `kind=transient`
- a non-LLM source such as retry middleware or orchestrator
- resolution outcomes such as resumed successfully or failed after suspend timeout

The implementation may use a translation layer rather than forcing `SUSPEND` through the ordinary blocked-reporting tool.

## FailureInfo

`FailureInfo` should evolve from a small flat struct into a structured envelope with four responsibilities:

1. capture the original report
2. capture triage and resolved classification
3. track recovery actions and outcomes
4. support later analytics

The exact storage model is an implementation detail, but the logical shape is normative.

```go
type FailureInfo struct {
    ID            string
    CreatedAt     time.Time
    UpdatedAt     time.Time
    SessionID     string
    ProjectID     string
    EpochID       string
    SpecID        string
    StoryID       string
    AttemptNumber int

    Report     FailureReport
    Triage     FailureTriage
    Resolution FailureResolution
    Analytics  FailureAnalytics
}

type FailureReport struct {
    Source            FailureSource
    ReporterAgentID   string
    ReporterAgentType string
    FailedState       string
    ToolName          string
    Kind              FailureKind
    ScopeGuess        FailureScope
    Explanation       string
    HumanNeededGuess  bool
    Evidence          []FailureEvidence
}

type FailureTriage struct {
    ResolvedKind     FailureKind
    ResolvedScope    FailureScope
    HumanNeeded      bool
    AffectedStoryIDs []string
    Summary          string
}

type FailureResolution struct {
    Owner         FailureOwner
    Action        FailureAction
    Status        FailureResolutionStatus
    AttemptCount  int
    RequestedAt   time.Time
    StartedAt     *time.Time
    CompletedAt   *time.Time
    Outcome       string
}

type FailureAnalytics struct {
    Tags                   []string
    Model                  string
    Provider               string
    BaseCommit             string
    DirtyWorkspace         bool
    Signature              string
    WorkspaceFingerprint   string
    EnvironmentFingerprint string
}

type FailureEvidence struct {
    Kind    string
    Summary string
    Snippet string
}
```

### Required Semantics

The following semantics are required even if the implementation uses a different concrete shape:

- every failure must have a stable ID
- failure data must survive restarts
- the original report must remain distinguishable from later triage
- recovery attempts must be tracked separately from the original report
- evidence must be sanitized and truncated before storage or logging
- enough metadata must exist to group failures by recurring patterns, with signatures as an optional later enrichment

### Tiered Expansion

The full logical envelope above is the target model, but implementation may ship in two tiers.

#### Tier 1: Required for routing and durable recovery

Tier 1 includes:

- identity and timestamp fields
- session, project, epoch, spec, story, and attempt identifiers
- report source, failed state, tool name, kind, scope guess, and explanation
- resolved kind, resolved scope, human-needed decision, affected stories, and triage summary
- owner, action, resolution status, and resolution timestamps
- minimal analytics fields needed for filtering, such as model, provider, base commit, and tags
- sanitized evidence snippets

#### Tier 2: Optional analytics enrichment

Tier 2 may be added after real production data is available.

Tier 2 includes:

- normalized signatures
- workspace or environment fingerprints
- richer evidence capture and reporting helpers

Tier 2 fields must not block shipment of the routing and recovery architecture.

### Notes on `scope_guess`

The reporting agent may provide a `scope_guess`, but it is advisory only. The system may widen or narrow scope during triage.

### Notes on `HumanNeeded`

The reporting agent may guess whether human help is needed, but the final `HumanNeeded` field is resolved during triage.

## Story Hold Model

### New Durable Story State: `on_hold`

Maestro should add exactly one new durable story status:

- `on_hold`

This is a queue and persistence concept, not an agent FSM state.

### Why `on_hold` is necessary

`on_hold` is justified because it represents a recoverable pause that is neither active work nor terminal failure.

Without `on_hold`, the system would be forced to overload:

- `failed`, which incorrectly implies terminality
- `pending`, which incorrectly implies dispatchable work
- agent `ERROR`, which is too low-level and too ephemeral

`on_hold` keeps recovery state attached to the story rather than to the dead coder attempt.

### Hold Metadata

Each `on_hold` story must also carry:

- `hold_reason`
- `hold_since`
- `hold_owner`
- `blocked_by_failure_id`
- optional `hold_note`

`hold_reason` should be a small enum, for example:

- `story_redraft`
- `environment_repair`
- `awaiting_human`
- `requirements_change`

No additional story statuses are required for this design.

### Terminal Failure

`failed` remains terminal and should be used only when Maestro has exhausted the allowed recovery path for the story or when the work is explicitly abandoned.

`on_hold` is recoverable.

## Hold Release Model

Putting a story on hold and releasing it from hold are both explicit operations.

A story must not leave `on_hold` implicitly just because:

- a rewrite finished
- a repair finished
- a PM message arrived

The release side must be as explicit as the hold side.

### Release Authority

Architect owns all `on_hold -> pending` transitions.

PM and orchestrator may complete prerequisite work, collect answers, or execute repairs, but they do not directly release held stories into the queue.

This keeps queue mutation centralized and ensures all released work re-enters the normal architect dispatch path.

### Release Target State

The canonical release target is:

- `pending`

Release does not move a story directly to `dispatched`.

This is intentional:

- `pending` means eligible for normal dependency evaluation
- `DISPATCHING` remains the only canonical path that sends work to coders

If a released story still has unmet dependencies, it remains `pending` and is not immediately dispatched.

### Release Semantics

Releasing a held story must:

- clear `hold_reason`
- clear `hold_since`
- clear `hold_owner`
- clear `blocked_by_failure_id`
- clear any optional hold note
- clear the current assignment and any in-flight attempt ownership
- place the story in `pending`

Release must be treated as a fresh attempt restart.

That means the implementation must clear attempt-scoped execution data such as:

- `AssignedAgent`
- `StartedAt`
- `ApprovedPlan`

This applies even for environment repair because Maestro does not preserve coder plans or in-flight execution when a coder is killed.

### Release Trigger Contract

The system needs an explicit release operation.

Conceptually:

```go
ReleaseHeldStories(target, release_cause) -> released_story_ids
```

Where `target` is one of:

- specific story IDs
- all stories associated with a failure or recovery case ID

Releasing by failure or recovery case is preferred over releasing by `hold_reason` alone.

`blocked_by_failure_id` exists specifically so a bulk release can target the correct set of held stories.

### Dispatch After Release

Every successful release that may make work eligible must trigger a normal architect dispatch pass.

Conceptually:

1. architect releases held stories to `pending`
2. architect schedules or enters `DISPATCHING`
3. `DISPATCHING` evaluates dependencies via the normal ready-story logic
4. only ready stories are sent to coders and moved to `dispatched`

This means the system should not invent a second direct-to-coder release path for held stories.

### Canonical Release Triggers

#### A. `story_redraft`

Owner: architect

Flow:

1. architect rewrites the held story or stories
2. architect calls the explicit release operation for the affected story IDs
3. stories move from `on_hold` to `pending`
4. architect triggers `DISPATCHING`

#### B. `environment_repair`

Owner of repair execution: orchestrator

Owner of release: architect

Flow:

1. orchestrator executes the repair sequence
2. orchestrator sends a structured repair-complete or repair-failed signal to architect, including the failure or recovery case ID
3. on success, architect bulk-releases the stories held for that failure or recovery case
4. architect triggers `DISPATCHING`
5. on failure, stories remain `on_hold` or are escalated/failed according to the recovery budget

#### C. `awaiting_human`

Owner of human interaction: PM

Owner of release: architect

Flow:

1. PM receives the human answer
2. PM sends the structured answer back to architect
3. architect decides whether the answer requires story edits, broader rewrite, or a fresh retry with no further rewrite
4. architect releases the affected held stories
5. architect triggers `DISPATCHING`

PM response alone does not release stories.

#### D. `requirements_change`

Owner: architect

Flow:

1. architect completes impact analysis and any required rewrites
2. architect releases the affected held stories
3. architect triggers `DISPATCHING`

## Agent Responsibilities

### Coder

Coder is responsible for:

- detecting and reporting blocked work
- providing a concise explanation and supporting evidence
- terminating the current attempt after reporting a non-transient block

Coder is not responsible for:

- deciding final blast radius
- deciding whether to ask the human
- executing system-level repair plans

### Architect

Architect is responsible for:

- triaging story, epoch, and work-definition failures
- deciding affected stories
- rewriting stories or broader work definitions
- deciding when a human clarification request must be sent to PM
- requesting deterministic repair actions from orchestrator when needed

Architect should remain in its normal monitoring/request workflow while blocked stories are on hold. Architect should not use a global human-wait state for blocked-story handling.

### Orchestrator

Orchestrator is responsible for:

- receiving failure notifications from terminated attempts
- restarting agents and refreshing local workspaces
- executing deterministic environment-repair actions
- temporarily suppressing dispatch if shared-environment repair is in progress
- persisting failure and resolution metadata

Orchestrator is the executor of repair plans, not the author of story rewrites.

## Scope of Orchestrator Repair

In this design, orchestrator repair does not mean arbitrary mutation of story workspaces or ad hoc container surgery.

For Phase 1, orchestrator repair should be limited to rerunning or reusing existing deterministic startup/bootstrap work that Maestro already performs at application start.

This includes actions such as:

- rerunning project verification
- recreating missing agent work directories
- ensuring or recovering the git mirror
- rerunning safe/bootstrap container validation and target-container validation
- rerunning preflight or bootstrap checks
- optionally rerunning bootstrap requirements detection if that becomes useful

Attempt-scoped repair may be even smaller than this:

- kill the attempt
- let coder `SETUP` recreate the local workspace as it already does today

System-scoped repair should mean rerunning deterministic startup/bootstrap actions that are already understood by Maestro, not inventing a new general-purpose repair executor in the first phase.

### PM

PM is the only agent allowed to communicate with the human.

PM is responsible for:

- asking clarification questions when requested
- relaying high-signal status updates when appropriate
- returning human answers to architect in structured form

PM should only block on a human reply when a reply is actually required.

Informational notifications must not be forced through a blocking human-wait path.

## Recovery Routing

Recovery is driven by `kind`, `scope`, and resolved human need.

### Canonical Routing Matrix

| Resolved Kind | Resolved Scope | Primary Owner | Canonical Action |
| --- | --- | --- | --- |
| `transient` | `attempt` | orchestrator | existing `SUSPEND` path, then resume |
| `story_invalid` | `story` | architect | rewrite or replace the story, then retry |
| `story_invalid` | `epoch` | architect | impact analysis, hold affected stories, rewrite affected work, then retry |
| `environment` | `attempt` | orchestrator | kill attempt, refresh workspace, retry |
| `environment` | `system` | orchestrator | execute shared repair sequence, then retry held work |
| `prerequisite` | `attempt` | orchestrator | re-check or refresh prerequisite, then retry |
| `prerequisite` | `story` | architect | decide if the story should be rewritten to avoid the prerequisite or held for clarification |
| `prerequisite` | `system` | PM after architect/orchestrator triage | ask human if deterministic checks fail |

### Human Clarification Rule

Human input should be requested only when both of the following are true:

1. deterministic repair or rewrite is not sufficient
2. a concrete human decision or resource is required

This is expected to be rare.

### Separate Retry Budgets

Retry and recovery budgets must be tracked per recovery class, not as a single global attempt counter.

At minimum, Maestro must distinguish:

- attempt retry budget
- story rewrite budget
- environment repair budget
- human clarification round-trip budget

Exhausting one budget must not implicitly exhaust another.

Examples:

- a corrupted workspace should not consume the same budget as a contradictory story
- a failed redraft should not consume the environment repair budget

## Initial Triage Rules

The long-term architecture allows architect-assisted impact analysis, but the first implementation should keep triage mostly mechanical.

Initial rules:

- default `story_invalid` to `story` scope
- widen `story_invalid` to `epoch` only when:
  - the architect explicitly chooses to widen it, or
  - the same failure kind recurs across multiple stories in the same epoch within a configured window
- default `environment` and `prerequisite` to `attempt` scope
- widen `environment` or `prerequisite` to `system` only when:
  - the orchestrator detects failure in shared startup/bootstrap assets such as the mirror, preflight checks, or shared container validation, or
  - the same resolved failure recurs across multiple agents or stories within a configured window

The system should not depend on broad LLM-based cross-story reasoning for the initial scope-routing implementation.

Advanced architect-led impact analysis is a later enhancement, not a prerequisite for shipping the architecture.

## Concurrent Failure Rules

The routing matrix describes isolated cases. Initial implementation must also define ordering rules for concurrent failures.

Required rules:

- only one `system`-scoped repair may be active at a time
- priority order is `system` > `epoch` > `story` > `attempt`
- lower-priority failures observed while a higher-priority recovery is active must still be recorded, but may be attached to the active recovery case or deferred
- repeated failures with the same effective cause should be coalesced rather than spawning multiple competing recoveries
- dispatch suppression during active `system` repair should apply before any new stories are launched
- story rewrites and system repair must not race to release the same held story

## Canonical Flows

### A. Story-Only Invalidity

Use when the story is locally bad but the broader epoch is sound.

Flow:

1. coder reports `story_invalid`
2. coder attempt terminates
3. orchestrator persists failure and requeues triage
4. architect triages scope as `story`
5. architect sets the story `on_hold` with `hold_reason=story_redraft`
6. architect rewrites or replaces the story
7. story returns to dispatchable state
8. unaffected work continues throughout

### B. Epoch-Level Invalidity

Use when the reported problem implies multiple stories are wrong or stale.

Flow:

1. coder reports `story_invalid`
2. architect triages scope as `epoch`
3. architect identifies affected stories
4. coders on affected stories are terminated
5. affected stories move to `on_hold`
6. unaffected stories continue
7. architect rewrites affected stories or replaces them
8. repaired stories are returned to dispatchable state

### C. Attempt-Level Environment Failure

Use when the issue appears isolated to one workspace or one attempt.

Flow:

1. coder reports `environment` or an automatic classifier produces the failure
2. orchestrator triages scope as `attempt`
3. orchestrator terminates the attempt
4. orchestrator refreshes or recreates the local workspace
5. story is retried

Architect involvement is not required unless the issue recurs or scope widens.

### D. System-Level Environment Failure

Use when the problem likely affects multiple stories or shared infrastructure.

Flow:

1. coder or orchestrator reports `environment`
2. architect and/or orchestrator triage scope as `system`
3. orchestrator suppresses new dispatch as needed
4. affected work moves to `on_hold` with `hold_reason=environment_repair`
5. orchestrator executes the repair sequence
6. on success, held stories are released for retry
7. on repeated failure, the issue escalates through the human-clarification path or becomes terminal

### E. Missing or Invalid Prerequisite

Use when progress depends on an external input or capability that Maestro cannot autonomously restore.

Flow:

1. coder reports `prerequisite`
2. orchestrator performs deterministic checks and refresh attempts
3. if resolved, retry the story
4. if unresolved, architect decides affected scope
5. affected stories move to `on_hold` with `hold_reason=awaiting_human`
6. architect sends a structured clarification request to PM
7. PM asks the human
8. PM returns the answer to architect
9. architect rewrites or resumes affected stories

### F. Human Requirements Change While Stories Are In Flight

This is not a failure kind.

However, the same machinery should be reused:

1. PM receives and approves a change request
2. architect performs impact analysis
3. affected stories move to `on_hold` with `hold_reason=requirements_change`
4. coders on affected stories are terminated
5. architect rewrites affected work
6. unaffected work continues
7. updated stories are released back to dispatch

This is a forward-compatibility requirement for the design even if not implemented in the first pass.

## State Model Requirements

### Agent FSMs

The desired end state does not require new agent FSM states for blocked recovery.

#### Coder

- blocked coder attempts terminate through existing error/termination mechanics
- `report_blocked` or equivalent blocked reporting must be available in all active work states where the coder can discover a genuine block

#### Architect

- architect should not enter a global human-wait state for ordinary blocked-story handling
- architect should continue monitoring and dispatching unaffected work while blocked stories are held

#### PM

- PM continues to use its existing user-wait state for actual human replies
- PM must not be forced into a blocking wait for informational messages

### Queue and Persistence

The queue and persistence layers must support:

- `on_hold` as a durable, queryable story state
- `failed` as a distinct durable terminal state
- hold metadata attached to stories
- durable failure records linked to stories and attempts

## Analytics and Improvement Requirements

Failure handling must produce data that can be mined for recurring patterns.

### Minimum Analytics Dimensions

Maestro must be able to analyze failures by at least:

- project or repository
- session
- epoch
- spec
- story
- attempt number
- reporting agent type and ID
- failed state
- triggering tool
- kind
- scope guess
- resolved scope
- resolved action
- model and provider
- base commit
- time to resolution
- final outcome

### Signature Requirement

Normalized signatures are useful, but they are not required for the first shipment.

Phase 1 may rely on grouping by explicit structured fields such as kind, tool, failed state, base commit, and explanation family.

If signature support is added later, it should be derived from structured normalized inputs rather than raw message text alone.

Useful signature targets include issues such as:

- repeated git corruption during commit
- repeated missing credential failures
- repeated story contradictions in the same prompt family

### Evidence Requirement

Failure evidence should support analysis without storing unbounded logs.

Requirements:

- evidence must be truncated
- obvious secrets must be redacted
- evidence should preserve the most diagnostic lines or snippets
- evidence should distinguish between raw output and summarized interpretation

### Evidence Sanitization Plan

The first implementation should use a concrete, conservative sanitization pipeline rather than a vague "redact secrets" rule.

Minimum plan:

1. capture only targeted snippets from tool output rather than full logs or diffs
2. run snippets through the existing secret-redaction mechanisms where possible, plus failure-specific token patterns as needed
3. truncate sanitized output to a fixed size budget
4. if sanitization is uncertain or fails, drop the raw snippet and keep only a summarized explanation
5. never store full diffs, full command output, or unbounded stack traces by default

### Resolution History

It must be possible to answer questions such as:

- how often did Maestro fix this class of failure autonomously?
- which failure kinds most often required human help?
- which models or tools correlate with story-invalid reports?
- how long do prerequisite blocks stay unresolved?

This requires durable storage of both failure and resolution history.

## Functional Requirements

### FR-1: Structured blocked reporting

Maestro must support structured blocked reports from coder attempts and automatic classifiers using the same logical `FailureInfo` envelope.

### FR-2: Distinct failure kinds

Maestro must distinguish at minimum `transient`, `story_invalid`, `environment`, and `prerequisite`.

### FR-3: Distinct blast-radius scopes

Maestro must distinguish at minimum `attempt`, `story`, `epoch`, and `system`.

### FR-4: Minimal state expansion

Maestro must not add a persistent `BLOCKED` agent state for this feature.

### FR-5: Durable story hold

Maestro must support a durable `on_hold` story status with hold metadata.

### FR-6: PM-only human communication

Only PM may communicate with the human about blocked work, clarification, or missing prerequisites.

### FR-7: Architect-owned work-definition recovery

Architect must own story and epoch rewrite decisions, including impact analysis for affected stories.

### FR-8: Orchestrator-owned deterministic repair

Orchestrator must own workspace refresh and system/environment repair execution.

### FR-9: Separate recovery budgets

Maestro must track retry and recovery budgets separately for attempt retry, redraft, repair, and human clarification.

### FR-10: Unaffected work continues

When a failure is scoped below `system`, unaffected stories must be allowed to continue unless an explicit safety rule blocks them.

### FR-11: Human escalation is rare and explicit

A human should only be consulted when Maestro determines that a concrete answer, resource, permission, or policy decision is required.

### FR-12: Persistence across restarts

Failure records, hold metadata, and recovery history must survive process restarts.

### FR-13: Queryable analytics

Failure and recovery data must be queryable for recurring-pattern analysis.

### FR-14: Forward compatibility with requirements changes

The same hold and impact-analysis mechanisms must be usable for future mid-flight requirements changes.

## Non-Functional Requirements

### NFR-1: Bounded blast radius

Recovery actions should affect the minimum necessary set of stories.

### NFR-2: Deterministic repair first

Before involving a human, Maestro should try deterministic repair or validation paths when safe to do so.

### NFR-3: Durable observability

Failure records must be retained long enough to support debugging and product improvement.

### NFR-4: Simplicity

The architecture should prefer richer metadata and routing over proliferation of agent states.

## Acceptance Criteria

The design should be considered implemented only when all of the following are true:

1. A coder can report a blocked condition during planning or execution and the system records a structured failure with durable history.
2. A story-specific contradiction can be redrafted and retried without halting unrelated stories.
3. A multi-story invalidity can hold only affected stories, terminate only affected coders, and allow unaffected stories to continue.
4. An attempt-level environment failure can be repaired by orchestrator without architect involvement unless the issue recurs or widens.
5. A system-level environment failure can suppress dispatch, hold affected stories, run repair, and resume work when the repair succeeds.
6. A missing or invalid prerequisite can be retried deterministically first, then escalated through PM only if needed.
7. Human clarification does not require architect to stop monitoring unrelated work.
8. Recovery budgets are tracked separately and do not collapse into a single retry counter.
9. Stories paused for recovery are represented as `on_hold`, not as terminal `failed`, unless recovery is actually exhausted.
10. Failure records survive restart and can be grouped by recurring fields for analysis, with normalized signatures as a later enrichment if needed.
11. The design does not require a new coder, architect, or PM blocked state.
12. The same hold mechanism can be reused later for mid-flight requirements changes.

## Suggested Implementation Boundaries

This section is descriptive, not prescriptive, and exists only to help planning.

- coder and toolloop: blocked reporting, evidence capture, active-state availability
- orchestrator and supervisor: attempt retry, environment repair execution, dispatch suppression, persistence hooks
- architect: triage, impact analysis, story redraft, hold/release decisions
- PM: structured clarification request and response flow
- persistence: durable failure records, `on_hold` story state, hold metadata, recovery history
- analytics or reporting layer: grouped failure queries and later signature-based summaries if warranted

## Explicit Design Decisions

The following decisions are intentional:

1. No persistent `BLOCKED` agent state will be added.
2. `on_hold` is the only new durable story status required by this design.
3. `kind` remains small; routing complexity belongs in `scope`, triage, and recovery metadata.
4. Human communication remains PM-only.
5. Requirements changes in flight should reuse this recovery architecture rather than inventing a separate one.

## Open Planning Questions for the Coding Team

These are implementation-planning questions, not architecture questions:

1. Which parts of `FailureInfo` should be persisted as first-class columns versus JSON?
2. Where should signature computation live?
3. What should the initial repair sequences be for attempt-level and system-level environment failures?
4. What is the minimum viable impact-analysis algorithm for epoch-scoped invalidity?
5. Which metrics or reports should be built first to make the new failure data actionable?

## Implementation Plan

### Task Dependency Graph

```
Task 1 (FailureInfo expansion)  ──┐
Task 2 (DB migration)           ──┼── Task 4 (Circuit breaker fix)
Task 3 (on_hold + queue)        ──┘        │
                                           ├── Task 5 (Retry budgets)
                                           ├── Task 6 (Hold/release)
                                           └── Task 7 (Expand report_blocked)
```

Tasks 1-3 are independent foundations. Tasks 4-7 depend on all three.

### Phase 1: Fix the highest-value failure handling gaps

#### Task 1: Expand FailureInfo (Tier 1 Fields)

Evolve `proto.FailureInfo` from 4 fields to the Tier 1 envelope. All changes additive — existing `NewFailureInfo()` constructor continues to work.

Files:
- **Modify** `pkg/proto/failure.go` — add `FailureScope`, `FailureSource`, `FailureOwner`, `FailureAction`, `FailureResolutionStatus` types and enums. Add `FailureKindEnvironment` and `FailureKindPrerequisite` constants (not wired to `report_blocked` until Phase 2). Expand `FailureInfo` struct with Tier 1 fields: identity (ID, timestamps), context (SessionID, SpecID, StoryID, AttemptNumber), classification (Source, ScopeGuess, ResolvedScope), resolution (Owner, Action, ResolutionStatus, ResolutionOutcome), evidence (`[]FailureEvidence`), analytics (Tags, Model, Provider, BaseCommit). Add `NewFailureInfoV2()` constructor, `GenerateID()` helper.
- **Create** `pkg/proto/failure_test.go` — backward-compat construction, JSON round-trip, ID uniqueness.

#### Task 2: Database Migration — `failures` Table

Files:
- **Modify** `pkg/persistence/schema.go` — bump to version 20. Migration: table-swap on `stories` to add `on_hold` and `failed` to CHECK constraint and add hold metadata columns (`hold_reason`, `hold_since`, `hold_owner`, `hold_note`, `blocked_by_failure_id`). Create `failures` table with report, triage, resolution, and analytics columns. Indexes on `story_id`, `session_id`, `kind`, `resolution_status`. Update `createFreshSchema()`.
- **Modify** `pkg/persistence/models.go` — add `FailureRecord` struct. Add hold metadata fields to `Story` (persistent, not `db:"-"`). Add `StatusOnHold` and `StatusFailed` constants.
- **Create** `pkg/persistence/failure_ops.go` — `PersistFailure`, `UpdateFailureResolution`, `QueryFailuresByStory`, `QueryFailureByID`, `CountFailuresByStoryAndAction` (for budget reconstruction on resume).
- **Modify** `pkg/persistence/operations.go` — add operation constants and case handlers. Include hold metadata in story upsert/update.

#### Task 3: `on_hold` Story Status + Queue Updates

Files:
- **Modify** `pkg/architect/queue.go` — add `StatusOnHold`. Fix `ToDatabaseStatus()` to map `StatusFailed` → `persistence.StatusFailed` (currently maps to `"done"`). **Do not change** `AllStoriesCompleted()` or `AllNonMaintenanceStoriesCompleted()` — these stay success-only (`StatusDone`). Add `AllStoriesTerminal()` for `done|failed` (used by circuit breaker). Add `GetHeldStories()`, `HoldStory()`, `ReleaseHeldStories()`, `ReleaseHeldStoriesByFailure()`.
- **Modify** `pkg/architect/dispatching.go` — `detectDeadlock()` excludes `StatusOnHold` and `StatusFailed`.
- **Modify** `pkg/persistence/operations.go` — story upsert includes hold metadata columns.

#### Task 4: Fix Circuit Breaker (Critical)

When a story exceeds `MaxStoryAttempts`, mark it `failed` and notify PM, but the architect continues processing other work.

Files:
- **Modify** `pkg/architect/driver.go` (`processRequeueRequests`, lines ~886-911) — remove `d.TransitionTo(ctx, StateError, ...)`. Keep `SetStatus(StatusFailed)` and PM notification. Add: persist failed status, create `FailureRecord` with `Action=mark_failed`. After marking failed, check `AllStoriesTerminal()` — if true, transition to `StateDone` (not `StateError`).
- **Update** `pkg/architect/retry_limit_test.go` — assert story → failed, architect does NOT enter ERROR, other stories dispatchable. Assert `AllStoriesCompleted()` false with failed stories, `AllStoriesTerminal()` true.

Decision: stories depending on a failed story remain stuck (Phase 2 can cascade-fail).

#### Task 5: Separate Retry Budgets

Phase 1 wires `attempt` and `rewrite` budgets only. `repair` and `human` budgets defined as constants but no control flow (Phase 2).

Files:
- **Modify** `pkg/persistence/models.go` — add `AttemptRetryBudget`, `RewriteBudget` to Story (`db:"-"`).
- **Modify** `pkg/architect/queue.go` — add `MaxAttemptRetries = 3`, `MaxStoryRewrites = 2`. Add `IncrementBudget()`, `IsBudgetExhausted()`, `ReconstructBudgetsFromFailures()`.
- **Modify** `pkg/architect/driver.go` — replace `AttemptCount++` / `>= MaxStoryAttempts` with budget methods. In `handleBlockedRequeue()`: increment rewrite budget on story edit.

Budget reconstruction on resume: query `CountFailuresByStoryAndAction()` from failures table, call `ReconstructBudgetsFromFailures()` per story. Failures table is the durable source of truth.

#### Task 6: Hold/Release Mechanism

Files:
- **Modify** `pkg/architect/driver.go` — in `processRequeueRequests()`: hold story with `story_redraft` before rewrite when rewrite budget allows. After rewrite, release and dispatch. Add `releaseAndDispatch()` helper that calls `ReleaseHeldStories()` then transitions architect to `StateDispatching` (the canonical dispatch path via `handleDispatching()` → `GetReadyStories()` → `dispatchReadyStory()`). Do not rely on ready-stories channel.
- **Modify** `pkg/architect/monitoring.go` — log held story count. Warn if all remaining stories are on hold.
- **Modify** `pkg/architect/dispatching.go` — log on-hold count after completion check.
- **Modify** `pkg/architect/request.go` — `notifyPMOfBlockedStory()` includes failure ID and hold reason.
- **Modify** `pkg/proto/payload.go` — add `FailureID` and `HoldReason` to `StoryBlockedPayload`.

#### Task 7: Clarify `report_blocked` Availability

`report_blocked` is already available in: PLANNING (AppPlanningTools, DevOpsPlanningTools) and CODING (AppCodingTools, DevOpsCodingTools). Adding `report_blocked` to TESTING is deferred to Phase 2 because TESTING runs procedurally (no toolloop). Not needed in: PLAN_REVIEW, BUDGET_REVIEW, AWAIT_MERGE, QUESTION (passive/waiting states).

Files:
- **Modify** `pkg/tools/constants.go` — add comment documenting Phase 2 deferral for TESTING.
- **Modify** `pkg/coder/planning.go` — add `SignalBlocked` case to PLANNING outcome switch (was missing, fell to "unknown signal" default).

### Orchestrator Repair (Phase 1 Definition)

Orchestrator repair maps to existing startup/bootstrap operations, not a new general-purpose executor.

**Attempt-scoped** (already works): kill coder attempt → coder SETUP recreates workspace from mirror.

**System-scoped** (rerun existing checks):
- `verifyProject()` in `cmd/maestro/main.go` — `.maestro/` structure, database, agent workspaces
- Container validation in `internal/startup/` — bootstrap and target image validation
- Mirror recovery in `pkg/workspace/manager.go` — git mirror health, target branch, worktree creation
- Preflight checks — GitHub auth, API keys, external tools

Phase 1 does not build new repair sequences. It wires hold/release so system failures can be manually resolved and released.

### Phase 2 Implementation Status

All 10 Phase 2 tasks are complete:

| Task | Description | Status |
| --- | --- | --- |
| 1 | Split `external` into `environment` + `prerequisite` | Done |
| 2 | Add `scope_guess` to `report_blocked` | Done |
| 3 | Architect triage step — resolve scope with mechanical defaults | Done |
| 4 | Mechanical scope widening (3 stories / 30 min) | Done |
| 5 | Epoch-scoped hold (multi-story) | Done |
| 6 | Kind-specific recovery routing | Done |
| 7 | Dispatch suppression during system repair | Done |
| 8 | Wire repair + human budget classes | Done |
| 9 | Transient failure adapter (SUSPEND events) | Done |
| 10 | Classify test failures as blocked reports | Done |

**Key decisions made during Phase 2:**

- `NormalizeFailureKind()` maps deprecated `external` → `environment` for backward compatibility. Old `report_blocked` calls with `failure_kind=external` still work.
- Architect triage resolves both `ResolvedKind` (normalized) and `ResolvedScope` (mechanical defaults: `story_invalid` → `story`, others → `attempt`) before routing.
- Scope widening is in-memory only (ring buffer, pruned by time window). No database index needed yet — cross-session widening deferred to Phase 3.
- Multi-story hold for epoch/system scope holds active (pending/dispatched) stories, not stories already in-progress with coders. Coder termination deferred to Phase 2.5.
- Kind-specific routing: `story_invalid` → hold + rewrite + release; `environment` → retry with fresh workspace; `prerequisite` → hold (`awaiting_human`) + PM clarification + manual release via `release_held_stories`. No retry churn for prerequisite failures.
- Dispatch suppression is a boolean flag on Queue checked by `GetReadyStories()`. System-scoped failures suppress; manual release resumes.
- Repair and human budget classes are defined with limits (MaxRepairAttempts=2, MaxHumanRoundTrips=1) and wired into budget tracking/reconstruction, but no control flow uses them yet (Phase 2.5 adds PM clarification + repair completion signals).
- Transient failure adapter: supervisor persists failure records on SUSPEND with `action=retry_attempt, status=running`, updates to `succeeded` on resume or `failed` on SUSPEND→ERROR timeout.
- Test failure classification: removed mechanical `classifyTestFailure()` pattern matcher (Phase 3 Task 1). All test failures now route back to CODING, where the coder LLM evaluates the output and calls `report_blocked` for environment/prerequisite issues. Test failure templates explicitly guide this decision. This avoids false positives from string matching (e.g., tests that exercise TLS or permission logic) and gives the LLM full context for classification.
- TESTING state now allows ERROR transitions (added to valid transitions map in `coder_fsm.go`).

### Phase 2.5 Implementation Status

All 3 Phase 2.5 tasks are complete:

| Task | Description | Status |
| --- | --- | --- |
| a | PM clarification round-trip for prerequisite/system failures | Done |
| b | Manual release mechanism (`release_held_stories` tool) | Done |
| c | System repair completion signal (repair_complete REQUEST) | Done |

**Key decisions made during Phase 2.5:**

- `release_held_stories` is a PM tool that returns a `SignalReleaseHeld` ProcessEffect. PM's working loop converts this into a `repair_complete` REQUEST message dispatched to the architect via the standard dispatcher — PM has no direct architect communication path.
- `RepairCompletePayload` carries `failure_id` (optional, for targeted release) and `reason`. Architect's `handleRepairComplete` releases by failure ID if provided, otherwise releases all held stories.
- `ClarificationRequestPayload` includes full context (failure ID, story ID, title, failure kind/scope, explanation, question, held story IDs) so PM can relay a complete picture to the human.
- PM receives clarification requests via `AWAIT_USER` payload routing — the existing `handleClarificationRequest` builds a structured context message for the PM LLM to relay to the human.
- Architect's `handleRepairComplete` both releases held stories and resumes dispatch (if suppressed), then transitions to DISPATCHING to pick up released work.

### Phase 2: Improve routing fidelity

Phase 2 makes the recovery routing matrix functional: failures get classified into the right kind and scope, different kinds follow different recovery paths, and the system can suppress dispatch during system-wide repair.

#### Task 1: Split `external` into `environment` + `prerequisite`

Split `FailureKindExternal` in both `report_blocked` tool params and the auto-classifier (`classifyCommitFailure`). The constants already exist in `pkg/proto/failure.go` but are not yet exposed. `report_blocked` enum becomes `[story_invalid, environment, prerequisite]`. Auto-classifier splits infrastructure patterns into environment (git corruption, disk, permissions) and prerequisite (auth, credentials, host resolution). Deprecate `FailureKindExternal` with backward-compat mapping.

Files: `pkg/tools/blocked_tool.go`, `pkg/tools/build_tools.go`, `pkg/proto/failure.go`

#### Task 2: Add `scope_guess` to `report_blocked`

Add optional `scope_guess` parameter (`attempt|story|epoch|system`) to `report_blocked`. Coders hint at blast radius; architect resolves during triage. Auto-classifier sets mechanical defaults: `environment` → `attempt`, `prerequisite` → `attempt`.

Files: `pkg/tools/blocked_tool.go`, `pkg/tools/build_tools.go`

#### Task 3: Architect triage step — resolve scope

When architect receives a requeue with FailureInfo, resolve `ResolvedScope` and `ResolvedKind` before routing recovery. Phase 2 uses mechanical defaults: `story_invalid` → `story`, `environment` → `attempt`, `prerequisite` → `attempt`. Persist resolved fields in failure record. Route recovery based on resolved scope: `attempt` → retry, `story` → rewrite or hold, `epoch` → multi-story hold (Task 5), `system` → dispatch suppression (Task 7).

Files: `pkg/architect/driver.go`, `pkg/persistence/failure_ops.go`

#### Task 4: Mechanical scope widening

Auto-escalate scope when the same failure kind recurs across multiple stories in a configurable time window (default: 3 stories in 30 minutes). In-memory ring buffer of recent failures, pruned by time window. Widening: `attempt` → `story` → `epoch` → `system`. Add `idx_failures_kind_created` index for cross-session queries if needed later.

Files: `pkg/architect/scope_widening.go` (new), `pkg/architect/driver.go`, `pkg/persistence/schema.go`

#### Task 5: Epoch-scoped hold (multi-story)

When resolved scope is `epoch`, identify all stories in the same spec that are not done/failed. Hold them with `hold_reason=epoch_rewrite` linking to the same failure ID. Terminate active coders on affected stories. Run architect rewrite for all affected stories. Release after rewrite. Populate `AffectedStoryIDs` in the failure record.

Files: `pkg/architect/driver.go`, `pkg/architect/queue.go`, `pkg/dispatch/dispatcher.go`

#### Task 6: Kind-specific recovery routing

Wire `environment` and `prerequisite` failures into different recovery paths from `story_invalid`. Environment + attempt → existing retry. Environment + story/system → hold with `environment_repair`. Prerequisite + story → hold with `awaiting_human`, send structured clarification request to PM. Add `ClarificationRequestPayload` for PM to relay questions to the human.

Files: `pkg/architect/driver.go`, `pkg/architect/request.go`, `pkg/proto/payload.go`

#### Task 7: Dispatch suppression during system repair

When resolved scope is `system`, suppress new story dispatch. Add `dispatchSuppressed` flag + `SuppressDispatch()`/`ResumeDispatch()` on queue. `handleDispatching()` checks flag before dispatching. Hold all non-terminal stories. For Phase 2 repair is manual (human resolves, then architect releases). Add suppression timeout (2 hours) that escalates to PM.

Files: `pkg/architect/queue.go`, `pkg/architect/dispatching.go`, `pkg/architect/driver.go`, `pkg/architect/monitoring.go`

#### Task 8: Wire repair + human budget classes

Uncomment `MaxRepairAttempts = 2`, `MaxHumanRoundTrips = 1`. Add `BudgetClassRepair` and `BudgetClassHuman` constants. Extend `IncrementBudget()`, `IsBudgetExhausted()`, `ReconstructBudgetsFromFailures()` for new classes. In `routeRecovery()`: check repair budget before environment repair (exhausted → escalate to human); check human budget before PM clarification (exhausted → mark failed).

Files: `pkg/architect/queue.go`, `pkg/persistence/models.go`, `pkg/architect/driver.go`

#### Task 9: Transient failure adapter

Create synthetic failure records from SUSPEND events. In `EnterSuspend()`, create a FailureInfo with `kind=transient`, `source=orchestrator`, `scope=attempt`. Pass in state metadata. Supervisor persists failure record on SUSPEND, updates to `succeeded` on resume or `failed` on timeout. Enables unified analytics across all failure types.

Files: `pkg/agent/internal/core/machine.go`, `internal/supervisor/supervisor.go`, `pkg/proto/failure.go`

#### Task 10: Classify test failures as blocked reports

TESTING runs procedurally (no toolloop), so `report_blocked` can't fire there. Instead, classify test failures procedurally: pattern-match test output for infrastructure issues (`cannot find package`, `permission denied`, `connection refused`, `executable not found`) → create FailureInfo with `kind=environment`, transition to ERROR (triggers existing requeue path). Test assertion failures continue returning to CODING as today. Container build/boot failures in DevOps testing get `kind=environment` + `scope=attempt`.

Files: `pkg/coder/testing.go`

#### Task dependency graph

```
Task 1 (kind split)  ──────────┐
Task 2 (scope_guess param)  ───┼── Task 3 (architect triage) ──┐
                               │                                ├── Task 5 (epoch hold)
                               │                                ├── Task 6 (kind routing)
Task 4 (scope widening)  ──────┘                                ├── Task 7 (dispatch suppression)
                                                                └── Task 8 (repair+human budgets)

Task 9 (transient adapter) ── standalone
Task 10 (TESTING failures) ── standalone (depends on Task 1 for kind values)
```

### Phase 3: Improve failure classification and analytics

Phase 3 focuses on making failure data more actionable and improving the coder's ability to recognize and report unrecoverable failures.

#### Task 1: Improved test failure classification in CODING — Done

Removed mechanical `classifyTestFailure()` pattern matcher. All test failures now route back to CODING, where the coder LLM evaluates output and calls `report_blocked` for environment/prerequisite issues. Updated test failure templates to explicitly guide this decision. Tests still run programmatically and must pass before code review — the LLM only interprets results, it cannot bypass testing.

Files changed: `pkg/coder/testing.go`, `pkg/templates/coder/test_failure_instructions.tpl.md`, `pkg/templates/coder/devops_test_failure_instructions.tpl.md`

#### Task 2: Normalized failure signatures

Hash of `kind+tool+state+explanation family` for grouping recurring failures with different error text. Enables pattern detection across sessions without requiring exact string matches. Stored alongside failure records in the database.

Files: `pkg/proto/failure.go`, `pkg/persistence/failure_ops.go`, `pkg/persistence/schema.go`

#### Task 3: Richer evidence capture with sanitization pipeline

Targeted snippets, secret redaction, and size budgets for failure evidence. Builds on the evidence capture from PR #175 to ensure data is both useful and safe to store/display. Evidence fields should respect configurable size limits and run through the secret scanner before persistence.

Files: `pkg/proto/failure.go`, `pkg/persistence/failure_ops.go`

### Phase 4: Smarter triage and environment awareness

Phase 4 replaces mechanical heuristics with reasoning-based approaches. These items depend on having sufficient failure signature data (Phase 3) to evaluate whether the mechanical approaches are making wrong calls.

#### Task 1: Workspace and environment fingerprints

Fingerprints to distinguish same-cause vs different-context failures. Useful for determining whether two `environment` failures on different stories share a root cause (same broken workspace) or are independent (different containers, different symptoms).

#### Task 2: Architect-led LLM impact analysis for epoch-scoped failures

Replaces mechanical scope widening (Phase 2 Task 4) with architect LLM reasoning. When scope widening is triggered, instead of mechanically escalating based on recurrence count, the architect uses a toolloop to inspect affected stories, read failure evidence, and decide whether the scope should widen — and if so, which stories are actually affected. Higher fidelity but higher cost; requires failure signature data to evaluate ROI vs mechanical approach.
