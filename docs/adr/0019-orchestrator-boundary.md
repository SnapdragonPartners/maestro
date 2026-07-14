+++
title = "ADR 0019: Orchestrator Boundary"
edit_date = "2026-07-14"
status = "live"
summary = "Defines the v2 Orchestrator as the programmatic, non-agentic layer owning agent lifecycle, tools, routing, forge, persistence, and scheduling — with the no-inference rule as the boundary test."
+++

# 0019. Orchestrator Boundary

Status: Accepted (Codex + DR, 2026-07-13); amended 2026-07-14 (work dispatch is Orchestrator machinery at both Epic and Story grain; dispatcher lineage corrected to rework)

## Context

The intake revision (roadmap D2, 2026-07-12) cemented the Orchestrator as v2's core component: it owns intake, Work Group lifecycle, and the dispatch seams that the Workbench and factory both use. A component this central needs a crisp boundary, or it drifts in one of two bad directions — becoming a hidden mega-agent (workflow logic buried in prompts), or being conflated with the agents it manages so that "just one small LLM call" creeps into infrastructure that must stay deterministic and fault tolerant.

## Decision

### What the Orchestrator is

The software layer that manages agents and the factory's foundational machinery: agent launch and destruction, Work Group lifecycle (per ADR 0018), tool implementation, message routing, forge interaction, persistence, scheduling, deterministic gate evaluation and enforcement (never review judgment), and restart/watchdog policy. It is entirely programmatic — maximally fault tolerant, and deterministic to the extent software can be.

### What the Orchestrator is not

It is not an agent. It never interacts with an LLM at any point in its lifecycle — only through the agents it spawns. It has no prompt, no persona, and no conversation state.

### The boundary rule

**Decisions from rules and config belong to the Orchestrator; decisions requiring inference belong to an agent.** The moment an LLM gets involved in a workflow step, that step is an agent — however small or short-lived. This is a mechanical test anyone (human or agent) can apply when designing a workflow: routing, retries, scheduling, and gate checks driven by configuration are Orchestrator work; anything needing judgment, language understanding, or generation is an agent, even a single-call one. Applied to intake: collecting structured answers from the operator is Orchestrator work; the escalation that consults a model to answer what the operator cannot spawns a short-lived agent.

The Orchestrator routes escalations and enforces bounds (e.g. contention limits, budgets) but never resolves ambiguity — resolution belongs to agents or humans.

### Seams

The Orchestrator exposes a dispatch seam consumed by intake and the Workbench entry (the blank-Feature-request contract in ADR 0018), and owns the artifact-persistence and message-routing seams the agents write through. The intake artifact contract (Phase 0 item 6) binds to these seams while leaving the intake executor open.

**Work dispatch is Orchestrator machinery at every grain** (amended 2026-07-14, resolving a v1 inheritance the port inventory surfaced). Principals author the work graph: humans or triage agents author Feature and Epic framing (ADRs 0021, 0024), and the Architect authors the Story decomposition and its dependency graph — inference and judgment, reviewed under ADR 0020 whoever the author is. Dispatching dependency-ready work to available executors is rules, not judgment, and belongs to the Orchestrator — at Story grain exactly as at the Epic grain of ADR 0024. v1 locating Story dispatch in the Architect was an accident of who held the queue, not a design decision; assignment policy (round-robin, affinity) is configuration by the boundary rule. The **durable backlog is the authoritative scheduler state**; transport — typed channels today, possibly RPC if agents ever split into separate runtimes — is delivery plumbing, never state. When Epics, Stories, or the DAG are amended or superseded, the Orchestrator invalidates the pending version-bound dispatch records, re-evaluates the DAG deterministically, and issues fresh version-bound dispatches — no agent in the loop, and never by draining in-flight channels, which races consumers and would not survive a queue-backend change. The policy for work already *executing* when its record is amended — cancel, suspend, or complete-then-reconcile — is Phase 3 runtime design, tracked in the ADR backlog.

### v1 lineage

The Orchestrator is the evolution of v1's runtime kernel, supervisor, and dispatcher — all classified **rework** in the port inventory (Phase 0 item 10, correcting D8's first-pass "port largely as-is"): the typed-channel discipline carries forward, but the package structures are re-cut — v1's Story/hotfix queues, spec exceptions, and Architect-held Story dispatch do not survive (dispatch moves here, per the amendment above). This ADR supersedes the single-user framing of historical note [0002](0002-local-single-user-runtime-kernel.md) for v2 design intent; the channel-dispatch discipline of [0004](0004-channel-dispatch-and-typed-agent-protocol.md) carries forward. The v3 trajectory (orchestration plane: supervisor and dispatcher for agents running in external environments) is sketched in roadmap pillar 15 and deliberately not designed here.

## Consequences

- Every future workflow design gets a mechanical litmus test; "add a small LLM call to the dispatcher" is a category error by definition, not a judgment call.
- Orchestrator code is testable deterministically with ordinary unit and integration tests; golden stories measure the agents and the harness around them. The reliability budget concentrates where reliability is cheap.
- Agent sprawl has a counterweight: a step is an agent only when it needs inference, and infrastructure never silently becomes one.
- Cloud/queue execution (v3) changes where agents run, not what the Orchestrator is.

## Related Documents

- [ADR 0018](0018-v2-work-taxonomy.md) (Work Group lifecycle ownership, dispatch contracts), [ADR 0017](0017-v2-documentation-authority-and-lifecycle.md).
- [Roadmap](../v2/roadmap.md) Core Vocabulary (Orchestrator), D2, pillar 15; [ADR backlog](../v2/adr-backlog.md) Orchestrator Boundary entry.
- Historical notes [0002](0002-local-single-user-runtime-kernel.md) (superseded for v2 by this ADR) and [0004](0004-channel-dispatch-and-typed-agent-protocol.md) (discipline carried forward).
