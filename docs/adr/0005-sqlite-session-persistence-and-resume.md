# ADR 0005: SQLite Session Persistence and Resume

- Status: Proposed
- Date: 2026-07-06

## Context

Maestro needs durable local state for sessions, messages, stories, agent state,
tool executions, failures, chat, knowledge indexes, and resume behavior. Older docs
sometimes treat logs as primary evidence; current code uses SQLite as the structured
source of truth for durable runtime data.

## Decision

Use one project-local SQLite database at `.maestro/maestro.db` as the canonical
structured persistence store for each project. Keep logs as human-readable debug
streams, not structured data inputs.

Session IDs isolate runtime data. New normal runs create a new session ID. Resume
mode reuses a shutdown session ID. Crash recovery resets in-flight stories to new
and restores only the safer state boundaries.

Active story orchestration remains architect-owned in memory during a run, while
the database records history, audit data, messages, chat, checkpoints, and resume
state.

## Current Implementation

- `pkg/persistence/schema.go` defines `CurrentSchemaVersion = 23`, opens SQLite
  with foreign keys, WAL, and busy timeout, and runs versioned migrations.
- `internal/kernel/kernel.go` initializes `.maestro/maestro.db`, starts a
  persistence worker, creates session records, and drains the queue on shutdown.
- `cmd/maestro/main.go` and `cmd/maestro/flows.go` implement resume mode, session
  selection, session status updates, crash recovery, state serialization, and
  graceful shutdown.
- `CLAUDE.md` documents that database is canonical for messages and audit data,
  while architect state is canonical for active stories.

## Consequences

- Runtime metadata belongs in SQLite rather than config files.
- Persistence writes should be session-scoped.
- Agents may write via the persistence queue during normal execution; explicit
  restore/serialization paths are the exception.
- Schema changes require migrations and tests.
- Logs can be rotated, summarized, or discarded without losing canonical structured
  history.

## Related Documents

- `CLAUDE.md`
- `docs/RESUME_MODE_SPEC.md`
- `docs/FAILURE_RECOVERY_V2_SPEC.md`
- `docs/FAILURE_TELEMETRY.md`
- `docs/DOC_GRAPH.md`

