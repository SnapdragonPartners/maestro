+++
title = "ADR 0019: Orchestrator Boundary"
edit_date = "2026-08-15"
status = "live"
summary = "Defines the v2 Orchestrator as the programmatic, non-agentic layer owning agent lifecycle, tools, routing, forge, persistence, and scheduling — with the no-inference rule as the boundary test. A proposed second amendment settles what happens to work already executing when its record is amended: cancel rather than suspend or complete-then-reconcile, triggered only when the execution's own work version is superseded or DAG re-evaluation leaves it not dependency-ready, and sequenced so admission closes, admitted actions drain to their commit point, the resource is fenced, and only then is the result recorded as cancelled/superseded — with output retained as attributable draft and Audit history, because an amendment revokes acceptance rather than the work."
+++

# 0019. Orchestrator Boundary

Status: Accepted (Codex + DR, 2026-07-13); amended 2026-07-14 (work dispatch is Orchestrator machinery at both Epic and Story grain; dispatcher lineage corrected to rework); second amendment **PROPOSED 2026-08-15**, pending Codex and DR approval (amendment versus running work — item A5)

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

**Work dispatch is Orchestrator machinery at every grain** (amended 2026-07-14, resolving a v1 inheritance the port inventory surfaced). Principals author the work graph: humans or triage agents author Feature and Epic framing (ADRs 0021, 0024), and the Architect authors the Story decomposition and its dependency graph — inference and judgment, reviewed under ADR 0020 whoever the author is. Dispatching dependency-ready work to available executors is rules, not judgment, and belongs to the Orchestrator — at Story grain exactly as at the Epic grain of ADR 0024. v1 locating Story dispatch in the Architect was an accident of who held the queue, not a design decision; assignment policy (round-robin, affinity) is configuration by the boundary rule. The **durable backlog is the authoritative scheduler state**; transport — typed channels today, possibly RPC if agents ever split into separate runtimes — is delivery plumbing, never state. When Epics, Stories, or the DAG are amended or superseded, the Orchestrator invalidates the pending version-bound dispatch records, re-evaluates the DAG deterministically, and issues fresh version-bound dispatches — no agent in the loop, and never by draining in-flight channels, which races consumers and would not survive a queue-backend change. The policy for work already *executing* when its record is amended is settled by the amendment below; its runtime remains Phase 3 design.

### Amendment versus running work

**PROPOSED 2026-08-15 — pending Codex and DR approval.** Item A5 of the accepted
[pre-Phase-3 blocker plan](../v2/phase_3/plan_blockers.md), resolving
[backlog candidate 3](../v2/notes_adr-backlog.md). Attribution is restored and
this qualifier removed in the final reviewed commit.

The dispatch amendment above settled the **pending** case — invalidate
version-bound dispatch records, re-evaluate the DAG deterministically, reissue —
and left the **executing** case open as one of cancel, suspend, or
complete-then-reconcile. It is an ADR 0019 question because it is about what the
Orchestrator may decide without an agent, and it lands here as an amendment
rather than a new ADR because the mechanisms it stands on are owned elsewhere:
fencing by [ADR 0029](0029-incubator-and-habitat-execution-boundaries.md), the
mediated boundary by [ADR 0030](0030-tool-execution-policy-hook.md), and the
terminal result by [ADR 0032](0032-agent-execution-contract.md). What is left is
the policy, and the policy is this ADR's business.

**The rule is cancel.**

#### What triggers it, and what does not

Two deterministic tests, either sufficient:

1. The execution's own **work version is superseded**.
2. **DAG re-evaluation leaves its work no longer dependency-ready** — a new
   predecessor was inserted, or an edge it depended on was removed.

Both are rules over records. Neither requires inference, so both are the
Orchestrator's.

**An amendment that satisfies neither test does not cancel anything.** This needs
saying because the obvious implementation is wrong: if a dispatch record bound to
a whole-graph version, every edit anywhere would cancel every running execution,
and a long Epic could never be edited while work was in progress. The binding is
**per work item**. Editing one Story does not disturb its siblings; editing the
graph disturbs only executions the second test catches.

#### The sequence, and why the order is the decision

1. **Close admission for that execution.** Further mediated actions are rejected.
   This is not new machinery: ADR 0030's admission stage already refuses a call
   whose work version has been superseded. The amendment reaches in-flight work
   by revoking the version, not by inventing a second check.
