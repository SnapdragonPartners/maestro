+++
title = "Maestro v2 Phase 3: Scope And Plan"
edit_date = "2026-08-16"
status = "draft"
summary = "Proposed Phase 3 scope and execution plan: build the smallest real v2 factory path — work hierarchy, a single Work Group lifecycle, the agent execution boundary, Incubators and Habitats, a contract-only intake and an Epic dashboard skeleton — then retire v1 behind a proven v2 benchmark adapter. Sixteen items in four blocks with a checkpoint at each seam, implementing the five Track A ADRs and settling the mechanisms ADR 0032 deliberately handed back as design inputs."
type = "plan"
+++

# Phase 3: Minimal Work Hierarchy And Work Group Runtime — Scope And Plan

Status: **draft** — proposed by Claude 2026-08-16, item A6 of the
[pre-Phase-3 blocker plan](plan_blockers.md). Flips to `live` in the acceptance
commit, before its own merge, following Phase 2's precedent (Phase 1's plan
merged still `draft` and needed a follow-up flip PR).

Goal (from the [roadmap](../plan_roadmap.md)): create the smallest real v2
factory path.

## What Binds This Phase

Track A is complete, and its five decisions are the specification. This plan
sequences the work and fixes what they left open; where it and they diverge,
**they win**.

| Decision | What it binds here |
| --- | --- |
| [ADR 0029](../../adr/0029-incubator-and-habitat-execution-boundaries.md) | Incubator and Habitat as two resources; identity, generation, lease and retention claim; the fencing protocol and its three-valued receipt; tools target a resource reference, never an Agent-derived path |
| [ADR 0030](../../adr/0030-tool-execution-policy-hook.md) | One mediated boundary in three gates, with resources acquired only after approval; a human-required call blocks; the tool call as the atomic Audit action unit |
| [ADR 0031](../../adr/0031-prompt-pack-identity-resolution-and-storage.md) | Scheme-qualified content identity; name as a label, never a selector; immutable content beside a mutable installation record; resolution once at dispatch |
| [ADR 0032](../../adr/0032-agent-execution-contract.md) | The thirteen binding items of its **closed** list — the versioned boundary, the three model identities, capabilities and fenced references, no agent data-plane access, mediated actions with durable intent and result records, the four axes and their applicability rule, drain-then-fence before a positive terminal result, rejection of superseded or fenced authority, the two lifetimes, the concurrency accounting, and the required provenance facts |
| [ADR 0019](../../adr/0019-orchestrator-boundary.md) 2nd amendment | Running work is cancelled when the **dispatch basis** it was issued under stops being current; enforcement supersedes the execution's authority; nothing already done is revoked |

Also binding: [ADR 0018](../../adr/0018-v2-work-taxonomy.md) (hierarchy),
[ADR 0021](../../adr/0021-artifacts-and-principal-instances.md) (artifacts,
principal instances, lifecycle),
[ADR 0022](../../adr/0022-v2-data-plane.md) (persistence seam and access
discipline), [ADR 0028](../../adr/0028-artifact-envelopes-and-payload-schemas.md)
(encoding), [ADR 0023](../../adr/0023-v2-branch-strategy.md) (Epic/Story
branching), [ADR 0024](../../adr/0024-intake-and-triage-artifact-contract.md)
(what intake produces).

**ADR 0032 handed five mechanisms back as design inputs rather than decisions**:
the execution FSM; restart, resume, re-attach and outstanding-action
enumeration; epochs, acknowledgements, watermarks and durable sender outboxes;
the generic question-wait lifecycle; and durable reusable approvals. This phase
is where they are settled — **against a real consumer, not on paper**. That is
the whole point of the demotion, and re-deriving them as a design document
before item 6 has a working agent would reproduce exactly what A4 got wrong.

## Scope

In scope:

- **The work hierarchy in the plane**: Features, Epics, Stories and Work Groups
  as records with non-null lineage, plus the **dispatch record and its dispatch
  basis** — the governing version set and the incoming dependency basis that
  ADR 0019's second amendment binds cancellation to.
- **The Orchestrator wired through the persistence seam** (ADR 0022 built the
  seam standing alone; this phase is where it acquires its caller), and
  **typed durable workflow checkpoints** replacing `StateStore.Save(id, any)`.
