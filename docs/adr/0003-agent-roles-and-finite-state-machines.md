+++
title = "ADR 0003: Agent Roles and Finite-State Machines"
edit_date = "2026-07-15"
status = "deprecated"
summary = "v1 agent roles (PM, Architect, Coder) and their finite state machines."
+++

# ADR 0003: Agent Roles and Finite-State Machines

- Status: Proposed
- Date: 2026-07-06

## Context

Some older docs describe Maestro as an Architect/Coder system. The current product
and code use three first-class agent roles: PM, Architect, and Coder. The hotfix
coder is a dedicated Coder instance with separate work routing.

The FSM docs under `pkg/*/STATES.md` are more current than many older documents in
`docs/`.

## Decision

Use explicit finite-state machines as the primary implementation model for agents:

- PM is the user-facing requirements agent. It interviews, previews specs, submits
  specs to the architect, remains available during development, and owns user asks.
- Architect is the coordinator and reviewer. It reviews specs, generates stories,
  dispatches work, answers coder questions, reviews plans and code, merges PRs,
  owns incidents, and tracks active stories.
- Coder agents are restartable workers. They plan, code, test, request review,
  prepare PRs, wait for merge, and then terminate/restart for new work.

Canonical state diagrams and transition semantics live in:

- `pkg/pm/STATES.md`
- `pkg/architect/STATES.md`
- `pkg/coder/STATES.md`

Any code change that alters a state, transition, terminal condition, restart rule,
SUSPEND behavior, durable ask/incident lifecycle, or merge workflow must update the
corresponding canonical FSM doc.

## Current Implementation

- `internal/factory/agent_factory.go` creates PM, Architect, and Coder agents from
  shared kernel dependencies.
- `cmd/maestro/flows.go` registers `architect-001`, optional `pm-001`, configured
  coders, and `hotfix-001`.
- `internal/supervisor/supervisor.go` restarts coders after DONE/ERROR, treats
  architect ERROR as fatal, restarts PM after errors, and handles SUSPEND.
- `pkg/agent/README.md` documents the single-goroutine FSM model. PM has a documented
  exception for direct WebUI method calls protected by mutex and detected by its run
  loop.

## Consequences

- New behavior should usually be modeled as state data plus explicit transitions,
  not hidden side effects.
- The supervisor owns lifecycle and restart policy; individual agents own their
  state-machine semantics.
- PM, Architect, and Coder have different concurrency rules. Do not blindly apply
  the PM direct-method pattern to other agents.
- Docs that only mention Architect and Coder should be treated as historical unless
  they have been updated for the PM flow.

## Related Documents

- `pkg/pm/STATES.md`
- `pkg/architect/STATES.md`
- `pkg/coder/STATES.md`
- `pkg/agent/README.md`
- `docs/AGENT_LIFECYCLE.md`
- `docs/archive/PM.md`