2. **Let already-admitted actions drain to their commit point.** Not abort —
   ADR 0032 requires that admitted actions drain and the resource be fenced
   before any positive terminal result. **A consequence worth stating plainly:
   an action admitted before the amendment may commit after it.** A forge push
   already past its commit point lands. That is the correct behaviour, it is
   bounded by the grace period, and it must be attributable rather than hidden,
   because pretending the amendment stopped it is how a record starts lying.
3. **Invalidate any pending operator decision.** ADR 0030 already holds that an
   amendment invalidates every prior-version decision unconditionally, so a gate
   waiting on a human terminates **stale** rather than being answered. The
   operator is not asked to approve work against a version that no longer exists.
4. **Release the lease and the retention claim, then fence.** ADR 0029 §7's
   protocol against the domain the execution was authorized in. `terminated` and
   `isolated` both satisfy this; only `unconfirmed` blocks, quarantining the
   resource. **Cancellation does not destroy the environment** — a Habitat
   instance outlives the executions authorized against it, and what is fenced is
   the generation, not the instance.
5. **Only then record the terminal result**: `cancelled`, with reason
   `superseded`. Never `failed` — the execution did nothing wrong, and a failure
   class here would feed retry policy with a false signal. A terminal result
   recorded while an unfenced process may still be writing is a false record, and
   downstream work dispatched against a resource that is not actually free
   inherits the lie.
6. **Re-evaluate the DAG and dispatch the new effective version** — into a **new
   generation** wherever the receipt was `isolated` or `unconfirmed`, per
   ADR 0029, never into the quarantined one.

#### What is kept, and what is revoked

Output already produced is **retained as attributable draft and Audit history**.
The amendment revokes **acceptance**, not the work: nothing produced against a
superseded version is ever accepted, and nothing is deleted to make that true.

**The already-terminal case, which the original deferral did not consider.** An
execution that completed before the amendment landed has nothing to cancel and
nothing to fence — but its output was produced against a version that no longer
exists, so it is not accepted either. It is retained exactly as the cancelled
case's output is. This is the common case under a fast amendment, and treating it
as a third outcome rather than the same one would put an accepted artifact behind
a superseded record.

**Cancellation is idempotent.** A second amendment arriving mid-cancellation
changes only which version is dispatched afterwards.

#### Rejected alternatives

**Suspend and resume** — rejected as machinery this phase does not have. It would
require restart, resume, and re-attach semantics, and ADR 0032 deliberately left
those as Phase 3 design inputs rather than settling them. Choosing suspend here
would force that decision now, on this question's schedule, with no consumer to
answer to.

**Complete-then-reconcile** — rejected on cost. It lets stale work keep
progressing against a superseded version, spending model turns and resource time
on output that cannot be accepted. Recorded explicitly: it is **not** rejected on
this ADR's boundary rule. Reconciliation judgment could legitimately be routed to
an agent, so the boundary is not what rules it out, and citing it would be the
easy wrong reason.

#### What this does not decide

The runtime, which stays where the original deferral put it: where the canceller
lives, the grace period's length, how the drain is implemented, and how the
watchdog distinguishes a healthy cancellation from a stuck one. The policy is
settled here; Phase 3 builds it.

**Boundary check**, since this is ADR 0019's own subject: every step above is a
rule over records or a protocol with a defined receipt. None requires inference.
The only judgment nearby — whether the work *should* be amended — belongs to
whoever authors the amended record, reviewed under
[ADR 0020](0020-review-invariant-reviewer-vs-partner.md).

#### Authority reconciliation on acceptance

Established by grepping the **concept**, not the phrase. **None of it is executed
while this amendment is Proposed** — a live document asserting a decision nobody
has accepted is the gap an early sweep opens. All of it lands in the final
reviewed commit, together with the attribution flip.

| Location | Change |
| --- | --- |
| [ADR backlog](../v2/notes_adr-backlog.md) candidate 3 | Mark **RESOLVED**, pointing here. The slot keeps its number, per its own citation rule. Its framing is accurate and needs no correction — it named the three options and this amendment picks one |
| [Pre-Phase-3 blockers](../v2/phase_3/plan_blockers.md) item A5 | Add the RESOLVED banner in the form A1–A4 use, recording what was settled differently from what the item asked. Two things are: the trigger has a **second test** the item never stated (DAG re-evaluation leaving work not dependency-ready), and the **already-terminal case** is a fourth outcome the item did not consider |
| [Pre-Phase-3 blockers](../v2/phase_3/plan_blockers.md) Track A graph and A6 | A5's dependency on A1 and A4 is discharged; A6 is unblocked and becomes the only open Track A item |
| [ADR 0031](0031-prompt-pack-identity-resolution-and-storage.md) §-on-levers | It says "A5 governs work already executing" — a forward reference to an item, which becomes a citation of this amendment |
| [ADR README](README.md) | The 0019 row quotes the front-matter summary verbatim; both change together, and the word *proposed* comes out of both |

