# Rate Limiting Implementation

**Author:** Claude Code
**Date:** 2025-01-30
**Status:** Design Complete - Ready for Implementation

## Overview

This document describes the implementation of provider-level rate limiting for LLM API calls in Maestro. The rate limiter replaces the current stub implementation with a real token bucket algorithm combined with concurrency limiting.

**This is a complete replacement** - all stub code will be removed, not maintained alongside the new implementation.

## Motivation

API providers enforce two independent types of limits:

1. **Token Rate Limits**: Total tokens consumed over time (e.g., 300,000 tokens/minute)
2. **Concurrency Limits**: Maximum simultaneous in-flight requests (e.g., 5 concurrent connections)

Without proper rate limiting:
- Multiple agents can overwhelm provider APIs with parallel requests
- We risk hitting rate limits and getting 429 errors
- No visibility into whether slowness is due to API latency vs our own congestion

## Architecture

### Provider-Level Limiting

Rate limits are enforced **per provider**, not per model:
- All Anthropic models (Claude 3, Claude 4, etc.) share one limiter
- All OpenAI models (o3, o3-mini, gpt-5, etc.) share another limiter
- All OpenAI Official models share a third limiter

Multiple agents in the same orchestrator instance compete for shared provider resources.

### Token Bucket Algorithm

Each provider maintains a token bucket that:
- Has a maximum capacity of **90% of configured tokens_per_minute** (safety buffer)
- Refills every **6 seconds** with `tokens_per_minute / 10` tokens
- Never exceeds maximum capacity
- Requests must acquire estimated tokens before making API calls

**No burst capacity** - we removed burst to avoid overrunning limits since our token estimates are approximate.

### Concurrency Limiting (Semaphore)

Each provider maintains a semaphore with `max_concurrency` slots:
- Requests must acquire a slot before making API calls
- Slots are **borrowed** (returned immediately when request completes)
- Independent from token bucket (tokens are consumed, slots are recycled)

### Atomic Acquisition

**Critical Design Decision**: Token and concurrency acquisition must be atomic.

**Why?** Non-atomic acquisition causes token over-commitment:
```
Time 0: Bucket has 100K tokens, all 5 slots busy
Time 1: Agent A acquires 100K tokens → SUCCESS (bucket now empty)
Time 2: Agent A waits for slot... bucket refills +50K
Time 3: Agent B acquires 50K tokens → SUCCESS
Time 4: Bucket refills again, Agent C acquires tokens...

Result: 190K tokens committed, but only 5 requests can run!
```

**Solution**: Acquire both resources under a single mutex:
```go
lock()
if hasTokens && hasSlot {
    consumeTokens()
    takeSlot()
    unlock()
    return releaseFunc
}
unlock()
wait_and_retry()
```

Maximum token commitment is now bounded: `max_concurrency × max_request_size`

## Stale Acquisition Cleanup

### The Problem

If a request acquires a concurrency slot but never releases it (panic, bug, etc.), the slot is leaked permanently. With 5 slots, 5 leaked slots = provider completely blocked.

### The Solution: Opportunistic Cleanup

When concurrency appears full, check for stale acquisitions and auto-release them:

```go
if activeRequests >= maxConcurrency {
    cleanStaleAcquisitions()  // Check timestamps, release if > timeout
}
```

**Benefits**:
- No background goroutine (cleanup only runs when needed)
- Self-healing without complexity
- Logs loudly when triggered (indicates a bug to investigate)

**Configuration**:
- Release timeout: **2× middleware timeout** (e.g., 6 minutes if request timeout is 3 min)
- Cleanup only runs when slots appear full

**Defense in Depth**:
1. `defer release()` handles 99.9% of cases (panic, timeout, cancellation)
2. Opportunistic cleanup catches the 0.1% edge cases
3. Loud logging when cleanup triggers (debugging aid)

## Congestion Metrics

Each limiter tracks:
- `token_limit_hits`: Number of times we had to wait for tokens to refill
- `concurrency_hits`: Number of times we had to wait for a slot

