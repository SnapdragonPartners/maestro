+++
title = "Phase 2 Artifacts"
edit_date = "2026-07-24"
status = "live"
summary = "Index of Phase 2 working artifacts: the scope/plan for the data plane and artifact core; later the migration conventions record and the vertical-slice report."
+++

# Phase 2 Artifacts

Working artifacts of Phase 2 (data plane and artifact core), produced under the [build process](../process_build.md) and the [Phase 2 plan](plan_scope.md). The binding specification for the phase is [ADR 0022](../../adr/0022-v2-data-plane.md), with shapes from [ADR 0021](../../adr/0021-artifacts-and-principal-instances.md); these documents carry the work that executes it.

- [Maestro v2 Phase 2: Scope And Plan](plan_scope.md) — Proposed Phase 2 scope and execution plan: implement ADR 0022's data plane — Docker-local Postgres and MinIO under the durable data root, golang-migrate from empty, sqlc typed queries for the artifact, principal-instance, call, configuration, and secrets families, the object module with the cross-store commit-order invariant, the cold-backup operation, and one vertical slice importing golden runner results as benchmark-scoped artifacts. Eleven serial work items, opening with the Phase-2-blocking artifact-envelopes ADR.

Expected to land here as the phase executes: the migration and schema conventions record (item 3) and the vertical-slice report (item 9). The artifact-envelopes ADR (item 1) lands in `docs/adr/` as an Accepted decision, not here.
