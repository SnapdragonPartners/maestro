# pkg/architect Code Review Actions

**Review Date:** 2024-12-13
**Reviewer:** Claude Code
**Package Health:** Good (B+)

This file tracks action items from the periodic code review of `pkg/architect`.

---

## Implementation Plan

Work will be completed in 5 batches, each committed separately:

### Batch 1: Dead Code Removal (Low Risk)
- Remove `KnowledgeEntry` struct
- Remove `knowledgeBuffer` and `knowledgeMutex` fields
- Remove unused `story.KnowledgePack` access in `buildSystemPrompt`

### Batch 2: Queue Mutex Fixes (Correctness)
- Add mutex locking to `AddStory`
- Add mutex locking to `GetReadyStories`
- Add mutex locking to `NextReadyStory`
- Add mutex locking to `GetAllStories`
- Add mutex locking to `GetStoriesByStatus`

### Batch 3: File Organization (request.go split)
- Move `handleIterativeApproval` to `request_approval.go`
- Move `handleIterativeQuestion` to `request_question.go`
- Keep `request.go` as thin router

### Batch 4: Minor Fixes & Cleanup
- Use `StateKeyCurrentRequest` constant consistently
- Fix error message in `monitoring.go`
- Remove stale comment in `scoping.go`
- Use `GetStory()` instead of direct map access

### Batch 5: API Improvements (requires caller updates)
- Add return error to `QueuedStory.SetStatus()`
- Extract `StoryStatus.ToDatabaseStatus()` method

### Batch 6: Test Additions (see docs/TESTING_STRATEGY.md)
- Create `internal/mocks/` infrastructure (chat, GitHub, LLM clients)
- Unit tests for `handleEscalated` with mock chat service
- Unit tests for `handleMergeRequest` error paths with mock GitHub
- Integration tests for `handleMergeRequest` happy path (build tag: integration)
- Unit tests for iterative toolloop state transitions with mock LLM

---

## Priority: High

### 1. Add tests for critical untested paths

**Status:** [ ] Not Started
**Files to create:**
- `request_merge_test.go` - Test `handleMergeRequest` with mock GitHub client
- `escalated_test.go` - Test `handleEscalated` with timeout scenarios
- `request_approval_test.go` - Test iterative approval/question toolloops

**Why:** These are business-critical paths with complex branching logic that currently have no test coverage. Regressions could cause production issues.

---

### 2. Fix inconsistent Queue mutex usage

**Status:** [ ] Not Started
**Location:** `queue.go`

**Issue:** Some Queue methods acquire locks (`AddMaintenanceStory`, `UpdateStoryStatus`) while others don't (`AddStory`, `GetReadyStories`). This could cause data races.

**Action:** Audit all public Queue methods and ensure consistent locking:

```go
// Example fix for AddStory (line 101)
func (q *Queue) AddStory(...) {
    q.mutex.Lock()
    defer q.mutex.Unlock()
    // ... existing logic
}
```

**Methods to audit:**
- [ ] `AddStory` (line 101) - needs mutex
- [ ] `GetReadyStories` (line 211) - needs mutex
- [ ] `NextReadyStory` (line 190) - needs mutex
- [ ] `GetAllStories` (line 406) - needs mutex
- [ ] `GetStoriesByStatus` (line 421) - needs mutex

---

## Priority: Medium

### 3. Split request.go into focused handler files

**Status:** [ ] Not Started
**Location:** `request.go` (~1200 lines)

**Action:** Keep `request.go` as thin router (~100 lines), move handlers to dedicated files:

| Handler | Target File |
|---------|-------------|
| `handleIterativeApproval` | `request_approval.go` |
| `handleIterativeQuestion` | `request_question.go` |
| `handleSingleTurnReview` | Already in appropriate location |

---

### 4. Remove unused knowledge recording scaffolding

**Status:** [ ] Not Started
**Location:** `driver.go:29-41, 90-91, 172`

