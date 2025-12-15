# Ollama LLM Provider

This document describes the implementation plan for adding Ollama support as an LLM provider, enabling use of locally-hosted models like Phi4, Llama 3.1, Qwen3, and others.

## Overview

Ollama is a local LLM runtime that allows running open-source models on your own hardware. Adding Ollama support enables:
- Cost-free local development and testing
- Privacy-sensitive workloads that can't use cloud APIs
- Experimentation with open-source models
- Reduced latency for local development

## Design Decisions

### Go SDK Choice

**Decision**: Use the official `github.com/ollama/ollama/api` package

**Rationale**:
- Maintained alongside the Ollama project
- Used by the Ollama CLI itself
- Provides typed Go structures matching the REST API
- Best compatibility guarantee

**Alternative considered**: Raw HTTP to `http://localhost:11434/api/chat` (simpler but less robust)

### Authentication

**Decision**: Use host URL instead of API key

**Rationale**:
- Ollama typically runs without authentication locally
- Configuration via `OLLAMA_HOST` environment variable (Ollama's standard convention)
- Default: `http://localhost:11434`

### Tool Calling

Ollama supports tool calling via the `/api/chat` endpoint with the following format:

**Request**:
```json
{
  "model": "phi4:latest",
  "messages": [
    {"role": "user", "content": "What's the weather?"}
  ],
  "tools": [{
    "type": "function",
    "function": {
      "name": "get_weather",
      "description": "Get current weather",
      "parameters": {
        "type": "object",
        "properties": {
          "location": {"type": "string", "description": "City name"}
        },
        "required": ["location"]
      }
    }
  }],
  "stream": false
}
```

**Response**:
```json
{
  "message": {
    "role": "assistant",
    "content": "",
    "tool_calls": [{
      "function": {
        "name": "get_weather",
        "arguments": {"location": "San Francisco"}
      }
    }]
  }
}
```

**Note**: Not all models support tool calling. Recommended models:
- Phi4 (good for coder tasks)
- Llama 3.1 (8B, 70B variants)
- Qwen3
- Mistral 7B

## Implementation Plan

### Phase 1: Configuration Infrastructure

**Files to modify**: `pkg/config/config.go`

Add:
- `ProviderOllama = "ollama"` constant
- `EnvOllamaHost = "OLLAMA_HOST"` environment variable name
- Provider patterns for model name inference:
  - `{"phi", ProviderOllama}`
  - `{"llama", ProviderOllama}`
  - `{"qwen", ProviderOllama}`
  - `{"mistral", ProviderOllama}`
  - `{"ollama:", ProviderOllama}` (explicit prefix)
- Default rate limits (generous for local):
  - `TokensPerMinute: 1000000`
  - `MaxConcurrency: 2`
- `Ollama ProviderLimits` field in `RateLimitConfig` struct

### Phase 2: Client Implementation

**Files to create**: `pkg/agent/internal/llmimpl/ollama/client.go`

```go
type OllamaClient struct {
    client  *api.Client  // from github.com/ollama/ollama/api
    model   string
    hostURL string
}

func NewOllamaClientWithModel(hostURL, model string) llm.LLMClient
func (o *OllamaClient) Complete(ctx context.Context, in llm.CompletionRequest) (llm.CompletionResponse, error)
func (o *OllamaClient) Stream(ctx context.Context, in llm.CompletionRequest) (<-chan llm.StreamChunk, error)
func (o *OllamaClient) GetModelName() string
```

Key conversion functions:
- `convertMessagesToOllama()` - Convert `[]llm.CompletionMessage` to Ollama format
- `convertToolsToOllama()` - Convert `[]tools.ToolDefinition` to Ollama function format
- `convertToolCallsFromOllama()` - Extract `[]llm.ToolCall` from response

### Phase 3: Factory Integration

**Files to modify**: `pkg/agent/factory.go`

Add case in `createClientWithMiddleware()`:
```go
case config.ProviderOllama:
    hostURL := os.Getenv(config.EnvOllamaHost)
    if hostURL == "" {
        hostURL = "http://localhost:11434"
    }
    rawClient = ollama.NewOllamaClientWithModel(hostURL, modelName)
```

### Phase 4: API Key Handling

**Files to modify**: `pkg/config/config.go`

Update `GetAPIKey()` to handle Ollama specially:
- Return host URL instead of API key
- Don't require API key validation for Ollama provider

### Phase 5: Testing

**Files to create**: `pkg/agent/internal/llmimpl/ollama/client_test.go`

- Unit tests with mock HTTP server
- Integration tests requiring running Ollama instance

## Configuration

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `OLLAMA_HOST` | Ollama server URL | `http://localhost:11434` |

### Config.json Example

```json
{
  "agents": {
    "coder_model": "phi4:latest",
    "architect_model": "llama3.1:70b",
    "resilience": {
      "rate_limit": {
        "ollama": {
          "tokens_per_minute": 1000000,
          "max_concurrency": 2
        }
      }
    }
  }
}
```

### Known Models (Optional Registry)

```go
"phi4:latest": {
    Provider:         ProviderOllama,
    InputCPM:         0.0,  // Local = free
    OutputCPM:        0.0,
    MaxContextTokens: 16384,
    MaxOutputTokens:  4096,
},
```

## Considerations & Risks

| Consideration | Impact | Mitigation |
|---------------|--------|------------|
| Model capability variance | Not all local models handle complex tool calling well | Document recommended models for each agent role |
| Resource constraints | Local GPU memory limits concurrency | Default `max_concurrency: 2`, let users tune |
| Connection handling | Ollama might not be running | Add connectivity check in validation, graceful error |
| Context window limits | Varies wildly by model | Conservative defaults (16K), user can override |
| No streaming initially | Some agents expect streaming | Return stub (matches Gemini pattern) |

## Prerequisites

1. Ollama installed and running on the host
2. A tool-calling capable model pulled (e.g., `ollama pull phi4`)
3. `OLLAMA_HOST` set if not using default localhost

## References

- [Ollama Tool Calling Documentation](https://docs.ollama.com/capabilities/tool-calling)
- [Official Ollama Go API Package](https://pkg.go.dev/github.com/ollama/ollama/api)
- [Ollama Streaming Tool Calling Blog](https://ollama.com/blog/streaming-tool)
- [Best Ollama Models for Function Calling 2025](https://collabnix.com/best-ollama-models-for-function-calling-tools-complete-guide-2025/)
