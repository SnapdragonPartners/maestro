# LLM Client Middleware Migration Spec
**Status:** Draft for implementation  
**Author:** ChatGPT  
**Date:** 2025-08-08  
**Audience:** Senior Go devs & coding LLMs  
**Scope:** Convert resilience/utilities (circuit breaker, retry, timeout, rate limit, metrics) into composable middlewares around a stable `llm.Client` interface.  
**Assumption:** The subpackage migration discussed is already complete.

---

## 1) Goals & Non‑Goals

### Goals
- Introduce a **chainable middleware** pattern for all LLM calls (sync and streaming).
- Preserve the **public `agent` façade** API; existing 26+ packages continue to compile.
- Enforce **deterministic ordering** of concerns: metrics, retry, circuit breaker, rate limit, timeout, etc.
- Keep **Prometheus** behind an interface. No Prom imports leak into core packages.
- Ensure **no tokens** are consumed when the circuit is OPEN.
- Provide **high‑coverage tests** for each middleware + one integration test for ordering.

### Non‑Goals
- Changing prompt/response schemas or adding new providers.
- Overhauling state machine / driver logic beyond swapping the LLM client instance.

---

## 2) Package Layout (post‑migration)

```
pkg/agent/
  agent.go                 // public facade; composes LLM chain
  types.go, errors.go

  config/                  // public config and validation
    types.go
    validate.go

  llm/                     // public interface and chain helpers
    api.go                 // Client, Request, Response, Chunk
    chain.go               // Middleware, Chain(), clientFunc

  msg/                     // message models & validation (reusable)
    types.go
    validate.go
    stream.go

  resilience/
    circuit/               // stateful breaker + errors
      breaker.go
    retry/
      retry.go
      backoff.go
    ratelimit/
      limiter.go           // token bucket + (optional) concurrency gate
      bucket.go

  metrics/                 // public interface (no Prometheus imports)
    recorder.go            // LLMRecorder interface
    prom/                  // exporter (Prometheus only here)
      recorder.go

  internal/
    core/                  // state machine (unchanged)
    runtime/               // driver, resume, compaction (unchanged)
    effects/               // approval/question helpers (unchanged)
    llmimpl/
      anthropic/           // concrete Claude client (raw)
        client.go
      openai/              // concrete O3 (raw)
        client.go
      common/
        wrap.go            // wiring helpers shared by providers
```

---

## 3) Public Interfaces

### 3.1 `llm.Client` & Middleware

```go
// pkg/agent/llm/api.go
package llm

type Client interface {
    Complete(ctx context.Context, req Request) (Response, error)
    Stream(ctx context.Context, req Request, onChunk func(Chunk) error) error
}

type Middleware func(next Client) Client
```

```go
// pkg/agent/llm/chain.go
package llm

type clientFunc struct {
    complete func(context.Context, Request) (Response, error)
    stream   func(context.Context, Request, func(Chunk) error) error
}

func (f clientFunc) Complete(ctx context.Context, r Request) (Response, error) {
    return f.complete(ctx, r)
}
func (f clientFunc) Stream(ctx context.Context, r Request, h func(Chunk) error) error {
    return f.stream(ctx, r, h)
}

// Earlier middlewares are OUTERMOST.
func Chain(c Client, mws ...Middleware) Client {
    for i := len(mws) - 1; i >= 0; i-- {
        c = mws[i](c)
    }
    return c
}
```

### 3.2 Metrics Recorder Interface

```go
// pkg/agent/metrics/recorder.go
package metrics

type LLMRecorder interface {
    ObserveRequest(model, op, agentType string, promptTokens, completionTokens int, success bool, errType string, dur time.Duration)
    IncThrottle(model, reason string)
    ObserveQueueWait(model string, d time.Duration) // optional
}

func Nop() LLMRecorder { /* returns a no-op instance */ }
```

Prometheus adapter lives in `pkg/agent/metrics/prom` and implements `LLMRecorder`. Keep label cardinality low (model, op, agent_type, success, err_type).

---

## 4) Middlewares (Implementations)

### 4.1 Circuit Breaker

**Behavior**
- Pre‑check `Allow()`; if OPEN ⇒ fast fail (`ErrOpen`), **no token acquisition**.
- After call returns, `Record(success)`.

