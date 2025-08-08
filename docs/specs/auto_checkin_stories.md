# Epic: Prevent Infinite Loops with Auto‑Check‑In

## Context
Coding agents sometimes exceed their iteration budget in **CODING** or **FIXING** without asking for help, leading to silent loops or hard errors.  
We will introduce an **AUTO_CHECKIN** mechanism that pauses the agent and asks the architect for guidance instead of failing outright.

---

## Story 1 – Config: Iteration Budgets

**As** a system maintainer  
**I want** per‑phase loop budgets configurable in YAML  
**So that** architects can tune budgets for story complexity.

| Field | Type | Default | Notes |
|-------|------|---------|-------|
| `coding_budget` | int | 8 | Max CODING loops |
| `fixing_budget` | int | 3 | Max FIXING loops |

### Acceptance Criteria
1. `IterationBudgets` struct added to config (`pkg/config` or similar).
2. Defaults applied when fields are missing.
3. Driver reads the struct and exposes `d.config.CodingBudget` and `d.config.FixingBudget`.

---

## Story 2 – Helper: `checkLoopBudget`

**As** a coder driver developer  
**I want** one helper that tracks loop counts and triggers AUTO_CHECKIN  
**So that** both CODING and FIXING share identical guard‑rails.

### Tasks
1. Implement `checkLoopBudget(sm *agent.BaseStateMachine, key string, budget int, origin CoderState) bool`.
2. Store counters in `state_data` keys:
   * `coding_iterations`
   * `fixing_iterations`
3. Return `true` and populate QUESTION fields when budget is reached:
   * `question_reason = "AUTO_CHECKIN"`
   * `question_origin = origin`
   * `loops`, `max_loops`

### Acceptance Criteria
* Unit test passes for increment & trigger logic.
* No duplication of loop code in `handleCoding` or `handleFixing`.

---

## Story 3 – Trigger AUTO_CHECKIN Transition

**As** a coder agent  
**I want** to transition into `QUESTION.WAITING_ANSWER` when the loop budget is hit  
**So that** the architect can decide what happens next.

### Acceptance Criteria
1. `handleCoding` and `handleFixing` call `checkLoopBudget`.  
2. When it returns `true`, the state machine transitions to `QUESTION`.  
3. Existing channel wiring is reused (no new proto types).

---

## Story 4 – Process Architect Reply

**As** a coder agent  
**I want** to interpret architect replies to AUTO_CHECKIN questions  
**So that** I can continue, pivot, escalate, or abandon appropriately.

### Tasks
* Extend `ProcessAnswer` to parse:
  * `CONTINUE <n>` → increase relevant budget & reset loops.
  * `PIVOT` → reset loops, remain in current state.
  * `ESCALATE` → transition to `STATE_REVIEW`.
  * `ABANDON` → transition to `STATE_FAILED`.
* Reset only the counter matching `question_origin`.

### Acceptance Criteria
* Integration tests confirm loop counters reset on `CONTINUE`.
* Invalid commands yield a structured error sent back to architect.

---

## Story 5 – Tests: Auto‑Check‑In Behaviour

Location: `./tests/integration`

| Test | Scenario | Expected Result |
|------|----------|-----------------|
| `auto_checkin_coding` | Agent hits `coding_budget` | QUESTION raised with reason `AUTO_CHECKIN`, origin `CODING`. |
| `auto_checkin_fixing` | Agent hits `fixing_budget` | QUESTION raised with reason `AUTO_CHECKIN`, origin `FIXING`. |
| `continue_resets_counter` | Architect replies `CONTINUE 2` | Counter resets; agent resumes CODING or FIXING; new budget = old+2. |

### Acceptance Criteria
* All tests pass in CI (`make test`).
* Tests use Harness API only.

---

## Story 6 – Documentation Updates

**As** a developer  
**I want** STATES.md & DESIGN.md to reflect AUTO_CHECKIN  
**So that** contributors understand the new flow.

### Acceptance Criteria
* `STATES.md` state table includes “AUTO_CHECKIN via QUESTION”.
* A sequence diagram shows CODING/FIXING → QUESTION (AUTO_CHECKIN) → CODING/FIXING.

---

## Definition of Done
1. All new & existing tests pass.
2. Lint (`go vet`, `go fmt`, `staticcheck`) is clean.
3. CI pipeline green.
4. Documentation merged.

