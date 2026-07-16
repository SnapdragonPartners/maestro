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

- [Maestro v2 Roadmap](plan_roadmap.md) — The v2 roadmap: thesis, economic argument, vocabulary, 17 design pillars, phases 0-9 with exit criteria, and decisions D1-D10. Decisions are progressively ratified into ADRs (0017+), which outrank this document.
- [Maestro v2 Build Process (Interim)](process_build.md) — Working agreement for building v2 until Maestro can build Maestro: Claude authors, Codex reviews, DR orchestrates and accepts; branching, review cadence, spikes, testing, and merge rules.
- [Maestro Wishlist For maestro-llms](requirements_maestro-llms-wishlist.md) — Maestro's running feature-request wishlist to the maestro-llms team, with generality arguments and a response field per item — so the toolkit team can say what they're comfortable adding given their other consumers.
- [Maestro Wishlist For maestro-cms](requirements_maestro-cms-wishlist.md) — Maestro's running feature-request wishlist to the maestro-cms team, with generality arguments and a response field per item — the sibling of the maestro-llms wishlist, originating from the Phase 0 cms spike.
- [Maestro v2 Research Synthesis](research_synthesis.md) — Synthesis of the external research corpus that informed the v2 roadmap's pillars and decisions.
- [Maestro v2 Provenance Matrix](notes_provenance-matrix.md) — Tracks where each major v2 idea came from — Maestro v1, DR notes, the research corpus, Codex synthesis, or Claude review — including decisions that deliberately diverge from research orthodoxy.
- [Maestro v2 ADR Backlog](notes_adr-backlog.md) — Reconciled, dependency-ordered ADR backlog (Phase 0 item 12): candidates resolved in Phase 0 with their Accepted ADRs, and open candidates ordered by the phase they block.
- [Historical ADR Alignment With Maestro v2](notes_v1-adr-alignment.md) — Mapping of v1 subsystems to the historical ADR notes 0001-0016; an input to roadmap D8 and the port inventory.
- [Maestro v2 Parking Lot](notes_parking-lot.md) — Design ideas parked for later consideration — not planned work; an idea graduates to the roadmap or an ADR when picked up.

Per-phase working artifacts (scope/plan, spike reports, inventories, manifests) live in `phase_x/` directories matching the branch namespace, each with its own README index — see [phase_0/](phase_0/README.md) and [phase_1/](phase_1/README.md). Cross-phase documents stay at this root. Accepted decisions land in `docs/adr/`.

## Planning Posture

Maestro v2 is expected to be a breaking change. Clean, comprehensible architecture is more important than preserving v1 compatibility.

The current working thesis:

> Maestro v2 is a measurable, artifact-first agentic factory where epic-scoped work groups create and review production-grade changes under explicit Model/Prompt/Harness control.
