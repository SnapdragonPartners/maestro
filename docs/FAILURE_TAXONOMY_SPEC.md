# Failure Taxonomy: Structured Coder Failure Classification and Recovery

## Problem Statement

During production testing, a coder agent completed its implementation work but could not git commit due to container filesystem corruption (`bad tree object HEAD`). The done tool's commit step failed, but the toolloop kept the agent in CODING state. The LLM posted a message to chat describing the problem, but no structured signal propagated to the system. The agent stalled overnight.

On the architect side, the coder's story was eventually requeued via budget review timeout. However, the architect had no structured failure information -- every requeue looked like a generic ERROR. The architect kept approving budget reviews and reassigning the story because it could not distinguish "the story is bad" from "the container broke" from "the agent hit a transient network error." The result was wasted compute, wasted time, and no corrective action.

### Root Cause

The system had a single failure path: toolloop exceeds iteration limit, coder transitions to ERROR, supervisor requeues. There was no mechanism for:

1. **Classifying** failures by kind (infrastructure vs. story quality vs. transient)
2. **Propagating** failure metadata through the requeue pipeline
3. **Recovering** differently based on failure kind (rewrite story vs. escalate vs. retry)
4. **Detecting** stalled agents that are alive but making no progress

## Failure Taxonomy

Three failure kinds, each with a distinct recovery path:

| Kind | Constant | Description |
|------|----------|-------------|
| Transient | `FailureKindTransient` | Temporary issues (rate limits, network blips). Agent can retry. |
| Story Invalid | `FailureKindStoryInvalid` | Story is ambiguous, contradictory, or impossible as written. |
| External | `FailureKindExternal` | Environment, container, tooling, or dependency failure outside the coder's control. |

`FailureKindTransient` already existed as the SUSPEND mechanism. This work adds `story_invalid` and `external`.

The `external` kind is intentionally broad as a v1 umbrella. It covers container corruption, missing dependencies, broken toolchains, and workspace issues. It can be split into finer categories (environment/dependency/workspace) in a future iteration once production data reveals meaningful sub-categories.

### FailureInfo Structure

```go
type FailureInfo struct {
    Kind        FailureKind `json:"kind"`                  // transient | story_invalid | external
    Explanation string      `json:"explanation"`           // Human-readable description
    FailedState string      `json:"failed_state"`          // State when failure occurred (CODING, PLANNING)
    ToolName    string      `json:"tool_name,omitempty"`   // Tool that triggered the failure
}
```

## Recovery Matrix

| FailureKind | Coder Destination State | Architect Action |
|-------------|------------------------|------------------|
| `transient` | SUSPEND (existing) | None -- agent retries automatically after backoff |
| `story_invalid` | ERROR | Rewrite story via `attemptStoryEdit` with failure context, then requeue |
| `external` | ERROR | Inspect failure: if story-related, rewrite and requeue; if system-level, annotate story and escalate |

## Dual Detection Paths

Failures reach the system through two independent paths, converging on the same `FailureInfo` structure.

### Path 1: Auto-Classification (done tool git failure)

When the `done` tool attempts `git add -A && git commit` and the commit fails:

```
done tool
  -> git commit fails
  -> classifyCommitFailure(stderr) -> FailureInfo
  -> return BlockedError{FailureInfo}
  -> toolloop detects BlockedError
  -> returns OutcomeBlocked with FailureInfo
  -> coder Step() stores FailureInfo in state data (KeyFailureInfo)
  -> coder transitions to ERROR with failure metadata
```

`classifyCommitFailure()` examines git stderr to classify:
- `bad tree object`, `corrupt`, `broken` -> `external` (filesystem/container corruption)
- Unrecognized git errors -> returns empty string (no classification; handled by caller)

### Path 2: LLM-Reported (report_blocked tool)

When the coder LLM recognizes it is stuck and cannot proceed:

```
coder LLM calls report_blocked(kind, summary)
  -> tool returns ProcessEffect{Signal: "BLOCKED", FailureInfo: ...}
  -> toolloop detects ProcessEffect with BLOCKED signal
  -> returns OutcomeProcessEffect
  -> coder Step() extracts FailureInfo from result
  -> coder transitions to ERROR with failure metadata
```

The `report_blocked` tool schema:

```go
{
    Name: "report_blocked",
    Parameters: {
        "failure_kind": enum["story_invalid", "external"],  // transient handled separately
        "explanation":  string,                               // What went wrong
    },
}
```

## Data Flow: FailureInfo Propagation

The full propagation chain from coder detection to architect recovery:

```
Coder agent
  1. FailureInfo stored in state data under KeyFailureInfo
  2. buildErrorMetadata() reads KeyFailureInfo from state data
  3. TransitionTo(ERROR, metadata) fires StateChangeNotification

Supervisor
  4. Receives StateChangeNotification with metadata
  5. Extracts FailureInfo from metadata
  6. Calls dispatcher.UpdateStoryRequeue(storyID, failureInfo)
     - Sets story status to REQUEUE
     - Attaches FailureInfo to story's RequeueInfo

Architect (processRequeueRequests)
  7. Reads requeued stories from dispatcher
  8. Checks RequeueInfo.FailureInfo.Kind
  9. Routes to handleBlockedRequeue() for story_invalid/external
```

### Metadata Encoding

```go
// In coder Step(), on transition to ERROR:
metadata := buildErrorMetadata(stateData)
// metadata["failure_info"] = proto.FailureInfo value (Go value, not JSON)

// In supervisor, on StateChangeNotification:
fi := notification.Metadata["failure_info"].(proto.FailureInfo)  // type assertion
dispatcher.UpdateStoryRequeue(storyID, fi)
```

