+++
title = "Design: Runner Engine And CLI (Item 3)"
edit_date = "2026-07-16"
status = "draft"
summary = "Design sketch for the runner-core work item: attempt lifecycle (cleanup before the append-only record), pre-run target description with error-path metric synthesis, immutable solution binding, streamed budget enforcement with conservative suite admission, the suite manifest, and the CLI surface."
+++

# Design: Runner Engine And CLI (Item 3)

Status: draft — mini-plan for Phase 1 item 3 (`runner-core`), the checkpoint before implementation, per the item 1 precedent; revised for Codex round 1 (five P1s). Binding sources: [ADR 0025](../../adr/0025-golden-stories-and-benchmark-runner.md), the [Phase 1 plan](plan_scope.md), [design_runner.md](design_runner.md) (the contracts this engine executes), and [process_fixtures.md](process_fixtures.md).

## Packages

| Package | Responsibility |
|---|---|
| `engine` | The execution engine: attempt lifecycle, isolation, budget enforcement, validator/check execution, verdict composition, record assembly, N-repeat and suite orchestration, cleanup verification, suite manifest. |
| `cmd/runner` | The CLI binary (`bin/runner`; `make run` becomes real). |

Adapters register by name (`map[string]target.Adapter` supplied by the CLI wiring); `harness.adapter` in the MPH bundle selects one. Item 3 ships only `faketarget` plus a local **stub target** used by integration tests; items 4 and 8 add the real ones.

## Contract Amendments (Codex round 1)

Made now, before any adapter beyond the fake exists; no persisted records exist anywhere, so the record schema evolves in place at version 1.

- **`Adapter.Describe(ctx, spec) (runrecord.TargetDescriptor, error)`** — called *before* `Run`. The descriptor (commit, binary identity, MPH identity including the content-derived prompt hash) is knowable pre-execution and must not depend on the run surviving; it single-sources what `Observation.Target` used to carry, so **`Observation` drops `Target`** and observation capability-coherence validates against `Adapter.Capabilities()`.
- **`Observation.SolutionBranch`** — the ref (inside the run namespace) holding the target's result; empty means the solution is the workspace tree (in-place adapters).
- **Usage streaming**: `AttemptSpec.ReportUsage func(UsageDelta{Tokens, CostUSD})` — an engine-provided callback adapters invoke as usage accrues; the engine cancels the attempt the moment a cap is crossed (hard abort, `budget-overrun`). Adapters that cannot stream declare it: the descriptor gains **`budget_enforcement`**: `streamed` | `self-enforced` | `post-hoc`, so every record states how its target's caps were actually enforced.
- **`RunRecord.SolutionCommit`** — the resolved, validated solution commit (below).

## Attempt Lifecycle

1. **Describe.** `adapter.Describe` produces the target descriptor. Failure here is a `failed`/`target-error` record with synthesized metrics (below) — never a crash, never a missing record.
2. **Isolate.** Fresh workspace under the runner's workdir keyed by run ID (`<story-id>--<config-name>--r<N>--<suffix>`, lowercase, filename-safe); `git clone` the fixture, check out the story's pinned commit, verify `HEAD` equals the pin (mismatch → `invalid`). Branch namespace `golden/<run-id>/` — every ref the target creates must live under it.
3. **Run.** `adapter.Run(ctx, spec)` with a context deadline from the effective wall-clock budget and cancellation wired to streamed-usage overrun.
4. **Bind the solution immutably.** A branch name is mutable and proves nothing. The engine requires `SolutionBranch` to be inside the run namespace, resolves it **once** to a commit, verifies the story pin is an ancestor of that commit, and validates at a detached checkout of it. For in-place solutions the engine snapshots the working tree as a synthetic commit first (uncommitted changes are otherwise invisible to `<pin>..HEAD`). The resolved commit is recorded as `SolutionCommit`.
5. **Verify.** Engine-executed at the solution commit: the story's validators (commands), then checks (`command`, `files_changed_within` via `git diff --name-only <pin>..<solution-commit>`, `file_contains`); `evidence_shape` and `required_artifacts` must each be covered by the observation's evidence kinds.
6. **Clean up — before the record.** `adapter.Cleanup` plus engine verification (no refs left under the namespace via `git ls-remote`, workspace removed) run on **every exit path**, under a **fresh bounded context** — never the attempt context, or a wall-clock overrun would masquerade as a cleanup failure. The store is append-only: nothing is appended until cleanup verification has a result.
7. **Compose the verdict and append once.** Spec + descriptor + observation + engine results + timestamps → one `RunRecord`, one append.

