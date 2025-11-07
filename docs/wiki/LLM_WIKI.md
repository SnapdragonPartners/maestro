# Maestro LLM Abstraction and API Implementation

## Overview

Maestro provides a sophisticated LLM (Large Language Model) abstraction layer that enables AI agents to interact with multiple LLM providers through a unified interface. The system is built on a middleware architecture that provides resilience, observability, cost control, and flexible provider integration.

The LLM system is built on five core principles:
1. **Provider Abstraction**: Unified interface works with any LLM provider (Anthropic, OpenAI, future providers)
2. **Middleware Composition**: Modular, composable middleware for cross-cutting concerns
3. **Resilience First**: Automatic retries, circuit breakers, timeouts, and rate limiting
4. **Cost Awareness**: Token tracking, cost calculation, and budget enforcement
5. **Observability**: Comprehensive metrics, logging, and error classification

**Living Documentation Notice**: The Maestro LLM system evolves as new models and providers emerge. While this document covers the current architecture, the definitive source of truth is always the codebase:
- `pkg/agent/llm/` - Core LLM interface and middleware chain
- `pkg/agent/internal/llmimpl/` - Provider implementations
- `pkg/agent/middleware/` - Middleware implementations
- `pkg/config/config.go` - Model registry and configuration

Model examples and configurations in this document are illustrative and may not represent the complete current catalog.

## Table of Contents

