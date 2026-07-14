+++
title = "Maestro Wishlist For maestro-llms"
edit_date = "2026-07-13"
status = "live"
type = "requirements"
summary = "Maestro's running feature-request wishlist to the maestro-llms team, with generality arguments and a response field per item — so the toolkit team can say what they're comfortable adding given their other consumers."
+++

# Maestro Wishlist For maestro-llms

Status: live (approved by Codex and DR, 2026-07-13) — awaiting maestro-llms team responses.

`maestro-llms` is a shared, general-purpose package; Maestro is one first-class consumer among several. This document is Maestro's running request list, governed by the shared-package principle: we use toolkit functionality where it exists (DRY), we propose upstream anything that would serve other consumers, we prefer non-breaking additions, and we never fork. Each item carries a generality argument and a **Response** field for the toolkit team (comfortable / needs discussion / declined — with notes). Declined items get Maestro-side answers; nothing here is a demand.

Items 1–5 originate from the Phase 0 toolloop spike ([spike report](phase_0/spike_toolloop.md)); more will follow (Phase 1 expects to propose reusable metrics-capture pieces).

## 1. Turn-boundary stop in `llms/toolloop` — required for convergence

- **What**: a way to end the loop at a **turn boundary** with a typed reason: the stop latches (e.g. via a `ToolResult` flag or stop sentinel), every sibling tool call in the current turn still executes, every result is appended, and the loop then returns a distinct outcome carrying the typed reason.
- **Why (Maestro)**: two uses of the same mechanism. First, terminal completion — our protocol is "one goal, one terminal tool" (forced tool choice, exit signaled by a terminal tool), which the current loop cannot express. Second, hard-limit escalation: Maestro executes the limit-hitting iteration and then escalates, whereas the toolkit's `MaxIterations` stops *before* executing the limit-hitting response's tool calls — a direct mapping would drop work or cost an extra provider call. A latched turn-boundary stop serves both.
- **Generality**: any consumer building goal-directed loops needs a tool-driven exit that doesn't discard in-flight sibling results; a no-tool final answer is the wrong terminator whenever tool choice is forced.
- **Breaking-ness**: additive in spirit, but not categorically non-breaking — a new outcome kind extends a set maestro-llms ADR-0011 declares closed (requires a superseding toolkit ADR), and a new exported struct field can break unkeyed composite literals. Framed as an additive proposal requiring compatibility and ADR review.
- **Response**: _pending_

## 2. Controlled, fallible pre-request extension point — required for convergence

- **What**: an optional hook invoked before each provider call, able to append messages to the outgoing request (injection/flush), observe pre-call state, and **fail** — returning an error or typed abort that ends the run cleanly.
- **Why (Maestro)**: we flush buffered human input, inject pre-iteration guidance, and record activity heartbeats before each call. The toolkit's transcript is deliberately immutable and its hooks fire post-response only. Fallibility is not optional for us: the buffered-input flush can fail during context compaction, and an infallible callback would silently lose that existing failure contract.
- **Generality**: mid-loop context injection (fresh user input, tool-derived reminders, budget warnings) is a common agent-loop need; a *controlled* extension (append-only, or request-copy-in/request-out) preserves the toolkit's immutability guarantees for consumers that don't opt in, and a pre-request hook that can abort is the natural place for consumer-side preflight.
- **Breaking-ness**: additive optional hook; non-breaking.
- **Response**: _pending_

## 3. Design review: fallible hooks

- **What**: not a feature request — a request to revisit the decision that observation hooks cannot return errors, e.g. additive error-returning variants alongside the existing signatures. (Items 1–2 carry the narrow fallibility Maestro requires; this item is the broader, optional review of the post-response observation hooks.)
- **Why (Maestro)**: nothing durable can ride an infallible hook. We route audit persistence around adapted `Execute` instead, so this does not block convergence — but the constraint shapes every consumer's architecture.
- **Generality**: audit, quota enforcement, and policy-abort use cases all eventually want a hook that can fail the run.
- **Breaking-ness**: signature change if done in place — hence framed as a review; additive variants would be non-breaking.
- **Response**: _pending_

## 4. Per-tool circuit breaker — strong candidate

- **What**: Maestro's per-tool circuit breaker (fingerprinted by tool+params+error, consecutive-failure threshold, trip callback, synthetic skip result) offered upstream as an optional toolloop component. We would contribute the implementation (~200 LOC; its only coupling is logging).
- **Why (Maestro)**: prevents burning iterations on a deterministically failing tool.
- **Generality**: entirely provider- and application-generic; any loop consumer benefits.
- **Breaking-ness**: additive optional component; non-breaking.
- **Response**: _pending_

## 5. Terminal-tool protocol as an optional mode — proposal, lower priority

- **What**: the full "one goal, one terminal tool" protocol (forced tool choice + typed terminal exit) as an opt-in toolloop mode.
- **Why (Maestro)**: it is our entire loop discipline; item 1 alone lets us build it consumer-side, so this is a consolidation proposal, not a blocker.
- **Generality**: the pattern is agent-generic — but we note it touches maestro-llms ADR-0011's stated non-goals, so this is explicitly the toolkit owner's call to revisit or decline.
- **Breaking-ness**: opt-in mode; non-breaking.
- **Response**: _pending_

## Non-requests

For clarity about the boundary, these stay Maestro-side by our own analysis: tool-execution persistence and Story lineage, watchdog heartbeat integration, BlockedError/FailureInfo routing, ContextManager ownership, ProcessEffect→state-machine signals, and semantic failure classification (it parses Maestro's own JSON `success:false` convention; the toolkit rightly treats result content as opaque).
