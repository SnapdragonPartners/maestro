# Budget Review Story Edit + Temperature Configuration

## Problem

Budget review thrashing: when a coder fails to complete a story within its iteration budget, the architect rejects the budget review and the story is requeued. However, the requeued story is identical to the original, and with temperature at 0.0 (the float32 zero value — never explicitly set), the next coder attempt produces the same deterministic output and fails in the same way.

Two specific issues were identified:

1. **`attemptStoryEdit` only called on auto-reject at hard limit (streak >= 6).** The architect LLM can independently decide to REJECT a budget review at any streak count. When it does, the story is requeued without any annotation — `attemptStoryEdit` is never called. This means the next coder has zero additional context about what went wrong.

2. **Temperature was never set.** All toolloop `Config` structs left `Temperature` at the float32 zero value. The `TemperatureDefault` and `TemperatureDeterministic` constants in `pkg/agent/llm/api.go` were unused — the toolloop constructs `CompletionRequest` directly, bypassing `NewCompletionRequest`. At temperature 0.0, identical prompts produce identical outputs, guaranteeing repeated failures.

## Changes

### Part A: Story Edit on Any Rejection

The architect now calls `attemptStoryEdit` whenever it returns a REJECTED status for a budget review, not just at the hard-limit auto-reject. This gives the architect LLM a chance to annotate the story with implementation notes, pitfalls, and clarifications before the story is requeued.

The story edit template (`pkg/templates/architect/story_edit.tpl.md`) was updated to handle both scenarios:
- **Auto-reject (streak >= 6)**: Multiple failed attempts, emphasizes persistent issues
- **LLM-reject (streak < 6)**: Single failed attempt, emphasizes what went wrong and how to fix it

Both paths emphasize that the next coder has **no memory** of previous attempts.

### Part B: Centralized Temperature Configuration

Temperature settings are now centralized in the `AgentConfig` section of `config.json`:

```json
{
  "agents": {
    "coder_planning_temp": 0.60,
    "coder_coding_temp": 0.25,
    "coder_hotfix_temp": 0.10,
    "architect_temp": 0.65,
    "pm_temp": 0.30
  }
}
```

All values have sensible defaults applied when not set in config.

#### Temperature Defaults

| Role / Phase | Default | Rationale |
|---|---|---|
| Architect | 0.65 | Needs creative latitude for reviews, story editing, questions |
| Coder - planning | 0.60 | Exploring solution space, considering alternatives |
| Coder - coding | 0.25 | Low variance for code generation, tool calling |
| Coder - hotfix | 0.10 | Minimal variance for targeted fixes |
| PM | 0.30 | Structured output, spec generation |

#### Temperature Laddering

When a coder receives NEEDS_CHANGES feedback from the architect, the temperature is incrementally increased to escape local minima. The coder tracks its own NEEDS_CHANGES counter (separate from the architect's streak counter).

| Phase | Formula | Cap |
|---|---|---|
| Planning | min(0.85, base + 0.05 * k) | 0.85 |
| Coding | min(0.45, base + 0.03 * k) | 0.45 |
| Hotfix | min(0.20, base + 0.02 * k) | 0.20 |

Where `k` = number of NEEDS_CHANGES received, `base` = config default for that phase.

The counter resets on APPROVED and when a new story begins (SETUP state).

#### Constant Cleanup

The unused constants `TemperatureDefault` (0.3) and `TemperatureDeterministic` (0.2) in `pkg/agent/llm/api.go` were deleted. All temperature values now come exclusively from config, enforced by the compiler.

#### Metrics Logging

The LLM call metrics log line in `pkg/agent/middleware/metrics/middleware.go` now includes the temperature used for each call, aiding in debugging and tuning.

## Deferred

- **top_p**: Would require adding a `TopP` field to `CompletionRequest` and changes to all 4 provider implementations (Anthropic, OpenAI, Google, Ollama)
- **Max output tokens**: Current values (8192 coder, 5000 architect) are tuned for tool-calling patterns and don't need adjustment

## Verification

```bash
make build    # Compile + lint
make test     # Unit tests
```

To verify temperature in production: run with `debug.llm_messages: true` and confirm log lines show `temp: 0.25` (or appropriate value) for each LLM call.
