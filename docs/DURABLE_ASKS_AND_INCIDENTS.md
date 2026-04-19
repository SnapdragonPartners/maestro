# Durable Asks & Incidents

*Design document for fixes to GitHub issues #200 (PM silent in AWAIT_USER) and #201 (architect silent in MONITORING).*

## Problem Statement

Maestro goes silent for hours when work stalls. Two root causes:

1. **PM memory loss (#200):** PM sends ACTION REQUIRED via `chat_ask_user`, transitions to AWAIT_USER, and waits indefinitely. When the user eventually responds, PM has no structured memory of what's pending, so it gives stale or incorrect status.

2. **Architect silence (#201):** When all coders die (watchdog kills) or stall, the architect loops in MONITORING every 30s checking for messages that will never arrive. No escalation to PM or user.

## Design Principle

"Waiting on user" should be a durable product-state object, not a timer problem. Timers compensate for missing memory; durable asks and incidents solve the actual bug.

Timers are intentionally not part of this design. Edge-triggered communication (one notification per incident open/close) replaces polling and reminders.

---

## Data Model

### Incident

An **incident** is an architect-owned operational blocker. The architect is the sole authority for opening and closing incidents.

```go
type Incident struct {
    ID               string           // incident-{kind}-{storyID|system}-{failureID|timestamp}
    Kind             IncidentKind     // story_blocked | clarification_needed | system_idle
    Scope            string           // "story" | "system"
    StoryID          string           // set for story-scoped incidents
    FailureID        string           // cross-reference to failure record
    Title            string
    Summary          string
    AffectedStoryIDs []string         // for system_idle: which stories are stuck
    AllowedActions   []IncidentAction // advisory in Phase 1
    Blocking         bool
    OpenedAt         string
    ResolvedAt       string
    Resolution       string           // how it was resolved
}
```

**Incident kinds:**
- `story_blocked` — Story abandoned after exhausting retries. Scoped to a single story.
- `clarification_needed` — Failure requires human input (credentials, unclear requirements). Scoped to a story.
- `system_idle` — No active coders making progress but pending work exists. Scoped to system.

### UserAsk

A **UserAsk** is a PM-owned conversational obligation. Created when PM calls `chat_ask_user`, resolved when the user responds.

```go
type UserAsk struct {
    ID                string // ask-{kind}-{timestamp}
    Prompt            string
    Kind              string // "interview_question" | "clarification" | "decision_required"
    RelatedIncidentID string // optional link to triggering incident
    OpenedAt          string
    ResolvedAt        string
}
```

**Constraint:** At most one active ask at a time. A new ask implicitly supersedes any prior unresolved ask.

### IncidentAction

Recovery actions a user can take. Advisory metadata in Phase 1.

- `try_again` — Retry the failed story with same or edited content
- `change_request` — Modify story requirements before retry
- `skip` — Abandon the story permanently
- `resume` — Signal that the blocking condition has been resolved externally

---

## Ownership Rules

These rules are the heart of the design:

1. **Asks are PM-owned.** PM creates asks; user replies resolve asks.
2. **Incidents are architect-owned.** Architect opens incidents; architect-side recovery closes incidents.
3. **User replies do NOT automatically resolve incidents.** A user chatting with PM may provide information that PM relays to the architect, but the incident closes only when the architect observes recovery (story requeued, hold released, coder becomes active).
4. **Architect does NOT create or resolve asks.** The PM decides when to ask the user for input.

---

## Lifecycle Rules

### Incident Lifecycle

**Opening triggers:**
| Kind | Trigger | Location |
|------|---------|----------|
| `story_blocked` | Story abandoned (exhausted retries, `!willRetry`) | `notifyPMOfBlockedStory` in `request.go` |
| `clarification_needed` | Failure requires human input | `notifyPMOfClarificationNeeded` in `request.go` |
| `system_idle` | No active coders + pending work + idle > 60s | `checkAndOpenIdleIncident` in `monitoring.go` |

**Closing triggers:**
| Kind | Trigger | Resolution value |
|------|---------|-----------------|
| `story_blocked` | Story status no longer `failed`/`on_hold` | `"story_requeued"` |
| `clarification_needed` | Hold released via `repair_complete` | `"manual"` |
| `system_idle` | Idle predicate false (coder becomes active) | `"work_resumed"` |
| Any | All stories terminal | `"all_terminal"` |

**Idle detection specifics:**
- **Opening** uses a 60s debounce guard (2 heartbeats) to avoid false positives during dispatch transitions.
- **Closing** is predicate-based only — no timing guard. Once work resumes, close immediately.
- The `monitoringIdleSince` timestamp is a debounce guard, not durable business state. It is not persisted.

### UserAsk Lifecycle

**Opening:** PM calls `chat_ask_user` → `SignalAwaitUser` handler creates `UserAsk`.

**Closing:** User sends a chat message while PM is in AWAIT_USER → current ask resolved.

**Supersession:** If PM issues a new `chat_ask_user` before the prior ask is resolved, the new ask replaces the old one. Only one ask can be active.

---

## Communication Pattern

Incidents use edge-triggered, not level-triggered, communication:

1. Architect opens incident → sends `incident_opened` payload to PM (once)
2. PM stores incident in `openIncidents` and injects context into LLM
3. Architect closes incident → sends `incident_resolved` payload to PM (once)
4. PM removes incident from `openIncidents`

No polling, no reminders, no timers. The PM's `maybeInjectPendingItemsSummary()` re-injects the summary only when the digest changes (hash-based deduplication), preventing context bloat from `handleWorking()` re-entry loops.

---

## Persistence

Both asks and incidents must survive process restart:

- **PM:** `currentAsk` and `openIncidents` serialized as JSON in `PMState` (explicit DB columns via schema migration).
- **Architect:** `openIncidents` serialized as JSON in `ArchitectState` (explicit DB column).
- **State data mirroring:** Runtime state is also mirrored into state data keys (`StateKeyCurrentAsk`, `StateKeyOpenIncidents`) for FSM visibility through existing inspection tools.

---

## Phase Boundaries

### Phase 1 (this implementation)

- Durable `UserAsk` and `Incident` models
- `incident_opened` / `incident_resolved` payload kinds
- Architect incident lifecycle (open on story_blocked/clarification, system_idle detection, reconciliation)
- PM durable ask (singular, created on `chat_ask_user`, resolved on user reply)
- PM mirrored incidents (stored on `incident_opened`, removed on `incident_resolved`)
- Pending items summary injection with hash-based change detection
- Persistence via explicit JSON columns (schema migration v22)
- `AllowedActions` populated as advisory metadata
- User acts through natural language via PM; PM routes to architect as needed

### Phase 1.5 — `incident_action` tool with `resume`

Adds a structured `incident_action` PM tool with `resume` as the only supported action. Closes the loop: PM detects incident → tells user → user says retry → PM calls tool → architect recovers.

**Routing:** `PayloadKindIncidentAction` maps to `RequestKindExecution` (no new request kind).

**Resume semantics per incident kind:**

| Kind | Story Status | Recovery Action |
|------|-------------|-----------------|
| `system_idle` | N/A | Sweep orphaned dispatched stories (no live coder), resume dispatch |
| `story_blocked` | `StatusFailed` | `RetryFailedStory` — reset to pending for fresh attempt (preserves attempt count) |
| `story_blocked` | `StatusOnHold` | Release held stories by failure ID (same as repair_complete path) |
| `clarification_needed` | `StatusOnHold` | Release held stories by failure ID, resume dispatch |

**Key rules:**
- Recovery executes *before* incident resolution. If recovery fails, incident stays open and PM gets a failure result.
- When resuming by failure ID, *all* related incidents for that failure are resolved (not just the clicked one). A single prerequisite failure can open both `story_blocked` and `clarification_needed`.
- Both PM (tool side) and architect (handler side) validate `resume` ∈ `AllowedActions`.
- `incident_action_result` RESPONSE payload delivers typed success/failure back to PM.
- Orphan recovery in `system_idle`: dispatched stories whose assigned coder is no longer active are requeued to pending before re-dispatching.

### Phase 2: Full Action Semantics

Implemented. Four actions on the `incident_action` tool:

| Action | Description |
|---|---|
| `resume` | External blocker resolved — release held stories and re-dispatch |
| `try_again` | Identical to `resume` (PM picks the word that fits the context) |
| `skip` | Intentionally abandon a story — marks it `StatusSkipped` (new terminal state) |
| `change_request` | Append user instructions to story content, reset retry budget, requeue |

**Action × incident-kind matrix:**

| Action | `system_idle` | `story_blocked` | `clarification_needed` |
|---|---|---|---|
| `resume` / `try_again` | orphan sweep + re-dispatch | Failed→retry; OnHold→release holds | release holds + re-dispatch |
| `skip` | N/A | mark story skipped (no sibling release) | N/A |
| `change_request` | N/A | StatusFailed only; append + reset + retry | N/A |

**Key design decisions:**

- **`StatusSkipped`** is a new terminal status distinct from `StatusFailed`. Terminal guards, `AllStoriesTerminal()`, deadlock detection, session summaries, and PM notifications all include it. `AllStoriesCompleted()` does *not* — skipped ≠ completed. Dependencies on a skipped story are never satisfied.
- **Reverse-dependency gating:** `SkipStory` rejects if non-terminal stories depend on the target (they would become permanently unstartable).
- **Skip does not release siblings:** Failure-group holds represent shared blockers. Skipping one story doesn't resolve the shared condition, so siblings stay on hold. Only `resume` releases failure groups.
- **`change_request` restricted to `StatusFailed`:** For group-scoped incidents (`StatusOnHold`, `clarification_needed`), annotating one story and resuming the group would leave siblings without the annotation. `change_request` only operates on story-local (failed) incidents.
- **AttemptCount reset:** `change_request` resets attempts to 0 — a user-supplied change is a new direction and deserves a fresh retry budget.
- **Content annotation:** Appends `"## Change Request (User)"` section to story content, consistent with existing `"## Implementation Notes (Auto-generated)"` and `"## Failure Context (Auto-generated)"` patterns.

---

## Relationship to Existing Notifications

Phase 1 layers incidents on top of existing notifications. The existing `story_blocked` and `clarification_request` payloads continue to flow and inject immediate LLM context. The new `incident_opened` messages arrive separately and add durable state. Both coexist:

- **Existing notification** → immediate context injection into PM's LLM conversation
- **New incident** → durable state that survives resume and provides accurate status on re-entry

This avoids a risky migration while still solving the core problem. Phase 2+ may consolidate.
