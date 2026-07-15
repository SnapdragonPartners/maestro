+++
title = "Maestro v2 ADR Backlog"
edit_date = "2026-07-15"
status = "live"
type = "notes"
summary = "Reconciled, dependency-ordered ADR backlog (Phase 0 item 12): candidates resolved in Phase 0 with their Accepted ADRs, and open candidates ordered by the phase they block."
+++

# Maestro v2 ADR Backlog

Status: live — reconciled 2026-07-15 (Phase 0 item 12); supersedes the interim priority list in [notes_v1-adr-alignment.md](notes_v1-adr-alignment.md). New ADR needs discovered mid-phase land here, not in the phase.

## Resolved In Phase 0

| Candidate | Resolution |
| --- | --- |
| v2 Documentation Authority And Planning Reset | [ADR 0017](../adr/0017-v2-documentation-authority-and-lifecycle.md) (amended 2026-07-15); archive plan executed by the [doc-reset manifest](phase_0/manifest_doc-reset.md) |
| v2 Taxonomy: Product, Feature, Epic, Story, Work Group | [ADR 0018](../adr/0018-v2-work-taxonomy.md) (repo-Product rule amended by 0022) |
| Orchestrator Boundary | [ADR 0019](../adr/0019-orchestrator-boundary.md) (amended 2026-07-14: dispatch at both grains); the in-flight-work policy is carried below as an open candidate |
| Intake And Triage — stage 1 (artifact contract) | [ADR 0024](../adr/0024-intake-and-triage-artifact-contract.md) (amended 2026-07-14); stage 2 is carried below |
| Reviewer vs Partner/Supervisor | [ADR 0020](../adr/0020-review-invariant-reviewer-vs-partner.md) (amended: agentic review, unconditional human Accept) |
| Management And Audit Artifacts | [ADR 0021](../adr/0021-artifacts-and-principal-instances.md) |
| Agent Instance And Lightweight Signatures | [ADR 0021](../adr/0021-artifacts-and-principal-instances.md) (principal instances + MPH signature; no cryptographic signing, as recommended) |
| Golden Stories And Benchmark Runner | [ADR 0025](../adr/0025-golden-stories-and-benchmark-runner.md) |
| v1 Freeze And Port-Vs-Rewrite Inventory | Freeze: roadmap D7 and the `v1-freeze` tag. Inventory and breaking-change principles: [inventory_v1-port.md](phase_0/inventory_v1-port.md) (live) — recorded as a phase artifact, not an ADR, by agreement |
| Postgres Data Plane | [ADR 0022](../adr/0022-v2-data-plane.md) (amended: local durability invariant, config/secrets, backup contract) |
| Branch Strategy | [ADR 0023](../adr/0023-v2-branch-strategy.md) |
| Binary Attachment Storage | [ADR 0022](../adr/0022-v2-data-plane.md) — object storage first-class, content-addressed digests, binaries never in relational rows |
| User Credentials And Configs | [Project-folder spike](phase_0/spike_project-folder.md) + ADR 0022 amendment (2026-07-14): config records and secrets vault in the plane, key-file root of trust outside it |

## Open Candidates, Dependency-Ordered

Ordered by the phase each blocks. An entry should be Accepted before its blocking phase starts implementation.

### 1. Artifact Envelopes And Payload Schemas — blocks Phase 2

Phase 2's DDL and typed queries need the canonical artifact encoding fixed first. ADR 0021 fixed the model; the remaining decisions are the encoding layer:

