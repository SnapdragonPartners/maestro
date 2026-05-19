# Maestro → `maestro-llms` Migration Spec

**Status:** Revised after extraction-team review (round 1)
**Branch:** `spec/maestro-llms-migration`
**Scope:** Replace Maestro's in-tree LLM provider clients + resilience
middleware with the extracted [`maestro-llms`](https://github.com/SnapdragonPartners/maestro-llms)
module (`github.com/SnapdragonPartners/maestro-llms`).

This document is the implementation plan and the cut-over acceptance
checklist. It maps every row of the toolkit's
[`docs/MAESTRO_DIVERGENCES.md`](https://github.com/SnapdragonPartners/maestro-llms/blob/main/docs/MAESTRO_DIVERGENCES.md)
to the concrete Maestro code site that must change.

> **Review status:** Round-1 extraction-team feedback incorporated. Changes
> from the original draft are summarized in §0. The open questions in §7 are
> resolved (kept for the record with their resolutions).

---

## 0. Changes from round-1 review

1. **New §4.2 "Adapter transcript normalization"** — the adapter must split
   Maestro's combined `RoleUser`+tool-results messages into toolkit
   `RoleTool` then optional `RoleUser`, and drop the `"Tool results:"`
   placeholder. This was missing entirely from the draft.
2. **Tool-choice is now explicit, not a blanket adapter default** (OC2/G2).
   `ToolChoice` is threaded through `toolloop.Config` and
   `CompletionRequest`; `Required` for unattended loops, `Auto` for
   finalizing turns. Reverses the original "force Required when tools
   present" plan, which would have silently changed Anthropic behavior.
3. **SUSPEND mapping promoted to its own boundary helper** (§5 M4) — a
   single Maestro-side `llmsuspend` wrapper maps `*CircuitOpenError` **and**
   exhausted retryable `*ProviderError`/`*LimitError` to the suspend path
   that `llmerrors.IsServiceUnavailable` handlers expect today.
4. **Stop-reason normalization broadened beyond Ollama** (§4.2, §5) — the
   adapter normalizes provider stop reasons to Maestro's legacy strings
   (esp. `max_tokens`) so the toolloop truncation branch keeps working.
5. **Metrics placement called out** (§5 M2) — `RecommendedChat` puts metrics
   innermost; Maestro hand-composes the chain (or adds an outer metrics
   pass) to keep observing validation/limiter/circuit/retry-exhaustion as
   one event.
6. **Daily-budget row removed** — Maestro config (`pkg/config/config.go:360`
   `ProviderLimits`) is tokens/min + concurrency only; there is no existing
   daily budget. Not a cut-over concern; logged as a possible future toolkit
   feature in §7.
7. **Go directive decided** (§2) — Maestro bumps to ≥ `go 1.26.3` (earlier
   `1.26.x` has known vulns); the toolkit's directive stays. G1 (Gemini
   multi-turn) explicitly punted to phase-5 live testing.
8. **New §9 "Ownership split"** — what lives in Maestro vs. what could move
   into the toolkit, per the team's "what belongs where."

### Round-2 review (phase-1 scaffold) fixes

9. **[P2] OpenAI `incomplete` truncation** (§8.1, §9) — the adapter now
   resolves OpenAI's real stop reason from `Raw.IncompleteDetails.Reason`
   (status `"incomplete"` alone would miss the toolloop's `max_tokens`
   guard). Upstream fix filed as a §9 toolkit candidate.
10. **[P3] Preflight honors the flag** (§2, factory `CreateRawClient`) —
    `MAESTRO_USE_LLMS` now also routes the bare preflight/key-validation
    client through the adapter, so preflight exercises the same
    implementation agents use (matters most for Ollama: old SDK vs. new
    hand-rolled HTTP differ by design).

### Phase 2 implemented (middleware + observability)

11. **Chain hand-composed, metrics OUTERMOST** (`factory_llms.go`
    `buildMaestroLLMsClient`) — order `metrics → validation → retry →
    timeout → circuit → rate limit → provider`. Resolves the M2 tradeoff by
    relocating the single metrics layer from RecommendedChat's innermost to
    outermost: one aggregate `Event` per logical call still observes
    validation/limiter/circuit-open/retry-exhaustion (matches Maestro's
    current semantics). Documented tradeoff: latency now includes retry
    backoff; per-attempt granularity is not separately emitted.
12. **M4 SUSPEND boundary = `suspendBoundary` + `mapSuspend`** — concrete
    `llm.LLMClient` (not `llm.WrapClient`, so phase-6 chain.go deletion is
    safe). Maps `*middleware.CircuitOpenError` and retry-exhausted
    retryable `*llms.ProviderError`/`*llms.LimitError` →
    `llmerrors.NewServiceUnavailableError`, preserving the existing
    `IsServiceUnavailable` handler contract. `context.Canceled` passes
    through (X5). Non-retryable provider errors (auth/bad_request) pass
    through (M1: fail fast, no longer "retry almost everything").
13. **M2 metrics = `metricsObserver`** — toolkit `Observer` →
    `metrics.Recorder`; cost via `config.CalculateCost` stays app-side;
    uses the toolkit's now-reliable `Usage` instead of tokenizer
    estimation (X4); nil-safe `stateProvider`/`logger`.
14. **Rate limiting** = per-provider `ratelimit.NewInMemoryLimiter` from
    existing `ProviderLimits` config (tokens/min + concurrency). No daily
    budget (confirmed non-existent, §7 Q3).

### Phase 3 implemented (empty-response validation rework)

15. **Validator → concrete decorator** (`validation.EmptyResponseValidator
    .Wrap`) — the agent-aware empty-response/pause-turn logic moved off
    `llm.WrapClient` (chain.go) into a concrete `emptyResponseClient`
    implementing `llm.LLMClient`, so it survives the phase-6 deletion of
    `llm/chain.go`. `Middleware()` is retained (delegates to `Wrap`) so the
    legacy path is byte-for-byte unchanged.
16. **Wired into the flag-on chain** — `buildMaestroLLMsClient` now wraps
    `suspendBoundary( validator.Wrap( adapter ) )`: the validator sits
    OUTSIDE the adapter (it mutates `req.Messages` with guidance and
    retries) but INSIDE the suspend boundary (an empty-response error is
    not a provider-down signal). Toolkit `ValidationChat` (structural) is
    still in the chain; the app-side validator handles the agent-aware
    empty/pause-turn policy the toolkit deliberately omits (M3).
17. **Sentinel deferred** — still emits `llmerrors.ErrorTypeEmptyResponse`
    so `pkg/coder/{coding,planning}.go isEmptyResponseError` keep working
    unchanged. Relocating that sentinel off the to-be-deleted `llmerrors`
    package moves to phase 6 *with its consumers* (cross-cutting; out of
    phase-3 scope).

### Phase 4 implemented (tool-choice plumbing)

18. **Canonical `ToolChoice` constants** (`llm.ToolChoice{Auto,None,
    Required}`, re-exported via `agent`) — the previously-vestigial
    `CompletionRequest.ToolChoice` string is now meaningful.
19. **Adapter honors it, no blanket default** (`llmadapter.toToolChoice`) —
    `""`→Auto (toolkit default), `required`/`any`→Required, `none`→None,
    any other value→named-tool. A Required/named choice with **no tools**
    is defensively downgraded to Auto (the toolkit would reject it). This
    is the OC2/G2 resolution: no "force tools whenever tools present".
20. **Toolloop sets `required` when it offers tools** (`toolloop.go`) — the
    loop is unattended and every iteration expects a tool call. On the
    legacy path the field is ignored (OpenAI/Gemini already forced,
    Anthropic stayed auto) so behavior is unchanged; on the maestro-llms
    path Anthropic now also requires a tool (intended explicit policy).
    Non-tool-loop callers (architect single-turn, PM text) leave it unset →
    Auto.

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
| Go version | `go 1.26` / `toolchain go1.26.1` | `go 1.26.3` | **Needs tweak** — see below |
| Anthropic SDK | `anthropic-sdk-go v1.37.0` | `v1.37.0` | Identical (now indirect) |
| OpenAI SDK | `openai-go v1.12.0` | `v1.12.0` | Identical (now indirect) |
| Gemini | `google.golang.org/genai` | `genai v1.54.0` | Compatible |
| Ollama | `github.com/ollama/ollama v0.21.0` | none (hand-rolled HTTP) | **Dep removed — clears govulncheck gate** |
| Streaming | `Stream()` defined, **unused** outside impl/middleware/tests | not implemented | Drop from adapter |

**Go directive:** `maestro-llms` declares `go 1.26.3`; Maestro declares
`go 1.26` with `toolchain go1.26.1`. Go module resolution will demand the
higher `go` directive. **Decided:** Maestro bumps its `go`/`toolchain` to
≥ `1.26.3` — the toolkit's directive stays. Rationale: significant
vulnerabilities were found in earlier `1.26.x` releases, so moving Maestro
forward is preferable to pinning the shared toolkit back. Action in phase 1
(§6 step 1): set `go 1.26.3` (or later patch) and `toolchain` accordingly in
Maestro's `go.mod`; verify the build/lint/test gate and CI Go version pass.

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
            llmadapter (NEW: type map §4.1 + transcript normalization §4.2 + llmsuspend boundary)
                  │
   hand-composed chain (≈ RecommendedChat order; metrics positioned to see aggregate failures — M2)
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

### 4.2 Adapter transcript normalization

Maestro's conversation shape is **not** what `maestro-llms` validation
accepts. The adapter must normalize the transcript on every request. Three
distinct rewrites:

**(a) Tool-result message splitting.**
`ContextManager.FlushUserBuffer` (`pkg/contextmgr/contextmgr.go:805`)
combines pending tool results **and** user-buffer text into **one**
`RoleUser` message, and when there is no buffered text it injects the
placeholder string `"Tool results:"` (added only because the Anthropic SDK
rejected an empty content field). `maestro-llms` validation
(`llms/middleware/validation.go:81`) requires tool results in a `RoleTool`
message and **rejects mixed text + tool-result content**. The adapter must
split each such Maestro message into:

```
Message{Role: RoleTool, Content: [tool_result, ...]}
(optional) Message{Role: RoleUser, Content: [text]}   // only if real text exists
```

and **drop the `"Tool results:"` placeholder** entirely (the toolkit does
not need non-empty content; emitting it would create a spurious user turn).

**(b) System extraction (A3).** Leading/in-band `role=system` messages →
`ChatRequest.System` (text-only), as in §4.1.

**(c) Stop-reason normalization.** The toolloop branches on the legacy
canonical string `"max_tokens"` to detect truncated tool calls
(`pkg/agent/toolloop/toolloop.go:357`). Toolkit stop reasons are
provider-specific and *not* normalized: OpenAI derives from response status
(`llms/providers/openai/chatconvert.go:236`), Ollama returns raw
`done_reason`, Gemini returns SDK finish enums. The adapter must map each
provider's "output truncated" / "max tokens" / "length" reason back to
Maestro's legacy `"max_tokens"` (and any other canonical strings consumers
or tests assert on) so the toolloop and existing tests keep working without
touching every consumer. Audit all `resp.StopReason ==` comparisons before
choosing the canonical set.

