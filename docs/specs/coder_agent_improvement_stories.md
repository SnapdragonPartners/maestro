# Coder Agent Improvement Stories
_Author: OpenAI o3_  
_Last updated: 2025‑07‑05_

These executable stories capture every change discussed in the code review.  
Implement them **in order**; each story should pass before you start the next one.

## Implementation Decisions
- **Focus**: Coder agent improvements only, using mock architect (not real architect)
- **Story order**: Can be reordered for implementation efficiency if needed  
- **File organization**: Keep proto consolidated in message.go for now (~300 lines acceptable)
- **Test location**: Integration tests go in /tests subdirectories
- **Breaking changes**: Temporary workflow breakage is acceptable during implementation
- **State changes**: Check before modifying original STATES.md concepts

---

## Story 1 – Centralise Protocol Constants & Enums

### Context
Many literals that define the wire protocol (`"approval"`, `"approval_request"`, payload keys…) are scattered through agent code. This leads to duplication and mismatches.

### Tasks
- [ ] **IMPLEMENTATION NOTE**: Add to existing `proto/message.go` instead of new file
- [ ] Add to consolidated `proto/message.go` file, exposing:
  ```go
  type RequestType string
  const (
      RequestApproval RequestType = "approval"
      RequestApprovalReview RequestType = "approval_request"
      RequestQuestion RequestType = "question"
      // …add any others you need
  )

  // Common payload / metadata keys
  const (
      KeyRequestType   = "request_type"
      KeyApprovalType  = "approval_type"
      KeyAnswer        = "answer"
      KeyReason        = "reason"
      KeyQuestion      = "question"
  )
  ```
- [ ] Export helper `func ParseRequestType(string) (RequestType, error)`.
- [ ] Replace every hard‑coded literal in **all** packages with these constants.
- [ ] Update unit tests.

### Acceptance Criteria
1. **Search** the repo for hard‑coded `"approval"`/`"request_type"` strings – none remain outside `proto`.
2. All tests pass.

---

## Story 2 – Robust Approval Message Handling

### Context
`coder.handleResultMessage` trusts that `request_type` and `approval_type` are in the payload and already lower‑case.

### Tasks
- [ ] Add helper in `proto`:  
  `func NormaliseApprovalType(string) (ApprovalType, error)`
- [ ] Modify `handleResultMessage` to:
  1. Try payload, then metadata, for both keys.
  2. Pass value through the new normaliser.
  3. Return an explicit error if still unknown.
- [ ] Extend unit tests covering:
  * Mixed‑case payload.
  * Value appearing only in metadata.

### Acceptance Criteria
* A RESULT with `Metadata["request_type"]="approval"` and `Payload["approval_type"]="Plan"` is accepted and moves FSM to **PLAN_APPROVED**.

---

## Story 3 – Thread‑Safe, Agent‑Local Transition Tables

### Context
`performTransition` mutates a package‑level map `agent.ValidTransitions`; concurrent agents race here.

### Tasks
- [ ] Copy the canonical transition map at **agent initialisation** into an **unexported struct field**.
- [ ] Remove the global map mutation; validate transitions against the local copy.
- [ ] Guard any shared maps in `proto` with a `sync.RWMutex`.

### Acceptance Criteria
* Run `go test ./... -race` with 500 concurrent agents – **no data races** detected.

---

## Story 4 – Single‑Step State Processing Loop

### Context
`Run()` and `ProcessTask()` both spin; when an external RESULT arrives `Run()` is re‑entered and nests loops.

### Tasks
- [ ] Refactor to expose `Step()` that executes **one** state transition.
- [ ] `Run()` loops on `Step()`.
- [ ] When an external event is injected (e.g., RESULT) call only `Step()`.

### Acceptance Criteria
* CPU usage during idle periods is <2% (previously >15%).
* Integration tests still pass.

---

## Story 5 – Accept Code Blocks Without Language Fences

### Tasks
- [ ] In `parseAndCreateFiles`, recognise ```code``` fences _without_ a language tag and default to `"txt"`.
- [ ] Add tests with LLM output missing the language specifier.

### Acceptance Criteria
* A markdown chunk  
  ```
  ```
  some content
  ```
  ```
  is written to `unknown.txt` (or similar) without panic.

---

## Story 6 – Auto‑derive the **Coder** State Set

### Tasks
- [ ] Replace the hard‑coded slice in `isCoderState` by evaluating keys of `ValidCoderTransitions`.
- [ ] Add a build‑time check that the derived set is non‑empty.

### Acceptance Criteria
* Adding a new coder state in the transition map automatically admits it to validation – no manual edits.

---

## Story 7 – Extract Reusable Debug Logging

### Tasks
- [ ] Add `logx` package exposing  
  `func Debug(ctx context.Context, domain, format string, v ...any)`.
- [ ] Replace local file‑log boilerplate with `logx.Debug`.
- [ ] Provide env var `DEBUG_LOG_DIR` to override output path for all agents.

### Acceptance Criteria
* One helper used by ≥2 different agent drivers.
* Setting `DEBUG_LOG_DIR=/tmp` writes logs there.

---

## Story 8 – Integration Tests for REQUEST → RESULT Handshake

### Tasks
- [ ] Spin up in‑memory architect + coder agents with channels.
- [ ] Simulate PLAN request, approve, then CODE request, approve.
- [ ] Assert final coder state is **DONE**.

### Acceptance Criteria
* `go test ./integration -run TestPlanCodeHappyPath` passes.
* Flake‑rate <0.1% over 1000 runs (`gotest -count 1000`).

---

## Done Definition
All acceptance criteria satisfied, `go vet ./...` clean, **race tests pass**, and code coverage ≥ 80 % overall.

---
