+++
title = "Design: Engine-Owned Behavioural Oracles (Item 9-oracle)"
edit_date = "2026-07-22"
status = "draft"
type = "design"
summary = "Mini-plan for first-class oracle assets in the golden runner: a story schema v2 `oracle` check that ships adjacent, hashed Go files the engine materialises against the bound solution and executes under the existing deadline/cleanup machinery. Replaces the base64-shell oracles embedded in the three new rung-4/5 stories. Bounded 2-4 day item, no reusable mutation framework, design amendment only (no new ADR)."
+++

# Design: Engine-Owned Behavioural Oracles

Status: **draft** — mini-plan authorised by Codex + DR (2026-07-22) as a bounded prerequisite to Phase 1 item 10; flips to `live` on implementation. This is a design amendment under [ADR 0025](../../adr/0025-golden-stories-and-benchmark-runner.md)'s existing rule that new check types arrive via a schema bump; **no new ADR is needed**. If the implementation estimate exceeds ~4 focused days it falls back to option 3 (a documented structural floor), per the authorisation.

## Why

Rung-4 and rung-5 stories need acceptance checks that verify *behaviour* — structural greps demonstrably accept implementations that ignore the requirement. The three new stories carry such checks today, but embedded as base64-encoded shell inside the story TOML, and that medium has produced **accidental false results with a cooperative agent, not just adversarial ones**: a truncated multi-line command reported success, a mutant that failed to compile was credited as detection, and cleanup artifacts leaked. Those corrupt honest measurements, which is the real defect. The fix is to stop smuggling code through shell and make oracles first-class, reviewable assets the engine owns.

Two things are deliberately **out of scope**: a generic mutation-testing framework (the arms-race generator), and any change to run records, manifests, adapters, budgets, or verdicts (all unchanged). Where a story genuinely benefits from mutation, it ships story-specific Go — readable and reviewable — not a reusable API.

Now is the cheapest moment: the three new stories have no accepted current-identity runs, so converting them moves no recorded hash.

## The contract

### Story schema v2 (`story/load.go`)

- Add `schema_version = 2`. **v1 continues to load unchanged**, so every existing story — and the recorded v1 baseline against `smoke-comment`/`dep-bump-xnet` — keeps its hash. Only the three new stories declare v2.
- A v2 story may carry an `oracle` check. A v1 story may not; the loader rejects `oracle` under v1. (Formal version bump per ADR 0025, not a silently-additive check type — Codex constraint.)
- An `oracle` check references one or more files under the story's own oracle directory, e.g. `stories/oracles/<story-id>/*.go`, by **relative path**.

### Oracle asset loading and identity

- At load time the engine reads each referenced oracle file **once** into memory, and that in-memory byte set is the single source of truth for both hashing and materialisation. The files are **never re-read later** — hashing one set of bytes and materialising a different set is the TOCTOU gap this closes (Codex constraint).
- The story content hash extends to cover the oracle: **sorted relative paths plus their contents**, folded into the existing canonical-hash computation. The acceptance contract is therefore fully captured by `story_hash`, so comparability and the "changing the contract moves the hash" invariant both hold.
- **Path safety, enforced at load:** regular files only (reject symlinks and non-regular entries); reject any path escaping the oracle directory (`..`, absolute paths); reject paths colliding with reserved destination names the materialiser uses. A story that violates these fails to load, loudly.

### Materialisation and execution (`engine/checks.go`)

- The `oracle` check materialises its in-memory bytes into the **bound solution checkout**, runs the declared command (a `go test`-shaped invocation), and captures pass/fail — under the **existing deadline and process-group machinery** that already governs command checks.
- **Per-check materialisation with guaranteed cleanup** on success, failure, *and* timeout. Correction to the earlier framing: engine diff-confinement compares two commits, so an untracked oracle file does **not** affect it (Codex correction) — but the achievability script and any subsequent check run against the worktree can still observe a leak, so strict cleanup remains mandatory. Materialise immediately before the oracle runs; remove immediately after, unconditionally.
- The oracle sees the solution as built; it is invisible to the *target*, because the target (the v1 factory) has already produced its solution branch before any check runs. Oracles exist only in the engine's post-hoc verification checkout.

### Mutation helpers (story-specific, no framework)

- Where a story verifies test-authoring quality (rung 4: are the agent's tests real, or vacuous?), it may ship a story-specific Go helper that swaps in reference implementations and requires the authored tests to fail against each.
- **Mutants run against scratch copies / worktrees, never the bound solution checkout** (Codex constraint), so a mutation cannot corrupt the state other checks see.
- **A mutant must compile before a test failure counts as detection.** In readable Go this is a trivial guard (`go build` before `go test`); it is the exact bug the shell medium kept reintroducing.

## Work

1. **Design sign-off** (this doc) — Codex + DR.
2. `story/load.go`: schema v2, oracle-asset load + hash, path-safety validation. Unit tests for v1/v2 coexistence, hash-includes-oracle, and every rejection (symlink, traversal, reserved name).
3. `engine/checks.go`: the `oracle` check type — materialise-run-cleanup under the deadline machinery. **Tested against the actual production materialiser/executor, not a duplicated facsimile** (Codex constraint) — covering success, failure, timeout-cleanup, and leak-freedom.
4. Convert `flag-instance-name`, `api-option-lookup`, `app-healthz-endpoint` to readable Go oracles; **delete all base64/shell oracle machinery**, and rework the achievability script's check execution to exercise the same production path.
5. Verify all three still achievability-green with the real oracles; both directions of each mutation helper proven.
6. **Then** item 10's phase-end `golden-all`, so official verdicts are recorded against sound contracts.

## What does not change

Run records, the manifest, adapters, budget accounting, verdict composition, and the v1 story schema are all untouched. The change is confined to how a story declares an acceptance check and how the engine executes that one new type. That containment is what keeps this a bounded item rather than a framework project.
