+++
title = "Maestro v2 ADR Backlog"
edit_date = "2026-08-08"
status = "live"
type = "notes"
summary = "Reconciled, dependency-ordered ADR backlog (Phase 0 item 12): candidates resolved in Phase 0 and in later phases with their Accepted ADRs, and open candidates ordered by the phase they block."
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

## Candidates, Dependency-Ordered

Ordered by the phase each blocks. An entry should be Accepted before its blocking phase starts implementation.

Entries are numbered, and those numbers are cited from phase plans and session notes, so a resolved candidate **keeps its slot** here as a pointer to its ADR rather than being deleted and renumbering everything below it. The section is therefore mostly open candidates with resolved stubs among them; the resolved tables above are the authoritative list of what is done.

### 1. Artifact Envelopes And Payload Schemas — RESOLVED by [ADR 0028](../adr/0028-artifact-envelopes-and-payload-schemas.md)

Accepted 2026-07-24 as Phase 2 item 1; see the Resolved In Later Phases table above. All five decisions it carried are fixed there: the JSON envelope and its JCS digest discipline, the code-resident payload type registry validated at the seam, additive-within-version evolution with the reader as the only compatibility layer, RFC 7386 merge-patch amendments materialized on read, and review linkage over the whole reviewable projection.

### 2. Online Backup And Restore — trails Phase 2 (non-blocking)

The cold-backup baseline shipped in ADR 0022 as amended; this candidate is the online upgrade: snapshot/`pg_basebackup`-class backup, restore validation, cross-store consistency across Postgres, object store, and local forge.

### 3. Amendment Vs Running Work — blocks Phase 3

Deferred from ADR 0019's dispatch amendment (2026-07-14): the policy for work already executing when its Epic/Story/DAG record is amended or superseded — cancel, suspend, or complete-then-reconcile. The Work Group runtime cannot ship without it.

### 4. Tool Execution Policy Hook — blocks Phase 3

A narrow, binding ADR: where the per-action policy hook lives (toolloop, dispatcher, tool execution layer, or a policy service) and its interface — no policy content. Chosen before Phase 3 builds tool plumbing, or per-action policy gets retrofitted into every tool. The full gating-policy ADR stays post-MVP (below).

### 5. Prompt Pack Identity, Resolution, And Storage — blocks Phase 3

Split from the broader packs/skills candidate (2026-07-15): the port inventory moves templates and packs into the data plane during Phase 3, and the MPH signature's P component needs pack identity from Phase 1's runner onward. The minimal contract — pack identity and content hash, resolution (which pack a run uses), and data-plane storage (family reserved since Phase 2, ADR 0022) — blocks Phase 3. Skills and registry expansion (installed org-level packs, versioning/export, repo-local packs) remain a later candidate below.

### 6. UAT And Demo Mode — blocks Phase 4

Whether UAT is optional in MVP or required for Epic merge gates the evidence-package and Accept flow. `pkg/demo` reworks against this ADR (port inventory).

### 7. Intake And Triage — stage 2 — blocks Phase 5 (pre-Phase-5 spike)

Settled by the pre-Phase-5 spike: the executor (form logic, short-lived triage agent, provisional Work Group), the "I don't know" escalation flow, provisional Work Group lifecycle, recipient pushback protocol, cross-Epic coherence checking, and graduation criteria for a standing intake agent.

### 8. Workbench And The Interactive Loop — blocks Phase 5 (dedicated pre-Phase-5 spike + ADR)

Anchored 2026-07-15 (Phase 0 item 12 review; the reconcile found it had no phase slot). The Workbench is critical to v2 and is now scheduled end-to-end: a **dedicated pre-Phase-5 spike and Accepted ADR**, separate from intake stage 2; an explicit **Phase 5 output and end-to-end exit criterion** (dashboard entry → session on a real Epic branch → trailing evidence and drift review → human Accept); and **tempo-neutrality constraints on Phases 3 and 4** so the runtime and branch/evidence contracts cannot foreclose it. The open design questions live in the roadmap's Workbench spike section.

### 9. Skills And Pack Registry Expansion — Phase 5/6

The remainder of the packs/skills candidate after the Phase-3-blocking split above: installed org-level packs/skills as DB-canonical, immutable, versioned, exportable; repo-local packs; the skills registry (pillar 10).

### 10. Knowledge Hierarchy And Knowledge Packs — blocks Phase 6

