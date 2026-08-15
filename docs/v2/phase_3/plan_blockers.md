+++
title = "Pre-Phase-3 Blockers: Scope And Sequencing"
edit_date = "2026-08-15"
status = "live"
summary = "What must be settled before Phase 3 implementation begins: five design decisions — four ADRs (Habitat with its fencing protocol, tool-execution policy hook, prompt-pack identity, agent execution contract) and an ADR 0019 amendment for amendment-vs-running-work — plus a parallel cloud-portability proof gating Orchestrator wiring, benchmark repair for the two runs Phase 3 owes, and the authority cleanup the ADR backlog needs before any of it can be Accepted."
type = "plan"
+++

# Pre-Phase-3 Blockers: Scope And Sequencing

Status: **live** — proposed by Claude 2026-08-09 and accepted the same day after
five review rounds: Codex approved `607e0ee` with no blocking findings, and DR
approved. DR's two interventions shaped it materially — scoping the tool
boundary to Maestro's own agents, and bounding the Habitat backend work.

Flipped to `live` in its own PR rather than after merge, following Phase 2's
recorded lesson that Phase 1's plan merged still `draft` and needed a follow-up
flip PR.

**Carried forward to the A1 ADR** (agreed at approval, deliberately *not* a
revision to this plan, which already implies it): an `isolated` receipt requires
dispatch into a **new Habitat generation** — the quarantined generation is never
reused.

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

- Design items produce an **Accepted ADR and nothing else**, with **one bounded
  exception**: A4's contract cannot be proven by inspection, so it carries a
  conformance executable. That executable lives **outside `pkg/`, `internal/`,
  and `cmd/`** for the pre-entry period, on the same footing as spike code under
  the [build process](../process_build.md). Where it lands permanently is a
  Phase 3 decision. No other pre-entry item ships code.
- The engineering runway and the benchmark repair are **not gates on the design
  track**. They gate specific Phase 3 activities — the paid runs, the
  concurrency work — and are sequenced against those, not against phase entry.

## The Shape Of The Work

Four tracks, not one chain. Track A is **five design decisions** — four new ADRs
and one amendment to [ADR 0019](../../adr/0019-orchestrator-boundary.md).

```text
Track A — design (critical path for phase entry)

    A1 Habitat ──────────────────── Accepted first ──┐
    A2 tool execution policy hook ───────────────────┤
    A3 prompt pack identity ─────────────────────────┼─→ A4 agent execution contract
                                                     │      (absorbs #272's contract portion)
                                                     │                 │
                                       A1 fencing protocol ────────────┴─→ A5 amendment vs running work
                                                                             (ADR 0019 amendment)
                                                                                    │
                                                                                    ▼
                                                                          A6 accept Phase 3 scope

Track B — cloud portability proof (#286)     parallel authoring; gates Orchestrator/persistence wiring
Track C — benchmark repair (#317, probe, #318, #319, #316)          gates the two paid runs
Track D — engineering runway (#314, #321, #306, #307, #308)         gates the concurrency work
```

A1, A2 and A3 are drafted concurrently and reviewed as a set for consistency.
**A1 is Accepted before A4**, because A4 consumes an accepted Habitat contract;
#282 blocks #273's *implementation* completion, not its design ADR.

Only Track A gates phase entry. B, C and D gate specific things inside Phase 3.

---

## Track A — Design

### A1. Habitat execution boundary — [#273](https://github.com/SnapdragonPartners/maestro/issues/273), design portion only

> **RESOLVED 2026-08-12 by [ADR 0029: Incubator And Habitat Execution Boundaries](../../adr/0029-incubator-and-habitat-execution-boundaries.md)** (Codex + DR), with both spike artifacts: the [Docker fencing reproducer](spike_docker-fencing.md) and the [Kubernetes partition walkthrough](spike_kubernetes-partition.md). Four things below are superseded by it and are left in place as the record of what was asked rather than what was decided:
>
> 1. **One resource became two.** Every *concrete* requirement this item states under the name `Habitat` — the tool-routing rule, read-only Architect inspection, removal of Coder workspace bind-mounts, the live socket escape, the Docker/Compose gating row — describes what the ADR calls the **Incubator**. Dependent-service lifecycle is the **Habitat's**. Identity, generation, and fencing belong to both.
> 2. **The four remedies for the socket escape are down to two.** "Mediate it through the Orchestrator" and "use a constrained proxy" were falsified by the reproducer: filtering a general-purpose daemon API is not capability-closed, and "include every created container in the fencing domain" is not an action available at fencing time. What remains: **no daemon route**, or **a daemon owning only the domain** — both closed by construction.
> 3. **The `isolated` row's means clause is too narrow.** "Its capabilities are revoked" is one of two ways the property holds and, read strictly, approaches termination. The settled rule: every path into state current or future work will touch is *either* closed by the authority enforcing it *or* leads to a permanently abandoned target. The row's statement of the *property* — "cannot mutate state reachable by any current or future generation" — was right all along.
> 4. **Fencing gained a cleanup obligation.** Fence state and cleanup state are independent axes; every resource not confirmed deallocated stays visible, reconciled, and flagged as potentially billable regardless of its receipt.

Establish Habitat as the Orchestrator-managed execution-resource boundary:
identity, `HabitatSpec` versus mutable `HabitatInstance`, generation/fencing so
a recycled Habitat cannot satisfy a stale reference, lifecycle
(provision → ready → lease → release → reconcile → destroy), **revocation and
the fencing protocol below** (A5 depends on it directly), agent-to-Habitat
cardinality including read-only Architect inspection, restart and reconciliation
expectations, and the rule that **Maestro tools target a Habitat reference,
never an Agent-derived local path**.