**Signature**
```go
// pkg/agent/resilience/circuit/middleware.go
func Middleware(breaker Breaker, rec metrics.LLMRecorder) llm.Middleware
```

**Sketch**
```go
return func(next llm.Client) llm.Client {
    return llm.clientFunc{
        complete: func(ctx context.Context, r llm.Request) (llm.Response, error) {
            if !breaker.Allow() {
                rec.IncThrottle(r.Model, "circuit_open")
                return llm.Response{}, ErrOpen
            }
            resp, err := next.Complete(ctx, r)
            breaker.Record(err == nil)
            return resp, err
        },
        stream: func(ctx context.Context, r llm.Request, h func(llm.Chunk) error) error {
            if !breaker.Allow() {
                rec.IncThrottle(r.Model, "circuit_open")
                return ErrOpen
            }
            err := next.Stream(ctx, r, h)
            breaker.Record(err == nil)
            return err
        },
    }
}
```

### 4.2 Rate Limit (Token Bucket)

**Behavior**
- Estimate tokens: `prompt = est.EstimatePrompt(req)`; `need = prompt + req.MaxOutputTokens`.
- Acquire tokens **after CB pre‑check**; on failure, increment throttle metric.

**Signature**
```go
// pkg/agent/resilience/ratelimit/middleware.go
func Middleware(l Limiter, est TokenEstimator, rec metrics.LLMRecorder) llm.Middleware
```

**Limiter Interface**
```go
type Limiter interface {
    Acquire(ctx context.Context, n int) error
    TryAcquire(n int) bool
    // Optional concurrency gate:
    AcquireSlot(ctx context.Context) error
    ReleaseSlot()
}
```

**Estimator Interface**
```go
type TokenEstimator interface {
    EstimatePrompt(req llm.Request) int
}
```

### 4.3 Retry

**Behavior**
- Attempt‑scoped retry with backoff.
- Never retry on: `context.Canceled`, `context.DeadlineExceeded`, `circuit.ErrOpen`, or classifier==NoRetry.

**Signature**
```go
// pkg/agent/resilience/retry/middleware.go
func Middleware(p Policy, classify func(error) Retriability) llm.Middleware
```

### 4.4 Timeout

**Behavior**
- Per‑attempt timeout using `context.WithTimeout`.

**Signature**
```go
// pkg/agent/resilience/timeout/middleware.go
func Middleware(d time.Duration) llm.Middleware
```

### 4.5 Metrics

**Behavior**
- Records per‑attempt latency, tokens, and outcome for both Complete and Stream.

**Signature**
```go
// pkg/agent/metrics/middleware.go
func Middleware(rec metrics.LLMRecorder, usageFrom func(r llm.Request, resp llm.Response) (prompt, completion int)) llm.Middleware
```

---

## 5) Wiring in `agent.New(...)`

**Goal:** Keep external API stable; only internals change.

```go
// pkg/agent/agent.go (excerpt)
func New(opts Options) (*Agent, error) {
    raw := llmimplanthropic.New(opts.ClaudeConfig, /* ... */) // or switch by provider

    // Build chain. Order matters: outermost first.
    client := llm.Chain(raw,
        metricsmw.Middleware(opts.Metrics, usageFrom),
        retry.Middleware(opts.RetryPolicy, classify),
        circuitmw.Middleware(opts.CircuitBreaker, opts.Metrics),
        ratelimitmw.Middleware(opts.TokenLimiter, opts.TokenEstimator, opts.Metrics),
        timeout.Middleware(opts.Timeout),
    )

    return &Agent{client: client, /* ... */}, nil
}
```

**Config additions (pkg/agent/config/types.go)**
```go
type RateLimits struct {
    TokensPerMinute int
    Burst           int
    RequestsPerMin  int
    MaxConcurrency  int
}

type MetricsConfig struct {
    Enabled   bool
    Exporter  string // "prometheus"
    Namespace string
    Subsystem string
}

type Resilience struct {
    RetryPolicy  retry.Policy
    Circuit      circuit.Config
    Timeout      time.Duration
    RateLimits   RateLimits
}
```

---