Source precedence (ADRs, interfaces/contracts, docs, skills, AST/code facts), citation rules, staleness, pack generation. Inputs: the [cms spike](phase_0/spike_cms.md) (ingestion from maestro-cms, graph contributed upstream per its ADR 0005) and the [cms wishlist](requirements_maestro-cms-wishlist.md) responses.

### 11. Container Runtime Abstraction — post-MVP

A future container/execution interface with Docker as the only initial implementation. Useful for future Apple/iPhone/raw-filesystem cases.

### 12. Tool And Action Policy Gating — post-MVP

The full gating-policy ADR behind the Phase 3 hook: structural gates (role/env/tool allowlists, filesystem scopes), semantic gates (high-risk action summaries checked against policy), and human gates, per the research corpus (Day 4/Day 5).

### 13. External Agent Runtime Contract — post-MVP

Whether Maestro can run Claude Code, OpenHands, or other headless agents inside containers as first-class executors (beyond the v1-style Coder integration the port keeps).

### 14. Dispatcher/Message Abstraction For Cloud Jobs — v3

Whether agent communication should anticipate cloud job execution. ADR 0019 already records the trajectory (channels are transport, never state; RPC possible if runtimes split); avoid overbuilding before v3.

### 15. Story Decomposition Review — the review invariant's missing seat — Phase 5

[ADR 0020](../adr/0020-review-invariant-reviewer-vs-partner.md) requires that **every persistent Management artifact be reviewed by at least one party other than its author**. The Story set an Architect decomposes out of a spec/Epic is such an artifact — and today nothing reviews it: the Architect authors the decomposition and queues it directly. The invariant is satisfied for plans, code, completion evidence, and merges, but not for the decomposition that determines all of them. It is the one artifact the Architect both authors and acts on unchecked.

Empirically observed in Phase 1 (2026-07-21, `cal-d9-cleanup-4m`): the `cleanup-provider-options` story — one refactor consolidating five near-identical provider functions behind a shared helper — was decomposed into **5 Stories, one per provider**, each paying full plan → review → code → test → review ceremony. The split is incoherent for the task (all five converge on rewriting the same helper), and it burned 2.26M tokens / $13.11 with **0 of 5 Stories completed**. No agent was positioned to say "this is one Story, not five." v1's P-4 prompt patch carries general right-sizing guidance, but prompt guidance is not a review seat: a prompt that enumerates N items still invites an N-way split, and nothing catches it when it happens.

**Severity update (2026-07-22).** The `cleanup-provider-options` prompt was rewritten to stop enumerating its five call sites and to state the task's cohesion outright; the architect then produced **one** Story rather than five. So over-decomposition is **avoidable by well-formed authoring** — it is a latent risk, not a standing blocker, which is a weaker claim than this entry originally made. The case for the review seat is unchanged but now rests on cost asymmetry rather than necessity: we only discovered the wording by burning ~$22 on two runs that never completed, and nothing in the system would have caught it. A reviewer would have; a prompt convention only works until someone writes an enumerating prompt again.