- **The agent execution boundary**: ADR 0030's three gates and ADR 0032's
  binding items, with the toolloop refactored behind them rather than rewritten.
- **A shared agent core**, extracted from v1's role machinery by evidence and
  wired to the real action boundary — the **first** consumer of the contract.
- **Two external-process consumers of the same boundary**: the real standalone
  code-review agent ([#282](https://github.com/SnapdragonPartners/maestro/issues/282)'s
  executable, doing actual review work rather than A4's deliberate stub) and the
  **Claude Code execution adapter**. ADR 0032's central claim is one contract for
  two runtime kinds; one consumer cannot test it.
- **Story-scoped Incubators and leased Habitats**, with generations, the fencing
  protocol, and reset-before-evidence.
- **Prompt pack resolution at dispatch**, with the minimal installation record
  ([backlog candidate 9](../notes_adr-backlog.md), narrowed into this phase).
- **A single Work Group lifecycle** and the Epic-level plan workflow.
- **Feature intake, contract-only**: a minimal manual path honoring ADR 0024's
  artifact contract. It must not preempt the pre-Phase-5 intake spike.
- **An Epic dashboard skeleton** showing live state for one Epic.
- **V1 retirement**: the v1 factory path and every `drop` disposition in the
  [port inventory](../phase_0/inventory_v1-port.md), which is
  [#298](https://github.com/SnapdragonPartners/maestro/issues/298)'s five
  deletions, plus `pkg/persistence`, `dataplanectl`'s fold-in
  ([#287](https://github.com/SnapdragonPartners/maestro/issues/287)), and the
  `ProviderPatterns` routing replacement
  ([#272](https://github.com/SnapdragonPartners/maestro/issues/272)'s
  implementation half).
- **A v2 benchmark target adapter**, built and proven **before** any deletion.

Out of scope:

- **Any further v1 patching to keep the measuring instrument running.** DR,
  2026-08-16 — see the amendment below. v1 is being deleted; patching it to
  benchmark it is work with no surviving consumer.
- The intake **design** — form, triage agent, provisional Work Groups. That is
  the pre-Phase-5 spike, and the roadmap says so explicitly.
- **GitHub Actions annotation presentation** for the standalone reviewer, and
  blocking findings producing a failing workflow status. The blocker plan's
  scope decision 1 puts it here or later precisely so it cannot hold anything
  hostage; the reviewer itself is in scope, its presentation layer is not.
- Branch hierarchy beyond what a Story execution needs, evidence packages, and
  the evidence viewer (Phase 4).
- Gates policy content ([candidate 12](../notes_adr-backlog.md)), the knowledge
  base ([candidate 10](../notes_adr-backlog.md)), skills and patterns
  (Phase 5/6).
- Multiple concurrent Work Groups and a standing intake agent (roadmap MVP
  constraint).
- The Workbench (Phase 5) — but see the tempo-neutrality constraint below, which
  is in scope.
- Cloud mode (Phase 7). Track B's portability proof is authored in parallel and
  **gates** item 3; it is not built here.
- Remote and socket transports for the execution contract
  ([candidate 14](../notes_adr-backlog.md)); the provenance retention
  traversal's mechanism (Phase 4); composite and paired execution's runtime
  (Phase 5).

**Tempo-neutrality is a constraint, not an output.** Nothing in the Work Group
lifecycle, gate wiring, or workspace model may assume leading gates only. The
Workbench tempo must remain expressible as a harness preset rather than a
parallel system, and a design that forecloses it is a defect found in Phase 5 at
five times the cost.

## The Regression Obligation, Amended

**Phase 2 carried an unmet regression checkpoint into this phase**, and the
roadmap made it a gate on v1 retirement: fix
[#317](https://github.com/SnapdragonPartners/maestro/issues/317), then run
`golden-all` at N = 1 against the **v1-as-patched** target while v1 still
exists.

**Overridden by DR, 2026-08-16: that run does not happen.** #317 and
[#316](https://github.com/SnapdragonPartners/maestro/issues/316) are defects in
the v1 architect path and the v1 adapter, and this phase deletes both. Patching
a target in order to measure it immediately before removing it produces no
artifact that outlives the phase.

Three consequences, recorded rather than absorbed:

1. **There will never be a measured v1-to-v2 comparison point.** Phase 2 lost
   its own regression run; this makes the loss permanent. That is the price of
   the override and it is not recoverable later.
2. **The phase-end run is a baseline, not a regression check.** With no prior
   measurement on the v2 path, `golden-all` against the v2 adapter establishes
   the first one. It still proves something worth proving — that the v2 path
   completes the suite at all, on stories with behavioural oracles — but the
   plan must not inherit Phase 2's "regression" framing for it. From Phase 4
   onward it becomes a regression test in the ordinary sense.
3. **ADR 0025 is not amended and neither is `process_build.md`.** The roadmap's
   warning that "relaxing any of this is an amendment to ADR 0025" attaches to
   the *ordering* — adapter before deletion, conformance run after it — which
   this plan keeps intact. The phase-end cadence run still happens. What changes
   is one roadmap exit criterion, amended in this branch.

**What survives from Track C**, retargeted at the v2 path:
[#318](https://github.com/SnapdragonPartners/maestro/issues/318) (dirty-tree
preflight) and [#319](https://github.com/SnapdragonPartners/maestro/issues/319)
(model-lifecycle preflight) are runner-side and still required — #319 is the
check that would have caught the Opus 4.1 retirement seven weeks early.
[#323](https://github.com/SnapdragonPartners/maestro/issues/323)'s question —
whether a viable architect model exists — survives and retargets to the **v2**
architect seat, where it must be answered before item 14's paid run. #316 and
#317 close with v1, and each leaves a **requirement** behind rather than a fix:
a terminal tool must be forceable, and sampling parameters must be optional.

## Decisions This Plan Fixes

Proposed here, ratified by this plan's approval.

1. **Classify v1 by evidence; preserve or replace nothing wholesale.** Item 1
   produces the inventory and the disposition for each surface. The direction
   carried out of A4's scope correction stands as the starting hypothesis:
   retain and refactor the role state machines and transition tables; **replace
   `StateStore.Save(id string, value any)`** with typed durable checkpoints;
   retain `QUESTION` and map it to artifacts and Story state; **retire or
   redesign `SUSPEND`**, whose return-to-origin resumption over a process-local
   channel conflicts with artifact-level restart; refactor the toolloop behind
   the boundary rather than rewriting it; split `pkg/proto`, keeping the useful
   domain types and replacing its process-local messaging as the external
   boundary; replace the Claude MCP `lastEffect` and signal-correction path with
   a real adapter over the contract (item 8, not a side effect of item 5); and replace process-local dispatcher and supervisor authority as
   scheduling moves into the Orchestrator. **`StateStore` is the one already
   decided** — `json.MarshalIndent` plus `os.WriteFile` with no temporary file,
   rename, or fsync is not a durable checkpoint.
2. **Restart is artifact-level, and it is a rule before it is a mechanism.** An
   agent restarts from the last committed workflow artifact, never from an
   arbitrary instruction. Item 3 establishes it; every later item is built
   against it. This is what makes ADR 0032's demoted restart machinery
   answerable by a consumer instead of by speculation.
3. **The demoted mechanisms are settled by the first consumer that needs them,
   and not before.** Restart, reliable delivery, remote transport and richer
   approval persistence are added when item 6, **item 8** or item 13 demonstrates the
   requirement — which is A4's seventh step, and the check that keeps this phase
   from rebuilding the spike.
4. **One state vocabulary with a stated mapping**
   ([#330](https://github.com/SnapdragonPartners/maestro/issues/330)). The
   agent's role FSM and the Orchestrator's action record answer different
   questions and both survive, but `proto.StateQuestion` and any plane-side
   response wait must be one concept with one mapping, decided in item 6 rather
   than allowed to drift into two.
5. **The `tool_calls` migration replaces `tool_calls_finished_check`.** ADR 0030
   §8 as amended and ADR 0032 both record this; it lands in item 2 so no later
   item discovers it against a populated table.

## Deliverables And PR Sequence

One short-lived branch per item (`v2/phase_3/<suffix>`), one open at a time, per
the [build process](../process_build.md). New ADR needs discovered mid-phase go
to the [backlog](../notes_adr-backlog.md), not into the phase.

Four blocks, with a **checkpoint** closing each. A checkpoint is a review
against a demonstrated capability, not a document: it either runs or the block
is not done.

### Block A — Foundations

| # | Branch suffix | Deliverable | Size |
|---|---|---|---|
| 0 | `scope-and-plan` | This document, Accepted. Includes the roadmap amendment above. | S |
| 1 | `agent-inventory` | **Inventory and classify** the existing agent, toolloop, `pkg/proto`, supervisor, dispatcher and Claude adapter surfaces, against the frozen v1 tree, with a disposition per surface: retain, refactor, replace, retire. Authoring work, no code. Reconciles the [port inventory](../phase_0/inventory_v1-port.md) against the real import graph, which is what #298's deletions need in order to be complete rather than approximate. | M |
| 2 | `schema-work-hierarchy` | The work-hierarchy schema families: work groups, runs, executions, and **dispatch records carrying their dispatch basis** — the governing version set and the incoming dependency basis. Includes the **`tool_calls` migration that replaces `tool_calls_finished_check`** and adds the nonterminal states ADR 0030 §8 requires, so a healthy operator wait, a healthy resource wait and an interrupted attempt are distinguishable. Every table traces to an Accepted ADR and a Phase 3 consumer, as in Phase 2. | M |
| 3 | `orchestrator-seam` | **The data plane acquires its caller.** Phase 2 built the seam and its local modules standing alone; this is where the Orchestrator routes through them. Five parts: (a) agent lifecycle, dispatch, artifact and call writes go through the seam; (b) the **durable-checkpoint rule** — typed workflow checkpoints replacing `StateStore.Save(id, any)`, and restart from the last committed artifact; (c) **configuration and secrets acquire their first consumer** — config read through the registry, the vault unlocked by the key-file root of trust at startup, including the locked-plane failure path Phase 2 tested and nothing yet exercised; (d) a **defined startup contract for a plane that is not ready** — absent, unmigrated, locked, or carrying **Phase 2** item 8's interrupted-recovery marker are four distinct states with four behaviours, not one crash; (e) **organization, product and repository provisioning** as the real entry point — its **prompt-pack half is item 4's**, which is why packs move into this block rather than sitting behind the execution boundary. Deletes `pkg/persistence`, which Phase 2 deferred here by design. **Gated by Track B's portability proof** ([#286](https://github.com/SnapdragonPartners/maestro/issues/286)), authored in parallel. | L |
| 4 | `prompt-packs` | ADR 0031: immutable content records beside mutable installation records, scheme-qualified content identity, resolution once at dispatch, and **organization provisioning seeding the scoped selector — which completes item 3's provisioning rather than amending it later**. Carries `principal_instances.prompt_pack_id`'s three-roles-in-one-column correction and the `"v1-embedded"` foreign-pack case the benchmark importer writes. Placed in block A because packs are plane storage and dispatch-time resolution; nothing here needs the execution boundary. | M |

> **Checkpoint 1 — the plane holds the work.** An Epic and its Stories are
> created, dispatched, and durably checkpointed through the seam; the
> Orchestrator recovers its own state across a restart; a fresh organization is
> provisioned with a resolvable prompt-pack selector; and startup is correct
> against **all four** not-ready states — absent, unmigrated, locked, and
> **carrying an interrupted-recovery marker**, where normal startup must neither
> bypass nor corrupt the recovery protocol. No agent has run.
> Reviewed before block B opens, because everything below persists through it.

### Block B — Execution

| # | Branch suffix | Deliverable | Size |
|---|---|---|---|
| 5 | `execution-boundary` | ADR 0030's three gates and ADR 0032's binding boundary items: mediated actions with durable intent and result records, capability-scoped tools, the four-axis terminal result **with its applicability rule**, fenced resource references, and rejection of superseded or fenced execution authority at every mediated boundary. The toolloop is refactored behind it, and the MCP `lastEffect` and signal-correction path is **removed** — the Claude Code adapter that replaces it is item 8's, not this item's. | L |
| 6 | `agent-core` | The smallest shared agent core, extracted and refactored from v1's role machinery per item 1's dispositions, wired to item 5's boundary as its **first real consumer**. Settles #330's vocabulary mapping, `QUESTION`'s artifact mapping, and `SUSPEND`'s fate. Whichever of ADR 0032's demoted mechanisms this consumer actually demonstrates a need for is built here; the rest stay unbuilt. | L |
| 7 | `incubator-habitat` | ADR 0029: Incubator provisioning and Story-scoped lifecycle; Habitat instance, generation-bound lease and retention claim; the fencing protocol returning `terminated`, `isolated` or `unconfirmed`, with quarantine and the independent cleanup axis; and reset on ownership transfer. **Three decisions the [execution-contract notes](notes_execution-contracts.md) assign to this plan and which have no other owner:** (a) **routing is by declared requirement, never by contract name** — a `test` with no dependencies routes to the Incubator and the same `test` needing a database routes to the Habitat, so a label-based implementation satisfies a loose checkpoint while violating ADR 0029; (b) **both run kinds exist and behave differently** — an *iteration* run redeploys into the existing instance and keeps accumulated state, an *evidence-bearing* run resets first, and collapsing them silently destroys the reset guarantee; (c) the **verb inventory is pruned**, including whether `integration` is a distinct verb or the requirement-routed form of `test`. Tools target a resource reference. | L |
| 8 | `external-consumers` | **The contract's second and third consumers, both external processes.** (a) The **real standalone reviewer** — an external-process code-review agent that speaks the wire contract, does actual review work, and publishes its findings as artifacts. This is [#282](https://github.com/SnapdragonPartners/maestro/issues/282)'s executable, and it is what A4's conformance slice explicitly was **not**: that slice's backend was a stub proving shape. (b) The **Claude Code execution adapter**, replacing the `lastEffect` and signal-correction path item 5 removed with a real adapter over the contract. GitHub Actions annotation presentation stays deferred. Both consume item 5's boundary and item 7's resources unchanged — **if either needs the boundary widened, that is the finding**, and it is how ADR 0032's demoted mechanisms acquire evidence instead of speculation. | L |
| 9 | `dispatch-cancellation` | Dispatch against the basis, and ADR 0019's second amendment implemented: authority superseded, admission closed **linearizing with the basis becoming authoritative**, drain to one of ADR 0030 §5's three dispositions, fence, release, record `cancelled`/`superseded`. Watchdog policy for the waits. Includes [#265](https://github.com/SnapdragonPartners/maestro/issues/265)'s single-owner restart, which removes the dual death-observer shape. | L |

> **Checkpoint 2 — one Story executes, and the boundary has two kinds of
> consumer.** A single Story runs end to end on a fixture repo: mediated actions
> recorded, artifacts emitted with correct provenance, an Incubator provisioned
> and fenced, and a cancellation demonstrated mid-flight. **Both a native
> in-process agent and an external-process agent drive the same boundary** — a
> checkpoint the native agent alone could pass would leave ADR 0032's central
> claim, one contract for two runtime kinds, untested exactly where it is
> load-bearing. This is the first point at which the
> contract has a consumer, and therefore the first point at which the demoted
> mechanisms can be judged.

### Block C — Surface

| # | Branch suffix | Deliverable | Size |
|---|---|---|---|
| 10 | `work-group-lifecycle` | The single Work Group lifecycle end to end, and the Epic-level plan workflow: decomposition authored by the Architect, reviewed under ADR 0020, dispatched by the Orchestrator. **Tempo-neutral by construction** — no leading-gate assumption anywhere in the lifecycle. | L |
| 11 | `intake-contract` | Feature intake, **contract-only**: a minimal manual path producing ADR 0024's artifacts with provenance. No form, no triage agent, no provisional Work Groups — those are the pre-Phase-5 spike's, and preempting them here is the failure this item is scoped against. | M |
| 12 | `dashboard-skeleton` | The Epic dashboard skeleton: live state for one Epic — Stories, their executions, and their artifacts — read through the seam. Skeleton means legible, not designed. | M |

> **Checkpoint 3 — one Epic, intake to merge.** One Epic goes from intake
> through Story execution to merged Story branches on a fixture repo, driven by
> a single Work Group, with the dashboard showing it live. This is the roadmap's
> first exit criterion, demonstrated before the retirement block starts
> deleting things.

### Block D — Retirement

| # | Branch suffix | Deliverable | Size |
|---|---|---|---|
| 13 | `v2-target-adapter` | The **v2 benchmark target adapter**, so the runner has a target that is not v1. Carries #318's dirty-tree preflight and #319's model-lifecycle preflight into the Run Protocol, and answers #323's architect-seat question against the v2 loop. Built and proven **before** anything is deleted — this ordering is the roadmap's and is not this plan's to relax. | L |
| 14 | `v1-retirement` | Delete the v1 factory path and every `drop` disposition: #298's five deletions, now complete rather than approximate because of item 1; `ProviderPatterns` replaced by explicit provider/model/endpoint declaration (#272's implementation half); `dataplanectl` folded into the main binary with embedded compose assets (#287). No v1 factory entrypoint remains; build, test and integration verification pass on the v2 path alone. | L |
| 15 | `phase-exit` | The phase-end conformance runs against the **v2 adapter**, both required by [ADR 0025](../../adr/0025-golden-stories-and-benchmark-runner.md)'s cadence table: **`golden-minimal` at N = 1** — the harness-is-alive smoke check it requires at the end of *every* phase, a few dollars — and **`golden-all` at N = 1, `paired-default`**, the ~$50 conformance run required from Phase 2 onward. Both are the first measurements on the v2 path. Each needs DR's explicit approval for that specific run; phase or plan approval is not reusable. Exit review against the checklist below; backlog reconciliation; this document flips to `archive`. | M |

> **Checkpoint 4 — the instrument survives the transition.** Item 13's adapter
> is proven **by contract and integration tests against the v2 path, with no
> hosted-model spend**, before item 14 deletes anything: the runner's adapter
> contract exercised end to end, including a story reaching a terminal verdict.
> **Not a golden run** — the paid conformance runs stay after deletion, in item
> 15, where the roadmap puts them. If the adapter cannot pass this, the deletion
> does not start; that is the whole reason it precedes it.

### Sequencing notes

- **Item 3 is where the data plane stops being a library.** Everything from
  checkpoint 1 onward assumes a running plane, which `make dataplane-up` already
  provides;
  [#287](https://github.com/SnapdragonPartners/maestro/issues/287)'s fold-in of
  `dataplanectl` is single-binary consolidation rather than a prerequisite, and
  stays in item 14 with the `cmd/maestro` rework it belongs to.
- **Items 1 → 2 → 3 → 4 are a strict chain**, and so are 5 → 6. Item 7 depends
  on 5; item 8 depends on 5, 6 and 7; item 9 depends on 2, 5 and 7. Block C
  depends on all of block B. Block D depends on block C, and 13 → 14 → 15 is
  strict.
- **Prompt packs sit in block A, not beside the execution boundary.** ADR 0031
  makes organization provisioning seed the pack selector, so leaving them until
  block B would have merged item 3's provisioning in a state ADR 0031 does not
  permit and amended it afterwards. Packs are plane storage plus dispatch-time
  resolution; nothing in them needs the boundary.
- **Item 1 is the designated slack**, as item 1 was in Phase 2 and story
  authoring was in Phase 1: authoring work, reviewable independently, blocking
  only item 2, so it can absorb a stalled review without violating the
  one-branch rule.
- **Item 6 is the phase's main design risk.** It is where five demoted
  mechanisms either find a consumer or stay unbuilt, and where two state
  vocabularies either reconcile or fork permanently. It is deliberately placed
  after the boundary exists and before anything is built on top of it.
- **Track D's runway is scheduled against the work it gates**, not against phase
  entry: [#306](https://github.com/SnapdragonPartners/maestro/issues/306) (the
  pre-push gate passes on cached results) is worth taking **before block B**,
  because from there on this phase's verification claims depend on that gate
  actually running. Then #314, #321, and #307 before the concurrency work; #308
  whenever `make build` is on the critical path.
- **Testing rule for this phase**: the agent core, boundary and lifecycle are
  tested against a real ephemeral plane, as Phase 2 established; resource
  fencing is tested against a live provider, as the
  [Docker fencing spike](spike_docker-fencing.md) established. A mock of the
  thing under test proves nothing about it.
- **Defect-Shaped Verification** (`process_build.md`) applies to every guard and
  regression test in this phase: a mutation must falsify the named claim for the
  named reason.

## Exit Checklist

### From the roadmap

- [ ] One Epic goes from intake through Story execution to merged Story branches,
      driven by a single Work Group, on a fixture repo. *(Checkpoint 3.)*
- [ ] Every step emits artifacts to the data plane with correct provenance —
      principal instance, Epic, Story. *(Items 3, 5, 6, 10.)*
- [ ] The Epic dashboard shows live state for that Epic. *(Item 12.)*
- [ ] No v1 factory entrypoint remains, every `drop` disposition is complete, and
      the v2 path passes build, test and integration verification. *(Items 1
      and 14.)*
- [ ] Before the v1 factory path is removed, a **v2 target adapter** exists and
      is proven by contract and integration tests without hosted-model spend;
      after removal, **both** runs ADR 0025's cadence requires complete against
      it — **`golden-minimal` at N = 1**, required at the end of every phase, and
      **`golden-all` at N = 1** against `paired-default`. *(Items 13 and 15;
      checkpoint 4.)*
- [x] ~~Carried from Phase 2: fix #317, then run `golden-all` against
      v1-as-patched before v1 is removed.~~ **Struck by DR, 2026-08-16** — see
      the amendment above. Recorded struck rather than deleted, so the lost
      comparison point stays visible.

### From the Track A ADRs

- [ ] No tool reaches its effect around the mediated boundary, and this is
      **demonstrable** rather than asserted (ADR 0030's own standard). *(Item 5.)*
- [ ] A positive fence receipt is never issued while an admitted action can still
      commit, and an `unconfirmed` receipt quarantines rather than being rounded
      up. *(Items 7 and 9.)*
- [ ] Agents hold no data-plane connection and issue no queries; every artifact,
      decision and state transition passes through an action record. *(Items 3, 5, 6.)*
- [ ] Prompt pack identity is resolved once at dispatch and reused verbatim
      across restarts. *(Item 4.)*
- [ ] Cancellation on a changed dispatch basis is demonstrated end to end,
      including the `unconfirmed` path that leaves it unresolved. *(Item 9;
      checkpoint 2.)*

### From this plan

- [ ] Every table in the Phase 3 migrations traces to an Accepted ADR **and** a
      Phase 3 consumer, or carries a written justification. *(Phase 2's rule,
      carried forward.)*
- [ ] `StateStore.Save(id, any)` is gone, and no workflow state persists through
      a non-atomic write. *(Item 3.)*
- [ ] **`paths.Bootstrap` is not imported from above the seam.** Phase 2's exit
      record makes this a rule and predicts its violation here by name — "the
      pressure to import the concrete struct from above the seam will be real in
      Phase 3, and that is precisely how a local-only assumption hardens into
      architecture." Checkable by import graph, so it is checked rather than
      trusted. *(Item 3.)*
- [ ] Configuration and secrets have a live consumer, and the locked-plane path
      is exercised by the Orchestrator rather than only by its own tests.
      *(Item 3.)*
- [ ] Startup is defined and demonstrated for **all four** not-ready plane
      states, including **interrupted recovery**, where normal startup must
      neither bypass nor corrupt Phase 2's recovery protocol — the state whose
      mishandling destroys a staged key. *(Item 3, checkpoint 1.)*
- [ ] **Execution-resource routing is by declared requirement, never by contract
      name**, and **both run kinds exist with different behaviour** — an
      iteration run redeploys into the existing instance and keeps accumulated
      state, an evidence-bearing run resets first. Both demonstrated, because a
      label-based router and a collapsed run kind each satisfy a loosely-worded
      checkpoint while violating ADR 0029. *(Item 7, checkpoint 2.)*
- [ ] Each of ADR 0032's five demoted mechanisms is either **built with a named
      consumer** or **recorded as still unbuilt**, and none is built because a
      document said so. *(Items 6 and 8 — the external consumers are where
      delivery, restart and re-attach acquire evidence, since those are the
      mechanisms that only bite across a process boundary.)*
- [ ] **The boundary has consumers of both runtime kinds**: the native agent, the
      standalone external-process reviewer, and the Claude Code adapter, all
      through the same contract. A4's carry-forward requires all three and its
      conformance slice deliberately supplied none of them — its backend was a
      stub proving shape. *(Items 6 and 8, checkpoint 2.)*
- [ ] The state-vocabulary reconciliation (#330) is decided, not deferred again.
      *(Item 6.)*
- [ ] The Work Group runtime is tempo-neutral: no lifecycle, gate, or workspace
      decision assumes leading gates. *(Item 10, checked at review.)*
- [ ] #298's five deletions are complete and the issue is closed against this
      plan rather than against a promise. *(Items 1 and 14.)*
- [ ] ADR needs discovered in-phase are filed in the backlog, and the
      Phase-4-blocking entries are confirmed open or resolved. *(Item 15.)*

## Risks

- **The phase is large enough to lose its shape.** Sixteen items and four
  blocks, with a deletion at the end that cannot be partially done. The
  checkpoints exist for this: each is a demonstrated capability, and a block
  that cannot demonstrate its checkpoint has not finished regardless of how many
  of its items merged.
- **The measuring instrument is unavailable for most of the phase.** With the
  carried v1 run struck and the v2 adapter arriving at item 13, there is no
  end-to-end conformance signal from item 0 to item 12 — the longest such gap in
  the project. Mitigation is the checkpoints, which are capability
  demonstrations rather than test-suite passes, and the fixture-repo Story at
  checkpoint 2.
- **The external consumers get cut under schedule pressure.** They are the
  expensive half of item 8 and the easiest to defer, and deferring them leaves
  the boundary tested only where it is cheapest to satisfy. The guard is
  checkpoint 2, which does not pass on a native agent alone.
- **Item 6 rebuilds the spike.** The failure A4 was corrected for is
  specifying mechanism with no consumer to refuse it. The guard is the exit
  checklist item above: a demoted mechanism is built with a *named* consumer or
  recorded as unbuilt.
- **The deletion strands something.** #298's dispositions were approximate when
  filed, which is why item 1 reconciles them against the real import graph
  before item 13 acts on them. The risk is a dependency retained only by deleted
  code, or a live caller of a package the inventory marked `drop`.
- **Tempo-neutrality is violated invisibly.** A leading-gate assumption does not
  fail any test in this phase; it fails in Phase 5. Mitigation is a review
  check, which is weak — worth strengthening if item 9 finds a concrete case.
- **Review bottleneck** (standing risk since Phase 0). Serial PRs bound operator
  load; item 1 is the pressure-relief valve, and the block structure gives four
  natural places to pause.

## Reviewer Questions — Resolutions

Codex answered all four on 2026-08-16; DR confirmation rides on this document's
approval.

1. **Is sixteen items in one phase right, or should block D be a subphase
   (3a)?** **Resolved: keep one phase**, per DR's direction of 2026-08-16 and
   Codex's concurrence. Block D is cohesive and already has a clean checkpoint
   seam; splitting it now would add release mechanics without reducing technical
   scope.
2. **Should item 1 produce a document or a set of dispositions in the plan?**
   **Resolved: a document** (`inventory_v1-agent.md`), which gives retirement a
   durable, reviewable basis outliving this plan's archival — which is what
   #298's deletions need.
3. **Is Track B's proof a real gate on item 3, or advisory?** **Resolved: a real gate**, exactly as the accepted
   blocker plan states. Item 3 is on the critical path, so a stalled parallel
   authoring effort blocks it.
4. **Does striking the carried regression run need anything beyond the roadmap
   amendment?** **Resolved: no.** The carried v1 run is a roadmap debt
   rather than an ADR 0025 cadence requirement, and the accepted
   adapter-before-deletion and post-deletion conformance ordering is intact.

## Related Documents

- [Roadmap](../plan_roadmap.md): Phase 3 goal, outputs, MVP constraint and exit
  criteria; D7 (v1 break); pillar 17 (Workbench) for the tempo constraint.
- [Pre-Phase-3 blocker plan](plan_blockers.md): Track A's five decisions, the
  Track B/C/D scheduling, and the *Carried forward from A4's scope correction*
  section this plan absorbs.
- Track A: [ADR 0029](../../adr/0029-incubator-and-habitat-execution-boundaries.md),
  [ADR 0030](../../adr/0030-tool-execution-policy-hook.md),
  [ADR 0031](../../adr/0031-prompt-pack-identity-resolution-and-storage.md),
  [ADR 0032](../../adr/0032-agent-execution-contract.md),
  [ADR 0019](../../adr/0019-orchestrator-boundary.md) as twice amended.
- [Port inventory](../phase_0/inventory_v1-port.md) and
  [#298](https://github.com/SnapdragonPartners/maestro/issues/298).
- [Phase 2 plan](../phase_2/plan_scope.md) (`archive`) and
  [exit record](../phase_2/notes_exit-record.md), for the carried obligation
  this plan strikes.
- [Build process](../process_build.md): roles, one-branch rule, Defect-Shaped
  Verification, phase-end cadence.
- [Conformance log](../notes_conformance-log.md).
