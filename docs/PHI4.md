# Phi4 Tool Calling Issues in Ollama

This document describes the challenges encountered when attempting to use Microsoft's Phi4 model for tool calling with Ollama.

## Summary

Phi4 (14B) was **not trained for tool/function calling**. While the model can understand and generate JSON, it does not produce the structured `message.tool_calls` response that Ollama's API provides for models with native tool support.

## The Problem

### Expected Behavior (llama3.2)

When a tool-calling-capable model like `llama3.2` receives a tool request, Ollama returns:

```json
{
  "message": {
    "role": "assistant",
    "content": "",
    "tool_calls": [
      {
        "id": "call_abc123",
        "function": {
          "name": "get_weather",
          "arguments": {"location": "San Francisco"}
        }
      }
    ]
  }
}
```

The `tool_calls` array is populated, and `content` is empty. This structured format is what maestro's toolloop expects.

### Actual Behavior (phi4)

With `phi4:latest` or community variants like `zac/phi4-tools` and `jacob-ebey/phi4-tools`, Ollama returns:

```json
{
  "message": {
    "role": "assistant",
    "content": "```json\n{\"name\": \"get_weather\", \"parameters\": {\"location\": \"San Francisco\"}}\n```"
  }
}
```

The JSON is embedded in the text `content` field (often wrapped in markdown code blocks), and `tool_calls` is absent.

## Root Cause

1. **Phi4 was not trained for tool use** - The Ollama team has stated they track upstream models closely, and since Phi4 doesn't have native tool calling, they won't add it ([GitHub Issue #9647](https://github.com/ollama/ollama/issues/9647)).

2. **Template limitations** - Community models like `zac/phi4-tools` modify the Modelfile template to prompt Phi4 to output JSON, but this doesn't trigger Ollama's structured tool call parsing. The template instructs the model to respond with JSON, but Phi4 adds explanations and markdown formatting.

3. **Ollama's parser requires specific patterns** - The Ollama tool call parser uses template-specific markers to identify and extract tool calls. Models like llama3.2 were trained to output in formats the parser recognizes.

## Attempted Solutions

### Custom Modelfile Templates

We tried several template variations:

1. **xLAM-style template** - Using the format from `allenporter/xlam:1b`
2. **Strict JSON-only prompts** - Explicitly forbidding markdown and explanations
3. **llama3.2-style template** - Adapting llama3.2's template to Phi4's token format

All attempts resulted in Phi4 outputting JSON as text content rather than structured tool calls. The model consistently:
- Adds explanatory text before/after JSON
- Wraps JSON in markdown code blocks
- Uses `{"name": ..., "parameters": ...}` format instead of triggering Ollama's parser

### Models Tested

| Model | Tool Calls Structured? | Notes |
|-------|----------------------|-------|
| `llama3.2:latest` | Yes | Native support, works perfectly |
| `phi4:latest` | No | Returns "does not support tools" error |
| `zac/phi4-tools` | No | JSON in content, not tool_calls |
| `jacob-ebey/phi4-tools` | No | JSON in content, not tool_calls |
| `phi4-reasoning:plus` | No | Returns "does not support tools" error |
| `allenporter/xlam:1b` | No | JSON in content (but correct format) |

## Working Alternatives

For maestro's agent system, use models with native Ollama tool support:

- `llama3.2:latest` (3B) - Tested and working
- `llama3.1:8b` / `llama3.1:70b` - Should work
- `qwen2.5:7b` and larger - Ollama docs use qwen as examples
- `mistral:7b` - Reported to have tool support

## Potential Future Solutions

1. **Microsoft releases tool-trained Phi4** - A future Phi4 version trained for function calling would work natively.

2. **Text-based tool parsing** - Add parsing logic to extract tool calls from text content. This would require:
   - Detecting JSON patterns in response content
   - Extracting and parsing the JSON
   - Converting to `llm.ToolCall` structs
   - Handling various formatting (code blocks, explanations)

3. **Fine-tuning** - Fine-tune Phi4 specifically for tool calling output format.

## References

- [Ollama Tool Calling Docs](https://docs.ollama.com/capabilities/tool-calling)
- [GitHub Issue #9647 - Phi4 tool calling](https://github.com/ollama/ollama/issues/9647)
- [GitHub Issue #8337 - Empty content with tool calls](https://github.com/ollama/ollama/issues/8337)
- [zac/phi4-tools template](https://ollama.com/zac/phi4-tools/blobs/5620746093d7)
- [Microsoft Phi4-mini function calling blog](https://techcommunity.microsoft.com/blog/educatordeveloperblog/building-ai-agents-on-edge-devices-using-ollama--phi-4-mini-function-calling/4391029)
