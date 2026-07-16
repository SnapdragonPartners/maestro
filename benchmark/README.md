+++
title = "Benchmark Runner Module"
edit_date = "2026-07-16"
status = "live"
summary = "The golden story benchmark runner (ADR 0025): a standalone Go module that drives benchmark targets black-box and owns its results store. Never imports the orchestrator module."
+++

# Benchmark Runner Module

The measuring instrument of Maestro v2 (Phase 1): golden story definitions,
MPH configuration bundles, the normalized run-record contract, the
self-contained results store, and the per-target adapter interface.
Specification: [ADR 0025](../docs/adr/0025-golden-stories-and-benchmark-runner.md);
design: [design_runner.md](../docs/v2/phase_1/design_runner.md).

## Black-Box Rule

This is a **standalone Go module** (`github.com/SnapdragonPartners/maestro/benchmark`).
It must never import the `orchestrator` module: the runner drives its
targets only through external surfaces (config, CLI/API invocation,
branches, PRs, artifacts, metrics), which is what lets one runner benchmark
v1-as-patched today, v2 as it comes up, and harnesses that do not exist
yet. Adding an `orchestrator` dependency to `go.mod` is a defect by
definition.

## Layout

- `story/` — golden story definitions: TOML schema, strict loader, validation, canonical content identity.
- `mph/` — MPH configuration bundles: TOML schema, loader, content-hash identity.
- `runrecord/` — the normalized run-record contract: four-state metrics, registry, verdicts, failure kinds, target descriptor. `recordtest/` builds valid records for tests.
- `results/` — append-only, schema-versioned JSONL results store, plus the rewritable suite manifest.
- `target/` — the `Adapter` interface (`Describe`/`Run`/`Cleanup`), `AttemptSpec`, `Observation`; `faketarget/` is the scripted in-memory test adapter, `stubtarget/` the scripted real-git test adapter.
- `engine/` — the execution engine: attempt lifecycle, isolation, budget enforcement, engine-executed validators/checks, verdict composition, cleanup verification, suite orchestration (design_engine.md).
- `cmd/runner/` — the CLI (`bin/runner`): `validate`, `run`, `list`.
- `internal/contenthash/` — canonical `sha256:` identity helper; `internal/gitx/` — git CLI wrapper.
- `stories/` — authored golden stories.
- `configs/` — authored MPH bundles (land with their adapters, items 4 and 8).

Comparison reports with spread are item 7 and get their own package here.

## Development

In this directory: `make build`, `make test`, `make test-race`,
`make lint`, `make run` (validates the authored stories and configs —
executing a suite spends real tokens, so it stays an explicit
`bin/runner run ...` invocation). From the repo root,
`make benchmark-build`/`benchmark-test`/`benchmark-lint` delegate here and
run as prerequisites of the root `build`, `test`, and `lint` targets, so
hooks and CI cover this module automatically. Tests spend no tokens and
touch no network: engine integration tests run against local bare git
fixtures with a scripted stub target.
