+++
title = "Phase 1 Artifacts"
edit_date = "2026-07-16"
status = "live"
summary = "Index of Phase 1 working artifacts: the scope/plan for the golden story runner and measurement harness; later the v1 patch record, D9 policy record, and cost-instrumentation report."
+++

# Phase 1 Artifacts

Working artifacts of Phase 1 (golden stories and measurement harness), produced under the [build process](../process_build.md) and the [Phase 1 plan](plan_scope.md). The binding specification for the phase is [ADR 0025](../../adr/0025-golden-stories-and-benchmark-runner.md); these documents carry the work that executes it.

- [Maestro v2 Phase 1: Scope And Plan](plan_scope.md) — Approved Phase 1 scope and execution plan: build the golden story runner per ADR 0025 — 11 serial work items covering the runner module, fixtures, the v1-as-patched target, the single-agent baseline, D9 cost instrumentation, and the first 5-10 stories.
- [Design: Benchmark Runner Module Contracts (Item 1)](design_runner.md) — Design sketch for the runner-skeleton work item: module layout, the run-record and four-state metric contracts, story and MPH bundle schemas, the results store, the adapter interface with its engine/adapter division of labor, and build wiring.
- [Fixture Conventions For Golden Stories](process_fixtures.md) — The golden story fixture repositories (pinned variants of maestro-llms, maestro-cms, and the extracted chat app), their provenance, and the conventions that keep them honest: pinned immutable bases, solution-leakage truncation, no tags, run-branch cleanup, and the re-pin procedure.

Expected to land here as the phase executes: the fixture conventions doc (item 2), the v1 patch record (item 5), the D9 policy record and cost-instrumentation report (item 6), and the phase exit baseline report (item 10).
