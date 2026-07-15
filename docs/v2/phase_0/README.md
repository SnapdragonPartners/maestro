+++
title = "Phase 0 Artifacts"
edit_date = "2026-07-15"
status = "live"
summary = "Index of Phase 0 working artifacts: the scope/plan, the three spike reports, the v1 port inventory, and the doc-reset manifest."
+++

# Phase 0 Artifacts

Working artifacts of Phase 0 (v2 design groundwork), produced under the [build process](../process_build.md) and the [Phase 0 plan](plan_scope.md). Accepted decisions live in `docs/adr/` (0017-0025); these documents carry the work that produced and grounded them.

- [Scope And Plan](plan_scope.md) — Phase 0 deliverables, work items 0-13, and the exit checklist.
- [Toolloop Spike](spike_toolloop.md) — is a Maestro-owned toolloop still justified? Harness layer yes, engine no; converge on `llms/toolloop` during the D8 port.
- [Project-Folder Spike](spike_project-folder.md) — the disposable project folder: the four-way config/cache/state/data split, secrets root of trust, and the retirement of the "project directory."
- [maestro-cms Spike](spike_cms.md) — the cms boundary: adopt its ingestion pipeline, contribute the generic graph, keep retrieval and packs Maestro-side.
- [v1 Port Inventory](inventory_v1-port.md) — the D8 port/rework/rewrite/drop table at package grain, with breaking-change principles.
- [Doc-Reset Manifest](manifest_doc-reset.md) — the file-by-file record of the item 11 archive move, front-matter sweep, and renames.
