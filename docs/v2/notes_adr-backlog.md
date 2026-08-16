+++
title = "Maestro v2 ADR Backlog"
edit_date = "2026-08-15"
status = "live"
type = "notes"
summary = "Reconciled, stable-numbered ADR backlog (Phase 0 item 12): candidates resolved in Phase 0 and in later phases with their Accepted ADRs, and open candidates each labelled with the phase it blocks — slot numbers are cited by other documents and never change, so a heading rather than a position in the list is what says when a candidate is due."
+++

# Maestro v2 ADR Backlog

Status: live — reconciled 2026-07-15 (Phase 0 item 12); supersedes the interim priority list in [notes_v1-adr-alignment.md](notes_v1-adr-alignment.md). New ADR needs discovered mid-phase land here, not in the phase.

## Resolved In Phase 0

| Candidate | Resolution |
| --- | --- |
| v2 Documentation Authority And Planning Reset | [ADR 0017](../adr/0017-v2-documentation-authority-and-lifecycle.md) (amended 2026-07-15); archive plan executed by the [doc-reset manifest](phase_0/manifest_doc-reset.md) |
| v2 Taxonomy: Product, Feature, Epic, Story, Work Group | [ADR 0018](../adr/0018-v2-work-taxonomy.md) (repo-Product rule amended by 0022) |
| Orchestrator Boundary | [ADR 0019](../adr/0019-orchestrator-boundary.md) (amended 2026-07-14: dispatch at both grains); the in-flight-work policy is carried below as an open candidate |
| Intake And Triage — stage 1 (artifact contract) | [ADR 0024](../adr/0024-intake-and-triage-artifact-contract.md) (amended 2026-07-14); stage 2 is carried below |
| Reviewer vs Partner/Supervisor | [ADR 0020](../adr/0020-review-invariant-reviewer-vs-partner.md) (amended: agentic review, unconditional human Accept) |
| Management And Audit Artifacts | [ADR 0021](../adr/0021-artifacts-and-principal-instances.md) |
| Agent Instance And Lightweight Signatures | [ADR 0021](../adr/0021-artifacts-and-principal-instances.md) (principal instances + MPH signature; no cryptographic signing, as recommended) |
| Golden Stories And Benchmark Runner | [ADR 0025](../adr/0025-golden-stories-and-benchmark-runner.md) |
| v1 Freeze And Port-Vs-Rewrite Inventory | Freeze: roadmap D7 and the `v1-freeze` tag. Inventory and breaking-change principles: [inventory_v1-port.md](phase_0/inventory_v1-port.md) (live) — recorded as a phase artifact, not an ADR, by agreement |
| Postgres Data Plane | [ADR 0022](../adr/0022-v2-data-plane.md) (amended: local durability invariant, config/secrets, backup contract) |
| Branch Strategy | [ADR 0023](../adr/0023-v2-branch-strategy.md) |
| Binary Attachment Storage | [ADR 0022](../adr/0022-v2-data-plane.md) — object storage first-class, content-addressed digests, binaries never in relational rows |
| User Credentials And Configs | [Project-folder spike](phase_0/spike_project-folder.md) + ADR 0022 amendment (2026-07-14): config records and secrets vault in the plane, key-file root of trust outside it |

## Resolved In Later Phases

| Candidate | Resolution |
| --- | --- |
| Artifact Envelopes And Payload Schemas (blocked Phase 2) | [ADR 0028](../adr/0028-artifact-envelopes-and-payload-schemas.md), Accepted 2026-07-24 as Phase 2 item 1 |
| Reviewer Heterogeneity Means Distinct Lineage (blocked Phase 5) | [ADR 0020](../adr/0020-review-invariant-reviewer-vs-partner.md) amendment, proposed 2026-08-08 and accepted 2026-08-09 — the rule only; the mechanism is deferred as tracked work |
| Habitat Execution Boundary (blocked Phase 3) | [ADR 0029](../adr/0029-incubator-and-habitat-execution-boundaries.md), Accepted 2026-08-12 as item A1 of the pre-Phase-3 blocker plan — split into the Incubator and the Habitat, with two spike artifacts |
| Tool Execution Policy Hook (blocked Phase 3) | [ADR 0030](../adr/0030-tool-execution-policy-hook.md), Accepted 2026-08-13 as item A2 — one boundary in three gates, with a human-required call logically blocked rather than denied and retried |
| Agent Execution Contract (blocked Phase 3) | [ADR 0032](../adr/0032-agent-execution-contract.md), Accepted 2026-08-15 as item A4 — a versioned wire contract, a four-axis terminal result, and the served-versus-underlying model identity split #319 waited on |
| Amendment Vs Running Work (blocked Phase 3) | [ADR 0019](../adr/0019-orchestrator-boundary.md) second amendment, Accepted 2026-08-15 as item A5 — running work is cancelled when the dispatch basis it was issued under stops being current, and what already happened is never rewritten |
| Prompt Pack Identity, Resolution, And Storage (blocked Phase 3) | [ADR 0031](../adr/0031-prompt-pack-identity-resolution-and-storage.md), Accepted 2026-08-13 as item A3 — scheme-qualified content identity, immutable content beside a mutable installation, resolution once at dispatch |

