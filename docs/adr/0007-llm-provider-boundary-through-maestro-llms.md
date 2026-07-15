+++
title = "ADR 0007: LLM Provider Boundary Through maestro-llms"
edit_date = "2026-07-15"
status = "deprecated"
summary = "v1 LLM provider boundary through the maestro-llms toolkit."
+++

# ADR 0007: LLM Provider Boundary Through maestro-llms

- Status: Proposed
- Date: 2026-07-06

## Context

Maestro originally owned provider clients and resilience middleware. That created
duplicated maintenance for OpenAI, Anthropic, Google/Gemini, Ollama, retries,
circuit breaking, timeouts, rate limiting, and error classification. The current
code depends on the extracted `github.com/SnapdragonPartners/maestro-llms` module.

## Decision

Provider I/O and generic provider resilience belong to `maestro-llms`. Maestro keeps
an app-facing `agent.LLMClient` contract and adapts it to `maestro-llms` through one
adapter seam.

Maestro owns app-specific behavior:

- transcript normalization for the app's context model
- tool choice policy for unattended toolloops
- agent-aware empty-response and pause-turn validation
- SUSPEND mapping into agent FSM behavior
- chat injection
- cost/story metrics and WebUI-facing rate-limit shape
- Gemini provider signature round-tripping through context/tool calls
- prompt templates, tool schemas, and state-machine transitions

Adding a provider or changing provider retry/circuit/rate-limit behavior should
generally happen in `maestro-llms`, not in Maestro.

## Current Implementation

- `go.mod` depends on `github.com/SnapdragonPartners/maestro-llms v0.4.2`.
- `pkg/agent/internal/llmadapter/` bridges Maestro's app contract to the toolkit.
- `pkg/agent/factory_llms.go` builds the shared LLM client factory and middleware
  chain.
- `pkg/contextmgr` and `pkg/agent/toolloop` preserve `ProviderSignature` for Gemini
  multi-turn function calling.
- `docs/MAESTRO_LLMS_MIGRATION.md` records the migration details and current
  ownership split.

## Consequences

- Provider-specific fixes should be upstreamed to `maestro-llms` when they are not
  Maestro-only behavior.
- Maestro tests should mock the LLM contract for state-machine behavior and reserve
  live provider tests for integration gates.
- The adapter seam is intentional; replacing it with direct toolkit types at every
  call site would be a separate, high-churn decision.
- Docs that mention in-tree provider clients are historical unless they discuss the
  current adapter boundary.

## Related Documents

- `docs/MAESTRO_LLMS_MIGRATION.md`
- `docs/OLLAMA.md`
- `docs/PHI4.md`
- `docs/GEMINI_INTEGRATION_PLAN.md`
- `pkg/agent/doc.go`