Why pre-entry: Phase 3 cuts `pkg/workspace`, `pkg/exec`, container state, Coder
setup, and Architect inspection of Coder workspaces. Establishing the boundary
after Phase 3 cuts all of them twice. It also permanently removes the need for
the Architect to bind-mount Coder workspace roots, which is what makes inode
preservation a cross-agent contract today (CLAUDE.md's bind-mounted workspace
invariant, ADR 0027).

Done: an Accepted ADR covering the list above. **Explicitly out**: the local
Docker provider implementation, the persistence migration, and speculative
warming policy — all Phase 3 items.

**Name: `Habitat` is accepted** (Codex, 2026-08-09) — more precise than
`Workspace`, less misleading than `Sandbox`, `Environment`, or `Runtime`. It
will appear in table names; the ADR fixes it rather than leaving it provisional.

#### Revocation does not stop execution — the fencing protocol

Revoking a lease invalidates **authorization**. It does not stop a process that
is already running inside the Habitat: that process keeps editing files and can
keep spawning children, and it needs no further authorization to do either. An
earlier draft of this plan rested A5 on revocation alone, which would have left
exactly the gap it was written to close.

A1 must therefore specify a **fencing protocol**, not merely a revocation verb.
Five requirements:

1. **Cooperative cancellation with a bounded grace period.** The lease holder is
   asked to stop and given a defined window to reach a safe boundary.
2. **Provider-enforced fencing of the whole domain** once the grace period
   expires, proving **non-interference** — see the domain model below. A provider
   that cannot produce a positive receipt is not conformant.
3. **Quarantine the Habitat when fencing cannot be confirmed.** Not knowing
   whether the old generation can still act is the dangerous case, and it must
   have a defined resting state rather than an assumption.
4. **No reassignment and no fresh dispatch into that Habitat until fencing is
   acknowledged.** A Habitat with an unconfirmed occupant is not a free resource.
5. **Generation fencing for subsequent mediated calls**, so a call issued by a
   fenced holder is rejected at the boundary even if it arrives late.

#### The fencing unit is a provider domain, not a process tree

A second correction, from the same review that produced the protocol above: the
first version of requirement 2 said "the lease-holding process **and its
descendants**." **Process ancestry is not a portable containment boundary**, and
building on it would have made the protocol unimplementable on every backend
that matters:

The provider contract therefore defines:

- An immutable **`FencingDomainID` and generation**, created *before* execution
  starts.
- A guarantee that **every process or subordinate resource able to mutate the
  Habitat is contained in that domain**.
- **`Fence()` returns a `FenceReceipt`** — see the three-valued result below.
  Best-effort success is forbidden; "we tried" is not a receipt.
- **Explicit collateral semantics:** if one lease cannot be isolated, fencing
  covers every lease sharing that domain. Say so rather than discovering it.
- **Quarantine and no reuse** whenever confirmation is unavailable.

#### The receipt proves non-interference, not death

A third correction, and the one the wider backend question actually earned.
Requiring every provider to confirm *termination* would exclude valid remote and
native providers for no safety gain — what the system needs is not that the old
generation died, but that it **cannot affect state reachable by current or
future work**. A `FenceReceipt` is therefore three-valued:

| Receipt | Meaning | Terminal? |
| --- | --- | --- |
| `terminated` | The execution domain is confirmed stopped. | Yes |
| `isolated` | The old generation is permanently quarantined, its capabilities are revoked, and it cannot mutate state reachable by any current or future generation — even if cleanup continues asynchronously. | Yes |
| `unconfirmed` | Neither could be established. | **No** — quarantine, no reuse, no dispatch. |

**Docker/Compose uses the `terminated` path**, and it is the only path Phase 3
implements. `isolated` exists so that a future partitioned Kubernetes, macOS, or
Terraform-provisioned backend has a conformant answer that does not require
synchronous confirmation of death.

#### Compatibility matrix — examples, explicitly non-gating

Recorded so the contract can be sanity-checked against shapes it will eventually
meet. **Only the Docker/Compose row gates A1.** Nothing here authorizes backend
research or a second provider implementation in Phase 3.

| Backend | Plausible fencing unit | Likely receipt | Gating? |
| --- | --- | --- | --- |
| **Docker / Compose** | The container, or the Habitat's complete Compose project | `terminated` — stop with a grace period, force-kill, then **wait for every recorded container to be non-running**. Never walk descendants. | **Yes — the only gating row** |
| Kubernetes | The Pod, or all Pods and resources in the Habitat's namespace | `terminated` on kubelet-confirmed terminal Pods; force deletion is *not* confirmation. A partitioned node or uncertain volume attachment yields `isolated` or `unconfirmed`, never `terminated`. | No |
| Linux filesystem execution | A cgroup v2 domain — **not** the directory | `terminated` via `cgroup.kill`, confirmed by `cgroup.events populated` = 0. **chroot-only cannot conform**: it changes pathname resolution, gives no process isolation, and so proves neither termination nor non-interference. | No |
| macOS native | An OS-level isolation unit, to be determined | Likely `terminated`; the unit is an open question, not a Phase 3 one. | No |
| Terraform / OpenTofu | — | Likely **provisions the fencing substrate** rather than being the termination primitive. Fencing would be delegated to whatever it provisioned. | No |

#### A live escape proves this is not theoretical

Two verified facts about the current v1 executor, `pkg/exec/docker_long_running.go`:

- **The raw host Docker socket is mounted into every long-running container**,
  unconditionally — `pkg/exec/docker_long_running.go:243` appends
  `--volume /var/run/docker.sock:/var/run/docker.sock` for all containers, not
  just Claude Code mode. A holder can therefore create **sibling** containers
  outside its own PID tree, and stopping the original container does not fence
  them. Descendant-walking would have missed them by construction.
