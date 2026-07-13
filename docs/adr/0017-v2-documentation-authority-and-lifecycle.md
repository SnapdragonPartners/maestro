+++
title = "ADR 0017: v2 Documentation Authority And Lifecycle"
edit_date = "2026-07-12"
status = "draft"
+++

# 0017. v2 Documentation Authority And Lifecycle

Status: Proposed

## Context

Maestro v2 is a breaking redesign built by an agent fleet under one human operator ([build-process](../v2/build-process.md)). Agents consume repo documentation as ground truth, so documentation authority must be deterministic: which document wins when two disagree, and which documents carry no authority at all.

The repo currently holds three generations of documentation: ADRs 0001–0016 (proposed current-state notes about v1, never accepted as binding), the v2 planning set under `docs/v2/`, and roughly 130 v1-era specs, plans, and TODO files under `docs/` and `docs/specs/` of widely varying staleness. The v1 codebase is deprecated (`v1-freeze`) but remains the running implementation until v2 replaces it, so "what does the code do" and "what is the v2 design" have different authoritative sources during the transition.

## Decision

### ADR numbering and status

- One ADR sequence. v2 decisions continue at 0017. ADRs 0001–0016 are historical v1 current-state notes: useful context, never binding, and never to be Accepted. A v2 ADR that replaces one marks it Superseded explicitly.
- ADR lifecycle: `Proposed` → `Accepted` (requires both Codex and DR approval; the status flips as the final commit on the PR, and merge waits until that commit is visible) → `Superseded` or `Rejected`.
- v2 ADR template: Status, Context, Decision, Consequences, Related Documents, plus optional Implementation Notes. (The v1 template's mandatory "Current Implementation" section is dropped — v2 ADRs often precede implementation.)

### Front-matter

All markdown documents under `docs/` that are created or substantively edited from now on carry Hugo-style TOML front-matter with three fields: `title`, `edit_date`, and `status` ∈ {`draft`, `live`, `archive`}.

The doc status and the ADR status are two views of one state: Proposed ↔ `draft`, Accepted ↔ `live`, Superseded/Rejected ↔ `archive`. Phase artifacts follow the lifecycle defined in the [Phase 0 plan](../v2/phase_0/scope-and-plan.md): `draft` under review, `live` after both approvals, `archive` when the phase closes.

### Documentation authority

Authority depends on the question being asked.

For current runtime behavior (the code as it runs today):

1. Code and tests.
2. Canonical FSM docs: `pkg/pm/STATES.md`, `pkg/architect/STATES.md`, `pkg/coder/STATES.md`.
3. Implementation summaries: `CLAUDE.md`, `README.md`.

For v2 design intent:

1. Accepted ADRs (0017+).
2. `live` phase artifacts in `docs/v2/phase_x/`.
3. The roadmap and cross-phase documents in `docs/v2/`.
4. Historical ADRs 0001–0016.

Documents with `status = "archive"` carry no authority for any question; they are history. When a v2 ADR and current code disagree, both are right about different things: the code describes the present, the ADR describes the committed direction.

### Archive plan

Executed in Phase 0 work item 11 (`doc-reset`); this ADR fixes the rules and the keep list.

- Archived documents move to `docs/archive/`, preserving filenames, and receive `status = "archive"` front-matter. Git history preserves original paths; no redirects are maintained.
- Keep list (remain live at their current locations): `docs/adr/`, `docs/v2/`, `docs/wiki/` (human-facing set, pending the wiki/docs-site decision), and the operational docs referenced by `CLAUDE.md` and `README.md` — currently `GIT.md`, `TESTING_STRATEGY.md`, `MAESTRO_LLMS_MIGRATION.md`, `ARCHITECT_CONTEXT.md`, `MAESTRO_CHAT_SPEC.md`, `HOTFIX_MODE_SPEC.md`, `MODES.md`, `AIRPLANE_MODE.md`, `MAINTENANCE_MODE_SPEC.md`, `OLLAMA.md`, `DOC_GRAPH.md`, plus `BENCHMARK_HOWTO.md` and `BENCHMARKS.md` as Phase 1 seeds and `WELCOME_TO_MAESTRO.md`. Kept v1 docs get front-matter with `status = "live"` and remain authoritative only for current runtime behavior.
- Everything else under `docs/` root and all of `docs/specs/` archives: these are v1 design specs, plans, and TODOs whose decisions are either implemented (the code is now the authority) or abandoned.
- `docs/screenshots/` archives unless referenced by a kept document.
- Item 11 produces the exact file-by-file manifest as `docs/v2/phase_0/doc-reset-manifest.md`, reviewed like any other work item. Files whose disposition is unclear default to archive — recovery from git or `docs/archive/` is cheap; stale authority is not.

## Consequences

- Agents get a deterministic answer to "which document wins," at the cost of maintaining front-matter discipline going forward.
- ADRs 0001–0016 stay useful as v1 context without ever being mistaken for v2 decisions.
- The keep list makes `CLAUDE.md`/`README.md` references the de facto liveness test for v1 docs; both files should be updated in item 11 if their references change.
- Roughly 110 files move in item 11 — a large but mechanical diff, reviewed via the manifest rather than file-by-file.

## Related Documents

- [ADR 0001](0001-documentation-authority-and-adr-lifecycle.md) — the v1 authority order this ADR supersedes for v2 questions (0001 remains an accurate historical note about v1 practice).
- [Roadmap Phase 0](../v2/roadmap.md), [Phase 0 scope and plan](../v2/phase_0/scope-and-plan.md), [build-process](../v2/build-process.md).
