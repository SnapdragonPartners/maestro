# ADR 0006: Toolloop ProcessEffect and Terminal Tools

- Status: Proposed
- Date: 2026-07-06

## Context

Agents use iterative LLM tool-calling loops for planning, coding, testing,
reviewing, PM work, and architect work. Older documentation describes a few
toolloop eras: callback-based terminal checks, terminal tools with typed extraction,
and the current ProcessEffect pattern.

The current code uses terminal tools plus `ProcessEffect` to signal state-machine
transitions and carry structured data.

## Decision

Use `pkg/agent/toolloop` for unattended LLM tool-calling loops. Each toolloop has
one goal and exactly one configured terminal tool. General tools may be called many
times; the terminal tool is the intended exit path.

Terminal tools should return `tools.ProcessEffect` with:

- `Signal`: state-machine transition signal.
- `Data`: structured data for the caller.

Callers should switch on `toolloop.Outcome.Kind`, then handle `OutcomeProcessEffect`
by reading the signal and effect data. Soft/hard iteration limits, tool circuit
breaking, no-tool nudges, LLM error checkpoints, tool execution persistence, and
activity tracking belong in the toolloop layer rather than duplicated across agents.

## Current Implementation

- `pkg/agent/toolloop/toolloop.go` requires `Config[T].TerminalTool`, constructs
  the tool provider from general tools plus the terminal tool, and returns an
  `Outcome[T]`.
- `pkg/tools/mcp.go` defines `ProcessEffect` and signal constants.
- Many terminal tools return ProcessEffect, including planning, done, submit stories,
  submit reply, todo, bootstrap, blocked, verification, and probing tools.
- `toolloop.Config[T]` still carries a generic `T` for compatibility, but current
  terminal signaling is ProcessEffect-based rather than ExtractResult-based.
- `docs/TOOLLOOP_DESIGN.md` and `docs/TOOLLOOP_REFACTOR_PLAN.md` are useful history
  but contain details that lag the current ProcessEffect implementation.

## Consequences

- New terminal tools should return ProcessEffect directly.
- Avoid adding multiple terminal tools to one loop. If a workflow has multiple goals,
  use sequential toolloops or a terminal tool whose data explicitly encodes the
  decision.
- Remove or update older docs that still claim terminal tools extract results through
  an `ExtractResult` method before using them as implementation guidance.
- The remaining `TResult` generic is technical debt and should be removed only after
  a focused migration.

## Related Documents

- `CLAUDE.md`
- `docs/TOOLLOOP_DESIGN.md`
- `docs/TOOLLOOP_REFACTOR_PLAN.md`
- `docs/CODER_TOOLLOOP_MIGRATION.md`
- `docs/TOOL_LOOP.md`

