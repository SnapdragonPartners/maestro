# Blocked stories

Story definitions parked out of the active suite. `story.LoadDir` is
**non-recursive**, so files here are not loaded by `bin/runner validate` or by
a `run` that does not name them — they cannot be executed by accident, and
nothing here can silently spend money. Move a file back up to `stories/` to
re-activate it.

Parking a story is a statement about the *story*, not the target: these are
authoring defects to fix, not v1 failures to patch around.

| Story | Blocked since | Why |
|---|---|---|
| `cleanup-provider-options.toml` | 2026-07-21 (Phase 1, item 6) | Over-decomposes. Its prompt enumerates five near-identical provider functions to consolidate behind one shared helper, and the Architect splits that into **5 Stories, one per provider** — a split that is incoherent for the task, since all five converge on rewriting the same helper. Two attempts, **0 of 5 Stories ever completed**: 2.01M tokens / $9.37, then 2.26M tokens / $13.11. The cost is 5× ceremony multiplication, not intrinsic difficulty. **To unblock:** reword the prompt so it states the outcome without enumerating the five call sites, then re-measure. The missing structural fix — nothing reviews an Architect's decomposition, violating [ADR 0020](../../../docs/adr/0020-review-invariant-reviewer-vs-partner.md) — is [ADR backlog #15](../../../docs/v2/notes_adr-backlog.md) (Phase 5). |
