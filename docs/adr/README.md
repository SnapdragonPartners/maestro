+++
title = "Architecture Decision Records"
edit_date = "2026-07-13"
status = "live"
+++

# Architecture Decision Records

This directory contains Architecture Decision Records (ADRs) for Maestro, in a single
numbered sequence with two tiers:

- **0001–0016: historical v1 notes.** Proposed current-state summaries of the v1
  implementation (as of 2026-07-06). They were never accepted as binding and never
  will be; they remain useful context about how v1 works and carry `deprecated`
  status (stamped in the Phase 0 doc reset). v1 is deprecated as of 2026-07-11
  (tag `v1-freeze`).
- **0017+: v2 decisions.** These follow the lifecycle in
  [ADR 0017](0017-v2-documentation-authority-and-lifecycle.md): Proposed →
  Accepted (Codex + DR approval) → Superseded/Rejected. A v2 ADR that replaces a
  historical note marks it Superseded explicitly.

## Documentation Authority

Defined by [ADR 0017](0017-v2-documentation-authority-and-lifecycle.md). In short:
for current runtime behavior — code and tests, then the canonical FSM docs
(`pkg/*/STATES.md`), then `CLAUDE.md`/`README.md`, then `deprecated` v1 docs as
unverified hints. For v2 design intent — Accepted ADRs (0017+), then live phase
artifacts in `docs/v2/phase_x/`, then the roadmap and cross-phase docs in
`docs/v2/`, then the historical notes below. Archived documents carry no authority.

## v2 ADRs

| ADR | Title | Status |
| --- | --- | --- |
| [0017](0017-v2-documentation-authority-and-lifecycle.md) | v2 documentation authority and lifecycle | Proposed |

## Historical v1 Notes

| ADR | Title |
| --- | --- |
| [0001](0001-documentation-authority-and-adr-lifecycle.md) | Documentation authority and ADR lifecycle |
| [0002](0002-local-single-user-runtime-kernel.md) | Local single-user runtime kernel |
| [0003](0003-agent-roles-and-finite-state-machines.md) | Agent roles and finite-state machines |
| [0004](0004-channel-dispatch-and-typed-agent-protocol.md) | Channel dispatch and typed agent protocol |
| [0005](0005-sqlite-session-persistence-and-resume.md) | SQLite session persistence and resume |
| [0006](0006-toolloop-process-effect-and-terminal-tools.md) | Toolloop ProcessEffect and terminal tools |
| [0007](0007-llm-provider-boundary-through-maestro-llms.md) | LLM provider boundary through maestro-llms |
| [0008](0008-container-workspace-and-compose-isolation.md) | Container, workspace, and compose isolation |
| [0009](0009-clone-mirror-and-forge-pr-workflow.md) | Clone, mirror, and forge PR workflow |
| [0010](0010-pm-led-spec-bootstrap-hotfix-and-demo-lifecycle.md) | PM-led spec, bootstrap, hotfix, and demo lifecycle |
| [0011](0011-configuration-operating-modes-and-secrets.md) | Configuration, operating modes, and secrets |
| [0012](0012-knowledge-graph-as-repository-artifact.md) | Knowledge graph as repository artifact |
| [0013](0013-testing-strategy-and-service-boundaries.md) | Testing strategy and service boundaries |
| [0014](0014-failure-taxonomy-durable-asks-and-incidents.md) | Failure taxonomy, durable asks, and incidents |
| [0015](0015-agent-chat-and-human-in-the-loop-escalation.md) | Agent chat and human-in-the-loop escalation |
| [0016](0016-architect-per-agent-conversation-context.md) | Architect per-agent conversation context |

## ADR Format

v2 ADRs (0017+) carry TOML front-matter (`title`, `edit_date`, `status`) and include:

- `Status`: Proposed, Accepted, Superseded, or Rejected.
- `Context`: Why this decision matters.
- `Decision`: The design contract.
- `Consequences`: Trade-offs and follow-up obligations.
- `Related Documents`: Sources, superseded notes, and history.
- `Implementation Notes` (optional): code paths, when implementation exists.

The historical notes (0001–0016) used a mandatory `Current Implementation` section
instead, appropriate to their current-state purpose.

