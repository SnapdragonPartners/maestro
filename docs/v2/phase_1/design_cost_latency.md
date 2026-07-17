+++
title = "Design: Cost And Latency Reduction (Item 5.1)"
edit_date = "2026-07-17"
status = "draft"
summary = "Mini-plan for the cost-latency work item: a registry-published, digest-pinned union cache image that kills the cold-cache tax (#268, deterministically verified with GOPROXY=off), then an Ollama-only paired-local configuration that makes basic end-to-end exercise of the harness near-free (#266) — gated on a viability probe, with local cost marked unavailable and local runs budgeted on tokens and wall-clock with zero USD reservation."
+++

# Design: Cost And Latency Reduction (Item 5.1)

Status: draft — mini-plan for Phase 1 item 5.1 (`cost-latency`), the DR-directed step added to [plan_scope.md](plan_scope.md) after item 5's discovery loop made the harness's running costs concrete. Two independent instrument-economics changes, neither v1 maintenance nor a change to the run-record contract. Incorporates Codex design round 1 (five P1 resolutions: fixture-only scope, union image + registry distribution, `unavailable` cost with defined admission/settlement, Ollama-only provider path, GOPROXY=off deterministic cache proof). Binding sources: [ADR 0025](../../adr/0025-golden-stories-and-benchmark-runner.md) (target strategy, four-state metrics, cost-to-accepted-change), the [Phase 1 plan](plan_scope.md), [design_engine.md](design_engine.md) (conservative admission/settlement), [design_adapter_v1.md](design_adapter_v1.md) (the container-image pin, the P-1 usage surface). Issues: [#268](https://github.com/SnapdragonPartners/maestro/issues/268) (dependency caches), [#266](https://github.com/SnapdragonPartners/maestro/issues/266) (local models).

## Why Now

Item 5's live runs priced the instrument for the first time, and two costs stood out:

- **Wall clock.** discovery-011 spent a **295-second idle block** — ~40% of its 750s run — on the first in-container `go test`, which cold-downloads the fixture's entire module tree because the pinned `golang:1.26` base has no cache. LLM latency averaged ~3.7s/call; the container, not the model, was the bottleneck.
- **Dollars.** Each hosted discovery attempt cost ~$1–8; the 14-run loop spent ~$71 of a $96 suite cap. Items 6–10 (instrumented cost runs, the growing story suite, the single-agent baseline across every story) multiply that.

Both are the **benchmark-cost risk** the plan already names, now with numbers. Fixing them before the token-heavy items is cheaper than after.

## Sequencing: Caching First

Part A (caching) lands before Part B (local models), because the dependency is one-way: a faster harness accelerates the **many** iterations Part B's viability probe will need (local inference is slow; each avoided cold-download compounds across probe runs), while local models give the caching work nothing back. Caching is also the lower-risk half — self-contained, no external unknowns — so it banks a guaranteed win first.

## Part A: Dependency-Cache Pre-Warming (#268)

**The tax.** The Go fixtures (`golden-fixture-cms`, `-llms`, and the LLM-tester CLI) carry no benchmark-usable module cache, so the harness runs v1's coder in a stock, digest-pinned `golang:1.26` (item 5). That image ships no cache; the first `go build`/`go test` downloads everything.

**The fix: one registry-published union cache image.** A single MPH bundle carries one `container_image` for every story, so per-fixture image selection is impossible without a schema change — and a locally built digest would not exist on CI or another machine. Both problems dissolve with a **union image**: one image `FROM` the digest-pinned Go base that bakes *every* Go fixture's module cache into an immutable layer, published to the org registry, and pinned by digest.

```dockerfile
FROM golang@sha256:<pinned base>
WORKDIR /cache
# one COPY+download per pinned Go fixture; caches share GOMODCACHE
COPY cms.go.mod cms.go.sum ./cms/
COPY llms.go.mod llms.go.sum ./llms/
COPY tester.go.mod tester.go.sum ./tester/
RUN cd cms && go mod download && cd ../llms && go mod download && cd ../tester && go mod download
```

The `go.mod`/`go.sum` inputs come from the fixtures at their pinned commits, so the image is a pure function of the fixture pins and is rebuilt-and-republished whenever any fixture re-pins (owned by the fixture-conventions procedure, [process_fixtures.md](process_fixtures.md)). It is **published to the SnapdragonPartners registry (ghcr.io) and referenced by immutable digest** as the config's `container_image`, so CI and every machine pull byte-identical contents and the digest binds into the harness hash H — same discipline as the base and Gitea pins. One image serves all stories; the cache lives *in the pinned image*, so hermeticity holds — no shared mutable module-cache volume across runs (explicitly rejected as reintroduced cross-run state).

All current fixtures are Go modules, so a single union image covers them. A future non-Go fixture is handled by extending the union image per-language or by that fixture carrying its own cache-warming Dockerfile — noted, not built.

**Build path.** A make target (`benchmark/`) assembles the fixtures' pinned `go.mod`/`go.sum`, builds the union image, pushes it to ghcr.io, and records the resulting digest into the config. CI builds/pulls it as part of the Docker-required job.

**Explicitly deferred (Codex round 1):** the second half of #268 — the *bootstrapper* baking `go mod download`-style layers into the dev-container Dockerfiles it generates for arbitrary user projects. That is a v1/v2 factory improvement bordering on v1 maintenance; it is not needed for the benchmark cost win and stays out of Phase 1. #268 remains open for it.

## Part B: The `paired-local` Configuration (#266)

**Ollama-only (Codex round 1).** v1 already maps `qwen*`/`mistral*` model prefixes to Ollama and takes the endpoint from `OLLAMA_HOST` — no new v1 provider glue, so the no-v1-maintenance guarantee holds. v1 has **no** configurable vLLM/OpenAI-compatible base-URL path, so vLLM and other OpenAI-compatible endpoints stay deferred to #266's remaining scope; adding that glue would be v1 maintenance and is out of item 5.1.

**The real unknown, probed first.** `maestro-llms` is validated against Ollama, but *full maestro* — the paired factory's structured reviews, tool-calling, and JSON-schema'd terminal tools — has never run end-to-end on local models. So Part B opens with a **viability probe**, not a config build: point a throwaway config at Ollama (`OLLAMA_HOST` set) and run the `smoke-comment` story. The probe answers whether qwen3-coder/mistral can actually drive the architect's single-turn reviews and the coder's tool loop before we invest in a real config. If they cannot yet, the honest outcome is a documented finding and a deferral — not a forced config.

**Model mapping (starting point, adjustable from the probe):**

| Role | Hosted (paired-default) | Local (paired-local, Ollama) |
|---|---|---|
| architect | claude-opus-4-1 | qwen3-coder:30b |
| coder, pm | claude-sonnet-4-6 | mistral-small3.2:24b |

`gpt-oss:20b` is available as an Ollama alternative if mistral underperforms on the coder role.

**Cost marking — `unavailable`, not `not_applicable` (Codex round 1).** A local run *does* incur total attempt cost — it simply is not modeled in USD, and ADR 0025 reserves `not_applicable` for metrics a story does not exercise. So `cost_usd` is **`unavailable`** for a local config (with a reason: "local provider; USD cost unmodeled"), while `tokens_total` and `llm_calls` stay `value` through the P-1 usage surface. Passing the usage log's `$0` through as a `value` would be a lie that poisons cost-to-accepted-change (item 7); `unavailable` is the honest marking. The adapter learns a config is local from an **explicit harness flag** on the MPH bundle (`local = true`) — least magic, documents intent — not by sniffing model names.

**Budgeting local configs — the admission/settlement gap (Codex round 1).** An `unavailable` observed cost cannot settle a USD reservation, so under the engine's conservative admission a `paired-local` run would hold its full USD reservation for the whole suite — "near-free" would be false at the suite-accounting level, and the bundle validator currently *requires* `expected_cost_usd_per_run > 0`. Resolution, defined here:

- A `local`-flagged bundle is budgeted and enforced on **tokens and wall-clock** (`max_tokens_per_run`, `max_tokens_per_suite`, wall-clock deadline), not USD.
- The engine applies **zero USD reservation** for `local` configs: their API dollar spend is $0 by construction, so there is nothing to reserve or settle, and the USD suite cap does not gate them.
- The bundle validator, for `local` configs, drops the positive-USD requirement and instead requires the token caps to be present and positive.

These are small, honest, benchmark-side additions (a bundle flag, a validator branch, an admission branch in `design_engine.md`'s reservation logic) — not the run-record contract, not v1. With them, "near-free" is true and defined: $0 API dollars, token/wall-clock-bounded, no phantom USD reservation.

**Done means** either: a `paired-local` config that completes `smoke-comment` locally with honest metrics (tokens/calls `value`, cost `unavailable`) under token/wall-clock budgets, giving a near-free e2e path for future harness iteration; **or** a documented finding that local models cannot yet drive the paired factory reliably, with the config deferred and the reason recorded. Both are acceptable — the probe decides, and neither blocks item 6.

## Testing

- **Caching — deterministic, not timing (Codex round 1).** A wall-clock comparison cannot *prove* the cache is warm — model latency, compilation, and test variance dominate it. The acceptance test instead runs each pinned fixture against the union image with **dependency networking disabled** (`GOPROXY=off`, no module proxy reachable) and proves `go build`/`go test` succeeds: success is only possible if every dependency was already in the baked cache. This runs in the Docker-required CI job. Before/after wall-clock (against discovery-011's 295s block) is kept as *secondary* evidence of the win, not as the proof.
- **Local:** the viability probe *is* the test. If viable, `paired-local` passes `runner validate` and completes `smoke-comment`; the record shows `cost_usd: unavailable` (reason recorded) with `tokens_total`/`llm_calls` as `value`, under token/wall-clock budgets.

## Risks

- **Local models can't drive the structured protocols.** The paired factory leans on reliable tool-calling and JSON-schema'd terminal tools; smaller local models may not comply. Mitigation: the probe surfaces this *before* config investment; deferral is a first-class outcome.
- **Union image drift.** An image built from stale `go.mod`/`go.sum` would cache the wrong deps. Mitigation: the image is a pure function of the fixture pins, rebuilt-and-republished on any re-pin; the `GOPROXY=off` CI proof fails loudly if a dependency is missing from the cache, so drift cannot pass silently.
- **Admission/settlement change touches the engine.** Zero-USD reservation and token-based caps for `local` configs are a branch in the engine's reservation logic (design_engine.md). Mitigation: the branch is guarded by the explicit `local` bundle flag, so hosted configs are untouched; covered by unit tests on both admission paths.
- **Scope creep into v1's bootstrapper.** Mitigation: the bootstrapper half of #268 is explicitly deferred; item 5.1 touches only benchmark/fixture-side images.

## Resolutions (Codex Design Round 1)

The round-1 open questions are settled:

1. **Image identity/distribution:** **registry-published immutable digests** (ghcr.io), because CI exercises the images — a locally built digest would not exist on CI or another machine. One **union image** serves the single-`container_image` bundle field; its digest binds into H.
2. **Local-provider convention:** already discoverable in v1 — model **prefixes route to Ollama**, and **`OLLAMA_HOST`** selects the endpoint. Item 5.1 is **Ollama-only**; vLLM/OpenAI-compatible endpoints remain #266 work unless separately scoped (they would need new v1 glue).
3. **Local cost:** an **explicit `local` harness flag** on the bundle, and unmodeled local cost is marked **`unavailable`** (not `not_applicable`); local configs are budgeted on **tokens + wall-clock** with **zero USD reservation**, and the validator requires token caps in place of positive USD.
4. **Deferral boundary confirmed:** the **bootstrapper half of #268 stays deferred** (v1/v2 factory work); **vLLM stays deferred** to #266. Item 5.1 is the union image + the Ollama `paired-local` config only.

## Build Order

1. **Part A — union cache image:** the make target, the ghcr.io publish, the digest pin into `paired-default` (and any config using library fixtures), the `GOPROXY=off` CI proof. Lands first — it accelerates every Part B probe iteration.
2. **Part B — viability probe:** throwaway Ollama config, run `smoke-comment`, judge whether qwen3-coder/mistral drive the paired factory. Gate.
3. **Part B — `paired-local` config** (only if the probe passes): the `local` bundle flag, the validator + engine admission branches (token/wall-clock budget, zero USD reservation), `cost_usd: unavailable` in the adapter, the config bundle itself.
