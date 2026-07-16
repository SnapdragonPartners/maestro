+++
title = "Golden Stories"
edit_date = "2026-07-16"
status = "draft"
summary = "Authored golden story definitions (TOML, one file per story), validated against the story schema in CI. The first three ladder rungs — dependency bump, small cleanup, focused bug fix — against the pinned golden-fixture repos."
+++

# Golden Stories

Authored golden story definitions: TOML, one file per story, strict keys,
validated against `story.SchemaVersion` by `stories_test.go` in CI.
Definitions reference pinned fixture repositories — see the schema in
[`../story/`](../story/story.go) and the
[fixture conventions](../../docs/v2/phase_1/process_fixtures.md).

The ladder's first three rungs:

- `dep-bump-xnet` — dependency bump on `golden-fixture-cms`.
- `cleanup-provider-options` — small code cleanup on `golden-fixture-chat`
  (natural duplication, not seeded).
- `bugfix-openai-stopreason` — focused bug fix on `golden-fixture-llms`,
  pinned at the parent of the real upstream fix (solution not in history).

Definitions are drafts until the runner executes them (items 3–5 make
them runnable; item 6 fixes budgets from instrumented costs). The suite
grows to 5–10 in item 9 (`stories-suite`), which also assigns the
`golden-minimal` and `golden-all` tiers.
