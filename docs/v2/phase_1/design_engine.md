+++
title = "Design: Runner Engine And CLI (Item 3)"
edit_date = "2026-07-16"
status = "draft"
summary = "Design sketch for the runner-core work item: attempt lifecycle, verdict composition, budget enforcement split (wall-clock hard, cost post-hoc), isolation and cleanup verification, N-repeat and suite orchestration, the CLI surface, and the one contract addition (Observation.SolutionBranch)."
+++

# Design: Runner Engine And CLI (Item 3)

Status: draft — mini-plan for Phase 1 item 3 (`runner-core`), the checkpoint before implementation, per the item 1 precedent. Binding sources: [ADR 0025](../../adr/0025-golden-stories-and-benchmark-runner.md), the [Phase 1 plan](plan_scope.md), [design_runner.md](design_runner.md) (the contracts this engine executes), and [process_fixtures.md](process_fixtures.md).

## Packages

| Package | Responsibility |
|---|---|
| `engine` | The execution engine: attempt lifecycle, isolation, budget enforcement, validator/check execution, verdict composition, record assembly, N-repeat and suite orchestration, cleanup verification. |
| `cmd/runner` | The CLI binary (`bin/runner`; `make run` becomes real). |

Adapters register by name (`map[string]target.Adapter` supplied by the CLI wiring); `harness.adapter` in the MPH bundle selects one. Item 3 ships only `faketarget` plus a local **stub target** used by integration tests; items 4 and 8 add the real ones.

## Attempt Lifecycle

1. **Isolate.** Create a fresh workspace under the runner's workdir, keyed by run ID; `git clone` the fixture and check out the story's pinned commit; verify `HEAD` equals the pin (mismatch → the attempt is `invalid`, never silently continued). Run IDs are `<story-id>--<config-name>--r<N>--<suffix>` (lowercase, filename-safe, unique per attempt); the branch namespace is `golden/<run-id>/` — every ref the target creates must live under it.
2. **Run the target.** `adapter.Run(ctx, spec)` with a context deadline from the effective wall-clock budget. The adapter returns a normalized `Observation`.
3. **Check out the solution.** New contract field — `Observation.SolutionBranch`: the branch (inside the namespace) holding the target's result. Empty means the workspace tree itself is the solution (adapters that work in place). The engine checks the solution out into the workspace for verification.
4. **Verify.** Engine-executed, in the solution workspace: the story's validators (commands), then its checks (`command`, `files_changed_within` via `git diff --name-only <pin>..HEAD`, `file_contains`). Expectations: `evidence_shape` and `required_artifacts` must each be covered by the observation's evidence kinds.
5. **Compose the verdict** (below), assemble the `RunRecord` (spec + observation + engine results + timestamps), append to the results store.
6. **Clean up.** `adapter.Cleanup`, then engine verification: no refs remain under the branch namespace on the fixture remote (`git ls-remote`), workspace removed. Unverifiable cleanup → the record is (re)marked `invalid` with the reason — loud, per ADR 0025.

## Budget Enforcement Split

Declared budgets have two enforcement modes, stated explicitly because a generic engine cannot see tokens mid-run:

- **Wall-clock: hard, engine-enforced.** Context deadline; on expiry the attempt is aborted and recorded `failed`/`budget-overrun`.
- **Tokens and cost: post-hoc, engine-checked.** The effective caps ride in the `AttemptSpec`; adapters are expected to self-limit where their target allows, and the engine compares observed `tokens_total`/`cost_usd` against the caps after the run — overrun → `failed`/`budget-overrun`, costs still counted (ADR 0025).
- **Effective budget** = the story's caps bounded by the bundle's `max_cost_usd_per_run`. The suite carries `max_cost_usd_per_suite`: when accumulated observed cost crosses it, remaining attempts are not started and the suite report says so — partial is reported as partial, never truncated into a fake pass.

## Verdict Composition

`invalid` (isolation or cleanup unverifiable, with reason) takes precedence over everything. Otherwise `failed` with exactly one kind, first match in this order: `budget-overrun` → `target-error` (adapter returned an error) → `branch-state` (terminal state not reached) → `validator-failed` → `checks-failed` → `evidence-missing`. Otherwise `accepted` — which by the record contract requires every validator and check passed, terminal state reached, and verified cleanup.

## Suite Orchestration

`stories × configs × N` repeats, sequential in item 3 (parallelism is a later optimization, not a contract change). Each attempt is fully isolated; no repeat inherits state (ADR 0025). Smoke mode is just N=1. The suite run ID names the results file; the D9 numbers (N, caps) are wired as flags now and fixed by item 6's instrumented runs.

## CLI Surface (`cmd/runner`)

- `runner validate` — load stories + bundles, print what would run (no execution, no cost).
- `runner run --stories <dir|file> --configs <dir|file> [--story <id>] [--config <name>] --repeats N --results <dir> --workdir <dir> [--suite-id <id>]` — execute; one human summary line per attempt; records to the store.
- `runner list --results <dir>` — enumerate suite runs in a results store.
Reports with spread are item 7; the CLI prints only per-attempt outcomes.

## Testing

- **Unit** (faketarget): verdict composition table, budget math, record assembly, evidence coverage, solution-checkout handling.
- **Integration, hermetic:** fixtures are **local bare repos created in `t.TempDir()`** (`file://` remotes) — no network, no GitHub, no tokens. The stub target commits a scripted solution onto a namespace branch; tests cover isolation (two repeats share nothing), pin-mismatch invalidation, wall-clock abort, cleanup verification including a deliberately-left-behind ref flagging the run invalid. These run under the ordinary test tag — they need only git and the filesystem.

## Explicitly Deferred

v1 invocation and observation (item 4), cost instrumentation and D9 numbers (item 6), spread reports (item 7), parallel attempts, any data-plane writes (Phase 2).

## Review Questions

1. **`Observation.SolutionBranch`** as the one contract addition (empty = in-place workspace solution). Concur?
2. **Budget split** — wall-clock hard via context, tokens/cost post-hoc with adapter self-limiting expected. Any objection to post-hoc as the Phase 1 posture?
3. **Failure-kind precedence** order above. Agree `target-error` outranks `branch-state`?
4. **Hermetic integration tests** via local `file://` fixtures under the normal test tag (no `integration` tag needed — no keys, no network). OK?
