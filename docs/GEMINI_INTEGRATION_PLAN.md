# Google Gemini Integration Plan

## Overview

This document outlines the plan for integrating Google Gemini models into the Maestro orchestrator, with primary focus on **Gemini 3 Pro**.

## Goals

1. Enable Gemini 3 Pro as a supported LLM provider
2. Support tool calling with required tool use mode
3. Leverage Gemini's 1M token context window
4. Integrate seamlessly with existing middleware (rate limiting, circuit breaker, metrics)

## Model Specifications

### Gemini 3 Pro (Primary Target)
- **Model Name**: `gemini-3-pro`
- **Provider**: Google (via `google.golang.org/genai`)
- **Context Window**: 1,048,576 tokens (1M)
- **Max Output**: 65,536 tokens
- **Pricing**:
  - Input: $2.00/1M tokens (≤200k prompts), $4.00/1M (>200k prompts)
  - Output: $12.00/1M tokens (≤200k prompts), $18.00/1M (>200k prompts)
- **Environment Variable**: `GOOGLE_GENAI_API_KEY`

### Additional Models (Nice to Have)
- **Gemini 2.5 Flash**: 1M context, 65k output, $0.30/$2.50 per 1M tokens
- **Gemini 2.0 Flash**: 1M context, 8k output, $0.10/$0.40 per 1M tokens

## Key Features

### Function Calling Configuration
Gemini supports tool/function calling through `function_calling_config` with the following modes:

- **AUTO** (default): Model decides whether to call functions or respond naturally
- **ANY**: Forces tool use - equivalent to `tool_choice: required` in other APIs
- **NONE**: Prohibits function calls
- **VALIDATED** (preview): Ensures schema adherence for functions or natural language

**Implementation**: When `ToolChoice` is set in `CompletionRequest`, use mode "ANY" to force tool use.

## Implementation Plan

### Phase 1: Configuration Setup ✓ (Partially Complete)

#### Completed
- [x] Add `ProviderGoogle = "google"` constant
- [x] Add `EnvGoogleAPIKey = "GOOGLE_GENAI_API_KEY"`
- [x] Add model entries to `KnownModels`:
  - `gemini-3-pro`: 1M context, 65k output
  - `gemini-2.5-flash`: 1M context, 65k output
  - `gemini-2.0-flash`: 1M context, 8k output

#### Remaining
- [ ] Add provider pattern: `{"gemini", ProviderGoogle}` to `ProviderPatterns`
- [ ] Add rate limits to `ProviderDefaults` for Google
- [ ] Update `GetAPIKey()` function to handle `ProviderGoogle` case

### Phase 2: Dependency Installation

```bash
go get google.golang.org/genai
```

### Phase 3: Client Implementation

Create `pkg/agent/internal/llmimpl/google/client.go`:

#### GeminiClient Structure
```go
type GeminiClient struct {
    client *genai.Client
    model  string
}
```

#### Methods to Implement

1. **NewGeminiClientWithModel(apiKey, model string) llm.LLMClient**
   - Initialize genai.Client
   - Store model name
   - Return client implementing llm.LLMClient interface

2. **Complete(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error)**
   - Convert messages: `[]llm.CompletionMessage` → Gemini format
   - Convert tools: `[]tools.ToolDefinition` → Gemini function declarations
   - Set `function_calling_config`:
     - If `req.ToolChoice` is set → mode: "ANY"
     - Otherwise → mode: "AUTO"
   - Handle system messages (extract to system instruction if needed)
   - Make API call
   - Convert response back to `llm.CompletionResponse`

3. **Stream(ctx context.Context, req llm.CompletionRequest) (<-chan llm.StreamChunk, error)**
   - Similar to Complete() but return streaming channel
   - Handle incremental content and tool calls

4. **GetModelName() string**
   - Return stored model name

#### Message Conversion Considerations
- **System Messages**: Gemini may handle system messages differently - research if they need to be extracted to a system instruction parameter
- **Message Alternation**: Determine if Gemini requires strict user/assistant alternation like Anthropic
- **Tool Results**: Map our `ToolResult` format to Gemini's function response format

#### Tool Definition Conversion
Convert from our format:
```go
type ToolDefinition struct {
    Name        string
    Description string
    InputSchema InputSchema
}
```

To Gemini's function declaration format (research exact structure from SDK docs).

### Phase 4: Factory Integration

Update `pkg/agent/factory.go`:

