+++
title = "Maestro UI v1.0 — Engineering Stories"
edit_date = "2026-07-15"
status = "archive"
+++


# Maestro UI v1.0 — Engineering Stories

> **Purpose**  
> Break the agreed‑upon Maestro UI technical specification into discrete, testable engineering tasks suitable for implementation by a coding LLM.  
> Target runtime is a *single‑user*, *localhost‑only* deployment served over **HTTP on port 8080** from inside the Go orchestrator binary.

---

## 🗺️ Context

| Aspect | Decision |
|--------|----------|
| **Languages** | Go backend + Vanilla JS frontend, Tailwind CSS (compiled) |
| **Process model** | One Maestro **run** at a time; one spec per run |
| **Work directory** | Passed via CLI flag or created as temp dir |
| **Agent scale** | ≤ 12 agents expected |
| **Logging** | Truncate and recreate `<workdir>/logs/run.log` each run |
| **Polling** | 1 s interval (XHR); backend‑offline banner after 3 consecutive failures |
| **Security** | Localhost only; no auth / CSRF for v1.0 |

---

## 📦 Epics & Stories

### **Epic A — Backend API (Go)**

| # | Title | Description | Acceptance Criteria |
|---|-------|-------------|---------------------|
| **A‑1** | **Extend `AgentState` schema** | Add the following typed fields to `state.AgentState`: `Plan *string`, `TaskContent *string`, `Transitions []Transition` where `Transition {From, To string; TS time.Time}`. Provide helper `AppendTransition(from, to)`. Bump JSON `Version` tag to `"v1"`. | • Round‑trip JSON preserves new fields.<br>• Saving state twice adds two transition entries. |
| **A‑2** | **`GET /api/agents`** | Return list `[ {id, role, state, last_ts} ]` for every agent file discovered via `Store.ListAgents()`. | • Hitting endpoint with three mock files returns three items sorted by ID. |
| **A‑3** | **`GET /api/agent/:id`** | Return full `AgentState` JSON. | • Unknown ID → HTTP 404. |
| **A‑4** | **`GET /api/queues` (MVP)** | Return per‑queue `{name, length, heads:[{id,type,from,to,ts}]}` where `heads` ≤ 25. Uses stub `dispatcher.DumpHeads(n int)`. | • Unit test with stub dispatcher returns correct counts & head slice length. |
| **A‑5** | **`POST /api/upload`** | Accept `.md` ≤ 100 kB; reject if architect not `WAITING` (HTTP 409). Save to `<workdir>/stories/` and inject message. | • Happy‑path returns 201; busy architect returns 409. |
| **A‑6** | **`POST /api/answer`** | Body `{text}`. Inject `ANSWER` message to architect queue; remove corresponding escalation. | • Escalation banner in UI clears within 2 s of posting. |
| **A‑7** | **`POST /api/shutdown`** | Call existing `dispatcher.Stop(ctx)` and return 202 when accepted. | • Dispatcher stub records `Stop` call. |
| **A‑8** | **`GET /api/logs`** | Params: `domain` (opt), `since` (RFC3339). Return ≤1 000 newest lines from `<workdir>/logs/run.log` filtered by prefix. | • Filtering by `domain=coder` excludes architect lines. |
| **A‑9** | **`GET /api/healthz`** | Respond `{status:"ok", version:"v1.0"}`. | • Always 200 when server alive. |

---

### **Epic B — Frontend Skeleton (Vanilla JS + Tailwind)**

