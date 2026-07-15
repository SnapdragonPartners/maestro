+++
title = "Phase 0 Artifacts"
edit_date = "2026-07-15"
status = "live"
summary = "Index of Phase 0 working artifacts: the scope/plan, the three spike reports, the v1 port inventory, and the doc-reset manifest."
+++

# Phase 0 Artifacts

Working artifacts of Phase 0 (v2 design groundwork), produced under the [build process](../process_build.md) and the [Phase 0 plan](plan_scope.md). Accepted decisions live in `docs/adr/` (0017-0025); these documents carry the work that produced and grounded them.

- [Maestro v2 Phase 0: Scope And Plan](plan_scope.md) — Approved Phase 0 scope and execution plan: 14 serial work items (ADRs, three spikes, port inventory, doc reset) with sizes, ordering, exit checklist, and resolved reviewer questions.
- [Spike Report: Toolloop Ownership](spike_toolloop.md) — Is a Maestro-owned toolloop distinct from maestro-llms still justified? Recommendation: yes as a harness layer, no as an engine — converge on llms/toolloop during the D8 port, contingent on the upstream requests in the maestro-llms wishlist.
- [Spike Report: The Disposable Project Folder](spike_project-folder.md) — How much non-disposable state can leave the user's filesystem? Answer: nearly all of it. v2 retires the project directory for a four-way OS-standard split: config (bootstrap pointer + root-of-trust key), cache (mirrors and reconstructible workspaces), state (active workspaces until pushed), data (the durable Postgres/object/local-forge root). The committed repo .maestro/ is the only surviving .maestro.
- [Spike Report: maestro-cms Boundary And Adoption](spike_cms.md) — What the v2 knowledge/document/binary work consumes from maestro-cms versus builds in Maestro. Recommendation: adopt cms for the ingestion pipeline; Maestro owns retrieval, citations, and packs; the generic graph primitive is contributed to cms per its ADR 0005 and consumed back, with Maestro keeping only ontology and policy.
- [Inventory: v1 Port, Rework, Rewrite, Drop](inventory_v1-port.md) — The D8 disposition table at package grain over the actual v1 package list: what ports as-is, what ports with rework, what is rewritten, and what is dropped — with breaking-change principles and the deltas from D8's first-pass guess.
- [Manifest: Doc Reset (Phase 0 Item 11)](manifest_doc-reset.md) — File-by-file record of the ADR 0017 archive-plan execution: every move to docs/archive/, every deprecated/live front-matter stamp, and every type_slug rename with its reference ripple.
