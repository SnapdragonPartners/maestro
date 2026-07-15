+++
title = "Maestro v2 Planning Notes"
edit_date = "2026-07-15"
status = "live"
summary = "Index of the v2 planning doc set: roadmap, build process, phase artifacts, wishlists, and companion notes, with the per-phase directory convention."
+++

# Maestro v2 Planning Notes

This directory collects working notes for the Maestro v2 redesign.

These documents are planning material, not implementation instructions. They are intended
to be reviewed, challenged, and refined before being converted into accepted ADRs,
specifications, and stories.

## Documents

- [Roadmap](plan_roadmap.md) - the main v2 roadmap: north star, pillars, phases 0-9 with exit criteria, decisions D1-D10, risks.
- [Build Process](process_build.md) - working agreement for how v2 gets built (author/reviewer/operator roles, branching, review cadence, testing).
- [maestro-llms Wishlist](requirements_maestro-llms-wishlist.md) - Maestro's running feature-request list to the toolkit team, with response fields for their feedback.
- [maestro-cms Wishlist](requirements_maestro-cms-wishlist.md) - the sibling wishlist for the content toolkit, from the Phase 0 cms spike.
- [Research Synthesis](research_synthesis.md) - synthesis of the external research corpus that informed the roadmap's pillars and decisions.
- [Provenance Matrix](notes_provenance-matrix.md) - where major ideas came from: Maestro v1, client/lived experience, research corpus, or Codex synthesis.
- [ADR Backlog](notes_adr-backlog.md) - concepts that need ADRs before implementation; reconciled and dependency-ordered in Phase 0 item 12.
- [v1 ADR Alignment](notes_v1-adr-alignment.md) - mapping of v1 subsystems to the historical ADR notes 0001-0016; an input to roadmap D8 and the port inventory.
- [Parking Lot](notes_parking-lot.md) - design ideas parked for later; an idea graduates to the roadmap or an ADR when picked up.

Per-phase working artifacts (scope/plan, spike reports, inventories, manifests) live in `phase_x/` directories matching the branch namespace, each with its own README index — see [phase_0/](phase_0/README.md). Cross-phase documents stay at this root. Accepted decisions land in `docs/adr/`.

## Planning Posture

Maestro v2 is expected to be a breaking change. Clean, comprehensible architecture is more important than preserving v1 compatibility.

The current working thesis:

> Maestro v2 is a measurable, artifact-first agentic factory where epic-scoped work groups create and review production-grade changes under explicit Model/Prompt/Harness control.
