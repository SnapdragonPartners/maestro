+++
title = "Pre-Phase-3 Blockers: Scope And Sequencing"
edit_date = "2026-08-09"
status = "draft"
summary = "What must be settled before Phase 3 implementation begins: a design cluster of three mutually-constraining ADRs (Habitat, tool-execution policy hook, agent execution contract) plus prompt-pack identity and the amendment-vs-running-work policy, a parallel cloud-portability proof, benchmark repair for the two runs Phase 3 owes, and the authority cleanup the ADR backlog needs before any of it can be Accepted."
type = "plan"
+++

# Pre-Phase-3 Blockers: Scope And Sequencing

Status: **draft** — proposed by Claude 2026-08-09 from DR's and Codex's joint
sequencing, not yet reviewed. Nothing here is binding until Codex and DR accept
it.

## What This Document Is

Phase 2 closed on 2026-08-08 ([exit record](../phase_2/notes_exit-record.md)).
Phase 3 builds the minimal Work Hierarchy and Work Group runtime and retires
v1 — the phase that reworks agent lifecycle, tools, workspaces, and
`cmd/maestro` all at once. The [ADR backlog](../notes_adr-backlog.md) states its
own rule: *an entry should be Accepted before its blocking phase starts
implementation.* Three of its candidates name Phase 3, two GitHub issues name
themselves pre-Phase-3, and the phase inherits an unmet regression obligation
from Phase 2.

This document collects that work, sequences it, and names what "done" means for
each item, so that Phase 3's own scope and plan (`plan_scope.md`, not yet
written) opens against settled contracts rather than discovering them.

It is **not** the Phase 3 plan. It ends where that plan begins: the last item
here is "accept the Phase 3 scope and plan."

## What It Is Not Allowed To Become

Every item below has a defensible claim to being pre-entry work. That is exactly
the failure mode: a pre-phase that absorbs the phase. Two constraints:

- Design items produce an **Accepted ADR and nothing else**. No implementation
  lands on the strength of a pre-entry ADR; implementation is Phase 3 items.
- The engineering runway and the benchmark repair are **not gates on the design
  track**. They are gates on specific Phase 3 activities (the paid runs, the
  concurrency work) and are sequenced against those, not against phase entry.

## The Shape Of The Work

