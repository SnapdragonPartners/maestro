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
- `results/` — the results-store **package**: append-only, schema-versioned JSONL records, plus the rewritable suite manifest.
- `runs/` — the results-store **data**, and the runner's `--results` default: `<suite-id>.jsonl`, `<suite-id>.manifest.json`, and per-run evidence under `runs/evidence/<run-id>/` (logs, `usage.jsonl`, SQLite). **Durable-on-disk but git-ignored** (`benchmark/.gitignore`): kept across sessions so instrumented-run measurements survive, but never committed — it holds transcripts/DBs and is reproducible by re-running. Distilled outputs (e.g. the item-6 D9 budget policy) land in version control instead. **Look here first for run evidence.** Kept a sibling of `results/` so runtime output never comingles with package source.
- `target/` — the `Adapter` interface (`Describe`/`Run`/`Cleanup`), `AttemptSpec`, `Observation`; `faketarget/` is the scripted in-memory test adapter, `stubtarget/` the scripted real-git test adapter, and `v1target/` the real **v1-as-patched** adapter: per-run Gitea forge isolation, subprocess invocation with DB polling, post-hoc metric normalization (streamed after item 5's P-1 patch), durable evidence export, and audited prompt-content MPH identity (design_adapter_v1.md). Its `modernc.org/sqlite` import is an adapter-scoped v1-compatibility dependency, removed when the v1 adapter retires.
- `engine/` — the execution engine: attempt lifecycle, isolation, budget enforcement, engine-executed validators/checks, verdict composition, cleanup verification, suite orchestration (design_engine.md).
- `cmd/runner/` — the CLI (`bin/runner`): `validate`, `run`, `list`.
- `internal/contenthash/` — canonical `sha256:` identity helper; `internal/gitx/` — git CLI wrapper.
- `stories/` — authored golden stories.
- `configs/` — authored MPH bundles (land with their adapters, items 4 and 8).

Comparison reports with spread are item 7 and get their own package here.

## Run Protocol

**Follow these steps in order, every time.** A benchmark run spends real
money and its value depends entirely on methodology being identical
between runs — an out-of-order or skipped step produces a number that
looks valid and is not comparable to anything.

### 1. Preflight (never skip)

```bash
# From the repo root — REBUILD THE TARGET BINARY AFTER *ANY* CHANGE TO v1.
# The adapter launches bin/maestro as a subprocess; a stale binary silently
# benchmarks old code. This has already cost a full run (discovery-006).
make maestro                    # or: go build -o bin/maestro ./cmd/maestro

# Verify the P-1 capability handshake — the adapter refuses a target
# that does not advertise this, and fails runs whose usage log never validates.
./bin/maestro -version | grep 'usage-surface: v1'

# In benchmark/: build the runner, then confirm identity BEFORE spending.
make build                      # produces bin/runner
bin/runner validate             # loads stories/ + configs/, prints content hashes
```

`validate` is the cheap gate: it proves every story and config parses,
validates, and hashes. Read the printed hashes — they are the run's
identity (see *Comparability* below).

### 2. Credentials

`MAESTRO_ANTHROPIC_API_KEY` is the preferred variable. Maestro's secret
lookup precedence is `MAESTRO_<NAME>` → user secrets → system secrets →
`<NAME>`, so a bare `ANTHROPIC_API_KEY` also works but is overridden by the
prefixed form if both are set. The runner passes its environment through
to the target subprocess. `paired-local` needs no key (see *Local Models*).

### 3. Execute

```bash
# One story under one config. Results default to runs/ — no --results needed.
bin/runner run \
  --story smoke-comment \
  --config paired-default \
  --suite-id <purpose>-<subject>   # e.g. cal-d9-stage1-bugfix

# N repeats (the D9 sampling dimension), or the full stories × configs matrix
bin/runner run --repeats 3 --suite-id <purpose>-002

# Inspect stored results
bin/runner list
```

Conventions that keep the store readable: **one suite-id per purpose**,
named `<purpose>-<subject>` so the results directory self-documents; and
let `--results` default to `runs/` so evidence always lands in the durable
store. `--workdir` is scratch and may point anywhere (it is discarded);
`--keep-infra` leaves the adapter's Gitea container up between suites to
skip startup cost.

`run` writes one JSONL run record per attempt plus a rewritable suite
manifest under `--results`, and durable evidence (diff, PR metadata, DB
snapshot, usage log, launch log, validator output) under
`runs/evidence/<run-id>/`.

### 4. Read the results

Metrics use **four-state semantics** — a value, `unsupported`,
`not_applicable`, or `unavailable`. Missing is never zero, so always read
`status`, not just `value`:

```bash
python3 -c "
import json,glob
for f in sorted(glob.glob('runs/*.jsonl')):
    for l in open(f):
        r=json.loads(l); m=r['metrics']
        g=lambda k: m.get(k,{}).get('value', m.get(k,{}).get('status'))
        print(r['story_id'], r['verdict'], r.get('failure_kind') or '',
              g('tokens_total'), g('cost_usd'), g('wall_clock_seconds'), g('llm_calls'))"
```

Keys: `tokens_total`, `cost_usd`, `wall_clock_seconds`, `llm_calls`,
`tool_calls`. A cancelled run (e.g. budget overrun) legitimately reports
`unavailable` for metrics it could not collect.

### 5. Police the run (a verdict alone is not enough)

`accepted` is necessary but not sufficient evidence of a *healthy* run,
and a failure is not automatically a story defect. Before trusting a
number, check the target log in `runs/evidence/<run-id>/logs/maestro.log`:

```bash
ML=runs/evidence/<run-id>/logs/maestro.log
grep -oE "state: [A-Z_]+" "$ML" | uniq -c        # progression, not a loop
grep -niE "state machine failed|FATAL SHUTDOWN|requeue|abandoned" "$ML"
```

Interpret failures by kind before spending again:

- **`budget-overrun`** — the run hit a declared cap. Distinguish *healthy
  but heavy* (orderly `PLANNING → CODING → TESTING` progression, substantive
  review feedback) from *pathological* (a repeating state cycle, repeated
  requeues, abandonment). Only the former justifies raising a cap.
- **`target-error`** — the target died. Check for an architect fatal
  shutdown or an infrastructure race before blaming the story.
- **`branch-state` / invalid** — harness or cleanup problem, not a target result.

Known environment flake: on macOS the **first exec of a freshly built
binary can be SIGKILLed** while the code signature is re-validated,
surfacing as `signal: killed`. Re-run once to confirm before investigating.

### 6. Comparability

Two runs are comparable only when their **`story_hash`, `config_hash`, and
harness identity match** — all three are recorded on every run record.
The story hash covers the *entire* definition including its `[budget]`
block, so changing a cap changes the story's identity by design; caps are
a safeguard, but a calibration series that varies them necessarily spans
several hashes. Fix the caps before collecting a distribution you intend
to compare or publish.

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
