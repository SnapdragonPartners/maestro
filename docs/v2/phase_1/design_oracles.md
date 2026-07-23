+++
title = "Design: Engine-Owned Behavioural Oracles (Item 9-oracle)"
edit_date = "2026-07-22"
status = "draft"
type = "design"
summary = "Mini-plan for first-class oracle assets in the golden runner: a story schema v2 `oracle` check that ships adjacent, hashed Go files the engine materialises against the bound solution and executes under the existing deadline/cleanup machinery. Replaces the base64-shell oracles embedded in the three new rung-4/5 stories. Bounded 2-4 day item, no reusable mutation framework, design amendment only (no new ADR)."
+++

# Design: Engine-Owned Behavioural Oracles

Status: **draft** — mini-plan authorised by Codex + DR (2026-07-22) as a bounded prerequisite to Phase 1 item 10. It flips to `live` only **after the implementation is approved**, as the final pre-merge commit — not on first code, not on plan sign-off. This is a design amendment under [ADR 0025](../../adr/0025-golden-stories-and-benchmark-runner.md)'s existing rule that new check types arrive via a schema bump; **no new ADR is needed**. If the implementation estimate exceeds ~4 focused days it falls back to option 3 (a documented structural floor), per the authorisation.

## Why

Rung-4 and rung-5 stories need acceptance checks that verify *behaviour* — structural greps demonstrably accept implementations that ignore the requirement. The three new stories carry such checks today, but embedded as base64-encoded shell inside the story TOML, and that medium has produced **accidental false results with a cooperative agent, not just adversarial ones**: a truncated multi-line command reported success, a mutant that failed to compile was credited as detection, and cleanup artifacts leaked. Those corrupt honest measurements, which is the real defect. The fix is to stop smuggling code through shell and make oracles first-class, reviewable assets the engine owns.

Two things are deliberately **out of scope**: a generic mutation-testing framework (the arms-race generator), and any change to run records, manifests, adapters, budgets, or verdicts (all unchanged). Where a story genuinely benefits from mutation, it ships story-specific Go — readable and reviewable — not a reusable API.

Now is the cheapest moment: the three new stories have no accepted current-identity runs, so converting them moves no recorded hash.

## The contract

### Story schema v2 and the concrete `oracle` check (`story/load.go`)

`schema_version = 2`. **v1 continues to load unchanged**, so every existing story — and the recorded v1 baseline against `smoke-comment`/`dep-bump-xnet` — keeps its hash. Only the three new stories declare v2. A v2 story may carry an `oracle` check; a v1 story may not, and the loader rejects `oracle` under v1 (formal version bump per ADR 0025, not a silently-additive type).

Concrete TOML shape:

```toml
schema_version = 2

[[checks]]
name    = "oracle-lookup-semantics"
type    = "oracle"
# Source assets, relative to the story's own oracle dir (stories/oracles/<id>/).
# Every asset is materialised into <package_dir> in the bound solution before
# the command runs, and removed after.
assets      = ["oracle_test.go"]
# Where assets land in the solution tree. Package files (e.g. _test.go) go
# beside the code they compile against; "" means the repo root.
package_dir = ""
# argv (not a shell string) run with cwd = the bound solution checkout.
command     = ["go", "test", "-run", "TestOracle", "."]
```

Field semantics:

- **`assets`** — one or more source files, each a relative path under `stories/oracles/<story-id>/`. These are the bytes hashed and materialised.
- **`package_dir`** — the destination directory within the bound solution. All of a check's assets share one destination; `""` is the repo root.
- **`command`** — an **argv array**, not a shell string. Run with `cwd` set to the bound solution checkout, under the existing deadline/process-group machinery. Argv, so there is no shell to re-introduce the truncation/quoting hazards this whole change exists to remove.
- **All assets are injected into the solution** — there is no "private helper" asset class. A helper a story wants kept out of the compiled package is simply not referenced by any oracle; assets named in a check are, by definition, materialised. Keeping one asset class removes a decision point and a failure mode.
- **Collision behaviour: exclusive create, never overwrite.** Materialisation uses `O_CREATE|O_EXCL`. If a destination path already exists in the solution — an agent wrote a file of that name — the check **fails loudly** rather than clobbering a solution file. Destination basenames are therefore drawn from a reserved namespace (a fixed prefix, e.g. `zz_oracle_*`) that story authoring must not collide with, checked at load.

### Identity and path invariants

- **Canonical v2 hash envelope.** The oracle folds into the existing canonical-JSON hash as a sorted list of `{path, sha256(content)}` entries — sorted by normalised relative path, each carrying the content digest — appended to the definition before hashing. Deterministic regardless of TOML ordering. The acceptance contract is thus fully captured by `story_hash`, preserving comparability and the "changing the contract moves the hash" invariant.
- **v1 hashes are pinned in regression tests.** The known `story_hash` of every existing v1 story (`smoke-comment` `sha256:75495b46c1a2…`, `dep-bump-xnet` `sha256:6b5141b820bb…`, and the rest) is asserted verbatim, so the schema-v2 work cannot silently move a v1 identity and invalidate the recorded baseline.
- **Materialise only the bytes retained in `Loaded`.** The files are read once at load into `Loaded`; hashing and materialisation both use that retained byte set and never touch the filesystem copy again (closes the TOCTOU gap — hashing one set of bytes and materialising another).
- **Path safety, enforced at load, rejecting loudly:**
  - **Symlinks in any path component** — every component of every asset path is `lstat`-checked; a symlink anywhere is rejected (not just the leaf).
  - **Absolute or traversing paths** (`/…`, `..`) that escape the oracle directory.
  - **Duplicate normalised paths** within one check — two assets that normalise to the same destination.
  - **Non-regular files** — only regular files are assets.
  - **Reserved-namespace collisions** — a destination basename outside the oracle prefix, or one that would land on an existing solution path at materialise time (the exclusive-create guard is the second line of defence).

