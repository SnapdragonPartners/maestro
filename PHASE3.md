# Phase 3 Development Stories — Core Agent Workflow & State Machine

Building on Phase 2, these stories flesh out the Claude agent’s **core workflow**:

* **State machine driver** covering PLAN → TOOL → CODE → TEST → APPROVAL → DONE/ERROR
* **State-specific prompts** for each stage
* **MCP shell tool** integration as the critical execution primitive
* **QUESTION** and **FIX** transitions
* **Approval** interaction with the Architect agent

Front‑matter schema:

```
---
id: <numeric id>
title: "<short description>"
depends_on: [<ids>]  # refers to Phase 2 story IDs
blocks: [<ids>]      # optional
est_points: <int>
---
```

## Table of Contents (execution order)

| ID  | Title                                          | Est. | Depends |
| --- | ---------------------------------------------- | ---- | ------- |
| 030 | Core state machine driver scaffolding          | 3    | 021,020 |
| 031 | Prompt templates for each workflow state       | 2    | 030     |
| 032 | MCP `shell` tool integration & validation      | 2    | 020     |
| 033 | Implement `QUESTION` & `FIX` state transitions | 2    | 030     |
| 034 | Approval state: Architect review & transition  | 2    | 030     |

---

### Story 030 — Core state machine driver scaffolding

```markdown
---
id: 030
title: "Core state machine driver scaffolding"
depends_on: [021,020]
blocks: [031,033,034]
est_points: 3
---
**Task**  
Implement the agent’s main driver in `pkg/agent/driver.go`:
1. Define an enum `State` with values: `PLANNING`, `TOOL_INVOCATION`, `CODING`, `TESTING`, `AWAIT_APPROVAL`, `DONE`, `ERROR`.
2. Wire a loop that:
   - Loads current `State` and context from in-memory buffer (or `STATUS.md`).
   - Invokes the appropriate prompt template for that `State`.
   - Parses MCP output (tools or assistant replies).
   - Transitions to next `State`, persisting to `STATUS.md`.
3. Embed compaction check after each transition using `contextmgr.CompactIfNeeded()`.

**Acceptance Criteria**
* State transitions occur in the correct order for the `/health` story.
* `STATUS.md` reflects current state and partial results after each step.
```

### Story 031 — Prompt templates for each workflow state

```markdown
---
id: 031
title: "Prompt templates for each workflow state"
depends_on: [030]
blocks: []
est_points: 2
---
**Task**  
Create a `templates/` folder with Markdown templates for each `State`:
- `planning.tpl.md`
- `tool_invocation.tpl.md`
- `coding.tpl.md`
- `testing.tpl.md`
- `approval.tpl.md`

Each template must:
1. Instruct the model on its current objective.
2. Specify MCP channel usage (`<tool name="shell">`).
3. Define expected response format (JSON or MCP tags).

**Acceptance Criteria**
* Unit tests render each template with dummy data into a valid prompt.
```

### Story 032 — MCP `shell` tool integration & validation

```markdown
---
id: 032
title: "MCP shell tool integration & validation"
depends_on: [020]
blocks: [030]
est_points: 2
---
**Task**  
Enhance `pkg/tools/mcp.go`:
1. Ensure `shell` is registered as a `ToolChannel`.
2. Support args `{cmd:string, cwd:string}` and return `{stdout, stderr, exit_code}`.
3. In `agentctl --mock`, provide a canned shell response for testing.

**Acceptance Criteria**
* Live `agentctl run claude --mock` shows a `<tool name="shell">` call and correctly prints the tool’s mock output.
```

### Story 033 — Implement `QUESTION` & `FIX` state transitions

```markdown
---
id: 033
title: "Implement QUESTION & FIX state transitions"
depends_on: [030]
blocks: [031]
est_points: 2
---
**Task**  
In `driver.go`, augment the state machine:
1. If model outputs `<tool name="get_help">` or explicit request text, switch to `QUESTION`; send a `QUESTION` AgentMsg to Architect.
2. On receiving an Architect `RESULT` or code suggestion, return to `CODING` or `FIXING` state.
3. If tests fail in `TESTING`, transition to `FIXING` and re-enter `CODING` once fixes are applied.

**Acceptance Criteria**
* Simulated test failure triggers the `FIXING` path.
* Simulated help request emits a `QUESTION` AgentMsg.
```

### Story 034 — Approval state: Architect review & transition

```markdown
---
id: 034
title: "Approval state: Architect review & transition"
depends_on: [030]
blocks: []
est_points: 2
---
**Task**  
Extend the state machine with `AWAIT_APPROVAL`:
1. After `TESTING` passes, enter `AWAIT_APPROVAL` and send a `QUESTION` AgentMsg to Architect asking for review.
2. Await Architect’s `RESULT` of approval or change request.
3. On approval, transition to `DONE`; on change request, transition to `FIXING`.

**Acceptance Criteria**
* Mock Architect run approves a change and agent finishes in `DONE` state.
* Mock Architect requests changes and agent loops back to `CODING`.
```

---

> **Generated:** 2025‑06‑10

