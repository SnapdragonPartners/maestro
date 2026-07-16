+++
title = "Design: Benchmark Runner Module Contracts (Item 1)"
edit_date = "2026-07-16"
status = "draft"
summary = "Design sketch for the runner-skeleton work item: module layout, the run-record and tri-state metric contracts, story and MPH bundle schemas, the results store, the adapter interface with its engine/adapter division of labor, and build wiring."
type = "design"
+++

# Design: Benchmark Runner Module Contracts (Item 1)

Status: draft — mini-plan for Phase 1 item 1 (`runner-skeleton`), added by agreement before implementation because this item fixes the contracts items 3–8 consume. Reviewed like a checkpoint, not a phase gate: Codex + DR pass on the shapes below, then implementation proceeds on the same branch. Binding sources: [ADR 0025](../../adr/0025-golden-stories-and-benchmark-runner.md), the [Phase 1 plan](plan_scope.md) and its ratified delegated decisions (standalone `benchmark/` module, TOML authored / JSON emitted, content-hash identity).

## Module

- Path `benchmark/`, its own Go module `github.com/SnapdragonPartners/maestro/benchmark`, go 1.26. It never imports the `orchestrator` module — black-box structurally.
- One external dependency: `github.com/BurntSushi/toml` (strict decoding; unknown keys in authored files are rejected, catching typos at load time).
- Housekeeping note: an untracked stale binary named `benchmark` (a local April build of v1's `cmd/benchmark`) occupied the directory name at repo root; it has been moved aside.

## Package Layout

| Package | Responsibility |
|---|---|
| `runrecord` | The normalized run-record contract: tri-state metrics, metric key registry, verdicts, failure kinds, target descriptor, MPH identity, evidence pointers, isolation record. Pure types + validation; no I/O. |
| `story` | Golden story definitions: TOML schema, loader, validation, content hash. |
| `mph` | MPH configuration bundles: TOML schema, loader, validation, content-hash identity. |
| `results` | The self-contained results store: append-only, schema-versioned JSONL. |
| `target` | The `Adapter` interface, `AttemptSpec`, `Observation`. |
| `target/faketarget` | Scripted fake adapter, exported for this module's tests and the item 3 engine tests. |
| `internal/contenthash` | `sha256:<hex>` hashing helper shared by `story` and `mph`. |
| `stories/`, `configs/` | Authored fixtures (data, not code). Placeholder READMEs now; content lands in items 2, 4, and 8. |

The engine (item 3), CLI, and reporting (item 7) get their own packages later; nothing here presumes their internals.

## The Run-Record Contract (`runrecord`)

**Tri-state metric.** `Metric{Status, Value *float64}` with status `value` | `unsupported` | `not_applicable`; `Value` is a pointer so a measured zero survives JSON round-trips (`omitempty` can never eat it). Validation enforces status/value coherence both ways.

**Completeness rule (proposal).** ADR 0025 says missing is never zero. This design makes it *missing is never missing*: a record's metrics map must contain **every** key in the registry, each explicitly `value`, `unsupported`, or `not_applicable`. An adapter that forgets a metric fails validation instead of silently narrowing comparisons.

**Metric key registry** (from the ADR's numeric per-run metrics): `tokens_total`, `cost_usd`, `wall_clock_seconds`, `llm_calls`, `tool_calls`, `iterations`, `review_cycles`, `self_repair_cycles`, `human_interventions`, `human_attention_seconds`. All values are `float64` (counts included) for one uniform aggregation path in item 7.

**Verdicts and failure kinds.** `accepted` | `failed` | `invalid` (invalid = isolation/cleanup unverifiable, excluded from aggregation, per the ADR). Failed records carry exactly one failure kind: `budget-overrun`, `checks-failed`, `validator-failed`, `evidence-missing`, `branch-state`, `target-error`. Invalid records carry a reason string. Validation enforces the pairing.

**Record shape.** `record_schema_version`, `run_id`, `suite_run_id`, story id + story content hash, config name + config hash, target descriptor (adapter name/version, target commit, binary/image identity, declared capabilities, MPH identity: model, prompt pack, prompt hash, harness hash, maestro version), started/finished timestamps, verdict (+ failure kind / invalid reason), per-check results, the complete metrics map, raw evidence pointers (`kind` + `location` into whatever the target exposes), and an isolation block (workspace, branch namespace, `cleanup_verified`).

## Story Schema (`story`)

TOML, one file per story, `schema_version = 1`, strict keys. Sections: identity (`id` kebab-case, `title`, `level` feature|epic|story), `[fixture]` (repo URL, **full 40-hex pinned commit**, base branch), `[prompt]` (text), `[expectations]` (allowed paths, validators, required artifacts, evidence shape), `[[checks]]` (deterministic pass/fail), `[budget]`, optional `[[rubrics]]` (recorded, never gating).

**Check types (proposal — minimal, extended only by schema-version bump):** `command` (run in workspace, exit 0 = pass), `files_changed_within` (diff confined to `expectations.allowed_paths`), `file_contains` (path + substring). Execution semantics are item 3; item 1 fixes shape and validation only.

**Budget:** three explicit required caps — `max_tokens`, `max_wall_clock_seconds`, `max_cost_usd`. Declared, not discovered; integers/floats, no duration strings.

**Story identity** = `sha256:` content hash of the definition file bytes, recorded in every run record so a story edit is visible across runs.

## MPH Bundle Schema (`mph`)

TOML, one bundle per file in `configs/`, strict keys:

- `[model]` — `default` routing plus per-role overrides (`roles.reviewer = ...`), making ADR 0020 reviewer heterogeneity a config fact.
- `[prompt]` — pack label + content hash. **Hash may be omitted for embedded-prompt targets** (v1): the adapter computes it from actual prompt content (the Codex P1 resolution) and records it in the MPH identity; a bundle-declared hash wins when present.
- `[harness]` — `adapter` (which target adapter this bundle drives — adapter selection is a harness lever) plus adapter-interpreted string settings. The runner never interprets these; black-box.
- `[budget]` — declared expectations and caps: `expected_tokens_per_run`, `expected_cost_usd_per_run`, `max_cost_usd_per_run`, `max_cost_usd_per_suite` (caps ≥ expected, enforced at load).

**Bundle identity** = `sha256:` hash of file bytes — content, never location, per the plan's ratified decision.

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

- `AttemptSpec`: run/suite IDs, story + story hash, bundle + bundle hash, engine-provided fresh workspace dir and run-scoped branch namespace, effective budget. The **engine** (item 3) owns isolation, budget enforcement, deterministic checks, verdict composition, and record assembly; the **adapter** owns target invocation, observation, and normalization. Adapters never write records.
- `Observation`: target descriptor, complete tri-state metrics map, raw evidence pointers, plus adapter-reported facts the verdict needs — validator results and whether the expected branch/PR terminal state was reached.
- `faketarget.Fake` returns scripted observations and records calls — the unit-test seam, no tokens spent.

## Build Wiring

New make targets `benchmark-build`, `benchmark-test`, `benchmark-lint` (root Go walkers don't descend into nested modules), wired as prerequisites of `build`, `test`, and `lint` — so pre-commit hooks and CI cover the module with no workflow changes. Same pinned golangci-lint and root `.golangci.yaml` (config discovery walks up). The v1 `benchmark` target (builds `cmd/benchmark`) is untouched and dies with its package.

## Testing (Item 1 Scope)

Unit tests only, no execution, no tokens: schema load/validation happy and error paths (unknown keys, bad commit pins, budget coherence), metric tri-state round-trips including measured zero, record validation pairings, store append/read round-trip and unknown-version rejection, fake-adapter contract. Real-execution tests arrive with item 3 behind tags.

## Explicitly Deferred

Engine semantics and repeat orchestration (item 3), v1 observation sources (item 4), report aggregation math (item 7), CLI surface (item 3), rubric evaluation (post-MVP per ADR 0025), any data-plane awareness (Phase 2).

## Review Questions

1. The metrics **completeness rule** — every registry key present in every record, validation-enforced. Agree this is the right hardening of "missing is never zero"?
2. `Observation` carrying adapter-reported **validator results and branch/PR terminal state** as facts the engine composes into the verdict — right division, or should validators be engine-run commands only?
3. **`float64` for all metric values** (counts included) to keep one aggregation path. Acceptable?
4. Check-type set frozen at three (`command`, `files_changed_within`, `file_contains`) until a schema-version bump. Sufficient for the first ladder rungs?
