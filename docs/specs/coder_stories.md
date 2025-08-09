# Coder Agent FSM Refactor – Implementation Stories (v1.0 Readiness)

_This document captures the **minimum set of engineering stories** required to bring the coder‑agent’s finite‑state‑machine (FSM) implementation in line with the canonical workflow agreed on 2025‑07‑06.  
Each story is **independent, testable, and written for an LLM coding agent**.  Follow the order given._

---

## Story 01 – Canonicalise `STATES.md`

|  |  |
|---|---|
| **Goal** | The authoritative description of the coder FSM lives in `STATES.md` and nowhere else. |
| **Tasks** | <br>1. Delete any outdated Mermaid diagrams, tables or prose that conflict with the July 6 decisions.<br>2. Insert an updated Mermaid diagram reflecting the valid transitions:<br>```mermaid<br>stateDiagram-v2<br>    [*] --> PLANNING<br>    PLANNING --> CODING<br>    PLANNING --> QUESTION<br>    CODING --> TESTING<br>    CODING --> QUESTION<br>    FIXING --> TESTING<br>    FIXING --> QUESTION<br>    TESTING --> CODE_REVIEW : tests pass<br>    TESTING --> FIXING      : tests fail<br>    CODE_REVIEW --> DONE    : approved<br>    CODE_REVIEW --> FIXING  : changes‑requested<br>    QUESTION --> PLANNING<br>    QUESTION --> CODING<br>    QUESTION --> FIXING<br>    state DONE <<terminal>>
state ERROR <<terminal>><br>``` |
| **Acceptance Criteria** | *STATES.md* parses in Mermaid Live Editor and shows exactly the arrows above. |

---

## Story 02 – Create a **single source of truth** for transitions

|  |  |
|---|---|
| **Goal** | Eliminate duplicated state tables (`ValidCoderTransitions`, `coderTable`, switch‑case). |
| **Tasks** | 1. Introduce `fsm/coder_fsm.go` containing:<br>&nbsp;&nbsp;```go
var CoderTransitions = map[agent.State][]agent.State{ /* content mirrors STATES.md */ }
```<br>2. Add `func IsValidCoderTransition(from, to agent.State) bool` that consults the map.<br>3. Auto‑generate any handler lookup table from `CoderTransitions` in `init()`. |
| **Acceptance Criteria** | <ul><li>Only **one** hard‑coded transition map in the repository.</li><li>`go vet ./...` and unit tests pass.</li></ul> |

---

## Story 03 – Replace **string literals** with typed constants

|  |  |
|---|---|
| **Goal** | Remove all hard‑coded state names such as `"CODING"` scattered through the code. |
| **Tasks** | 1. Define `type State string` or use existing `agent.State` enum constants.<br>2. Search‑and‑replace raw strings in handlers, tests, and logging calls.<br>3. Add compile‑time checks (`var _ = StateValues["CODING"]`) to prevent regression. |
| **Acceptance Criteria** | `grep -R ""CODING""` and similar for each state returns **0** hits outside the enum definition and tests. |

---

## Story 04 – Align handlers & guards with canonical map

|  |  |
|---|---|
| **Goal** | Ensure every state handler returns only allowed next states. |
| **Tasks** | 1. Edit handlers for **TESTING**, **CODING**, **FIXING**, and **QUESTION** to remove disallowed transitions (see Story 01).<br>2. Add a defensive check:<br>&nbsp;&nbsp;```go
if !IsValidCoderTransition(current, next) { return error }
``` |
| **Acceptance Criteria** | Unit test: walking `CoderTransitions` matrix passes; any illegal transition panics or errors. |

---

## Story 05 – Clean up orphaned code & tests

|  |  |
|---|---|
| **Goal** | Remove dead handlers and outdated tests left by stories 02‑04. |
| **Tasks** | 1. Delete handlers like `handlerTestingToDone` now unreachable.<br>2. Update or remove unit tests that referenced retired edges.<br>3. Run `go test ./...` in CI. |
| **Acceptance Criteria** | No build tags or TODOs referencing removed states; CI green. |

---

### Out‑of‑scope (future stories)

* Structured logging
* Payload helper functions
* AUTO_CHECKIN counter reset and other functional hardening
* Directory traversal guard in `writeFile`

---

## Definition of Done for this refactor epic

1. `STATES.md` matches runtime behaviour.  
2. The FSM is expressed **exactly once** in code.  
3. `go vet`, `staticcheck`, and unit tests pass with `-race`.  
4. No literal state strings appear outside the enum/type definition.  
5. All handlers are defended by `IsValidCoderTransition`.

> **Hand‑off:** Once these stories are merged, open a new ticket set for the next tranche of robustness improvements noted in the code‑review summary.
