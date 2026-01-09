# RC1 Fix Tracking

Tracking fixes identified from rc1 log analysis (story 45cfa904 failures).

## Root Cause Analysis

Story 45cfa904 failed 3 times due to multiple issues:
1. Rate limiter deadlock (request > bucket capacity)
2. `ask_question` tool bug (question data not stored in state)
3. Architect blocked, unable to respond to coder requests (SUSPEND not triggered)

## Issue-to-Fix Mapping

| Issue | Root Cause | Fix |
|-------|------------|-----|
| Attempt 1: coder-002 timeout | Architect blocked on rate limiter (impossible request) | Fix #1 (prevents impossible requests) + Fix #3 (timeout safety net) |
| Attempt 2: ask_question bug | Question data not stored before state transition | Fix #2 |
| Attempt 3: coder-001 timeout | Architect still blocked from Attempt 1 | Fix #1 + Fix #3 |
| SUSPEND not triggered | Rate limiter blocks before retry middleware sees error | Fix #3 (timeout returns error → retry → SUSPEND) |
| Fatal shutdown on 3x failure | No escalation path to PM | Deferred |

**All identified issues are addressed by Fixes #1-3.**

---

## Fixes

### 1. Rate Limiter Config Validation
**Status:** DONE

**Problem:** If `max_context_size` > `tokens_per_minute * 0.9`, requests can exceed the bucket's max capacity, causing permanent deadlock.

- Config: `tokens_per_minute: 60,000`
- Effective max capacity: 54,000 (90% buffer at line 105 of limiter.go)
- Request size: 55,416 tokens
- Result: Blocked forever - bucket can never satisfy request

**Fix:**
- At config load, enforce: `max_context_size ≤ tokens_per_minute * 0.9`
- If violated, set `max_context_size = tokens_per_minute * 0.9` and log warning
- Update default configs so TPM ≥ model's max context size

**Files:**
- `pkg/config/config.go` - validation logic
- `config/config.json` - update defaults

---

### 2. Fix `ask_question` Tool Bug
**Status:** TODO

**Problem:** The `ask_question` tool returns a ProcessEffect that triggers QUESTION state transition, but question data is not stored in state before transition occurs.

**Log Evidence:**
```
[coder-003] INFO: Executing tool: ask_question
[coder-003] INFO: Tool ask_question completed in 0.000s
[coder-003] INFO: Tool returned ProcessEffect with signal: QUESTION
[coder-003] INFO: State machine transition: PLANNING → QUESTION
[system] ERROR: no pending question data found in state
```

**Fix:** Ensure `ask_question` tool populates `state.PendingQuestion` before returning ProcessEffect.

**Files:**
- TBD - need to locate ask_question implementation

---

### 3. Rate Limiter Timeout (Safety Net)
**Status:** TODO

**Problem:** Even if config validation (Fix #1) prevents impossible requests, the rate limiter has no maximum wait time. If something goes wrong, it blocks indefinitely and SUSPEND is never triggered.

**Root Cause:**

The rate limiter blocks **BEFORE** the retry middleware:
```
Rate Limit Middleware → Retry Middleware → LLM Client
        ↓ (blocks here)
   limiter.Acquire() loops forever
```

The retry middleware never sees an error, so `ServiceUnavailableError` is never emitted, and SUSPEND is never triggered.

**Fix:**

Add a sanity-check timeout to `Acquire()` based on theoretical maximum wait time:

```go
// Maximum logical wait = agent_count × 1 minute
// Rationale:
// - Agents use LLM serially (one request at a time per agent)
// - Bucket refills to full capacity over ~1 minute (10 refills × 6 seconds)
// - Worst case FIFO: each agent ahead drains bucket, you wait for their refill cycle
// - If waiting longer than this, something is fundamentally wrong

maxWait := time.Duration(config.GetTotalAgentCount()) * time.Minute
startTime := time.Now()

for {
    // ... existing acquire logic ...

    if time.Since(startTime) > maxWait {
        return nil, fmt.Errorf("rate limit acquisition timeout after %v "+
            "(requested %d tokens, max capacity %d)",
            maxWait, tokens, l.maxCapacity)
    }

    // ... existing select ...
}
```

**Agent Count Calculation:**
```go
// In pkg/config/config.go
func GetTotalAgentCount() int {
    // 1 architect + 1 PM + MaxCoders + 1 hotfix
    return config.Agents.MaxCoders + 3
}
```

This error flows through retry middleware → `ServiceUnavailableError` → SUSPEND.

**Files:**
- `pkg/config/config.go` - Add `GetTotalAgentCount()`
- `pkg/agent/middleware/resilience/ratelimit/limiter.go` - Add timeout to `Acquire()`

---

## Deferred (Future Work)

### 4. 3x Story Failure Should Escalate to PM
**Status:** DEFERRED (complex)

**Problem:** Currently, 3 story failures cause architect ERROR state and fatal shutdown. Should instead escalate back to PM for remediation.

**Current Behavior:**
```
[architect-001] INFO: Story 45cfa904 attempt count: 3/3
[architect-001] ERROR: Story 45cfa904 exceeded retry limit (3 attempts). Transitioning architect to ERROR.
[supervisor] ERROR: FATAL SHUTDOWN
```

**Desired Behavior:** Escalate to PM, allow PM to modify spec/stories, retry with fresh context.

---

## Timeline

| Time | Event |
|------|-------|
| 01:45:10 | Story 45cfa904 dispatched to coder-002 |
| 01:56:56 | Architect hits rate limit (need 55,416, have 54,000) |
| 02:10:01 | Attempt 1 fails - coder-002 timeout waiting for architect |
| 02:10:05 | Attempt 2 starts (coder-003) |
| 02:10:47 | Attempt 2 fails - ask_question bug |
| 02:10:50 | Attempt 3 starts (coder-001) |
| 02:22:47 | coder-001 submits plan, waits for approval |
| 02:29:16 | Attempt 3 fails - timeout (architect still blocked) |
| 02:29:16 | 3/3 retries exhausted, FATAL SHUTDOWN |
