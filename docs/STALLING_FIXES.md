# Stalling Bugs Fix Tracker

**Branch:** `fix/stalling-bugs`
**Analysis:** `../maestro-issues/docs/stalling-bugs-analysis.md` (MAE-0005, MAE-0006, MAE-0007)
**Started:** 2026-04-18

## Overview

Five bugs cascade into system-wide deadlocks where agents stop working and never recover. A failed story traps the architect in an infinite MONITORING loop, idle coders get killed by the watchdog, killed agents never restart, and the self-healing path is silently blocked.

## Phases

### Phase 1: Bug #1 — Split Completion Review Paths
**Status:** COMPLETE

Split planning-side vs coding-side completion review. Route coding-side `SignalStoryComplete` (zero-diff/empty-commit from `done` tool) to `CODE_REVIEW` instead of the invalid `PLAN_REVIEW` transition. Planning-side completion continues through `PLAN_REVIEW`.

Key changes:
- FSM: add `CODING → CODE_REVIEW` transition
- `coding.go` / `claudecode_coding.go`: store evidence, return `StateCodeReview`
- `code_review.go`: completion `REJECTED → ERROR` with `FailureKindStoryInvalid` metadata
- Templates: align REJECTED semantics to "story abandoned"
- `STATES.md`: update diagram and tables

Files: `coder_fsm.go`, `coding.go`, `claudecode_coding.go`, `code_review.go`, `coder_fsm_test.go`, `STATES.md`, `completion_request.tpl.md`, `completion_response.tpl.md`, `request_completion.go`

---

### Phase 2: Bug #4 — Dispatcher Rejects repair_complete
**Status:** NOT STARTED

Add `repair_complete` to story-independent message exemptions in dispatcher validation. Pattern matches existing `HotfixRequestPayload` exemption.

Files: `dispatcher.go`, dispatcher tests

---

### Phase 3: Bug #2 — Watchdog Kills WAITING Agents
**Status:** NOT STARTED

Add `AgentStates` map to supervisor, updated via state change notifications. Watchdog skips agents in WAITING state.

Files: `supervisor.go`, `supervisor_test.go`

---

### Phase 4: Bug #3 — Killed Agents Never Restart
**Status:** NOT STARTED

Detect unexpected agent exits (Run() returns without state notification) and restart. Guard against double-restart with `exitHandled` map. Distinguish system shutdown from watchdog kills.

Files: `supervisor.go`, `supervisor_test.go`

---

### Phase 5: Bug #5 — Failed Story Limbo
**Status:** NOT STARTED

Add `AllStoriesTerminal` check to DISPATCHING and MONITORING handlers. New `AllStoriesTerminalPayload` notifies PM with failure summary. PM clears `in_flight` flag so new specs can be accepted.

Files: `payload.go`, `driver.go`, `request.go`, `scoping.go`, `dispatching.go`, `monitoring.go`, `await_user.go`

---

## The Cascade

```
Trigger (FSM crash, git corruption, etc.)
    ↓
Story fails terminally (Bug #1)
    ↓
Architect MONITORING → MONITORING infinite loop (Bug #5)
    ↓
Coders sit idle in WAITING
    ↓
Watchdog kills idle coders (Bug #2)
    ↓
Killed coders never restart (Bug #3)
    ↓
User triggers repair → silently dropped (Bug #4)
    ↓
System dead
```
