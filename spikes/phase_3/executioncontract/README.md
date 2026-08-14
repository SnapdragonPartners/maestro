# Phase 3 conformance slice: the agent execution contract

The executable half of [ADR 0032](../../../docs/adr/0032-agent-execution-contract.md).
The written-up evidence is
[`docs/v2/phase_3/spike_execution-contract.md`](../../../docs/v2/phase_3/spike_execution-contract.md);
this directory is the code.

```bash
go run .            # every claim
go run . -v         # with the event stream per scenario
go run . -run NAME  # one claim
go run ./mutate     # defect-shaped verification of the claims themselves
```

**Exit code is 0 only when every claim is `PROVEN`.** Outcomes are three-valued,
following the [Docker fencing spike](../fencing/README.md):

| Outcome | Exit | Means |
| --- | --- | --- |
| `PROVEN` | 0 | The claim held, and its control held too. |
| `FALSIFIED` | 1 | The claim is false. A **successful run that changes the ADR**. |
| `ERROR` | 1 | An observation failed; **nothing is established either way**. |

No Docker, no network, no API keys, no money. It builds one binary into a temp
directory and spawns it once per scenario.

## What it is

`reviewagent` is a **real external process** that speaks the contract over
stdin and stdout. A4 is explicit that an in-process fake or an echo fixture does
not discharge it, so every wire scenario spawns a process and exchanges
newline-delimited JSON with it. Twenty-four of the thirty-three claims do; the
remaining nine exercise boundary and schema properties the wire scenarios depend
on but cannot isolate, and are labelled separately so nothing there reads as
evidence about the wire.

It is a code-review agent because [#282](https://github.com/SnapdragonPartners/maestro/issues/282)
names one as the contract's first external consumer. **It is not a code
reviewer.** Its analysis backend is an explicit stub (DR, 2026-08-13) that
minimally satisfies the contract rather than pretending to do useful work; the
real build-out is Phase 3's. The stub flags added lines containing `TODO`, a
rule chosen because it is obviously not review.

| Package | Is |
| --- | --- |
| `contract/` | The wire schema — the normative half. Messages, the invocation, the four-axis terminal result and its applicability rule, the codec |
| `host/` | The Orchestrator side: ADR 0030's three gates in miniature, the attempt recorder, and process supervision |
| `reviewagent/` | The external executable |
| `mutate/` | Defect-shaped verification of the conformance claims |

## What it does not cover

Stated because a suite that is silent about its gaps reads as covering
everything. None of these is fabricated to make a scenario look green —
ADR 0025's `unavailable`-versus-zero discipline applied to test evidence.

- **Model calls, usage events, token accounting, and per-model-call provenance
  bindings.** The stub makes no model calls, so nothing here exercises them —
  and it emits **nothing in their place**. An earlier version emitted a `closed`
  provenance record with call reference `no-model-call`, fabricating exactly the
  coverage this list declares missing; `result/no-provenance-event-without-a-model-call`
  now asserts the absence.
- **Concurrency accounting** (ADR 0032 §7). There is no scheduler in the slice,
  so its claim that a blocked execution consumes no runnable concurrency is
  reasoned, not measured.
- **Resumable runtimes, the resume token, and the retention window.** The stub
  declares `resumable: false`.
- **The provenance retention traversal** (§9). No retention runs here.
- **Composite and paired execution** (§9), and any transport other than the
  local one — stdio produces no reconnection case at all, only restarts.
- **The data plane.** The recorder is in-memory. What is checked against the
  real schema is checked by reading the migrations, not by running them.

## Status

**Spike code, unmaintained**, per CLAUDE.md's Spikes And Deferred Work. It is a
standalone Go module, so the root module's build, test, and lint walkers skip it
by module boundary. It never merges into `pkg/`, `internal/`, or `cmd/`.

It is the single bounded exception to the pre-Phase-3 rule that a design item
produces an Accepted ADR and nothing else
([blocker plan](../../../docs/v2/phase_3/plan_blockers.md)). **Where it lands
permanently is a Phase 3 decision.**

## Mutation harness

`go run ./mutate` restores fourteen defects, one per protected property, and
requires each to falsify its named claim **for the named reason**. A compiler
failure, an `ERROR`, or a failure at a neighbouring guard is not a killed
mutation.

It writes `.mutation-in-progress` before touching anything and refuses to start
while one exists, because a killed harness does not run its restore and the next
run would otherwise layer a second mutation on a tree that no longer describes
the defect. Restoration is verified by SHA-256, not assumed.

One mutation initially **survived**, and the survivor indicted the assertion
rather than the mutation: `capability/denial-is-data` was checking the
execution's status before checking that a denial had been recorded, so
disabling the capability gate tripped the agent's own consistency check first
and the claim failed for the wrong reason. The claim now asserts the mechanism
before the consequence.
