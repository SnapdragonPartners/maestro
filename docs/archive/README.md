+++
title = "Archived Documentation"
edit_date = "2026-07-15"
status = "live"
summary = "docs/archive/ holds v1-era documents with no authority for any question; bodies preserved verbatim as history per ADR 0017's archive plan."
+++

# Archived Documentation

The archived documents in this directory are **history with no authority for any question** (ADR [0017](../adr/0017-v2-documentation-authority-and-lifecycle.md), status `archive`; this README itself is the directory's live no-authority notice — the one index 0017's amendment prescribes here). These are v1-era specs, plans, TODOs, and notes whose decisions were either implemented — making the code the authority — or abandoned. Their bodies are preserved verbatim beneath inserted front-matter, filenames intact, moved here by Phase 0 item 11 (see the [manifest](../v2/phase_0/manifest_doc-reset.md)); original paths are in git history, and no redirects are maintained. Because bodies were not rewritten, links *inside* archived documents may be stale or broken (many predate the move or point at other moved files) — that is expected, not a defect of the archive.

Do not cite these documents for v2 design or v1 runtime behavior. For v1 behavior, the authority order is code and tests, then the FSM docs, then the `deprecated`-status documents still at `docs/` root. For v2 design, use the Accepted ADRs (`docs/adr/` 0017+) and `docs/v2/`.
