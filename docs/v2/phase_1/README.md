+++
title = "Phase 1 Artifacts"
edit_date = "2026-07-22"
status = "live"
summary = "Index of Phase 1 working artifacts: the scope/plan for the golden story runner and measurement harness; later the v1 patch record, D9 policy record, and cost-instrumentation report."
+++

# Phase 1 Artifacts

Working artifacts of Phase 1 (golden stories and measurement harness), produced under the [build process](../process_build.md) and the [Phase 1 plan](plan_scope.md). The binding specification for the phase is [ADR 0025](../../adr/0025-golden-stories-and-benchmark-runner.md); these documents carry the work that executes it.

- [Maestro v2 Phase 1: Scope And Plan](plan_scope.md) — Approved Phase 1 scope and execution plan: build the golden story runner per ADR 0025 — 12 serial work items covering the runner module, fixtures, the v1-as-patched target, cost/latency reduction, D9 cost instrumentation, and the first 5-10 stories. Resequenced 2026-07-22 (proposed) to conformance-first: the single-agent economic baseline and comparison reporting retime to Phase 1B, and the exit criteria split accordingly.
- [Design: Benchmark Runner Module Contracts (Item 1)](design_runner.md) — Design sketch for the runner-skeleton work item: module layout, the run-record and four-state metric contracts, story and MPH bundle schemas, the results store, the adapter interface with its engine/adapter division of labor, and build wiring.
- [Fixture Conventions For Golden Stories](process_fixtures.md) — The golden story fixture repositories (pinned variants of maestro-llms, maestro-cms, and the extracted chat app), their provenance, and the conventions that keep them honest: pinned immutable bases, solution-leakage truncation, no tags, run-branch cleanup, and the re-pin procedure.
- [Design: Runner Engine And CLI (Item 3)](design_engine.md) — Design sketch for the runner-core work item: attempt lifecycle (cleanup before the append-only record), pre-run target description with error-path metric synthesis, immutable solution binding, streamed budget enforcement with conservative suite admission, the suite manifest, and the CLI surface.
- [Design: The v1-As-Patched Adapter (Item 4)](design_adapter_v1.md) — Design sketch for the adapter-v1 work item: per-run Gitea forge isolation with a complete Docker lifecycle, subprocess invocation and DB-poll lifecycle, the usage-surface patch seam that earns streamed enforcement, durable evidence export with consistent WAL snapshots, the audited prompt manifest, canonical model-routing identity, and immutable binary identity.
- [Patch Record: v1-As-Patched (Item 5)](patches_v1.md) — The enumerate-and-justify record of every patch to the main-branch v1 factory path made for the Phase 1 benchmark target: what changed, why the target strategy permits it, and what runs discovered it.
- [Design: Cost And Latency Reduction (Item 5.1)](design_cost_latency.md) — Mini-plan for the cost-latency work item: a registry-published, digest-pinned union cache image that kills the cold-cache tax (#268, deterministically verified with GOPROXY=off), then an Ollama-only paired-local configuration that makes basic end-to-end exercise of the harness near-free (#266) — gated on a viability probe, with local cost marked unavailable and local runs budgeted on tokens and wall-clock with zero USD reservation.
- [D9 Sampling And Budget Policy (Item 6)](d9_budget_policy.md) — The D9 policy record required by Phase 1 item 6: per-story costs measured on instrumented runs, N fixed at 3 for the primary configuration, and per-story and per-suite budget caps fixed as runner-enforced values with overrun-as-failure. Caps are a runaway safeguard sized from observed accepted runs, not a performance target; the samples behind them are thin and disclosed as such.

Expected to land here as the phase executes: the cost-instrumentation report (item 6) and the phase exit baseline report (item 10).
