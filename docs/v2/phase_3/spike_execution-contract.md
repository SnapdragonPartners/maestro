+++
title = "Conformance Slice: The Agent Execution Contract"
edit_date = "2026-08-15"
status = "draft"
summary = "Evidence for ADR 0032: a real external-process agent driven over the local transport, with fifty-nine claims proven and forty-four mutations killed for their named reason, every run under the race detector. Five review rounds, each mostly finding the previous round's fixes wrong one level down — and the later ones finding guarantees the ADR stated with no machinery behind them. The mutation harness was twice wrong itself: it read green from output text alone, and it counted a selector matching no claims as a pass."
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
  over stdin and stdout. Thirty-nine of the fifty-nine claims spawn it.
- **`host`** — the Orchestrator side: [ADR 0030](../../adr/0030-tool-execution-policy-hook.md)'s
  three gates in miniature, an attempt recorder carrying ADR 0032 §6's state
  vocabulary, and process supervision.
- **`mutate`** — defect-shaped verification of the claims themselves.

**The analysis backend is an explicit stub** (DR, 2026-08-13): it minimally
satisfies the contract rather than pretending to do useful review work, and the
real build-out is Phase 3's. It is a *code-review* agent because
[#282](https://github.com/SnapdragonPartners/maestro/issues/282) names one as
the contract's first external consumer, not because the slice reviews anything.

**Result: 59 claims, 59 `PROVEN`, 0 `FALSIFIED`, 0 `ERROR`. 44 of 44 mutations
killed for their named reason, every run under `-race`, agent subprocess
included.** The suite runs in about
nine seconds; the mutation harness in about a minute.

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
| Scoped reconciliation to `open` and nothing else | Over-corrected. A `resource_waiting` attempt whose provisioning operation died is stranded forever — leaving it alone is as wrong as settling it | Each state gets its own treatment: `open` settles `unknown`, and a declared wait settles **`stale`** with its requirement or operation preserved. *(Round four narrowed this further: see below — restoration was itself the wrong answer.)* |
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
  Identity is now `(execution, epoch, stream, sequence)` — the stream because
  reliable and best-effort events cannot share a contiguous space, and derived
  from the message type rather than trusted as supplied — and the receiver
  checks what was **committed**, not merely the watermark.
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

## Round three — the guarantees that had no machinery

Also from review. Where round two found fixes wrong one level down, round three
mostly found **claims asserted without anything implementing them** — which is a
worse failure, because a stated guarantee reads as a delivered one.

| Asserted | What was actually there | What it is now |
| --- | --- | --- |
| Cancellation fences the execution | The resource domain was fenced and admitted **actions** were never settled. An in-flight data-plane write or forge push lands outside every resource domain, so the domain receipt said nothing about it. Admission closure was also racy with registration, so the drain closed over a set that could still grow | ADR 0030 §5's drain runs **before** the domain fence, closure **linearizes** with registration, and an undrained attempt yields no positive receipt |
| Delivery is at-least-once | An identity, and nothing else: no acknowledgement, no sender retention obligation, and deduplication state in memory — reset by exactly the restart it existed to survive | An acknowledged **watermark** per epoch, committed after the effect, with durable receiver state and an explicit sender replay obligation |
| Protocol violations are fatal | Version, execution identity, epoch, unknown message types and malformed bodies were all silently accepted or ignored | The fatal list is **enumerated** and enforced before anything acts on the message |
| A correlation is at-most-once | It was keyed on itself alone, so one key could replay the result of a **different action**, or of the same action with different arguments | Bound to the action identity and the substituted-input digest; a mismatched reuse is refused |
| A preserved operator wait is recovered | The row survived and the in-memory continuation did not. The conformance case kept its goroutine alive, so it demonstrated only that the row survived | *(Superseded in round four: persisting the substituted request violated ADR 0030 §3's redaction rule and promised more than §6 offers. A wait now goes **`stale`**; only the operator decision is persisted.)* |
| A headless block stops the execution | It was composed only when the adapter closed its own stream, so a non-cooperative runtime kept working under a terminal Story | An Orchestrator-driven stop on the same cancel → drain → fence path as any other forced stop |

Two more: **`message.ask` had no response lifecycle** — it now routes and
returns a delivery acknowledgement, with the answer arriving as an artifact
reference because ADR 0021 makes artifacts the sole agent handoff, and the
waiting is an execution state with another principal as its responder; and the
**durability table** in §9, since several new families had no stated home.

### The harness was wrong, and fixing it found a real defect

`runSuite` discarded the subprocess error and read green from the output text
alone, so a suite that printed its summary and then hung would have passed both
the positive control and every mutation check.

This was verified **by construction** rather than argued: a suite was made to
print its green summary and then sleep past the timeout. The old form called it
`green`; the new form, which also requires a clean exit, failed the positive
control. That is the discrimination, and the earlier natural case — a 5m14s
suite against a 5m cap — does *not* demonstrate it, because there the summary
never printed at all.

Requiring a clean exit immediately surfaced a real defect in the host: deferred
calls run last-in-first-out, so `defer cancelRun()` registered early ran *after*
`cmd.Wait()`. The host was waiting for processes it had already decided to kill,
and an adapter that ignores its closed stdin spun until the caller's own context
expired. **The suite went from 5m14s to 9s**, with all claims still proven —
five minutes of it had been waiting on the dead.

## Round four — a green claim over a data race

Review ran `go run -race .` and the resumption claim reported `PROVEN` **while
racing**: the abandoned continuation and the resumed one both settled the same
attempt. Nothing in the suite noticed, because nothing in the suite was
race-instrumented. **Every run is now under `-race`**, including each mutation
check — the cheapest thing that would have caught it.

The rest were guarantees the ADR **stated and the code did not implement**:

| Stated | Actually |
| --- | --- |
| A wait is recoverable after a restart | Recovery persisted the complete substituted request — data ADR 0030 §3 keeps out of Audit — and promised resumption where §6 offers only artifact-level restart. A wait now goes **`stale`**; only the operator **decision** is persisted, so the human is not re-asked |
| Delivery is at-least-once | The watermark could skip a gap, acknowledgements were one scalar across epochs, the sender retained nothing, and an `action_request` was acknowledged before its intent was durable. The guarantee is now **narrowed by event type** rather than universally promised |
| Cancellation drains admitted actions | "Settled" was treated as "safely drained". Attempts now record a **disposition**; a declared wait is *stopped* rather than waited on |
| Every forced ending fences | Protocol violations and transport failures went straight to recording a failure, leaving actions unsettled and the resource unfenced |
| A question is an artifact | The spike still sent inline text, and there was no wire path for the answer. The question is published and its **reference** routed; the answer arrives on the mutable **bindings** |
| Identity is provenance | `started` was carried and never compared — not required to be first, unique, or consistent with the handshake |

### And one of the fixes was wrong in the same way

Caught by the suite rather than by review: marking a drained attempt `stale` did
not **stop** its goroutine, which committed anyway and drove the receipt to
`unconfirmed`. That is ADR 0030 §5's own rejected option — *invalidate the
attempt* — rediscovered by implementing it. The continuation now re-reads its own
attempt before the effect and abandons it if the drain settled it.

A second, smaller one: the first version of the drain rejected **any** commit
after admission closed, which made every cooperative cancellation unconfirmed —
an action admitted before closure and finishing inside the grace period is the
ordinary case. What the drain guarantees is that nothing commits *after the
receipt*; a commit during the drain is recorded, not refused.

## Round five — the fixes that only half applied

Every one of these was a rule the previous round introduced and applied
incompletely, which is the same shape as round two but one turn further in.

| Introduced | Applied only half-way |
| --- | --- |
| `approve_once` is consumed once | The grant was promoted the moment it was **given**, so the action it was given for never consumed it and the next identical action inherited it. It is promoted only when its own attempt goes stale before commit, and keyed by the requirement set |
| Gate 1 collects the complete requirement set | The set was collapsed again before it reached anyone: the operator saw the first requirement, the blocked result carried the first, and the computed scope intersection went unused |
| A wait blocks the caller | Only the **headless** block set the guard, so the interactive wait the rule was written for went unguarded — and the guard ran after registration, so a rejected call left an unsettled attempt for the drain to wait on |
| Reliable and best-effort are separate streams | The stream became part of event identity and nothing said so; the receiver also trusted the caller's stream, so a `usage` could claim `best_effort` |
| Replay is deduplicated | Against the watermark alone. An event committed **beyond a gap** sits above the watermark, so its replay was accepted and applied twice |

Two of the mutations written for round four stopped killing anything once these
landed, and both are informative rather than embarrassing: moving deduplication
from the watermark to the committed set made the *watermark* mutation
non-discriminating, and separating the streams made `ResetSeq` no longer
load-bearing. The first was retargeted; the second was **retired**, because a
mutation that no longer describes a defect is not evidence of anything.

## Round six — truthfulness, atomicity, and a correction I had followed

| Found | Why it mattered |
| --- | --- |
| Acknowledgements lied across gaps | Committing 2 while 1 was missing acknowledged `Through: 2`, declaring a 1 that never landed. An ack now reports the **watermark**, the only number that is true |
| Stale settlement and promotion were not atomic | A crash or a successor between them loses the grant; a late operator answer could write onto an already-settled attempt. One critical section, and a settled attempt refuses a late grant |
| The response guard was parallel in-memory state | Lost on restart, and cleared before the record it shadowed. It is now **derived** from the durable response-wait record, and delivering the answer is the single write that closes both |
| Both ACK tests relied on scheduling | "Replay before ack" assumed an ack could not arrive first; release testing slept. The host now has an explicit **drop-acks** hook, and the agent waits on the **outbox draining** rather than on any ack — the first one to arrive may be for the other stream |

**And one correction I had followed into a mistake.** Round five told me to reject
a call while waiting *before opening a record*; I read that as opening **no**
record. ADR 0030 §8 requires a denial to be opened and completed **together** —
the rule is never to leave an *unsettled* attempt, not never to record one. The
guard now writes a terminal denied record atomically. Denials are the
observations candidate 12 will be tuned against, so losing them silently was the
worse failure of the two.

**A mutation aimed at the drain killed nothing**, because the only claim
exercising stale-promotion went through *reconciliation*. The drain path had no
coverage at all; it has its own claim now. A mutation that survives because no
claim reaches its path is the harness reporting a hole, not a false alarm.

## Round seven — and a fix that silently did not apply

Three, all narrow, and the third is the one worth recording.

| Found | Why |
| --- | --- |
| A terminal denial did not claim its correlation | Left deliberately unclaimed on the reasoning that a retry should be admitted once the wait cleared. That reasoning *was* the defect: one correlation is one logical action, so leaving it free lets the same key produce a second terminal record, or a denial **and** an effect. Replaying the denial is the correct answer; an intentional later attempt needs a new correlation |
| The outbox recorded **after** transmission | An acknowledgement processed against an empty outbox strands the entry appended after it — the sender waits forever for a release that already happened. Retention is now registered under the lock the ACK handler takes. A **durable** outbox must go further and persist *before* transmitting; that is Phase 3's |
| The gap-ACK claim slept | Finding the entry retained after 400ms proves nothing if the acknowledgement was merely late. It now **observes** the reliable-stream ACK and asserts its watermark is zero before inspecting retention |

**The outbox fix silently did not apply.** An earlier `gofmt` had rewrapped the
line the replacement targeted, so the edit matched nothing — and the claim
written for it **passed anyway**, because the race is timing-dependent and did
not happen to occur. What caught it was the mutation harness reporting
`MUTATION DID NOT APPLY`: the anchor it needed was absent, which could only mean
the code did not look the way the fix claimed. The mutation now also injects a
delay, so the mutant exhibits the race deterministically rather than hoping for
it — a defect a claim can only catch by luck is not protected.

## The claims

Fifty-nine. Thirty-nine spawn a process; twenty exercise boundary and schema
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
| `protocol/unknown-message-type-is-fatal` | §8 — the enumerated fatal list |
| `protocol/malformed-known-body-is-fatal` | §8 |
| `protocol/epoch-ahead-of-binding-is-fatal` | §4 — an incarnation never issued |
| `cancel/undrained-action-yields-no-positive-receipt` | ADR 0030 §5 — actions, not only the domain |
| `gate/headless-block-is-orchestrator-driven` | §5 — a runtime that refuses to stop is stopped |
| `boundary/correlation-is-bound-to-its-logical-action` | ADR 0030 §3 — action and argument digest |
| `boundary/admission-closure-linearizes-with-registration` | ADR 0029 §7 step 2, deterministically |
| `events/prior-epoch-replay-deduped-across-restart` | §4 — durable watermark |

**`wait/transport-stays-live-during-an-operator-wait` is the claim that closes
round two's largest gap.** The first implementation ran both gates synchronously
inside the transport's only event loop, so while an action waited the host could
process no cancellation, no heartbeat, and no re-attachment — ADR 0030's detached
logical wait *claimed* rather than demonstrated. The claim now asserts that the
loop sent cancellation while the operator gate was still deciding, by timestamp.

## Defect-shaped verification

`go run ./mutate` restores forty-four defects, one per protected property, and
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
| `correlation-not-bound-to-its-action` | One key replaying a different action's result | KILLED |
| `correlation-not-bound-to-its-arguments` | A result computed for different input | KILLED |
| `admission-closure-not-linearized` | An attempt joining the set after it closed | KILLED |
| `wait-persists-no-executable-request` | A wait preserved and permanently stuck | KILLED |
| `watermark-does-not-outlive-the-process` | Dedup reset by the restart it exists to survive | KILLED |
| `no-drain-before-the-fence` | A receipt that covers the domain and not the actions | KILLED |
| `headless-block-waits-for-a-courtesy-exit` | A blocked Story still doing work | KILLED |
| `envelope-not-validated` | Version skew as silent data loss | KILLED |
| `malformed-known-body-ignored` | Unattributable spend recorded as attributed | KILLED |

**One mutation initially survived, and the survivor indicted the assertion rather
than the mutation.** `capability/denial-is-data` checked the execution's status
before checking that a denial had been recorded, so disabling the capability gate
tripped the *agent's own* consistency check first and the claim failed with a
different message. The claim now asserts the mechanism — a recorded denial, on
capability grounds — before the consequence. The harness caught it only because
it required the failure to match a named reason rather than merely to occur.

**One intermittent failure, unreproduced, and what was done about it.** A
verification run reported a single `FALSIFIED` under `-race` that nine
subsequent runs could not reproduce, and the command that caught it had not
captured which claim. Rather than record it as noise, the most plausible cause
was removed: `wait/transport-stays-live-during-an-operator-wait` requested
cancellation on a **timer** and asserted an ordering against it, so under race
instrumentation and load the timer could fire before the state it meant to
interrupt was reached. It is now **signal-driven** — the gate asks for the
cancellation once it is demonstrably holding — so the ordering holds by
construction rather than by margin. Eight further race runs are clean. It is
recorded because an unreproduced failure that was never explained is not the
same as one that was, and the difference matters more than the tidiness.

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
| Model calls, token accounting, per-model-call provenance bindings | The stub makes none. The ordinary path emits nothing in their place; the three delivery scenarios emit a synthetic `usage` **transport fixture** only, and no accounting conclusion is drawn from it — see below | Phase 3's real build-out |
| Concurrency accounting (§7) | There is no scheduler in the slice, so the claim that a blocked execution consumes no runnable concurrency is **reasoned, not measured** | Phase 3 |
| Resumable runtimes, the resume token, and the retention window | The stub declares `resumable: false` | Phase 3, with an adapted runtime that can resume |
| A **durable** sender outbox surviving the adapter's own restart | The spike retains in-process only | Phase 3 |
| The provenance retention traversal (§9) | No retention runs here | Phase 4, per ADR 0029's deferred list |
| Composite and paired execution (§9) | One participant only | Phase 5, where heterogeneity is an exit criterion |
| Any transport but the local one | Only stdio is implemented, and it produces no reconnection case at all | Deferred with [candidate 14](../notes_adr-backlog.md) |
| The data plane | The recorder is in-memory | Checked instead by reading the migrations — see below |

**One exception, stated because the claim above would otherwise be false.** Three
delivery scenarios (`replay_before_ack`, `replay_outbox`, `replay_beyond_gap`)
DO emit a synthetic `usage` envelope. It is a **transport fixture**: the
retention, acknowledgement and deduplication machinery needs a message carrying
a retention obligation in order to move one, and `usage` is that type. Its
numbers are invented and **no accounting or provenance conclusion is drawn from
them** -- the claims assert only how many envelopes were recorded, never what
they contained. The ordinary execution path still emits none, which is what
`result/no-provenance-event-without-a-model-call` asserts.

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
