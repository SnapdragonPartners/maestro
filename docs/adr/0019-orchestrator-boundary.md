+++
title = "ADR 0019: Orchestrator Boundary"
edit_date = "2026-08-15"
status = "live"
summary = "Defines the v2 Orchestrator as the programmatic, non-agentic layer owning agent lifecycle, tools, routing, forge, persistence, and scheduling — with the no-inference rule as the boundary test. A proposed second amendment settles the case the first one deferred — what happens to work already executing when the record it was dispatched against changes: it is cancelled rather than suspended or completed-then-reconciled, and the cancellation is ordered so that nothing is recorded as finished until the resource it held is proven unable to interfere. What already happened is left intact, because an amendment changes what the work must satisfy rather than rewriting what was done."
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

1. The execution's **bound effective work version is no longer current.** Stated
   over the *effective* version rather than over supersession, because
   [ADR 0021](0021-artifacts-and-principal-instances.md) is explicit that an
   accepted amendment does **not** supersede the original — it changes the
   effective view and leaves the original's status alone. A test written on
   `superseded` would miss every amendment, which is the common case and the one
   this decision is named for.
2. **DAG re-evaluation leaves its work no longer dependency-ready** — a
   predecessor was inserted that is not satisfied, or a formerly satisfied
   predecessor's own effective version changed so that it no longer is. Removing
   an edge is not such a case: fewer predecessors can only make work *more*
   ready.

Both are rules over records. Neither requires inference, so both are the
Orchestrator's.

**An amendment that satisfies neither test does not cancel anything.** This needs
saying because the obvious implementation is wrong: if a dispatch record bound to
a whole-graph version, every edit anywhere would cancel every running execution,
and a long Epic could never be edited while work was in progress. The binding is
**per work item**. Editing one Story does not disturb its siblings; editing the
graph disturbs only executions the second test catches.

**Two predicates, one enforcement — and conflating the two was this draft's first
error.** The tests differ in what they detect: test 1 compares versions, test 2
re-evaluates the graph. They must not differ in how they stop the work, and a
version comparison cannot be that mechanism, because under test 2 the execution's
own work version is still current — what changed is the graph around it.
Enforcement is therefore the same in both cases: supersede the **execution's own
authority** and close admission. That is a fact about the execution rather than
about the work version, and it is what the boundary already checks.

#### The sequence, and why the order is the decision

1. **Mark the execution's authority superseded, and close admission.** Every
   further mediated action is rejected, on ADR 0032's rule that a request
   carrying superseded or fenced execution authority is refused at every mediated
   boundary. ADR 0030 §5's own ordering applies — close admission for the
   generation *first*, then settle the attempts already registered against it.
