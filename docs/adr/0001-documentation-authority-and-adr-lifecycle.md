+++
title = "ADR 0001: Documentation Authority and ADR Lifecycle"
edit_date = "2026-07-15"
status = "deprecated"
summary = "v1 documentation authority order and ADR lifecycle as practiced; superseded for v2 questions by ADR 0017."
+++

# ADR 0001: Documentation Authority and ADR Lifecycle

- Status: Proposed
- Date: 2026-07-06

## Context

Maestro grew before the project had ADRs. The `docs/` tree now contains a mix of
active specs, implementation summaries, old TODOs, research notes, migration plans,
and canonical state-machine files. Several older documents describe designs that
have since changed, such as the move from a two-agent system to a PM/Architect/Coder
system, the migration of provider I/O to `maestro-llms`, and the ProcessEffect-based
toolloop.

Without a documentation hierarchy, future work can accidentally follow an outdated
plan instead of the code's current architecture.

## Decision

Use `docs/adr/` as the durable design decision log for Maestro. ADRs record the
current intended architecture and link to deeper specs when useful. Historical docs
remain in place unless they are actively harmful, but accepted ADRs become the
short-form source of design intent.

Documentation authority should be:

1. Code and tests.
2. Canonical FSM docs in `pkg/pm/STATES.md`, `pkg/architect/STATES.md`, and
   `pkg/coder/STATES.md`.
3. Accepted ADRs.
4. Current implementation summaries and focused docs.
5. Older plans, TODOs, and implementation specs.

New design changes should add or update an ADR when they materially change agent
roles, protocol boundaries, persistence semantics, runtime isolation, provider
ownership, forge workflow, or configuration/secrets behavior.

## Current Implementation

This `docs/adr/` directory is introduced by this proposal batch. `CLAUDE.md` has
the richest current architecture summary. `AGENTS.md` was previously a separate
file containing Codex review guidelines; it is now a symbolic link to `CLAUDE.md`
(see `ls -l AGENTS.md`), so shared guidance lives in one place.

## Consequences

- Older docs do not need a wholesale cleanup before ADRs are useful.
- ADRs should stay concise and should cite implementation paths instead of
  duplicating every spec detail.
- When an ADR conflicts with a historical spec, update the spec or add a note only
  when the conflict is likely to mislead implementation work.
- Proposed ADRs should be reviewed before being treated as accepted architecture.

## Related Documents

- `README.md`
- `CLAUDE.md`
- `docs/DOC_GRAPH.md`
- `docs/wiki/OVERVIEW_WIKI.md`
- `pkg/pm/STATES.md`
- `pkg/architect/STATES.md`
- `pkg/coder/STATES.md`

