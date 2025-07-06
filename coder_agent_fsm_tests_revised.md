# Comprehensive Integration & Property Tests for Coder FSM  
_All tests live under `./tests/integration` and are run with `go test ./...`._

## Harness & Test Utilities

* **TestHarness**  
  * `Run(ctx)` – pumps messages until **all** agents satisfy `StopWhen`.  
  * `Wait(ctx, wantState proto.CoderState)` – blocks until the coder reaches `wantState` or context timeout.  
  * `StopWhen` – optional func passed to `Run` that returns `true` once global stop‑condition is met (default: `coderState == proto.DONE`).  
* **Agent Channels**  
  * One singleton **architect** agent.  
  * **Per‑coder channels** (`archToCoder`, `coderToArch`) – prevents head‑of‑line blocking in concurrency tests.  
* **Shared helpers**  
  * `requireState(t, coder, want)`  
  * `expectMessage(t, ch, matcherFn)`  

---

## Story 1 Happy‑Path `REQUEST → RESULT → DONE`

| Aspect | Value |
|--------|-------|
| **Given** | A single coder and an architect that always replies `APPROVED`. |
| **When** | The coder sends `PLAN_REQUEST`, receives `APPROVED`, transitions. |
| **Then** | Coder reaches `proto.DONE`, architect receives exactly 1 `RESULT`. |

---

## Story 2 Revision Cycle

1. Architect replies `CHANGES_REQUESTED`.  
2. Coder transitions `PLAN_REVIEW → REVISING`, sends new `PLAN_REQUEST`.  
3. Architect now replies `APPROVED`.  
4. Assert final state `DONE` and both plans are recorded.

---

## Story 3 Invalid Code Block Recovery

* Architect returns `CHANGES_REQUESTED` with an _invalid_ code block (no back‑ticks).  
* Coder must generate a helpful error message, stay in `REVISING`, and request clarification.  
* Architect resends a valid block → coder proceeds to `CODING` then `DONE`.

---

## Story 4 High‑Concurrency (10 Coders)

| Aspect | Value |
|--------|-------|
| **Agents** | 1 architect, **10 coders**, each with its own channel pair. |
| **Goal** | All coders finish a happy‑path run within **500 ms** wall‑clock. |
| **Assert** | No inter‑coder blocking; `go test -race` & `goleak.VerifyNone` pass. |

---

## Story 5 Unknown `approval_type` Fallback

* Architect responds with malformed `approval_type = "YEP"`.  
* Coder stays in `PLAN_REVIEW`, logs the anomaly, _resends_ `PLAN_REQUEST`.  
* Architect replies with a valid `APPROVED`.  
* **Expect** coder transitions `PLAN_REVIEW → CODING`.

---

## Story 6 Plan Timeout & Resubmission

* Architect deliberately withholds a response for **> coderTimeout** (configurable 30 s in harness, shrunk to 100 ms via `time.Sleep`).  
* Coder **resends the identical `PLAN_REQUEST`** exactly once.  
* Architect answers `APPROVED` → coder continues to `CODING` then `DONE`.  
* Assert only one automatic resubmission per timeout window.

---

## Story 7 Enum & Helper Unit Tests (coverage booster)

* Table‑driven unit tests for:  
  * `proto.ParseRequestType`  
  * `proto.NormaliseApprovalType`  
  * Transition validity via `IsValidCoderTransition`  
* Must hit edge cases (unknown enums, deprecated aliases).

---

## Story 8 Property‑Based Fuzzing

* Use `testing/quick` to generate random interleavings of:  
  * `APPROVED`, `CHANGES_REQUESTED`, malformed payloads, timeouts.  
* Context timeout **≤ 2 s**.  
* **Pass condition**: coder ends in either `DONE` **or** a legal waiting state (`PLAN_REVIEW`, `REVISING`, `CODING`) _without panic or deadlock_.  

---

## Coverage Target

* `go test ./... -race -cover` must report **≥ 90 %** overall coverage for `pkg/coder` and `pkg/proto`.  

---

### CLI Examples

```bash
# Run everything with the race detector
go test -race ./...

# Single concurrency test with verbose output
go test -v ./tests/integration -run TestHighConcurrency
```
