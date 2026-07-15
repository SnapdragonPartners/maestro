+++
title = "Architect-Agent FSM Refactor — Implementation Stories"
edit_date = "2026-07-15"
status = "archive"
+++


# Architect-Agent FSM Refactor — Implementation Stories

These stories bring the **architect agent** codebase into full alignment with the new state diagram and coding standards.  
All stories are **blocking for v1 launch** unless explicitly marked otherwise.

> **Legend**  
> **P0** – Must‑have for launch  
> **P1** – High value, can defer a *few* if needed  
> **TEST** – Test automation only  
> **DOC** – Documentation/cleanup only  

---

## P0 Stories

| ID | Title | Description / Tasks | Acceptance Criteria |
|----|-------|---------------------|---------------------|
| **ARCH‑001** | 💠 *Introduce canonical FSM* | • Create `pkg/architect/architect_fsm.go`<br>• Define `ArchitectState` enum using `iota` (*Waiting, Scoping, Dispatching, Monitoring, Request, Merging, Escalated, Done, Error*)<br>• Add `IsValidArchitectTransition(from, to ArchitectState) bool` with a transition map mirroring the Mermaid diagram.<br>• Provide `String()` for log friendliness. | ✔ File compiles and `go vet ./...` passes.<br>✔ Table‑driven unit test exhaustively checks legal + illegal transitions. |
| **ARCH‑002** | 🔄 *Refactor driver to new states* | • Replace ad‑hoc state constants in `driver.go` with `ArchitectState`.<br>• Re‑wire `processCurrentState` / switch branches.<br>• Loading an unknown state sets state to **Error** and logs a warning. | ✔ All compiler references to removed constants are gone.<br>✔ Driver can load, run, and complete the demo flow. |
| **ARCH‑003** | 🧹 *Purge legacy code & constants* | • Delete unused states (`SPEC_PARSING`, `AWAIT_HUMAN_FEEDBACK`, etc.).<br>• Remove commented‑out code paths.<br>• Ensure no `TODO(backcompat)` remains. | ✔ `go vet ./...` shows zero dead code.<br>✔ `git grep -n "SPEC_PARSING"` returns nothing. |
| **ARCH‑004** | 🔁 *Per‑story merge loop* | • In `review.go`, after a code‑review approval, call `queue.MarkCompleted(storyID)`.<br>• Driver transitions **Merging → Dispatching** immediately.<br>• `Dispatching` enqueues newly‑ready stories; if none remain ⇒ **Done**. | ✔ Integration test `TEST‑002` passes (see below). |
| **ARCH‑005** | 👂 *Unified RequestWorker* | • Delete `AnswerWorker` & `ReviewWorker`.<br>• Add `RequestWorker` goroutine (one per architect).<br>• Handle four sub‑types: `plan`, `code`, `resource`, `question`.<br>• Approve(`code`) ⇒ send to merge channel else return to **Monitoring**.<br>• Decline / change‑request routes as per FSM.<br>• Use single `requestCh chan message.Message`. | ✔ Concurrency model: one request processed at a time.<br>✔ Unit test simulates concurrent requests; order preserved. |
| **ARCH‑006** | 📜 *Extend message protocol* | • In `proto/message.go` add:<br>`const RequestTypeResource = "resource"`.<br>• Struct field additions:<br>`RequestedTokens int \`json:"requestedTokens"\``<br>`RequestedIterations int \`json:"requestedIterations"\``<br>`Justification string \`json:"justification"\`` | ✔ `make test` succeeds across repo.<br>✔ JSON tags use CamelCase. |
| **ARCH‑007** | 🔧 *Handle resource approvals* | • Driver: on approve(resource) send `ResourceApproval` reply to coder.<br>• Document expectation that coder auto‑transitions to **Coding** on approval. | ✔ Unit test mocks coder inbox and asserts approval message. |
| **ARCH‑008** | ⏲ *Escalation timeout guard* | • Add `const EscalationTimeout = 7 * 24 * time.Hour` in `architect_fsm.go`.<br>• In `ESCALATED` state, start timer; on expiry ⇒ **Error**.<br>• Leave timer cancellable on human reply. | ✔ Integration test waits < 100 ms using time stub; transition fires. |

## P1 Stories

| ID | Title | Description / Tasks | Acceptance Criteria |
|----|-------|---------------------|---------------------|
| **ARCH‑009** | 🗄 *Refactor dispatcher helpers* | • Thin wrapper `ArchitectAck()` for approvals; keeps dispatcher generic. | ✔ No architect logic leaks into dispatcher layer. |

## TEST Automation

| ID | Title | Description / Tasks | Acceptance Criteria |
|----|-------|---------------------|---------------------|
| **TEST‑001** | 🔬 *FSM unit tests* | • Table‑driven tests for all legal & illegal transitions in `architect_fsm_test.go`. | ✔ `go test ./...` passes; branch coverage ≥ 95 % for FSM. |
| **TEST‑002** | 🔄 *Merge/dispatch integration* | • Spin up in‑memory queue with two dependent stories A→B.<br>• Approve code for A; expect B auto‑queued.<br>• Approve B; expect architect FSM ⇒ **Done**. | ✔ Test passes without race detector complaints. |
| **TEST‑003** | ❓ *RequestWorker concurrency* | • Fire 100 mixed requests; ensure they are processed serially in FIFO order.<br>• Verify channel buffer sizes and no goroutine leaks (`runtime.NumGoroutine()`). | ✔ Passes with `-race`. |

## DOC & Cleanup

| ID | Title | Description / Tasks | Acceptance Criteria |
|----|-------|---------------------|---------------------|
| **DOC‑001** | 📝 *README & diagram* | • Replace old diagram with new Mermaid.<br>• Document message schema changes & escalation timeout. | ✔ `README.md` builds via `markdownlint`. |

---

### “Definition of Done” checklist (global)

- ✅ All **P0** stories complete and tests green (`go test ./... -race`).  
- ✅ No unused code, no TODO(backcompat) markers.  
- ✅ `golangci-lint run` returns **zero** issues (except test‑only).  
- ✅ `make bench` performance hit ≤ 5 % versus pre‑refactor.  
- ✅ CI pipeline passes on first attempt.

---

### Out‑of‑scope

* Queue snapshot/persistence  
* Metrics/observability hooks  
* Backward compatibility with legacy states or messages

---
