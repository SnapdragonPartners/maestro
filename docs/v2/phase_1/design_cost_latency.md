+++
title = "Design: Cost And Latency Reduction (Item 5.1)"
edit_date = "2026-07-17"
status = "draft"
summary = "Mini-plan for the cost-latency work item: pre-baked per-fixture container images that kill the cold-cache tax (#268), then a paired-local Ollama/vLLM configuration that makes basic end-to-end exercise of the harness near-free (#266) — gated on a viability probe, with honest not_applicable cost marking for local models."
+++

# Design: Cost And Latency Reduction (Item 5.1)

Status: draft — mini-plan for Phase 1 item 5.1 (`cost-latency`), the DR-directed step added to [plan_scope.md](plan_scope.md) after item 5's discovery loop made the harness's running costs concrete. Two independent instrument-economics changes, neither v1 maintenance nor a change to the run-record contract. Binding sources: [ADR 0025](../../adr/0025-golden-stories-and-benchmark-runner.md) (target strategy, four-state metrics, cost-to-accepted-change), the [Phase 1 plan](plan_scope.md), [design_adapter_v1.md](design_adapter_v1.md) (the container-image pin, the P-1 usage surface). Issues: [#268](https://github.com/SnapdragonPartners/maestro/issues/268) (dependency caches), [#266](https://github.com/SnapdragonPartners/maestro/issues/266) (local models).

## Why Now

Item 5's live runs priced the instrument for the first time, and two costs stood out:

- **Wall clock.** discovery-011 spent a **295-second idle block** — ~40% of its 750s run — on the first in-container `go test`, which cold-downloads the fixture's entire module tree because the pinned `golang:1.26` base has no cache. LLM latency averaged ~3.7s/call; the container, not the model, was the bottleneck.
- **Dollars.** Each hosted discovery attempt cost ~$1–8; the 14-run loop spent ~$71 of a $96 suite cap. Items 6–10 (instrumented cost runs, the growing story suite, the single-agent baseline across every story) multiply that.

Both are the **benchmark-cost risk** the plan already names, now with numbers. Fixing them before the token-heavy items is cheaper than after.

## Sequencing: Caching First

Part A (caching) lands before Part B (local models), because the dependency is one-way: a faster harness accelerates the **many** iterations Part B's viability probe will need (local inference is slow; each avoided cold-download compounds across probe runs), while local models give the caching work nothing back. Caching is also the lower-risk half — self-contained, no external unknowns — so it banks a guaranteed win first.

## Part A: Dependency-Cache Pre-Warming (#268)

**The tax.** Library fixtures (`golden-fixture-cms`, `-llms`) carry no Dockerfile, so the harness runs v1's coder in a stock, digest-pinned `golang:1.26` (item 5). That image ships no module cache; the first `go build`/`go test` downloads everything.

**The fix: pre-baked per-fixture images.** Build a thin image `FROM` the digest-pinned base that bakes the fixture's dependency cache into an immutable layer:

```dockerfile
FROM golang@sha256:<pinned base>
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
```

The `go.mod`/`go.sum` come from the fixture at its pinned commit, so the image is fixture-commit-specific and rebuilds when the fixture re-pins (owned by the fixture-conventions procedure, [process_fixtures.md](process_fixtures.md)). The built image is **pinned by digest** and `container_image` in the config points at it — same discipline as the base pin, so two nominally identical runs execute identical container contents. The cache lives *in the pinned image*, so hermeticity is preserved: no shared mutable module-cache volume across runs (which would reintroduce cross-run state — explicitly rejected).

For any fixture that *does* carry its own Dockerfile (the app-bearing fixture), the same cache-warming layers are added to that fixture's Dockerfile — a fixture change, in scope.

**Build path.** A make target (`benchmark/`) builds and digest-pins the per-fixture images from the fixtures' pinned commits; the config references the resulting digests. Local build + local digest pin is single-machine reproducible, which suffices for the local dev + CI-with-Docker setup; pushing to a registry for cross-machine byte-identity is a later option (noted, not built).

**Explicitly deferred:** the second half of #268 — the *bootstrapper* baking `go mod download`-style layers into the dev-container Dockerfiles it generates for arbitrary user projects. That is a v1/v2 factory improvement bordering on v1 maintenance; it is not needed for the benchmark cost win and stays out of Phase 1. #268 remains open for it.

## Part B: The `paired-local` Configuration (#266)

**The real unknown, probed first.** `maestro-llms` is validated against Ollama and LAN vLLM, but *full maestro* — the paired factory's structured reviews, tool-calling, and JSON-schema'd terminal tools — has never run end-to-end on local models. So Part B opens with a **viability probe**, not a config build: point a throwaway config at Ollama and run the `smoke-comment` story. The probe answers whether qwen3-coder/mistral can actually drive the architect's single-turn reviews and the coder's tool loop before we invest in a real config around them. If they cannot yet, the honest outcome is a documented finding and a deferral — not a forced config.

**Model mapping (starting point, adjustable from the probe):**

| Role | Hosted (paired-default) | Local (paired-local) |
|---|---|---|
| architect | claude-opus-4-1 | qwen3-coder:30b |
| coder, pm | claude-sonnet-4-6 | mistral-small3.2:24b |

`gpt-oss:20b` is available as an alternative if mistral underperforms on the coder role. Endpoint (Ollama local, or vLLM on the LAN) is expressed through `maestro-llms`' provider routing; pinning the exact model-name/endpoint convention v1's config uses to reach a local provider is a probe deliverable.

**Cost marking — honest, not zero.** Local inference has no dollar cost, so for a local config `cost_usd` is **`not_applicable`** (ADR 0025's four-state), while `tokens_total` and `llm_calls` stay `value` — still measured through the P-1 usage surface. Reporting `$0` would be wrong: it would feed a false "free" into cost-to-accepted-change (item 7), whose undefined case this is exactly. The adapter must mark cost `not_applicable` when the config is local rather than passing the usage log's `$0` through. How the adapter learns a config is local (an MPH flag vs. inferring from the provider/model names) is an open question below.

**Done means** either: a `paired-local` config that completes `smoke-comment` locally with honest metrics (tokens/calls `value`, cost `not_applicable`), giving a near-free e2e path for future harness iteration; **or** a documented finding that local models cannot yet drive the paired factory reliably, with the config deferred and the reason recorded. Both are acceptable — the probe decides, and neither blocks item 6.

## Testing

- **Caching:** a run against a pre-baked fixture image completes the first `go test` without a download stall; the wall-clock drop is the acceptance signal (compare against discovery-011's 295s block). The image-build make target is exercised in CI-with-Docker.
- **Local:** the viability probe *is* the test. If viable, `paired-local` passes `runner validate` and completes `smoke-comment`; the record shows `cost_usd: not_applicable` with `tokens_total`/`llm_calls` as `value`.

## Risks

- **Local models can't drive the structured protocols.** The paired factory leans on reliable tool-calling and JSON-schema'd terminal tools; smaller local models may not comply. Mitigation: the probe surfaces this *before* config investment; deferral is a first-class outcome.
- **Pre-baked image drift.** An image built from stale `go.mod` would cache the wrong deps. Mitigation: tie the image build to the fixture's pinned commit and rebuild on re-pin; digest-pin the result.
- **Cross-machine reproducibility** of locally-built images. Mitigation: single-machine digest pin now; registry push noted as the later cross-machine option.
- **Scope creep into v1's bootstrapper.** Mitigation: the bootstrapper half of #268 is explicitly deferred; item 5.1 touches only benchmark/fixture-side images.

## Open Questions — For Review

1. **Pre-baked image identity:** local build + local digest pin (single-machine reproducible, zero infra) versus a registry push (cross-machine byte-identity). Recommend local for Phase 1; revisit if CI needs to share images.
2. **Local-provider config convention:** the exact model-name/endpoint form v1's config + `maestro-llms` use to reach Ollama/vLLM — a probe deliverable, pinned before the config lands.
3. **Marking cost `not_applicable`:** does the adapter learn "this config is local" from an explicit MPH flag or infer it from provider/model names? Recommend an explicit flag on the config bundle — least magic, and it documents intent.
4. **Deferral boundary:** confirm the bootstrapper half of #268 is correctly out of Phase 1 (v1/v2 factory work), leaving item 5.1 to the benchmark/fixture-side images only.