- **The stop path cannot back a receipt.** `StopContainer`
  (`pkg/exec/docker_long_running.go:356-380`) logs `docker stop` and
  `docker rm -f` failures at Error level, then *swallows both*, deletes the entry
  from `activeContainers`, unregisters it from the global registry, and returns
  `nil`. There is no `docker wait` and no non-running check. Worse than the
  missing confirmation: **the failure path destroys the evidence** — after a
  failed stop, Maestro no longer records that the container exists, so nothing
  downstream can reconcile it. That is exactly the "recorded containers" set a
  receipt would have to wait on.

This is frozen v1 code and is **not** a defect to fix in v1 (CLAUDE.md's v1
freeze). It is a requirement on the Phase 3 provider, which must remove that
access, mediate it through the Orchestrator, use a constrained proxy or private
daemon, or include every created container in the immutable fencing domain.

#### A1 spike requirement — deliberately bounded

**Two pieces of evidence, and no more.** An earlier version of this section asked
for proofs against three backends, which invites exactly the scope creep DR
flagged: the backend list is open-ended (Kubernetes, macOS, Terraform, and
others), and chasing it would turn a design ADR into a research project.

1. **One executable Docker/Compose reproducer.** Demonstrate the socket escape,
   and demonstrate that domain-based fencing catches the sibling container that
   descendant-walking misses.
2. **One bounded paper walkthrough of a materially different failure shape.** A
   Kubernetes node partition is the convenient choice — **no cluster required**
   — because it is the case where confirmed termination is unavailable and the
   `isolated` receipt has to carry the weight.

Everything in the compatibility matrix beyond those two is an example, not an
obligation. **No further backend research and no second provider implementation
is warranted in Phase 3.**

Confirmed fencing is the precondition for A5's terminal result, and the
containment guarantee A2 states is only as strong as this protocol makes it.

### A2. Tool execution policy hook — [backlog candidate 4](../notes_adr-backlog.md)

> **RESOLVED 2026-08-13 by [ADR 0030: The Tool Execution Boundary And Its Policy Hook](../../adr/0030-tool-execution-policy-hook.md)** (Codex + DR). Three things below are superseded by it and are left in place as the record of what was asked rather than what was decided:
>
> 1. **The escalation model is inverted.** This item's "no policy content" scope held, but its unstated assumption — that a gate needing a human returns an answer to the caller — did not. A human-required call is **one logically blocked call**: the Story enters `awaiting_resolution`, the caller performs no LLM turns, and one operator decision resolves one logical action. The ADR was drafted the other way, on the premise that blocking was too expensive to hold, and DR overturned that premise after six review rounds of correct fixes inside it. A blocked execution burns no tokens; the retained Incubator or Habitat is the only real cost, and it is the same either way.
> 2. **"Mediated versus in-resource" is two axes, not one split, and the effect site is three-valued.** Mediation is the request path; containment is the effect site — Orchestrator-side, in-resource, or **external**. This item's wording has no place for an agent runtime's own shell calling an outside service, which is neither the Orchestrator's nor inside the resource's own state and is bounded only by the grants made at provisioning.
> 3. **"in-Habitat" is "in-resource" throughout.** The wording below predates [ADR 0029](../../adr/0029-incubator-and-habitat-execution-boundaries.md) splitting the resource. The ungoverned-actions half is chiefly the **Incubator's**, and the split applies to both types.
>
> Two things this item asked for are settled as asked: the placement, and the honest statement of each mode's guarantee.

Where the mandatory per-action policy hook lives and what its interface is. No
policy content — the gating rules are [candidate 12](../notes_adr-backlog.md),
post-MVP.

Placement: one mandatory hook at the Orchestrator's central tool-execution
boundary, after principal/Habitat capability resolution and before the side
effect. Not in the toolloop, not in the dispatcher, not in individual tools, not
a separate policy service.

#### The two modes, and what each actually guarantees

The draft of this document proposed a scope invariant prohibiting external agent
runtimes from touching Maestro-managed resources directly. **That is withdrawn.**
It would have disabled Claude Code-style built-in editing unless every internal
tool were replaced with a mediated one (Codex), and it reaches for control over
agents that are not Maestro's to control — an engineer may legitimately run other
agents inside a Habitat, and the application under development may itself be an
agent (DR). Maestro's job is scoped to **the behavior of Maestro's own agents**.

The ADR must therefore distinguish two modes and state honestly what each
receives:

**Mediated actions** — anything crossing back into the Orchestrator: data-plane
writes, artifact publication, forge operations, Habitat lifecycle, retrieval.
These pass through the hook, are policed per action, and are recorded in the
Audit family. This is the chokepoint, and it applies to native Go agents and
adapted external agents alike, because both reach these resources only through
the contract (A4).

**In-Habitat actions** — what a process does inside a Habitat already granted to
it: file edits, running commands, an agent runtime's own internal tools. These
are **not** individually policed and Maestro makes no per-action claim about
them. The guarantee is **containment, decided at grant time**: the lease defines
the blast radius, and enforcement lives at the Habitat boundary rather than in a
per-action gate. When such a process must be *stopped* rather than merely
deauthorized, that is A1's fencing protocol — terminate, confirm, quarantine on
doubt — because revoking the lease removes authorization without removing the
running process.

Stating the second guarantee as narrowly as it really is, is the point. An agent
holding a lease can do anything the Habitat permits between mediated actions.
That is a property of the design, not a gap in it, and A5's cancellation
semantics are built on top of the honest version rather than the aspirational
one.

