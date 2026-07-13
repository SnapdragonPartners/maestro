+++
title = "ADR 0022: v2 Data Plane"
edit_date = "2026-07-13"
status = "draft"
summary = "Postgres/sqlc/golang-migrate as the v2 data plane, Docker-local by default; schema families derived from the taxonomy and artifact model; multi-user boundaries; all access through the Orchestrator's persistence seam."
+++

# 0022. v2 Data Plane

Status: Proposed

## Context

v1's SQLite database began as a searchable log file and grew into session persistence. v2's data plane is canonical: artifacts and their relationships, principal instances, metrics, and work-hierarchy records are the system's memory (ADR 0021 — memory lives in the data plane, not in agent context). Multi-user operation, concurrent Work Groups, retention-pinned evidence, and MPH-signature queries all demand a substrate stronger than a session log. The roadmap decided the stack (pillar 4, D1); this ADR fixes the shape and its invariants.

## Decision

### Stack

- **Postgres by default.** Docker-hosted local Postgres is the community default — Maestro already requires Docker, so this adds no new dependency class. Cloud-hosted Postgres serves team/cloud mode. Non-Docker local Postgres may be supported later but is never the default path.
- **`sqlc`** for typed, compile-checked queries; **`golang-migrate`** for versioned schema migrations, applied from empty. There is no migration from v1's SQLite — v1 data is frozen with `v1-freeze` (roadmap D7); the v2 story is migration from nothing.

### Schema families

Derived mechanically from ADRs 0018 and 0021; enumerated here so Phase 2 lands them in one coherent slice:

- Organizations and users (single-user mode uses a default organization and user, mirroring the default Product).
- Products and repositories (one repository belongs to exactly one Product; default Product for unassigned repos — ADR 0018).
- Features, Epics, Stories (non-null lineage at every level; wrapper Features).
- Work Groups and runs.
- Principal instances — agent, human, and system kinds (ADR 0021).
- Artifacts: **Management and Audit in separate storage families** with opposite retention postures (ADR 0021); amendment and supersession links; review records; retention pins.
- LLM calls and tool calls (Audit family).
- Metrics.
- Prompt packs (immutable, hash-addressed — roadmap pillar 10).
- Gates.
- Knowledge items (Phase 6 fills this out; the family is reserved).
- Skills/patterns (same).
- Binary attachments: content-addressed references into data-plane/object storage; binaries never live in relational rows.
- Audit events.

### Access discipline

All database access flows through the Orchestrator's persistence seam (ADR 0019 — persistence is Orchestrator machinery). Agents never hold connections or issue queries: they receive artifacts as seeds and effective views, and they emit through tools that write via the seam. This preserves v1's proven isolation posture (agents produce data; the persistence layer manages storage) while replacing fire-and-forget writes with the artifact contracts of ADR 0021.

### Multi-user boundaries (MVP)

Per roadmap pillar 5: users belong to organizations; major records carry organization and user lineage; fine-grained roles and security groups are post-MVP non-goals; individual credentials (forge tokens, LLM keys) do most early enforcement. The schema carries the lineage now so team mode is a deployment change, not a migration.

### Repo vs database

Roadmap D5 is ratified by reference: source code, project docs/ADRs, and true project artifacts live in the repo; users/orgs, work hierarchy, runs, artifacts, calls, metrics, attachments, indexed knowledge, installed packs/skills, and audit events live in the data plane. Config and credential placement is deliberately deferred to the disposable-project-folder spike (Phase 0 item 9): the schema must be capable of holding them; whether it does is the spike's recommendation.

### Phase 2 scope

Phase 2 implements exactly this ADR: one-command local setup from a clean checkout (Docker Postgres + migrations from empty), typed queries with tests for the artifact, principal-instance, and call families, and one real vertical slice — importing golden story runner results as `benchmark`-scoped artifacts.

## Consequences

- SQLite is fully retired from the v2 design; historical note 0005 is superseded for v2 intent (ADR 0021 began this; the data-plane decision completes it).
- Docker becomes load-bearing for all modes, not just agent execution — acceptable because it already was required.
- The Phase 1 benchmark runner keeps its own self-contained store (runner design constraints) and imports later; Phase 1 has no dependency on this ADR's implementation.
- Phase 2's DDL is mechanical: every family above traces to an Accepted ADR, so schema review is conformance checking, not design.
- Truncating Audit families (ADR 0021's retention posture) is an operational feature, planned for, not an incident.

## Related Documents

- [ADR 0018](0018-v2-work-taxonomy.md) (hierarchy, lineage, default Product), [ADR 0021](0021-artifacts-and-principal-instances.md) (artifact model, storage families, principals), [ADR 0019](0019-orchestrator-boundary.md) (persistence seam).
- [Roadmap](../v2/roadmap.md) pillars 4–5, D5, D7; [ADR backlog](../v2/adr-backlog.md) Postgres Data Plane entry; Phase 2 exit criteria in the [Phase 0 plan](../v2/phase_0/scope-and-plan.md).
- Historical note [0005](0005-sqlite-session-persistence-and-resume.md) (superseded for v2 design intent).
