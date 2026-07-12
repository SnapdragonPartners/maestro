+++
title = "Maestro v2 Phase 0: Scope And Plan"
edit_date = "2026-07-12"
status = "live"
+++

# Phase 0: v2 Design Groundwork — Scope And Plan

Status: live — approved by Codex and DR, 2026-07-12, per [build-process.md](../build-process.md).

Status lifecycle (applies to all phase artifacts): `draft` while under review; flipped to `live` after both approvals, as the final commit before merge and before any work items start; `archive` when the phase completes and the document becomes a historical record. Work item 1 formalizes this lifecycle as part of the front-matter convention.

Goal (from the [roadmap](../roadmap.md)): decide the conceptual shape before code churn.

## Scope

In scope:

- The Phase 0 ADR set (below), each Accepted per the build process.
- The two Phase 0 spikes: toolloop ownership, and the disposable project folder.
- The port-vs-rewrite inventory at package grain (roadmap D8), informed by the spikes.
- Documentation reset: archive stale docs, adopt the front-matter convention, make remaining repo docs agent-ingestible.
- Reconciled, dependency-ordered ADR backlog (supersedes the interim priority list in [v1-adr-alignment.md](../v1-adr-alignment.md)).
- Breaking-change principles (D7 is agreed; recorded alongside the inventory).

Out of scope:

- Implementation code. Spike code never merges into app packages; scripts worth revisiting may be preserved under `spikes/phase_0/` (see sequencing notes).
- Golden story and runner *implementation* (Phase 1) — but their ADR is in scope and Phase 0 exit-blocking.
- The final intake executor design (pre-Phase-5 spike). Phase 0 fixes only the intake artifact contract and orchestrator seam.
- Postgres DDL and migrations (Phase 2). Phase 0 decides the stack and schema families only.
- Any renaming or refactoring of v1 code.

The v2 product thesis and MPH definition already exist in the roadmap and README; Phase 0 ratifies them by reference in the ADRs rather than producing a separate memo.

## Deliverables And PR Sequence

One short-lived branch per item (`v2/phase_0/XXX`), one open at a time, single end-of-work review each per the build process. Order respects dependencies; sizes are rough (S under a day of review-ready work, M a few days).

Deliverable locations: ADRs land in `docs/adr/` (continuing the single 0017+ sequence); all other deliverables — spike reports, the port inventory, checklists — land in this directory (`docs/v2/phase_0/`). The backlog reconciliation edits the existing cross-phase docs at the `docs/v2/` root.

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
- Before a spike begins, all open document work is committed — risk minimization against spike churn.
- Spike scripts worth revisiting may be preserved under `spikes/phase_0/`, which must be its own Go module (own `go.mod`) so `go build ./...`, `go test ./...`, and lint walkers exclude it. Spike code never lives under `pkg/`, `internal/`, or `cmd/`. The report remains the deliverable; preserved scripts are a courtesy to the future, not a maintained surface.

## Exit Checklist

The roadmap's Phase 0 exit criteria, plus the Phase 0 scope items that are not themselves roadmap exit criteria:

- [ ] Taxonomy, artifact model (including artifact scope), branch strategy, data plane, and reviewer/partner ADRs Accepted. (Items 2–5.)
- [ ] Phase 1-blocking ADR Accepted before any Phase 1 implementation: golden story schema / benchmark runner, including the D9 mechanism and the Phase 1 target strategy. (Item 7.)
- [ ] The v2 MVP boundary (D1) and the port-vs-rewrite inventory (D8) written down and agreed. (Items 2 and 10; D1 is ratified inside the taxonomy ADR.)
- [ ] Documentation reset done: stale docs archived, remaining repo docs safe for agent ingestion. (Items 1 and 11.)
- [ ] Reconciled ADR backlog. (Item 12.)
- [ ] Intake/triage contract ADR Accepted (item 6); both spike reports delivered (items 8–9).
- [ ] All remaining table deliverables completed, or explicitly deferred by agreement of DR and Codex.

## Risks

- **ADR sprawl.** The Phase 0 set is capped at the table above; every other backlog candidate stays in the backlog for later phases. New ADR needs discovered mid-phase go to the backlog, not the phase.
- **Spike scope creep.** Spikes produce a report and a recommendation, never a refactor. Throwaway code stays on the spike branch.
- **Review bottleneck.** Serial PRs are deliberate (bounded operator load); the spikes are the pressure-relief valve when a review stalls.

## Reviewer Questions — Resolutions

Codex has answered (2026-07-12); DR confirmation rides on this document's approval.

1. ADR numbering: **continue the single sequence (0017+)** in `docs/adr/` with the v1 notes marked historical. Codex concurs: a second v2 series creates avoidable authority ambiguity. Formalized in item 1.
2. Item 2 bundling three conceptual ADRs in one PR: **acceptable**, with the stated review checkpoint after the taxonomy ADR — the three are coupled enough that reviewing together beats serial churn.
3. Doc reset ordering: **after the ADRs**, as sequenced — the archive plan should be informed by the accepted decisions it applies.
