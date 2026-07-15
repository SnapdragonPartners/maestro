+++
title = "ADR 0016: Architect Per-Agent Conversation Context"
edit_date = "2026-07-15"
status = "deprecated"
summary = "v1 architect per-agent conversation context design."
+++

# ADR 0016: Architect Per-Agent Conversation Context

- Status: Proposed
- Date: 2026-07-06

## Context

The architect communicates with multiple agents (each coder and the PM) across the
lifecycle of a story: plan reviews, iterative Q&A, code reviews, and spec review. A
single shared conversation context caused two problems: contradictory feedback
(the architect "forgetting" its own earlier guidance) and bloated prompts (repeating
full story context on every request). Context boundaries must also survive resume and
story reassignment, where the request payload may disagree with the authoritative
lease.

## Decision

Maintain one `ContextManager` per agent the architect talks to, keyed by agent ID.
Each context opens with a persistent system prompt carrying the agent ID, story ID,
full story details, and role/tool descriptions, so per-request prompts carry only the
request content plus a brief instruction. Story-specific knowledge packs are delivered
through request content rather than stored on the story record.

Scope each context to the current story idempotently: on every request, compare the
context's template name (`agent-{agentID}-story-{storyID}`) against the current story
and reset the context with a fresh system prompt on mismatch. Use the dispatcher lease
as the authoritative story source, not the request payload, to avoid desync during
resume/reassignment. Guard concurrent context creation with double-checked locking.

## Current Implementation

- `pkg/architect/driver.go` holds `agentContexts map[string]*contextmgr.ContextManager`
  guarded by `contextMutex` (RWMutex), with `getContextForAgent()` and
  `ensureContextForStory()`; `ResetAgentContext()` remains as a legacy wrapper.
- `ensureContextForStory()` is called at the top of `handleRequest()`
  (`pkg/architect/request.go`) using `Dispatcher.GetStoryForAgent()` as the story
  source of truth.
- All request handlers (single-turn review, iterative question, iterative approval,
  spec review) use the per-agent context.

## Consequences

- The architect gives consistent, non-contradictory feedback within a story and pays
  far smaller per-request prompts.
- Story transitions are detected structurally (template-name comparison), so no
  external "reset context" trigger is needed.
- The lease — not the request payload — is the authoritative story binding; new
  request handlers must preserve that invariant.
- This context model is architect-specific; it is a consequence of the architect's
  reviewer/coordinator role (ADR 0003) and does not generalize to coder/PM agents.

## Related Documents

- `docs/ARCHITECT_CONTEXT.md`
- `CLAUDE.md` (Architect Context Management)
- ADR 0003 (agent roles and FSMs)
- ADR 0004 (typed agent protocol)