## Candidates, Stable-Numbered

**Each heading names the phase that candidate blocks, and the heading is authoritative — position in this list is not.** An entry should be Accepted before its blocking phase starts implementation.

Numbers are stable because phase plans and session notes cite them. A resolved candidate therefore **keeps its slot** as a pointer to its ADR rather than being deleted and renumbering everything below it, and a candidate whose blocking phase changes **keeps its slot** rather than being moved. The section is mostly open candidates with resolved stubs among them; the resolved tables above are the authoritative list of what is done.

The list was originally dependency-ordered and no longer is: slots **11** (Incubator and Habitat, RESOLVED) and **13** (Agent Execution Contract) were re-scoped from post-MVP to **blocks Phase 3** on 2026-08-09 and kept their numbers, so they now sit after entries blocking Phases 4, 5 and 6. Stable numbering and true dependency order cannot both hold, and numbering won because it is what other documents depend on.

### 1. Artifact Envelopes And Payload Schemas — RESOLVED by [ADR 0028](../adr/0028-artifact-envelopes-and-payload-schemas.md)

Accepted 2026-07-24 as Phase 2 item 1; see the Resolved In Later Phases table above. All five decisions it carried are fixed there: the JSON envelope and its JCS digest discipline, the code-resident payload type registry validated at the seam, additive-within-version evolution with the reader as the only compatibility layer, RFC 7386 merge-patch amendments materialized on read, and review linkage over the whole reviewable projection.

### 2. Online Backup And Restore — trails Phase 2 (non-blocking)

The cold-backup baseline shipped in ADR 0022 as amended; this candidate is the online upgrade: snapshot/`pg_basebackup`-class backup, restore validation, cross-store consistency across Postgres, object store, and local forge.

### 3. Amendment Vs Running Work — RESOLVED by [ADR 0019](../adr/0019-orchestrator-boundary.md)'s second amendment

**Accepted 2026-08-15** (Codex + DR) as item A5 of the [pre-Phase-3 blocker plan](phase_3/plan_blockers.md), after eight review rounds. The slot keeps its number because phase plans and session notes cite it.

Deferred from ADR 0019's dispatch amendment (2026-07-14): the policy for work already executing when its Epic/Story/DAG record is amended or superseded — cancel, suspend, or complete-then-reconcile. **The answer is cancel**, and it landed as an amendment to [ADR 0019](../adr/0019-orchestrator-boundary.md) rather than a new ADR: it completes a case 0019 itself deferred, concerns 0019's own subject, and its mechanisms are owned by candidates 11 (fencing) and 13 (cancellation lifecycle, terminal result), leaving only the policy. It was **last** in the design track because it depends on both.

Three things a reader of the old entry must carry forward, each wider than what this slot asked for:

- **The trigger is not "its record is amended".** A dispatch binds to a **dispatch basis** in two halves: a **governing version set** — for a Story execution, exactly its own effective version and its governing Epic's — and the work item's **incoming dependency basis**, meaning its predecessor identities together with the effective completions that satisfied them. Cancellation fires when either half stops being current. Reading only the Story's own record would let a governing Epic amendment sail past the work running under it, which is the ordinary case that made this candidate blocking.
- **Enforcement is not a version comparison.** A dependency-only change leaves the Story's version current, so nothing about that version can stop the work. Cancellation supersedes the **execution's own authority**, which ADR 0032 already has the boundary reject, and that closure **linearizes** with the changed basis becoming authoritative — otherwise there is a window in which the new basis is effective and the old authority still admits actions.
- **An amendment revokes nothing already done.** Audit stays final, drafts stay draft, and accepted artifacts stay accepted — ADR 0021's status vocabulary has no path back to draft. What changes is that a result no longer satisfies the current dispatch basis, which is a separate statement reconciled through 0021's own lifecycle. An execution that completed *before* the change keeps its completion for the same reason.

