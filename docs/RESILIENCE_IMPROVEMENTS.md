# Resilience Improvements Tracking

This document tracks identified issues and proposed improvements for system resilience.

## Completed Fixes

### Fix 1: Container Requirements for App Stories

**Problem**: The `maestro-go-dev` container built by coders is missing:
- User with UID 1000:1000 (required for `--user 1000:1000` flag)
- Git CLI (required for version control operations)

**Impact**: All coders fail with "Permission denied" when trying to create user at runtime because container is read-only.

**Solution**:
1. Updated bootstrap template (`pkg/templates/bootstrap/bootstrap.tpl.md`) with MANDATORY section:
   - Container MUST create unprivileged user with UID 1000:1000
   - Container MUST include git CLI
   - Clarified gh CLI is NOT required (runs on host)
2. Updated `ValidateContainerCapabilities()` helper in `pkg/tools/container_common.go`:
   - Added UserUID1000 check (`id -u 1000`)
   - Removed gh/GitHub API validation (not relevant)
3. All container tools and TESTING state use the same validation helper

**Status**: COMPLETED (commits `611b75d`, `6254666`)

---

## Proposed Improvements

### Improvement 2: SUSPEND State for Network/Service Unavailability

**Problem**: When laptop suspends or network is unavailable:
- Git SSH times out: `ssh: connect to host github.com port 22: Operation timed out`
- LLM API calls fail with timeout
- Agents enter ERROR state, losing all in-flight work
- Restart loops waste resources and lose progress

**Design Decision**: Universal SUSPEND state (not orchestrator-only pause)

Key insight: An agent 80% done with a story shouldn't lose that progress just because GitHub was unreachable for 30 seconds.

**Architecture**:

```
Agent Detection (decentralized):
- Track consecutive external API failures (LLM, GitHub)
- After N consecutive timeouts (3?) → transition to SUSPEND
- Save originating state to KeySuspendedFrom (NOT KeyOrigin - avoid clobbering)
- State data preserved (not cleared)

SUSPEND State Handler (in base class):
- Block on restore channel
- On restore signal → return to originating state unchanged
- Hard timeout (10-15 min) → transition to ERROR (full recycle)

Orchestrator Recovery (centralized):
- Watch for agents entering SUSPEND via state change notifications
- Start connectivity polling when first agent suspends
- Ping ALL configured APIs (LLM providers from config + GitHub)
- Only broadcast restore when ALL services pass
- Send to shared restore channel (buffered to agent count)

Natural Backpressure:
- Agents in SUSPEND hold their stories
- Dependent stories blocked automatically
- If outage persists, agents cascade into SUSPEND one by one
- No special queue management needed
```

**Implementation Details**:

1. **State data key** (not struct field - uses existing stateData map):
   ```go
   const KeySuspendedFrom = "suspended_from"  // Separate from KeyOrigin to avoid clobbering
   ```

   **BaseStateMachine additions** (`pkg/agent/internal/core/machine.go`):
   ```go
   restoreCh <-chan struct{} // Channel to receive restore signal
   ```

2. **Validation** (`pkg/agent/internal/core/validation.go`):
   ```go
   // Allow any transition to SUSPEND (like ERROR at line 34)
   if to == proto.StateSuspend {
       return true
   }
   // Allow SUSPEND to return to originating state (read from stateData)
   if from == proto.StateSuspend {
       if suspendedFrom, ok := sm.GetStateValue(KeySuspendedFrom); ok {
           if to == suspendedFrom.(proto.State) {
               return true
           }
       }
   }
   ```

3. **SUSPEND handler** (base class, pattern from BUDGET_REVIEW):
   ```go
   func (sm *BaseStateMachine) handleSuspend(ctx context.Context) (proto.State, bool, error) {
       // Get the state we came from
       suspendedFrom, _ := sm.GetStateValue(KeySuspendedFrom)
       originState := suspendedFrom.(proto.State)

       select {
       case <-sm.restoreCh:
           return originState, false, nil  // Resume exactly where we left off
       case <-time.After(15 * time.Minute):
           return proto.StateError, false, fmt.Errorf("suspend timeout exceeded")
       case <-ctx.Done():
           return proto.StateError, false, ctx.Err()
       }
   }
   ```

4. **Orchestrator polling**:
   - APIs to check determined at startup from config (LLM providers + GitHub)
   - Poll every 30 seconds during suspension
   - Require ALL APIs healthy before restore broadcast

**Status**: READY FOR IMPLEMENTATION

---

### Improvement 3: Story Retry Limit Circuit Breaker

**Problem**: Failing stories get infinitely requeued:
1. Story dispatched to coder-001
2. Coder-001 fails (story issue, not system)
3. Story requeued to coder-002
4. Same failure
5. Infinite loop until all coders exhausted

**Note**: This is separate from Improvement 2. SUSPEND handles system-wide failures (network down). This handles story-specific failures (bad spec, impossible requirements).

**Design**:

```
Per-Story Retry Tracking:
- Track attempt count per story
- Track error context from each attempt
- After N failures (3?) → circuit breaker trips

Circuit Breaker Action (for now):
- Send architect to ERROR state
- Story marked as failed with error contexts
- Future: architect rewrites story given error feedback

Future Enhancement:
- Architect receives: original story + all error contexts
- Architect can: rewrite story, split story, mark blocked, escalate
- New tools: rewrite_story, block_story, escalate_story
```

**Implementation**:

1. **Story attempt tracking** (in story queue or dispatcher):
   ```go
   type StoryAttempt struct {
       CoderID    string
       Timestamp  time.Time
       ErrorMsg   string
       State      proto.State  // State when failure occurred
   }

   // Per-story tracking
   attempts map[string][]StoryAttempt  // storyID -> attempts
   maxRetries = 3
   ```

2. **Circuit breaker check** (before dispatch):
   ```go
   if len(attempts[storyID]) >= maxRetries {
       // Trip circuit breaker - don't dispatch
       // For now: architect → ERROR
       // Future: architect rewrites with error context
   }
   ```

3. **Failure categorization** (future):
   - System failures (timeout, network) → count toward SUSPEND, not retry limit
   - Story failures (test failures, build errors) → count toward retry limit

**Status**: READY FOR IMPLEMENTATION (simple version: retry limit → architect ERROR)

---

## Related Issues

### Database Schema Mismatch
```
SQL logic error: no such column: last_mtime
```
Minor issue - needs schema migration or column addition.

### Git Protocol Mismatch
Mirror may be using SSH even when HTTPS is configured. Need to verify mirror remote configuration.

---

## Summary

| Item | Priority | Status | Notes |
|------|----------|--------|-------|
| Fix 1: Container requirements | HIGH | COMPLETED | Commits `611b75d`, `6254666` |
| Improvement 2: SUSPEND state | HIGH | READY | Preserves in-flight work during outages |
| Improvement 3: Story retry limit | MEDIUM | READY | Simple version: retry limit → ERROR |
| Schema migration | LOW | TODO | Minor issue |