| # | Title | Description | Acceptance Criteria |
|---|-------|-------------|---------------------|
| **B‑1** | **Tailwind build pipeline** | Commit compiled `tailwind.css`; add `npm run build-css`. Simple smoke‑test page shows Tailwind button. | • `npm run build-css` regenerates identical file. |
| **B‑2** | **Global polling service** | `main.js` fetches `/api/agents` every 1 s; header shows “Last updated hh:mm:ss”. Backend‑offline banner after 3 consecutive failures; clears on next success. | • Dev‑tools network tab shows steady 1 s cadence. |
| **B‑3** | **Agent grid** | Render colored blocks per agent (state‑based color). Expander fetches `/api/agent/:id` once and shows Plan, TaskContent, transition table. | • Time‑in‑state counter updates live. |
| **B‑4** | **Queue viewer** | Accordion with three queues; open state polls `/api/queues` every 1 s and displays heads table. | • Closing accordion stops extra polling. |
| **B‑5** | **Escalation banner + modal** | Poll agent list for any `state=="ESCALATED"`. Banner opens modal listing all pending questions. Submitting answer posts to `/api/answer`. | • Submitting answer removes question from list. |
| **B‑6** | **Spec upload UI** | Drag‑drop & file picker. Disabled when architect not `WAITING`. Success toast on 201; error toast on 409 or size violation. | • Dropping >100 kB file shows validation toast without request. |
| **B‑7** | **Logs panel** | Toggle panel; fetch `/api/logs?domain=X`. Autoscroll checkbox default on. | • Turning off autoscroll keeps viewer stationary while new lines arrive. |
| **B‑8** | **Cancel run** | Button posts `/api/shutdown`, disables itself, shows “stopping…” until `/api/agents` returns empty list. | • On success, upload button re‑enabled. |

---

### **Epic C — Dev Experience & Fixtures**

| # | Title | Description | Acceptance Criteria |
|---|-------|-------------|---------------------|
| **C‑1** | **Dev‑mode runner** | `make ui-dev` launches orchestrator with `--workdir=$(mktemp -d)` and serves UI at `http://localhost:8080`, auto‑opens browser. | • Running target displays empty dashboard without errors. |
| **C‑2** | **Static fixture server** | `make ui-fixture` serves JSON & logs from `/test/fixtures/` so frontend can be developed without live backend. | • UI loads and displays fixture data correctly. |

---

## 🔚 Done‑Definition

A story is *done* when:

1. All acceptance criteria pass via automated test or manual check.
2. `go test ./...` and `npm run lint` both succeed.
3. Documentation (`README.md` or inline) updated where applicable.

---

### ⏭️ Next Steps (post‑v1.0)

* SSE/WebSocket stream for agent summaries if polling proves heavy.  
* Multi‑run history view (workdir sub‑folders keyed by `runID`).  
* Basic‑auth middleware for remote deployments.  
* Message payload inspection & filtering.

---

## 📝 Implementation Clarifications (Added During Development)

### Backend Integration Decisions:
- **A-1 AgentState Schema**: ✅ Add `Plan`, `TaskContent`, `Transitions` fields directly to struct (not in `Data` map) for type safety
- **A-4 Queue Inspection**: ✅ Use existing queue slices (`architectQueue`, `coderQueue`, `sharedWorkQueue`) + input channel status monitoring via `dispatcher.DumpHeads(n)`
- **A-6 Escalation System**: 🔄 Frontend for existing `pkg/architect` escalation flow with `EscalationHandler.ResolveEscalation()` API *(pending)*
- **A-8 Logging**: ✅ File-based log streaming with domain filtering (`?domain=coder`), time filtering (`?since=RFC3339`), supports both debug logs and `<workdir>/logs/run.log`
- **Architecture**: Web UI served from same Go orchestrator binary on port 8080 for easier management and message injection
- **Agent Lifecycle**: "Empty list" after shutdown means no active agents (state files remain)

### Frontend Decisions:
- **B-1 Tailwind CSS**: Build-time generation (`npm run build-css`), no real-time compilation
- **B-3 Time-in-State**: Calculate from transition history (new `Transitions` field)
- **C-1 Dev Mode**: Use same config defaults as orchestrator (existing config file)

### Implementation Status:
- **Epic A - Backend API**: 8/9 complete (missing A-6)
  - ✅ A-1: AgentState schema extended
  - ✅ A-2: GET /api/agents  
  - ✅ A-3: GET /api/agent/:id
  - ✅ A-4: GET /api/queues with DumpHeads
  - ✅ A-5: POST /api/upload with file validation
  - 🔄 A-6: POST /api/answer *(pending escalation integration)*
  - ✅ A-7: POST /api/shutdown
  - ✅ A-8: GET /api/logs with filtering
  - ✅ A-9: GET /api/healthz

- **Epic C - Dev Experience**: 1/2 complete
  - ✅ C-1: Dev-mode runner with `make ui-dev` target

---

Happy building! 🎉
