+++
title = "Golden Stories"
edit_date = "2026-07-22"
status = "live"
summary = "Authored golden story definitions (TOML, one file per story), validated against the story schema in CI. Six active rungs — smoke, dependency bump, focused bug fix, multi-file breadth, API change with tests, and an app change proven against the running server — plus parked stories under blocked/."
+++

# Golden Stories

Authored golden story definitions: TOML, one file per story, strict keys,
validated against `story.SchemaVersion` by `stories_test.go` in CI.
Definitions reference pinned fixture repositories — see the schema in
[`../story/`](../story/story.go) and the
[fixture conventions](../../docs/v2/phase_1/process_fixtures.md).

Active rungs (everything in this directory; `bin/runner validate` loads it
non-recursively, so `blocked/` is excluded by construction):

- `smoke-comment` — rung 0, a one-line append on `golden-fixture-cms`.
- `dep-bump-xnet` — rung 1, dependency bump on `golden-fixture-cms`.
- `bugfix-openai-stopreason` — rung 3, focused bug fix on
  `golden-fixture-llms`, pinned at the parent of the real upstream fix
  (solution not in history), proven by SEEDED tests.
- `flag-chat-timeout` — breadth at roughly rung 3: a coordinated change
  across two source files, where a signature ripple means they cannot be
  edited independently.
- `api-option-lookup` — rung 4, an API contract change the agent must prove
  with tests it AUTHORS itself.
- `app-healthz-endpoint` — rung 5, an app change proven behaviourally: a real
  HTTP request through the application's own router.

Parked, in `blocked/`:

- `cleanup-provider-options` — rung 2, small code cleanup on
  `golden-fixture-chat`. Authored and proven achievable, but no run has
  completed; see the file header for the diagnosis.

**Acceptance contracts carry engine-owned oracles where behaviour can be
exercised hermetically.** Agent-authored tests are a deliverable, never proof:
`api-option-lookup` and `app-healthz-endpoint` each inject a test the agent
never sees, assert the contract, and remove it. Structural greps alone were
shown to accept implementations that ignore the requirement entirely.

Definitions are drafts until the runner executes them (items 3–5 make
them runnable; item 6 fixes budgets from instrumented costs). The suite grew to six active rungs in item 9 (`stories-suite`), which also assigns the
`golden-minimal` and `golden-all` tiers.
