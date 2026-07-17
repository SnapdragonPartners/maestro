+++
title = "Patch Record: v1-As-Patched"
edit_date = "2026-07-17"
status = "draft"
summary = "The enumerate-and-justify record of every patch to the main-branch v1 factory path made for the Phase 1 benchmark target: what changed, why the target strategy permits it, and what runs discovered it."
+++

# Patch Record: v1-As-Patched

Status: draft — Phase 1 item 5 (`target-v1-patch`), the reviewed record required by the [Phase 1 plan](plan_scope.md): every patch to the v1 factory path enumerated, each justified in a sentence against the target strategy (patches are instrument work, never v1 maintenance, never backported to `v1-freeze`). Growth in this file is visible, not silent.

| # | Patch | Kind | Justification | Discovered by |
|---|---|---|---|---|
| P-1 | **Durable per-LLM-call usage surface**: `Recorder.ObserveRequest` gains agent/model (intentional interface change); `UsageLogRecorder` fans out every observation to the `InternalRecorder` singleton (story aggregates untouched — `handleWorkAccepted` still reads them) and appends one JSONL line per call to `.maestro/usage.jsonl` with a versioned header (`usage_surface_version: 1`); `maestro -version` advertises `usage-surface: v1` (the pre-run capability handshake); log-open failure degrades to in-memory metrics with a warning. | Instrumentation (measurement-enabling) | ADR 0025 requires failed-attempt costs to count and overruns to abort; v1 persisted usage only at story acceptance, making honest streamed enforcement impossible without this surface. Pre-enumerated and approved with item 4's design (plan amendment 2026-07-16). | Item 4 design review (Codex): story aggregates are written only in `handleWorkAccepted`. |

Adapter-side consequences (benchmark module, not v1 patches): the v1 adapter now declares `streamed` enforcement, tails the usage log (deltas via `ReportUsage`, engine-cancelled at caps), takes the log as the canonical tokens/cost/`llm_calls` source, validates the surface version on both handshake halves, and exports `usage.jsonl` as evidence.

Run-blocking fix candidates known from v1's dying-defect list (patched only if the runs below actually hit them): the architect spec-review strictness defect (terse story prompts rejected), the watchdog requeue race (#221).

## Run Log

The discovery loop: each real attempt of story 1 (`dep-bump-xnet` × `paired-default`), its outcome, and what it taught. Recorded here as runs happen.
