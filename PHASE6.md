# Phase 6 — Refactor & State‑Machine Alignment

This phase updates the codebase to match the **v2 state diagrams** and cleans up the package layout.  It assumes all Phase 3–5 functionality is merged.

Front‑matter schema unchanged.

## Table of Contents

| ID  | Title                                                   | Est. | Depends |
| --- | ------------------------------------------------------- | ---- | ------- |
| 060 | Repository refactor to new package layout               | 3    | 051,053 |
| 061 | Coding Agent driver update to v2 FSM                    | 4    | 060     |
| 062 | Architect driver update (merged queue/dispatch + chans) | 4    | 060     |
| 063 | Dispatcher & channel wiring                             | 2    | 061,062 |
| 064 | Documentation & diagram sync                            | 1    | 061,062 |

---

### Story 060 — Repository refactor to new package layout

```markdown
---
id: 060
title: "Repository refactor to new package layout"
depends_on: [051,053]
est_points: 3
---
**Task**  
Re‑organize source tree:
```

/pkg/agent       # common abstractions (LLMClient, Tool registry, FSM base)
/pkg/coder       # Claude coding agent implementation
/pkg/architect   # o3 architect agent implementation
/pkg/tools       # existing MCP tools (shell, file, github)
/agents          # REMOVE (moved code into pkg/\*)

```
Steps:
1. Move `driver_agent.go` → `/pkg/architect/driver.go`.
2. Move `claude_live.go` → `/pkg/coder/claude.go` and update imports.
3. Extract shared structs (`AgentMsg`, `State`, contextmgr) into `/pkg/agent`.
4. Update `go.mod`, fix import paths, run `go vet` & `go test`.

**Acceptance Criteria**
* `go test ./...` green after move.
* No code references `/agents/`.
```

### Story 061 — Coding Agent driver update to v2 FSM

```markdown
---
id: 061
title: "Coding Agent driver update to v2 FSM"
depends_on: [060]
est_points: 4
---
**Task**  
Refactor `/pkg/coder/driver.go`:
1. Implement new `State` enum per master diagram.
2. Create single `QUESTION` state with `origin` metadata (use helper: `NewQuestion(origState, msg)`).
3. Add explicit `TESTING` and `FIXING` states; on test fail go to `FIXING`.
4. Remove `TOOL_INVOCATION` state; treat tool calls as in‑state events.
5. Add retry & timeout logic; store `{state, retries, ts}` in `STATUS.json`.
6. PLAN_REVIEW and CODE_REVIEW both use REQUEST→RESULT flow with approval payload.

**Acceptance Criteria**
* Integration: `/health` story runs through PLANNING → DONE with mocks.
* Test simulate failure path using table-driven tests: `{pass bool, attempts int}` where mock test runner fails first N attempts, driver loops FIXING→TESTING until pass or timeout.
```

### Story 062 — Architect driver update (merged queue/dispatch + channels)

```markdown
---
id: 062
title: "Architect driver update (merged queue/dispatch + channels)"
depends_on: [060]
est_points: 4
---
**Task**  
Replace separate queue & dispatch states with `QUEUE_AND_DISPATCH` in `/pkg/architect/driver.go`:
1. Maintain `architect_queue.json` with statuses.
2. Use buffered channels (size 1): `readyStoryCh`, `idleAgentCh`, `reviewDoneCh`, `questionAnsweredCh`.
3. Spawn long-running **ANSWER_WORKER** & **REVIEW_WORKER** goroutines at driver start; workers send back on channels.
4. Implement `AWAIT_HUMAN_FEEDBACK` state with retry counter.

**Acceptance Criteria**
* Mock run shows queue processed, workers spawn, DONE when queue empty.
* Escalation path logs and waits for human flag.
```

### Story 063 — Dispatcher & channel wiring

```markdown
---
id: 063
title: "Dispatcher & channel wiring"
depends_on: [061,062]
est_points: 2
---
**Task**  
Update `pkg/dispatch`:
1. Expose `SubscribeIdleAgents()` returning `idleAgentCh`.
2. Notify architect driver when coding agent finishes (`RESULT`).
3. Route architect answer/review messages back to coding agent.
4. Close `idleAgentCh` when architect exits for graceful dispatcher shutdown.

**Acceptance Criteria**
* End‑to‑end smoke test: architect dispatches → coder completes → architect marks done.
```

### Story 064 — Documentation & diagram sync

```markdown
---
id: 064
title: "Documentation & diagram sync"
depends_on: [061,062]
est_points: 1
---
**Task**  
Update `/docs/` and `master_state_diagrams_v2.md`:
1. Ensure diagrams exactly match implemented enums.
2. Add README section on channel architecture.

**Acceptance Criteria**
* `make lint-docs` passes (Markdown linter).
```

---

## Implementation Notes

### Question State Helper Pattern
```go
func NewQuestion(orig State, q AgentMsg) AgentMsg {
    q.Type = MsgTypeQuestion
    q.Metadata = map[string]string{"origin": orig.String()}
    return q
}
// Driver reads msg.Metadata["origin"] to know which state to resume
```

### Worker Creation Pattern  
```go
answerW := NewAnswerWorker(questionCh, questionAnsweredCh)
reviewW := NewReviewWorker(reviewReqCh, reviewDoneCh)
go answerW.Run(ctx)
go reviewW.Run(ctx)
// Workers block on inbound channel; architect pushes work onto it
```

### Review Flow Pattern
Both PLAN_REVIEW and CODE_REVIEW emit `QUESTION` with `origin = PLAN` and payload `{"plan": <json>}`. Architect replies with `RESULT { "approved": true|false, "feedback": "…" }`. Driver routes back to `PLANNING` on rejection or forward to `CODING` on approval.

---

> **Generated:** 2025‑06‑11

