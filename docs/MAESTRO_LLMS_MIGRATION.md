# Maestro → `maestro-llms` Migration Spec

**Status:** Draft for review by the `maestro-llms` extraction team
**Branch:** `spec/maestro-llms-migration`
**Scope:** Replace Maestro's in-tree LLM provider clients + resilience
middleware with the extracted [`maestro-llms`](https://github.com/SnapdragonPartners/maestro-llms)
module (`github.com/SnapdragonPartners/maestro-llms`).

This document is the implementation plan and the cut-over acceptance
checklist. It maps every row of the toolkit's
[`docs/MAESTRO_DIVERGENCES.md`](https://github.com/SnapdragonPartners/maestro-llms/blob/main/docs/MAESTRO_DIVERGENCES.md)
to the concrete Maestro code site that must change.

> **Extraction team:** please sanity-check Sections 4 (adapter seam),
> 5 (behaviors to preserve), and 7 (open questions). The open questions are
> where Maestro might need a toolkit affordance that doesn't exist yet.

---

## 1. Goal & non-goals

**Goal:** delete Maestro's bespoke provider clients and resilience stack;
depend on `maestro-llms` for provider I/O, error classification, retry,
circuit breaking, timeout, and rate limiting. Reduce maintenance and shared
integration-test burden; remove the CVE-bearing `github.com/ollama/ollama`
dependency.

**Non-goals:** no behavior change visible to agents *except* the intentional
divergences in Section 5; no rewrite of the ~175 LLM consumer call sites; no
streaming (unused today, not implemented by the toolkit).

## 2. Compatibility baseline

| Dimension | Maestro | maestro-llms | Verdict |
|---|---|---|---|
| Go version | `go 1.26` | `go 1.26.3` | Compatible |
| Anthropic SDK | `anthropic-sdk-go v1.37.0` | `v1.37.0` | Identical (now indirect) |
| OpenAI SDK | `openai-go v1.12.0` | `v1.12.0` | Identical (now indirect) |
| Gemini | `google.golang.org/genai` | `genai v1.54.0` | Compatible |
| Ollama | `github.com/ollama/ollama v0.21.0` | none (hand-rolled HTTP) | **Dep removed — clears govulncheck gate** |
| Streaming | `Stream()` defined, **unused** outside impl/middleware/tests | not implemented | Drop from adapter |

## 3. Prune inventory (≈3,700 LOC removed)

| Code | ~LOC | Disposition |
|---|---|---|
| `pkg/agent/internal/llmimpl/{anthropic,openaiofficial,google,ollama}` | 1,598 | **Delete** → `llms/providers/*` |
| `pkg/agent/middleware/resilience/{retry,circuit,timeout,ratelimit}` | 1,460 | **Delete** → `llms/middleware` + `llms/ratelimit` |
| `pkg/agent/llmerrors/` | 265 | **Delete** → `llms.ProviderError`/`llms.Retryable` (see §5) |
| `pkg/agent/llm/chain.go` (`Middleware`/`Chain`/`WrapClient`) | 68 | **Delete** → `middleware.ChainChat`/`RecommendedChat` |
| `pkg/agent/middleware/validation` (empty-response) | 210 | **Keep & rework** (toolkit omits agent-aware empty-response — M3) |
| `pkg/agent/middleware/metrics` | 324 | **Keep logic, re-shape as toolkit `Observer`** (M2) |

`pkg/limiter/` (referenced in `CLAUDE.md`) no longer exists; rate limiting
already lives under `pkg/agent/middleware/resilience/ratelimit` and is fully
replaced by `llms/ratelimit`.

## 4. Architecture: the adapter seam

**Decision:** keep Maestro's `pkg/agent/llm.LLMClient` interface and its
`CompletionRequest`/`CompletionResponse`/`CompletionMessage`/`ToolCall`
types as the app-facing contract. Introduce **one** adapter implementing
`llm.LLMClient` over `llms.ChatClient`.

Rationale: ~175 references, `BaseStateMachine` (`SetLLMClient`/
`GetLLMClient`), the toolloop, and `internal/mocks.MockLLMClient` (29 refs)
all bind to `llm.LLMClient`. An adapter contains the change to ~1 new file +
`pkg/agent/factory.go`; `MockLLMClient` keeps working unchanged.

```
consumers ─► llm.LLMClient (unchanged)
                  │
            llmadapter (NEW: translate types, split system, map cache hint)
                  │
   middleware.RecommendedChat( validation? ► retry ► timeout ► circuit ► ratelimit ► metrics )
                  │
        llms.ChatClient  (anthropic | openai | google | ollama)
```

### 4.1 Type mapping (mechanical)

| Maestro | maestro-llms | Adapter responsibility | Divergence |
|---|---|---|---|
| `CompletionRequest.Messages` with in-band `role=system` | `ChatRequest.System []ContentPart` + `Messages` | Split leading/any system messages into `System`; replicate current `ensureAlternation` extraction | **A3** |
| `tools.ToolDefinition{InputSchema InputSchema}` (`pkg/tools/mcp.go:14`) | `llms.ToolDefinition{InputSchema json.RawMessage}` | `json.Marshal` the structured schema | — |
| `ToolCall.Parameters map[string]any` | `ToolCall.Parameters json.RawMessage` | Adapter `json.Unmarshal` → `map[string]any` so the 5 consumer sites stay untouched | **A2** |
| `CompletionMessage.CacheControl *CacheControl` | `ContentPart.CacheBreakpoint bool` | Non-nil `CacheControl` → `CacheBreakpoint=true` on that part | **A5** |
| `CompletionRequest.Temperature float32` | `*float32` | Pointer-wrap (preserve "0 = deterministic") | — |
| `LLMClient.GetModelName() string` | `ChatClient.Model() ModelRef` | Return `ModelRef` name | — |
| `LLMClient.Stream(...)` | n/a | Adapter returns an unsupported error or drop method (unused) | — |

**Consumer sites that read `ToolCall.Parameters` as a map (must keep working
via adapter unmarshal):**
`pkg/architect/toolloop_results.go:21,78`,
`pkg/coder/toolloop_results.go:113`,
`pkg/coder/driver.go:651,1508`.

**Consumer sites that set `CacheControl` (map to `CacheBreakpoint`):**
`pkg/coder/driver.go:159,204,210` (system prompt + last cacheable context).

## 5. Behaviors to preserve (divergence → code site)

Each row is a toolkit-intentional divergence that Maestro must absorb. This
is the cut-over acceptance checklist.

| # | Divergence | Maestro code site | Required action | Risk |
|---|---|---|---|---|
| **A3** | System via `ChatRequest.System`, text-only; not in-band | adapter; mirrors `llmimpl/anthropic` `ensureAlternation` | Adapter extracts system parts before send | Low |
| **A2** | Tool args are raw JSON, not `map[string]any` | adapter + 5 sites in §4.1 | Adapter unmarshals; no consumer change | Low |
| **A5** | `CacheBreakpoint` hint replaces `CacheControl`; only Anthropic caches | `pkg/coder/driver.go:159/204/210` | Map non-nil → hint; accept Gemini/OpenAI no longer get explicit caching | Low–med (cost only) |
| **M3** | No agent-aware empty-response retry | `pkg/agent/middleware/validation/empty_response.go` | **Keep** this middleware; rework to wrap the toolkit client and stop emitting `llmerrors.ErrorTypeEmptyResponse` (own sentinel) | Med |
| **M4 / X3** | Circuit-open = non-retryable `*middleware.CircuitOpenError`; no `ServiceUnavailableError` | `pkg/pm/working.go:546` (`llmerrors.IsServiceUnavailable`), `pkg/pm/driver.go:367`, audit architect/coder equivalents | Replace with `errors.As(err, **middleware.CircuitOpenError)` to drive SUSPEND | **Med–high** (escalation path) |
| **M2 / X4** | Neutral `Observer`; no cost/story/StateProvider | `pkg/agent/middleware/metrics/*` | Reimplement Maestro `Recorder` as a toolkit `Observer`; cost via `config.CalculateCost` stays app-side; `Usage` now reliably populated | Med |
| **M1 / X3** | Retry iff `llms.Retryable` (not blocklist) | `pkg/agent/factory.go` middleware assembly | Use `middleware.RecommendedChat`; audit flows that assumed "retry almost everything" | Med |
| **OC2 / G2** | Caller-controlled `ToolChoice`; not forced when tools present | adapter + toolloop (`pkg/agent/toolloop`) | **Verify first.** Maestro toolloop relies on the model calling tools. Preserve old behavior by setting `ToolChoice{Type: ToolChoiceRequired}` when `len(Tools)>0` (in adapter, or thread through `CompletionRequest`) | **High** |
| **G1** | Gemini thought-signature replay dropped (stateless clients) | n/a (toolkit) | Live-validate multi-turn Gemini tool loops; possible quality regression | **High** (needs live test) |
| **OL1/OL3** | Hand-rolled Ollama; raw `done_reason` | any consumer reading canonical stop strings | Audit `StopReason` consumers; remove `ollama/ollama` dep | Low |
| **X1** | SDK retries default 0; retry is middleware's job | `pkg/agent/factory.go` | Ensure every client is wrapped in retry middleware | Low |
| **X5** | `context.Canceled` passes through (not a `ProviderError`) | shutdown/cancel paths | Confirm code switches on `errors.Is(err, context.Canceled)` | Low |
| — | Daily token **budget** enforcement | old `ratelimit` config | `llms/ratelimit` does tokens/min + concurrency only. If daily budget still required → custom `ratelimit.Limiter` or app-side gate (see §7 Q3) | Med |

## 6. Phased plan

1. **Scaffold (flagged):** add `maestro-llms` dep; build `llmadapter` + a
   factory path behind a config flag; old path remains default. Build green.
2. **Middleware + observability:** wire `middleware.RecommendedChat`;
   reimplement metrics as `Observer`; map `*CircuitOpenError` → SUSPEND
   (M4); confirm retry classifier (M1).
3. **Empty-response:** keep/rework agent-aware validation around the new
   client (M3).
4. **Tool-choice & live validation:** resolve OC2/G2 (force `Required` when
   tools present, or thread `ToolChoice`); live-validate A5, G1, OL1/OL3,
   M4 against real Anthropic + Gemini + Ollama architect/coder runs.
5. **Cut over & prune:** flip default; delete `internal/llmimpl/`,
   `llmerrors/`, `resilience/{retry,circuit,timeout,ratelimit}`,
   `llm/chain.go`; drop `github.com/ollama/ollama`; update `CLAUDE.md`.

## 7. Open questions for the extraction team

1. **Tool-choice default (OC2/G2):** Maestro's toolloop assumes the model
   will call a tool each turn (old behavior forced `required`/ANY). We plan
   to set `ToolChoiceRequired` whenever `len(Tools)>0` in the adapter. Is
   that the intended migration path, or do you recommend threading an
   explicit per-request `ToolChoice` (the toolloop sometimes wants `auto`
   for the final summarizing turn)? Any precedent from Morris?

2. **Empty-response retry (M3):** Confirmed Maestro owns this app-side. Does
   the toolkit's validation middleware *reject* an empty assistant response
   structurally, or pass it through? We need it to pass through so our
   agent-aware retry sees it.

3. **Daily budget:** `llms/ratelimit` exposes tokens/min + concurrency.
   Maestro previously enforced a per-provider **daily token budget**. Is the
   intended answer "implement a custom `ratelimit.Limiter`," or is a
   budget-style limiter in scope for the toolkit?

4. **Gemini multi-turn (G1):** Without thought-signature replay, how much
   quality regression have you observed on multi-turn tool loops with
   thinking models? Any recommended mitigation short of a stateless
   encoding?

5. **`CacheBreakpoint` economics (A5):** Maestro currently marks the system
   prompt + last cacheable context. Confirm the toolkit maps exactly one (or
   N) `CacheBreakpoint` parts to Anthropic `cache_control: ephemeral`, and
   whether multiple breakpoints are supported (we set two).

## 8. Effort estimate

| Workstream | Estimate |
|---|---|
| Adapter + factory rewiring + prune | 1–2 days |
| Behavior preservation (M1–M4, OC2/G2, budget) | 2–3 days |
| Live integration validation (A5, G1, OL1, M4) | 1–2 days |
| **Total** | **~1 week**, dominated by validation, not coding |

**Net:** low architectural risk (adapter contains it); behavioral risk
concentrated in tool-choice (OC2/G2) and Gemini multi-turn (G1), neither
unit-testable — both need live agent runs.
