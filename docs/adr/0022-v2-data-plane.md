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

- The requirement is the principle: a **multi-user, network-friendly, relational database**. Postgres is the implementation. Docker-hosted local Postgres is the community default — Maestro already requires Docker, so this adds no new dependency class. Cloud-hosted Postgres serves team/cloud mode; AlloyDB is a possible later variant (notably for larger vector storage), and staying on the Postgres-compatible surface keeps the tooling valid either way. Non-Docker local Postgres may be supported later but is never the default path.
- **`sqlc`** for typed, compile-checked queries; **`golang-migrate`** for versioned schema migrations, applied from empty. There is no migration from v1's SQLite — v1 data is frozen with `v1-freeze` (roadmap D7); the v2 story is migration from nothing.
- **Object storage is a first-class data-plane component alongside the relational database.** The contract is the **S3-compatible API** — the de facto universal object surface (MinIO, SeaweedFS, Garage, GCS interop, and AWS all speak it) — fronted by the persistence interface's object module, so implementations swap by configuration. The initial Docker-local implementation is **MinIO**: a single container composed next to Postgres. Named fallback: SeaweedFS (Apache-2.0), should MinIO's community-edition stewardship worsen — the S3 contract makes that a config change, not a migration. Cloud mode plugs in GCS/S3 behind the same module. Uploads, screenshots, browser traces, and large evidence media live here, referenced from relational rows by content-addressed digest; retention pinning (ADR 0021) applies to objects exactly as to Audit rows.

### Schema families

Derived mechanically from ADRs 0018 and 0021; enumerated here so Phase 2 lands them in one coherent slice:

- Organizations and users (single-user mode uses a default organization and user, mirroring the default Product).
- Products and repositories. Membership is **many-to-many**: a Product contains one or more repositories, and a repository — a shared API, say — may belong to as many Products as makes sense. Each repository designates exactly one **primary Product**, which is what the degenerate path's wrapper-Feature inference uses (this amends ADR 0018's one-repo-one-Product MVP rule, which anticipated the revisit); the default Product remains the fallback for unassigned repos.
- Repositories are logical, forge-independent entities: a repo record may carry multiple forge bindings — a local forge in airplane mode, GitHub after sync — and the binding is an attribute of the record, never its identity.
- Features, Epics, Stories (non-null lineage at every level; wrapper Features).
- Work Groups and runs.
- Principal instances — agent, human, and system kinds (ADR 0021).
- Artifacts: **Management and Audit in separate storage families** with opposite retention postures (ADR 0021); amendment and supersession links; review records; retention pins.
- Tool calls — the atomic Audit **action** unit: an LLM call that produces no tool call does nothing. LLM call records exist for token/cost accounting (which is metrics) and optional trace debugging. This refines ADR 0021's Audit enumeration: all of these are Audit-family records, but the tool call is the unit of action.
- Metrics.
- Prompt packs (immutable, hash-addressed — roadmap pillar 10).
- Gates.
- Knowledge items (Phase 6 fills this out; the family is reserved).
- Skills/patterns (same).
- Binary attachments: content-addressed digest references into object storage (see Stack); binaries never live in relational rows.
- Audit events.

### Access discipline

All data-plane access flows through the Orchestrator's persistence seam (ADR 0019 — persistence is Orchestrator machinery). Agents never hold connections or issue queries: they receive artifacts as seeds and effective views, and they emit through tools that write via the seam. This preserves v1's proven isolation posture (agents produce data; the persistence layer manages storage) while replacing fire-and-forget writes with the artifact contracts of ADR 0021.

The seam is a generalized **persistence interface** — restructured from v1's persistence layer, keeping its generality — behind which authentication, relational data, and object persistence are pluggable modules selected per deployment mode. Local mode plugs in direct modules; cloud mode plugs in its own (below). The interface is the same in every mode.

### Multi-user boundaries (MVP)

Per roadmap pillar 5: users belong to organizations; major records carry organization and user lineage; fine-grained roles and security groups are post-MVP non-goals; individual credentials (forge tokens, LLM keys) do most early enforcement. The schema carries the lineage now so team mode never requires a data migration — but cloud mode is more than pointing at a different database: it adds a gatekeeping **auth mini-app** that authenticates users and maps them into the data plane (roadmap pillar 15). The persistence interface is where those modules swap; the schema does not change.

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