## 5. Behaviors to preserve (divergence → code site)

Each row is a toolkit-intentional divergence that Maestro must absorb. This
is the cut-over acceptance checklist.

| # | Divergence | Maestro code site | Required action | Risk |
|---|---|---|---|---|
| **A3** | System via `ChatRequest.System`, text-only; not in-band | adapter §4.2(b); mirrors `llmimpl/anthropic` `ensureAlternation` | Adapter extracts system parts before send | Low |
| **A2** | Tool args are raw JSON, not `map[string]any` | adapter + 5 sites in §4.1 | Adapter unmarshals; no consumer change | Low |
| **TR** | Mixed text + tool-result message rejected by toolkit validation (toolkit-structural, not a numbered divergence) | `pkg/contextmgr/contextmgr.go:805` → adapter §4.2(a); validation `llms/middleware/validation.go:81` | Split into `RoleTool` + optional `RoleUser`; drop `"Tool results:"` placeholder | **Med** |
| **A5** | `CacheBreakpoint` hint replaces `CacheControl`; only Anthropic caches (≤4 markers) | `pkg/coder/driver.go:159/204/210` | Map non-nil → hint; we set 2, under the cap; Gemini/OpenAI lose explicit caching (cost only) | Low–med (cost only) |
| **M3** | No agent-aware empty-response retry; toolkit **passes empty through** | `pkg/agent/middleware/validation/empty_response.go` | **Keep** this middleware; rework to wrap the toolkit client and stop emitting `llmerrors.ErrorTypeEmptyResponse` (own sentinel) | Med |
| **M4 / X3** | Circuit-open = non-retryable `*middleware.CircuitOpenError`; exhausted retry returns the **final retryable error** (no synthesized `ServiceUnavailableError`) | old behavior `pkg/agent/middleware/resilience/retry/middleware.go:57`; consumers `pkg/pm/working.go:546`, `pkg/pm/driver.go:367`, `pkg/coder/planning.go:284`, audit architect equivalents | Add a Maestro-side boundary helper (`llmsuspend`) that maps `*CircuitOpenError` **and** exhausted retryable `*llms.ProviderError`/`*llms.LimitError` into the existing suspend path; keep `llmerrors.IsServiceUnavailable`-style call sites working via this helper | **Med–high** (escalation path) |
| **M2 / X4** | Neutral `Observer`, **innermost** in `RecommendedChat` (does not see validation/limiter/circuit-open/retry-exhaustion) | `pkg/agent/middleware/metrics/*`, `pkg/agent/factory.go:240`, `llms/middleware/recommended.go:31` | Reimplement `Recorder` as an `Observer` (cost via `config.CalculateCost` app-side; `Usage` now reliable) **and** hand-compose the chain or add an outer metrics pass so aggregate failures are still observed | Med |
| **M1 / X3** | Retry iff `llms.Retryable` (not blocklist) | `pkg/agent/factory.go` middleware assembly | Use `middleware.RecommendedChat` or hand-composed chain; audit flows that assumed "retry almost everything" | Med |
| **OC2 / G2** | Caller-controlled `ToolChoice`; Anthropic default was `auto`, OpenAI/Gemini forced internally | `pkg/agent/toolloop/toolloop.go:303` (no `ToolChoice` set today); old defaults `anthropic/client.go:437`, `openaiofficial/client.go:181`, `google/client.go:80` | **Thread explicit `ToolChoice`** through `toolloop.Config` + `CompletionRequest`: `Required` for unattended tool loops, `Auto` for text/finalizing turns. **No blanket adapter default** (would change Anthropic behavior) | **High** |
| **G1** | Gemini thought-signature replay dropped (stateless clients) | n/a (toolkit) | Live-validate multi-turn Gemini tool loops; possible quality regression. If real degradation, request stateless encoding in toolkit (§9) | **High** (needs live test) |
| **OL1/OL3** | Hand-rolled Ollama; raw `done_reason`; provider-specific stop reasons broadly | adapter §4.2(c); `pkg/agent/toolloop/toolloop.go:357`; `llms/providers/openai/chatconvert.go:236` | Normalize stop reasons to legacy canonical strings in adapter; remove `ollama/ollama` dep | **Med** (was Low — broader than Ollama) |
| **X1** | SDK retries default 0; retry is middleware's job | `pkg/agent/factory.go` | Ensure every client is wrapped in retry middleware | Low |
| **X5** | `context.Canceled` passes through (not a `ProviderError`) | shutdown/cancel paths | Confirm code switches on `errors.Is(err, context.Canceled)` | Low |