### 4. Tool Execution Policy Hook — RESOLVED by [ADR 0030](../adr/0030-tool-execution-policy-hook.md)

Accepted 2026-08-13 (Codex + DR) as item A2 of the [pre-Phase-3 blocker plan](phase_3/plan_blockers.md); see the Resolved In Later Phases table above. The slot keeps its number because phase plans and session notes cite it.

Three things a reader of the old entry must carry forward, because each is wider than what this slot asked for:

- **The placement question is closed.** Not the toolloop, not the dispatcher, not a policy service: one mandatory boundary at the Orchestrator's tool-execution seam, and Phase 3 must be able to demonstrate that no tool reaches its effect around it.
- **The mediated / in-resource split became two independent axes**, and the effect site is **three-valued**. Mediation is the request path — what the Orchestrator can refuse; containment is the effect site, which is Orchestrator-side, in-resource, or **external**. An unmediated call to an outside service is bounded only by the network and credential grants made at provisioning, and only direct access to Orchestrator-managed effects is structurally forbidden. Reading the split as binary understates what Maestro does not govern.
- **A human-required call blocks; it is not denied and retried.** The Story enters `awaiting_resolution` and one operator decision resolves one logical action. Headless runs mark the Story blocked immediately rather than waiting.

**Maestro's enforcement stays scoped to Maestro's own agents** (DR, 2026-08-09): an engineer may legitimately run other agents in a resource, and the application under development may itself be an agent.

Four constraints the ADR places on candidate 12, recorded here so they are not rediscovered: a semantic gate is an **agent**, because the hook may not infer; a human gate returns *requires an operator* and declares which scopes it permits; **composition across gates is intersection and belongs to the boundary**, never to a gate or a UI; and a rule may only read fields the action schema declares safe to persist, since a denial whose grounds cannot be recorded cannot be audited.

### 5. Prompt Pack Identity, Resolution, And Storage — RESOLVED by [ADR 0031](../adr/0031-prompt-pack-identity-resolution-and-storage.md)

Accepted 2026-08-13 (Codex + DR) as item A3 of the [pre-Phase-3 blocker plan](phase_3/plan_blockers.md); see the Resolved In Later Phases table above.

**It re-cuts one line of candidate 9 below**, which is the thing to carry forward: "installed org-level packs" was a single deferred item and is two. The **minimal installation record** — display name, declared Maestro version range, declared role coverage, and a revision — is **required now**, because those facts sit outside the content digest that makes a pack immutable and could otherwise never be corrected, and Phase 3 cannot resolve a pack without them. What stays deferred is the **registry**: browsing and installing as a user-facing act, governance over who may install what, inheritance and overlays, versioning and export formats, distribution, and sharing across organizations.

Two further constraints the ADR fixes, both easy to lose in transit: a pack digest is **scheme-qualified** and no comparison crosses schemes, so an imported v1 identity stays opaque rather than joining a group with a v2 pack; and the pack **name is a label**, never a selector and never a comparison key. The concrete debt it settles: `principal_instances.prompt_pack_id` was a nullable `text` column doing three jobs, whose only writer records a pack the plane will never own.

### 6. UAT And Demo Mode — blocks Phase 4

Whether UAT is optional in MVP or required for Epic merge gates the evidence-package and Accept flow. `pkg/demo` reworks against this ADR (port inventory).

### 7. Intake And Triage — stage 2 — blocks Phase 5 (pre-Phase-5 spike)

Settled by the pre-Phase-5 spike: the executor (form logic, short-lived triage agent, provisional Work Group), the "I don't know" escalation flow, provisional Work Group lifecycle, recipient pushback protocol, cross-Epic coherence checking, and graduation criteria for a standing intake agent.

### 8. Workbench And The Interactive Loop — blocks Phase 5 (dedicated pre-Phase-5 spike + ADR)

