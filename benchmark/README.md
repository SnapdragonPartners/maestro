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
- `target/` — the `Adapter` interface (`Describe`/`Run`/`Cleanup`), `AttemptSpec`, `Observation`; `faketarget/` is the scripted in-memory test adapter, `stubtarget/` the scripted real-git test adapter, and `v1target/` the real **v1-as-patched** adapter: per-run Gitea forge isolation, subprocess invocation with DB polling, post-hoc metric normalization (streamed after item 5's P-1 patch), durable evidence export, and audited prompt-content MPH identity (design_adapter_v1.md). Its `modernc.org/sqlite` import is an adapter-scoped v1-compatibility dependency, removed when the v1 adapter retires.
- `engine/` — the execution engine: attempt lifecycle, isolation, budget enforcement, engine-executed validators/checks, verdict composition, cleanup verification, suite orchestration (design_engine.md).
- `cmd/runner/` — the CLI (`bin/runner`): `validate`, `run`, `list`.
- `internal/contenthash/` — canonical `sha256:` identity helper; `internal/gitx/` — git CLI wrapper.
- `stories/` — authored golden stories.
- `configs/` — authored MPH bundles (land with their adapters, items 4 and 8).

Comparison reports with spread are item 7 and get their own package here.

## Usage

Build the runner and the target binary, then validate and run:

```bash
# From the repo root: build the v1 target binary the adapter launches
make maestro

# In benchmark/: build the runner and validate all stories × configs
make build                      # produces bin/runner
bin/runner validate             # loads stories/ and configs/, prints hashes

# Execute one story under one config (spends real tokens — see budgets
# in the story/config TOMLs; the suite cap is enforced conservatively)
ANTHROPIC_API_KEY=... bin/runner run \
  --story smoke-comment \
  --config paired-default \
  --suite-id my-suite-001 \
  --results results \
  --workdir /tmp/bench-work

# Repeat attempts, or run the full stories × configs matrix
ANTHROPIC_API_KEY=... bin/runner run --repeats 3 --suite-id my-suite-002

# Inspect stored results
bin/runner list --results results
```

`run` writes one JSONL run record per attempt plus a rewritable suite
manifest under `--results`, and durable evidence (diff, PR metadata, DB
snapshot, usage log, launch log, validator output) under
`results/evidence/<run-id>/`. `--keep-infra` leaves the adapter's Gitea
container running between suites to skip its startup cost; omit it to
tear down. The adapter requires the target binary to advertise
`usage-surface: v1` in `-version` output (the P-1 handshake) and fails
runs whose usage log never validates.

## Local Models (paired-local)

The `paired-local` configuration (item 5.1, [#266](https://github.com/SnapdragonPartners/maestro/issues/266)) drives v1's factory with locally-hosted **Ollama** models instead of hosted APIs, making basic end-to-end exercise of the harness ~free — cost is `unavailable` (unmodeled), while tokens/calls are still measured. Local models are weaker and slower; the trade is wall-clock time for near-zero dollar cost on simple testing tasks.

Not every open model works in every seat. Tested against the `smoke-comment` story (a one-line append), the results:

| Model | as **Coder** | as **Architect** |
|---|---|---|
| `qwen3-coder:30b` | ✅ lands the edit (methodical shell/file ops) | ✅ structured reviews work |
| `gpt-oss:20b` | not tested | ✅ structured reviews work |
| `mistral-small3.2:24b` | ❌ invents the `file_edit` param schema (`find`/`replacement` vs. `old_string`/`new_string`) — the edit never lands | ❌ fails to emit the terminal review tool even after the nudge → **fatal architect shutdown** |

**Recommended:** coder = `qwen3-coder:30b`, architect = `gpt-oss:20b` — a clean end-to-end `accepted` run on the smoke story at **$0 hosted-API spend (total cost unmodeled — recorded `unavailable`) / ~8.7 min** wall clock. `qwen3-coder` is also reliable in the architect seat, so `qwen3-coder` for both roles is a valid single-model fallback. **`mistral-small` is not recommended** in either seat: it is unreliable at structured tool-calling (wrong parameter schemas as coder, missing terminal-tool calls as architect). The recurring theme is **tool-calling fidelity**, not task comprehension — the plans were correct; execution failed on schema adherence.

**Routing:** the provider is inferred from the model-name prefix (`pkg/config/config.go` `ProviderPatterns`). `qwen*`/`mistral*`/`llama*`/`phi*`/`deepseek*` route to Ollama automatically. `gpt-oss` needed an explicit routing entry — its `gpt` prefix otherwise routes it to the hosted OpenAI API — now fixed so `gpt-oss:*` routes to Ollama. The Ollama endpoint comes from `OLLAMA_HOST` (default `http://localhost:11434`).

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