## 6. Phased plan

1. **Scaffold (flagged):** align Go directives (§2); add `maestro-llms` dep;
   build `llmadapter` (type mapping §4.1 + transcript normalization §4.2) +
   a factory path behind a config flag; old path remains default. Build
   green.
2. **Middleware + observability:** hand-compose the chain (or
   `RecommendedChat` + outer metrics pass) so metrics still see aggregate
   failures (M2); reimplement `Recorder` as `Observer`; add the `llmsuspend`
   boundary helper mapping `*CircuitOpenError` + exhausted retryable errors
   to SUSPEND (M4); confirm retry classifier (M1).
3. **Empty-response:** keep/rework agent-aware validation around the new
   client (M3); toolkit passes empty responses through (confirmed), so the
   middleware still sees them.
4. **Tool-choice plumbing:** thread explicit `ToolChoice` through
   `toolloop.Config` + `CompletionRequest` — `Required` for unattended tool
   loops, `Auto` for finalizing turns (OC2/G2). No blanket adapter default.
5. **Acceptance tests (see §8.1):** unit-test transcript normalization,
   suspend mapping, and stop-reason normalization; then live-validate A5,
   G1, OL1/OL3, M4 against real Anthropic + Gemini + Ollama architect/coder
   runs.
6. **Cut over & prune:** flip default; delete `internal/llmimpl/`,
   `llmerrors/`, `resilience/{retry,circuit,timeout,ratelimit}`,
   `llm/chain.go`; drop `github.com/ollama/ollama`; update `CLAUDE.md`.

