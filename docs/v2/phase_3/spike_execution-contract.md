+++
title = "Conformance Slice: The Agent Execution Contract"
edit_date = "2026-08-13"
status = "draft"
summary = "Evidence for ADR 0032 from a real external-process agent driven over the local transport: twenty-two claims proven, seven mutations killed for their named reason, and five findings that changed the ADR — reconciliation must be scoped to open attempts or it destroys the requirement a blocked result must reference, re-attach over a stdio transport is restart rather than reconnection, correlation identities must be derivable rather than merely chosen, an invalid terminal result is a protocol violation rather than a result, and the blocked status is the Orchestrator's to record rather than the agent's to claim; with the model-call, concurrency-accounting, resume and retention surfaces declared uncovered rather than fabricated."
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
  over stdin and stdout. Sixteen of the twenty-two claims spawn it.
- **`host`** — the Orchestrator side: [ADR 0030](../../adr/0030-tool-execution-policy-hook.md)'s
  three gates in miniature, an attempt recorder carrying ADR 0032 §6's state
  vocabulary, and process supervision.
- **`mutate`** — defect-shaped verification of the claims themselves.

**The analysis backend is an explicit stub** (DR, 2026-08-13): it minimally
satisfies the contract rather than pretending to do useful review work, and the
real build-out is Phase 3's. It is a *code-review* agent because
[#282](https://github.com/SnapdragonPartners/maestro/issues/282) names one as
the contract's first external consumer, not because the slice reviews anything.

**Result: 22 claims, 22 `PROVEN`, 0 `FALSIFIED`, 0 `ERROR`. 7 of 7 mutations
killed for their named reason.**

## Findings that changed the ADR

Five, and the first is a defect in the design rather than in the harness.

### 1. Reconciliation must be scoped to `open`, or it destroys what `blocked` references

The `gate/headless-blocks-immediately` claim failed on the first full run: the
execution *was* recorded `blocked`, and its requirement set was **empty**.

The cause is a rule nobody wrote. ADR 0030 §8's table says the **watchdog**
leaves the two waits alone. The **reconciler** is a different actor, and a first
implementation settled *every attempt not already settled* as `unknown` — which
swept up the attempt sitting healthily in `operator_waiting` and destroyed the
requirement reference the `blocked` terminal result must carry.

The scoping is now explicit: reconciliation settles attempts in `open` and no
others. An attempt in a **declared** wait is healthy, not interrupted, which is
the entire distinction the nonterminal states were introduced to draw — and the
first implementation collapsed it one level below where ADR 0030 drew it.

This is now mutation `reconciler-sweeps-declared-waits`, so the defect stays
protected rather than being quietly repaired.

### 2. Re-attach over a stdio transport is restart, not reconnection

ADR 0032 §6 was drafted saying the runtime re-announces its outstanding
correlations "after a transport reconnection." Over the local transport that
sentence has no referent: **a broken stdio transport is a dead process**, so
there is no live runtime to reconnect to.

The case that actually matters in Phase 3 is a **restarted** runtime rejoining
an existing execution, and that is what the slice runs
(`restart/does-not-reissue-a-settled-action`): one process settles its actions,
a second process for the same invocation re-announces the correlation, learns it
is settled, and does not reissue it. One effect, across two processes.

Reconnection semantics remain in the contract because a future socket or remote
transport needs them. What changed is that the ADR no longer implies the local
transport exercises them.

### 3. Correlation identities must be derivable, not merely "chosen by the runtime"

Finding 2's claim is only reachable because the agent derives its correlations
from the invocation identity and a step ordinal. **A restarted runtime cannot
re-announce correlations it invented and lost**, so "an identity it chooses" is
too weak: the rule is that the choice must survive the runtime's own death.

Without it, at-most-once holds within a process and silently stops holding
across a restart — which is exactly the boundary a restart crosses.

### 4. An invalid terminal result is a protocol violation, not a result

§5 defined the applicability rule and did not say what happens when a runtime
sends a result that breaks it. The slice forced the answer: a `completed` result
carrying a failure class is refused at the boundary and the execution fails
`non_retryable_agent` (`result/invalid-axes-rejected`). Recording it would put
the exact axis collision the four-axis schema exists to prevent into the plane,
with the schema's own validator having seen it and shrugged.

### 5. `blocked` is the Orchestrator's to record, not the agent's to claim

In the headless scenario the agent stops and exits **without** a terminal event,
and the host synthesizes `blocked` from the boundary's own state. That is the
right split and the ADR now says so: the Story's state is not the agent's to
declare, and an agent that named itself blocked would be asserting something
about a gate it cannot see.

## The claims

| Claim | Evidences |
| --- | --- |
| `handshake/version-agreed` | §11 negotiation |
| `handshake/version-rejected-at-dispatch` | §11 fails before resources; §5 a refused invocation is not a sixth status |
| `result/completed-changed` | §5 axes 1–2; §8 mediated actions |
| `result/completed-already-satisfied` | §5 axis 2 — [#280](https://github.com/SnapdragonPartners/maestro/issues/280) |
| `capability/denial-is-data` | §8 — a denial the agent reads and acts on |
| `capability/protocol-violation-is-fatal` | §8 — the other side of that line |
| `result/invalid-axes-rejected` | §5 applicability enforced on the wire (finding 4) |
| `cancel/cooperative` | §6 steps 1–2 and step 4's fence precondition |
| `cancel/uncooperative-is-fenced` | §6 step 3 — revocation does not stop a process |
| `cancel/terminal-withheld-on-unconfirmed-fence` | §6 step 4 — a result over an unfenced process is a false record |
| `events/claim-overridden-after-cancel` | §4 — the terminal event is a claim; the claim is retained |
| `gate/headless-blocks-immediately` | ADR 0030 §4; §5's `blocked` (findings 1 and 5) |
| `gate/interactive-approval-proceeds` | ADR 0030 §4 gate 2; §6's durable `operator_waiting` transition |
| `gate/resource-wait-is-a-distinct-state` | §6 — two waits, different responders |
| `result/timed-out` | §5 — a deadline is Orchestrator-observed |
| `record/interrupted-attempt-reconciles-unknown` | ADR 0030 §8's `Interrupted` row |
| `schema/applicability-rule-both-directions` | §5 — 5 valid shapes accepted, 10 invalid refused |
| `boundary/blocked-caller-is-an-invariant-violation` | ADR 0030 §4 — not an ordinary denial |
| `boundary/stale-generation-rejected-late` | [ADR 0029](../../adr/0029-incubator-and-habitat-execution-boundaries.md) §7 requirement 5 |
| `boundary/amended-version-rejected-at-admission` | ADR 0019 version-bound dispatch |
| `boundary/at-most-once-per-correlation` | ADR 0030 §3 — a transport retry is not a new action |
| `restart/does-not-reissue-a-settled-action` | §6 (findings 2 and 3) |

Six of these do not spawn a process, and are labelled separately in the code so
nothing there is read as evidence about the wire.

## Defect-shaped verification

`go run ./mutate` restores seven defects, one per protected property, and counts
a mutation as evidence only when it falsifies its named claim **for the named
reason**. A compiler failure, an `ERROR`, or a failure at a neighbouring guard
does not count. A positive control proves the suite is green before anything is
mutated.

| Mutation | Protected defect | Result |
| --- | --- | --- |
| `applicability-rule-one-direction-only` | A validator checking only *required axis present* accepts the axis collision | KILLED |
| `reconciler-sweeps-declared-waits` | **Finding 1**, kept as a regression | KILLED |
| `reconciler-settles-nothing` | An interrupted attempt left open forever — v1's shape | KILLED |
| `at-most-once-mints-a-second-attempt` | How an adapted runtime duplicates a forge push | KILLED |
| `terminal-recorded-on-unconfirmed-fence` | A false record written over a possibly-live process | KILLED |
| `stale-generation-not-revalidated` | A late call from a fenced holder | KILLED |
| `capability-set-not-enforced` | The gate an empty policy must not be able to disable | KILLED |

**One mutation initially survived, and the survivor indicted the assertion
rather than the mutation.** `capability/denial-is-data` checked the execution's
status before checking that a denial had been recorded, so disabling the
capability gate tripped the *agent's own* consistency check first and the claim
failed with a different message. The claim now asserts the mechanism — a
recorded denial, on capability grounds — before the consequence. This is the
repository's recorded lesson arriving on schedule, and the harness caught it
only because it required the failure to match a named reason rather than merely
to occur.

**Residue discipline.** The harness writes `.mutation-in-progress` before
touching anything and refuses to start while one exists, because a killed
harness does not run its restore and the next run would layer a second mutation
on a tree that no longer describes the defect. Restoration is verified by
SHA-256 rather than assumed. Formatting the module after the first successful
run re-invalidated every whitespace-bearing mutation anchor, so the harness was
re-run after `gofmt` and all seven were confirmed killed again.

## What is not covered

Stated because a suite silent about its gaps reads as covering everything. None
of it is fabricated to make a scenario look green — ADR 0025's
`unavailable`-versus-zero discipline, applied to test evidence.

| Surface | Why | Discharged by |
| --- | --- | --- |
| Model calls, `usage` events, token accounting, per-model-call provenance bindings | The stub makes none | Phase 3's real build-out |
| Concurrency accounting (§7) | There is no scheduler in the slice, so the claim that a blocked execution consumes no runnable concurrency is **reasoned, not measured** | Phase 3 |
| Resumable runtimes and the resume token | The stub declares `resumable: false` | Phase 3, with an adapted runtime that can resume |
| The provenance retention traversal (§9) | No retention runs here | Phase 4, per ADR 0029's deferred list |
| Composite and paired execution (§9) | One participant only | Phase 5, where heterogeneity is an exit criterion |
| Any transport but the local one | Only stdio is implemented | Deferred with [candidate 14](../notes_adr-backlog.md) |
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
  (fencing and late-call rejection),
  [ADR 0030](../../adr/0030-tool-execution-policy-hook.md) (the three gates and
  §8's recording rules),
  [ADR 0031](../../adr/0031-prompt-pack-identity-resolution-and-storage.md) §3
  (the provenance obligations).
- [Docker fencing spike](spike_docker-fencing.md) — the three-valued outcome
  discipline and the practice of recording a spike's own defects.
- [`spikes/phase_3/executioncontract`](../../../spikes/phase_3/executioncontract/README.md)
  — the executable.
