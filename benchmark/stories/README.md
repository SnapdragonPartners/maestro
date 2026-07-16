+++
title = "Golden Stories"
edit_date = "2026-07-16"
status = "draft"
summary = "Authored golden story definitions (TOML, one file per story), validated against the story schema on load. First definitions land with Phase 1 item 2 (fixtures)."
+++

# Golden Stories

Authored golden story definitions: TOML, one file per story, strict keys,
validated against `story.SchemaVersion` on load. Definitions reference
pinned external fixture repositories — see the schema in
[`../story/`](../story/story.go) and the fixture conventions doc (Phase 1
item 2, forthcoming).

The first 3 definitions land with item 2 (`fixtures`); the suite grows to
5–10 in item 9 (`stories-suite`), which also assigns the `golden-minimal`
and `golden-all` tiers.