Done: an Accepted ADR fixing the placement, the interface, the mediated/in-Habitat
split with each mode's guarantee stated explicitly, and the relationship to
candidate 12.

Cheapest item here to decide and the most expensive to defer: Phase 3 builds the
tool plumbing, and a seam not chosen gets retrofitted into every tool.

### A3. Prompt pack identity, resolution, and storage — [backlog candidate 5](../notes_adr-backlog.md)

> **RESOLVED 2026-08-13 by [ADR 0031: Prompt Pack Identity, Resolution, And Storage](../../adr/0031-prompt-pack-identity-resolution-and-storage.md)** (Codex + DR). Three things below are narrower than what was decided, and are left in place as the record of what was asked:
>
> 1. **"`principal_instances.prompt_pack_id` becomes a real reference" is not what the debt needed.** That column is doing three jobs — naming a pack, identifying its content, and standing in for a plane reference — and its only writer records `"v1-embedded"`, a pack the plane will never own. A foreign key on every row would mean fabricating records for prompts the plane never held, or refusing the benchmark import. The reference is therefore **required at dispatch and absent only for imported foreign runs**.
> 2. **Storage is not one record.** Content is immutable and content-addressed; the mutable facts a pack cannot be resolved without — display name, declared version range, declared role coverage — live on a separate **installation** record, because they sit outside the digest. That re-cuts one line of [candidate 9](../notes_adr-backlog.md), which is amended in place.
> 3. **Identity is scheme-qualified.** An `sha256:` prefix names an algorithm, not a semantic scheme, so a v1-embedded identity and a v2 pack digest are never comparable. The Phase 3 migration backfills the scheme without rewriting a digest or touching [ADR 0025](../../adr/0025-golden-stories-and-benchmark-runner.md)'s run-record contract.
>
> Resolution-once-at-dispatch is settled exactly as asked, with the addition that a restarted agent reuses the resolved identity rather than re-resolving.

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

A3 has no dependency on A1 or A2 and is authored in parallel with them. It joins
the graph at A4, whose invocation schema carries pack identity.

### A4. Agent execution contract — [#282](https://github.com/SnapdragonPartners/maestro/issues/282)

> **RESOLVED 2026-08-15 by [ADR 0032: The Agent Execution Contract](../../adr/0032-agent-execution-contract.md)** (Codex + DR), with its conformance executable at `spikes/phase_3/executioncontract` and the [report](spike_execution-contract.md). Eight review rounds. Four things below are narrower than what was decided, and are left in place as the record of what was asked rather than what was settled:
>
> 1. **The identity split is three concepts, not two.** Scope decision 3 asks whether the served and underlying identities are one key. They are not — and neither is the **route** (provider, endpoint, model name), which is what the runtime needs to make the call and carries no comparison identity at all. Retirement keys to the served offering; lineage to the underlying model; the reference between them is nullable and its null is ADR 0020's existing `unclassified`.
> 2. **"The invocation" is two records.** This item speaks of one. An immutable, persisted **execution configuration** is reused verbatim across incarnations; per-incarnation **bindings** carry resource grants and generations, the epoch, the resume token, and inbound artifact references. Gate 3 replaces resources mid-execution and a resume token exists only on a restart, so a single immutable record was never available.
> 3. **The terminal result gained two rules the four axes do not state.** `rejected` is not an execution status — a reviewer that rejects has *completed*, and its judgment is the work product. And `blocked` is composed by the **Orchestrator**, not claimed by the agent, so `terminal` is *at most* one event per execution rather than exactly one.
> 4. **The conformance slice is larger than "one executable" implies.** Sixty claims and forty-five mutations, because each review round demanded evidence the previous one had not produced. Where it lands permanently remains a Phase 3 decision, and its `host/` half is implementation to be rewritten rather than contract to be kept.
>
> **It also amends an Accepted ADR.** [ADR 0030](../../adr/0030-tool-execution-policy-hook.md) §8 called the `tool_calls` migration "additive"; A4 established that it must also replace `tool_calls_finished_check`, since settling an attempt requires a boolean `succeeded` and §8's own `unknown` outcome is neither. That amendment landed with this acceptance.

The versioned wire contract: invocation, events, terminal result, lifecycle,
provenance, transport, and capability-based tool/knowledge access. Finalized
against A1 (Accepted), A2 and A3.

**Three scope decisions:**

**1 — Contract, not presentation.** The issue bundles the contract with a
standalone code-review agent producing GitHub Actions annotations. Only the
contract and one exercised vertical slice are pre-entry; Actions presentation
polish is Phase 3 or later and must not hold phase entry hostage.

**2 — The terminal result is a structured schema, not one enum.** Four
independently-discovered needs converge here, and flattening them into a single
status list would conflate axes that are not the same kind of fact:

