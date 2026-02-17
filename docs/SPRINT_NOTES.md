# Sprint-Based Spec Delivery

## Concept

Instead of the PM producing one large monolithic spec per user request, guide it to think in terms of incremental deliverables ("sprints"). Each sprint is a focused spec that the architect breaks into stories independently. The PM maintains context across spec boundaries and can queue the next sprint while the current one is in progress.

## Why

- Large specs hit token limits (PMMaxTokens truncation — see commit b33f7d3)
- Smaller specs are easier for the architect to review and break into stories
- Enables pipelining: coders work on sprint 1 while PM plans sprint 2
- More natural feedback loop: user sees results sooner, can course-correct

## Current Architecture (already supports multi-spec)

The queue (`pkg/architect/queue.go`) is a flat map of stories tagged by `specID`. It already has:
- `CheckSpecComplete(specID)` — per-spec completion tracking
- `GetUniqueSpecIDs()` — lists all specs in queue
- `GetSpecTotalPoints(specID)` — points per spec
- `AllNonMaintenanceStoriesCompleted()` — cross-spec completion check

Stories from multiple specs coexist and dispatch together. No architectural changes needed to the queue.

## What Needs to Change

### 1. Bug Fix: `pmAllCompleteNotified` never resets (do this regardless)

**File**: `pkg/architect/request.go` (~line 735) and `pkg/architect/driver.go` (~line 112)

`pmAllCompleteNotified` is a single bool on the architect. Set to `true` when `AllNonMaintenanceStoriesCompleted()` fires, but **never reset**. Second spec completion will never notify the PM.

**Fix**: Reset to `false` when new stories are added to the queue (in `handleSpecReview()` after `loadStoriesFromSubmitResultData()`).

### 2. Relax `in_flight` guard in `spec_submit`

**File**: `pkg/tools/spec_submit.go` (lines 100-103)

Currently rejects full specs when `in_flight=true`:
```go
if s.inFlight && !isHotfix {
    return nil, fmt.Errorf("cannot submit new full spec while development is in progress...")
}
```

**Options**:
- Remove the guard entirely (allow queuing next spec anytime)
- Change to a warning in the tool result instead of an error
- Add a `sprint` parameter (like `hotfix`) that bypasses the guard

### 3. Update PM prompt (`pkg/templates/pm/working.tpl.md`)

Current prompt says (lines 9-13):
```
Your role ends at specification/requirements creation. You are NOT responsible for:
- Breaking specifications into stories (the architect does this)
- Discussing story IDs, story points, or implementation order
- Creating task breakdowns or sprint planning
```

**Change to**: Guide PM to prefer smaller focused specs. Something like:
- "Prefer smaller, focused specs over large monolithic ones"
- "Each spec should represent one deliverable increment (think sprints, not epics)"
- "After a spec is approved, continue the conversation to plan the next increment"
- Keep "not responsible for breaking specs into stories" (that's still the architect's job)

### 4. Update post-approval injection message

**File**: `pkg/pm/await_architect.go` (lines 107-111)

Currently tells PM to inform user and wait for hotfixes. Should instead encourage PM to continue planning next increment:
```
"The specification has been approved... Let the user know development is underway.
If there are more features to build from the conversation, start planning the next
specification. You don't need to wait for the current one to finish."
```

### 5. Per-spec completion notifications (optional, more complex)

Currently `all_stories_complete` fires once when ALL non-maintenance stories are done. With sprints, the PM might want per-spec notifications ("Sprint 1 is done, Sprint 2 still in progress").

Could use the existing `story_complete` + `CheckSpecComplete(specID)` to send a `spec_complete` notification distinct from `all_stories_complete`. This is optional — the PM already gets individual `story_complete` messages.

## Interactions to Watch

- **Maintenance mode**: Triggered by `onSpecComplete()`. Multiple specs completing at different times means maintenance could trigger multiple times. The `after_specs` counter already handles this correctly.
- **Architect state machine**: Needs to handle incoming spec review requests while in MONITORING/DISPATCHING states (currently it can — REQUEST handling works from most states).
- **Budget/token concerns**: More specs = more architect review cycles. But each is smaller, so should balance out.

## Testing Plan

1. Unit test: Submit two specs sequentially, verify both sets of stories appear in queue
2. Unit test: `pmAllCompleteNotified` resets after second spec's stories are added
3. Unit test: `spec_submit` allows full spec when in_flight (after guard change)
4. Integration test: PM submits spec 1, gets approved, submits spec 2 while spec 1 stories are in progress