**Analysis Complete:** The `KnowledgeEntry` struct, `knowledgeBuffer`, and `knowledgeMutex` are scaffolding for a hypothetical "architect generates knowledge entries" feature that was never implemented. This is **not needed** for the current model (coders edit `.maestro/knowledge.dot`, architect validates).

**Current Knowledge Pack Flow (Working Correctly):**
1. Coders retrieve knowledge packs during PLANNING via `pkg/knowledge/retrieval.go`
2. Knowledge packs are embedded in request content templates
3. Architect receives knowledge context via request messages

**Items to Remove:**
- [ ] `KnowledgeEntry` struct (lines 29-41)
- [ ] `knowledgeBuffer` field (line 90)
- [ ] `knowledgeMutex` field (line 91)
- [ ] Buffer initialization (line 172)

**Related:** Also remove the `story.KnowledgePack` access in `buildSystemPrompt` (line 360) since this field is always empty - knowledge comes via request content, not story records.

---

## Priority: Low

### 5. Use state key constants consistently

**Status:** [ ] Not Started
**Locations:**
- `waiting.go:29` - uses `"current_request"` instead of `StateKeyCurrentRequest`
- `monitoring.go:41` - same issue

---

### 6. Add return error to `QueuedStory.SetStatus()`

**Status:** [ ] Not Started
**Location:** `queue.go:50-58`

**Issue:** Silent failure when attempting to modify completed story status.

```go
// Current (silent failure)
func (s *QueuedStory) SetStatus(status StoryStatus) {
    if s.GetStatus() == StatusDone {
        _ = logx.Errorf("INVALID: Attempted to change status...")
        return  // Caller can't tell this failed
    }
    s.Status = string(status)
}

// Proposed
func (s *QueuedStory) SetStatus(status StoryStatus) error {
    if s.GetStatus() == StatusDone {
        return fmt.Errorf("cannot modify completed story %s", s.ID)
    }
    s.Status = string(status)
    return nil
}
```

---

### 7. Extract status mapping to dedicated method

**Status:** [ ] Not Started
**Locations:** `queue.go:140-154`, `request.go:374-387`

**Action:** Create `StoryStatus.ToDatabaseStatus() string` method to eliminate duplication.

---

### 8. Address or remove TODO comments

**Status:** [ ] Not Started

| Location | TODO | Recommendation |
|----------|------|----------------|
| `maintenance.go:69` | Driver-level context for shutdown | Create issue or implement |
| `dispatching.go:138` | Extract requirements from story content | Clarify if still needed |
| `queue.go:381` | Persist requeue event to database | Medium priority - aids debugging |

---

### 9. Fix minor issues

**Status:** [ ] Not Started

- [ ] `monitoring.go:49` - Error message says "dispatching" but should say "monitoring"
- [ ] `scoping.go:31-32` - Remove stale historical comment about removed functions
- [ ] `driver.go:705` - Use `d.queue.GetStory()` instead of direct map access

---

## Completed

*(Move items here when done)*

---

## Notes

### Test Coverage Summary

Existing test files:
- `architect_fsm_test.go` - FSM transitions ✓
- `persistence_test.go` - Message persistence ✓
- `transitions_test.go` - State transitions ✓
- `multicontext_test.go` - Per-agent contexts ✓
- `maintenance_test.go` - Maintenance cycles ✓
- `retry_limit_test.go` - Story retry limits ✓

Missing coverage:
- Merge request handling
- Escalation flows
- Iterative toolloop flows
- Spec review (two-phase toolloop)

### Related Documentation

- `STATES.md` - Canonical FSM specification
- `docs/DOC_GRAPH.md` - Knowledge graph implementation spec (92% complete)
- `docs/ARCHITECT_CONTEXT.md` - Per-agent context design and phases
- `docs/wiki/DOCS_WIKI.md` - Knowledge graph user documentation
