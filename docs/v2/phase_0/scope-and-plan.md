+++
title = "Maestro v2 Phase 0: Scope And Plan"
edit_date = "2026-07-12"
status = "draft"
+++

# Phase 0: v2 Design Groundwork — Scope And Plan

Status: draft. Per [build-process.md](build-process.md), the scope and the plan each require Codex and DR approval before Phase 0 work items start.

Goal (from the [roadmap](roadmap.md)): decide the conceptual shape before code churn.

## Scope

In scope:

- The Phase 0 ADR set (below), each Accepted per the build process.
- The two Phase 0 spikes: toolloop ownership, and the disposable project folder.
- The port-vs-rewrite inventory at package grain (roadmap D8), informed by the spikes.
- Documentation reset: archive stale docs, adopt the front-matter convention, make remaining repo docs agent-ingestible.
- Reconciled, dependency-ordered ADR backlog (supersedes the interim priority list in [v1-adr-alignment.md](v1-adr-alignment.md)).
- Breaking-change principles (D7 is agreed; recorded alongside the inventory).

Out of scope:

- Implementation code. Spike code is throwaway and never merges.
- Golden story and runner *implementation* (Phase 1) — but their ADR is in scope and Phase 0 exit-blocking.
- The final intake executor design (pre-Phase-5 spike). Phase 0 fixes only the intake artifact contract and orchestrator seam.
- Postgres DDL and migrations (Phase 2). Phase 0 decides the stack and schema families only.
- Any renaming or refactoring of v1 code.

The v2 product thesis and MPH definition already exist in the roadmap and README; Phase 0 ratifies them by reference in the ADRs rather than producing a separate memo.

## Deliverables And PR Sequence

One short-lived branch per item (`v2/phase_0/XXX`), one open at a time, single end-of-work review each per the build process. Order respects dependencies; sizes are rough (S under a day of review-ready work, M a few days).

| # | Branch suffix | Deliverable | Size |
|---|---|---|---|
| 0 | `scope-and-plan` | This document, Accepted. | S |
| 1 | `adr-lifecycle` | ADR: v2 documentation authority and ADR lifecycle — numbering, what Accepted means (Codex + DR approval), TOML front-matter convention, and the archive plan for stale docs (plan only; execution is item 11). | S |
| 2 | `adr-taxonomy` | Three coupled conceptual ADRs: taxonomy (Product/Feature/Epic/Story/Work Group/Workbench, incl. collapsible hierarchy and MPH by reference); Orchestrator boundary (no-inference rule, v1 kernel/supervisor/dispatcher lineage); Reviewer vs Partner/Supervisor incl. the symmetric review invariant. | M |
| 3 | `adr-artifacts` | ADR: Management/Audit artifacts, the scope model (`scope_type`/`scope_id` + lineage), minimal signatures, agent instances. | M |
| 4 | `adr-data-plane` | ADR: Postgres/sqlc/golang-migrate, Docker-local default, multi-user scope boundaries, repo-vs-database split (D5). | M |
| 5 | `adr-branching` | ADR: branch hierarchy (Story → Epic → default), rebase as a harness function, and the working branch-naming convention (including the leaf-vs-namespace rule learned on #239). | S |
| 6 | `adr-intake-contract` | ADR: intake/triage artifact contract with the executor deliberately unbound (D2). | S |
| 7 | `adr-benchmark` | ADR: golden stories and benchmark runner — black-box contract, self-contained persistence, D9 sampling/budget mechanism (numeric values provisional until first instrumented runs), Phase 1 target strategy (minimally patched v1 path), `golden-minimal`/`golden-all` build tags. **Phase 1-blocking.** | M |
| 8 | `spike-toolloop` | Spike report: is a Maestro-owned toolloop distinct from the `maestro-llms` toolloop still justified? Recommendation only; no refactor. | M |
| 9 | `spike-project-folder` | Spike report: how much non-disposable state can leave the user's filesystem for the data plane (or the repo, where it is a true project artifact)? Includes the bootstrap-mode / `.maestro/` question. | S |
| 10 | `port-inventory` | The D8 port/rework/rewrite/drop inventory at package grain over the actual v1 package list, using both spike results. Records breaking-change principles. | M |
| 11 | `doc-reset` | Execute the archive plan from item 1; apply front-matter to live docs. | M |
| 12 | `backlog-reconcile` | Reconciled, dependency-ordered ADR backlog; Phase 0 exit checklist review against the roadmap. | S |

Sequencing notes:

- Items 8 and 9 have no ADR dependencies and are the designated slack: if an ADR review stalls, a spike proceeds without violating the one-branch rule (the stalled branch closes or merges first).
- Item 2 bundles three closely coupled conceptual ADRs for review coherence. If it runs large, the plan's checkpoint mechanism applies: review checkpoint after the taxonomy ADR before the other two are drafted.
- Item 7 is deliberately last of the ADRs: it consumes the artifact model (3), data plane families (4), and branching (5).

## Exit Checklist

Maps one-to-one to the roadmap's Phase 0 exit criteria:

- [ ] Taxonomy, artifact model (including artifact scope), branch strategy, data plane, and reviewer/partner ADRs Accepted. (Items 2–5.)
- [ ] Phase 1-blocking ADR Accepted before any Phase 1 implementation: golden story schema / benchmark runner, including the D9 mechanism and the Phase 1 target strategy. (Item 7.)
- [ ] The v2 MVP boundary (D1) and the port-vs-rewrite inventory (D8) written down and agreed. (Items 2 and 10; D1 is ratified inside the taxonomy ADR.)
- [ ] Documentation reset done: stale docs archived, remaining repo docs safe for agent ingestion. (Items 1 and 11.)
- [ ] Reconciled ADR backlog. (Item 12.)

## Risks

- **ADR sprawl.** The Phase 0 set is capped at the table above; every other backlog candidate stays in the backlog for later phases. New ADR needs discovered mid-phase go to the backlog, not the phase.
- **Spike scope creep.** Spikes produce a report and a recommendation, never a refactor. Throwaway code stays on the spike branch.
- **Review bottleneck.** Serial PRs are deliberate (bounded operator load); the spikes are the pressure-relief valve when a review stalls.

## Open Questions For Reviewers

1. ADR numbering: continue the single sequence (0017+) in `docs/adr/` with the v1 notes marked historical, or start a v2 series (`docs/adr/v2/0001+`)? Recommendation: continue the single sequence — one authority trail, no ambiguity about which series wins. Decided in item 1.
2. Item 2 bundles three ADRs in one PR. Acceptable for review coherence, or split into three serial PRs?
3. Should the doc reset (item 11) land before the ADRs so they're written into a clean tree, or after so the archive plan can account for everything the ADRs supersede? Recommendation: after, as sequenced.
