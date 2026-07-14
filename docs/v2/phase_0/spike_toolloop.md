+++
title = "Spike Report: Toolloop Ownership"
edit_date = "2026-07-13"
status = "draft"
type = "spike"
summary = "Is a Maestro-owned toolloop distinct from maestro-llms still justified? Recommendation: yes as a harness layer, no as an engine — converge on llms/toolloop as the inner engine during the D8 port, contingent on one small upstream extension."
+++

# Spike Report: Toolloop Ownership

Status: draft. Phase 0 item 8. Question (from roadmap D8): is maintaining a Maestro-owned toolloop distinct from the `maestro-llms` toolloop still justified?

## Method

Code survey of both implementations: Maestro's `pkg/agent/toolloop/` and `maestro-llms`'s `llms/toolloop/` (verified byte-identical between the local repo and the pinned v0.7.1, so the toolkit loop is already available to us without a dependency bump). Feature inventory, coupling analysis, and call-site count. No code written; no refactor performed (spike rules).

## Findings

**The two loops are not competitors — they are two layers, correctly drawn on both sides.** `maestro-llms`'s own ADR-0011 lists agent state, terminal tools, persistence, and audit as *binding non-goals*; Maestro's loop exists almost entirely to provide exactly those.

- **Maestro's loop (~1,150 LOC non-test)** carries nine features the toolkit loop deliberately excludes, and every one is harness-specific under v2 doctrine: terminal-tool/ProcessEffect state signals (the 0022 guardrail's enforcement point), soft/hard escalation (0020's bounded contention), tool-execution persistence with Story lineage (the 0022 seam), activity heartbeats for the watchdog (Orchestrator machinery), BlockedError→FailureInfo routing (failure taxonomy), ContextManager ownership (an H lever), per-tool circuit breaking, graceful shutdown, and semantic failure classification.
- **The toolkit's loop (~580 LOC non-test)** is provider-generic by design and has genuinely better fundamentals where the two overlap: fail-closed config validation, a richer typed ToolChoice model, usage aggregation, an immutable transcript, and observation hooks with latency.
- **The real duplication is only the inner engine** — the iterate/execute/append loop, ProviderSignature round-trip, cancellation handling — a few hundred lines. That duplication has a real cost: provider-handling improvements land in the toolkit loop (usage semantics, tool-choice typing) and Maestro's copy drifts, exactly the class of bug the 0.7.1 `BillableOutputTokens` episode already demonstrated once.
- **One genuine gap blocks convergence today**: the toolkit loop terminates only on a no-tool final answer, max iterations, or error — it has no early-exit affordance a terminal tool could trigger. Maestro's protocol (forced tool choice, exit via terminal ProcessEffect) cannot be expressed over it without either a small upstream extension (e.g. a `Terminal` flag on `ToolResult`, or a stop sentinel) or a context-cancellation hack that would conflate terminal exit with user cancellation.
- Replacement blast radius if the engine converges *behind the existing API*: near zero — 17 `toolloop.Run` call sites across coder/architect/pm keep their `Config[T]`/`Outcome[T]` contract; only the loop's internals change.

## Governing principle

`maestro-llms` is a general-purpose package with multiple consumers, of which Maestro is one. Two rules follow (per DR): **DRY** — use toolkit functionality where it exists rather than maintaining our own; and **upstream-first** — anything Maestro builds that fits the package's general purpose and could serve other consumers is at least considered for implementation there. The toolkit is not static; feature requests are the normal path, and its ADR-0011 non-goals are its owner's to revisit, not walls to build around.

## Recommendation

**Yes as a harness layer; no as an engine.** Two independently maintained full loops are not justified — but neither is abandoning the Maestro layer for the concerns that are genuinely Maestro-specific.

1. **Converge during the D8 port, not before.** Reshape `pkg/agent/toolloop` into a thin harness layer whose inner engine is `llms/toolloop.Run`: Maestro's `tools.Tool` adapts to the toolkit's `Tool`, the observation hooks carry the harness concerns, and the external `Config[T]`/`Outcome[T]` contract is preserved so the 17 call sites migrate without change.
2. **Sort every Maestro-loop feature by upstream candidacy**, not by current toolkit non-goals:
   - **Required upstream request**: an early-termination affordance (a `ToolResult` terminal flag or stop sentinel) — small, provider-generic, and the convergence blocker.
   - **Strong upstream candidates**: the per-tool circuit breaker (provider-generic, its only coupling is logging) and semantic failure classification — any toolloop consumer benefits.
   - **Worth proposing**: the terminal-tool protocol itself as an optional mode — "one goal, one exit" is agent-pattern-generic, not Maestro-specific, though it requires the toolkit revisiting ADR-0011's non-goal. A feature request, never a fork.
   - **Stays in Maestro** (genuinely harness-specific): the persistence seam and Story lineage (0022), activity heartbeats for the watchdog (0019), BlockedError→FailureInfo routing (failure taxonomy), ContextManager ownership, and ProcessEffect→state-machine signals.
   - **Composable from existing hooks**: escalation soft/hard limits can likely build on the toolkit's `OnIteration` — adopt, don't duplicate.
3. **If upstream declines the required extension**, keep Maestro's loop standalone — do **not** fork the toolkit loop. Given shared ownership, this outcome is unlikely; the point is that forking is off the table either way.
4. **D8 inventory input**: `pkg/agent/toolloop` moves from "port largely as-is" (the D8 first-pass guess) to **"port with rework: harness layer over llms/toolloop, with upstream feature requests filed first."** The terminal-tool discipline itself is unchanged doctrine (0022); only its plumbing moves.
5. **No Phase 1 impact.** Phase 1 patches v1 minimally; no toolloop work happens before the port.

No spike scripts were produced (this was a reading spike); `spikes/phase_0/` is not needed for this item.

## Related Documents

- [Roadmap](../roadmap.md) D8 and the Phase 0 spike bracket; [Phase 0 plan](scope-and-plan.md) item 8.
- ADRs [0019](../../adr/0019-orchestrator-boundary.md) (watchdog/persistence as Orchestrator machinery), [0021](../../adr/0021-artifacts-and-principal-instances.md)/[0022](../../adr/0022-v2-data-plane.md) (tool call as Audit action unit; terminal-tool guardrail), [0020](../../adr/0020-review-invariant-reviewer-vs-partner.md) (bounded contention).
- Historical note [0006](../../adr/0006-toolloop-process-effect-and-terminal-tools.md) (the v1 toolloop design this spike revisits); maestro-llms ADR-0011 (its toolloop's binding non-goals).
