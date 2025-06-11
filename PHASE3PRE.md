# Phase 3 Foundation Stories — Core Infrastructure & Persistence

Before diving into the workflow stories, we need to establish the underlying MCP, context management, and state persistence infrastructure.  These foundation stories ensure that all later Phase 3 tasks (030–034) have the required primitives in place.

Front-matter schema:

```markdown
---
id: <numeric id>
title: "<short description>"
depends_on: [<ids>]  # maps to Phase 2 or earlier Phase 3 IDs
est_points: <int>
---
```

## Table of Contents

| ID  | Title                                         | Est. | Depends |
| --- | --------------------------------------------- | ---- | ------- |
| 025 | MCP infrastructure and pkg/tools scaffolding  | 2    | 014     |
| 026 | Basic Context Manager skeleton                | 2    | 025     |
| 027 | State persistence mechanism (beyond shutdown) | 2    | 025     |

---

### Story 025 — MCP infrastructure and pkg/tools scaffolding

```markdown
---
id: 025
title: "MCP infrastructure & pkg/tools scaffolding"
depends_on: [014]
est_points: 2
---
**Task**  
Establish the basic MCP tooling layer:
1. Create `pkg/tools/mcp.go` with the `ToolChannel` interface and a global registry (`Register`, `Get`).
2. Add a minimal MCP parser stub in `pkg/tools/mcp_parser.go` that can detect `<tool name="...">` tags and extract raw payload.
3. Define a `tools.ShellTool` struct implementing `ToolChannel.Exec(ctx, args)` (but leave Exec body empty or minimal for now).
4. Add unit tests that verify registration, retrieval, and parser stub behavior.

**Acceptance Criteria**
* `pkg/tools` exists with `mcp.go` and `mcp_parser.go`.
* Registry unit tests pass.
* Parser stub can identify tags and tool names without error.
```

### Story 026 — Basic Context Manager skeleton

```markdown
---
id: 026
title: "Basic Context Manager skeleton"
depends_on: [025]
est_points: 2
---
**Task**  
Provide a placeholder context manager:
1. Create `pkg/contextmgr/contextmgr.go` with struct `ContextManager` that holds a slice of messages and tracks token count (stub count as message length).
2. Add methods:
   - `AddMessage(role string, content string)` stores the pair.
   - `CountTokens() int` returns a simple sum of lengths.
   - `CompactIfNeeded(threshold int) error` currently no-op.
3. Unit tests verify that adding messages increases CountTokens and that CompactIfNeeded does not error.

**Acceptance Criteria**
* ContextManager type compiles and unit tests pass.
* Story 030 can import and call `CompactIfNeeded` without missing symbol errors.
```

### Story 027 — State persistence mechanism (beyond shutdown)

```markdown
---
id: 027
title: "State persistence mechanism"
depends_on: [025]
est_points: 2
---
**Task**  
Implement a durable state store for the agent’s current step:
1. Create `pkg/state/store.go` exposing `SaveState(agentID, state, data)` and `LoadState(agentID) (state, data, error)` which read/write a JSON file (e.g., `STATUS_<agentID>.json`).
2. Migrate shutdown-only code to use this store for tracking `state`, `lastTimestamp`, and `contextSnapshot`.
3. Unit tests cover Save/Load with a temp directory.

**Acceptance Criteria**
* `pkg/state/store.go` exists with SaveState/LoadState.
* Subsequent calls to LoadState return the same data saved.
* Phase 3 state machine (Story 030) can call LoadState without errors.
```

---

> **Generated:** 2025-06-10