### Materialisation and execution (`engine/checks.go`)

- The `oracle` check materialises its in-memory bytes into the **bound solution checkout**, runs the declared command (a `go test`-shaped invocation), and captures pass/fail — under the **existing deadline and process-group machinery** that already governs command checks.
- **Per-check materialisation with guaranteed cleanup** on success, failure, *and* timeout. Correction to the earlier framing: engine diff-confinement compares two commits, so an untracked oracle file does **not** affect it (Codex correction) — but the achievability script and any subsequent check run against the worktree can still observe a leak, so strict cleanup remains mandatory. Materialise immediately before the oracle runs; remove immediately after, unconditionally.
- The oracle sees the solution as built; it is invisible to the *target*, because the target (the v1 factory) has already produced its solution branch before any check runs. Oracles exist only in the engine's post-hoc verification checkout.

### Mutation helpers (story-specific, no framework)

- Where a story verifies test-authoring quality (rung 4: are the agent's tests real, or vacuous?), it may ship a story-specific Go helper that swaps in reference implementations and requires the authored tests to fail against each.
- **Scratch is created from the immutable solution commit, not from a filesystem copy taken after oracle materialisation** (Codex constraint). This is the load-bearing detail: if the scratch were a copy of the materialised tree, it would contain the engine's oracle, and the *oracle* — not the agent's authored tests — could detect the mutant, defeating the point. Scratch is a fresh checkout of the solution commit, which carries the agent's tests but no oracle, so only the authored tests judge the mutant.
- **The engine owns the scratch root and removes it on every exit path, including timeout.** A killed helper must not leak worktrees; the scratch root is registered for cleanup with the same guarantee as the materialised assets, not left to the helper's own `defer`.
- **A mutant must compile before a test failure counts as detection.** `go build` before `go test`; a non-compiling mutant is a broken check, reported as such, never scored as detection. It is the exact bug the shell medium kept reintroducing, trivial to get right in readable Go.

### Shared verification entrypoint (`engine/`, `cmd/runner/`)

Both the engine's own attempt flow and the achievability script must run checks through **one** production path — otherwise the achievability script re-implements check execution and we are back to a facsimile, which is what step 4 exists to prevent.

- Expose an **exported verifier** the engine already uses internally (today's `runChecks` and validator execution, promoted to an exported `Verify(ctx, boundDir, def) (Verdict-ish)` seam, or equivalent), and a **`runner verify <story> --workspace <dir>`** subcommand that calls it against an already-prepared solution directory.
- The achievability script drops its own check loop and shells out to `runner verify`. It then exercises the exact materialiser, argv executor, cleanup, and oracle semantics the engine uses — no second copy to drift.
- `cmd/runner` and its tests are **in scope for this item** (the current runner has selection-contract tests but none for `verify`).

## Work

1. **Design sign-off** (this doc, revised) — Codex + DR. Codex asked that the concrete schema *and* the shared verification entrypoint be settled before any code; both are now specified above.
2. `story/load.go`: schema v2, oracle-asset load into `Loaded`, hash envelope, path-safety validation. Unit tests for v1/v2 coexistence, **verbatim-pinned v1 hashes**, hash-includes-oracle, and every rejection (symlink-in-any-component, traversal, absolute, duplicate-normalised, non-regular, reserved-name).
3. Shared verifier: the exported `Verify` seam and `runner verify` subcommand, with `cmd/runner` tests.
4. `engine/checks.go`: the `oracle` check type — exclusive-create materialise → argv run under the deadline machinery → unconditional cleanup. **Tested against the actual production materialiser/executor, not a facsimile** — success, failure, timeout-cleanup, leak-freedom, and the exclusive-create-refuses-to-overwrite guard.
5. Convert `flag-instance-name`, `api-option-lookup`, `app-healthz-endpoint` to readable Go oracles under `stories/oracles/<id>/`; **delete all base64/shell oracle machinery**; point the achievability script at `runner verify`.
6. Verify all three achievability-green with the real oracles; both directions of each mutation helper proven; each mutant shown to compile.
7. **Then** item 10's phase-end `golden-all`, so official verdicts are recorded against sound contracts.

## What changes, and what does not

**Changes** (stated plainly, since one is a contract change): the **story schema** gains version 2 and the `oracle` check type — an additive schema evolution, but a schema change nonetheless, so the claim is not "no contracts change." Story identity gains the oracle hash envelope for v2 stories only.

**Unchanged:** run records, the manifest, adapters, budget accounting, verdict composition, and the entire v1 schema and every v1 story's hash. The change is confined to how a v2 story declares an acceptance check and how the engine executes that one new type, plus the exported verifier seam. That containment — not an absence of contract change — is what keeps this a bounded item rather than a framework project.
