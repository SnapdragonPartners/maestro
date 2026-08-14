+++
title = "Conformance Slice: The Agent Execution Contract"
edit_date = "2026-08-14"
status = "draft"
summary = "Evidence for ADR 0032 from a real external-process agent driven over the local transport: thirty-three claims proven and fourteen mutations killed for their named reason, across two rounds whose second was more valuable than its first. Round one found that reconciliation destroyed the requirement a blocked result must reference, that re-attach over a stdio transport is restart rather than reconnection, that an invalid terminal result is a protocol violation, and that blocked is the Orchestrator's to record. Round two found that four of those fixes were wrong one level down — reconciliation over-corrected into stranding resource waits, derivable correlations are unsound for a nondeterministic runtime, at-most-once covered only settled retries, and closing admission at gate 3 aborted in-flight work instead of draining it — and that the stub had fabricated a provenance record inside the very coverage gap the report declares. Model calls, concurrency accounting, resume, retention and the data plane remain declared uncovered rather than filled."
type = "spike"
+++

# Conformance Slice: The Agent Execution Contract

Status: **draft** — evidence for [ADR 0032](../../adr/0032-agent-execution-contract.md),
item A4 of the [pre-Phase-3 blocker plan](plan_blockers.md). The executable is
[`spikes/phase_3/executioncontract`](../../../spikes/phase_3/executioncontract/README.md).

The blocker plan grants A4 the **single bounded exception** to its rule that a
pre-entry design item produces an Accepted ADR and nothing else, because this
contract cannot be proven by inspection. It also fixes what the exception has to
buy: *any real external-process executable driven over the local transport*,
exercising the wire boundary, capability handling, the event stream,
cancellation, and the terminal result — and explicitly **not** an in-process
fake or an echo fixture.

## What was run

One Go module outside `pkg/`, `internal/`, and `cmd/`, on the same footing as
the [Docker fencing spike](spike_docker-fencing.md). No Docker, no network, no
API keys, no spend.

- **`reviewagent`** — a real external process speaking newline-delimited JSON
  over stdin and stdout. Twenty-four of the thirty-three claims spawn it.
- **`host`** — the Orchestrator side: [ADR 0030](../../adr/0030-tool-execution-policy-hook.md)'s
  three gates in miniature, an attempt recorder carrying ADR 0032 §6's state
  vocabulary, and process supervision.
- **`mutate`** — defect-shaped verification of the claims themselves.