Four tracks, not one chain. The serial reading of this list is the main
correction proposed to the original sequencing (see [Deltas](#deltas-from-the-proposed-sequencing)).

```text
Track A — design (critical path for phase entry)
    A1 Habitat  ─┐
    A2 tool hook ├─ one cluster, reviewed as a set ──┐
                 │                                   ├─ A4 agent execution contract ─┐
    A3 prompt pack identity ───────────────────────────┘                             │
                                                                                     ├─ A5 amendment vs running work
                                                                                     │
Track B — cloud portability proof (#286)          parallel, no design dependency
Track C — benchmark repair (#317, #316, #318, #319, model probe)   gates the paid runs
Track D — engineering runway (#314, #321, #306, #307, #308)        gates the concurrency work
```

Track A is the only track that gates *phase entry*. B, C and D gate specific
things inside Phase 3 and are scheduled against those.

---

## Track A — Design

### A1. Habitat execution boundary — [#273](https://github.com/SnapdragonPartners/maestro/issues/273), design portion only

Establish Habitat as the Orchestrator-managed execution-resource boundary:
identity, `HabitatSpec` versus mutable `HabitatInstance`, generation/fencing so
a recycled Habitat cannot satisfy a stale reference, lifecycle
(provision → ready → lease → release → reconcile → destroy), agent-to-Habitat
cardinality including read-only Architect inspection, restart and reconciliation
expectations, and the rule that **tools target a Habitat reference, never an
Agent-derived local path**.

Why pre-entry: Phase 3 cuts `pkg/workspace`, `pkg/exec`, container state, Coder
setup, and Architect inspection of Coder workspaces. Establishing the boundary
after Phase 3 cuts all of them twice. It also permanently removes the need for
the Architect to bind-mount Coder workspace roots, which is what makes inode
preservation a cross-agent contract today (CLAUDE.md's bind-mounted workspace
invariant, ADR 0027).

Done: an Accepted ADR covering the list above. **Explicitly out**: the local
Docker provider implementation, the persistence migration, and speculative
warming policy — all Phase 3 items.

Open for review: the name. `Habitat` is provisional in the issue;
`Environment`, `Workspace`, `Runtime`, and `Sandbox` are all more overloaded.
Settle it in the ADR, because it will appear in table names.

### A2. Tool execution policy hook — [backlog candidate 4](../notes_adr-backlog.md)

Where the mandatory per-action policy hook lives and what its interface is. No
policy content — the gating rules are [candidate 12](../notes_adr-backlog.md),
post-MVP.

Proposed placement (Codex, endorsed): one mandatory hook at the Orchestrator's
central tool-execution boundary, after principal/Habitat capability resolution
and before the side effect. Not in the toolloop, not in the dispatcher, not in
individual tools, not a separate policy service.

**The decision this ADR must actually make is broader than placement.** A single
in-process hook enforces nothing against an external agent that executes its own
tools inside its own runtime — a Claude Code adapter running its own file edits
does not pass through Maestro's tool boundary. So the ADR must state the scope
invariant that makes the hook meaningful:

> Any tool action with a side effect on a Maestro-managed resource — Habitat
> state, repository content, the data plane, the forge — is executed by the
> Orchestrator through this boundary. An external agent runtime's internal tools
> are outside the boundary and therefore may not reach those resources; their
> access is mediated by capability through the execution contract (A4).

Without that sentence, "native and external agents share the same enforcement
point" is an aspiration rather than a property. With it, the hook is a real
chokepoint and A4 has a concrete requirement to satisfy.

Done: an Accepted ADR fixing the placement, the interface, the scope invariant
above, and the relationship to candidate 12.

Cheapest item here to decide and the most expensive to defer: Phase 3 builds the
tool plumbing, and a seam not chosen gets retrofitted into every tool.

### A3. Prompt pack identity, resolution, and storage — [backlog candidate 5](../notes_adr-backlog.md)

The minimal contract: pack identity and content hash, resolution (which pack a
run uses, decided once and deterministically at dispatch), and data-plane
storage. The resulting invocation carries an immutable pack ID and content
digest; the data plane holds the authoritative pack record; and
`principal_instances.prompt_pack_id` becomes a real reference.

The concrete debt: that column is a plain nullable `text` today with **no table
behind it and no FK**. The [schema inventory](../phase_2/inventory_schema-tables.md)
lists prompt packs as a deferred family whose creator is Phase 3. Meanwhile the
MPH signature's P component has been recorded informally since Phase 1 (bundle
`prompt.pack` plus an optional hash the adapter computes).

Explicitly deferred to [candidate 9](../notes_adr-backlog.md): registry
inheritance, installed org-level packs, versioning and export, repo-local packs,
and skills.

Done: an Accepted ADR. The migration that creates the family is a Phase 3 item.

A3 has no dependency on A1 or A2 and can be authored in parallel with them. It
joins the graph at A4, whose invocation schema carries pack identity.

### A4. Agent execution contract — [#282](https://github.com/SnapdragonPartners/maestro/issues/282)

The versioned wire contract: invocation, events, terminal outcomes, lifecycle,
provenance, transport, and capability-based tool/knowledge access. Finalized
against the Habitat, tool-policy, and prompt-pack contracts above.

Two splits are proposed:

**Split 1 — contract from presentation.** The issue bundles the contract and
conformance work with a standalone code-review agent that produces GitHub
Actions annotations. Only the contract and one exercised vertical slice are
pre-entry. Actions presentation polish must not hold Phase 3 hostage.

**Split 2 — the terminal-outcome vocabulary is one artifact with four
feeders.** The contract owns it, and these all resolve into it rather than being
solved separately:

| Source | What it needs the vocabulary to express |
| --- | --- |
| [#317](https://github.com/SnapdragonPartners/maestro/issues/317) | A headless escalation terminates as a durable **blocked** or **timed-out** outcome that stops requeues and stops billing. Today it deadlocks into `ESCALATED`, which no headless run can answer. |
| [#280](https://github.com/SnapdragonPartners/maestro/issues/280) | **already-satisfied** (or equivalent) is distinct from a completed Story with a PR. Today the empty-PR case is recorded as ordinary completion and reads as a false negative. |
| A5 below | **superseded** is distinct from failed, for work cancelled because its version was amended. |
| The issue itself | retryable infrastructure failure distinct from non-retryable agent failure. |

Treating these as one vocabulary is the point: three of them were discovered
independently and each would otherwise add a one-off status.

Done: an Accepted contract ADR plus one executable agent proven against it. The
#317 and #280 *code* fixes are Phase 3 items; what is pre-entry is that the
vocabulary has a place for them.

### A5. Amendment versus running work — [backlog candidate 3](../notes_adr-backlog.md)

Narrower than it reads. ADR 0019's dispatch amendment already settled the
*pending* case: invalidate version-bound dispatch records, re-evaluate the DAG
deterministically, reissue — no agent in the loop, and explicitly never by
draining in-flight channels. ADR 0021 settled the record side, ADR 0028 the
encoding. **Only the in-flight executor's fate is open.**

Proposed rule (Codex, endorsed):

- Mark the old execution cancellation-requested.
- Allow its current atomic tool action to reach a safe boundary.
- Prohibit further actions, and prohibit acceptance against the superseded
  version.
- Retain its output as attributable draft/Audit history.
- Terminate it as **superseded**, not failed.
- Recompute the DAG and dispatch against the new effective version.

Rejected alternatives and why: suspend/resume is too much machinery for Phase 3;
complete-then-reconcile lets stale work progress and then requires judgment from
deterministic machinery, which ADR 0019 forbids.

Depends on A2 more concretely than "after the lifecycles exist on paper": *the
tool-execution hook is the natural observation point for cancellation-requested*,
because it is the one place a per-action boundary already exists. And it depends
on A4 for the `superseded` terminal outcome. A5 is therefore genuinely last in
Track A.

### A6. Accept the Phase 3 scope and plan

Phase entry proper. Written against A1–A5 as Accepted, and carrying the Track
B/C/D items as scheduled Phase 3 work with their gates named.

---

## Required Authority Cleanup

Two conflicts must be reconciled *before* A1 and A4 can be Accepted, because
they leave two competing abstractions on the books.

1. **The ADR backlog still places the superseded items post-MVP.**
   [Candidate 11 "Container Runtime Abstraction"](../notes_adr-backlog.md) is
   what Habitat supersedes — #273 says so directly ("amend the existing post-MVP
   Container Runtime Abstraction backlog item rather than leaving two competing
   abstractions"). [Candidate 13 "External Agent Runtime Contract"](../notes_adr-backlog.md)
   is what #282 supersedes. Both must be re-scoped or converted to resolved
   stubs pointing at the new ADRs. The backlog's own convention applies: a
   resolved candidate **keeps its slot** rather than being deleted, so Habitat
   and the execution contract take new numbered slots and 11/13 become pointers.

2. **#273 requires "Phase 2 persistence hooks," and Phase 2 is closed.**
   Proposed resolution: **no Phase 2.1 for schema.** The Habitat tables become a
   Phase 3 migration, exactly as prompt packs already are — the schema inventory
   already carries deferred families with a named creator phase, migrations are
   additive, and the plane is versioned. Reopening a closed phase to add tables
   would set a worse precedent than the one it fixes. Rewrite #273's section 2 to
   name Phase 3 as the creator.

   This leaves **Phase 2.1 meaning exactly one thing: #286** (below), which is
   genuinely Phase 2's seam being proven rather than new Phase 2 scope.

---

## Track B — Cloud Data-Plane Portability ([#286](https://github.com/SnapdragonPartners/maestro/issues/286))

Prove the persistence composition boundary is genuinely pluggable — one managed
Postgres, one real cloud object store, mode selected at the composition boundary
with no application-level branching — before Phases 3–6 make local assumptions
expensive to remove.

**Proposed: this runs parallel to Track A, not at the head of it.** It is the
only implementation item in the pre-entry set, it needs cloud credentials,
a project, and spend approval, and **no Track A design decision depends on its
result.** Habitat is execution, not persistence; the tool hook and the execution
contract do not touch the storage seam; prompt-pack storage depends on the seam
holding, which Phase 2 items 4 and 9 already demonstrated. Putting an
externally-blocked implementation item at the head of a serial chain buys
schedule risk for no design certainty.

Two additions proposed to its acceptance criteria:

- **The proof must be a re-runnable manual workflow, not a one-shot report.**
  Phase 3 adds migrations (Habitat, prompt packs, Work Groups). A one-time
  portability report is stale the moment those land; a workflow that can be
  re-triggered stays a live check.
- **Explicit DR approval for the cloud spend**, on the same footing as a paid
  golden run.

---

## Track C — Benchmark Repair

Phase 3 owes **two** paid golden runs: Phase 2's carried regression run against
v1-as-patched (which gates v1 retirement, because removing v1 destroys the only
target that can discharge it) and Phase 3's own phase-end run against the v2
adapter. Both are currently unrunnable. This track is early Phase 3 work, not
phase-entry work, but it must be scheduled early because everything downstream
of v1 retirement waits on it.

| Item | Status here |
| --- | --- |
| [#317](https://github.com/SnapdragonPartners/maestro/issues/317) architect approval loop cannot force its terminal tool | **Required before either run.** It blocks the committed `paired-default` outright. |
| **Architect model probe** | **Required, and not yet an issue.** `claude-opus-4-1` is retired. `gpt-5`, `o4-mini` and `claude-opus-5` are excluded by #316. `gpt-4o`, `claude-opus-4-5` and `claude-opus-4-6` accept temperature and have **never been tested against #317** — the viable set was not shown empty, only unexplored. This needs a cheap targeted probe, not a full paid suite. |
| [#318](https://github.com/SnapdragonPartners/maestro/issues/318) dirty-tree preflight | **Required before either run.** The phase-exit target was built from a dirty tree; the digest pinned what ran and the commit did not reproduce it. |
| [#319](https://github.com/SnapdragonPartners/maestro/issues/319) model-lifecycle preflight | **Required, upgraded from "preferably."** This is the check that would have caught the Opus 4.1 retirement seven weeks before it cost the phase-exit run. It is cheap and it is the only item on this list that prevents a *recurrence* rather than repairing a *symptom*. |
| [#316](https://github.com/SnapdragonPartners/maestro/issues/316) sampling parameters forced non-nil | **Not a gate on the carried run** — `gpt-4.1` accepts temperature — but it should land early anyway. It is a small fix, and until it does, every reasoning-tier model is undrivable, which is what shrank the replacement pool to guesswork. |

**One design note carried from ADR 0020's amendment.** #319 creates a
model-metadata surface (a pinned model ID's published deprecation and retirement
dates). Model **lineage** — the set of originating labs, the one genuinely
missing input for the reviewer-heterogeneity mechanism — is the same *kind* of
fact about the same key, and belongs in the same structure rather than being
restated per configuration. #319 should therefore land with room for it. The
mechanism itself remains a Phase 5 exit criterion; only the shape of its home is
decided here, and only to avoid building the surface twice.

Each of the two runs needs **explicit DR approval for that specific run**. Phase,
plan, and previous-run approval are not reusable.

Open question for DR/Codex: does
[#279](https://github.com/SnapdragonPartners/maestro/issues/279) (no behavioural
evidence kind; ADR 0025 rung 5 cannot be expressed as written) gate *recording*
these two runs in the conformance log, or can they be recorded under the
existing kinds with the gap noted? Phase 3 is the first phase that will produce
two runs against two different targets, which is when a recording gap is most
likely to bite.

---

## Track D — Engineering Runway

Serial, before the code-heavy Phase 3 work, if schedule permits. None of it
gates phase entry.

1. [#314](https://github.com/SnapdragonPartners/maestro/issues/314) — a checked-in
   mutation harness. Non-blocking, but Defect-Shaped Verification is now binding
   in [`process_build.md`](../process_build.md) and Phase 2 paid for it by hand
   in every item. This repays across all of Phase 3.
2. [#321](https://github.com/SnapdragonPartners/maestro/issues/321) — reap leaked
   integration Compose projects. Treat this as a **prerequisite** of #307 rather
   than merely adjacent to it: raising concurrency multiplies the leak.
3. [#306](https://github.com/SnapdragonPartners/maestro/issues/306) — the pre-push
   integration gate passes on cached results, so it is not currently a gate at
   all. Fixing it makes the suite slower, which is why the instinct is to do it
   after #307 — but #307 is the **high-risk** change (process-global config in
   the isolated-plane harness; the path where an earlier bug had children
   resolving the developer's real roots, and a child `reset` would have deleted
   their cluster). Do not perform that refactor behind a gate that does not run.
   **#306 first, and eat the slow window.**
4. [#307](https://github.com/SnapdragonPartners/maestro/issues/307) — parallelize
   the isolated integration planes. The work that materially improves local
   integration time.
5. [#308](https://github.com/SnapdragonPartners/maestro/issues/308) — profile
   `make build` (194s of CI's 339s). A separate CI optimization; it will not
   improve the local integration suite.

---

## In Phase 3's Plan, Not Pre-Entry

These are Phase 3 gates that must appear in `plan_scope.md`, but do not block
opening it:

- [#265](https://github.com/SnapdragonPartners/maestro/issues/265) — single-owner
  agent restart; remove the dual death-observer shape.
- [#272](https://github.com/SnapdragonPartners/maestro/issues/272) — explicit
  provider/endpoint declaration, folded into the new execution contract rather
  than solved separately. (Note the trap recorded in ADR 0020's amendment:
  `Provider` describes routing and is **not** lineage.)
- [#287](https://github.com/SnapdragonPartners/maestro/issues/287) — fold
  `dataplanectl` into the replacement main binary, which needs embedded compose
  assets. Naturally sequenced with the `cmd/maestro` rework.
- [#298](https://github.com/SnapdragonPartners/maestro/issues/298) — no longer a
  scheduling blocker: the roadmap now assigns all `drop` dispositions to Phase 3
  retirement. **Close it once that roadmap statement is accepted**, and carry the
  five deletions into the Phase 3 exit checklist so closing the issue does not
  lose them.

---

## Deltas From The Proposed Sequencing

Recorded explicitly so DR can arbitrate each one rather than accepting the whole
document. Everything not listed here is Codex's proposal adopted unchanged.

| # | Delta | Reason |
| --- | --- | --- |
| D1 | #286 moves off the head of the serial chain to a parallel track | It is the only implementation item, it is externally blocked on credentials and spend, and no Track A decision depends on its result. |
| D2 | A1 and A4 are accepted as a set, not in sequence | #282's own text says it **blocks completion of** #273. A strict 2 → 5 ordering inverts a declared dependency. The spikes proceed concurrently; the ADRs are consistency-checked in one review round. |
| D3 | A2's ADR must state a scope invariant, not only a hook location | A single in-process hook enforces nothing against an external agent running its own tools. Without the invariant, the shared-enforcement-point claim is not a property of the design. |
| D4 | The terminal-outcome vocabulary is one artifact with four feeders, adding A5's `superseded` | #317, #280 and A5 were discovered independently; each would otherwise add a one-off status to the same enum. |
| D5 | No Phase 2.1 for schema — Habitat tables are a Phase 3 migration | Matches the existing reserved-family convention (prompt packs already work this way). Phase 2.1 then means exactly #286. |
| D6 | #286's proof must be a re-runnable workflow | Phase 3 adds migrations; a one-shot report is stale on arrival. |
| D7 | Backlog candidates 11 and 13 become resolved stubs; Habitat and the execution contract take new slots | The backlog's own numbering convention, and #273's explicit instruction not to leave two competing abstractions. |
| D8 | #319 is required, not "preferably", and lands with room for model lineage | It is the only Track C item that prevents recurrence rather than repairing a symptom, and lineage is the same kind of fact about the same key. |
| D9 | #316 added to Track C as early-but-not-gating | Small fix; until it lands the replacement-model pool is restricted to non-reasoning models, which is what made the phase-exit failure unrecoverable. |
| D10 | Track D order kept, with #306 before #307 for a stated reason | #307 is the high-risk refactor and must not be performed behind a gate that does not actually run. |
| D11 | An architect-model probe is added to Track C as its own item | The viable set is **untested, not empty** — a distinction the Phase 2 exit record had to be corrected on once already. |

## Open Questions

1. Does #279 gate recording Phase 3's two conformance runs, or only rung-5
   claims about them?
2. Is `Habitat` the accepted name? It will appear in table names, so it should
   be settled in A1 rather than after.
3. Does the A4 vertical slice have to be the GitHub Actions review agent, or may
   it be any single executable agent exercised over the local transport? The
   former couples phase entry to a presentation surface; the latter proves the
   contract just as well.
4. Track B needs a spend decision from DR before it can be scheduled at all.
