+++
title = "Spike Report: Toolloop Ownership"
edit_date = "2026-07-13"
status = "live"
type = "spike"
summary = "Is a Maestro-owned toolloop distinct from maestro-llms still justified? Recommendation: yes as a harness layer, no as an engine — converge on llms/toolloop during the D8 port, contingent on the upstream requests in the maestro-llms wishlist."
+++

# Spike Report: Toolloop Ownership

Status: live — approved by Codex and DR, 2026-07-13. Phase 0 item 8. Question (from roadmap D8): is maintaining a Maestro-owned toolloop distinct from the `maestro-llms` toolloop still justified?

## Method

Code survey of both implementations: Maestro's `pkg/agent/toolloop/` and `maestro-llms`'s `llms/toolloop/` (verified byte-identical between the local repo and the pinned v0.7.1, so the toolkit loop is already available to us without a dependency bump). Feature inventory, coupling analysis, and call-site count. No code written; no refactor performed (spike rules).

## Findings

**The two loops are not competitors — they are two layers, correctly drawn on both sides.** `maestro-llms`'s own ADR-0011 lists agent state, terminal tools, persistence, and audit as *binding non-goals*; Maestro's loop exists almost entirely to provide exactly those.

- **Maestro's loop (~1,150 LOC non-test)** carries nine features the toolkit loop deliberately excludes, and every one is harness-specific under v2 doctrine: terminal-tool/ProcessEffect state signals (the 0022 guardrail's enforcement point), soft/hard escalation (0020's bounded contention), tool-execution persistence with Story lineage (the 0022 seam), activity heartbeats for the watchdog (Orchestrator machinery), BlockedError→FailureInfo routing (failure taxonomy), ContextManager ownership (an H lever), per-tool circuit breaking, graceful shutdown, and semantic failure classification.
- **The toolkit's loop (~580 LOC non-test)** is provider-generic by design and has genuinely better fundamentals where the two overlap: fail-closed config validation, a richer typed ToolChoice model, usage aggregation, an immutable transcript, and observation hooks with latency.
- **The real duplication is only the inner engine** — the iterate/execute/append loop, ProviderSignature round-trip, cancellation handling — a few hundred lines. That duplication has a real cost: provider-handling improvements land in the toolkit loop (usage semantics, tool-choice typing) and Maestro's copy drifts, exactly the class of bug the 0.7.1 `BillableOutputTokens` episode already demonstrated once.
- **Two required capabilities block convergence today, plus one design review**:
  1. *Turn-boundary stop* (required): the toolkit loop terminates only on a no-tool final answer, max iterations, or error — no affordance a terminal tool could trigger, and a context-cancellation hack would conflate terminal exit with user cancellation. The required shape is a **latched, typed stop honored at the turn boundary**: every sibling tool call in the turn executes, every result appends, then the loop returns with the typed reason.
  2. *Fallible pre-request extension* (required): the toolkit copies its transcript (immutable by design) and its hooks fire post-response only, while Maestro flushes buffered human input, injects pre-iteration guidance, and records activity heartbeats *before* each provider call — and the flush can fail during context compaction, so the hook must propagate errors.
  3. *Infallible observation hooks* (design review, not a blocker): nothing durable can ride a hook that cannot return errors, but durable audit has an adapter-side answer regardless — persistence wraps the adapted `Execute`, which is synchronous and error-capable. The review is about the constraint shaping every consumer's architecture, not about unblocking Maestro.
- **Escalation mapping**: soft limits ride the toolkit's `OnIteration` counter (the warning callback is Maestro-side and fires between iterations). The hard limit does **not** map directly onto the toolkit's `MaxIterations` — the toolkit stops *before* executing the limit-hitting response's tool calls, while Maestro executes that iteration and then escalates; a direct translation drops work or costs an extra provider call. Instead, the harness layer counts iterations via `OnIteration` and triggers the turn-boundary stop (capability 1) with a typed hard-limit reason, escalating after the final turn completes.
- Replacement blast radius if the engine converges *behind the existing API*: near zero — 15 production `toolloop.Run` call sites across coder/architect/pm keep their `Config[T]`/`Outcome[T]` contract; only the loop's internals change.

## Governing principle

`maestro-llms` is a general-purpose package with multiple consumers, of which Maestro is one. Two rules follow (per DR): **DRY** — use toolkit functionality where it exists rather than maintaining our own; and **upstream-first** — anything Maestro builds that fits the package's general purpose and could serve other consumers is at least considered for implementation there. The toolkit is not static; feature requests are the normal path, and its ADR-0011 non-goals are its owner's to revisit, not walls to build around.

## Recommendation

**Yes as a harness layer; no as an engine.** Two independently maintained full loops are not justified — but neither is abandoning the Maestro layer for the concerns that are genuinely Maestro-specific.

1. **Converge during the D8 port, not before.** Reshape `pkg/agent/toolloop` into a thin harness layer whose inner engine is `llms/toolloop.Run`: Maestro's `tools.Tool` adapts to the toolkit's `Tool`, durable audit persistence wraps the adapted `Execute` (synchronous, error-capable — never an infallible hook), and the external `Config[T]`/`Outcome[T]` contract is preserved so the 15 production call sites migrate without change.
2. **Sort every Maestro-loop feature by upstream candidacy**, not by current toolkit non-goals:
   - **Required upstream requests**: the turn-boundary stop (latched, typed, sibling calls execute before return — serving both terminal completion and hard-limit escalation), and a controlled, *fallible* pre-request extension point (message injection/flush plus pre-call observation, able to abort). Alongside these, request a review of the infallible observation-hook design — a reasonable multi-consumer need, not Maestro special-pleading.
   - **Strong upstream candidate**: the per-tool circuit breaker (provider-generic; its only coupling is logging).
   - **Worth proposing**: the terminal-tool protocol itself as an optional mode — "one goal, one exit" is agent-pattern-generic, though it requires the toolkit revisiting ADR-0011's non-goal. A feature request, never a fork.
   - **Stays in Maestro** (genuinely harness- or convention-specific): the persistence seam and Story lineage (0022), activity heartbeats for the watchdog (0019), BlockedError→FailureInfo routing (failure taxonomy), ContextManager ownership, ProcessEffect→state-machine signals, and **semantic failure classification** — it interprets Maestro's own JSON `success:false` convention, which the toolkit rightly treats as opaque; the Maestro tool adapter applies it and sets the toolkit's existing `ToolResult.IsError`.
   - **Composable from existing hooks**: escalation soft limits ride `OnIteration`; the hard limit rides the turn-boundary stop with a typed reason (see the escalation-mapping finding — the toolkit's `MaxIterations` is not an equivalent).
3. **If upstream declines the required extension**, keep Maestro's loop standalone — do **not** fork the toolkit loop. Given shared ownership, this outcome is unlikely; the point is that forking is off the table either way.
4. **D8 inventory input**: `pkg/agent/toolloop` moves from "port largely as-is" (the D8 first-pass guess) to **"port with rework: harness layer over llms/toolloop, with upstream feature requests filed first."** The terminal-tool discipline itself is unchanged doctrine (0022); only its plumbing moves.
5. **No Phase 1 impact.** Phase 1 patches v1 minimally; no toolloop work happens before the port.

The upstream requests are formalized as a standing wishlist document the toolkit team can annotate: [requirements_maestro-llms-wishlist.md](../requirements_maestro-llms-wishlist.md).

No spike scripts were produced (this was a reading spike); `spikes/phase_0/` is not needed for this item.

## Related Documents

- [Roadmap](../roadmap.md) D8 and the Phase 0 spike bracket; [Phase 0 plan](scope-and-plan.md) item 8.
- ADRs [0019](../../adr/0019-orchestrator-boundary.md) (watchdog/persistence as Orchestrator machinery), [0021](../../adr/0021-artifacts-and-principal-instances.md)/[0022](../../adr/0022-v2-data-plane.md) (tool call as Audit action unit; terminal-tool guardrail), [0020](../../adr/0020-review-invariant-reviewer-vs-partner.md) (bounded contention).
- Historical note [0006](../../adr/0006-toolloop-process-effect-and-terminal-tools.md) (the v1 toolloop design this spike revisits); maestro-llms ADR-0011 (its toolloop's binding non-goals).