- The envelope: JSON as storage/API canonical format; schema/version in every artifact; Markdown as rendering format; TOML/YAML allowed for prompt-facing fragments.
- Payload type registry and validation.
- Version evolution rules for payload schemas.
- Amendment and effective-view encoding (how a flat amendment chain renders as one view — 0021's semantics, made concrete).
- Review linkage encoding (how a review record binds to its artifact and revision).

### 2. Online Backup And Restore — trails Phase 2 (non-blocking)

The cold-backup baseline shipped in ADR 0022 as amended; this candidate is the online upgrade: snapshot/`pg_basebackup`-class backup, restore validation, cross-store consistency across Postgres, object store, and local forge.

### 3. Amendment Vs Running Work — blocks Phase 3

Deferred from ADR 0019's dispatch amendment (2026-07-14): the policy for work already executing when its Epic/Story/DAG record is amended or superseded — cancel, suspend, or complete-then-reconcile. The Work Group runtime cannot ship without it.

### 4. Tool Execution Policy Hook — blocks Phase 3

A narrow, binding ADR: where the per-action policy hook lives (toolloop, dispatcher, tool execution layer, or a policy service) and its interface — no policy content. Chosen before Phase 3 builds tool plumbing, or per-action policy gets retrofitted into every tool. The full gating-policy ADR stays post-MVP (below).

### 5. Prompt Pack Identity, Resolution, And Storage — blocks Phase 3

Split from the broader packs/skills candidate (2026-07-15): the port inventory moves templates and packs into the data plane during Phase 3, and the MPH signature's P component needs pack identity from Phase 1's runner onward. The minimal contract — pack identity and content hash, resolution (which pack a run uses), and data-plane storage (family reserved since Phase 2, ADR 0022) — blocks Phase 3. Skills and registry expansion (installed org-level packs, versioning/export, repo-local packs) remain a later candidate below.

### 6. UAT And Demo Mode — blocks Phase 4

Whether UAT is optional in MVP or required for Epic merge gates the evidence-package and Accept flow. `pkg/demo` reworks against this ADR (port inventory).

### 7. Intake And Triage — stage 2 — blocks Phase 5 (pre-Phase-5 spike)

Settled by the pre-Phase-5 spike: the executor (form logic, short-lived triage agent, provisional Work Group), the "I don't know" escalation flow, provisional Work Group lifecycle, recipient pushback protocol, cross-Epic coherence checking, and graduation criteria for a standing intake agent.

### 8. Workbench And The Interactive Loop — blocks Phase 5 (dedicated pre-Phase-5 spike + ADR)

Anchored 2026-07-15 (Phase 0 item 12 review; the reconcile found it had no phase slot). The Workbench is critical to v2 and is now scheduled end-to-end: a **dedicated pre-Phase-5 spike and Accepted ADR**, separate from intake stage 2; an explicit **Phase 5 output and end-to-end exit criterion** (dashboard entry → session on a real Epic branch → trailing evidence and drift review → human Accept); and **tempo-neutrality constraints on Phases 3 and 4** so the runtime and branch/evidence contracts cannot foreclose it. The open design questions live in the roadmap's Workbench spike section.

### 9. Skills And Pack Registry Expansion — Phase 5/6

The remainder of the packs/skills candidate after the Phase-3-blocking split above: installed org-level packs/skills as DB-canonical, immutable, versioned, exportable; repo-local packs; the skills registry (pillar 10).

### 10. Knowledge Hierarchy And Knowledge Packs — blocks Phase 6

Source precedence (ADRs, interfaces/contracts, docs, skills, AST/code facts), citation rules, staleness, pack generation. Inputs: the [cms spike](phase_0/spike_cms.md) (ingestion from maestro-cms, graph contributed upstream per its ADR 0005) and the [cms wishlist](requirements_maestro-cms-wishlist.md) responses.

### 11. Container Runtime Abstraction — post-MVP

A future container/execution interface with Docker as the only initial implementation. Useful for future Apple/iPhone/raw-filesystem cases.

### 12. Tool And Action Policy Gating — post-MVP

The full gating-policy ADR behind the Phase 3 hook: structural gates (role/env/tool allowlists, filesystem scopes), semantic gates (high-risk action summaries checked against policy), and human gates, per the research corpus (Day 4/Day 5).

### 13. External Agent Runtime Contract — post-MVP

Whether Maestro can run Claude Code, OpenHands, or other headless agents inside containers as first-class executors (beyond the v1-style Coder integration the port keeps).

### 14. Dispatcher/Message Abstraction For Cloud Jobs — v3

Whether agent communication should anticipate cloud job execution. ADR 0019 already records the trajectory (channels are transport, never state; RPC possible if runtimes split); avoid overbuilding before v3.
