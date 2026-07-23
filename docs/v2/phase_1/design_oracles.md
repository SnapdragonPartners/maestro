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
# Source BASENAMES must already be in the reserved zz_oracle_ namespace, so the
# destination is the source name verbatim — no rename, no mapping to predict.
assets      = ["zz_oracle_lookup_test.go"]
# Destination directory within the bound solution; each asset lands at
# <package_dir>/<basename>. "" is the repo root. Test files go beside the code
# they compile against.
package_dir = ""
# argv (a distinct array field, NOT v1's string-valued `command`), run with
# cwd = the bound solution checkout. No shell, so nothing re-introduces the
# truncation/quoting hazards this change exists to remove.
argv        = ["go", "test", "-run", "TestOracle", "."]
```

Field semantics:

- **`assets`** — source files under `stories/oracles/<story-id>/`, whose **basenames must already be in the reserved `zz_oracle_` namespace** (`zz_oracle_lookup_test.go`, not `oracle_test.go`). Enforced at load. This makes the source→destination mapping trivial and unambiguous: **basename preserved verbatim** into `package_dir`. No flattening ambiguity (paths are basename-only at the destination), no rename the author must predict, and the reserved prefix guarantees the destination cannot shadow an ordinary solution file.
- **`package_dir`** — destination directory within the bound solution; all of a check's assets share it; `""` is the repo root. Subject to the **same path validation as assets**: rejected if absolute or traversing at load, and its components are symlink-checked in the *solution* at materialise time (the solution is agent-controlled, so this is a runtime check, not just a load-time one).
- **`argv`** — a **distinct array field**, deliberately not an array overload of v1's string `command` (which would force custom union decoding on the shared struct). v1 `command` checks keep their string; `oracle` checks use `argv`. Run with `cwd` = the bound solution checkout, under the existing deadline/process-group machinery.
- **One asset class.** Every named asset is materialised into the solution; there is no separate "private helper" class. A file a story wants kept out of the compiled package is simply not referenced. (Scratch-mode oracles, below, are the one exception: their assets land in a tool dir, never the solution or the scratch.)
- **Collision: exclusive create, never overwrite.** Materialisation is `O_CREATE|O_EXCL` at `<package_dir>/<basename>`. Because destination basenames are always in the `zz_oracle_` namespace, a collision means either two assets of the same basename (rejected at load as a duplicate destination) or an agent-authored file already using a reserved name — either way the check **fails loudly**, never clobbers.

### Identity and path invariants

- **Canonical v2 hash envelope.** The oracle folds into the existing canonical-JSON hash as a sorted list of `{path, sha256(content)}` entries — sorted by normalised relative path, each carrying the content digest — appended to the definition before hashing. Deterministic regardless of TOML ordering. The acceptance contract is thus fully captured by `story_hash`, preserving comparability and the "changing the contract moves the hash" invariant.
- **v1 hashes are pinned in regression tests.** The known `story_hash` of every existing v1 story (`smoke-comment` `sha256:75495b46c1a2…`, `dep-bump-xnet` `sha256:6b5141b820bb…`, and the rest) is asserted verbatim, so the schema-v2 work cannot silently move a v1 identity and invalidate the recorded baseline.
- **Materialise only the bytes retained in `Loaded`.** The files are read once at load into `Loaded`; hashing and materialisation both use that retained byte set and never touch the filesystem copy again (closes the TOCTOU gap — hashing one set of bytes and materialising another).
- **Path safety, enforced at load, rejecting loudly:**
  - **Symlinks in any path component** — every component of every asset path is `lstat`-checked; a symlink anywhere is rejected (not just the leaf).
  - **Absolute or traversing paths** (`/…`, `..`) that escape the oracle directory.
  - **Duplicate normalised paths** within one check — two assets that normalise to the same destination.
  - **Non-regular files** — only regular files are assets.
  - **Reserved-namespace violations** — a source basename NOT in the `zz_oracle_` namespace (rejected at load); at materialise time, an existing solution path at the destination is the exclusive-create guard's second line of defence.
  - **`package_dir` traversal/symlink** — `package_dir` is load-checked for absolute/`..`, and its components in the bound solution are symlink-checked at materialise time.

### Materialisation and execution (`engine/checks.go`)

- The `oracle` check materialises its in-memory bytes into the **bound solution checkout**, runs the declared command (a `go test`-shaped invocation), and captures pass/fail — under the **existing deadline and process-group machinery** that already governs command checks.
- **Per-check materialisation with guaranteed cleanup** on success, failure, *and* timeout. Correction to the earlier framing: engine diff-confinement compares two commits, so an untracked oracle file does **not** affect it (Codex correction) — but the achievability script and any subsequent check run against the worktree can still observe a leak, so strict cleanup remains mandatory. Materialise immediately before the oracle runs; remove immediately after, unconditionally.
- The oracle sees the solution as built; it is invisible to the *target*, because the target (the v1 factory) has already produced its solution branch before any check runs. Oracles exist only in the engine's post-hoc verification checkout.

### Mutation helpers — scratch mode (explicit in the check contract)

Rung-4 stories verify test-authoring quality (are the agent's tests real, or vacuous?) by running the agent's tests against reference implementations that each violate one clause. That needs a clean copy of the solution the helper can mutate — and the executor must be *told* a check needs one, so scratch is an explicit field on the `oracle` check, not an implicit helper behaviour.

```toml
[[checks]]
name        = "authored-tests-cover-each-behaviour"
type        = "oracle"
assets      = ["zz_oracle_mutate.go", "zz_oracle_refs.go"]
scratch     = "solution-commit"   # opt in; default "" = in-solution oracle
# cwd = the tool dir holding the assets; the helper reads $ORACLE_SCRATCH.
argv        = ["go", "run", "zz_oracle_mutate.go", "zz_oracle_refs.go"]
```

Contract when `scratch = "solution-commit"`:

- **The engine creates the scratch from the immutable solution commit** — `git worktree`/checkout of that commit into an engine-owned temp root — *not* a filesystem copy of the materialised tree. This is the load-bearing detail: a copy of the materialised tree would contain the engine's oracle assets, so the *oracle*, not the agent's authored tests, could detect the mutant. A clean checkout of the solution commit carries the agent's tests and no oracle, so only the authored tests judge it.
- **Engine oracle assets are never materialised into the scratch.** For a scratch-mode check the assets land in a separate **tool directory** (also engine-owned, ephemeral), and the argv runs with `cwd` = that tool dir. The scratch holds only the agent's solution.
- **The scratch path is passed to the command via `ORACLE_SCRATCH`** in its environment; the helper mutates files under `$ORACLE_SCRATCH` and runs the agent's tests there.
- **The engine owns and removes both the scratch root and the tool dir on every exit path, including timeout.** A killed helper cannot leak worktrees — cleanup is the engine's, registered with the same guarantee as materialised assets, not the helper's own `defer`.
- **A mutant must compile before a test failure counts as detection.** The helper runs `go build` in the scratch before `go test`; a non-compiling mutant is a broken check, reported as such, never scored as detection. This is the exact bug the shell medium kept reintroducing, trivial to get right in readable Go.

### Shared verification entrypoint (`engine/`, `cmd/runner/`)

Both the engine's own attempt flow and the achievability script must run checks through **one** production path — otherwise the achievability script re-implements check execution and we are back to a facsimile, which is what step 5 exists to prevent.

**Exported seam** — concrete signature, no hedging:

```go
// VerifyResult is the per-item outcome of running a story's validators and
// checks against a prepared solution. Reuses the existing result type.
type VerifyResult struct {
    Validators []runrecord.CheckResult
    // ValidatorOutputs holds each validator's FULL captured output, aligned
    // index-for-index with Validators. This preserves the existing evidence
    // contract: the engine writes these to EvidenceDir as `test-output`
    // evidence pointers (writeValidatorEvidence today), which evidenceCoverage
    // then requires for any story declaring evidence_shape = ["test-output"].
    // Verify must therefore surface the full outputs, not just the truncated
    // CheckResult.Detail — dropping them would make evidence coverage fail on
    // migration. `runner verify` ignores this field.
    ValidatorOutputs []string
    Checks           []runrecord.CheckResult
    OK               bool // true iff every validator and check passed
}

