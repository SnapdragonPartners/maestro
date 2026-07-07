# Architecture Decision Records

This directory contains proposed Architecture Decision Records (ADRs) for Maestro.

The repository has many design specs, implementation notes, and historical plans in
`docs/`. Those files are useful context, but they are not all equally current. These
ADRs are intended to become the concise decision log that future implementation work
and future agents can consult before reading older planning documents.

## Status

All ADRs in this initial batch are `Proposed`. They summarize the current code and
the apparent intended design as of 2026-07-06. They should be reviewed, edited, and
then either accepted, replaced, or deleted.

## Documentation Authority

Until an ADR is accepted, use this precedence when docs conflict:

1. Actual code and tests.
2. Canonical FSM docs in `pkg/pm/STATES.md`, `pkg/architect/STATES.md`, and
   `pkg/coder/STATES.md`.
3. Accepted ADRs in this directory.
4. Current implementation summaries such as `CLAUDE.md`, `README.md`, and focused
   docs like `docs/GIT.md`, `docs/TESTING_STRATEGY.md`, and
   `docs/MAESTRO_LLMS_MIGRATION.md`.
5. Older specs, plans, and TODO files under `docs/` and `docs/specs/`.

## Proposed ADRs

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

Each ADR should include:

- `Status`: Proposed, Accepted, Superseded, or Rejected.
- `Context`: Why this decision matters.
- `Decision`: The intended design contract.
- `Current Implementation`: Code paths that show the current state.
- `Consequences`: Trade-offs and follow-up obligations.
- `Related Documents`: Older docs that provide detail or history.