Scope for the ADR: who reviews a decomposition (Reviewer vs Partner/Supervisor per 0020's split — a decomposition reviewer plausibly needs judgment, not just blocking), what it checks (right-sizing, coherence of the split, dependency sanity, no padded verification Stories), and how it escalates. Note the cost asymmetry that makes this worth a gate: an over-decomposition multiplies ceremony across every downstream Story, so the review pays for itself on the first catch.

### 16. Reviewer Heterogeneity Means Distinct **Lineage**, Not Distinct Model — amends ADR 0020; applies to benchmark configs now

Raised by DR at Phase 2 exit (2026-08-08). **This is an amendment, not a new principle** — [ADR 0020](../adr/0020-review-invariant-reviewer-vs-partner.md) already carries the norm and the degradation semantics DR wants, including "degraded is not broken" and the requirement that the degraded state be *actively surfaced*. What it does not do is define the unit of distinctness, and the loose reading defeats the intent.

**The defect is one word.** ADR 0020 says the reviewer "runs a **distinct model** from the author" and speaks of "heterogeneous model **lineages**" without defining lineage. Under the literal reading, `claude-opus-4-1` reviewing `claude-sonnet-4-6` is heterogeneous: two distinct models. Under the intended reading it is homogeneous: one lab, one training lineage, highly correlated failure modes — which is precisely the correlation the invariant exists to break.

**The proposed decision text (DR, 2026-08-08).** Stated as the amendment should carry it, deliberately as a proxy rather than a technical claim:

> For Maestro's operational classification, a model's lineage is its **originating lab**. Serving provider and weight availability do not change it, and neither does fine-tuning or derivation — a fine-tune or derived model carries its base model's lineage. This is a **conservative proxy** for correlated training and alignment choices, not a claim that every model from one lab is technically identical. **Distinct-lineage review is preferred; same-lineage review remains valid but must be visibly marked degraded.**

Note the deliberate absence of "must use distinct lineages" — that would contradict warn-never-refuse below.

**Status matters: this is proposed, not operative.** ADR 0020 as written says "distinct model" and defines the degenerate case as author and reviewer *on the same model*. Under the text in force, `paired-default`'s Opus-plus-Sonnet pairing is **heterogeneous and conforming** — so nothing in the [conformance log](notes_conformance-log.md) violated ADR 0020. What can be said is conditional: **under the proposed lineage clarification, prior `paired-default` (paired-agent) runs would be classified as degraded.** The clarification becomes the operative rule only when ADR 0020 is amended and accepted.

**A separate, unconditional gap: the flagging machinery does not exist.** ADR 0020 already requires homogeneous review to be flagged and actively surfaced, and nothing implements that today — `mph/bundle.go` records the roles and `v1target/adapter.go` preserves them, but nothing computes heterogeneity at any granularity. So a genuinely same-*model* configuration would also go unflagged right now. That is a latent gap under the current rule, not a past violation, and it needs closing whichever way the lineage question lands.

**Two scope questions answered by DR, 2026-08-08.**

**Lineage is the lab; weight-openness, serving, and derivation do not change it.** An open-weight model shares a lineage with the same lab's closed-weight models — DeepSeek is the clean case, offering what is effectively the identical model in both forms — so `gpt-oss` is OpenAI-lineage even when Ollama serves it. **A fine-tune or derived model likewise carries its base model's lineage** (DR, 2026-08-08): fine-tuning does not undo the training-data ordering and alignment choices the proxy is standing in for. The rationale is what the amendment should encode, and it is why the proxy is drawn at the lab: shared lab-level training and alignment choices may correlate failures.

> ⚠️ **Implementation trap: `Provider` is not lineage.** `pkg/config`'s `Provider` field and `ProviderPatterns` describe *how a request is routed*, not who trained the model — `gpt-oss` is deliberately routed to `ProviderOllama` (ahead of the `gpt` → OpenAI rule) precisely because serving and origin differ. Building the lineage check on the existing `Provider` value would classify `gpt-oss` as Ollama-lineage and call an OpenAI-vs-OpenAI pairing heterogeneous. Lineage needs its own attribute.
>
> Worked consequence: `paired-local` pairs `gpt-oss:20b` (OpenAI lineage) with `qwen3-coder:30b` (Qwen/Alibaba lineage) — **cross-lineage and conforming**, though both route through Ollama. `paired-default` pre-2026-08-08 paired two Anthropic models through one provider — same lineage, degraded. Provider tells you neither answer.

**Same-lineage warns; it does not refuse.** "Degraded, not invalid" is load-bearing: there are legitimate deployments where distinct lineages are unavailable — high-security and sovereign-AI installations where only one lab is permitted, alongside ADR 0020's existing economic and airplane-mode cases. A hard failure would make those configurations unusable rather than honestly labelled. The bar the amendment must clear is that the warning is *visible*: what we have had until now is not an accepted degradation but an invisible one, which is the failure mode ADR 0020 explicitly forbids.

Remaining scope for the amendment:

- **Define the lineage attribute** — where it is declared, and how `human-<user_id>` principals classify (ADR 0020 already makes each human a distinct model; presumably each human is also a distinct lineage, which preserves the human-reviews-agent case).
- **Restate the degradation ladder** in lineage terms: distinct lineage → same lineage, distinct model → same model. ADR 0020 today has only the last rung.
- **Where it binds.** DR's "everywhere possible" spans the product's own reviewer routing (ADR 0020 calls that a Phase 5 deliverable) and the benchmark bundles (ADR 0025 makes heterogeneity benchmarkable). The benchmark half is cheap and can be applied immediately; the product half rides Phase 5.
- **Where the warning surfaces** — MPH bundle validation, the run record, the review record, or all three — given that it must be surfaced to the operator, not merely stored.

Blocks **Phase 5** (reviewer model routing), but the benchmark-side clarification should land sooner, since `paired-default` currently encodes the opposite of the intent and its comment says so.