| Axis | Values | Fed by |
| --- | --- | --- |
| Execution status | `completed`, `blocked`, `cancelled`, `timed_out`, `failed` | [#317](https://github.com/SnapdragonPartners/maestro/issues/317) — a headless escalation must terminate durably as `blocked` or `timed_out`, stopping requeues and stopping billing. Today it deadlocks into `ESCALATED`, which no headless run can answer. |
| Completion disposition | `changed`, `already_satisfied` | [#280](https://github.com/SnapdragonPartners/maestro/issues/280) — a Story whose work was already merged is a *completed* execution with a different disposition, not a distinct execution status. Today it is recorded as ordinary completion and reads as a false negative. |
| Cancellation reason | `superseded`, … | A5 — work cancelled because its version was amended terminates as superseded, not failed. |
| Failure class | retryable infrastructure, non-retryable agent | #282 as filed. |

`already_satisfied` is a work disposition; infrastructure failure is an
operational fact. Separate axes keep them from colliding.

**3 — A4 absorbs the contract portion of
[#272](https://github.com/SnapdragonPartners/maestro/issues/272).** Provider,
model and endpoint identity cannot be deferred past a wire contract that carries
model identity in its invocation and its provenance. #272 as filed settles
*routing* — `{provider, model, endpoint}` explicit rather than inferred from the
model name, replacing v1's `ProviderPatterns`. A4 must settle one thing beyond
that: whether the **served model identity** (a provider's offering, which is
what has a retirement date) and the **underlying model identity** (which is what
has a lineage) are the same key. Track C's #319 depends on that answer. The #272
*implementation* remains a Phase 3 item.

**Vertical slice (settled):** any real external-process executable driven over
the local transport is sufficient. It need not carry GitHub Actions
presentation. It **must** exercise the actual wire boundary, capability
handling, the event stream, cancellation, and the terminal result — an
in-process fake or an echo fixture does not discharge this.

Done: an Accepted contract ADR plus that executable. The #317 and #280 *code*
fixes are Phase 3 items; what is pre-entry is that the schema has a place for
them.

### A5. Amendment versus running work — [backlog candidate 3](../notes_adr-backlog.md), an **ADR 0019 amendment**

**A5 lands as an amendment to [ADR 0019](../../adr/0019-orchestrator-boundary.md),
not as a fifth ADR.** It completes a decision 0019 itself deferred (its 2026-07-14
dispatch amendment settled the pending case and explicitly left the in-flight
case open), it is about the same subject — dispatch and what the Orchestrator
may decide deterministically — and the mechanisms it relies on are owned
elsewhere: fencing by A1, cancellation lifecycle and terminal result by A4. What
is left for A5 is the policy layered over them, which is 0019's business.

Narrower than it reads. ADR 0019's dispatch amendment already settled the
*pending* case: invalidate version-bound dispatch records, re-evaluate the DAG
deterministically, reissue — no agent in the loop, and explicitly never by
draining in-flight channels. ADR 0021 settled the record side, ADR 0028 the
encoding. **Only the in-flight executor's fate is open.**

Proposed rule:

- Mark the old execution cancellation-requested.
- Allow its current atomic tool action to reach a safe boundary.
- Prohibit further actions, and prohibit acceptance against the superseded
  version.
- Retain its output as attributable draft/Audit history.
- **Fence the execution per A1's protocol** — cooperative cancellation, then
  provider-enforced fencing of the **domain**, quarantining the Habitat when
  `Fence()` returns `unconfirmed`. A `terminated` or `isolated` receipt both
  satisfy this; only `unconfirmed` blocks.
- **Only after fencing is confirmed**, terminate it as `cancelled` with reason
  `superseded`, never `failed`. A terminal result recorded while an unfenced
  process may still be writing is a false record, and downstream work would be
  dispatched against a Habitat that is not actually free.
- Recompute the DAG and dispatch against the new effective version — never into
  a Habitat whose fencing is unacknowledged.

**Enforcement rests on A1's fencing protocol and A4's cancellation lifecycle,
not on A2's hook.** The hook observes cancellation only at mediated actions; per
A2's statement of the in-Habitat guarantee, a runtime holding a lease keeps
acting between them, so building A5 on the hook alone would leave exactly the
gap A2 declines to close. But **revocation alone does not close it either** —
that was this plan's own error one round ago. Revocation invalidates
authorization; a process already running needs none. Closing it takes a positive
`FenceReceipt`: either the domain is confirmed stopped, or it is confirmed unable
to reach anything current or future work will touch.

Rejected alternatives:

- **Suspend/resume** — too much machinery for Phase 3.
- **Complete-then-reconcile** — rejected because it lets stale work keep
  progressing against a version already superseded, spending tokens and Habitat
  time on output that cannot be accepted. *Not* rejected on ADR 0019 grounds:
  reconciliation judgment could legitimately be routed to an agent, so the
  boundary is not what rules it out.

Depends on A1 and A4; therefore last in Track A.

### A6. Accept the Phase 3 scope and plan

Phase entry proper. Written against A1–A5 as Accepted, and carrying the Track
B/C/D items as scheduled Phase 3 work with their gates named.

---

## Required Authority Cleanup

Two conflicts had to be reconciled *before* A1 and A4 could be Accepted, because
they left two competing abstractions on the books. **Both were discharged on
2026-08-09**; each is recorded below as it was found, with its resolution.

**One step remains, and it waits on the ADRs themselves: mark slots 11 and 13
RESOLVED when the Habitat and execution-contract ADRs are Accepted.** Nothing
else here blocks A1 or A4.

1. **RESOLVED — the ADR backlog placed the superseded items post-MVP.**
   [Candidate 11 "Container Runtime Abstraction"](../notes_adr-backlog.md) is
   what Habitat supersedes — #273 says so directly ("amend the existing post-MVP
   Container Runtime Abstraction backlog item rather than leaving two competing
   abstractions"). [Candidate 13 "External Agent Runtime Contract"](../notes_adr-backlog.md)
   is what #282 supersedes.

   **Amend slots 11 and 13 in place** — re-scope each to the work it has become,
   move it out of post-MVP, and then mark that same slot resolved by the ADR it
   produces. Do not open new slots: the backlog's convention that a resolved
   candidate keeps its slot exists to preserve citations, and minting new
   numbers beside resolved pointer stubs would duplicate the concept in two
   places.

   *Done in PR #325:* slot 11 became Habitat Execution Boundary and slot 13 the
   Agent Execution Contract, both blocking Phase 3 and both carrying the
   constraints easiest to lose in transit to an ADR; the two originating
   parking-lot entries are marked graduated with pointers; and the backlog's
   section was renamed `Candidates, Stable-Numbered`, since stable numbering and
   true dependency order cannot both hold once a candidate's blocking phase
   changes.

2. **RESOLVED — #273 required "Phase 2 persistence hooks," and Phase 2 is closed.**
   Resolution: **no Phase 2.1 for schema.** The Habitat tables become a Phase 3
   migration, exactly as prompt packs already are — the schema inventory already
   carries deferred families with a named creator phase, migrations are additive,
   and the plane is versioned. Reopening a closed phase to add tables would set a
   worse precedent than the one it fixes. Rewrite #273's section 2 to name Phase
   3 as the creator.

   This leaves **Phase 2.1 meaning exactly one thing: #286**, which is Phase 2's
   seam being proven rather than new Phase 2 scope.

   *Done:* #273's section 2 was rewritten to name Phase 3 as the creator of the
   Habitat persistence family.

---

## Track B — Cloud Data-Plane Portability ([#286](https://github.com/SnapdragonPartners/maestro/issues/286))

Prove the persistence composition boundary is genuinely pluggable — one managed
Postgres, one real cloud object store, mode selected at the composition boundary
with no application-level branching — before Phases 3–6 make local assumptions
expensive to remove.

**It runs parallel to Track A's authoring, and it does not gate phase entry —
but it is not thereby non-gating.** #286 as filed says it completes before Phase
3 begins. That wording is **amended, not quietly relaxed**: Track B gates the
Phase 3 item that wires the Orchestrator through the persistence seam. Nothing
in Track A depends on its result — Habitat is execution rather than persistence,
the tool hook and the execution contract never touch the storage seam, and
prompt-pack storage depends only on the seam holding, which Phase 2 items 4 and
9 already demonstrated. So it may be authored in parallel; it may not be
finished after the wiring it exists to protect. **#286's issue text must be
updated to say this** before this plan is Accepted.

Two additions to its acceptance criteria:

- **The proof is a re-runnable manual workflow, not a one-shot report.** Phase 3
  adds migrations (Habitat, prompt packs, Work Groups). A one-time portability
  report is stale the moment those land; a re-triggerable workflow stays a live
  check.
- **Explicit DR approval for the cloud spend**, on the same footing as a paid
  golden run.

**Spend status: broadly approved by DR (2026-08-09)** — start minimal with a
cheap, disposable managed Postgres plus whatever execution surface the proof
needs (likely Cloud Run), and expand from there. Four parameters are fixed at
Track B kickoff rather than here: the cloud project, the credential source, the
teardown/cleanup rule, and the maximum spend. Kickoff does not proceed until
they are written down.

---

## Track C — Benchmark Repair

Phase 3 owes **two** paid golden runs: Phase 2's carried regression run against
v1-as-patched (which gates v1 retirement, because removing v1 destroys the only
target that can discharge it) and Phase 3's own phase-end run against the v2
adapter. Both are currently unrunnable. This track is early Phase 3 work, not
phase-entry work, but it must be scheduled early because everything downstream
of v1 retirement waits on it.

Ordered, because two of these items depend on the one before:

| Order | Item | Status |
| --- | --- | --- |
| 1 | [#317](https://github.com/SnapdragonPartners/maestro/issues/317) architect approval loop cannot force its terminal tool | **Required before either run.** It blocks the committed `paired-default` outright. |
| 2 | **Architect model probe** — [#323](https://github.com/SnapdragonPartners/maestro/issues/323) | **Required, and runs after #317 is fixed.** `claude-opus-4-1` is retired; `gpt-5`, `o4-mini` and `claude-opus-5` are excluded by #316. `gpt-4o`, `claude-opus-4-5` and `claude-opus-4-6` accept temperature and have **never been tested against #317** — the viable set was not shown empty, only unexplored. The probe must exercise the **actual iterative approval loop and its real tool set**, not a raw model call: #317's diagnostic is that architect stages with 0 or 2 general tools converged and stages with 4 never did, so a bare call would prove nothing about the failure being probed. Small explicit spend cap, DR approval before it runs. |
| 3 | [#318](https://github.com/SnapdragonPartners/maestro/issues/318) dirty-tree preflight | **Required before either run.** The phase-exit target was built from a dirty tree; the digest pinned what ran and the commit did not reproduce it. |
| 4 | [#319](https://github.com/SnapdragonPartners/maestro/issues/319) model-lifecycle preflight | **Required.** The check that would have caught the Opus 4.1 retirement seven weeks before it cost the phase-exit run — the only item here that prevents a *recurrence* rather than repairing a *symptom*. Depends on A4's identity split (below). |
| — | [#316](https://github.com/SnapdragonPartners/maestro/issues/316) sampling parameters forced non-nil | **Not a gate on either run** — `gpt-4.1` accepts temperature — but small and worth landing early. Until it does, **the tested replacements that reject temperature stay undrivable**, which is what narrowed the replacement pool. |

**The metadata home, and why it waits on A4.** #319 needs somewhere to record a
pinned model's published deprecation and retirement dates. Model **lineage** —
the set of originating labs, the one genuinely missing input for ADR 0020's
reviewer-heterogeneity mechanism — is a similar kind of fact wanting a similar
home. But they are **not necessarily keyed the same**: a retirement date belongs
to a *provider's served model identity*, while lineage belongs to the
*underlying model* — an open-weight model served by three providers has one
lineage and up to three retirement calendars. A4 settles that identity split
(scope decision 3 above); #319 then builds an extensible home consistent with
it. The heterogeneity mechanism itself remains a Phase 5 exit criterion. Only
the shape of the home is decided here, and only so it is not built twice.

Each of the two runs needs **explicit DR approval for that specific run**. Phase,
plan, and previous-run approval are not reusable.

**[#279](https://github.com/SnapdragonPartners/maestro/issues/279) does not gate
either run** (settled 2026-08-09). Its own text says rung 5 is realised in
intent through `test-output` — `app-healthz-endpoint` stands up the real router
and speaks HTTP to it — and that it "is not blocking the ladder today." It
blocks only a claim to a *distinct behavioural-evidence kind* in the evidence
package. Both runs may be recorded in the conformance log under the existing
kinds.

---

## Track D — Engineering Runway

Serial, before the code-heavy Phase 3 work, if schedule permits. None of it
gates phase entry.

1. [#314](https://github.com/SnapdragonPartners/maestro/issues/314) — a checked-in
   mutation harness. Non-blocking, but Defect-Shaped Verification is now binding
   in [`process_build.md`](../process_build.md) and Phase 2 paid for it by hand
   in every item. This repays across all of Phase 3.
2. [#321](https://github.com/SnapdragonPartners/maestro/issues/321) — reap leaked
   integration Compose projects. A **prerequisite** of #307 rather than merely
   adjacent to it: raising concurrency multiplies the leak.
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
   `make build` (194s of CI's 339s). Independent CI work; it will not improve the
   local integration suite.

---

## In Phase 3's Plan, Not Pre-Entry

Phase 3 gates that must appear in `plan_scope.md` but do not block opening it:

- [#265](https://github.com/SnapdragonPartners/maestro/issues/265) — single-owner
  agent restart; remove the dual death-observer shape.
- [#272](https://github.com/SnapdragonPartners/maestro/issues/272) —
  **implementation only**; its contract portion moved into A4 above. (Note the
  trap recorded in ADR 0020's amendment: `Provider` describes routing and is
  **not** lineage.)
- [#287](https://github.com/SnapdragonPartners/maestro/issues/287) — fold
  `dataplanectl` into the replacement main binary, which needs embedded compose
  assets. Naturally sequenced with the `cmd/maestro` rework.
- [#298](https://github.com/SnapdragonPartners/maestro/issues/298) — not a
  scheduling blocker: the roadmap now assigns all `drop` dispositions to Phase 3
  retirement. **Close it once its five deletions are copied into the accepted
  Phase 3 plan and exit checklist**, so closing the issue does not lose them.

---

## Delta Arbitration

Codex arbitrated eleven proposed deltas on 2026-08-09; DR settled the tool
boundary. Recorded so the reasoning survives the document it changed.

| # | Delta | Verdict | How this document reflects it |
| --- | --- | --- | --- |
| D1 | #286 moves off the head of the serial chain | Accept with condition | Parallel authoring, but it explicitly **gates Orchestrator/persistence wiring**, and #286's own text is amended to say so rather than silently relaxed. |
| D2 | A1 and A4 accepted as a set | **Revised** | Drafted concurrently and reviewed together, but **A1 is Accepted first**. #282 blocks #273's implementation completion, not its design ADR, and #282 consumes an accepted Habitat contract. |
| D3 | A2 states a scope invariant, not only a hook location | **Revised substantially** | The coverage question stands; the proposed invariant is **withdrawn**. It would have disabled Claude Code-style built-in editing (Codex) and reached for control over agents outside Maestro's remit — an engineer may run other agents in a Habitat, and the app under development may itself be an agent (DR). Replaced by the mediated / in-Habitat split, with each mode's guarantee stated honestly. |
| D4 | One terminal-outcome vocabulary with four feeders | Accept as one **schema**, not one enum | Four axes: execution status, completion disposition, cancellation reason, failure class. `already_satisfied` is a work disposition, not the same axis as infrastructure failure. |
| D5 | Habitat tables are a Phase 3 migration | Accept | Unchanged. |
| D6 | #286's proof is re-runnable | Accept | Unchanged. |
| D7 | Backlog slots 11 and 13 | **Revised** | **Amend in place, then mark resolved** — no new slots. New slots beside resolved pointers would duplicate the concepts. |
| D8 | #319 required, with room for lineage | Accept with correction | #319 required. But retirement keys to a *provider's served model identity* and lineage to the *underlying model* — not necessarily one key. A4 settles the split first; #319 builds the home against it. |
| D9 | #316 early but not gating | Accept with factual edit | "Every reasoning-tier model is undrivable" → "**the tested replacements that reject temperature** are undrivable." The original claim contradicted this document's own correct statement that viable alternatives remain untested. |
| D10 | Track D order, #306 before #307 | Accept | #314 → #321 → #306 → #307; #308 independent. |
| D11 | Architect-model probe as its own item | Accept with conditions | Runs **after** #317 is fixed, against the real iterative approval loop and tool set rather than a raw model call, under an explicit spend cap with DR approval. |

Four further corrections from the same round, applied above: the
"ADR and nothing else" rule now carries a bounded exception for A4's conformance
executable, which lives outside production packages; #272's contract portion
moved into A4; A5's enforcement rests on Habitat fencing and the execution
contract rather than the policy hook alone; and complete-then-reconcile is
rejected for permitting stale work rather than on ADR 0019 grounds, since
reconciliation judgment could legitimately be routed to an agent.

### Third round, 2026-08-09 — revocation is not termination

One P1 and one editorial correction, both applied.

**P1: revocation alone does not stop execution.** The second revision rested A5
on lease revocation, which invalidates authorization but cannot stop a process
already running inside a Habitat — it keeps editing files and can keep spawning
children, needing no further authorization for either. A1 now specifies a
**fencing protocol** (cooperative cancellation with a bounded grace period →
provider-enforced termination → quarantine on unconfirmed termination → no
reassignment or dispatch until fencing is acknowledged → generation fencing for
late mediated calls), and A5's terminal `cancelled`/`superseded` follows only
**confirmed** fencing.

The defect had spread to three places — A1's lifecycle list, A2's in-Habitat
containment guarantee, and A5's enforcement paragraph — because each was written
against the same wrong mental model rather than copied from another. All three
are corrected. *This is the failure shape recorded in CLAUDE.md's Verification
Discipline and in the "recovery spans the region" lesson: a fix armed beside the
failure in view covers that one and no other.*

**Editorial: Track A is five design decisions, not five ADRs.** A5 lands as an
amendment to ADR 0019 rather than a new ADR — it completes a case 0019 itself
deferred, it concerns 0019's own subject, and its mechanisms are owned by A1
(fencing) and A4 (cancellation lifecycle, terminal result), leaving only the
policy for 0019 to carry.

### Fourth round, 2026-08-09 — fence a domain, not a process tree

One P1, applied. **Process ancestry is not a portable containment boundary.**
The third round's requirement 2 said "the lease-holding process and its
descendants," which is unimplementable on every backend that matters: Docker
socket access creates sibling containers outside the PID tree, a Kubernetes
agent can create sibling Pods, and a chroot provides no process isolation at
all. A1 now requires each provider to create an **immutable fencing domain**
containing everything able to mutate the Habitat, and `Fence()` returns a
positive receipt or `unconfirmed` — best-effort success is forbidden.

Two claims from the review were verified against the code before being written
in, and both hold. `pkg/exec/docker_long_running.go:243` mounts the raw host
Docker socket into **every** long-running container unconditionally, so the
sibling-container escape is live today. `StopContainer`
(`:356-380`) swallows both `docker stop` and `docker rm -f` failures and returns
`nil`. One detail beyond the review: that path also **deletes the container from
`activeContainers` and the global registry on the failure branch**, so a failed
stop destroys the record that would let anything reconcile it later — strictly
worse than an unconfirmed receipt. Frozen v1 code, so not a v1 fix; it is a
requirement on the Phase 3 provider and the reproducer target for A1's spike.

Same spread pattern as the third round, and the reason it is worth naming twice:
the wrong abstraction had reached **four** places including the #273 issue body
this plan had already amended. Grepping the concept rather than fixing the
flagged line is what caught the tracker copy.

### Fifth round, 2026-08-09 — non-interference, and a bounded spike

One P1 and one scope correction, both applied. **Codex approved the plan with
these edits; no further backend research or implementation is warranted now.**

**Scope, raised by DR.** The backend list is open-ended — macOS and Terraform
are real possibilities beyond the three the fourth round named — and "prove
against three backends" would have turned a design ADR into a research project.
The spike now asks for exactly two pieces of evidence: one executable
Docker/Compose reproducer, and one bounded paper walkthrough of a materially
different failure shape (a Kubernetes partition, no cluster required). Everything
else moves to a compatibility matrix marked explicitly non-gating.

**P1: specify non-interference, not mandatory death.** Requiring every provider
to confirm *termination* would exclude valid remote and native providers for no
safety gain. What the system actually needs is that the stale generation cannot
affect state reachable by current or future work. `FenceReceipt` is now
three-valued — `terminated`, `isolated` (permanently quarantined, capabilities
revoked, cleanup may continue asynchronously), or `unconfirmed`, which stays
non-terminal and quarantined. Docker proves the `terminated` path and is the
only backend Phase 3 implements.

Worth recording *why* the wider question improved the contract rather than
merely bounding it: asking what macOS and Terraform would do exposed that the
fourth round's requirement was stricter than the property it was protecting.
Terraform in particular is not a termination primitive at all — it would
provision the substrate that does the fencing.

## Settled Questions

All four questions the draft left open were answered in the same round.

1. **#279 does not gate either conformance run or its log record** — only a claim
   to a distinct behavioural-evidence kind. Track C, above.
2. **`Habitat` is the accepted name.** A1 fixes it rather than leaving it
   provisional.
3. **A4's vertical slice** may be any real external-process executable over the
   local transport, without Actions presentation, provided it exercises the wire
   boundary, capabilities, events, cancellation and the terminal result.
4. **Track B spend is broadly approved by DR**; project, credential source,
   cleanup rule and maximum spend are fixed at kickoff.

## Tracker Changes Made For This Plan

All three were completed on 2026-08-09, before this plan went for final review.

- **[#286](https://github.com/SnapdragonPartners/maestro/issues/286) amended** —
  scheduling changed from "before Phase 3 begins" to parallel authoring that
  **gates Orchestrator/persistence wiring**, stated explicitly as a relaxation of
  the schedule and not of the gate; re-runnable-workflow acceptance criterion
  added; the four spend parameters named as a kickoff precondition.
- **[#273](https://github.com/SnapdragonPartners/maestro/issues/273) amended** —
  `Habitat` name settled; section 2's "Phase 2 persistence hooks" retargeted to a
  Phase 3 migration; **the fencing protocol added as a new design requirement**;
  the tool-routing rule scoped to Maestro's own agents; acceptance sequenced
  before #282.
- **[#323](https://github.com/SnapdragonPartners/maestro/issues/323) filed** — the
  architect model probe, carrying D11's conditions: after #317, against the real
  iterative approval loop and tool set rather than a raw model call, under an
  explicit spend cap with per-run DR approval.

Nothing else in the tracker was changed.
