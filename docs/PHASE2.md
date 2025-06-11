# Phase 2 Development Stories — Agent Flesh‑Out & Stand‑Alone Testing

These stories build on the completed MVP (stories 001‑012).  The goal is to replace mock agents with real LLM integrations **and** enable each agent to be executed and tested outside the full orchestrator.

Front‑matter fields conform to the existing schema.

## Table of Contents

| ID  | Title                                         | Est. | Depends |
| --- | --------------------------------------------- | ---- | ------- |
| 013 | Real Architect Agent (o3) integration         | 3    | 007     |
| 014 | Real Claude Coding Agent integration ✅       | 3    | 007     |
| 015 | Agent CLI runner & standalone harness ✅      | 2    | 013,014 |
| 016 | Agent unit‑test helpers & mock LLM clients ✅ | 2    | 013,014 |
| 017 | Story replayer for offline regression testing | 2    | 015,016 |
| 018 | Per‑agent metrics & Prometheus exporter       | 2    | 015     |
| 019 | Config refactor for per‑agent credentials     | 1    | 015     |

---

## Stories

### Story 013 — Real Architect Agent (o3) integration

```markdown
---
id: 013
title: "Real Architect Agent (o3) integration"
depends_on: [007]
blocks: [015,016]
est_points: 3
---
**Task**  
Replace the mock architect with a real OpenAI o3 client:
1. Implement `agents/architect_o3/client.go` wrapping ChatCompletion with a streaming option.
2. Convert project spec + story file into a single system/user prompt following OpenAI best practices.
3. Map LLM output → `AgentMsg` (TASK or QUESTION).
4. Include retry/back‑off on `5xx` or rate errors.

**Acceptance Criteria**
* Integration test hits OpenAI sandbox or mock server and receives a well‑formed TASK.
* Rate limiter prevents exceeding `max_tokens_per_minute`.
* Architect CLI (see Story 015) can emit a TASK JSON to stdout.
```

### Story 014 — Real Claude Coding Agent integration

```markdown
---
id: 014
title: "Real Claude Coding Agent integration"
depends_on: [007]
blocks: [015,016]
est_points: 3
---
**Task**  
Replace the Claude stub with live API calls:
1. Add `agents/claude/client.go` wrapping Anthropic streaming API.
2. Implement prompt template that ingests TASK payload + STYLE.md.
3. After generation, run `make test lint` in a temp workspace; collect output.
4. Return `RESULT`, `ERROR`, or `QUESTION` based on test outcome.

**Acceptance Criteria**
* Unit test with mock Anthropic server passes.
* Live smoke test (flag‑gated) generates compilable Go code for the `/health` story.
* Fails gracefully if test/lint fail, returning an ERROR with logs in Payload.
```

### Story 015 — Agent CLI runner & standalone harness✅ COMPLETED

```markdown
---
id: 015
title: "Agent CLI runner & standalone harness"
depends_on: [013,014]
blocks: [017,018,019]
est_points: 2
---
**Task**  
Create `cmd/agentctl` binary that can:
* Run an individual agent (`architect` or `claude`) in isolation.
* Accept `--input <file.json>` containing an `AgentMsg` TASK.
* Output the resulting `AgentMsg` to stdout or file.
* Support `--live` (real API) vs `--mock` (fake client) modes.

**Acceptance Criteria**
* `agentctl run architect --input story.md --mock` prints TASK JSON.
* `agentctl run claude   --input task.json --mock` prints RESULT JSON after fake tests.
* Documentation added to README.

**Implementation Notes**
* Created `cmd/agentctl/main.go` with full CLI interface
* Supports both architect and claude agents in standalone mode
* Includes task capture dispatcher for architect TASK output
* Fixed logging to stderr for clean JSON stdout output
* Added comprehensive README documentation with examples
* Integrated with Makefile (`make agentctl` target)
* Handles live API mode with proper environment variable validation
```

### Story 016 — Agent unit‑test helpers & mock LLM clients✅ COMPLETED

```markdown
---
id: 016
title: "Agent unit‑test helpers & mock LLM clients"
depends_on: [013,014]
blocks: [017]
est_points: 2
---
**Task**  
Add `pkg/testkit/` providing:
* `httptest` servers that emulate OpenAI & Anthropic responses.
* Helpers for crafting synthetic TASK and RESULT messages.
* Assertions for lint/test pass/fail conditions.

**Acceptance Criteria**
* Architect and Claude agent tests use mocks exclusively — no real API calls.
* Coverage >80 % for agents’ critical paths.
```

**Implementation Notes**
* Created `pkg/testkit/` with comprehensive testing utilities
* Mock HTTP servers for Anthropic and OpenAI APIs with realistic responses
* Message builders for creating synthetic AgentMsg instances with fluent API
* Predefined message factories (HealthEndpointTask, SuccessfulCodeResult, etc.)
* Rich assertion library with reflection-based test result validation
* Coverage: 81.6% achieved, exceeding requirement
* Updated all agent tests to use testkit exclusively (no real API calls)
* Comprehensive test suite for testkit itself with 100% pass rate
### Story 017 — Story replayer for offline regression testing

```markdown
---
id: 017
title: "Story replayer for offline regression testing"
depends_on: [015,016]
blocks: []
est_points: 2
---
**Task**  
Implement `cmd/replayer` that:
1. Reads historical `logs/events.jsonl`.
2. Reconstructs each TASK, feeds it to the target agent via `agentctl --mock`.
3. Compares new RESULT to previous RESULT and reports drift.

**Acceptance Criteria**
* Works on sample log with at least 3 TASK/RESULT pairs.
* Exit code non‑zero if outputs differ.
```

### Story 018 — Per‑agent metrics & Prometheus exporter

```markdown
---
id: 018
title: "Per‑agent metrics & Prometheus exporter"
depends_on: [015]
blocks: []
est_points: 2
---
**Task**  
Expose HTTP `/metrics` endpoint publishing:
* LLM latency histogram (per model).
* Tokens consumed counter (per model).
* Budget remaining gauge.

**Acceptance Criteria**
* `curl localhost:9090/metrics` shows Prometheus‑formatted metrics.
* Unit test scrapes and validates key metrics exist.
```

### Story 019 — Config refactor for per‑agent credentials

```markdown
---
id: 019
title: "Config refactor for per‑agent credentials"
depends_on: [015]
blocks: []
est_points: 1
---
**Task**  
Extend `config.Config` so each model entry can optionally include an array `credentials` keyed by agent‑id.  CLI flags `--agent-id` pick matching creds.

**Acceptance Criteria**
* Regression suite passes.
* Editing `config.json` with agent‑specific keys works in live smoke test.
```

---

> **Generated:** 2025‑06‑10

