+++
title = "Historical ADR Alignment With Maestro v2"
edit_date = "2026-07-15"
status = "live"
type = "notes"
summary = "Mapping of v1 subsystems to the historical ADR notes 0001-0016; an input to roadmap D8 and the port inventory."
+++

# Historical ADR Alignment With Maestro v2

Status: rough companion note

The `v2/roadmap` branch (formerly `codex/v2-roadmap`) includes the commits from
`codex/proposed-adrs` through `b1af82a` and has since moved on.

The proposed ADRs under `docs/adr/` are not accepted decisions and should not be treated
as binding architecture. They are best read as historical/current-state notes about how
Maestro works or recently worked.

They are useful source material for v2, like the external research corpus and client
experience notes, but v2 ADRs can freely preserve, supersede, contradict, or discard them.
This table records the first consistency pass.

## Summary

Most notes are directionally useful for v2 because Maestro already had many of the right
primitives: explicit roles, FSMs, typed messages, container isolation, PR workflow, human
escalation, failure taxonomy, and LLM provider boundaries.

The largest v2 breaks are:

- Local single-user runtime becomes local-first/team-capable runtime.
- SQLite session persistence becomes Postgres data plane.
- Instance-scoped agents become Epic-scoped teams.
- DOT repo knowledge becomes database-backed governed knowledge.
- Demo mode becomes or feeds UAT.
- Logs/message records become Audit artifacts; user-facing docs become Management artifacts.

## Alignment Table

| ADR | v2 Alignment | Notes |
|---|---|---|
| 0001 Documentation Authority and ADR Lifecycle | Keep and extend | Still aligned. v2 needs a documentation-reset ADR that archives stale docs and defines LLM-facing repo docs vs human-facing wiki/docs-site output. |
| 0002 Local Single-User Runtime Kernel | Supersede | v1-current. v2 keeps "local-first" and concrete runtime discipline, but the single-user kernel gives way to a data-plane-backed, task/team-capable runtime. |
| 0003 Agent Roles and Finite-State Machines | Keep principle, supersede roles | FSM discipline remains central. PM/Architect/Coder becomes Product/Feature/Epic/Story/Work Group taxonomy with orchestrator-owned intake/triage and Epic-scoped PM/Architect/Coder roles. |
| 0004 Channel Dispatch and Typed Agent Protocol | Keep and generalize | Strongly aligned. v2 should preserve structured messages and treat messages as Audit artifacts. Later queue/cloud execution can abstract the dispatcher seam. |
| 0005 SQLite Session Persistence and Resume | Supersede | Principles remain: structured persistence beats logs, resume needs durable state. Implementation shifts to Postgres/sqlc/migrate and artifact-first schema. |
| 0006 Toolloop ProcessEffect and Terminal Tools | Keep and revisit | ProcessEffect and terminal-tool discipline align with v2. Revisit ownership with `maestro-llms` toolloop and preserve model commentary/reasoning summaries as Audit data. |
| 0007 LLM Provider Boundary Through maestro-llms | Keep | Strongly aligned. v2 should push provider metrics and reusable provider behavior to `maestro-llms` where possible. |
| 0008 Container, Workspace, and Compose Isolation | Keep and extend | Strongly aligned. v2 changes workspace scope from agent/story to Epic/Story hierarchy and may later define a container runtime abstraction, with Docker as initial implementation. |
| 0009 Clone, Mirror, and Forge PR Workflow | Keep and extend | Forge boundary and local mirrors align. Branch strategy changes: Story branches merge into Epic branches; Epic branches merge to default after acceptance. |
| 0010 PM-Led Spec, Bootstrap, Hotfix, and Demo Lifecycle | Partially supersede | PM remains valuable inside Work Groups. Orchestrator-owned intake/triage adds the Feature level. Hotfix generalizes to the Workbench. Demo Mode becomes UAT foundation. |
| 0011 Configuration, Operating Modes, and Secrets | Partially supersede | System/user secret split remains useful. v2 likely moves more config and credentials to the data plane and makes project folders more disposable. |
| 0012 Knowledge Graph as Repository Artifact | Supersede | Good v1 stepping stone. v2 moves knowledge to data plane, adds hierarchy, citations, skills, docs, interfaces/contracts, and eventually AST/code facts. |
| 0013 Testing Strategy and Service Boundaries | Keep and extend | Still aligned. Golden stories and loop analysis add system-level benchmark/eval coverage above normal unit/integration tests. |
| 0014 Failure Taxonomy, Durable Asks, and Incidents | Keep and generalize | Aligned. v2 should generalize failure and durable human-answerable items across Feature, Epic, Story, Team, and artifact/gate lifecycles. |
| 0015 Agent Chat and Human-in-the-Loop Escalation | Keep and refine | Aligned. v2 should treat chat/message records as Audit artifacts and route human attention through Management artifacts, gates, and evidence packages. |
| 0016 Architect Per-Agent Conversation Context | Keep principle, generalize | Aligned. v2 extends the idea into context governance and knowledge packs across Work Groups, while preserving scoped context boundaries. |

## ADR Strategy For v2

Do not try to canonize or rewrite every historical ADR note immediately. Instead:

1. Treat the proposed ADRs as historical/current-state context.
2. Add v2 ADRs that explicitly make the new grounding decisions.
3. Use the v2 ADR backlog to prioritize decision records before implementation.
4. When a v2 ADR replaces a historical note, mark the older note as historical or superseded.

## Highest-Priority v2 ADRs

Note (2026-07-11): this list predates several roadmap passes (Workbench, v1 freeze, policy gating, benchmark policy) and is superseded by the Phase 0 backlog reconciliation output defined in the roadmap.

The first v2 ADRs should probably cover:

1. v2 taxonomy: Product, Feature, Epic, Story, Work Group.
2. Documentation reset and LLM-facing vs human-facing docs.
3. Management vs Audit artifacts and minimal artifact signature.
4. Postgres data plane and local Docker Postgres default.
5. Golden stories and benchmark runner.
6. Reviewer vs Partner/Supervisor.
7. Branch hierarchy.
