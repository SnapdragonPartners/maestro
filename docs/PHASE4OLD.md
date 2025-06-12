# Phase 4 Development Stories — Architect Agent Core Workflow

These stories flesh out the **Architect Agent** from spec ingestion through story generation, queue management, technical Q\&A, and code review.  Git-based merge actions are deferred to Phase 5.

Front-matter schema (same as prior phases):

```markdown
---
id: <numeric id>
title: "<short description>"
depends_on: [<ids>]
est_points: <int>
---
```

## Table of Contents (execution order)

| ID  | Title                                        | Est. | Depends |
| --- | -------------------------------------------- | ---- | ------- |
| 040 | Spec parser & story skeleton generator       | 3    | 013,027 |
| 041 | Dependency resolver & queue manager          | 3    | 040,027 |
| 042 | Story dispatcher & assignment policy         | 2    | 041,007 |
| 043 | Technical question handler                   | 2    | 013,007 |
| 044 | Business escalation path (human-in-the-loop) | 2    | 043     |
| 045 | Code review evaluator & decision logic       | 3    | 013,006 |
| 046 | Architect CLI harness & queue persistence    | 2    | 041,027 |

---

### Story 040 — Spec parser & story skeleton generator

```markdown
---
id: 040
title: "Spec parser & story skeleton generator"
depends_on: [013,027]
est_points: 3
---
**Task**  
Implement `agents/architect_o3/spec2stories.go`:
1. Read `spec/project.md` and extract discrete requirements sections.
2. For each requirement, generate a story skeleton in Markdown with front-matter (`id`, `title`, `depends_on` empty, `est_points` placeholder).
3. Write these skeletons to `stories/NNN.md`, numbering sequentially after existing stories.

**Acceptance Criteria**
* CLI mode `agentctl architect generate --spec spec/project.md` creates one story file per requirement.
* Unit tests verify that given a simple spec, the correct number of skeleton files are generated.
```

### Story 041 — Dependency resolver & queue manager

```markdown
---
id: 041
title: "Dependency resolver & queue manager"
depends_on: [040,027]
est_points: 3
---
**Task**  
Enhance Architect in `pkg/architect/queue.go`:
1. Load all `stories/*.md`, parse front-matter for `depends_on`.
2. Build an in-memory DAG, detect cycles, and maintain a queue of `pending` stories whose dependencies are satisfied.
3. Persist queue state (statuses: `pending`, `in_progress`, `waiting_review`, `completed`) to the state store (`pkg/state`).
4. Provide APIs: `NextReadyStory()`, `MarkInProgress(id)`, `MarkCompleted(id)`, `MarkWaitingReview(id)`.

**Acceptance Criteria**
* For a set of stories with dependencies, `NextReadyStory()` returns only those with no unmet prerequisites.
* State store JSON reflects queue transitions and survives reload.
```

### Story 042 — Story dispatcher & assignment policy

```markdown
---
id: 042
title: "Story dispatcher & assignment policy"
depends_on: [041,007]
est_points: 2
---
**Task**  
In `pkg/architect/dispatch.go`, implement:
1. A scheduler that polls `NextReadyStory()` and, respecting `max_agents` from config, dispatches a `TASK` `AgentMsg` to Coding agents.
2. Marks stories `in_progress` and assigns an `agent_id` in queue status.
3. If no ready stories or all agents busy, idles until conditions change.

**Acceptance Criteria**
* Simulated run with 2 agents and 5 stories shows correct interleaving and respecting agent limits.
* Queue state transitions from `pending` to `in_progress` after dispatch.
```

### Story 043 — Technical question handler

```markdown
---
id: 043
title: "Technical question handler"
depends_on: [013,007]
est_points: 2
---
**Task**  
Support coding-agent queries in `pkg/architect/questions.go`:
1. Listen for incoming `AgentMsg` of type `QUESTION`.
2. Route the question payload to the Architect’s LLM (o3) with a prompt template for technical Q&A.
3. Return the Architect’s answer as a `RESULT` message to the requesting agent.

**Acceptance Criteria**
* Mock a `QUESTION` message; Architect returns a valid JSON answer via mock o3 client.
* Integration test shows question → answer cycle in logs.
```

### Story 044 — Business escalation path (human-in-the-loop)

```markdown
---
id: 044
title: "Business escalation path (human-in-the-loop)"
depends_on: [043]
est_points: 2
---
**Task**  
Implement detection of business-level queries in `pkg/architect/escalation.go`:
1. Define heuristics (e.g. front-matter `type: business`, or detect keywords) to classify a question as “business.”
2. On such a `QUESTION`, instead of auto-answering, write a record to `logs/escalations.jsonl` and emit a notification (e.g. console warning) for human review.
3. Provide CLI command `agentctl architect list-escalations` to view pending items.

**Acceptance Criteria**
* Given a simulated business question, the Architect does *not* auto-respond but logs an escalation.
* CLI listing shows the logged items.
```

### Story 045 — Code review evaluator & decision logic

```markdown
---
id: 045
title: "Code review evaluator & decision logic"
depends_on: [013,006]
est_points: 3
---
**Task**  
Implement `pkg/architect/review.go`:
1. On receiving a `RESULT` from a Coding agent (code diff or updated files), run automated checks:
   - Linting (`make lint`) via shell tool
   - Test suite (`make test`) via shell tool
   - Static analysis rules (STYLE.md) via custom parser
2. If all checks pass, record approval; else generate review comments and emit a `QUESTION` back to the agent with actionable feedback.
3. Update queue state to `completed` on success or `waiting_review` on feedback.

**Acceptance Criteria**
* Integration test simulates passing and failing code; correct queue state transitions and message emissions.
```

### Story 046 — Architect CLI harness & queue persistence

```markdown
---
id: 046
title: "Architect CLI harness & queue persistence"
depends_on: [041,027]
est_points: 2
---
**Task**  
Extend `cmd/agentctl`:
1. Add subcommand `architect run` to start the Architect loop (spec→stories→dispatch→review).
2. Persist queue state on each transition to a file under `state/architect_queue.json`.
3. Support `--mock` to use stubbed o3 and shell tools.

**Acceptance Criteria**
* `agentctl architect run --mock` processes stories end-to-end via mocks.
* Queue state file updates reflect transitions and survives restart.
```

---

> **Generated:** 2025-06-11