// Verify runs a story's validators then its checks against the bound solution
// at boundDir. It needs the Loaded object (for the retained oracle bytes) and
// both commits (files_changed_within diffs pin..solution). This is the single
// implementation the engine's attempt flow also calls.
func Verify(ctx context.Context, boundDir string, loaded *story.Loaded, pin, solution string) VerifyResult
```

The engine's attempt flow calls `Verify` where it today runs validators and checks inline, then feeds `result.Validators`/`result.ValidatorOutputs` to `writeValidatorEvidence` exactly as now — so run records and evidence coverage are unchanged. There is exactly one executor.

**`runner verify` subcommand** — concrete semantics:

- Usage: `runner verify --story <file.toml> --workspace <dir>`.
- Loads the one story. Derives `solution = git -C <dir> rev-parse HEAD` and validates it resolves; `pin = loaded.Definition.Fixture.Commit`.
- Calls `Verify(ctx, <dir>, loaded, pin, solution)`.
- Prints each validator and check result (name, pass/fail, detail).
- Exits **0** iff `VerifyResult.OK`, non-zero otherwise.

The achievability script drops its own check loop, checks out the fixture, lets the agent produce a solution, commits it, and calls `runner verify`. It then exercises the exact materialiser, argv executor, scratch handling, cleanup, and oracle semantics the engine uses — no second copy to drift.

`cmd/runner` and its tests are **in scope for this item** (the current runner has selection-contract tests but none for `verify`).

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

**Unchanged:** run records, the manifest, adapters, budget accounting, verdict composition, **the evidence contract** (validators' full outputs are still persisted as `test-output` evidence — the verifier seam carries `ValidatorOutputs` precisely so migrating the attempt flow to it changes nothing observable), and the entire v1 schema and every v1 story's hash. The change is confined to how a v2 story declares an acceptance check and how the engine executes that one new type, plus the exported verifier seam. That containment — not an absence of contract change — is what keeps this a bounded item rather than a framework project.