2. **Drain the admitted attempts, which is not "let each one reach its commit
   point."** ADR 0030 §5 permits a positive receipt only when **every** admitted,
   unsettled attempt holds one of three dispositions: confirmed **stopped short**
   of its commit point; **conditionally committed** under a generation predicate,
   available only where the effect site accepts one (a data-plane write does; a
   forge push, a container start, and an external call do not); or **confirmed
   passage into the fenced domain**. An attempt that has *already* passed its
   commit point needs none of the three — but **passing the commit point is not
   settlement.** It means only that the effect is no longer the Orchestrator's to
   withhold; the outcome may still be unknown and the record still open. **Two facts have to be
   recorded, and collapsing them loses one:** that the effect passed its commit
   point, and what the attempt actually came to. The attempt is completed with
   its real outcome — **succeeded or failed** where that is known, **`unknown`**
   where it is not — under ADR 0030 §8's record contract. "Committed" is not an
   outcome; it is the reason the outcome was no longer the Orchestrator's to
   decide. Without the completion, an execution can reach a terminal result with
   an action still open, which is the state the fence receipt exists to
   exclude. (`unknown` is also why the Phase 3
   migration must replace `tool_calls_finished_check` rather than only add to it,
   as ADR 0030 §8 records.) So an action admitted before the amendment can still
   land after it — correct, and attributable rather than hidden. What the drain
   does is establish dispositions and settle records, not force outcomes.
   **An attempt waiting on an operator is stopped stale inside this step**, on the
   ground that its execution authority was superseded — not on version
   invalidation, which under test 2 does not fire at all, since the Story's
   effective version is still current. Where test 1 *did* fire, ADR 0030's
   unconditional invalidation of prior-version decisions applies as well, but the
   authority is what stops the wait in both cases. Deferring this to a later step
   would leave the drain waiting on a human, which is the one wait with no bound.
   **A grant bound to the cancelled logical action dies with it.** For a grant
   scoped more widely, ADR 0032 demoting its own approval machinery does not
   demote ADR 0030's independent rule, and this amendment does not change it: a
   **Story-scoped grant binds to the effective Story version**, so test 1
   invalidates it along with every other prior-version decision, and under test 2
   — where that version is unchanged and the same work will be re-dispatched once
   its predecessor lands — **it survives**. Re-asking a human for an identical
   approval because the graph was reordered around them would be gratuitous. This
   is stated rather than deferred because it is policy, and leaving it to
   implementation would let the two triggers diverge silently.
3. **Revoke authorization immediately; hold capacity until the fence proves
   non-interference.** The lease only authorizes, and the execution is no longer
   authorized, so revoking it starts cancellation. The **retention claim is not
   released yet**: releasing it makes the instance eligible for demand-driven
   reclamation or reassignment while an unfenced process may still be writing,
   which hands a successor a resource nothing has proven free.
4. **Fence every domain the execution held**, per ADR 0029 §7. `terminated` and
   `isolated` are both positive receipts. `unconfirmed` is neither: that domain
   stays **quarantined**, and its provider record stays visible, reconciled and
   flagged as potentially billable under ADR 0029's independent cleanup axis.
   **Cancellation fences a generation; it does not destroy an instance**, which
   outlives the executions authorized against it.
5. **Once every domain has returned a positive receipt, release the cancelled
   execution's retention and ownership claims, and record the terminal result** —
   in that order, or atomically. Step 3 held those claims precisely until this
   point. **Releasing a claim is not deallocation, and a positive fence does not
   prove one**: an `isolated` resource may still be running and still billing,
   and even a `terminated` one may have cleanup outstanding. The provider record
   and its cleanup obligation stay governed by ADR 0029's independent cleanup
   axis until deallocation is confirmed, so what is released here is this
   execution's hold — not a guarantee that anything was freed. The result is
   `cancelled`, reason `superseded`. Never `failed` — the execution did nothing
   wrong, and a failure class here would feed retry policy a false signal.
   **An `unconfirmed` domain leaves the cancellation unresolved**: non-terminal,
   no result recorded, the claims and quarantine still held, and nothing
   dispatched. A terminal result recorded while an unfenced process may still be
   writing is a false record, and downstream work dispatched against a resource
   that is not actually free inherits the lie.
6. **Re-evaluate, then dispatch only if the current effective work is
   dependency-ready.** Test 2 exists precisely because it may not be: work
   cancelled for a newly inserted predecessor waits for that predecessor rather
   than being reissued immediately. A fresh execution is assigned an appropriate
   **new generation**, is never dispatched into a quarantined domain, and — since
   the released claim proves no deallocation — waits on **genuinely available
   capacity** under ADR 0029 §2 rather than on the release itself.

#### What is kept, and what changes

**An amendment changes what the work must satisfy. It does not rewrite what
already happened.** The three artifact populations follow from that, and from
ADR 0021 rather than from anything decided here:

- **Audit artifacts are born final** and stay. Cancellation adds to the record and
  removes nothing from it.
- **Draft Management output stays draft** and non-authoritative. It is simply not
  accepted against a version that is no longer current.