**Exposed via `/api/status` endpoint**:
```json
{
  "rate_limits": {
    "anthropic": {
      "available_tokens": 250000,
      "max_capacity": 270000,
      "active_requests": 2,
      "max_concurrency": 5,
      "token_limit_hits": 23,
      "concurrency_hits": 5
    }
  }
}
```

**Web UI Usage**:
- Distinguish between API slowness vs our own rate limiting
- Show warnings when congestion is high
- Help operators tune rate limits or add capacity

## Configuration

### Config Structure

```json
{
  "agents": {
    "resilience": {
      "rate_limit": {
        "anthropic": {
          "tokens_per_minute": 300000,
          "max_concurrency": 5
        },
        "openai": {
          "tokens_per_minute": 100000,
          "max_concurrency": 3
        },
        "openai_official": {
          "tokens_per_minute": 150000,
          "max_concurrency": 5
        }
      }
    }
  }
}
```

**Removed Fields**:
- `burst` - Removed entirely from config to avoid overrunning limits with approximate token estimates

### Default Values

Defined in `pkg/config/config.go`:
```go
var ProviderDefaults = map[string]ProviderLimits{
    ProviderAnthropic: {
        TokensPerMinute: 300000,
        MaxConcurrency:  5,
    },
    ProviderOpenAI: {
        TokensPerMinute: 100000,
        MaxConcurrency:  3,
    },
    ProviderOpenAIOfficial: {
        TokensPerMinute: 150000,
        MaxConcurrency:  5,
    },
}
```

## Implementation Details

### File Structure

```
pkg/agent/middleware/resilience/ratelimit/
├── limiter.go           # TokenBucketLimiter implementation (replaces stub)
├── middleware.go        # Middleware wrapper (already exists, minor updates)
└── limiter_test.go      # Tests (new)
```

### Core Types

```go
type TokenBucketLimiter struct {
    mu                sync.Mutex
    provider          string
    availableTokens   int
    tokensPerRefill   int       // tokens_per_minute / 10
    maxCapacity       int       // 90% of tokens_per_minute
    activeRequests    int
    maxConcurrency    int
    acquisitions      []*acquisition
    releaseTimeout    time.Duration
    tokenLimitHits    int64
    concurrencyHits   int64
}

type acquisition struct {
    timestamp time.Time
    agentID   string
}

type ProviderLimiterMap struct {
    limiters map[string]*TokenBucketLimiter
    ctx      context.Context
    cancel   context.CancelFunc
}
```

### Key Methods

```go
// Acquire tokens and concurrency slot atomically
func (l *TokenBucketLimiter) Acquire(ctx context.Context, tokens int, agentID string) (releaseFunc func(), error)

// Release concurrency slot (tokens already consumed)
func (l *TokenBucketLimiter) release(acq *acquisition)

// Opportunistic cleanup of stale acquisitions
func (l *TokenBucketLimiter) cleanStaleAcquisitions()

// Background refill timer (every 6 seconds)
func (l *TokenBucketLimiter) startRefillTimer(ctx context.Context)

// Get current statistics
func (l *TokenBucketLimiter) GetStats() LimiterStats

// Get all provider stats
func (p *ProviderLimiterMap) GetAllStats() map[string]LimiterStats
```

### Middleware Integration

The middleware (already implemented) needs minor updates to pass agentID and use release function:
```go
// OLD (stub):
if err := limiter.Acquire(ctx, totalTokens); err != nil {
    return llm.CompletionResponse{}, err
}

// NEW (real):
release, err := limiter.Acquire(ctx, totalTokens, agentID)
if err != nil {
    return llm.CompletionResponse{}, err
}
defer release()  // Always releases, even on panic

resp, err := next.Complete(ctx, req)
return resp, err
```

### Token Estimation

Uses existing `DefaultTokenEstimator` with TikToken:
- Estimates prompt tokens from request messages
- Uses configured `MaxTokens` for completion estimate
- Total tokens = prompt + MaxTokens

**Note**: TikToken is designed for OpenAI but works reasonably for Anthropic (tends to over-estimate, which is safer for rate limiting).

