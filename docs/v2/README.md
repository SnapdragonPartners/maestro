+++
title = "Maestro v2 Planning Notes"
edit_date = "2026-07-13"
status = "live"
summary = "Index of the v2 planning doc set: roadmap, build process, phase artifacts, wishlists, and companion notes, with the per-phase directory convention."
+++

# Maestro v2 Planning Notes

This directory collects working notes for the Maestro v2 redesign.

These documents are planning material, not implementation instructions. They are intended
to be reviewed, challenged, and refined before being converted into accepted ADRs,
specifications, and stories.

## Documents

- [Roadmap](roadmap.md) - main v2 roadmap draft.
- [maestro-llms Wishlist](requirements_maestro-llms-wishlist.md) - Maestro's running feature-request list to the toolkit team, with response fields for their feedback.
- [maestro-cms Wishlist](requirements_maestro-cms-wishlist.md) - the sibling wishlist for the content toolkit, from the Phase 0 cms spike.
- [Build Process](build-process.md) - interim working agreement for how v2 gets built (author/reviewer/operator roles, branching, review cadence, testing).
- [Phase 0 Scope And Plan](phase_0/scope-and-plan.md) - the first phase artifact under the build process: deliverables, PR sequence, exit checklist.

Per-phase working artifacts (scope/plan, spike reports, inventories) live in `phase_x/` directories matching the branch namespace; cross-phase documents stay at this root. Accepted decisions land in `docs/adr/`.
- [Research Synthesis](research-synthesis.md) - synthesis of the external research corpus and early Maestro v2 ideas.
- [Provenance Matrix](provenance-matrix.md) - where major ideas came from: Maestro v1, client/lived experience, research corpus, or Codex synthesis.
- [ADR Backlog](adr-backlog.md) - concepts that should probably become ADRs before implementation.
- [v1 ADR Alignment](v1-adr-alignment.md) - first pass on how the proposed v1 ADRs relate to the v2 vision.
- [Parking Lot](parking-lot.md) - useful ideas that are too granular, speculative, or post-MVP for the main roadmap.

## Planning Posture

Maestro v2 is expected to be a breaking change. Clean, comprehensible architecture is more important than preserving v1 compatibility.

The current working thesis:

> Maestro v2 is a measurable, artifact-first agentic factory where epic-scoped work groups create and review production-grade changes under explicit Model/Prompt/Harness control.
