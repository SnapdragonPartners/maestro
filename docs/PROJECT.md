# Development Stories — MVP Orchestrator

This file defines an **ordered set of implementation stories** for building the MVP described in *Multi‑Agent AI Coding System — Updated Project Specification (MVP)*.  Each story is self‑contained, includes acceptance criteria, and can be executed by a **single Coding Agent** (Claude) under the supervision of the **Architect Agent** (o3).

Front‑matter fields

```
---
id: <numeric id>
title: "<short description>"
depends_on: [<ids>]
blocks: [<ids>]   # optional
est_points: <int> # planning estimate (1–3)
---
```

## Table of Contents

| ID  | Title                                   | Est. | Depends |
| --- | --------------------------------------- | ---- | ------- |
| 001 | Scaffold repository & Makefile          | 2    | –       |
| 002 | JSON config loader with env overrides   | 2    | 001     |
| 003 | Define `AgentMsg` struct & enums        | 1    | 001     |
| 004 | Token‑bucket & budget ledger (in‑mem)   | 2    | 002     |
| 005 | Structured logger w/ agent prefix       | 1    | 001     |
| 006 | Event log writer (`logs/events.jsonl`)  | 1    | 005     |
| 007 | Task Dispatcher (router & retry policy) | 3    | 003,004 |
| 008 | Architect agent stub (o3)               | 2    | 007     |
| 009 | Claude coding agent stub                | 2    | 007     |
| 010 | Graceful shutdown & STATUS.md dump      | 2    | 007     |
| 011 | End‑to‑end smoke test story processing  | 2    | 008,009 |
| 012 | Documentation updates & STYLE.md        | 1    | 011     |

---

## Stories

### Story 001 — Scaffold repository & Makefile

```markdown
---
id: 001
title: "Scaffold repository & Makefile"
depends_on: []
blocks: [002,003,004,005]
est_points: 2
---
**Task**  
Create the base folder structure exactly as listed in the spec; add a root `Makefile` with `build`, `test`, `lint`, and `run` targets.  Include a minimal Go module (`go mod init orchestrator`) and an empty `main.go` that prints "orchestrator boot".

**Acceptance Criteria**
* Directory tree matches spec (agents/, config/, docs/, etc.)
* `make build` succeeds on a blank machine with Go 1.24.3+
* `make lint` runs `staticcheck and go fmt` (no issues on fresh repo)
* `make run` prints the banner without error
```

### Story 002 — JSON config loader with env overrides

```markdown
---
id: 002
title: "JSON config loader with env overrides"
depends_on: [001]
blocks: [004]
est_points: 2
---
**Task**  
Implement `pkg/config/loader.go` that:
1. Reads `config/config.json` (path supplied via flag or env `CONFIG_PATH`).
2. Replaces any "${ENV_VAR}" placeholders with the current environment variable’s value.
3. Allows *any* key in the JSON to be overridden by an exact‑name env variable (`CLAUDE_API_KEY`, etc.).
4. Provides a typed struct (`type Config struct { Models map[string]ModelCfg ... }`).

*Note: ModelCfg structure can be expanded as needed based on rate limiting and budget requirements discovered in Story 004.*

**Acceptance Criteria**
* Unit tests cover: file read, placeholder substitution, env‑override precedence, validation errors.
* Loader consumed in `main.go` and printed when `make run`.
```

### Story 003 — Define `AgentMsg` struct & enums

```markdown
---
id: 003
title: "Define AgentMsg struct & enums"
depends_on: [001]
blocks: [007]
est_points: 1
---
**Task**  
Add `pkg/proto/message.go` defining `MsgType` enum and `AgentMsg` struct exactly as in the spec.  Include `ToJSON()` / `FromJSON()` helpers.

**Acceptance Criteria**
* Unit tests marshal/unmarshal JSON round‑trip.
```

### Story 004 — Token‑bucket & budget ledger (in‑mem)

```markdown
---
id: 004
title: "Token‑bucket & budget ledger (in‑mem)"
depends_on: [002]
blocks: [007]
est_points: 2
---
**Task**  
Create `pkg/limiter/limiter.go` implementing per‑model:
* Weighted semaphore for `max_tokens_per_minute` (refilled every 60 s).
* Budget ledger that tracks spend vs `max_budget_per_day_usd`.
* Method `Reserve(model, tokens) error` returning `ErrRateLimit` or `ErrBudgetExceeded`.
* Daily reset at local midnight (use `time.AfterFunc`).

**Acceptance Criteria**
* Unit tests cover token leakage, budget enforcement, reset.
```

### Story 005 — Structured logger w/ agent prefix

```markdown
---
id: 005
title: "Structured logger with agent prefix"
depends_on: [001]
blocks: [006]
est_points: 1
---
**Task**  
Implement `pkg/logx/logx.go` – thin wrapper over `log.Logger` that prefixes `[<ts>] [agent-id] <level>`.

**Acceptance Criteria**
* Logger usable from other packages; unit test verifies format.
```

### Story 006 — Event log writer (`logs/events.jsonl`)

```markdown
---
id: 006
title: "Event log writer (events.jsonl)"
depends_on: [005]
blocks: [007]
est_points: 1
---
**Task**  
Write `pkg/eventlog/writer.go` that appends newline‑delimited JSON `AgentMsg` records to `logs/events.jsonl`, rotating daily.

**Acceptance Criteria**
* File rotates; unit tests write & parse back.
```