## Architect Recovery

When the architect processes a requeued story with `FailureInfo`, it uses the existing `attemptStoryEdit` pattern:

1. **Inject failure context**: Build a prompt containing the failure kind, summary, and detail. Inject into the per-agent context for the story.
2. **Single-turn toolloop**: Run the architect LLM with the `story_edit` tool available. The LLM can rewrite the story content based on the failure information.
3. **Best-effort**: Story edit is best-effort. If the LLM fails to produce a useful edit, the original story is requeued unchanged. The edit attempt does not block the requeue.

For `story_invalid`: the architect rewrites the story to address the coder's reported issue (ambiguity, contradictions, impossible requirements).

For `external`: the architect inspects the failure. If it appears story-related (e.g., story references a nonexistent dependency), it rewrites. If it appears system-level (e.g., container corruption), it annotates the story with context and may escalate.

## Coding Watchdog

A between-turns heartbeat mechanism detects stalled agents that are alive but not making progress.

### Design

```go
type ActivityTracker interface {
    RecordActivity(agentID string)
}
```

- The toolloop records activity at the start of each iteration via `ActivityTracker.RecordActivity()`.
- The supervisor maintains its own `lastActivity` map internally and polls every 30 seconds, checking elapsed time for each active coding agent.
- If the gap between now and last activity exceeds `CodingWatchdogMinutes` (configurable, default 30), the supervisor cancels the agent's context.
- The agent exits cleanly via `OutcomeGracefulShutdown` (context cancellation), without generating a `FailureInfo`.

### Exclusions

- **Claude Code mode**: Excluded from watchdog monitoring. Claude Code has its own timeout mechanisms and the interaction pattern (long-running subprocess) does not fit the between-turns heartbeat model.

## Config-Driven Iteration Limits

Budget review iteration limits are moved from hardcoded constants to `AgentConfig`:

| Config Field | Default | Description |
|-------------|---------|-------------|
| `CodingBudgetReviewTurns` | 12 | Max toolloop iterations in CODING state before budget review |
| `PlanningBudgetReviewTurns` | 10 | Max toolloop iterations in PLANNING state before budget review |

These replace the previously hardcoded values, allowing per-deployment tuning without code changes.

## Key Files

| File Path | What Changed |
|-----------|-------------|
| `pkg/proto/failure.go` | New: `FailureKind` constants, `FailureInfo` struct, `KeyFailureInfo` |
| `pkg/proto/message.go` | `FailureInfo` field on `StoryRequeueRequest` |
| `pkg/tools/blocked_tool.go` | New: `report_blocked` tool (LLM-reported blocked path) |
| `pkg/tools/build_tools.go` | `classifyCommitFailure()`, `BlockedError`, commit retry loop |
| `pkg/tools/mcp.go` | `SignalBlocked` constant |
| `pkg/tools/constants.go` | `ToolReportBlocked` added to planning/coding tool lists |
| `pkg/tools/registry.go` | `report_blocked` factory registration |
| `pkg/agent/toolloop/outcome.go` | `OutcomeBlocked` kind, `FailureInfo` field on `Outcome` |
| `pkg/agent/toolloop/toolloop.go` | Auto-classification via `BlockedError`, activity tracking call |
| `pkg/agent/toolloop/activity.go` | New: `ActivityTracker` interface |
| `pkg/coder/coder_fsm.go` | `KeyFailureInfo` state data key |
| `pkg/coder/coding.go` | `SignalBlocked` + `OutcomeBlocked` handling, config-driven iterations |
| `pkg/coder/planning.go` | Config-driven planning iteration limits |
| `pkg/coder/driver.go` | `buildErrorMetadata()`, `SetActivityTracker()`, `activityTracker` field |
| `pkg/dispatch/dispatcher.go` | `UpdateStoryRequeue` takes optional `*FailureInfo` |
| `pkg/persistence/models.go` | `LastFailureInfo` on `Story` (in-memory, `db:"-"`) |
| `internal/supervisor/supervisor.go` | `RecordActivity()`, watchdog goroutine, failure-aware requeue |
| `pkg/architect/driver.go` | `handleBlockedRequeue()`, `buildBlockedRequeuePrompt()` |
| `pkg/templates/renderer.go` | `BlockedRequeueTemplate` constant and registration |
| `pkg/templates/architect/blocked_requeue.tpl.md` | New: blocked requeue template |
| `pkg/config/config.go` | `CodingWatchdogMinutes`, `CodingBudgetReviewTurns`, `PlanningBudgetReviewTurns` |

## Known Limitations and Future Work

- **`external` is a v1 umbrella.** It covers container corruption, missing dependencies, broken toolchains, and workspace issues under a single kind. Production data will inform whether finer sub-categories (environment/dependency/workspace) are warranted.

- **Architect to PM escalation for system-level blocks is deferred.** When the architect determines a failure is system-level (not story-related), it currently annotates and requeues. A future iteration should escalate to the PM agent or a human operator.

- **Story content annotation via LLM story_edit, not direct mutation.** The architect uses the `story_edit` tool (LLM-driven) to rewrite stories. It does not directly mutate story fields. This is intentional -- the LLM can reason about what needs to change -- but means the quality of the rewrite depends on the LLM.

- **Watchdog is a coarse first guardrail.** It detects agents that stop calling tools entirely (e.g., hung on a blocking call, crashed subprocess). It does not detect agents that are actively calling tools but making no meaningful progress.

- **Progress-based stall detection is future work.** A more sophisticated detector would track state data changes, repeated failure patterns, and output quality metrics to identify agents that are "busy but stuck." This is deferred until the basic watchdog proves insufficient in production.
