+++
title = "Phase 2 Artifacts"
edit_date = "2026-07-26"
status = "live"
summary = "Index of Phase 2 working artifacts: the scope/plan for the data plane and artifact core; later the vertical-slice report."
+++

# Phase 2 Artifacts

Working artifacts of Phase 2 (data plane and artifact core), produced under the [build process](../process_build.md) and the [Phase 2 plan](plan_scope.md). The binding specification for the phase is [ADR 0022](../../adr/0022-v2-data-plane.md), with shapes from [ADR 0021](../../adr/0021-artifacts-and-principal-instances.md); these documents carry the work that executes it.

- [Maestro v2 Phase 2: Scope And Plan](plan_scope.md) — Approved Phase 2 scope and execution plan: implement ADR 0022's Docker-local Postgres and MinIO data plane, typed persistence, objects, backup, and a vertical slice importing golden records into the main Postgres database as benchmark-scoped artifacts through an append-only, idempotent path, while the runner keeps its self-contained store. Eleven serial items open with the Phase-2-blocking artifact-envelopes ADR.

- [Design: The Local Data-Plane Stack (Item 2)](design_local_stack.md) — Mini-plan for Phase 2 item 2: the four-root path resolver with MAESTRO_HOME collapse, the 0600 root-of-trust key file, and a Compose stack for Postgres and MinIO bind-mounted under the data root — isolated from v1's container labelling so a benchmark sweep cannot tear it down, digest-pinned, health-gated, and idempotent from a clean checkout.
- [Design: Core Schema And Migrations (Item 3)](design_schema_core.md) — Mini-plan for Phase 2 item 3: golang-migrate conventions and the core DDL applied from empty — ADR 0028's envelope as columns over a jsonb payload, Management and Audit in separate families, scope-conditional lineage enforced in SQL, app-generated UUIDv7 identifiers, text-plus-CHECK over Postgres enums, and the table-by-table trace to an Accepted ADR and a Phase 2 consumer.
- [Schema Table Inventory: ADR And Consumer](inventory_schema-tables.md) — Every table created by Phase 2 item 3, traced to the Accepted ADR that requires it and the Phase 2 item that consumes it — the checkable form of the reserved-by-name rule, plus the families deliberately not created and where they land instead.
- [Phase 2 Exit Record (In Progress)](notes_exit-record.md) — Running record of Phase 2: what each item delivered, exit-criteria status, decisions and what they cost, and the verification post-mortem behind CLAUDE.md's Verification Discipline. Accumulates as the phase runs; flips live at phase close.
- [Design: Artifact And Principal Queries (Item 4)](design_queries_artifacts.md) — Mini-plan for Phase 2 item 4: typed queries over the artifact and principal-instance families — named transitions with their preconditions in the UPDATE's WHERE clause rather than a preceding read, no generic status write, effective views assembled in Go against RFC 7386's own test vectors, and the MPH seeding set captured at instance creation.

Expected to land here as the phase executes: the migration and schema conventions record (item 3) and the vertical-slice report (item 9). The artifact-envelopes ADR (item 1) lands in `docs/adr/` as an Accepted decision, not here.