**The analysis backend is an explicit stub** (DR, 2026-08-13): it minimally
satisfies the contract rather than pretending to do useful review work, and the
real build-out is Phase 3's. It is a *code-review* agent because
[#282](https://github.com/SnapdragonPartners/maestro/issues/282) names one as
the contract's first external consumer, not because the slice reviews anything.

**Result: 33 claims, 33 `PROVEN`, 0 `FALSIFIED`, 0 `ERROR`. 14 of 14 mutations
killed for their named reason.**

## Round one — five findings

### 1. Reconciliation destroyed what `blocked` references

The `gate/headless-blocks` claim failed on the first full run: the execution
*was* recorded `blocked`, and its requirement set was **empty**.

The cause is a rule nobody wrote. ADR 0030 §8's table says the **watchdog**
leaves the two waits alone. The **reconciler** is a different actor, and a first
implementation settled *every attempt not already settled* as `unknown` — which
swept up the attempt sitting healthily in `operator_waiting` and destroyed the
requirement reference the `blocked` terminal result must carry.

### 2. Re-attach over a stdio transport is restart, not reconnection

ADR 0032 §6 was drafted saying the runtime re-announces after "a transport
reconnection." Over the local transport that sentence has no referent: **a broken
stdio transport is a dead process**, so there is no live runtime to reconnect to.
What Phase 3 actually meets is a **restarted** runtime rejoining an existing
execution, and that is what the slice runs.

### 3. An invalid terminal result is a protocol violation, not a result

§5 defined the applicability rule and did not say what happens when a runtime
breaks it. A `completed` result carrying a failure class is now refused at the
boundary and the execution fails `non_retryable_agent`.

### 4. `blocked` is the Orchestrator's to record

In the headless scenario the agent stops and exits **without** a terminal event,
and the host composes `blocked` from the boundary's own state. An agent that
named itself blocked would assert something about a gate it cannot see. This also
makes `terminal` *at most* one event per execution rather than exactly one.

### 5. Correlation identity needed a rule

At-most-once has to survive a restart, which round one addressed by requiring
correlations to be **derivable**. Round two found that wrong; see below.

## Round two — where the round-one fixes were wrong one level down

This round came from review rather than from running the suite, and it was worth
more than round one. **Four of round one's own fixes were incomplete or wrong**,
which is the failure shape this repository keeps paying for: the fix creates the
next defect one level down.

| What round one did | Why it was wrong | What it is now |
| --- | --- | --- |
| Scoped reconciliation to `open` and nothing else | Over-corrected. A `resource_waiting` attempt whose provisioning operation died is stranded forever — leaving it alone is as wrong as settling it | Each state gets its own treatment: `open` settles `unknown`, operator waits are preserved **and validated**, resource waits are preserved **and handed back for restoration**. A wait naming nothing it waits for is a defect, surfaced as one |
| Required correlations to be **derivable** so a restarted runtime could re-announce them | Unsound for a **nondeterministic** runtime: step 3 of the second incarnation need not be the same logical action as step 3 of the first, so a derived correlation can collide with an unrelated attempt | The **Orchestrator enumerates** what is outstanding and the runtime asks. Derivation is one legitimate strategy, not a contract requirement |
| Replayed a settled attempt's result on a duplicate request | Covered only half the case. A duplicate for an attempt still **in flight** fell straight back through policy, operator handling, and resource acquisition — a second pass at one logical action | An in-flight duplicate returns `outstanding` and re-enters no gate |
| Closed admission on cancellation, re-checked at gate 3 | Aborted work already **admitted** rather than draining it. "Revoke, then drain" means in-flight attempts reach their commit point | Admission closure blocks new attempts only; the grace period is what bounds one that will not settle |

Five further findings, none of them round-one regressions:

- **The receipt discipline belongs to the category, not to `cancelled`.** A
  forced timeout wrote a terminal result over an `unconfirmed` fence — the same
  false record, reached by a different route. Every path on which the
  Orchestrator forces a stop now owes a positive receipt.
- **`timed_out` was forced into a failure class it does not have.** Ordinary
  wall-clock exhaustion is neither retryable infrastructure nor a non-retryable
  agent defect, and the same draft said a timeout may be retried with a larger
  budget. The class now applies to `failed` alone.
- **Events had no durable identity.** At-least-once delivery was declared
  idempotent with nothing behind it: the sequence counter restarted at 1 with
  every process, so two messages from two incarnations shared one identity.
  Identity is now `(execution, epoch, sequence)` with the epoch assigned by the
  Orchestrator, and the receiver checks it.
- **The invocation was not one immutable thing.** It carried resource
  generations, which gate 3 replaces, and a resume token, which exists only on a
  restart. It is now an immutable **execution configuration** — persisted, so a
  restarted *Orchestrator* can reissue it — beside per-incarnation **bindings**.
- **A question bypassed the mandatory boundary.** Routing one changes execution
  state and invokes Orchestrator message routing, so under ADR 0022 it needs an
  action record. It is a mediated action, not a raw event.

### The finding worth naming on its own

**The stub emitted a `closed` provenance record with call reference
`no-model-call`.** The report declared per-call provenance uncovered, and the
code then fabricated exactly that coverage — an accounting of a model input that
never existed, asserted as `closed`.

It is removed, and a claim now asserts the *absence*:
`result/no-provenance-event-without-a-model-call`. Recorded rather than quietly
fixed because declaring a gap and then filling it with a hollow record is worse
than either alone: the declaration is what a reader trusts.

## The claims

Thirty-three. Twenty-four spawn a process; nine exercise boundary and schema
properties the wire scenarios depend on but cannot isolate, and are labelled
separately in the code so nothing there is read as evidence about the wire.

| Claim | Evidences |
| --- | --- |
| `handshake/version-agreed` | §11 negotiation |
| `handshake/version-rejected-at-dispatch` | §11 fails before resources; §5 a refused invocation is not a sixth status |
| `result/completed-changed` | §5 axes 1–2; §8 mediated actions |
| `result/completed-already-satisfied` | §5 axis 2 — [#280](https://github.com/SnapdragonPartners/maestro/issues/280) |
| `result/no-provenance-event-without-a-model-call` | §9 — the declared gap stays declared |
| `capability/denial-is-data` | §8 — a denial the agent reads and acts on |
| `capability/protocol-violation-is-fatal` | §8 — the other side of that line |
| `result/invalid-axes-rejected` | §5 applicability enforced on the wire |
| `action/ask-is-mediated` | §4, §8 — a question is an action, not an event |
| `cancel/cooperative` | §6 steps 2–3 and step 5's fence precondition |
| `cancel/admission-closes-before-the-drain` | §6 step 1 — ADR 0029 §7 step 2's ordering, applied to attempts |
| `cancel/uncooperative-is-fenced` | §6 step 4 — revocation does not stop a process |
| `cancel/terminal-withheld-on-unconfirmed-fence` | §6 step 5 |
| `timeout/terminal-withheld-on-unconfirmed-fence` | §6 — the rule belongs to the category |
| `events/claim-overridden-after-cancel` | §4 — the terminal event is a claim, and the claim is retained |
| `events/duplicate-rejected-by-identity` | §4 — at-least-once needs a checked identity |
| `gate/headless-blocks-with-one-durable-outcome` | ADR 0030 §4; §5's `blocked` and its Orchestrator-composed exception |
| `gate/interactive-approval-proceeds` | ADR 0030 §4 gate 2; §6's durable `operator_waiting` transition |
| `wait/transport-stays-live-during-an-operator-wait` | ADR 0030 §4 — the wait is **logical** and must not hold the transport |
| `gate/resource-wait-is-a-distinct-state` | §6 — two waits, different responders, each naming its operation |
| `result/timed-out-carries-no-failure-class` | §5 |
| `record/interrupted-attempt-reconciles-unknown` | ADR 0030 §8's `Interrupted` row |
| `record/duplicate-request-commits-once` | ADR 0030 §3, over the wire |
| `restart/does-not-reissue-a-settled-action` | §6 — re-attach across a restart |
| `schema/applicability-rule-both-directions` | §5 — 5 valid shapes accepted, 10 invalid refused |
| `boundary/blocked-caller-is-an-invariant-violation` | ADR 0030 §4 — not an ordinary denial |
| `boundary/stale-generation-rejected-late` | [ADR 0029](../../adr/0029-incubator-and-habitat-execution-boundaries.md) §7 requirement 5 |
| `boundary/amended-version-rejected-at-admission` | ADR 0019 version-bound dispatch |
| `boundary/settled-retry-replays-its-result` | ADR 0030 §3 |
| `boundary/outstanding-retry-re-enters-no-gate` | ADR 0030 §3 — the half the first version missed |
| `reconcile/preserves-an-operator-wait` | §6 |
| `reconcile/hands-back-a-resource-wait` | §6 — the half the over-correction missed |

**`wait/transport-stays-live-during-an-operator-wait` is the claim that closes
round two's largest gap.** The first implementation ran both gates synchronously
inside the transport's only event loop, so while an action waited the host could
process no cancellation, no heartbeat, and no re-attachment — ADR 0030's detached
logical wait *claimed* rather than demonstrated. The claim now asserts that the
loop sent cancellation while the operator gate was still deciding, by timestamp.

## Defect-shaped verification

`go run ./mutate` restores fourteen defects, one per protected property, and
counts a mutation as evidence only when it falsifies its named claim **for the
named reason**. A compiler failure, an `ERROR`, or a failure at a neighbouring
guard does not count. A positive control proves the suite is green before
anything is mutated.

| Mutation | Protected defect | Result |
| --- | --- | --- |
| `applicability-rule-one-direction-only` | A validator checking only *required axis present* accepts the axis collision | KILLED |
| `timed-out-forced-into-a-failure-class` | Recording a guess as a fact | KILLED |
| `reconciler-sweeps-an-operator-wait` | **Round one's finding**, kept as a regression | KILLED |
| `reconciler-abandons-a-resource-wait` | **Round two's correction to it**, likewise | KILLED |
| `reconciler-settles-nothing` | An interrupted attempt left open forever — v1's shape | KILLED |
| `settled-retry-mints-a-second-attempt` | How an adapted runtime duplicates a forge push | KILLED |
| `outstanding-duplicate-re-enters-the-gates` | A second pass at one logical action | KILLED |
| `headless-leaves-a-phantom-operator-wait` | A wait named for a responder that does not exist | KILLED |
| `terminal-recorded-on-unconfirmed-fence` | A false record over a possibly-live process | KILLED |
| `timeout-is-not-treated-as-a-forced-stop` | The same false record by another route | KILLED |
| `admission-not-closed-on-cancellation` | The drain chasing a set the holder keeps adding to | KILLED |
| `event-identity-not-checked` | At-least-once with nothing behind it | KILLED |
| `stale-generation-not-revalidated` | A late call from a fenced holder | KILLED |
| `capability-set-not-enforced` | The gate an empty policy must not be able to disable | KILLED |

**One mutation initially survived, and the survivor indicted the assertion rather
than the mutation.** `capability/denial-is-data` checked the execution's status
before checking that a denial had been recorded, so disabling the capability gate
tripped the *agent's own* consistency check first and the claim failed with a
different message. The claim now asserts the mechanism — a recorded denial, on
capability grounds — before the consequence. The harness caught it only because
it required the failure to match a named reason rather than merely to occur.

**Residue discipline.** The harness writes `.mutation-in-progress` before
touching anything and refuses to start while one exists, because a killed harness
does not run its restore and the next run would layer a second mutation on a tree
that no longer describes the defect. Restoration is verified by SHA-256 rather
than assumed. Formatting the module after a green run re-invalidates every
whitespace-bearing anchor, so the harness is re-run after `gofmt`.

## What is not covered

Stated because a suite silent about its gaps reads as covering everything. None
of it is fabricated to make a scenario look green — ADR 0025's
`unavailable`-versus-zero discipline, applied to test evidence.

| Surface | Why | Discharged by |
| --- | --- | --- |
| Model calls, `usage` events, token accounting, per-model-call provenance bindings | The stub makes none, and now emits nothing in their place | Phase 3's real build-out |
| Concurrency accounting (§7) | There is no scheduler in the slice, so the claim that a blocked execution consumes no runnable concurrency is **reasoned, not measured** | Phase 3 |
| Resumable runtimes, the resume token, and the retention window | The stub declares `resumable: false` | Phase 3, with an adapted runtime that can resume |
| The provenance retention traversal (§9) | No retention runs here | Phase 4, per ADR 0029's deferred list |
| Composite and paired execution (§9) | One participant only | Phase 5, where heterogeneity is an exit criterion |
| Any transport but the local one | Only stdio is implemented, and it produces no reconnection case at all | Deferred with [candidate 14](../notes_adr-backlog.md) |
| The data plane | The recorder is in-memory | Checked instead by reading the migrations — see below |

**The in-memory recorder is the largest honest gap.** What the slice proves is
the contract and the boundary's state machine, not that Postgres can hold them.
The schema claims in ADR 0032 were checked by reading migrations 000004 and
000005 directly, which is how the `tool_calls_finished_check` finding was made —
not by running anything.

## One thing the slice models rather than reproduces

The interrupted-attempt scenario injects the fault: the boundary opens the
attempt and deliberately returns without settling it. In the real case the
Orchestrator process dies, so **no reply is sent at all** and the agent's own
timeout is what ends its wait. The slice short-circuits that to keep the
scenario bounded, and the assertion it makes — that reconciliation settles the
attempt as `unknown` — is the same either way. Recorded rather than glossed,
because a fault injection that reads as a reproduction overstates what ran.

## Related Documents

- [ADR 0032](../../adr/0032-agent-execution-contract.md) — the contract this is
  evidence for.
- [Pre-Phase-3 blockers](plan_blockers.md) item A4 — the bounded exception and
  what the slice must exercise.
- [ADR 0029](../../adr/0029-incubator-and-habitat-execution-boundaries.md) §7
  (fencing, the late-call rejection, and the revoke-then-drain ordering),
  [ADR 0030](../../adr/0030-tool-execution-policy-hook.md) (the three gates and
  §8's recording rules),
  [ADR 0031](../../adr/0031-prompt-pack-identity-resolution-and-storage.md) §3
  (the provenance obligations).
- [Docker fencing spike](spike_docker-fencing.md) — the three-valued outcome
  discipline and the practice of recording a spike's own defects.
- [`spikes/phase_3/executioncontract`](../../../spikes/phase_3/executioncontract/README.md)
  — the executable.
