+++
title = "Design: Cost And Latency Reduction (Item 5.1)"
edit_date = "2026-07-17"
status = "draft"
summary = "Mini-plan for the cost-latency work item: a registry-published, digest-pinned union cache image that kills the cold-cache tax (#268, deterministically verified with GOPROXY=off), then an Ollama-only paired-local configuration that makes basic end-to-end exercise of the harness near-free (#266) — gated on a viability probe, with local cost marked unavailable and local runs budgeted on tokens and wall-clock with zero USD reservation."
+++

# Design: Cost And Latency Reduction (Item 5.1)

Status: draft — mini-plan for Phase 1 item 5.1 (`cost-latency`), the DR-directed step added to [plan_scope.md](plan_scope.md) after item 5's discovery loop made the harness's running costs concrete. Two independent instrument-economics changes, neither v1 maintenance nor a change to the run-record contract. Incorporates Codex design rounds 1–3: round 1 (fixture-only scope, union image + registry distribution, `unavailable` cost with defined admission/settlement, Ollama-only provider path, GOPROXY=off deterministic cache proof); round 2 (the complete token-budget contract — typed flag, bundle+manifest schema version bumps, effective-cap precedence, conservative reservation/settlement including unavailable usage; and the maintainer-publishes/PR-CI-read-only image lifecycle); and round 3 (reservation is unambiguously the effective per-run max and is retained on unavailable usage including failed attempts; the manifest carries per-config `budget_accounts` so mixed hosted+local suites are represented directly). Binding sources: [ADR 0025](../../adr/0025-golden-stories-and-benchmark-runner.md) (target strategy, four-state metrics, cost-to-accepted-change), the [Phase 1 plan](plan_scope.md), [design_engine.md](design_engine.md) (conservative admission/settlement), [design_adapter_v1.md](design_adapter_v1.md) (the container-image pin, the P-1 usage surface). Issues: [#268](https://github.com/SnapdragonPartners/maestro/issues/268) (dependency caches), [#266](https://github.com/SnapdragonPartners/maestro/issues/266) (local models).

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

**Publication lifecycle — maintainer publishes, PR CI is read-only (Codex round 2).** The tracked digest is a committed constant, changed only by a deliberate maintainer/release step, never by PR CI:

- **Maintainer publish (produces the digest).** A make target (`benchmark/`, run by a maintainer or a release workflow holding GHCR push credentials) assembles the fixtures' pinned `go.mod`/`go.sum`, builds the union image, pushes it to `ghcr.io/snapdragonpartners/golden-cache`, and writes the resulting `@sha256:` digest into the config. The image is published **public** — it holds only cached public Go modules, no secrets — so pulls need no auth. This step runs when a fixture re-pins (the only thing that changes the image's contents); it is the sole path that rewrites the tracked digest, exactly like the golang-base and Gitea digest pins.
- **PR CI (consumes the digest, read-only).** PR CI **pulls the committed digest and runs the `GOPROXY=off` offline verification** (below). It has no GHCR push credentials, never pushes, and never rewrites the tracked digest. If a maintainer re-pinned a fixture but forgot to republish, the offline check fails loudly (deps missing from the stale image) — drift cannot merge silently.

**Explicitly deferred (Codex round 1):** the second half of #268 — the *bootstrapper* baking `go mod download`-style layers into the dev-container Dockerfiles it generates for arbitrary user projects. That is a v1/v2 factory improvement bordering on v1 maintenance; it is not needed for the benchmark cost win and stays out of Phase 1. #268 remains open for it.

## Part B: The `paired-local` Configuration (#266)

**Ollama-only (Codex round 1, amended by the probe).** v1 already maps `qwen*`/`mistral*` model prefixes to Ollama and takes the endpoint from `OLLAMA_HOST` — no new glue for those. The probe then chose **gpt-oss** as the architect, which *did* need a one-line v1 routing entry (its `gpt` prefix otherwise routes to hosted OpenAI): patch **P-8**, an enumerated instrument-enabling v1 patch (`patches_v1.md`), generally correct rather than benchmark special-casing. v1 still has **no** configurable vLLM/OpenAI-compatible base-URL path, so vLLM and other OpenAI-compatible endpoints stay deferred to #266's remaining scope; the deeper fix — replacing name-based provider inference with explicit provider declaration — is v2 work ([#272](https://github.com/SnapdragonPartners/maestro/issues/272)).

**The real unknown, probed first.** `maestro-llms` is validated against Ollama, but *full maestro* — the paired factory's structured reviews, tool-calling, and JSON-schema'd terminal tools — has never run end-to-end on local models. So Part B opens with a **viability probe**, not a config build: point a throwaway config at Ollama (`OLLAMA_HOST` set) and run the `smoke-comment` story. The probe answers whether qwen3-coder/mistral can actually drive the architect's single-turn reviews and the coder's tool loop before we invest in a real config. If they cannot yet, the honest outcome is a documented finding and a deferral — not a forced config.