1. **Add Circuit Breaker** (in `NewLLMClientFactory`)
```go
for _, provider := range []string{
    string(config.ProviderAnthropic),
    string(config.ProviderOpenAI),
    string(config.ProviderGoogle), // Add this
} {
    circuitBreakers[provider] = circuit.New(...)
}
```

2. **Add Rate Limit Config** (in `NewLLMClientFactory`)
```go
rateLimitConfigs := map[string]ratelimit.Config{
    string(config.ProviderAnthropic): {...},
    string(config.ProviderOpenAI): {...},
    string(config.ProviderGoogle): {  // Add this
        TokensPerMinute: cfg.Agents.Resilience.RateLimit.Google.TokensPerMinute,
        MaxConcurrency:  cfg.Agents.Resilience.RateLimit.Google.MaxConcurrency,
    },
}
```

3. **Add Provider Case** (in `createClientWithMiddleware`)
```go
switch provider {
case config.ProviderAnthropic:
    rawClient = anthropic.NewClaudeClientWithModel(apiKey, modelName)
case config.ProviderOpenAI:
    rawClient = openaiofficial.NewOfficialClientWithModel(apiKey, modelName)
case config.ProviderGoogle:  // Add this
    rawClient = google.NewGeminiClientWithModel(apiKey, modelName)
default:
    return nil, fmt.Errorf("unsupported provider: %s", provider)
}
```

### Phase 5: Configuration Schema

Add to `RateLimitConfig` in `pkg/config/config.go`:
```go
type RateLimitConfig struct {
    Anthropic ProviderLimits `json:"anthropic"`
    OpenAI    ProviderLimits `json:"openai"`
    Google    ProviderLimits `json:"google"` // Add this
}
```

Add to `ProviderDefaults`:
```go
ProviderDefaults = map[string]ProviderLimits{
    ProviderAnthropic: {...},
    ProviderOpenAI: {...},
    ProviderGoogle: {  // Add this
        TokensPerMinute: 60000,  // Reasonable default, adjust based on API limits
        MaxConcurrency:  5,       // Conservative default
    },
}
```

### Phase 6: Testing

1. **Unit Tests** (`pkg/agent/internal/llmimpl/google/client_test.go`)
   - Test message conversion
   - Test tool definition conversion
   - Test function_calling_config mode selection
   - Mock API responses

2. **Integration Test**
   - Test with actual Gemini 3 Pro API (gated by env var)
   - Verify tool calling works with mode "ANY"
   - Test streaming responses
   - Verify 1M context window support

### Phase 7: Documentation

Update `CLAUDE.md`:
- Add Gemini to list of supported providers
- Document `GOOGLE_GENAI_API_KEY` environment variable
- Note Gemini-specific features (1M context, function_calling_config)
- Document any limitations or special behaviors

## Open Questions to Research

1. **System Message Handling**: Does Gemini handle system messages in the messages array, or do they need to be extracted to a separate system instruction parameter?

2. **Message Alternation**: Does Gemini require strict user/assistant alternation like Anthropic, or is it more flexible like OpenAI?

3. **Tool Result Format**: What's the exact format for function/tool results in Gemini's SDK?

4. **Rate Limits**: What are the actual rate limits for Gemini API? (tokens per minute, requests per minute, concurrent requests)

5. **Error Handling**: What error types does the Gemini SDK return? How should we classify them for retry logic?

6. **Streaming Tool Calls**: How does Gemini handle tool calls in streaming mode? Are they delivered incrementally or as complete chunks?

## Success Criteria

- [ ] Gemini 3 Pro can be used as coder model
- [ ] Gemini 3 Pro can be used as architect model
- [ ] Tool calling works with required mode (function_calling_config: ANY)
- [ ] Streaming responses work correctly
- [ ] Rate limiting and circuit breaker work with Gemini
- [ ] Integration test passes with actual API
- [ ] Documentation updated

## Timeline Estimate

- **Phase 1-2** (Config + Deps): 30 minutes
- **Phase 3** (Client Implementation): 2-3 hours
- **Phase 4-5** (Factory + Config): 1 hour
- **Phase 6** (Testing): 1-2 hours
- **Phase 7** (Docs): 30 minutes

**Total**: ~5-7 hours

## References

- [Gemini API Pricing](https://ai.google.dev/gemini-api/docs/pricing)
- [Gemini API Models](https://ai.google.dev/gemini-api/docs/models)
- [Gemini Function Calling](https://ai.google.dev/gemini-api/docs/function-calling)
- [Google GenAI SDK](https://pkg.go.dev/google.golang.org/genai)