Anchored 2026-07-15 (Phase 0 item 12 review; the reconcile found it had no phase slot). The Workbench is critical to v2 and is now scheduled end-to-end: a **dedicated pre-Phase-5 spike and Accepted ADR**, separate from intake stage 2; an explicit **Phase 5 output and end-to-end exit criterion** (dashboard entry → session on a real Epic branch → trailing evidence and drift review → human Accept); and **tempo-neutrality constraints on Phases 3 and 4** so the runtime and branch/evidence contracts cannot foreclose it. The open design questions live in the roadmap's Workbench spike section.

### 9. Skills And Pack Registry Expansion — Phase 5/6

The remainder of the packs/skills candidate after the Phase-3-blocking split above: installed org-level packs/skills as DB-canonical, immutable, versioned, exportable; repo-local packs; the skills registry (pillar 10).

**Narrowed 2026-08-13 by [ADR 0031](../adr/0031-prompt-pack-identity-resolution-and-storage.md)** (slot 5). "Installed org-level packs" is two items, not one, and the first has moved out of this candidate: the **minimal installation record** a pack cannot be resolved without — display name, declared Maestro version range, declared role coverage, revision — is **in Phase 3 scope and no longer deferred here**. Its design is settled; the migration and the code are Phase 3 implementation and are not written. What remains in this candidate is the **registry** around it: browsing and installing as a user-facing act, governance over who may install what, inheritance and overlays, human-readable version labels and their ordering, export formats, distribution, repo-local packs, deduplicating identical content across organizations, and the skills registry. Also carried here: agent-authored packs and their review posture, since making a pack a Management artifact would put every pack edit under the review invariant and that has not been decided.

### 10. Knowledge Hierarchy And Knowledge Packs — blocks Phase 6

Source precedence (ADRs, interfaces/contracts, docs, skills, AST/code facts), citation rules, staleness, pack generation. Inputs: the [cms spike](phase_0/spike_cms.md) (ingestion from maestro-cms, graph contributed upstream per its ADR 0005) and the [cms wishlist](requirements_maestro-cms-wishlist.md) responses.

### 11. Incubator And Habitat Execution Boundaries — RESOLVED by [ADR 0029](../adr/0029-incubator-and-habitat-execution-boundaries.md)

Accepted 2026-08-12 (Codex + DR) as item A1 of the [pre-Phase-3 blocker plan](phase_3/plan_blockers.md); see the Resolved In Later Phases table above. Re-scoped in place on 2026-08-09 from "Container Runtime Abstraction — post-MVP", and the slot keeps its number because phase plans and session notes cite it.

**The resolution split the resource in two**, which is the one thing a reader of the old entry must carry forward: the **Incubator** is the unitary Story-scoped development environment with a toolchain and no ecosystem, and the **Habitat** is the deployed application environment holding every Maestro-managed dependent service. Every *concrete* requirement this slot previously stated under the name Habitat — tool routing, read-only Architect inspection, the removal of Coder workspace bind-mounts, the Docker socket escape — attaches to the **Incubator**. Dependent-service lifecycle attaches to the Habitat. Identity, generation, and fencing attach to both.

Two constraints this slot recorded were **sharpened by the spike evidence** and should be read from the ADR rather than from here:

- `isolated` is not merely "capabilities revoked". Every path into state current or future work will touch is either closed by the authority enforcing it or leads to a permanently abandoned target — and an isolated generation may still be running.
- Fence state and cleanup state are independent. A resource is visible and reconciled until deallocation is *confirmed*, whatever its receipt.

### 12. Tool And Action Policy Gating — post-MVP

The full gating-policy ADR behind the Phase 3 hook: structural gates (role/env/tool allowlists, filesystem scopes), semantic gates (high-risk action summaries checked against policy), and human gates, per the research corpus (Day 4/Day 5).

### 13. Agent Execution Contract — RESOLVED by [ADR 0032](../adr/0032-agent-execution-contract.md)