1. [LLM Client Interface](#llm-client-interface)
2. [Provider Implementations](#provider-implementations)
3. [Middleware Architecture](#middleware-architecture)
4. [Resilience Middleware](#resilience-middleware)
5. [Rate Limiting System](#rate-limiting-system)
6. [Metrics and Observability](#metrics-and-observability)
7. [Validation and Error Recovery](#validation-and-error-recovery)
8. [Configuration and Model Management](#configuration-and-model-management)
9. [Chat Integration Middleware](#chat-integration-middleware)
10. [Error Classification](#error-classification)
11. [Key Implementation Patterns](#key-implementation-patterns)

---

## LLM Client Interface

All LLM interactions go through a unified `LLMClient` interface that abstracts provider-specific details.

### Core Interface (`pkg/agent/llm/api.go:88-98`)

```go
type LLMClient interface {
    Complete(ctx context.Context, in CompletionRequest) (CompletionResponse, error)
    Stream(ctx context.Context, in CompletionRequest) (<-chan StreamChunk, error)
    GetModelName() string
}
```

### Request Structure

**CompletionRequest** (`pkg/agent/llm/api.go:61-70`):

```go
type CompletionRequest struct {
    Messages    []CompletionMessage        // Conversation history
    Tools       []tools.ToolDefinition     // Available tools for LLM
    ToolChoice  string                     // "auto", "any", "tool"
    MaxTokens   int                        // Maximum response tokens
    Temperature float32                    // Sampling temperature (0-1)
}
```

**CompletionMessage** (`pkg/agent/llm/api.go:45-50`):

```go
type CompletionMessage struct {
    Content      string                    // Message text
    CacheControl *CacheControl             // Anthropic prompt caching
    Role         CompletionRole            // system, user, assistant
}
```

**Message Roles** (`pkg/agent/llm/api.go:12-22`):
- `RoleSystem` - System instructions and context
- `RoleUser` - User messages and prompts
- `RoleAssistant` - AI assistant responses

### Response Structure

**CompletionResponse** (`pkg/agent/llm/api.go:72-79`):

```go
type CompletionResponse struct {
    ToolCalls  []ToolCall                 // Tools the LLM wants to call
    Content    string                     // Text response
    StopReason string                     // end_turn, max_tokens, pause_turn, refusal
}
```

**ToolCall** (`pkg/agent/llm/api.go:54-59`):

```go
type ToolCall struct {
    ID         string                     // Unique identifier for correlation
    Name       string                     // Tool name to execute
    Parameters map[string]any             // JSON arguments for tool
}
```

### Streaming Support

**StreamChunk** (`pkg/agent/llm/api.go:81-86`):

```go
type StreamChunk struct {
    Content string                        // Incremental content
    Done    bool                          // Stream complete
    Error   error                         // Stream error if any
}
```

**Usage Pattern**:

```go
stream, err := client.Stream(ctx, request)
if err != nil {
    return err
}

for chunk := range stream {
    if chunk.Error != nil {
        return chunk.Error
    }
    if chunk.Done {
        break
    }
    // Process incremental content
    fmt.Print(chunk.Content)
}
```

---

## Provider Implementations

Maestro currently supports two LLM providers with distinct integration approaches.

### Anthropic Claude Client

**Implementation**: `pkg/agent/internal/llmimpl/anthropic/client.go`

**Key Features**:

1. **Message Alternation** (`client.go:91-187`)

Anthropic requires strict user↔assistant message alternation. The client handles this automatically:

```go
// Extract system messages to top-level parameter
var systemPrompt string
for _, msg := range messages {
    if msg.Role == "system" {
        systemPrompt += msg.Content + "\n\n"
    }
}

// Merge consecutive user messages
var alternatingMessages []Message
currentUserContent := ""
for _, msg := range messages {
    if msg.Role == "user" {
        currentUserContent += msg.Content + "\n\n"
    } else if msg.Role == "assistant" {
        if currentUserContent != "" {
            alternatingMessages = append(alternatingMessages, UserMessage(currentUserContent))
            currentUserContent = ""
        }
        alternatingMessages = append(alternatingMessages, msg)
    }
}

// Ensure ends with user message
if currentUserContent != "" {
    alternatingMessages = append(alternatingMessages, UserMessage(currentUserContent))
}
```

2. **Prompt Caching** (`client.go:211-244`)

Reduces cost and latency for repeated prompts using Anthropic's cache control:

```go
func (c *ClaudeClient) Complete(ctx context.Context, in CompletionRequest) {
    var messages []Message
    for _, msg := range in.Messages {
        content := []anthropic.ContentBlock{
            anthropic.NewTextBlock(msg.Content),
        }

        // Apply cache control if specified
        if msg.CacheControl != nil {
            content[0].CacheControl = &anthropic.CacheControl{
                Type: anthropic.CacheControlTypeEphemeral,
                TTL:  msg.CacheControl.TTL,  // "5m" or "1h"
            }
        }

        messages = append(messages, Message{
            Role:    msg.Role,
            Content: content,
        })
    }
    // ...
}
```

**Usage in agents**:

```go
messages := []CompletionMessage{
    {
        Role:    RoleSystem,
        Content: systemPrompt,
        CacheControl: &CacheControl{TTL: "1h"},  // Cache for 1 hour
    },
    {
        Role:    RoleUser,
        Content: userPrompt,
    },
}
```

3. **Tool Integration** (`client.go:266-340`)

Converts generic tool definitions to Anthropic format:

```go
// Convert tools.ToolDefinition to Anthropic format
anthropicTools := make([]Tool, len(in.Tools))
for i, tool := range in.Tools {
    anthropicTools[i] = Tool{
        Name:        tool.Name,
        Description: tool.Description,
        InputSchema: tool.InputSchema,  // Direct mapping
    }
}

// Convert tool choice
var toolChoice interface{}
switch in.ToolChoice {
case "auto":
    toolChoice = ToolChoiceAuto
case "any":
    toolChoice = ToolChoiceAny
default:
    toolChoice = ToolChoice{Type: "tool", Name: in.ToolChoice}
}
```

4. **Error Classification** (`client.go:415-482`)

Maps Anthropic errors to structured error types for retry logic:

```go
func classifyError(err error) llmerrors.ErrorType {
    errStr := strings.ToLower(err.Error())

    // Check for rate limiting
    if strings.Contains(errStr, "429") || strings.Contains(errStr, "rate limit") {
        return llmerrors.ErrorTypeRateLimit
    }

    // Check for authentication
    if strings.Contains(errStr, "401") || strings.Contains(errStr, "authentication") {
        return llmerrors.ErrorTypeAuth
    }

    // Check for network/timeout issues
    if strings.Contains(errStr, "timeout") || strings.Contains(errStr, "connection") {
        return llmerrors.ErrorTypeTransient
    }

    // Check for server errors (retryable)
    if strings.Contains(errStr, "500") || strings.Contains(errStr, "503") {
        return llmerrors.ErrorTypeTransient
    }

    // Check for bad prompts (non-retryable)
    if strings.Contains(errStr, "400") || strings.Contains(errStr, "invalid") {
        return llmerrors.ErrorTypeBadPrompt
    }

    return llmerrors.ErrorTypeUnknown
}
```

### OpenAI Official Client

**Implementation**: `pkg/agent/internal/llmimpl/openaiofficial/client.go`

**Key Features**:

1. **Responses API Integration** (`client.go:71-113`)

Uses OpenAI's Responses API optimized for GPT-5/o4 reasoning models:

```go
func (o *OfficialClient) Complete(ctx context.Context, in CompletionRequest) {
    // Combine messages into single input string for Responses API
    var inputText string
    for _, msg := range in.Messages {
        if msg.Role == "system" {
            inputText += fmt.Sprintf("System: %s\n\n", msg.Content)
        } else if msg.Role == "user" {
            inputText += msg.Content
        } else if msg.Role == "assistant" {
            inputText += fmt.Sprintf("Assistant: %s\n\n", msg.Content)
        }
    }

    // Cap MaxTokens to model's actual limit
    maxTokens := in.MaxTokens
    if modelInfo, exists := config.KnownModels[o.model]; exists {
        if maxTokens > modelInfo.MaxOutputTokens {
            maxTokens = modelInfo.MaxOutputTokens
        }
    }

    // Create Responses API request
    params := responses.ResponseNewParams{
        Model:           o.model,
        MaxOutputTokens: openai.Int(int64(maxTokens)),
        Input:           responses.ResponseNewParamsInputUnion{
            OfString: openai.String(inputText),
        },
    }

    resp, err := o.client.Responses.New(ctx, params)
    // ...
}
```

2. **Recursive Schema Conversion** (`client.go:40-69`)

Critical for tools with nested structures (arrays of objects, nested properties):

```go
// convertPropertyToSchema recursively converts a Property to OpenAI schema format
func convertPropertyToSchema(prop *tools.Property) map[string]interface{} {
    schema := map[string]interface{}{
        "type":        prop.Type,
        "description": prop.Description,
    }

    // Add enum if present
    if len(prop.Enum) > 0 {
        schema["enum"] = prop.Enum
    }

    // Handle array items recursively
    if prop.Type == "array" && prop.Items != nil {
        schema["items"] = convertPropertyToSchema(prop.Items)  // Recursive
    }

    // Handle object properties recursively
    if prop.Type == "object" && prop.Properties != nil {
        properties := make(map[string]interface{})
        for name, childProp := range prop.Properties {
            if childProp != nil {
                properties[name] = convertPropertyToSchema(childProp)  // Recursive
            }
        }
        schema["properties"] = properties
    }

    return schema
}
```

**Why This Matters**: The OpenAI Responses API strictly requires explicit `items` fields for arrays and `properties` fields for objects. Without recursive conversion, nested structures fail validation with errors like:

```
Invalid schema for function 'submit_stories': In context=('properties', 'requirements'),
array schema missing items.
```

3. **Response Processing** (`client.go:147-186`)

Extracts text and tool calls from Responses API format:

```go
// Process response output
for _, item := range resp.Output {
    switch item.Type {
    case "text":
        // Extract text content
        continue
    case "function_call":
        // Tool/function calls
        funcItem := item.AsFunctionCall()
        var parameters map[string]interface{}
        json.Unmarshal([]byte(funcItem.Arguments), &parameters)

        toolCalls = append(toolCalls, ToolCall{
            ID:         funcItem.ID,
            Name:       funcItem.Name,
            Parameters: parameters,
        })
    case "reasoning":
        // GPT-5 internal reasoning - don't include in final content
        continue
    }
}

// Fallback to built-in OutputText() if no text extracted
if content == "" {
    content = resp.OutputText()
}
```

---

## Middleware Architecture

Maestro uses a functional middleware pattern for composing LLM client behavior.

### Middleware Type (`pkg/agent/llm/chain.go:8-10`)

```go
type Middleware func(next LLMClient) LLMClient
```

Each middleware:
- Receives the next client in the chain
- Returns a wrapped client
- Can intercept requests and responses
- Can short-circuit the chain

### Chain Function (`pkg/agent/llm/chain.go:51-68`)

```go
func Chain(base LLMClient, middlewares ...Middleware) LLMClient {
    client := base
    // Apply middleware in reverse order
    for i := len(middlewares) - 1; i >= 0; i-- {
        client = middlewares[i](client)
    }
    return client
}
```

**Execution Order**: Middlewares wrap from outside to inside.

Given: `Chain(base, mw1, mw2, mw3)`

Execution flow:
```
Request:  mw1 → mw2 → mw3 → base client
Response: base client → mw3 → mw2 → mw1
```

### Factory Assembly (`pkg/agent/factory.go:191-199`)

The LLM factory assembles the complete middleware stack:

```go
client := llm.Chain(rawClient,
    validator.Middleware(),                      // Empty response validation
    metrics.Middleware(...),                     // Metrics recording
    circuit.Middleware(breaker),                 // Circuit breaker
    retry.Middleware(policy),                    // Retry logic
    logging.EmptyResponseLoggingMiddleware(),    // Debug logging
    ratelimit.Middleware(...),                   // Token bucket + concurrency
    timeout.Middleware(timeout),                 // Per-request timeout
)
```

**Stack Responsibilities**:

1. **Timeout** (innermost): Sets deadline for request
2. **Rate Limit**: Acquires tokens and concurrency slot
3. **Logging**: Records empty response details for debugging
4. **Retry**: Retries transient failures with backoff
5. **Circuit Breaker**: Prevents cascading failures
6. **Metrics**: Records latency, tokens, cost
7. **Validator** (outermost): Handles empty responses with guidance

### Middleware Example: Custom Logging

```go
func LoggingMiddleware(logger *log.Logger) llm.Middleware {
    return func(next llm.LLMClient) llm.LLMClient {
        return &loggingClient{
            next:   next,
            logger: logger,
        }
    }
}

type loggingClient struct {
    next   llm.LLMClient
    logger *log.Logger
}

func (c *loggingClient) Complete(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
    c.logger.Info("LLM request: %d messages, %d tools", len(req.Messages), len(req.Tools))

    resp, err := c.next.Complete(ctx, req)  // Call next in chain

    if err != nil {
        c.logger.Error("LLM error: %v", err)
        return resp, err
    }

    c.logger.Info("LLM response: %d tool calls, %d chars", len(resp.ToolCalls), len(resp.Content))
    return resp, nil
}

func (c *loggingClient) Stream(ctx context.Context, req llm.CompletionRequest) (<-chan llm.StreamChunk, error) {
    c.logger.Info("LLM stream request: %d messages", len(req.Messages))
    return c.next.Stream(ctx, req)
}

func (c *loggingClient) GetModelName() string {
    return c.next.GetModelName()
}
```

---

## Resilience Middleware

The resilience layer provides automatic recovery from transient failures through retry, circuit breaking, timeout, and rate limiting.

### Retry Middleware

**Implementation**: `pkg/agent/middleware/resilience/retry/`

**Configuration** (`policy.go:14-21`):

```go
type Config struct {
    MaxAttempts   int           // Maximum retry attempts
    InitialDelay  time.Duration // First retry delay
    MaxDelay      time.Duration // Maximum retry delay
    BackoffFactor float64       // Exponential backoff multiplier
    Jitter        bool          // Add randomization to delays
}
```

**Default Configs** (per error type):

```go
// Empty Response: 5 retries, 2s → 30s
llmerrors.ErrorTypeEmptyResponse: {
    MaxAttempts:   5,
    InitialDelay:  2 * time.Second,
    MaxDelay:      30 * time.Second,
    BackoffFactor: 2.0,
    Jitter:        true,
}

// Rate Limit: 6 retries, 1s → 60s
llmerrors.ErrorTypeRateLimit: {
    MaxAttempts:   6,
    InitialDelay:  1 * time.Second,
    MaxDelay:      60 * time.Second,
    BackoffFactor: 2.0,
    Jitter:        true,
}

// Transient: 4 retries, 500ms → 10s
llmerrors.ErrorTypeTransient: {
    MaxAttempts:   4,
    InitialDelay:  500 * time.Millisecond,
    MaxDelay:      10 * time.Second,
    BackoffFactor: 2.0,
    Jitter:        true,
}
```

**Error Classification** (`policy.go:38-89`):

```go
func shouldRetry(err error) bool {
    errType := llmerrors.TypeOf(err)

    switch errType {
    case llmerrors.ErrorTypeRateLimit,
         llmerrors.ErrorTypeTransient,
         llmerrors.ErrorTypeEmptyResponse:
        return true  // Retryable

    case llmerrors.ErrorTypeAuth,
         llmerrors.ErrorTypeBadPrompt:
        return false  // Non-retryable

    case llmerrors.ErrorTypeUnknown:
        return true  // One retry for unknown errors
    }

    // Never retry context cancellation
    if errors.Is(err, context.Canceled) {
        return false
    }

    // Never retry circuit breaker errors
    if errors.Is(err, circuit.ErrCircuitOpen) {
        return false
    }

    return false
}
```

**Exponential Backoff** (`middleware.go:14-103`):

```go
func calculateBackoff(attempt int, config Config) time.Duration {
    delay := config.InitialDelay

    // Exponential backoff
    for i := 0; i < attempt; i++ {
        delay = time.Duration(float64(delay) * config.BackoffFactor)
        if delay > config.MaxDelay {
            delay = config.MaxDelay
            break
        }
    }

    // Add jitter to prevent thundering herd
    if config.Jitter {
        jitter := time.Duration(rand.Float64() * float64(delay) * 0.1)  // ±10%
        delay = delay - jitter/2 + time.Duration(rand.Float64()*float64(jitter))
    }

    return delay
}
```

**Retry Loop**:

```go
var lastErr error
for attempt := 0; attempt < maxAttempts; attempt++ {
    resp, err := next.Complete(ctx, req)

    if err == nil {
        return resp, nil  // Success
    }

    lastErr = err

    if !shouldRetry(err) {
        return resp, err  // Non-retryable error
    }

    if attempt < maxAttempts-1 {
        backoff := calculateBackoff(attempt, config)
        logger.Info("Retry %d/%d after %v: %v", attempt+1, maxAttempts, backoff, err)

        select {
        case <-time.After(backoff):
            // Continue to next attempt
        case <-ctx.Done():
            return resp, ctx.Err()  // Context cancelled
        }
    }
}

return llm.CompletionResponse{}, fmt.Errorf("failed after %d attempts: %w", maxAttempts, lastErr)
```

### Circuit Breaker

**Implementation**: `pkg/agent/middleware/resilience/circuit/`

**States** (`breaker.go:10-18`):

```go
const (
    Closed   State = iota  // Normal operation, requests allowed
    Open                   // Failing, requests rejected immediately
    HalfOpen               // Testing recovery, limited requests allowed
)
```

**State Machine**:

```
          consecutive failures
    Closed ─────────────────────→ Open
      ↑                            │
      │                            │ timeout
      │                            ↓
      └───────────────────────── HalfOpen
        consecutive successes       │
                                    │ any failure
                                    └──→ Open
```

**Configuration** (`breaker.go:34-38`):

```go
type Config struct {
    FailureThreshold int           // Consecutive failures to open
    SuccessThreshold int           // Consecutive successes to close
    Timeout          time.Duration // Time before HalfOpen attempt
}
```

**Default Configs**:

```go
// Per-provider circuit breakers
config.CircuitBreaker = map[string]CircuitBreakerConfig{
    "anthropic": {
        FailureThreshold: 5,    // Open after 5 consecutive failures
        SuccessThreshold: 2,    // Close after 2 consecutive successes
        Timeout:          30s,  // Try recovery after 30 seconds
    },
    "openai": {
        FailureThreshold: 5,
        SuccessThreshold: 2,
        Timeout:          30s,
    },
}
```

**Allow Logic** (`breaker.go:94-118`):

```go
func (b *Breaker) Allow() error {
    b.mu.Lock()
    defer b.mu.Unlock()

    switch b.state {
    case Closed:
        return nil  // Always allow

    case Open:
        // Check if timeout has passed
        if time.Since(b.lastFailureTime) > b.config.Timeout {
            b.state = HalfOpen
            b.consecutiveSuccesses = 0
            return nil  // Transition to HalfOpen, allow request
        }
        return ErrCircuitOpen  // Reject request

    case HalfOpen:
        return nil  // Always allow (testing recovery)
    }

    return nil
}
```

**Record Logic** (`breaker.go:120-183`):

```go
func (b *Breaker) RecordSuccess() {
    b.mu.Lock()
    defer b.mu.Unlock()

    b.consecutiveFailures = 0

    switch b.state {
    case HalfOpen:
        b.consecutiveSuccesses++
        if b.consecutiveSuccesses >= b.config.SuccessThreshold {
            b.state = Closed  // Recovery successful
            b.consecutiveSuccesses = 0
        }
    case Closed:
        // Already closed, nothing to do
    }
}

func (b *Breaker) RecordFailure() {
    b.mu.Lock()
    defer b.mu.Unlock()

    b.consecutiveSuccesses = 0
    b.lastFailureTime = time.Now()

    switch b.state {
    case Closed:
        b.consecutiveFailures++
        if b.consecutiveFailures >= b.config.FailureThreshold {
            b.state = Open  // Circuit opened
        }

    case HalfOpen:
        b.state = Open  // Any failure reopens circuit
        b.consecutiveFailures = 0
    }
}
```

**Middleware Integration** (`middleware.go:14-64`):

```go
func Middleware(breaker *Breaker) llm.Middleware {
    return func(next llm.LLMClient) llm.LLMClient {
        return &circuitBreakerClient{
            next:    next,
            breaker: breaker,
        }
    }
}

func (c *circuitBreakerClient) Complete(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
    // Check if circuit allows request
    if err := c.breaker.Allow(); err != nil {
        return llm.CompletionResponse{}, err
    }

    // Execute request
    resp, err := c.next.Complete(ctx, req)

    // Record result
    if err != nil {
        c.breaker.RecordFailure()
    } else {
        c.breaker.RecordSuccess()
    }

    return resp, err
}
```

### Timeout Middleware

**Implementation**: `pkg/agent/middleware/resilience/timeout/middleware.go:13-42`

```go
func Middleware(duration time.Duration) llm.Middleware {
    return func(next llm.LLMClient) llm.LLMClient {
        return &timeoutClient{
            next:    next,
            timeout: duration,
        }
    }
}

func (c *timeoutClient) Complete(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
    // Create timeout context
    ctx, cancel := context.WithTimeout(ctx, c.timeout)
    defer cancel()

    // Execute with timeout
    return c.next.Complete(ctx, req)
}
```

**Default Timeouts**:
- Architect (o3): 180 seconds
- Coder (Claude): 120 seconds

---

## Rate Limiting System

Maestro implements a sophisticated token bucket rate limiter with concurrency control.

### Token Bucket Implementation

**Location**: `pkg/agent/middleware/resilience/ratelimit/limiter.go`

**Structure** (`limiter.go:64-88`):

```go
type TokenBucketLimiter struct {
    provider string

    // Token bucket for request rate limiting
    availableTokens int   // Current available tokens
    tokensPerRefill int   // Tokens added per refill (TPM/10)
    maxCapacity     int   // Max bucket capacity (90% of TPM)

    // Concurrency limiting
    activeRequests int              // Current active requests
    maxConcurrency int              // Max concurrent requests
    acquisitions   []*acquisition   // Tracked acquisitions
    releaseTimeout time.Duration    // Stale acquisition timeout

    // Metrics
    tokenLimitHits  int64  // Times blocked by token limit
    concurrencyHits int64  // Times blocked by concurrency limit

    mu        sync.Mutex
    cond      *sync.Cond
    ctx       context.Context
    ctxCancel context.CancelFunc
}
```

**Acquisition** (`limiter.go:53-62`):

```go
type acquisition struct {
    timestamp time.Time
    released  bool
}
```

### Acquire Logic (`limiter.go:127-188`)

```go
func (l *TokenBucketLimiter) Acquire(ctx context.Context, estimatedTokens int) (func(), error) {
    l.mu.Lock()
    defer l.mu.Unlock()

    // Opportunistically clean stale acquisitions
    l.cleanStaleAcquisitions()

    // Wait for both resources to be available
    for {
        // Check context cancellation
        if ctx.Err() != nil {
            return nil, ctx.Err()
        }

        // Check if we have enough tokens AND concurrency slot
        if l.availableTokens >= estimatedTokens && l.activeRequests < l.maxConcurrency {
            // Atomically acquire both resources
            l.availableTokens -= estimatedTokens
            l.activeRequests++

            // Track acquisition for stale cleanup
            acq := &acquisition{
                timestamp: time.Now(),
                released:  false,
            }
            l.acquisitions = append(l.acquisitions, acq)

            // Return release function
            return func() {
                l.mu.Lock()
                defer l.mu.Unlock()

                if !acq.released {
                    l.activeRequests--
                    acq.released = true
                    l.cond.Broadcast()  // Wake waiting goroutines
                }
            }, nil
        }

        // Track what blocked us
        if l.availableTokens < estimatedTokens {
            atomic.AddInt64(&l.tokenLimitHits, 1)
        } else {
            atomic.AddInt64(&l.concurrencyHits, 1)
        }

        // Wait for signal or context cancellation
        waitCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
        defer cancel()

        waitCh := make(chan struct{})
        go func() {
            l.cond.Wait()
            close(waitCh)
        }()

        select {
        case <-waitCh:
            // Woken up, check again
        case <-waitCtx.Done():
            // Timeout, check again
        case <-ctx.Done():
            return nil, ctx.Err()
        }
    }
}
```

**Key Properties**:
- **Atomic Acquisition**: Both tokens and concurrency slot acquired together
- **Fair Scheduling**: Uses condition variable for fairness
- **Context Aware**: Respects cancellation and deadlines
- **Stale Cleanup**: Auto-releases hung requests
- **Metrics Tracking**: Records why acquisitions blocked

### Refill Timer (`limiter.go:231-246`)

```go
func (l *TokenBucketLimiter) startRefillTimer() {
    ticker := time.NewTicker(6 * time.Second)  // Refill every 6 seconds
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            l.mu.Lock()

            // Add tokens (TPM/10 every 6 seconds)
            l.availableTokens += l.tokensPerRefill

            // Cap at max capacity (90% of TPM)
            if l.availableTokens > l.maxCapacity {
                l.availableTokens = l.maxCapacity
            }

            l.cond.Broadcast()  // Wake waiting goroutines
            l.mu.Unlock()

        case <-l.ctx.Done():
            return  // Shutdown
        }
    }
}
```

**Refill Strategy**:
- **Interval**: 6 seconds
- **Amount**: `tokensPerRefill = tokensPerMinute / 10`
- **Capacity**: `maxCapacity = tokensPerMinute * 0.9` (90% of limit)

Example: 300k TPM (Anthropic)
- Refills 30k tokens every 6 seconds
- Maximum capacity: 270k tokens

### Stale Acquisition Cleanup (`limiter.go:190-229`)

```go
func (l *TokenBucketLimiter) cleanStaleAcquisitions() {
    now := time.Now()
    staleThreshold := 2 * l.releaseTimeout  // 2x request timeout

    for _, acq := range l.acquisitions {
        if acq.released {
            continue  // Already released
        }

        // Check if acquisition is stale
        if now.Sub(acq.timestamp) > staleThreshold {
            l.activeRequests--
            acq.released = true
            l.cond.Broadcast()
        }
    }

    // Remove old released acquisitions from tracking
    // (keep only recent 100)
}
```

**Purpose**: Prevents leaked concurrency slots from hung requests that never call the release function (panics, unexpected returns, etc.)

### Provider Limiter Map (`limiter.go:285-338`)

```go
type ProviderLimiterMap struct {
    limiters map[string]*TokenBucketLimiter
    mu       sync.RWMutex
}

func (m *ProviderLimiterMap) GetLimiter(modelName string) *TokenBucketLimiter {
    // Infer provider from model name
    provider := config.InferProvider(modelName)

    m.mu.RLock()
    limiter, exists := m.limiters[provider]
    m.mu.RUnlock()

    if exists {
        return limiter
    }

    // Should not happen - all providers initialized at startup
    return nil
}

func (m *ProviderLimiterMap) GetStatistics() map[string]LimiterStats {
    m.mu.RLock()
    defer m.mu.RUnlock()

    stats := make(map[string]LimiterStats)
    for provider, limiter := range m.limiters {
        stats[provider] = limiter.GetStats()
    }
    return stats
}
```

### Middleware Integration (`middleware.go:14-96`)

```go
func Middleware(limiterMap *ratelimit.ProviderLimiterMap) llm.Middleware {
    return func(next llm.LLMClient) llm.LLMClient {
        return &rateLimitClient{
            next:       next,
            limiterMap: limiterMap,
        }
    }
}

func (c *rateLimitClient) Complete(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
    modelName := c.next.GetModelName()
    limiter := c.limiterMap.GetLimiter(modelName)

    // Estimate tokens for acquisition
    estimatedTokens := utils.CountTokensSimple(req.Messages)

    // Acquire tokens and concurrency slot
    release, err := limiter.Acquire(ctx, estimatedTokens)
    if err != nil {
        return llm.CompletionResponse{}, err
    }
    defer release()  // MUST release on all code paths

    // Execute request with resources acquired
    return c.next.Complete(ctx, req)
}
```

**Critical**: The `release()` function MUST be called via `defer` to prevent resource leaks.

---

## Metrics and Observability

Comprehensive metrics tracking for cost, performance, and usage analysis.

### Metrics Recording

**Implementation**: `pkg/agent/middleware/metrics/`

**Recorder Interface** (`recorder.go:18-28`):

```go
type Recorder interface {
    ObserveRequest(
        storyID string,
        promptTokens int,
        completionTokens int,
        cost float64,
        success bool,
    )
}
```

**Internal Recorder** (`internal.go:9-27`):

```go
type InternalRecorder struct {
    stories map[string]*StoryMetrics
    mu      sync.RWMutex
}

type StoryMetrics struct {
    StoryID          string
    PromptTokens     int64
    CompletionTokens int64
    TotalTokens      int64
    RequestCount     int64
    TotalCost        float64
    LastUpdated      time.Time
}

func (r *InternalRecorder) ObserveRequest(storyID string, promptTokens, completionTokens int, cost float64, success bool) {
    r.mu.Lock()
    defer r.mu.Unlock()

    metrics, exists := r.stories[storyID]
    if !exists {
        metrics = &StoryMetrics{StoryID: storyID}
        r.stories[storyID] = metrics
    }

    metrics.PromptTokens += int64(promptTokens)
    metrics.CompletionTokens += int64(completionTokens)
    metrics.TotalTokens += int64(promptTokens + completionTokens)
    metrics.RequestCount++
    metrics.TotalCost += cost
    metrics.LastUpdated = time.Now()
}
```

### Middleware Implementation (`middleware.go:35-155`)

```go
func Middleware(recorder Recorder, usageExtractor UsageExtractor, stateProvider StateProvider) llm.Middleware {
    return func(next llm.LLMClient) llm.LLMClient {
        return &metricsClient{
            next:            next,
            recorder:        recorder,
            usageExtractor:  usageExtractor,
            stateProvider:   stateProvider,
        }
    }
}

func (c *metricsClient) Complete(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
    modelName := c.next.GetModelName()
    start := time.Now()

    // Execute request
    resp, err := c.next.Complete(ctx, req)

    latency := time.Since(start)

    // Extract usage information
    promptTokens, completionTokens := c.usageExtractor.Extract(req, resp)

    // Calculate cost
    cost, _ := config.CalculateCost(modelName, promptTokens, completionTokens)

    // Get agent context
    agentID := ""
    storyID := ""
    state := ""
    if c.stateProvider != nil {
        agentID = c.stateProvider.GetAgentID()
        storyID = c.stateProvider.GetStoryID()
        state = c.stateProvider.GetState()
    }

    // Classify error type
    errorType := "none"
    if err != nil {
        errorType = classifyError(err)
    }

    // Log metrics with formatted numbers
    logger.Info("LLM call to model '%s': latency %.1fs, request tokens: %s, response tokens: %s, total tokens: %s, cost $%.6f (agent: %s, story: %s, state: %s)",
        modelName,
        latency.Seconds(),
        formatNumber(promptTokens),
        formatNumber(completionTokens),
        formatNumber(promptTokens+completionTokens),
        cost,
        agentID,
        storyID,
        state,
    )

    // Record metrics
    if c.recorder != nil && storyID != "" {
        c.recorder.ObserveRequest(storyID, promptTokens, completionTokens, cost, err == nil)
    }

    return resp, err
}
```

**Logged Metrics**:
- **Latency**: Request duration in seconds
- **Tokens**: Prompt, completion, and total tokens (with comma formatting)
- **Cost**: Calculated cost in USD
- **Context**: Agent ID, story ID, state
- **Error Type**: Classified error category

**Example Log**:
```
[system] INFO: LLM call to model 'claude-sonnet-4-5': latency 4.8s, request tokens: 4,531,
response tokens: 23, total tokens: 4,554, cost $0.013938 (agent: coder-001, story: 9ae50aa4,
state: PLANNING)
```

### Error Classification (`middleware.go:176-194`)

```go
func classifyError(err error) string {
    if errors.Is(err, circuit.ErrCircuitOpen) {
        return "circuit_breaker"
    }
    if errors.Is(err, context.DeadlineExceeded) {
        return "timeout"
    }
    if errors.Is(err, context.Canceled) {
        return "canceled"
    }

    errType := llmerrors.TypeOf(err)
    switch errType {
    case llmerrors.ErrorTypeRateLimit:
        return "rate_limit"
    case llmerrors.ErrorTypeAuth:
        return "auth"
    case llmerrors.ErrorTypeTransient:
        return "transient"
    case llmerrors.ErrorTypeEmptyResponse:
        return "empty_response"
    default:
        return "unknown"
    }
}
```

### Usage Extraction (`middleware.go:22-33`)

```go
type UsageExtractor interface {
    Extract(req llm.CompletionRequest, resp llm.CompletionResponse) (promptTokens, completionTokens int)
}

// DefaultUsageExtractor uses TikToken-based counting
type DefaultUsageExtractor struct{}

func (e *DefaultUsageExtractor) Extract(req llm.CompletionRequest, resp llm.CompletionResponse) (int, int) {
    // Estimate prompt tokens from all messages
    promptTokens := utils.CountTokensSimple(req.Messages)

    // Estimate completion tokens from response
    completionTokens := utils.EstimateTokens(resp.Content)

    return promptTokens, completionTokens
}
```

---

## Validation and Error Recovery

### Empty Response Validation

**Implementation**: `pkg/agent/middleware/validation/empty_response.go`

**Purpose**: Handles cases where LLM returns empty or invalid responses, providing guidance and retry.

**Agent Types** (`empty_response.go:15-23`):

```go
const (
    AgentTypeArchitect AgentType = "architect"
    AgentTypeCoder     AgentType = "coder"
)
```

**Validator Structure** (`empty_response.go:25-38`):

```go
type EmptyResponseValidator struct {
    agentType             AgentType
    tools                 []string        // Available tools for guidance
    completionTools       []string        // Tools that signal completion
    maxEmptyRetries       int             // Max retries for empty responses
    maxPauseTurnAttempts  int             // Max pause_turn resumptions
    logger                *logx.Logger
}
```

### Two-Tier Retry Pattern

The validator implements a sophisticated two-tier retry system:

**Tier 1: Empty Response Handling** (`empty_response.go:73-136`):

```go
// Inner retry loop for empty responses
for emptyRetry := 0; emptyRetry < v.maxEmptyRetries; emptyRetry++ {
    resp, err := v.next.Complete(ctx, req)

    // Check for ErrorTypeEmptyResponse
    if err != nil && llmerrors.Is(err, llmerrors.ErrorTypeEmptyResponse) {
        if emptyRetry < v.maxEmptyRetries-1 {
            // Add guidance message and retry
            guidance := v.buildGuidanceMessage()
            req.Messages = append(req.Messages, llm.CompletionMessage{
                Role:    llm.RoleUser,
                Content: guidance,
            })

            v.logger.Warn("Empty response from LLM, retrying with guidance (attempt %d/%d)",
                emptyRetry+1, v.maxEmptyRetries)
            continue  // Retry with guidance
        } else {
            // Max retries reached, escalate to state handler
            return resp, fmt.Errorf("LLM returned empty response after %d retries", v.maxEmptyRetries)
        }
    }

    // Validate response (not empty, has content or tools)
    if v.isEmptyResponse(resp, req) {
        if emptyRetry < v.maxEmptyRetries-1 {
            // Build guidance and retry
            guidance := v.buildGuidanceMessage()
            req.Messages = append(req.Messages, llm.CompletionMessage{
                Role:    llm.RoleUser,
                Content: guidance,
            })
            continue
        } else {
            return resp, llmerrors.NewError(llmerrors.ErrorTypeEmptyResponse, "LLM response validation failed")
        }
    }

    // Valid response
    return resp, err
}
```

**Tier 2: Pause Turn Handling** (`empty_response.go:138-150`):

```go
// Outer loop for pause_turn resumption
for pauseAttempt := 0; pauseAttempt < v.maxPauseTurnAttempts; pauseAttempt++ {
    // Execute inner empty response retry loop
    resp, err := innerRetryLoop()

    // Check if LLM paused (wants to continue)
    if resp.StopReason == "pause_turn" {
        v.logger.Info("LLM paused turn, resuming (attempt %d/%d)", pauseAttempt+1, v.maxPauseTurnAttempts)

        // Append partial response and continue
        req.Messages = append(req.Messages, llm.CompletionMessage{
            Role:    llm.RoleAssistant,
            Content: resp.Content,
        })
        continue  // Resume
    }

    return resp, err
}

return llm.CompletionResponse{}, fmt.Errorf("exceeded pause_turn attempts: %d", v.maxPauseTurnAttempts)
```

### Agent-Aware Validation (`empty_response.go:164-180`)

Different agents have different validity criteria:

```go
func (v *EmptyResponseValidator) isEmptyResponse(resp llm.CompletionResponse, req llm.CompletionRequest) bool {
    // If there are tool calls, response is valid
    if len(resp.ToolCalls) > 0 {
        return false
    }

    isArchitect := v.agentType == AgentTypeArchitect
    contentEmpty := strings.TrimSpace(resp.Content) == ""

    // Architect can return text responses
    if isArchitect {
        return contentEmpty  // Only truly empty content is invalid
    }

    // Coder must use tool calls or have content
    return contentEmpty  // Coder without tools or content is invalid
}
```

**Rationale**:
- **Architect**: Can provide analysis and answers as text (REQUEST, ANSWERING states)
- **Coder**: Should always use tools for actions (submit_plan, done, shell, etc.)

### Guidance Messages (`empty_response.go:183-210`)

When retry is needed, context-specific guidance is provided:

```go
func (v *EmptyResponseValidator) buildGuidanceMessage() string {
    var guidance strings.Builder

    if v.agentType == AgentTypeArchitect {
        guidance.WriteString("Your previous response was empty or unclear. ")
        guidance.WriteString("Please provide a clear response with your analysis, decision, or answer. ")
    } else {
        guidance.WriteString("Your previous response did not include any tool calls. ")
        guidance.WriteString("Please use the tool call API rather than describing tools in text ")
        guidance.WriteString("(e.g., don't say 'Tool shell invoked with...' - actually call the tool). ")
    }

    // List available tools
    if len(v.tools) > 0 {
        guidance.WriteString("\n\nAvailable tools:\n")
        for _, tool := range v.tools {
            guidance.WriteString(fmt.Sprintf("- %s\n", tool))
        }
    }

    // Highlight completion tools
    if len(v.completionTools) > 0 {
        guidance.WriteString("\n\nTo signal completion, use one of these tools:\n")
        for _, tool := range v.completionTools {
            guidance.WriteString(fmt.Sprintf("- %s\n", tool))
        }
    }

    return guidance.String()
}
```

**Example Guidance for Coder**:
```
Your previous response did not include any tool calls. Please use the tool call API
rather than describing tools in text (e.g., don't say 'Tool shell invoked with...' -
actually call the tool).

Available tools:
- shell
- build
- test
- lint
- done

To signal completion, use one of these tools:
- done
```

### Empty Response Logging

**Implementation**: `pkg/agent/middleware/logging/empty_response.go`

Provides detailed debugging when empty responses occur:

```go
func EmptyResponseLoggingMiddleware() llm.Middleware {
    return func(next llm.LLMClient) llm.LLMClient {
        return &emptyResponseLogger{next: next}
    }
}

func (l *emptyResponseLogger) Complete(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
    resp, err := l.next.Complete(ctx, req)

    // Detect empty response errors
    if err != nil && llmerrors.Is(err, llmerrors.ErrorTypeEmptyResponse) {
        logger.Warn("Empty response detected - logging full prompt for debugging")

        // Log complete prompt sent to LLM
        logger.Debug("=== LLM Request Details ===")
        logger.Debug("Model: %s", l.next.GetModelName())
        logger.Debug("Temperature: %.2f", req.Temperature)
        logger.Debug("MaxTokens: %d", req.MaxTokens)
        logger.Debug("Tools: %d available", len(req.Tools))

        logger.Debug("=== Messages ===")
        for i, msg := range req.Messages {
            content := msg.Content
            if len(content) > 10000 {
                content = content[:10000] + "... (truncated)"
            }
            logger.Debug("Message %d [%s]: %s", i+1, msg.Role, content)
        }
    }

    return resp, err
}
```

**Purpose**: Does NOT modify behavior, only provides debugging information to diagnose why empty responses occur.

---

## Configuration and Model Management

### Model Registry

**Location**: `pkg/config/config.go`

**ModelInfo Structure** (`config.go:91-99`):

```go
type ModelInfo struct {
    Provider         string   // "anthropic", "openai"
    InputCPM         float64  // Cost per million input tokens
    OutputCPM        float64  // Cost per million output tokens
    MaxContextTokens int      // Maximum context window
    MaxOutputTokens  int      // Maximum output tokens per request
}
```

**Known Models Registry** (`config.go:105-162`):

```go
var KnownModels = map[string]ModelInfo{
    // Anthropic Claude models
    "claude-3-7-sonnet-20250219": {
        Provider:         ProviderAnthropic,
        InputCPM:         3.00,
        OutputCPM:        15.00,
        MaxContextTokens: 200000,
        MaxOutputTokens:  8192,
    },
    "claude-sonnet-4-5": {
        Provider:         ProviderAnthropic,
        InputCPM:         3.00,
        OutputCPM:        15.00,
        MaxContextTokens: 200000,
        MaxOutputTokens:  8192,
    },
    "claude-sonnet-4-20250514": {
        Provider:         ProviderAnthropic,
        InputCPM:         3.00,
        OutputCPM:        15.00,
        MaxContextTokens: 200000,
        MaxOutputTokens:  8192,
    },

    // OpenAI models
    "o3-mini": {
        Provider:         ProviderOpenAI,
        InputCPM:         1.10,
        OutputCPM:        4.40,
        MaxContextTokens: 200000,
        MaxOutputTokens:  100000,
    },
    "o3": {
        Provider:         ProviderOpenAI,
        InputCPM:         10.00,
        OutputCPM:        40.00,
        MaxContextTokens: 200000,
        MaxOutputTokens:  100000,
    },
    "o4-mini": {
        Provider:         ProviderOpenAI,
        InputCPM:         1.10,
        OutputCPM:        4.40,
        MaxContextTokens: 128000,
        MaxOutputTokens:  16384,
    },
    "gpt-5": {
        Provider:         ProviderOpenAI,
        InputCPM:         5.00,
        OutputCPM:        15.00,
        MaxContextTokens: 128000,
        MaxOutputTokens:  16384,
    },
}
```

### Provider Inference

**Provider Patterns** (`config.go:174-200`):

```go
type ProviderPattern struct {
    Pattern  string
    Provider string
}

var ProviderPatterns = []ProviderPattern{
    {"claude", ProviderAnthropic},
    {"gpt", ProviderOpenAI},
    {"o1", ProviderOpenAI},
    {"o3", ProviderOpenAI},
    {"o4", ProviderOpenAI},
}

func InferProvider(modelName string) string {
    // Check known models first
    if info, exists := KnownModels[modelName]; exists {
        return info.Provider
    }

    // Infer from naming pattern
    modelLower := strings.ToLower(modelName)
    for _, pattern := range ProviderPatterns {
        if strings.Contains(modelLower, pattern.Pattern) {
            return pattern.Provider
        }
    }

    // Default to unknown
    return "unknown"
}
```

### Cost Calculation

**Implementation** (`config.go:1458-1472`):

```go
func CalculateCost(modelName string, promptTokens, completionTokens int) (float64, error) {
    info, exists := KnownModels[modelName]
    if !exists {
        // Unknown models return $0 cost (allows new models without updates)
        return 0.0, nil
    }

    // Calculate cost: (tokens / 1M) * cost_per_million
    inputCost := (float64(promptTokens) / 1_000_000.0) * info.InputCPM
    outputCost := (float64(completionTokens) / 1_000_000.0) * info.OutputCPM
    totalCost := inputCost + outputCost

    return totalCost, nil
}
```

**Example**:
```
Model: claude-sonnet-4-5
Prompt: 4,531 tokens
Completion: 23 tokens

Input cost:  (4,531 / 1,000,000) * $3.00  = $0.013593
Output cost: (23 / 1,000,000) * $15.00    = $0.000345
Total:                                      $0.013938
```

### Rate Limit Configuration

**Structure** (`config.go:230-257`):

```go
type ProviderLimits struct {
    TokensPerMinute int  // Token bucket capacity
    MaxConcurrency  int  // Max concurrent requests
}

type RateLimitConfig struct {
    Anthropic ProviderLimits
    OpenAI    ProviderLimits
}

// Default limits
var ProviderDefaults = map[string]ProviderLimits{
    ProviderAnthropic: {
        TokensPerMinute: 300000,  // 300k TPM
        MaxConcurrency:  5,
    },
    ProviderOpenAI: {
        TokensPerMinute: 150000,  // 150k TPM
        MaxConcurrency:  5,
    },
}
```

### Resilience Configuration

**Structure** (`config.go:274-280`):

```go
type ResilienceConfig struct {
    CircuitBreaker CircuitBreakerConfig
    Retry          RetryConfig
    RateLimit      RateLimitConfig
    Timeout        time.Duration
}

type CircuitBreakerConfig struct {
    FailureThreshold int           // Consecutive failures to open
    SuccessThreshold int           // Consecutive successes to close
    Timeout          time.Duration // Recovery attempt delay
}

type RetryConfig struct {
    MaxAttempts   int
    InitialDelay  time.Duration
    MaxDelay      time.Duration
    BackoffFactor float64
}
```

### Environment Variable Overrides

**Pattern** (`config.go`):

```go
// Config loader supports environment variable substitution
// In config.json:
{
    "anthropic_api_key": "${ANTHROPIC_API_KEY}",
    "openai_api_key": "${OPENAI_API_KEY}",
    "github_token": "${GITHUB_TOKEN}"
}

// Also supports direct environment variable overrides
// Environment variable name matches JSON key (case-insensitive)
CODER_MODEL=claude-sonnet-4-5
ARCHITECT_MODEL=o3
CODER_TIMEOUT=180s
```

---

## Chat Integration Middleware

**Implementation**: `pkg/agent/middleware/chat/injection.go`

Automatically injects collaborative chat messages into LLM context.

### Message Fetching (`injection.go:38-95`)

```go
func Middleware(chatService *chat.Service, config *config.Config) llm.Middleware {
    fetchAndFormatMessages := func(ctx context.Context) (llm.CompletionMessage, error) {
        // Check if chat enabled
        if !config.Chat.Enabled {
            return llm.CompletionMessage{}, nil
        }

        // Get agent ID from context
        agentID := ctx.Value(AgentIDKey).(string)

        // Fetch new messages since last read
        messages, err := chatService.FetchNewMessages(agentID, config.Chat.MaxNewMessages)
        if err != nil {
            return llm.CompletionMessage{}, err
        }

        if len(messages) == 0 {
            return llm.CompletionMessage{}, nil  // No new messages
        }

        // Format as markdown
        var formatted strings.Builder
        formatted.WriteString("## Recent Chat Messages\n\n")
        formatted.WriteString("The following messages were posted to the agent chat system:\n\n")

        for _, msg := range messages {
            formatted.WriteString(fmt.Sprintf("**%s**: %s\n\n", msg.AuthorID, msg.Content))
        }

        formatted.WriteString("You may respond using the `chat_post` tool if appropriate.\n")

        return llm.CompletionMessage{
            Role:    llm.RoleUser,
            Content: formatted.String(),
        }, nil
    }

    return buildMiddleware(fetchAndFormatMessages)
}
```

### Middleware Injection (`injection.go:97-151`)

```go
func buildMiddleware(fetcher MessageFetcher) llm.Middleware {
    return func(next llm.LLMClient) llm.LLMClient {
        return &chatInjectionClient{
            next:    next,
            fetcher: fetcher,
        }
    }
}

func (c *chatInjectionClient) Complete(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
    // Fetch new chat messages
    chatMsg, err := c.fetcher(ctx)
    if err != nil {
        logger.Warn("Failed to fetch chat messages: %v", err)
        // Continue without chat messages
    }

    // Inject chat messages if available
    if chatMsg.Content != "" {
        // Prepend to existing messages
        req.Messages = append([]llm.CompletionMessage{chatMsg}, req.Messages...)
    }

    // Execute request with injected messages
    return c.next.Complete(ctx, req)
}
```

**Message Format Example**:

```markdown
## Recent Chat Messages

The following messages were posted to the agent chat system:

**architect-001**: I've reviewed the implementation and it looks good. Please proceed with testing.

**coder-002**: I noticed a potential issue with the error handling. Should we discuss?

You may respond using the `chat_post` tool if appropriate.
```

### Configuration (`config.json`):

```json
{
  "chat": {
    "enabled": true,
    "max_new_messages": 100,
    "limits": {
      "max_message_chars": 4096
    }
  }
}
```

---

## Error Classification

Maestro uses structured error types to enable consistent retry and recovery behavior.

### Error Types (`pkg/agent/llmerrors/errors.go:11-32`)

```go
type ErrorType int

const (
    // Retryable errors
    ErrorTypeRateLimit      ErrorType = iota  // 429, rate limit exceeded
    ErrorTypeTransient                        // Network, timeout, 5xx errors
    ErrorTypeEmptyResponse                    // Empty/invalid LLM response

    // Non-retryable errors
    ErrorTypeAuth                             // 401, authentication failure
    ErrorTypeBadPrompt                        // 400, invalid request
    ErrorTypeUnknown                          // Unknown error type
)
```

### Error Structure (`errors.go:121-144`)

```go
type Error struct {
    Err        error         // Wrapped error
    Message    string        // Human-readable message
    BodyStub   string        // Sanitized error body
    Type       ErrorType     // Classification
    StatusCode int           // HTTP status code (if applicable)
}

func (e *Error) Error() string {
    if e.StatusCode > 0 {
        return fmt.Sprintf("LLM error (%s): %s [HTTP %d]", e.Type, e.Message, e.StatusCode)
    }
    return fmt.Sprintf("LLM error (%s): %s", e.Type, e.Message)
}

func (e *Error) Unwrap() error {
    return e.Err
}
```

### Retry Configurations (`errors.go:75-119`)

Different error types have different retry policies:

```go
var DefaultRetryConfigs = map[ErrorType]RetryConfig{
    ErrorTypeEmptyResponse: {
        MaxAttempts:   5,
        InitialDelay:  2 * time.Second,
        MaxDelay:      30 * time.Second,
        BackoffFactor: 2.0,
        Jitter:        true,
    },
    ErrorTypeRateLimit: {
        MaxAttempts:   6,
        InitialDelay:  1 * time.Second,
        MaxDelay:      60 * time.Second,
        BackoffFactor: 2.0,
        Jitter:        true,
    },
    ErrorTypeTransient: {
        MaxAttempts:   4,
        InitialDelay:  500 * time.Millisecond,
        MaxDelay:      10 * time.Second,
        BackoffFactor: 2.0,
        Jitter:        true,
    },
    ErrorTypeAuth: {
        MaxAttempts:   0,  // Non-retryable
    },
    ErrorTypeBadPrompt: {
        MaxAttempts:   0,  // Non-retryable
    },
    ErrorTypeUnknown: {
        MaxAttempts:   1,  // One retry for unknowns
        InitialDelay:  1 * time.Second,
        MaxDelay:      5 * time.Second,
        BackoffFactor: 2.0,
    },
}
```

### Helper Functions (`errors.go:36-73`)

```go
// Check if error matches type
func Is(err error, errorType ErrorType) bool {
    var llmErr *Error
    if errors.As(err, &llmErr) {
        return llmErr.Type == errorType
    }
    return false
}

// Get error type
func TypeOf(err error) ErrorType {
    var llmErr *Error
    if errors.As(err, &llmErr) {
        return llmErr.Type
    }
    return ErrorTypeUnknown
}

// Create new classified error
func NewError(errorType ErrorType, message string) error {
    return &Error{
        Type:    errorType,
        Message: message,
    }
}

// Create with HTTP status
func NewErrorWithStatus(errorType ErrorType, message string, statusCode int) error {
    return &Error{
        Type:       errorType,
        Message:    message,
        StatusCode: statusCode,
    }
}

// Wrap existing error
func NewErrorWithCause(errorType ErrorType, message string, cause error) error {
    return &Error{
        Type:    errorType,
        Message: message,
        Err:     cause,
    }
}
```

### Sanitized Logging (`errors.go:146-165`)

```go
func SanitizePrompt(prompt string) string {
    if len(prompt) <= 100 {
        return prompt
    }

    // Show first 50 and last 50 characters with hash in between
    hash := fmt.Sprintf("%x", sha256.Sum256([]byte(prompt)))
    return fmt.Sprintf("%s...[hash:%s]...%s",
        prompt[:50],
        hash[:8],
        prompt[len(prompt)-50:],
    )
}
```

**Purpose**: Safely logs prompts without exposing full content (may contain secrets, PII, etc.)

---

## Key Implementation Patterns

### 1. Middleware Composition

**Pattern**: Functional composition with `Chain()`

```go
client := llm.Chain(baseClient,
    middleware1,  // Outermost
    middleware2,
    middleware3,  // Innermost
)
```

**Benefits**:
- Each middleware is independent and testable
- Easy to add/remove middleware
- Clear separation of concerns
- Middleware can short-circuit the chain

**Execution Order**:
```
Request:  middleware1 → middleware2 → middleware3 → baseClient
Response: baseClient → middleware3 → middleware2 → middleware1
```

### 2. Provider Abstraction

**Pattern**: Common `LLMClient` interface for all providers

**Benefits**:
- Middleware works with any provider
- Easy to add new providers (implement interface)
- Agent code is provider-agnostic
- Can switch providers without code changes

**Implementation**:
```go
func CreateClient(modelName string) llm.LLMClient {
    provider := config.InferProvider(modelName)

    var rawClient llm.LLMClient
    switch provider {
    case config.ProviderAnthropic:
        rawClient = anthropic.NewClaudeClient(apiKey, modelName)
    case config.ProviderOpenAI:
        rawClient = openaiofficial.NewOfficialClient(apiKey, modelName)
    default:
        panic("unknown provider")
    }

    // Apply universal middleware
    return llm.Chain(rawClient, middlewares...)
}
```

### 3. Error Classification and Retry

**Pattern**: Structured error types with specific retry policies

**Benefits**:
- Consistent retry behavior across providers
- Type-safe error handling
- Configurable retry policies per error type
- Circuit breaker integration

**Implementation**:
```go
// Provider classifies error
err := classifyError(providerError)
llmErr := llmerrors.NewError(llmerrors.ErrorTypeRateLimit, "rate limited")

// Retry middleware checks if retryable
if shouldRetry(llmErr) {
    config := llmerrors.DefaultRetryConfigs[llmerrors.ErrorTypeRateLimit]
    // Retry with exponential backoff
}
```

### 4. Token Bucket Rate Limiting

**Pattern**: Atomic acquisition of tokens + concurrency slot

**Benefits**:
- Prevents thundering herd
- Respects provider API limits
- Fair scheduling with condition variables
- Auto-cleanup of stale acquisitions

**Implementation**:
```go
// Atomically acquire both resources
release, err := limiter.Acquire(ctx, estimatedTokens)
if err != nil {
    return err
}
defer release()  // MUST release

// Execute with resources held
resp, err := client.Complete(ctx, req)
```

### 5. Circuit Breaker State Machine

**Pattern**: Three states with automatic recovery

**Benefits**:
- Prevents cascading failures
- Gives failing APIs time to recover
- Automatic recovery testing
- Per-provider isolation

**States**:
- **Closed**: Normal operation
- **Open**: Rejecting requests (failing fast)
- **HalfOpen**: Testing recovery

### 6. Agent-Aware Validation

**Pattern**: Different validation rules per agent type

**Benefits**:
- Architect can return text responses
- Coder must use tools
- Appropriate guidance per agent
- Flexible validation logic

**Implementation**:
```go
if agentType == AgentTypeArchitect {
    return strings.TrimSpace(resp.Content) == ""  // Text OK
} else {
    return len(resp.ToolCalls) == 0  // Must use tools
}
```

### 7. Prompt Caching

**Pattern**: Cache control metadata on messages

**Benefits**:
- Reduces cost (50-90% savings)
- Reduces latency
- Automatic cache management
- Provider-specific implementation

**Usage**:
```go
messages := []CompletionMessage{
    {
        Role:    RoleSystem,
        Content: systemPrompt,
        CacheControl: &CacheControl{TTL: "1h"},
    },
}
```

### 8. Recursive Schema Conversion

**Pattern**: Recursive traversal for nested structures

**Benefits**:
- Handles arbitrarily complex tool schemas
- Supports arrays of objects
- Supports nested properties
- Provider-specific requirements

**Implementation**:
```go
func convertPropertyToSchema(prop *tools.Property) map[string]interface{} {
    schema := map[string]interface{}{
        "type": prop.Type,
        "description": prop.Description,
    }

    if prop.Type == "array" && prop.Items != nil {
        schema["items"] = convertPropertyToSchema(prop.Items)  // Recursive
    }

    if prop.Type == "object" && prop.Properties != nil {
        properties := make(map[string]interface{})
        for name, child := range prop.Properties {
            properties[name] = convertPropertyToSchema(child)  // Recursive
        }
        schema["properties"] = properties
    }

    return schema
}
```

---

## Architecture Diagrams

### Complete Request Flow

```
┌─────────────────────────────────────────────────────────────────┐
│                      LLM REQUEST FLOW                           │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  Agent                                                          │
│    ↓                                                            │
│  Build CompletionRequest (messages, tools, temperature)        │
│    ↓                                                            │
│  Call client.Complete(ctx, request)                            │
│    ↓                                                            │
│  ┌─────────────────── MIDDLEWARE STACK ──────────────────────┐ │
│  │                                                            │ │
│  │  Validator (outermost)                                    │ │
│  │    ├─ Check for empty response                            │ │
│  │    ├─ Add guidance if needed                              │ │
│  │    └─ Handle pause_turn                                   │ │
│  │       ↓                                                    │ │
│  │  Metrics                                                   │ │
│  │    ├─ Record latency                                      │ │
│  │    ├─ Extract token usage                                 │ │
│  │    ├─ Calculate cost                                      │ │
│  │    └─ Log metrics                                         │ │
│  │       ↓                                                    │ │
│  │  Circuit Breaker                                          │ │
│  │    ├─ Check state (Closed/Open/HalfOpen)                 │ │
│  │    ├─ Allow or reject request                            │ │
│  │    └─ Record success/failure                             │ │
│  │       ↓                                                    │ │
│  │  Retry                                                     │ │
│  │    ├─ Classify error type                                 │ │
│  │    ├─ Check if retryable                                  │ │
│  │    ├─ Exponential backoff with jitter                     │ │
│  │    └─ Retry up to MaxAttempts                            │ │
│  │       ↓                                                    │ │
│  │  Logging                                                   │ │
│  │    └─ Log empty response details                          │ │
│  │       ↓                                                    │ │
│  │  Rate Limit                                               │ │
│  │    ├─ Estimate tokens                                     │ │
│  │    ├─ Acquire tokens + concurrency                        │ │
│  │    ├─ Wait if unavailable                                 │ │
│  │    └─ Release on completion (defer)                      │ │
│  │       ↓                                                    │ │
│  │  Timeout (innermost)                                      │ │
│  │    └─ Create context with deadline                        │ │
│  │       ↓                                                    │ │
│  └────────────────────────────────────────────────────────────┘ │
│    ↓                                                            │
│  Provider Client (Anthropic or OpenAI)                         │
│    ├─ Convert messages (alternation, system extraction)       │
│    ├─ Convert tools (recursive schema)                        │
│    ├─ Apply prompt caching                                    │
│    ├─ Make API call                                           │
│    ├─ Parse response                                          │
│    ├─ Extract tool calls                                      │
│    └─ Classify errors                                         │
│    ↓                                                            │
│  Return CompletionResponse                                     │
│    ↓                                                            │
│  Agent processes tool calls or content                         │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

### Provider Integration

```
┌────────────────────────────────────────────────────────┐
│              PROVIDER ABSTRACTION                      │
├────────────────────────────────────────────────────────┤
│                                                        │
│  Agent Code                                           │
│      ↓                                                 │
│  LLMClient Interface                                  │
│      ├─ Complete(ctx, CompletionRequest)             │
│      ├─ Stream(ctx, CompletionRequest)               │
│      └─ GetModelName()                                │
│      ↓                                                 │
│  ┌──────────────────┬─────────────────────────┐      │
│  │                  │                         │      │
│  │  Anthropic       │  OpenAI                 │      │
│  │  Claude Client   │  Official Client        │      │
│  │                  │                         │      │
│  │  - Messages API  │  - Responses API        │      │
│  │  - Alternation   │  - Input string         │      │
│  │  - Caching       │  - MaxTokens cap        │      │
│  │  - Tool format   │  - Recursive schema     │      │
│  │  - Error map     │  - Reasoning output     │      │
│  │                  │                         │      │
│  └──────────────────┴─────────────────────────┘      │
│                                                        │
└────────────────────────────────────────────────────────┘
```

---

## Summary

The Maestro LLM abstraction provides a production-ready foundation for AI agent systems:

**Core Capabilities**:
- **Unified Interface**: Single LLMClient interface for all providers
- **Provider Support**: Anthropic Claude, OpenAI GPT/o-series
- **Resilience**: Retry, circuit breaker, timeout, rate limiting
- **Observability**: Metrics, logging, cost tracking
- **Validation**: Empty response handling, agent-aware validation
- **Cost Control**: Token bucket rate limiting, concurrent request limits
- **Chat Integration**: Automatic injection of collaborative messages
- **Error Recovery**: Structured error types with retry policies

**Key Design Principles**:
- **Composability**: Middleware chain allows flexible behavior composition
- **Testability**: Each middleware is independently testable
- **Extensibility**: Easy to add new providers and middleware
- **Type Safety**: Structured errors and responses
- **Observability**: Comprehensive metrics and logging
- **Resilience First**: Multiple layers of failure protection

**Production Features**:
- Automatic retries with exponential backoff
- Circuit breakers prevent cascading failures
- Rate limiting respects API quotas
- Token and cost tracking for budget management
- Empty response recovery with LLM guidance
- Prompt caching for cost and latency reduction
- Stale acquisition cleanup prevents resource leaks
- Agent-aware validation for different workflows

The architecture is battle-tested, handling thousands of LLM calls daily with automatic recovery from transient failures, cost optimization through caching, and comprehensive observability for debugging and performance analysis.

---

**Related Documentation**:
- [Tools System](TOOLS_WIKI.md) - MCP tools and agent capabilities
- [Git Workflow](GIT_FLOW_WIKI.md) - How agents use git operations
- [Knowledge Graph](DOCS_WIKI.md) - Architectural knowledge system
- [Project Architecture](../CLAUDE.md) - Overall system design