**Error-path record synthesis.** When `Run` returns an error (including deadline cancellation) or a contract-invalid observation, the engine still produces a complete, valid record: descriptor from step 1; metrics synthesized as `unavailable(reason)` for every capability-declared key and `unsupported` for the rest; verdict `failed` with `budget-overrun` (deadline/usage abort) or `target-error`; evidence whatever the adapter managed to report, else empty.

## Budget Enforcement

- **Wall-clock: hard.** Context deadline; expiry aborts → `budget-overrun`.
- **Tokens/cost: hard where streamable.** Streamed usage triggers engine cancellation at the cap — this satisfies ADR 0025's overrun-aborts rule for `streamed` targets; `self-enforced` targets enforce their own declared caps; `post-hoc` targets get the end-of-run comparison, and the record's `budget_enforcement` field says so honestly. No silent gap: enforcement capability is part of every record.
- **Effective budget** = story caps bounded by the bundle's `max_cost_usd_per_run`.
- **Conservative suite admission.** An attempt is not launched unless `spent + expected_cost_usd_per_run ≤ max_cost_usd_per_suite` — admission charges the *declared expectation up front*, so the suite cannot overshoot by launching. Attempts whose cost comes back `unavailable`/`unsupported` stay charged at the declared expectation (never zero).

## Suite Manifest

A partial suite must be distinguishable from a corrupt results file. Alongside `<suite-id>.jsonl` the store keeps `<suite-id>.manifest.json` — rewritten (not appended) as the suite progresses: the planned matrix (story × config × repeat), per-attempt status (`completed` with run ID / `skipped` with reason), budget accounting (cap, charged, observed), and the stop reason (`completed`, `suite-budget-exhausted`, `interrupted`). Item 7's reports consume it; "partial is reported as partial" becomes a recorded fact, not an inference.

## Suite Orchestration

`stories × configs × N` repeats, sequential in item 3 (parallelism is a later optimization, not a contract change). Each attempt fully isolated. Smoke mode is N=1. D9 numbers (N, caps) are flags now, fixed by item 6.

## CLI Surface (`cmd/runner`)

- `runner validate` — load stories + bundles, print what would run (no execution, no cost).
- `runner run --stories <dir|file> --configs <dir|file> [--story <id>] [--config <name>] --repeats N --results <dir> --workdir <dir> [--suite-id <id>]` — execute; one human summary line per attempt; records + manifest to the store.
- `runner list --results <dir>` — enumerate suite runs with their manifest status.
Reports with spread are item 7.

## Testing

- **Unit** (faketarget): verdict composition table, budget math and admission, error-path record synthesis, evidence coverage, manifest transitions.
- **Integration, hermetic** (normal test tag — needs only git and the filesystem): fixtures are local bare repos in `t.TempDir()` (`file://` remotes); a scripted stub target commits solutions onto namespace branches. Covers: two repeats share nothing; pin mismatch → invalid; wall-clock abort → budget-overrun with synthesized metrics; streamed-usage abort; solution-ancestry rejection (branch not descending from pin); left-behind ref → invalid; in-place snapshot commit; suite-cap admission stopping the matrix with a manifest that says why.

## Explicitly Deferred

v1 invocation and observation (item 4), cost instrumentation and D9 numbers (item 6), spread reports (item 7), parallel attempts, any data-plane writes (Phase 2).

## Review Questions — Resolutions

Codex round 1 (2026-07-16); all five P1s incorporated above.

1. `SolutionBranch`: **directionally agreed**, hardened to immutable commit resolution with ancestry verification and recorded `SolutionCommit` (P1).
2. Budget posture: **post-hoc-only rejected** as conflicting with ADR 0025's hard caps (P1); resolved by the usage-streaming/cancellation contract, per-target `budget_enforcement` declaration, and conservative suite admission.
3. Failure-kind precedence: **agreed** (`target-error` outranks `branch-state`).
4. Hermetic local-git integration tests under the normal test tag: **agreed**.
