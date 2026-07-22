+++
title = "Benchmark Runner Module"
edit_date = "2026-07-22"
status = "live"
summary = "The golden story runner (ADR 0025): a standalone Go module that drives targets black-box and owns its results store. Never imports the orchestrator module. Its near-term function is end-to-end conformance — proving the pipeline completes progressively harder stories, repeatably — with economic baselining deferred to Phase 1B."
+++

# Benchmark Runner Module

Golden story definitions, MPH configuration bundles, the normalized
run-record contract, the self-contained results store, and the per-target
adapter interface.
Specification: [ADR 0025](../docs/adr/0025-golden-stories-and-benchmark-runner.md);
design: [design_runner.md](../docs/v2/phase_1/design_runner.md).

**What this is for, near-term.** Primarily an **end-to-end conformance
harness**: does the pipeline complete this story, and does it still do so
after we change something? Phase 1's instrumented runs showed the target does
not reliably run (7 of 11 v1 patches were run-blocking), and measurement
presupposes function — so economic baselining, comparison reporting, and the
single-agent cost comparator defer to **Phase 1B** (after Phase 7, per the
2026-07-22 ADR 0025 amendment). The mechanics below are unchanged with one
exception — the tier/N binding, which that amendment explicitly changes: a
tier names a story set, and N follows the run's purpose (conformance N=1,
comparison N=3 primary / N=1 secondary). Cost and token data keep accruing on
every run, so the trend is there when Phase 1B arrives.

Cadence at every phase end: `golden-minimal` at N = 1 (the harness-is-alive
check, a few dollars), plus — from Phase 2 onward — `golden-all` at N = 1 on
`paired-default` as the conformance proof, which is what the order-of-$50 per
phase budget covers. N = 3 is comparison sampling and belongs to Phase 1B. Each
run appends a row to the committed [conformance log](../docs/v2/notes_conformance-log.md):
a proof that leaves no trace cannot distinguish a regression from a memory. `paired-local` is an
**experiment**, not a gate: it is for cheap tight loops and small-model
capability learning, never a pass requirement at any tier, and a story must
clear `paired-default` before it is attempted locally.

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

Comparison reports with spread are item 7, deferred to **Phase 1B** by the 2026-07-22 proposed amendment; they get their own package here when built.

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

# N follows the run's PURPOSE, not the tier:
#
#   conformance (phase-end, "did each rung still behave")  -> N=1
#   comparison  (D9 sampling, distribution/spread)         -> N=3 primary,
#                                                             N=1 secondary
#
# Conformance — the phase-end proof, N=1 over the full story set:
bin/runner run --config paired-default --suite-id conformance-<phase>
#
# Comparison — Phase 1B. --repeats is SUITE-WIDE, so it must be paired with
# --config; one unfiltered command cannot express the 3/1 split:
bin/runner run --config paired-default --repeats 3 --suite-id <purpose>-primary
bin/runner run --config paired-local   --repeats 1 --suite-id <purpose>-local

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

**Aggregation and comparison are different operations with different rules.**
Conflating them is the easiest way to publish noise:

- **Aggregate** (mean, spread, overrun rate) **only within one complete
  identity group**, including enforcement mode. A distribution pooled across
  identities is not a distribution of anything.
- **Compare** those labeled aggregates **side by side, across groups.** That is
  the entire point of the benchmark — v1-as-patched vs v2 vs a single-agent
  baseline are *deliberately* different identities. Comparing them is correct;
  pooling their attempts into one distribution is not.

The identity group is the full field set below — `story_hash` and `config_hash`
alone are **not sufficient**, since the target underneath can change while both
stay fixed, which is exactly what the P-9/P-10 patches on this branch did. Every
field is recorded on each run record; group by all of them before aggregating:

| Source | Fields | Why it moves |
|---|---|---|
| Story | `story_hash` | prompt, fixture pin, validators, checks, **and the `[budget]` block** |
| Configuration | `config_hash` | model routing, budgets declared by the MPH bundle |
| MPH identity | `harness_hash` | **adapter-derived harness content — e.g. the Gitea image the adapter runs — which `config_hash` does not cover** |
| MPH identity | `model`, `prompt_pack`, `prompt_hash`, `maestro_version` | a prompt-template edit (e.g. P-4) changes cost without touching any story |
| Target descriptor | `adapter_name`, `adapter_version`, `commit_hash`, `binary_identity` | **a target rebuild changes behavior invisibly to every hash above** |
| Target descriptor | `budget_enforcement`, `capabilities` | qualifies which *metrics* compare — see below |

Two qualifications that bite in practice:

- **Enforcement mode qualifies metrics selectively** ([ADR 0025](../docs/adr/0025-golden-stories-and-benchmark-runner.md),
  amended 2026-07-16), which is a statement about *comparison*, not aggregation:

  | Metric | Across enforcement modes |
  |---|---|
  | Raw cost / token **values** | **Comparable** — a dollar spent is a dollar spent |
  | Budget-overrun **rates** | **Not comparable** — mode-scoped by definition |
  | Cost **distributions** | **Not comparable** — a `streamed` target is cancelled at the cap, so its expensive runs are right-censored; a `post-hoc` target runs past the cap and reports the full figure |

  So: report a streamed group's cost alongside a post-hoc group's, but never
  merge their attempts into one distribution, and never put their overrun rates
  in the same column. A streamed group's spread must carry its censoring.
- **Changing a cap changes `story_hash` by design** — the budget is part of
  the story definition. Caps are a safeguard, so varying them during
  calibration is legitimate, but it means a calibration series spans several
  identities. **Fix the caps before collecting any distribution you intend to
  compare or publish**, and record which identity each attempt ran against.

Item 7's comparison reporting keys on exactly this: **aggregate within a group,
label the group with its identity and enforcement mode, then place those
aggregates side by side.** A report that pools attempts across differing target
descriptors is reporting noise as spread; a report that refuses to place
different targets side by side has no comparison to make.

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
