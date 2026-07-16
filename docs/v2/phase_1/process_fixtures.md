+++
title = "Fixture Conventions For Golden Stories"
edit_date = "2026-07-16"
status = "live"
summary = "The golden story fixture repositories (pinned variants of maestro-llms, maestro-cms, and the extracted chat app), their provenance, and the conventions that keep them honest: pinned immutable bases, solution-leakage truncation, no tags, run-branch cleanup, and the re-pin procedure."
+++

# Fixture Conventions For Golden Stories

Status: live — approved with item 2 (PR #262, Codex + DR, 2026-07-16; one Codex round incorporated), under [ADR 0025](../../adr/0025-golden-stories-and-benchmark-runner.md) and the [Phase 1 plan](plan_scope.md).

Golden stories run against **fixture repositories**: pinned, purpose-held variants of real codebases under the SnapdragonPartners GitHub org (the plan's ratified reviewer question 4). Fixtures give stories realistic brownfield friction without depending on the source repos' ongoing motion.

## The Fixture Repositories

| Fixture | Source provenance | `main` head | Role |
|---|---|---|---|
| [`golden-fixture-llms`](https://github.com/SnapdragonPartners/golden-fixture-llms) | `maestro-llms`, history **truncated** at `d5078df1dc3e304e9f1327e29ecfb48d1998f3dc` (the parent of upstream fix `92eceb8`), plus one seeded commit: `stopreason_seed_test.go` — the upstream fix's regression tests, seeded without the fix so acceptance is behavioral | `891dce215ef5c27cf575020e98f29bb876d96abc` | Library fixture; the bug-fix rung runs here against the pre-fix state |
| [`golden-fixture-cms`](https://github.com/SnapdragonPartners/golden-fixture-cms) | `maestro-cms` at `e7a7422bad4dec726b62eec2cd6d759cd7780deb` (full history) | `e7a7422bad4dec726b62eec2cd6d759cd7780deb` | Library fixture; dependency-bump and later rungs |
| [`golden-fixture-chat`](https://github.com/SnapdragonPartners/golden-fixture-chat) | `maestro-llms@6d9a7aa` `examples/chat`, extracted standalone: module repointed, toolkit dependency pinned to `v0.7.1`, provenance note in its README; second commit corrects the README's stale monorepo/`replace`-directive language | `e71d51bb8486137d8bf8fdf18da8913c3c021edd` | The app-bearing fixture; cleanup and later app-change rungs |

## Resolution Of ADR 0025's "LLM-tester CLI App"

ADR 0025 names "the standalone LLM-tester CLI app from the toolkit repos" as the starting app-bearing fixture. A search of both toolkit repos finds exactly one standalone application (`package main` with its own `go.mod`): `maestro-llms/examples/chat` — an LLM-tester that exercises every provider through one UI, explicitly designed to be copied out of the monorepo. It serves a local web page rather than being a pure CLI; this does not violate the ADR's constraint, which is about **evidence** ("no browser tooling required"): the low-rung stories against it validate through `go build`/`go vet`, not browser evidence. This document records that resolution; item 2's approval confirms it. (App-change stories with behavioral evidence remain deferred per the ADR until browser/evidence tooling exists.)

## Conventions

1. **Stories pin commits, not branches.** Every story definition carries a full 40-hex commit; the runner checks out that commit into a fresh run-scoped workspace (ADR 0025 repeat isolation). The fixture's `main` is a human convenience, not an input.
2. **Fixture `main` never advances casually.** A fixture moves only by the deliberate re-pin procedure below. Fixtures are never tracked against their upstreams automatically (Phase 1 plan risk list).
3. **No solution leakage.** A fixture whose story is "fix this bug" must not contain the future fix anywhere reachable — history is truncated at the story's base commit (`golden-fixture-llms` is cut at the parent of the upstream fix), and **no tags are pushed** to any fixture (a tag could reference a descendant carrying the solution).
4. **Fixtures are variants, and deltas carry provenance.** Any difference from source (the chat app's extraction: module path, version pin, README note) is recorded in the fixture's README and in the table above. Seeded content follows the same rule: recorded, never silent.
5. **Seeded regression tests make bug-fix acceptance behavioral.** A bug-fix story's expected behavior is expressed by tests seeded into the fixture (sourced from the real upstream fix where one exists), failing at the base and passing when solved — never by agent-authored tests, which cannot independently prove the behavior, and never by string-presence checks, which are gameable. Seed files declare themselves off-limits, and the story carries a deterministic check that the seed file is byte-identical to the base commit.
6. **Run-branch cleanup.** Everything a run creates in a fixture lives under its run-scoped branch namespace and is deleted after every run (ADR 0023's cleanup rule via ADR 0025). A run whose cleanup cannot be verified is recorded `invalid`. Fixture default branches are never written by runs.
7. **Fixtures are not dependencies.** No Maestro module may import fixture code; fixtures exist only to be cloned by the runner. They are public (their sources are public) and marked "not maintained" in their descriptions.

## Re-Pin Procedure

To move a fixture base (new rung requirements, upstream refresh):

1. Choose the new commit in the source repo; for bug-fix stories, choose the parent of the target fix and verify no descendant leaks the solution.
2. **Re-pins are additive — force-pushes are prohibited.** Every base commit any story definition has ever referenced must remain reachable from a ref, or a fresh clone cannot fetch it and old run records become unreproducible. New bases land as descendants of `main`, or as a new immutable `base/<story-id>` branch when the base is not a descendant. If the needed history genuinely diverges from what the fixture holds (e.g. a pre-fix cut *earlier* than the existing truncation), create a **new fixture repo** rather than rewriting this one.
3. Update the provenance table above and the affected story definitions' `fixture.commit` in the same PR — the story hash changes with it, keeping run records comparable-by-identity.

## Related Documents

- [ADR 0025](../../adr/0025-golden-stories-and-benchmark-runner.md) (fixture and cleanup rules), [ADR 0023](../../adr/0023-v2-branch-strategy.md) (branch cleanup).
- [Phase 1 plan](plan_scope.md) item 2; [design_runner.md](design_runner.md) (story schema the definitions follow).
- The first three story definitions: `benchmark/stories/` in the maestro repo.