## 6) Back‑Compat & Deprecations

- Keep any old helper calls as glue for 1–2 releases:
  - e.g., `agent.NewResilientClient(...)` returns `llm.Chain(...)`.
- Add `Deprecated:` comments in exported wrappers.
- DO NOT export middlewares from `internal/llmimpl/*`; those remain raw providers.

---

## 7) Testing Plan

### Unit tests per middleware
- **CircuitBreaker:** OPEN ⇒ fast‑fail; ensure next is NOT called; success/failure updates `Record`.
- **RateLimit:** Acquire succeeds/fails; ensure throttle metric increments on fail; verify no Acquire when CB is OPEN (via chain test).
- **Retry:** retries only on retriable errors; respects context cancel; per‑attempt backoff invoked.
- **Timeout:** per‑attempt cancellation; no lingering goroutines (use `-race`).
- **Metrics:** `ObserveRequest` called with correct labels; add fake usage extractor.

### Integration (ordering) test
Compose: Metrics → Retry → CircuitBreaker → RateLimit → Timeout
- CB OPEN ⇒ RateLimit **not** invoked.
- Retriable error ⇒ N attempts observed by metrics.
- Timeout per attempt ⇒ retry cycles honor timeout without leaking.

### Fakes
- `stubClient` with programmable responses for `Complete` and `Stream`.
- `fakeBreaker`, `fakeLimiter`, `fakeRecorder` with thread‑safe counters.

### Commands
```
go test ./... -race -coverprofile=cover.out -covermode=atomic
```

---

## 8) Performance & Concurrency

- Middleware adds ~O(1) branches; target ≤ ~200ns per wrapper per call.
- Use `sync/atomic` for counters in breaker and limiter.
- Avoid allocations in hot paths (pre‑allocate labels; reuse buffers in streaming).

---

## 9) Work Items (Checklist)

- [ ] Add `llm.Client`, `Middleware`, `Chain`, `clientFunc`.
- [ ] Adapt existing Claude/OpenAI clients to satisfy `llm.Client`.
- [ ] Implement **CircuitBreaker** middleware.
- [ ] Implement **RateLimit** middleware + token bucket.
- [ ] Implement **Retry** middleware + backoff + classifier.
- [ ] Implement **Timeout** middleware.
- [ ] Implement **Metrics** middleware + `metrics.Nop()` + Prom exporter.
- [ ] Wire chain in `agent.New(...)`; add config fields + validation.
- [ ] Unit tests for each middleware.
- [ ] Integration test proving ordering semantics.
- [ ] Remove legacy resilience code paths; keep deprecated shims.
- [ ] Update docs (`README.md`, `doc.go`) and examples.

---

## 10) Example Usage

```go
rec := metrics.Nop()
cb  := circuit.New(circuit.Config{ /* ... */ })
lim := ratelimit.NewTokenBucket(tokensPerMin, burst, nil)
est := myEstimator{} // implements TokenEstimator
pol := retry.Policy{ MaxAttempts: 3, Backoff: retry.Exp(50*time.Millisecond, 2.0, 1*time.Second) }

raw := llmimplanthropic.New(cfg)
client := llm.Chain(raw,
    metricsmw.Middleware(rec, usageFrom),
    retry.Middleware(pol, classify),
    circuitmw.Middleware(cb, rec),
    ratelimitmw.Middleware(lim, est, rec),
    timeout.Middleware(30*time.Second),
)
```

---

## 11) Acceptance Criteria

- `agent` public API unchanged; LLM calls flow through chain.
- CB OPEN ⇒ **no token acquisition**.
- Retry respects classifier & context; timeout is per attempt.
- Metrics emitted for both Complete/Stream paths.
- All tests green with `-race`; overall coverage ≥ **80%**; leaf packages (resilience, metrics) ≥ **90%**.

---

## 12) Rollout Plan

1. **PR1**: Add `llm` interfaces + middlewares skeletons + tests using a stub client.
2. **PR2**: Wrap real providers (Claude/OpenAI), wire chain in `agent.New`.
3. **PR3**: Land Prom exporter + dashboards (optional), remove legacy code, keep shims.
4. **PR4**: After adoption, delete shims and update docs.

---

*End of spec.*
