# Resilience Improvements Tracking

This document tracks identified issues and proposed improvements for system resilience.

## Completed Fixes

### Fix 1: Container Requirements for App Stories (Immediate)

**Problem**: The `maestro-go-dev` container built by coders is missing:
- User with UID 1000:1000 (required for `--user 1000:1000` flag)
- Git CLI (required for version control operations)

**Impact**: All coders fail with "Permission denied" when trying to create user at runtime because container is read-only.

**Solution**:
1. Update bootstrap template (`pkg/templates/bootstrap/bootstrap.tpl.md`) to specify:
   - Container MUST create unprivileged user with UID 1000:1000
   - Container MUST include git CLI
2. Add validation to container testing:
   - `pkg/tools/capability_test.go` - Add user 1000 check
   - Coder TESTING state validation

**Status**: COMPLETED (commit pending)

---

## Proposed Improvements

### Improvement 2: Network/Service Unavailability Handling

**Problem**: When laptop suspends or network is unavailable:
- Git SSH times out: `ssh: connect to host github.com port 22: Operation timed out`
- Mirror update fails, treated as fatal error
- Agents enter ERROR state and restart loops

**Current Behavior**:
- Mirror update failure blocks workspace setup entirely
- No distinction between transient network issues and permanent failures
- Agents keep retrying and failing

**Proposed Solution**: Universal SUSPEND mechanism

**Option A: SUSPEND state accessible from all non-terminal states**
- Pros: Clean state machine model, explicit state for suspended agents
- Cons: Need to preserve in-progress work, complex state restoration
- Implementation: Add SUSPEND to all agent FSMs, save context on entry, restore on exit

**Option B: Orchestrator-level suspension with heartbeat**
- Pros: Centralized control, agents don't need to know about suspension
- Cons: Orchestrator becomes more complex, may interrupt critical operations
- Implementation:
  1. Orchestrator detects connectivity loss (ping GitHub/LLM endpoints)
  2. Pauses agent dispatch (no new work assigned)
  3. Running agents complete or timeout naturally
  4. Orchestrator polls until connectivity restored
  5. Resumes normal operation

**Option C: Graceful degradation with exponential backoff**
- Pros: Simpler, no new states needed
- Cons: Agents still attempt work during outage
- Implementation:
  1. Mirror update failure is non-fatal if mirror exists
  2. Network operations use exponential backoff with max retries
  3. After max retries, agent enters WAITING (not ERROR)

**Open Questions**:
- How long to wait before declaring network unavailable?
- Should we preserve in-flight LLM conversations?
- How to handle partial work (e.g., code written but not committed)?

**Status**: NEEDS DESIGN DISCUSSION

---

### Improvement 3: Story Failure Circuit Breaker

**Problem**: Failing stories get infinitely requeued:
1. Story dispatched to coder-001
2. Coder-001 fails (system or story issue)
3. Story requeued to coder-002
4. Same failure
5. Infinite loop until all coders exhausted

**Current Behavior**: No distinction between:
- System failures (network, container issues) - affects all stories
- Story-specific failures (bad spec, impossible requirements) - only affects this story

**Proposed Solution**: Two-tier failure handling

**Tier 1: System Failure Detection**
- Track failure reasons across stories
- If multiple stories fail with same root cause → system issue
- Trigger Improvement 2 (suspension/backoff)

**Tier 2: Story-Specific Failure Handling**
- Track failure count per story
- After N failures with different root causes → story issue
- Provide architect with:
  - Original story
  - All error contexts from attempts
  - Request to rewrite/split/clarify story
- Architect can: rewrite story, mark as blocked, escalate to human

**Implementation Considerations**:
- Need failure categorization (system vs story)
- Need per-story attempt tracking with error context
- Architect needs new tools: `rewrite_story`, `block_story`, `escalate_story`
- May need PM involvement for major story rewrites

**Status**: NEEDS DESIGN DISCUSSION

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

## Timeline

| Item | Priority | Status | Notes |
|------|----------|--------|-------|
| Fix 1: Container requirements | HIGH | IN PROGRESS | Blocking all app stories |
| Improvement 2: Suspension | MEDIUM | DESIGN | Needed for laptop/network resilience |
| Improvement 3: Circuit breaker | MEDIUM | DESIGN | Prevents infinite loops |
| Schema migration | LOW | TODO | Minor issue |
