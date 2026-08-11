+++
title = "Phase 3 Artifacts"
edit_date = "2026-08-11"
status = "live"
summary = "Index of Phase 3 working artifacts: the pre-entry blocker plan, and the phase scope and plan once it is written."
+++

# Phase 3 Artifacts

Working artifacts of Phase 3 (minimal Work Hierarchy, Work Group runtime, and v1
retirement), produced under the [build process](../process_build.md). The phase
goal and exit criteria come from the [roadmap](../plan_roadmap.md); the binding
specifications will be the ADRs the pre-entry work below produces.

- [Pre-Phase-3 Blockers: Scope And Sequencing](plan_blockers.md) — What must be settled before Phase 3 implementation begins: five design decisions — four ADRs (Habitat with its fencing protocol, tool-execution policy hook, prompt-pack identity, agent execution contract) and an ADR 0019 amendment for amendment-vs-running-work — plus a parallel cloud-portability proof gating Orchestrator wiring, benchmark repair for the two runs Phase 3 owes, and the authority cleanup the ADR backlog needs before any of it can be Accepted.
- [Execution Contracts: Verbs, Result Shape, And Where They Run](notes_execution-contracts.md) — Design input for the Phase 3 plan on the build/test/lint/deploy contract set: what v1 actually has and how thin it is, why the two Habitat deployment stages are identity changes rather than two verbs, why the invocation half of a contract needs almost nothing and the result half needs a preserved audit artifact, the verb inventory Phase 3 should prune, and a recommended Habitat lease-reclamation design — Story-bound leases reclaimed on demand rather than on a clock, with provisioning cost measured rather than configured — which ADR 0029 deliberately left open.

The phase scope and plan (`plan_scope.md`) is written after the blocker plan's
Track A is Accepted, and is listed here when it exists.
