+++
title = "Design: Benchmark Runner Module Contracts (Item 1)"
edit_date = "2026-07-16"
status = "live"
summary = "Design sketch for the runner-skeleton work item: module layout, the run-record and four-state metric contracts, story and MPH bundle schemas, the results store, the adapter interface with its engine/adapter division of labor, and build wiring."
type = "design"
+++

# Design: Benchmark Runner Module Contracts (Item 1)

Status: live — approved with the item 1 implementation (PR #261, Codex + DR, 2026-07-16; two Codex rounds incorporated). Mini-plan for Phase 1 item 1 (`runner-skeleton`), added by agreement before implementation because this item fixes the contracts items 3–8 consume. Binding sources: [ADR 0025](../../adr/0025-golden-stories-and-benchmark-runner.md), the [Phase 1 plan](plan_scope.md) and its ratified delegated decisions (standalone `benchmark/` module, TOML authored / JSON emitted, content-hash identity).

## Module

- Path `benchmark/`, its own Go module `github.com/SnapdragonPartners/maestro/benchmark`, go 1.26. It never imports the `orchestrator` module — black-box structurally.
- One external dependency: `github.com/BurntSushi/toml` (strict decoding; unknown keys in authored files are rejected, catching typos at load time).
- Housekeeping note: an untracked stale binary named `benchmark` (a local April build of v1's `cmd/benchmark`) occupied the directory name at repo root; it has been moved aside.

## Package Layout

| Package | Responsibility |
|---|---|
| `runrecord` | The normalized run-record contract: four-state metrics, metric key registry, verdicts, failure kinds, target descriptor, MPH identity, evidence pointers, isolation record. Pure types + validation; no I/O. |
| `story` | Golden story definitions: TOML schema, loader, validation, content hash. |
| `mph` | MPH configuration bundles: TOML schema, loader, validation, content-hash identity. |
| `results` | The self-contained results store: append-only, schema-versioned JSONL. |
| `target` | The `Adapter` interface, `AttemptSpec`, `Observation`. |
| `target/faketarget` | Scripted fake adapter, exported for this module's tests and the item 3 engine tests. |
| `internal/contenthash` | `sha256:<hex>` hashing helper shared by `story` and `mph`. |
| `stories/`, `configs/` | Authored fixtures (data, not code). Placeholder READMEs now; content lands in items 2, 4, and 8. |

The engine (item 3), CLI, and reporting (item 7) get their own packages later; nothing here presumes their internals.

## The Run-Record Contract (`runrecord`)

**Metric statuses.** `Metric{Status, Value *float64, Reason}` with status `value` | `unsupported` | `not_applicable` | `unavailable` — four states per ADR 0025 as amended 2026-07-16 (Codex P1 here drove the amendment): `unavailable` means the target supports the metric but it could not be collected on this attempt (target crash, truncated logs), with an optional reason. This is what lets a target-error attempt still produce a *valid failed* record without lying `unsupported`. `Value` is a pointer so a measured zero survives JSON round-trips (`omitempty` can never eat it). Validation enforces status/value coherence both ways.

**Completeness rule.** ADR 0025 says missing is never zero. This design makes it *missing is never missing*: a record's metrics map must contain **every** key in the registry, each explicitly one of the four statuses. An adapter that forgets a metric fails validation instead of silently narrowing comparisons. (Aggregation semantics for `unavailable` — excluded from numeric spreads, counted visibly — are item 7's concern.)

**Metric key registry** (from the ADR's numeric per-run metrics): `tokens_total`, `cost_usd`, `wall_clock_seconds`, `llm_calls`, `tool_calls`, `iterations`, `review_cycles`, `self_repair_cycles`, `human_interventions`, `human_attention_seconds`. All values are `float64` for one uniform aggregation path in item 7, with validation requiring finite, nonnegative values, and integral values for the count-kind keys (everything except `cost_usd`, `wall_clock_seconds`, `human_attention_seconds`).

**Verdicts and failure kinds.** `accepted` | `failed` | `invalid` (invalid = isolation/cleanup unverifiable, excluded from aggregation, per the ADR). Failed records carry exactly one failure kind: `budget-overrun`, `checks-failed`, `validator-failed`, `evidence-missing`, `branch-state`, `target-error`. Invalid records carry a reason string. Validation enforces the pairing.

**Record shape.** `record_schema_version`, `run_id`, `suite_run_id`, story id + story content hash, config name + config hash, target descriptor (adapter name/version, target commit, binary/image identity, declared capabilities, MPH identity: model, prompt pack, prompt hash, harness hash, maestro version), started/finished timestamps, verdict (+ failure kind / invalid reason), per-check results, the complete metrics map, raw evidence pointers (`kind` + `location` into whatever the target exposes), and an isolation block (workspace, branch namespace, `cleanup_verified`).

## Story Schema (`story`)

TOML, one file per story, `schema_version = 1`, strict keys. Sections: identity (`id` kebab-case, `title`, `level` feature|epic|story), `[fixture]` (repo URL, **full 40-hex pinned commit**, base branch), `[prompt]` (text), `[expectations]` (allowed paths, required artifacts, evidence shape), `[[validators]]` (name + command — **engine-executed** in the isolated workspace, Codex P1: the target-independent acceptance boundary must not rest on target self-reporting), `[[checks]]` (deterministic pass/fail), `[budget]`, optional `[[rubrics]]` (recorded, never gating).

**Check types (proposal — minimal, extended only by schema-version bump):** `command` (run in workspace, exit 0 = pass), `files_changed_within` (diff confined to `expectations.allowed_paths`), `file_contains` (path + substring). Execution semantics are item 3; item 1 fixes shape and validation only.

**Budget:** three explicit required caps — `max_tokens`, `max_wall_clock_seconds`, `max_cost_usd`. Declared, not discovered; integers/floats, no duration strings.

**Story identity** = `sha256:` hash of the canonical JSON serialization of the validated definition (same canonicalization as bundles), recorded in every run record so a story edit is visible across runs.

## MPH Bundle Schema (`mph`)

TOML, one bundle per file in `configs/`, strict keys:

- `[model]` — `default` routing plus per-role overrides (`roles.reviewer = ...`), making ADR 0020 reviewer heterogeneity a config fact.
- `[prompt]` — pack label + content hash. **Hash may be omitted for embedded-prompt targets** (v1): the adapter computes it from actual prompt content (the Codex P1 resolution) and records it in the MPH identity; a bundle-declared hash wins when present.
- `[harness]` — `adapter` (which target adapter this bundle drives — adapter selection is a harness lever) plus adapter-interpreted string settings. The runner never interprets these; black-box.
- `[budget]` — declared expectations and caps: `expected_tokens_per_run`, `expected_cost_usd_per_run`, `max_cost_usd_per_run`, `max_cost_usd_per_suite` (caps ≥ expected, enforced at load).

**Bundle identity** = `sha256:` hash of the **canonical JSON serialization of the validated semantic bundle** (sorted keys, no insignificant whitespace), not of raw TOML bytes (Codex P1): comments, formatting, and serialization order are not identity, and the same bundle re-materialized from the Phase 2 data plane reproduces the same hash — content, never location, per the plan's ratified decision. Story identity uses the same canonicalization for the same transition reason.

## Results Store (`results`)

A directory of `<suite_run_id>.jsonl` files; one JSON run record per line; files opened `O_APPEND|O_CREATE`, never truncated. Every record self-describes with `record_schema_version`; the reader validates each record and rejects unknown versions loudly. Zero dependency on the Phase 2 data plane; record shapes are designed for later import as `benchmark`-scoped artifacts (ADR 0025).

## Adapter Interface (`target`)

```go
type Adapter interface {
    Identity() Identity                // name, version
    Capabilities() Capabilities        // which metric keys this target can report
    Run(ctx, AttemptSpec) (*Observation, error)
    Cleanup(ctx, AttemptSpec) error    // engine records loud failures; unverifiable => invalid run
}
```

- `AttemptSpec`: run/suite IDs, story + story hash, bundle + bundle hash, engine-provided fresh workspace dir and run-scoped branch namespace, effective budget. The **engine** (item 3) owns isolation, budget enforcement, **validator execution**, deterministic checks, verdict composition, and record assembly; the **adapter** owns target invocation, observation, and normalization. Adapters never write records and never report validator outcomes (Codex P1).
- `Observation`: target descriptor, complete metrics map, raw evidence pointers, and the target-specific observable facts only — whether the expected branch/PR terminal state was reached.
- `faketarget.Fake` returns scripted observations and records calls — the unit-test seam, no tokens spent.

## Build Wiring

New make targets `benchmark-build`, `benchmark-test`, `benchmark-lint` (root Go walkers don't descend into nested modules), wired as prerequisites of `build`, `test`, and `lint` — so pre-commit hooks and CI cover the module with no workflow changes. Same pinned golangci-lint and root `.golangci.yaml` (config discovery walks up). The v1 `benchmark` target (builds `cmd/benchmark`) is untouched and dies with its package.

## Testing (Item 1 Scope)

Unit tests only, no execution, no tokens: schema load/validation happy and error paths (unknown keys, bad commit pins, budget coherence), metric status round-trips including measured zero, record validation pairings, store append/read round-trip and unknown-version rejection, fake-adapter contract. Real-execution tests arrive with item 3 behind tags.

## Explicitly Deferred

Engine semantics and repeat orchestration (item 3), v1 observation sources (item 4), report aggregation math (item 7), CLI surface (item 3), rubric evaluation (post-MVP per ADR 0025), any data-plane awareness (Phase 2).

## Review Questions — Resolutions

Codex reviewed 2026-07-16 (three P1s, all incorporated above); DR confirmation rides on item 1's review.

1. Completeness rule: **agreed**, contingent on representable collection failure — resolved by the `unavailable` status (P1).
2. Validators: **engine-executed**, not adapter-reported (P1); adapters report only target-specific observable facts.
3. `float64` for all metrics: **accepted** with validation requiring finite, nonnegative values and integral values for count-kind keys.
4. Three check types: **sufficient** for the first ladder rungs; extension requires a schema-version bump.
