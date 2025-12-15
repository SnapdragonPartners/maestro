# Phi4 Tool Calling in Ollama

This document describes attempts to set up Microsoft's Phi4 model for tool calling with Ollama, including the challenges encountered and why it doesn't work.

## Summary

Phi4 (14B) was **not natively trained for tool/function calling**. Despite extensive template engineering with strict instructions, Phi4 consistently outputs JSON wrapped in markdown code blocks rather than raw JSON that Ollama can parse into structured `tool_calls`. **There is no known working solution for Phi4 tool calling with Ollama.**

For tool calling with local models, use models trained for it: **llama3.2**, llama3.1, qwen2.5, or mistral.

## Attempted Solution: phi4:14b-q5-toolcalls

We attempted to create a custom Phi4 model with strict tool-calling instructions. Despite extensive template engineering, **this approach does not work reliably**.

### The Template

```modelfile
FROM phi4:latest  # or Q5_K_M GGUF

TEMPLATE """
{{- if or .System .Tools -}}
<|im_start|>system<|im_sep|>
{{- if .System }}{{ .System }}{{ end }}
{{- if .Tools }}

You have access to tools.

## Tool calling rules (STRICT)
- If you decide a tool must be called, respond with ONLY valid JSON (no markdown, no code fences, no backticks).
- The JSON MUST match exactly this schema:
  {"tool_calls":[{"name":"<tool_name>","arguments":{...}}]}
- NEVER use ```json or ``` or any markdown formatting around the JSON.
...
{{- end }}
<|im_end|>
{{- end }}
...
"""
```

### Test Results

Despite explicit instructions to never use markdown formatting, Phi4 consistently wraps JSON in code blocks:

**Actual response:**
```json
{
  "message": {
    "role": "assistant",
    "content": "```json\n{\"tool_calls\":[{\"name\":\"get_weather\",\"arguments\":{\"city\":\"Chicago\"}}]}\n```"
  }
}
```

**Expected (but not achieved):**
```json
{
  "message": {
    "role": "assistant",
    "content": "",
    "tool_calls": [
      {
        "function": {
          "name": "get_weather",
          "arguments": {"city": "Chicago"}
        }
      }
    ]
  }
}
```

The JSON appears in the `content` field wrapped in markdown, not in the structured `tool_calls` array. Ollama can only parse raw JSON (no markdown) into `tool_calls`.

### Why Templates Can't Fix This

Template instructions can guide model behavior but cannot override learned patterns. When Phi4 sees a request to output JSON, its training says "wrap it in markdown code blocks for readability." No amount of "NEVER use markdown" instructions consistently overrides this behavior.

---

## Historical Context: The Original Problem

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
| `mistral-nemo:latest` | **Yes** | Native support, tested and working ✅ |
| `llama3.2:latest` | **Yes** | Native support, works perfectly ✅ |
| `phi4:latest` | No | Returns "does not support tools" error |
| `phi4:14b-q5-toolcalls` | No | JSON wrapped in markdown in content field |
| `zac/phi4-tools` | No | JSON in content, not tool_calls |
| `jacob-ebey/phi4-tools` | No | JSON in content, not tool_calls |
| `phi4-reasoning:plus` | No | Returns "does not support tools" error |
| `allenporter/xlam:1b` | No | JSON in content (but correct format) |

## Working Alternatives

For maestro's agent system, use models with native Ollama tool support:

- **`mistral-nemo:latest` (12B)** - Native support, tested and working ✅
- **`llama3.2:latest` (3B)** - Native support, tested and working ✅
- `llama3.1:8b` / `llama3.1:70b` - Should work
- `qwen2.5:7b` and larger - Ollama docs use qwen as examples
- `mistral:7b` - Reported to have tool support

**Note**: Phi4 does NOT work for tool calling regardless of template configuration.

## Why Template Solutions Don't Work

Ollama's tool call parser can detect raw JSON output matching the expected schema. The theory was that a strict template could force clean JSON output:

1. **Explicitly forbid markdown formatting** - No code fences, backticks, or ```json blocks
2. **Require jq-parseable output** - Force the model to output clean JSON
3. **Use proper stop tokens** - Prevent extra content after the JSON

**In practice, this doesn't work.** Even with explicit "NEVER use markdown" instructions, Phi4 consistently wraps JSON in code blocks. The model's training to "format code/JSON nicely" overrides prompt instructions.

### Why All Attempts Failed

Both community templates (like `zac/phi4-tools`) and our custom strict templates produce the same result: JSON wrapped in markdown code blocks. Adding instructions like:

```
- NEVER use ```json or ``` or any markdown formatting around the JSON.
- The response must be valid JSON parseable by jq with no trailing characters.
```

...does not change Phi4's behavior. The model simply ignores these instructions.

## Other Potential Improvements

1. **Microsoft releases tool-trained Phi4** - A future Phi4 version trained for function calling would work natively without custom templates.

2. **Fine-tuning** - Fine-tune Phi4 specifically for tool calling output format for more reliable results.

## References

- [Ollama Tool Calling Docs](https://docs.ollama.com/capabilities/tool-calling)
- [GitHub Issue #9647 - Phi4 tool calling](https://github.com/ollama/ollama/issues/9647)
- [GitHub Issue #8337 - Empty content with tool calls](https://github.com/ollama/ollama/issues/8337)
- [zac/phi4-tools template](https://ollama.com/zac/phi4-tools/blobs/5620746093d7)
- [Microsoft Phi4-mini function calling blog](https://techcommunity.microsoft.com/blog/educatordeveloperblog/building-ai-agents-on-edge-devices-using-ollama--phi-4-mini-function-calling/4391029)