**Re-scoped in place**, per the accepted [pre-Phase-3 blocker plan](phase_3/plan_blockers.md) (item A4) and [issue #282](https://github.com/SnapdragonPartners/maestro/issues/282). The slot keeps its number for the same citation reason as 11.

A versioned **wire** contract rather than a Go interface, usable by Go-native and non-Go agents alike: invocation (run ID, principal instance, role, task/artifact references, model and prompt-pack identity, policy/budgets, fenced resource references — Incubator and, where verification needs one, Habitat, per [ADR 0029](../adr/0029-incubator-and-habitat-execution-boundaries.md)), events, terminal result, lifecycle, provenance, transport, and capability-based tool/knowledge access. Proven by one executable agent exercising the real wire boundary, capabilities, events, cancellation, and terminal result.

The original entry asked *whether* Maestro can run Claude Code, OpenHands, or similar as first-class executors. That question is settled — it can, and the contract is how — so the slot now carries the contract itself. Three decisions from the blocker plan belong to it:

- **The terminal result is a four-axis schema, not one enum**: execution status, completion disposition (`already_satisfied` is a work disposition, not an execution status), cancellation reason (`superseded`), and failure class. Three of the four axes were discovered independently; each would otherwise have added a one-off status.
- **It absorbs the contract portion of [#272](https://github.com/SnapdragonPartners/maestro/issues/272)** — explicit provider/model/endpoint identity cannot be deferred past a contract that carries model identity. It must settle whether a provider's *served* model identity (which has a retirement date) and the *underlying* model identity (which has a lineage) are one key; [#319](https://github.com/SnapdragonPartners/maestro/issues/319)'s metadata home depends on the answer.
- **It is Accepted after candidate 11**, which it consumes. #282 blocks #273's *implementation* completion, not its design ADR.

**Accepted 2026-08-15** (Codex + DR) as item A4, after eight review rounds, with
its conformance executable at `spikes/phase_3/executioncontract` and the
[report](phase_3/spike_execution-contract.md). The slot keeps its number for the
citation reason above.

Three things a reader of the old entry must carry forward, each wider than what
this slot asked for:

- **The identity split is three concepts, not two.** A **route** (provider,
  endpoint, model name — explicit, never inferred) is neither identity; the
  **served** identity carries retirement and the **underlying** model carries
  lineage, with a nullable reference between them whose null is ADR 0020's
  existing `unclassified`. #319 builds its metadata home against that.
- **An invocation carries two lifetimes, not one.** What was resolved for an
  execution must not silently change; its resource grants may be replaced
  mid-execution, because gate 3 acquires resources after approval. **Two
  lifetimes is the decision** — how they are represented, and how either
  persists, is Phase 3's.
- **Recovery is artifact-level.** An agent restarts from the last committed
  workflow artifact, not from where it stopped. Resuming would require persisting
  the substituted request, which ADR 0030 §3 keeps out of the Audit family.

**Scope-corrected 2026-08-15, after acceptance** (Codex + DR). ADR 0032's conformance
slice validated an isolated contract model, not integration with v1's agent
framework, and the ADR now carries a **Status Of Decisions** section separating
what binds from what Phase 3 settles against a real consumer. Demoted to design
inputs: the execution FSM; restart, resume, re-attach and outstanding-action
enumeration; epochs, acknowledgements, watermarks and durable outboxes; the
question-wait lifecycle; and durable reusable approvals. Cite that section, not
the decision sections, when this slot's resolution is used as authority.

### 14. Dispatcher/Message Abstraction For Cloud Jobs — v3

Whether agent communication should anticipate cloud job execution. ADR 0019 already records the trajectory (channels are transport, never state; RPC possible if runtimes split); avoid overbuilding before v3.

### 15. Story Decomposition Review — the review invariant's missing seat — Phase 5

[ADR 0020](../adr/0020-review-invariant-reviewer-vs-partner.md) requires that **every persistent Management artifact be reviewed by at least one party other than its author**. The Story set an Architect decomposes out of a spec/Epic is such an artifact — and today nothing reviews it: the Architect authors the decomposition and queues it directly. The invariant is satisfied for plans, code, completion evidence, and merges, but not for the decomposition that determines all of them. It is the one artifact the Architect both authors and acts on unchecked.

Empirically observed in Phase 1 (2026-07-21, `cal-d9-cleanup-4m`): the `cleanup-provider-options` story — one refactor consolidating five near-identical provider functions behind a shared helper — was decomposed into **5 Stories, one per provider**, each paying full plan → review → code → test → review ceremony. The split is incoherent for the task (all five converge on rewriting the same helper), and it burned 2.26M tokens / $13.11 with **0 of 5 Stories completed**. No agent was positioned to say "this is one Story, not five." v1's P-4 prompt patch carries general right-sizing guidance, but prompt guidance is not a review seat: a prompt that enumerates N items still invites an N-way split, and nothing catches it when it happens.

**Severity update (2026-07-22).** The `cleanup-provider-options` prompt was rewritten to stop enumerating its five call sites and to state the task's cohesion outright; the architect then produced **one** Story rather than five. So over-decomposition is **avoidable by well-formed authoring** — it is a latent risk, not a standing blocker, which is a weaker claim than this entry originally made. The case for the review seat is unchanged but now rests on cost asymmetry rather than necessity: we only discovered the wording by burning ~$22 on two runs that never completed, and nothing in the system would have caught it. A reviewer would have; a prompt convention only works until someone writes an enumerating prompt again.

Scope for the ADR: who reviews a decomposition (Reviewer vs Partner/Supervisor per 0020's split — a decomposition reviewer plausibly needs judgment, not just blocking), what it checks (right-sizing, coherence of the split, dependency sanity, no padded verification Stories), and how it escalates. Note the cost asymmetry that makes this worth a gate: an over-decomposition multiplies ceremony across every downstream Story, so the review pays for itself on the first catch.

### 16. Reviewer Heterogeneity Means Distinct **Lineage**, Not Distinct Model — RESOLVED by the [ADR 0020](../adr/0020-review-invariant-reviewer-vs-partner.md) amendment (proposed 2026-08-08, accepted 2026-08-09)

Raised by DR at Phase 2 exit (2026-08-08) and accepted the next day as an amendment rather than a new ADR: 0020 already carried the norm and the "degraded is not broken" semantics, and the defect was one undefined word — it required the reviewer to run "a distinct model" without saying what made two models distinct, so two siblings from one lab conformed.

**The rule is now in ADR 0020** and is not restated here: model lineage is the set of originating labs, unchanged by serving provider or weight availability and only ever added to by derivation; the ladder is disjoint sets → overlapping sets, distinct model → same model, with unknown lineage held outside the ladder as *unclassified*; same-lineage warns and never refuses; and the classified unit is the author/reviewer *edge*. Read it there.

**What the amendment deliberately did not do is build the mechanism**, which does not exist at any granularity — so a same-*model* pairing goes unflagged today too. Be precise about what is actually absent, because the first draft of this entry overstated it:

- **Model-lineage metadata** — the one genuinely missing input. A property of a model, so it belongs with model metadata (alongside the lifecycle metadata [#319](https://github.com/SnapdragonPartners/maestro/issues/319) wants — the same *kind* of fact, but **not the same key**: [ADR 0032](../adr/0032-agent-execution-contract.md) §3 puts retirement on a provider's **served** identity and lineage on the **underlying** model, so the home is two levels) — never restated per configuration, or two configurations can disagree about one model.
- **Classification and surfacing** — the computation over an edge and the operator-visible result. A stored classification nothing shows an operator does not satisfy 0020.
- **The prospective routing contract** — needed *only* to classify a configuration before it runs. An MPH bundle's `[model.roles]` names the cast, not who reviews whom; that edge lives in the target harness and is rewritten by the Phase 3 runtime.

**The realized edge is not missing.** Phase 2's schema already persists it: `management_artifacts.author_instance_id`, `artifact_reviews.reviewer_instance_id`, both foreign-keyed to `principal_instances`, whose `model` is NOT NULL for every principal kind. So a completed review is already joinable to an (author model, reviewer model) pair, and **retrospective classification is unblocked as soon as lineage metadata exists — it does not wait on Phase 5.**

DR decided on 2026-08-08 not to reserve a slot in the MPH bundle for this: lineage is a model fact rather than a bundle fact, the field would have no consumer until the missing inputs land, and populating it would change `config_hash` on both configs and break the run identity group a second time for a configuration that cannot currently run.

> ⚠️ **Implementation trap, kept because it survives the ADR's general statement: `Provider` is not lineage.** `pkg/config`'s `Provider` field and `ProviderPatterns` describe *how a request is routed*. `gpt-oss` is deliberately matched to `ProviderOllama` ahead of the `gpt` → OpenAI rule, so a check built on the existing `Provider` value would call `gpt-oss` Ollama-lineage and score an OpenAI-vs-OpenAI pairing heterogeneous.
>
> Worked case: `paired-local` pairs `gpt-oss:20b` (OpenAI lineage) with `qwen3-coder:30b` (Qwen/Alibaba lineage) — **disjoint sets, rung 1**, though both route through Ollama. `paired-default` before 2026-08-08 paired two Anthropic models through one provider — overlapping sets, rung 2. Provider tells you neither answer.

Advance classification of a configuration blocks **Phase 5** (reviewer model routing), which is where the prospective routing contract gets defined. Retrospective classification of completed reviews does not.
