+++
title = "Phase 2 Artifacts"
edit_date = "2026-07-25"
status = "live"
summary = "Index of Phase 2 working artifacts: the scope/plan for the data plane and artifact core; later the migration conventions record and the vertical-slice report."
+++

# Phase 2 Artifacts

Working artifacts of Phase 2 (data plane and artifact core), produced under the [build process](../process_build.md) and the [Phase 2 plan](plan_scope.md). The binding specification for the phase is [ADR 0022](../../adr/0022-v2-data-plane.md), with shapes from [ADR 0021](../../adr/0021-artifacts-and-principal-instances.md); these documents carry the work that executes it.

- [Maestro v2 Phase 2: Scope And Plan](plan_scope.md) — Approved Phase 2 scope and execution plan: implement ADR 0022's Docker-local Postgres and MinIO data plane, typed persistence, objects, backup, and a vertical slice importing golden records into the main Postgres database as benchmark-scoped artifacts through an append-only, idempotent path, while the runner keeps its self-contained store. Eleven serial items open with the Phase-2-blocking artifact-envelopes ADR.

- [Design: The Local Data-Plane Stack (Item 2)](design_local_stack.md) — Mini-plan for Phase 2 item 2: the four-root path resolver with MAESTRO_HOME collapse, the 0600 root-of-trust key file, and a Compose stack for Postgres and MinIO bind-mounted under the data root — isolated from v1's container labelling so a benchmark sweep cannot tear it down, digest-pinned, health-gated, and idempotent from a clean checkout.

Expected to land here as the phase executes: the migration and schema conventions record (item 3) and the vertical-slice report (item 9). The artifact-envelopes ADR (item 1) lands in `docs/adr/` as an Accepted decision, not here.