## Testing Strategy

### Unit Tests

1. **Token bucket refill**
   - Verify refill rate (every 6 seconds)
   - Verify 90% capacity limit
   - Verify no over-refill

2. **Atomic acquisition**
   - Concurrent goroutines acquiring tokens + slots
   - Verify no over-commitment
   - Verify proper blocking/waiting

3. **Concurrency limiting**
   - Verify max_concurrency enforcement
   - Verify slot release on defer
   - Verify slot reuse after release

4. **Stale acquisition cleanup**
   - Create stale acquisition (old timestamp)
   - Verify auto-release when slots full
   - Verify logging

5. **Metrics tracking**
   - Verify token_limit_hits increments
   - Verify concurrency_hits increments
   - Verify GetStats() returns correct values

### Integration Tests

1. **Multi-provider independence**
   - Anthropic and OpenAI limiters don't interfere
   - Each tracks metrics independently

2. **Multi-agent contention**
   - Multiple agents competing for same provider
   - Verify fair resource distribution
   - Verify no deadlocks

3. **Stress testing**
   - High concurrent load
   - Verify no race conditions
   - Verify proper cleanup under load

## Code to Remove

**Complete removal of stub implementation**:
- `stubLimiter` type and all methods (limiter.go:56-72)
- `NewStubLimiter()` function
- Any references to stub in comments

**Config cleanup**:
- Remove `burst` field from any config.json files in the repo
- Remove `orchestrator.models` section from config.json (unused legacy)

## Future Enhancements

**Out of Scope for Initial Implementation**:

1. **Daily Dollar Budget Enforcement**
   - Track actual cost (already computed in metrics middleware)
   - Enforce per-day spending limits per provider
   - Reset at midnight or configurable time

2. **Actual Token Usage Tracking for Rate Limiting**
   - We already track actual usage in metrics middleware (preserve this!)
   - Could use actual tokens from API responses for more accurate rate limiting
   - Refund unused tokens if estimate was high
   - More accurate rate limiting

3. **Per-Model Rate Limiting**
   - Some models have different limits (e.g., GPT-4 vs GPT-3.5)
   - Would require model-level limiters instead of provider-level

4. **Anthropic-Specific Token Estimation**
   - Use Anthropic's actual tokenizer instead of TikToken
   - More accurate estimates for Claude models

5. **Adaptive Rate Limiting**
   - Automatically back off when hitting 429 errors
   - Dynamically adjust limits based on API responses

## Migration Plan

1. **Implement `TokenBucketLimiter` in `limiter.go`**
   - Replace stub implementation completely
   - Remove `stubLimiter` type and `NewStubLimiter()`
   - Add atomic acquisition logic
   - Add refill timer
   - Add stale cleanup
   - Add metrics tracking

2. **Update `ProviderLimiterMap`**
   - Start refill timers for each limiter
   - Add `GetAllStats()` method
   - Add `Stop()` method to clean up timers

3. **Update middleware.go**
   - Change `Acquire()` signature to return release function
   - Pass agentID to `Acquire()`
   - Use `defer release()` pattern

4. **Update factory.go**
   - Remove stub-specific initialization
   - Pass context to `NewProviderLimiterMap()` for refill timers
   - Ensure proper shutdown on context cancellation

5. **Config cleanup**
   - Remove `burst` from config.json files
   - Remove `orchestrator.models` section from config.json

6. **Expose stats via services/status endpoint**
   - Add rate limit stats to status API
   - Web UI can display congestion metrics

7. **Add comprehensive tests**
   - Unit tests for token bucket, concurrency, cleanup
   - Integration tests for multi-provider, multi-agent scenarios

8. **Update documentation**
   - Update CLAUDE.md to reflect real rate limiting (not stub)
   - Note that token tracking in metrics middleware is preserved

## References

- Token Bucket Algorithm: https://en.wikipedia.org/wiki/Token_bucket
- Middleware Design: docs/specs/MIDDLEWARE.md
- Config Management: pkg/config/config.go
- Metrics Tracking: pkg/agent/middleware/metrics/