- **Management artifacts already Accepted stay Accepted.** ADR 0021's status
  vocabulary is `draft` → (`invalidated` | `accepted`) → (`superseded` |
  `archived`) — there is no path back to draft, and immutable accepted history is
  the point of it. What changes is that their result **no longer satisfies the
  current effective work version**; correcting that is ADR 0021's
  amendment-and-supersession lifecycle, not this one's.

**The already-terminal case, which the original deferral did not consider.** An
execution that completed before the amendment landed **remains historically
completed**. There is nothing to cancel and nothing to fence, and its record is
not reopened. The consequence is a separate statement, not a status change: its
result no longer satisfies the current effective work version, so that version
still needs work. This is the common case under a fast amendment, and an earlier
draft got it wrong in the expensive direction — by saying the output reverts to
draft, which would both misdescribe what happened and use a transition ADR 0021
does not have.

**Cancellation is idempotent.** A second amendment arriving mid-cancellation
does not restart it; the execution's authority is already superseded and the
drain already running. What it changes is only which effective version step 6
evaluates when it gets there.

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
| [Pre-Phase-3 blockers](../v2/phase_3/plan_blockers.md) item A5 | Add the RESOLVED banner in the form A1–A4 use, recording what was settled differently from what the item asked. Three things are: the trigger is stated over the **effective work version** rather than supersession, since an accepted amendment does not supersede anything; it has a **second test** the item never stated (DAG re-evaluation leaving work not dependency-ready), enforced through the execution's **authority** rather than a version comparison; and the **already-terminal case** is a fourth outcome the item did not consider, in which nothing is cancelled and nothing is demoted |
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
- **The record of what happened is never rewritten to match what is now wanted.**
  A cancelled execution keeps its Audit, a completed one keeps its completion, and
  an accepted artifact keeps its acceptance. What an amendment changes is whether
  a result still satisfies the current effective work version — a separate
  statement, reconciled through ADR 0021's own amendment and supersession
  lifecycle rather than by demoting anything.
- **A cancellation can fail to complete, and that is a state rather than an
  error.** An `unconfirmed` fence leaves the execution non-terminal, its claims
  on the resource still held, and nothing dispatched. The alternative — recording a
  terminal result anyway — buys a tidy record by handing a successor a resource
  nothing has proven free.

## Related Documents

- [ADR 0018](0018-v2-work-taxonomy.md) (Work Group lifecycle ownership, dispatch contracts), [ADR 0017](0017-v2-documentation-authority-and-lifecycle.md).
- For the second amendment: [ADR 0029](0029-incubator-and-habitat-execution-boundaries.md) §2 and §7 (leases, retention claims, the fencing protocol and its three-valued receipt), [ADR 0030](0030-tool-execution-policy-hook.md) (the admission stage that refuses a superseded version, and the rule that an amendment invalidates prior-version decisions), [ADR 0032](0032-agent-execution-contract.md) (the drain-and-fence precondition on a positive terminal result, and the `cancelled`/`superseded` axis), [ADR 0021](0021-artifacts-and-principal-instances.md) (the artifact status vocabulary, which has no path back to draft, and what an accepted amendment does *not* supersede), [ADR 0020](0020-review-invariant-reviewer-vs-partner.md) (who authors and reviews the amendment itself).
- [Pre-Phase-3 blocker plan](../v2/phase_3/plan_blockers.md) item A5; [ADR backlog](../v2/notes_adr-backlog.md) candidate 3.
- [Roadmap](../v2/plan_roadmap.md) Core Vocabulary (Orchestrator), D2, pillar 15; [ADR backlog](../v2/notes_adr-backlog.md) Orchestrator Boundary entry.
- Historical notes [0002](0002-local-single-user-runtime-kernel.md) (superseded for v2 by this ADR) and [0004](0004-channel-dispatch-and-typed-agent-protocol.md) (discipline carried forward).