### Story 007 — Task Dispatcher (router & retry policy)

```markdown
---
id: 007
title: "Task Dispatcher (router)"
depends_on: [003,004,006]
blocks: [008,009,010]
est_points: 3
---
**Task**  
Implement `pkg/dispatch/dispatcher.go` that:
* Accepts new `AgentMsg` of type `TASK` (from architect).
* Applies rate limiter before forwarding to target agent’s channel.
* Retries external LLM calls with exponential back‑off (max 3 attempts).
* Emits `RESULT`, `ERROR`, or `QUESTION` back to architect.

**Acceptance Criteria**
* Integration test with two fake agents completes without race.
```

### Story 008 — Architect agent stub (o3)

```markdown
---
id: 008
title: "Architect agent stub (o3)"
depends_on: [007]
blocks: [011]
est_points: 2
---
**Task**  
Stub agent that reads a story file, converts it into a `TASK`, and routes to coding agent.  For MVP, simulate o3 call with a mock returning canned code.

**Acceptance Criteria**
* Smoke test: architect sends TASK, receives RESULT, logs success.
```

### Story 009 — Claude coding agent stub

```markdown
---
id: 009
title: "Claude coding agent stub"
depends_on: [007]
blocks: [011]
est_points: 2
---
**Task**  
Stub agent that pretends to call Claude API; returns dummy implementation code and passes tests.

**Acceptance Criteria**
* Unit tests: given TASK payload, agent returns RESULT within rate limits.
```

### Story 010 — Graceful shutdown & STATUS.md dump

```markdown
---
id: 010
title: "Graceful shutdown & STATUS.md dump"
depends_on: [007]
blocks: [011]
est_points: 2
---
**Task**  
Implement orchestrator SIGINT handler that broadcasts `SHUTDOWN`, waits `graceful_shutdown_timeout_sec`, collects agent `STATUS.md` files.

**Acceptance Criteria**
* Integration test kills orchestrator; STATUS.md exists for each agent.
```

### Story 011 — End‑to‑end smoke test story processing

```markdown
---
id: 011
title: "E2E smoke test (health story)"
depends_on: [008,009,010]
blocks: [012]
est_points: 2
---
**Task**  
Use the sample `stories/001.md` (health endpoint) to run full pipeline: architect ➜ coding agent ➜ tests ➜ merge simulation.

**Acceptance Criteria**
* All logs show TASK → RESULT cycle.
* No budget or rate errors.
```

### Story 012 — Documentation updates & STYLE.md

```markdown
---
id: 012
title: "Final docs & STYLE.md"
depends_on: [011]
blocks: []
est_points: 1
---
**Task**  
Populate `docs/STYLE.md` (Go fmt rules, commit message style).  Update `PROJECT.md` with any drift discovered during implementation.

**Acceptance Criteria**
* `go test ./...` passes.
* All documentation is up to date with implemented features.

## Implementation Summary

**COMPLETED STORIES (001-012):**
All 12 MVP stories have been successfully implemented and tested. The system demonstrates:

- **Complete orchestration pipeline**: orchestrator → architect → claude → results
- **Rate limiting & budget tracking**: Per-model token buckets with daily budget enforcement  
- **Graceful shutdown**: SIGINT handling with STATUS.md generation for all agents
- **Event logging**: JSONL format with daily rotation and message persistence
- **End-to-end testing**: Smoke tests verify TASK → RESULT cycles without errors
- **Agent communication**: Structured message protocol with retry logic and error handling

**KEY FIXES DURING DEVELOPMENT:**
- Fixed duplicate message logging in dispatcher causing test failures
- Resolved agent slot management in rate limiter reservation/release cycle
- Corrected type assertions and function signature mismatches
- Implemented proper thread-safety with mutex protection

**CURRENT SYSTEM CAPABILITIES:**
The MVP orchestrator can successfully:
- Load configuration with environment variable overrides
- Route messages between architect and coding agents with rate limiting
- Process development stories and generate mock code implementations  
- Handle graceful shutdown with comprehensive status reporting
- Log all agent interactions for debugging and monitoring
- Run end-to-end tests demonstrating complete message flow

**ITEMS NEEDED FOR PRODUCTION WORK:**
See production backlog todo for detailed requirements to make this system production-ready.

**PHASE 3 STATE MACHINE (IMPLEMENTED):**
The system has been enhanced with a Phase 3 state machine driver that provides structured workflows for coding agents:

- **State Machine States**: PLANNING → CODING → TESTING → AWAIT_APPROVAL → DONE
- **Template System**: Prompt templates for each state in `pkg/templates/`
- **MCP Tool Integration**: Model Context Protocol tools for file operations (`pkg/tools/`)
- **Live LLM Integration**: Real Claude API integration with structured prompts
- **Workspace Management**: Proper file creation in agent workspaces with absolute paths
- **JSON Tool Arguments**: Support for structured tool calls with JSON parameters

The Phase 3 implementation enables:
- Real code generation with file creation in agent workspaces
- Structured workflow progression through defined states
- Template-driven prompts for consistent LLM interactions
- Tool-based file operations using MCP protocol
- Live API integration with fallback to mock mode for testing

Current testing shows successful end-to-end code generation with proper file creation in workspace directories.
```

---

> **Updated:** 2025‑06‑11

