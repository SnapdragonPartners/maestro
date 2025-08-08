# Architect Agent v1.0 Backlog (Executable Stories)

*Version target: **v1.0.0***  
*Owner: Architect Agent team*  
*Created: 2025-07-09 00:56 UTC*

---

## AR‑001  Validate FSM Transitions & Global Self‑Loop Rule

### Background  
The architect’s driver can transition to any state without validation, and self‑loops are permitted implicitly. We need strict enforcement against illegal moves while retaining the **global** self‑loop allowance.

### Tasks  
1. Add `func IsValidArchitectTransition(from, to ArchitectState) bool` in `pkg/agent/state_validation.go` (reuse coder logic).  
2. Gate every call to `transitionTo()` through this helper.  
3. When an illegal transition is attempted:  
   * Log with `logx.Errorf` including `from`, `to`.  
   * Transition driver to `StateError`.  
4. Update **STATES.md** with a note: “Self‑loops (`state → same state`) are always valid.”

### Acceptance Criteria  
* Unit tests cover every legal transition and at least three illegal ones.  
* Driver never enters an undefined state in normal operation.  
* Self‑loops produce **no** warning or error.

### Estimate  
M (4 – 6 hours)

---

## AR‑002  Escalation Watchdog → ABANDON Review & Re‑queue

### Background  
If an escalation is not answered within the timeout, the story should be **abandoned** (via a review response) and pushed back to *DISPATCHING*.

### Tasks  
1. Add `const EscalationTimeout = 30 * time.Minute` (or value from STATES.md).  
2. Inside `handleEscalated` start a ticker to watch the timeout.  
3. On expiry:  
   1. `sendAbandon(storyID)` emitting a review of type **ABANDON**.  
   2. `queue.PushFront(storyID)`.  
   3. `transitionTo(StateDispatching)`.  
   4. `logx.Warnf("Escalation timeout for %s – abandoned", storyID)`.  

### Acceptance Criteria  
* A synthetic test simulating timeout shows the story re‑queued and driver in `DISPATCHING`.  
* No legacy `ERROR` transition path remains for this case.

### Estimate  
M (4 hours)

---

## AR‑003  Replace Busy Loops with Blocking `select` + Heartbeat

### Background  
100‑iteration polling loops waste CPU and hide liveness. Replace with blocking waits plus periodic debug heartbeats.

### Tasks  
1. Define `const HeartbeatInterval = 30 * time.Second`.  
2. Refactor the main workflow loop and long‑lived handlers (`Monitoring`, `Merging`, etc.):  
   * Block on work channels.  
   * Include `case <-time.After(HeartbeatInterval): logx.Debugf("heartbeat: %s", state)` in the `select`.  
3. Remove the old `maxIterations` guard.

### Acceptance Criteria  
* CPU usage remains near idle when idle.  
* Debug log shows heartbeat roughly every 30 s in idle states.

### Estimate  
M (3 hours)

---

## AR‑004  Context‑Aware Dispatcher Send with Timeout Protection

### Background  
Channel sends can block forever if buffers are full. Protect every send with timeout and context handling.

### Tasks  
1. Add `const DispatcherSendTimeout = 500 * time.Millisecond`.  
2. Wrap `DispatcherAdapter.SendMessage` send operation:

```go
select {
case ch <- msg:
    return nil
case <-time.After(DispatcherSendTimeout):
    return ErrSendTimeout
case <-ctx.Done():
    return ctx.Err()
}
```

3. On `ErrSendTimeout`, caller logs WARN and transitions to `StateError`.  
4. Add unit tests: success, timeout, and ctx‑cancel cases.

### Acceptance Criteria  
* Architect never blocks indefinitely on channel send.  
* WARN log emitted when timeout path triggered.  

### Estimate  
S (2 hours)

---

## AR‑005  Channel Construction via Orchestrator Configuration

### Background  
Buffer size should scale with agent count and be orchestrator‑controlled.

### Tasks  
1. Define JSON schema:

```jsonc
{
  "agents": [
    { "id": "coder‑1", "queue_size": 10 }
  ]
}
```

2. Orchestrator reads the file and constructs per‑agent channels with specified capacities.  
3. Remove hard‑coded `make(chan…, 1)` from driver; accept channel references in constructor.

### Acceptance Criteria  
* Architect driver takes pre‑created channels via its `NewDriver` signature.  
* Integration test with 3 agents and distinct buffer sizes passes.

### Estimate  
L (6‑8 hours)

---

## AR‑006  Logging Migration to `pkg/logx`

### Background  
Prints hinder structured logging.

### Tasks  
1. `go vet` to find `fmt.Print*` calls in architect packages.  
2. Replace with the nearest `logx` level (`Debug`, `Info`, `Warn`, `Error`).  
3. Ensure fields `state`, `story_id`, and `agent_id` are included where relevant.

### Acceptance Criteria  
* `grep -R "fmt\." pkg/architect` returns no matches.  
* Log lines emit in JSON when `logx` is configured that way.

### Estimate  
S (2 hours)

---

## AR‑007  Graceful Shutdown: Flush & Abort

### Background  
On *SIGINT/SIGTERM* the architect must persist state and exit quickly.

### Tasks  
1. Driver listens for context cancellation from orchestrator.  
2. On cancel:  
   * Complete any `SendMessage` in flight (respecting timeout).  
   * Persist current state to disk (existing `Flush()` already does this).  
   * Exit main loop.

### Acceptance Criteria  
* Integration test sends `ctx.Cancel()` and verifies flush file exists and process exits.  

### Estimate  
M (4 hours)

---

## AR‑008  Legacy Code & Mock Cleanup

### Background  
Stale helpers (`parseStateString`, legacy enums, etc.) risk confusion.

### Tasks  
1. Delete `parseStateString` and any unused legacy constants.  
2. Ensure only the new `ArchitectState` enum appears in persistence.  
3. Audit `*_mock.go` files; keep only ones used in tests.  

### Acceptance Criteria  
* Running `go test ./...` passes.  
* `git grep -i legacy` shows no hits in production code.

### Estimate  
S (2 hours)

---

## Out‑of‑Scope (Phase‑2 items)

* Prometheus metrics  
* Hot‑reload of JSON config  
* Build‑tag segregation of mocks

---

**Total estimated time:** ~27–31 hours (should fit a single sprint).