**Deliberately not changed**, recorded so nobody "reconciles" it: the Phase 2
[exit record](../v2/phase_2/notes_exit-record.md) states that candidate 3 was
open and unresolved *at Phase 2's close*. That was true when written and stays
true; an exit record is a statement about a moment.

### v1 lineage

The Orchestrator is the evolution of v1's runtime kernel, supervisor, and dispatcher — all classified **rework** in the port inventory (Phase 0 item 10, correcting D8's first-pass "port largely as-is"): the typed-channel discipline carries forward, but the package structures are re-cut — v1's Story/hotfix queues, spec exceptions, and Architect-held Story dispatch do not survive (dispatch moves here, per the amendment above). This ADR supersedes the single-user framing of historical note [0002](0002-local-single-user-runtime-kernel.md) for v2 design intent; the channel-dispatch discipline of [0004](0004-channel-dispatch-and-typed-agent-protocol.md) carries forward. The v3 trajectory (orchestration plane: supervisor and dispatcher for agents running in external environments) is sketched in roadmap pillar 15 and deliberately not designed here.

## Consequences

- Every future workflow design gets a mechanical litmus test; "add a small LLM call to the dispatcher" is a category error by definition, not a judgment call.
- Orchestrator code is testable deterministically with ordinary unit and integration tests; golden stories measure the agents and the harness around them. The reliability budget concentrates where reliability is cheap.
- Agent sprawl has a counterweight: a step is an agent only when it needs inference, and infrastructure never silently becomes one.
- Cloud/queue execution (v3) changes where agents run, not what the Orchestrator is.
- **An amendment is cheap to author and not free to land.** Cancelling running
  work discards model turns already spent, and the drain window means an action
  admitted a moment earlier still commits. Both are visible rather than absorbed:
  the spend is recorded against a `cancelled`/`superseded` execution, and the
  committed action is attributable to the version it was admitted under.
- **Editing a graph while it executes is safe, and editing an executing item is
  not meant to be.** Per-item version binding is what separates the two; a
  whole-graph version would have made every edit a mass cancellation and pushed
  operators toward not amending at all.
- **Nothing accepted ever sits behind a superseded record**, including work that
  completed before the amendment arrived. That case costs the most — finished
  output that will not be accepted — and it is the reason acceptance rather than
  execution is what the amendment revokes.

## Related Documents

- [ADR 0018](0018-v2-work-taxonomy.md) (Work Group lifecycle ownership, dispatch contracts), [ADR 0017](0017-v2-documentation-authority-and-lifecycle.md).
- For the second amendment: [ADR 0029](0029-incubator-and-habitat-execution-boundaries.md) §2 and §7 (leases, retention claims, the fencing protocol and its three-valued receipt), [ADR 0030](0030-tool-execution-policy-hook.md) (the admission stage that refuses a superseded version, and the rule that an amendment invalidates prior-version decisions), [ADR 0032](0032-agent-execution-contract.md) (the drain-and-fence precondition on a positive terminal result, and the `cancelled`/`superseded` axis), [ADR 0021](0021-artifacts-and-principal-instances.md) (what retaining draft and Audit history means), [ADR 0020](0020-review-invariant-reviewer-vs-partner.md) (who authors and reviews the amendment itself).
- [Pre-Phase-3 blocker plan](../v2/phase_3/plan_blockers.md) item A5; [ADR backlog](../v2/notes_adr-backlog.md) candidate 3.
- [Roadmap](../v2/plan_roadmap.md) Core Vocabulary (Orchestrator), D2, pillar 15; [ADR backlog](../v2/notes_adr-backlog.md) Orchestrator Boundary entry.
- Historical notes [0002](0002-local-single-user-runtime-kernel.md) (superseded for v2 by this ADR) and [0004](0004-channel-dispatch-and-typed-agent-protocol.md) (discipline carried forward).