## 7. Open questions — resolved in round 1

1. **Tool-choice default (OC2/G2):** ✅ **Resolved.** Do *not* use a blanket
   adapter default (it would silently change Anthropic's `auto` behavior).
   Thread explicit `ToolChoice` through `toolloop.Config` /
   `CompletionRequest`: `Required` for unattended loops, `Auto` for
   finalizing turns. (§5 OC2/G2, §6 step 4.)

2. **Empty-response retry (M3):** ✅ **Resolved.** Toolkit validation
   *passes* empty assistant responses through; Maestro's agent-aware retry
   still sees them. Keep the middleware app-side.

3. **Daily budget:** ✅ **Resolved — non-issue.** Current Maestro config
   (`pkg/config/config.go:360` `ProviderLimits`; `docs/RATE_LIMIT.md:341`)
   enforces tokens/minute + concurrency only — there is **no** existing
   daily token/dollar budget. Removed from the cut-over checklist. A generic
   composable window/quota limiter is logged as a possible future toolkit
   feature (§9), not migration scope.

4. **Gemini multi-turn (G1):** ⏳ **Explicitly punted to real testing.** No
   mitigation pre-committed and not investigated further now — we accept the
   unknown and will only assess after live multi-turn Gemini tool-loop runs
   during phase 5. If those runs show real degradation, request a stateless
   thought-signature encoding in the toolkit (§9). Not a blocker for the
   architecture or for starting implementation.

5. **`CacheBreakpoint` economics (A5):** ✅ **Resolved.** Toolkit supports
   multiple breakpoints up to Anthropic's 4-marker cap; Maestro sets 2
   (system prompt + last cacheable context) — within the cap.

## 8. Effort estimate

| Workstream | Estimate |
|---|---|
| Adapter (type map + transcript normalization) + factory rewiring + prune | 2–3 days |
| Behavior preservation (M1–M4, OC2/G2 plumbing, TR split) | 2–3 days |
| Acceptance tests + live integration validation | 2 days |
| **Total** | **~1.5 weeks**, dominated by adapter normalization + validation |

**Net:** low architectural risk (adapter contains it); behavioral risk
concentrated in tool-choice plumbing (OC2/G2), transcript normalization
(TR), suspend mapping (M4), and Gemini multi-turn (G1) — the last is the
only one not unit-testable and needs live agent runs.

### 8.1 Acceptance tests — status

Per round-1 feedback, strengthen tests around the behaviors most likely to
regress silently. **Status after phase-5:** transcript normalization,
tool-choice, empty-response, and stop-reason are unit-covered; the two
fault-only paths (M4 suspend, P2 OpenAI-`incomplete`) — which can't be
exercised by happy-path live runs — are now closed by **deterministic,
hermetic tests** (no API keys, normal CI gate), so they are **no longer
cut-over blockers**:

- **M4 — `TestSuspendBoundary_EndToEnd`** (`pkg/agent`): a toolkit-typed
  terminal error (`*CircuitOpenError`, retry-exhausted `*ProviderError`,
  `*LimitError`) → real `llmadapter.Wrap` (production `fmt.Errorf %w`
  wrapping) → real `suspendBoundary` → asserts
  `llmerrors.IsServiceUnavailable`; non-retryable/`context.Canceled` pass
  through. Uses a `testllm` fake as the provider — Maestro tests only its
  own translation of the toolkit's error *contract*, not toolkit
  retry/circuit internals (those are the toolkit's to test).
- **P2 — `TestOpenAIIncompleteRawCouplingGuard`**
  (`pkg/agent/internal/llmadapter`): real `openai-go` SDK + toolkit OpenAI
  provider via `httptest`, returning a truncated Responses body
  (`status:"incomplete"`, `incomplete_details.reason:"max_output_tokens"`,
  a function call). Asserts the adapter yields `StopReason "max_tokens"`
  and preserves the tool call. **Narrow drift guard only** for the `Raw`
  coupling — not a re-test of the toolkit's parser. Delete this test *and*
  the `rawStopReason` workaround when the upstream fix (below) lands.

Detailed list of the unit-covered behaviors:

- **Transcript normalization (§4.2):** table tests asserting Maestro
  `RoleUser`+tool-results+placeholder messages produce exactly
  `RoleTool` (+ optional `RoleUser`), placeholder dropped, and that the
  result passes `llms/middleware` validation.
- **Suspend mapping (M4):** `*CircuitOpenError`, exhausted-retry
  `*ProviderError`, and `*LimitError` each route through `llmsuspend` to the
  same SUSPEND outcome the `llmerrors.IsServiceUnavailable` handlers
  (`pkg/pm/working.go:546`, `pkg/coder/planning.go:284`) expect today.
- **Stop-reason normalization (§4.2c):** each provider's truncation/length
  reason maps to legacy `"max_tokens"`, exercising the toolloop truncation
  branch (`toolloop.go:357`). **Includes the OpenAI `incomplete` case:**
  maestro-llms v0.4.0 maps OpenAI's *response status* to `StopReason`, so a
  max-output truncation arrives as `"incomplete"`, not a length reason. The
  adapter resolves the real reason from `resp.Raw.(*responses.Response)
  .IncompleteDetails.Reason` (`"max_output_tokens"`→`max_tokens`,
  `"content_filter"`→`refusal`) before generic normalization. This is a
  deliberate, provider-scoped, defensive coupling to `Raw` (outside the
  toolkit stability contract) until the toolkit surfaces the incomplete
  reason itself — see §9.

## 9. Ownership split (per round-1 "what belongs where")

**Stays in Maestro:** the `llm.LLMClient` adapter; transcript normalization
(ToolResults split, system extraction, stop-reason mapping); explicit
tool-choice policy; empty-response / pause-turn behavior; the `llmsuspend`
boundary mapping; cost/story metrics; rate-limit UI stat shape; any
Maestro-specific budget policy.

**Upstream ask filed to maestro-llms (P2):** the OpenAI provider should
surface the incomplete reason in `ChatResponse.StopReason`. Today
`StopReason = StopReason(resp.Status)`, so a max-output truncation arrives
as `"incomplete"` and the real reason is only in
`Raw.(*responses.Response).IncompleteDetails.Reason`. Requested: when
`status=="incomplete"`, derive `StopReason` from `incomplete_details.reason`
(or expose a typed field). Provider-level hermetic test owned upstream
(`httptest` Responses body with `incomplete`/`max_output_tokens` + a
function call → assert `StopReason` reflects truncation, tool calls
preserved). **Until that lands, Maestro carries a temporary `rawStopReason`
`Raw` workaround guarded by `TestOpenAIIncompleteRawCouplingGuard`; both are
deleted at the toolkit bump that ships the fix.**

**Could move into `maestro-llms`** if multiple consumers need it (out of
migration scope; raise separately): a generic
retry-exhausted error or observer signal (so consumers don't reconstruct
"service unavailable" from the final error); a generic composable
daily/window quota limiter; possibly a stateless Gemini thought-signature
encoding *if* G1 live validation shows real degradation.

**Already well-covered in `maestro-llms`** (no Maestro work):
`ToolChoiceRequired`, empty-response pass-through, `CacheBreakpoint` with
Anthropic's 4-marker cap, typed provider errors, the generic limiter
interface.
