# Stories Complete Handling Specification

## Overview

This specification addresses issues discovered when initial stories complete and the system transitions to hotfix mode. The current implementation has several bugs that prevent proper PM notifications, demo tab access, and hotfix handling.

## Problem Statement

When all initial stories are completed:

1. **PM Reply Channel Drops Messages**: The PM's reply channel has a buffer of 1, and when PM is in AWAIT_USER state (blocking on user messages), architect notifications are dropped
2. **Demo Tab Inaccessible**: Frontend JavaScript auto-switches users away from the demo tab when PM is in WORKING/AWAIT_USER states
3. **Hotfix Preview Shows Original Spec**: When submitting a hotfix, the preview displays the original full spec instead of just the hotfix content
4. **Inconsistent State Variables**: Multiple overlapping variables (`draft_spec_markdown`, `spec_markdown`, etc.) cause confusion

## Design

### State Variable Cleanup

**Remove:**
- `draft_spec_markdown`
- `spec_markdown`
- `spec_uploaded`

**Add/Rename:**
- `user_spec_md` - User's feature requirements markdown (working copy during interview)
- `bootstrap_spec_md` - Infrastructure requirements from bootstrap phase
- `in_flight` - Boolean indicating development is in progress (spec submitted and accepted)

### PM State Machine Changes

#### `in_flight` Flag Behavior

**Set `in_flight = true` when:**
- Architect approves the spec (in `handleAwaitArchitect` approval branch)
- Clear `user_spec_md` and `bootstrap_spec_md` at this point (LLM context retains history)

**Set `in_flight = false` when:**
- All stories complete notification received from architect
- PM transitions back to accepting full specs

#### PM Behavior Based on `in_flight`

| `in_flight` | User Request | PM Action |
|-------------|--------------|-----------|
| `false` | Any | Full interview, PM judges if spec or hotfix based on scope |
| `true` | Small change | Accept as hotfix, call `spec_submit(hotfix=true)` |
| `true` | Large change | Explain to user that large changes must wait for current development |

### spec_submit Tool Enforcement

Add validation in `spec_submit` tool:

```go
if inFlight && !hotfix {
    return ProcessEffect{
        Signal: "ERROR",
        Data: map[string]any{
            "error": "Cannot submit new full spec while development is in progress. Wait for completion or scope down to the hotfix level for immediate processing.",
        },
    }
}
```

### AWAIT_USER Dual-Channel Select

Update `handleAwaitUser` to select on both channels:

```go
select {
case <-ctx.Done():
    return proto.StateDone, ctx.Err()

case msg := <-d.messageCh:
    // User chat message - process and transition to WORKING

case msg := <-d.replyCh:
    // Architect notification - could be:
    // - StoryComplete: Log progress, optionally inform user
    // - AllStoriesComplete: Set in_flight=false, inform user
    // - Escalation: Bubble up to user for help
}
```

### All Stories Complete Message

Add new payload type `PayloadKindAllStoriesComplete` that architect sends when:
- `d.queue.AllStoriesCompleted()` returns true
- Distinct from individual `StoryComplete` notifications

PM handles this by:
1. Setting `in_flight = false`
2. Informing user via `chat_ask_user` that development is complete
3. Mentioning demo is ready (if applicable)

### Demo Tab Frontend Fix

In `pkg/webui/web/static/pm.js`, modify `updatePMStatus()` to respect user's demo tab selection:

```javascript
// Skip auto-switch if user is intentionally on demo tab
if (this.currentTab === 'demo') {
    return; // Don't override user's demo tab selection
}

switch (status.state) {
    case 'WORKING':
        // existing logic...
    case 'AWAIT_USER':
        // existing logic...
    // etc.
}
```

This allows users to view/interact with demo while PM is active.

### Preview Content for Hotfixes

When `spec_submit(hotfix=true)` is called:
- Store ONLY the hotfix content in `user_spec_md`
- Do NOT prepend `bootstrap_spec_md` (it should be empty anyway post-approval)
- Preview shows just the hotfix requirement

## Implementation Plan

### Phase 1: State Variable Cleanup

1. Rename state keys in `pkg/pm/state_keys.go`:
   - Add `StateKeyUserSpecMd = "user_spec_md"`
   - Add `StateKeyBootstrapSpecMd = "bootstrap_spec_md"`
   - Add `StateKeyInFlight = "in_flight"`
   - Remove/deprecate old keys

2. Update all references in `pkg/pm/`:
   - `working.go` - Use new variable names
   - `preview.go` - Use `user_spec_md`
   - `await_architect.go` - Set `in_flight`, clear specs on approval
   - `driver.go` - Update `GetDraftSpec()` to use `user_spec_md`

3. Update `pkg/tools/spec_submit.go`:
   - Rename internal references
   - Add `in_flight` check with hotfix enforcement

### Phase 2: AWAIT_USER Dual-Channel

1. Update `pkg/pm/await_user.go`:
   - Add select on `d.replyCh`
   - Handle `StoryComplete` notifications
   - Handle `AllStoriesComplete` notification

2. Add `PayloadKindAllStoriesComplete` in `pkg/proto/`:
   - New payload type
   - Sent by architect when all stories done

3. Update `pkg/architect/dispatching.go`:
   - Send `AllStoriesComplete` to PM when transitioning to DONE

### Phase 3: Demo Tab Frontend

1. Update `pkg/webui/web/static/pm.js`:
   - Add guard in `updatePMStatus()` to skip auto-switch when on demo tab
   - Ensure demo tab remains accessible regardless of PM state

### Phase 4: Testing

1. Manual test flow:
   - Submit spec, get approval
   - Verify `in_flight = true`
   - Request hotfix while development ongoing
   - Verify preview shows only hotfix
   - Complete all stories
   - Verify `in_flight = false`
   - Verify demo tab accessible throughout

2. Unit tests:
   - `spec_submit` rejects non-hotfix when `in_flight`
   - AWAIT_USER handles both channels
   - State variables cleared correctly on approval

## Files to Modify

| File | Changes |
|------|---------|
| `pkg/pm/state_keys.go` | Add new state keys |
| `pkg/pm/working.go` | Use new variable names, update spec storage |
| `pkg/pm/preview.go` | Use `user_spec_md` |
| `pkg/pm/await_user.go` | Dual-channel select |
| `pkg/pm/await_architect.go` | Set `in_flight`, clear specs on approval |
| `pkg/pm/driver.go` | Update `GetDraftSpec()` |
| `pkg/tools/spec_submit.go` | Add `in_flight` validation |
| `pkg/proto/payload.go` | Add `PayloadKindAllStoriesComplete` |
| `pkg/architect/dispatching.go` | Send all-complete notification |
| `pkg/webui/web/static/pm.js` | Demo tab guard |

## Acceptance Criteria

1. [x] PM receives story completion notifications even when in AWAIT_USER state
2. [x] PM receives all-stories-complete notification and sets `in_flight = false`
3. [x] `spec_submit(hotfix=false)` returns helpful error when `in_flight = true`
4. [x] Hotfix preview shows only hotfix content, not original spec
5. [x] Demo tab is accessible at all times (no auto-redirect away)
6. [x] State variables are consistent: `user_spec_md`, `bootstrap_spec_md`, `in_flight`
7. [x] Original spec context preserved in LLM conversation for hotfix reference