**Model mapping — probe verdict (2026-07-18).** The probe ran (probes 1–4 on `smoke-comment`) and the starting-point mapping was *inverted* by the findings. `mistral-small` failed in **both** seats — wrong `file_edit` parameter schema as coder (edit never lands), and a fatal failure to emit the terminal review tool as architect. `qwen3-coder` is reliable in both seats; `gpt-oss` is a capable architect. The chosen mapping:

| Role | Hosted (paired-default) | Local (paired-local, Ollama) |
|---|---|---|
| architect | claude-opus-4-1 | **gpt-oss:20b** |
| coder, pm | claude-sonnet-4-6 | **qwen3-coder:30b** |

Verdict: **viable.** qwen-coder + gpt-oss-architect completed `smoke-comment` end-to-end, `accepted`, at **$0 hosted-API spend (total cost unmodeled)** and ~6–9 min wall clock. `qwen3-coder` for both roles is a valid single-model fallback. `mistral-small` is not used. The full model×seat matrix (with failures) is durable in `benchmark/README.md`. `gpt-oss` needed a routing fix (patch **P-8**: its `gpt` prefix otherwise routes to hosted OpenAI); name-based provider inference is flagged for v2 deprecation ([#272](https://github.com/SnapdragonPartners/maestro/issues/272)).

**Cost marking — `unavailable`, not `not_applicable` (Codex round 1).** A local run *does* incur total attempt cost — it simply is not modeled in USD, and ADR 0025 reserves `not_applicable` for metrics a story does not exercise. So `cost_usd` is **`unavailable`** for a local config (with a reason: "local provider; USD cost unmodeled"), while `tokens_total` and `llm_calls` stay `value` through the P-1 usage surface. Passing the usage log's `$0` through as a `value` would be a lie that poisons cost-to-accepted-change (item 7); `unavailable` is the honest marking. The adapter learns a config is local from an **explicit harness flag** on the MPH bundle (`local = true`) — least magic, documents intent — not by sniffing model names.

**The token-budget contract (Codex round 2).** An `unavailable` observed cost cannot settle a USD reservation, so a USD-budgeted `paired-local` run would hold its full reservation all suite long — "near-free" would be false at the accounting level, and the validator currently *requires* `expected_cost_usd_per_run > 0`. Resolution is a complete, versioned token-budget dimension, defined here point-by-point:

1. **Typed flag.** A new top-level bundle field `local bool` (default `false`) selects the token budget dimension. `false`/absent is today's hosted, USD-budgeted behavior, unchanged.

2. **Schema and versions.** MPH bundle `schema_version` bumps **1 → 2**: adds `local`, and `DeclaredBudget` gains `max_tokens_per_run` and `max_tokens_per_suite` (int64). Existing USD fields are unchanged. Validation is by dimension:
   - **Hosted** (`local=false`): require `expected_cost_usd_per_run>0`, `max_cost_usd_per_run>0`, `max_cost_usd_per_suite>0` (as today); the token-max fields must be absent/zero.
   - **Local** (`local=true`): require `expected_tokens_per_run>0` (a declared **estimate only**, for reporting — *not* the reservation; the reservation is the effective per-run max, item 4), `max_tokens_per_run>0`, `max_tokens_per_suite>0`; the USD caps **must be absent/zero** — a positive USD cap on a local bundle is a validation error (no ambiguous dual budget).

3. **Effective per-run token cap — precedence.** Stories already carry `max_tokens`. The effective per-run cap is **`min(story.max_tokens, bundle.max_tokens_per_run)`** — the tighter bound wins, mirroring the existing USD rule (`min(story.max_cost_usd, bundle.max_cost_usd_per_run)`). Streamed enforcement aborts the run as `budget-overrun` (same failure kind, same mechanism) when cumulative tokens from the P-1 usage deltas exceed this cap.

4. **Conservative suite reservation/settlement.** The reservation is unambiguously the **effective per-run token max** (item 3's `min(story, bundle)`), not `expected_tokens_per_run`. On admission, reserve that max against `max_tokens_per_suite`; admit only if the remaining suite token budget covers it — the token analogue of item 3's "reserve effective per-run max, settle to observed." On completion, settle the reservation down to the run's **observed** tokens (P-1 usage surface).

5. **Unknown-metric behavior.** If observed `tokens_total` is **`unavailable`**, the reservation is **retained** at the effective per-run max, never settled down — conservative, identical to USD-with-unavailable-cost. This holds **for failed attempts too**: item 5's P-1 hardening fails any run whose usage surface never validates, and that attempt's unavailable token count keeps its full reservation charged to the suite (conservative accounting assumes it spent the max). A reservation is never released on the ground that the attempt failed.

6. **Manifest accounting and version.** The suite manifest `manifest_schema_version` bumps **1 → 2**, replacing the flat `cap_usd`/`charged_usd`/`observed_usd` with a **`budget_accounts` array** — one entry per config in the suite, each `{config, dimension ("usd"|"tokens"), cap, charged, observed}`. A single-config suite has one entry; a mixed hosted+local suite carries a `usd` entry and a `tokens` entry, each accounted against its own config's suite cap. There is no lossy top-level dimension — a reader decodes each config's budget unambiguously from its own entry, and mixed suites are represented directly.

The engine applies **zero USD reservation** for `local` configs — API dollar spend is $0 by construction, nothing to reserve or settle — so the USD suite cap does not gate them and the token dimension does. These are bounded, honest, benchmark-side additions (a bundle field, a validator branch, an admission/settlement branch in [design_engine.md](design_engine.md), token fields in the manifest) — not the run-record contract, not v1. With them, "near-free" is true and defined: $0 API dollars, token/wall-clock-bounded, no phantom reservation.

**Done means** either: a `paired-local` config that completes `smoke-comment` locally with honest metrics (tokens/calls `value`, cost `unavailable`) under token/wall-clock budgets, giving a near-free e2e path for future harness iteration; **or** a documented finding that local models cannot yet drive the paired factory reliably, with the config deferred and the reason recorded. Both are acceptable — the probe decides, and neither blocks item 6.

## Testing

- **Caching — deterministic, not timing (Codex round 1).** A wall-clock comparison cannot *prove* the cache is warm — model latency, compilation, and test variance dominate it. The acceptance test instead runs each pinned fixture against the union image with **dependency networking disabled** (`GOPROXY=off`, no module proxy reachable) and proves `go build`/`go test` succeeds: success is only possible if every dependency was already in the baked cache. This runs in the Docker-required CI job. Before/after wall-clock (against discovery-011's 295s block) is kept as *secondary* evidence of the win, not as the proof.
- **Local:** the viability probe *is* the test. If viable, `paired-local` passes `runner validate` and completes `smoke-comment`; the record shows `cost_usd: unavailable` (reason recorded) with `tokens_total`/`llm_calls` as `value`, under token/wall-clock budgets.

## Risks

- **Local models can't drive the structured protocols.** The paired factory leans on reliable tool-calling and JSON-schema'd terminal tools; smaller local models may not comply. Mitigation: the probe surfaces this *before* config investment; deferral is a first-class outcome.
- **Union image drift.** An image built from stale `go.mod`/`go.sum` would cache the wrong deps. Mitigation: the image is a pure function of the fixture pins, rebuilt-and-republished on any re-pin; the `GOPROXY=off` CI proof fails loudly if a dependency is missing from the cache, so drift cannot pass silently.
- **Admission/settlement change touches the engine.** Zero-USD reservation and token-based caps for `local` configs are a branch in the engine's reservation logic (design_engine.md). Mitigation: the branch is guarded by the explicit `local` bundle flag, so hosted configs are untouched; covered by unit tests on both admission paths.
- **Scope creep into v1's bootstrapper.** Mitigation: the bootstrapper half of #268 is explicitly deferred; item 5.1 touches only benchmark/fixture-side images.

## Resolutions (Codex Design Rounds 1–3)

The round-1 open questions are settled (round 2's token-budget contract, round 3's reservation-semantics and mixed-suite manifest fixes, and the image-publication lifecycle are specified inline in Parts A and B above):

1. **Image identity/distribution:** **registry-published immutable digests** (ghcr.io), because CI exercises the images — a locally built digest would not exist on CI or another machine. One **union image** serves the single-`container_image` bundle field; its digest binds into H.
2. **Local-provider convention:** already discoverable in v1 — model **prefixes route to Ollama**, and **`OLLAMA_HOST`** selects the endpoint. Item 5.1 is **Ollama-only**; vLLM/OpenAI-compatible endpoints remain #266 work unless separately scoped (they would need new v1 glue).
3. **Local cost:** an **explicit `local` harness flag** on the bundle, and unmodeled local cost is marked **`unavailable`** (not `not_applicable`); local configs are budgeted on **tokens + wall-clock** with **zero USD reservation**, and the validator requires token caps in place of positive USD.
4. **Deferral boundary confirmed:** the **bootstrapper half of #268 stays deferred** (v1/v2 factory work); **vLLM stays deferred** to #266. Item 5.1 is the union image + the Ollama `paired-local` config only.

## Build Order

1. **Part A — union cache image:** the make target, the ghcr.io publish, the digest pin into `paired-default` (and any config using library fixtures), the `GOPROXY=off` CI proof. Lands first — it accelerates every Part B probe iteration.
2. **Part B — viability probe:** throwaway Ollama config, run `smoke-comment`, judge which local models drive the paired factory. Gate. (Verdict above: gpt-oss architect + qwen3-coder coder; mistral-small rejected.)
3. **Part B — `paired-local` config** (only if the probe passes): the `local` bundle flag and bundle `schema_version` 1→2 with token caps + dimension validation; the effective-cap/reservation/settlement branches in the engine; the manifest `manifest_schema_version` 1→2 with per-config `budget_accounts`; `cost_usd: unavailable` in the adapter; the config bundle itself.
