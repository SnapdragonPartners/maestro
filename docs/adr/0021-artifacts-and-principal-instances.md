+++
title = "ADR 0021: Artifacts And Principal Instances"
edit_date = "2026-07-15"
status = "live"
summary = "Defines the v2 artifact model: artifacts as the sole agent handoff, Management (inputs) vs Audit (exhaust) categories, the scope/lineage signature, principal instances (agent/human/system), the invalidate/amend/supersede lifecycle, evidence retention-pinning, and the MPH signature."
+++

# 0021. Artifacts And Principal Instances

Status: Accepted (Codex + DR, 2026-07-13)

## Context

Artifacts are the unit of handoff in v2: chat feeds artifacts, every significant workflow node emits one, and memory lives in the data plane rather than in any agent's conversation (roadmap pillars 2–3). Three Accepted ADRs have queued obligations for this model: non-null lineage at every level of the work hierarchy (0018), amendment records for mid-flight changes to accepted artifacts (0018), and principal-generic author/reviewer references with human principals as first-class model identities (0020). This ADR fixes the conceptual shape; Phase 2 turns it into DDL.

## Decision

### Artifacts are the handoff

Every agent is seeded at startup with one or more artifacts paired to its prompt template, and that seed must be sufficient to commence the task. There is no LLM-context sharing between agents — ever. Whatever an agent later does to clarify (QUESTION/ANSWER, knowledge lookups, workspace exploration) is clarification, not seeding. The seeding set is recorded as the instance's input artifact digests in the MPH signature below, so "what was this agent given to start?" is always a query, never a mystery.

The handoff has a **promotion boundary**. Message traffic (QUESTION/ANSWER, REQUEST/RESPONSE) is Audit exhaust and is never delivered as authoritative seed or context to downstream reviewers or consumers — only an artifact's effective view is. When an exchange changes intent, requirements, or acceptance — a Coder discovers a dependency cannot be satisfied without an out-of-scope change — the change is promoted through an **amendment request**: a REQUEST carrying the proposed amendment and its justification, whose approving RESPONSE constitutes the amendment's review (author = requester, reviewer = approver, satisfying ADR 0020 by construction). The Orchestrator then delivers the updated effective view to every subsequent consumer — deterministic delivery, no inference (ADR 0019). Unreviewed exhaust can never quietly become the real spec.

### Two artifact categories

The classification test is functional, not audience-based:

- **Management artifacts** are inputs: artifacts consumed by a subsequent task, agent, or decision — feature briefs, requirements, Epic plans, Story lists and plans, knowledge packs, evidence packages, acceptance decisions, incident summaries, postmortems. Because flawed inputs propagate, Management artifacts carry the review invariant (ADR 0020). Human review and comprehension is common but not defining: a knowledge pack is a Management artifact that humans rarely read.
- **Audit artifacts** are exhaust: durable, queryable records of what happened — tool calls, LLM call summaries, traces, metric events, checkpoints, message events (QUESTION/ANSWER and REQUEST/RESPONSE traffic is Audit data), compaction inputs/outputs. Their value is failure reconstruction and optimization fodder; they are queryable, durable log files.

Humans may inspect Audit artifacts; the UI summarizes and routes human attention through Management artifacts. Model commentary and provider reasoning summaries are preserved as Audit data and never automatically reinjected into future context (roadmap risk: reasoning capture as context poison).

### The artifact signature

Every artifact carries:

- `artifact_id`
- `artifact_type` — from a governed vocabulary; aligns with the doc `type` convention (ADR 0017)
- `artifact_category` — `management` or `audit`
- `status`
- `scope_type` (`organization`, `product`, `feature`, `epic`, `story`, `benchmark`, ...) and `scope_id` — artifacts attach to a scope, never assume an Epic
- Denormalized lineage for querying: `product_id`, `feature_id`, `epic_id`, `story_id`, populated as far up the hierarchy as the scope implies; per ADR 0018, lineage is non-null at every level the scope covers (wrapper Features and the default Product guarantee this)
- `author_instance_id` and `reviewer_instance_id` — principal-generic references (see below). Management artifacts require agent or human principals on both sides; Audit artifacts may be authored by any principal kind (including system principals) and their reviewer is null — Audit data is not review-bearing
- `created_at`, `payload`, `schema_version`

Payloads are JSON with schema/version fields; Markdown is a rendering format, never the substrate. A one-line `summary` field serves triage and the artifact-row UI, mirroring the doc front-matter convention.

### Principal instances

The v1 notion of agent instance generalizes to the **principal instance**: one record type for anything that can produce, author, or review. Three principal kinds:

- **Agent** principals carry `agent_type`, `model`, `prompt_pack_id`, `prompt_hash`, `harness_config_hash`, `start_time`/`stop_time`/`stop_reason`, and scope lineage (`organization_id`; nullable `feature_id`/`epic_id`/`story_id` for scoped instances).
- **Human** principals are user accounts: each gets an instance record whose `model` is `human-<user_id>` — two distinct humans are two distinct models (ADR 0020), so authorship, review, and the heterogeneity record are uniformly checkable with no nulls or side channels.
- **System** principals are Orchestrator components — the persistence worker, tool runtime, scheduler, metric collector — with `model` = `system-<component>`. System principals produce Audit artifacts (tool calls, traces, metric events, checkpoints, message events) but can never satisfy the Management review invariant, as author or reviewer: per ADR 0019 they perform no inference, so there is no judgment to review or to review with.

Mechanical assembly is not authorship. When the Orchestrator deterministically emits a Management artifact — the auto-created wrapper Feature at degenerate entry, an evidence package assembled from story records — `author_instance_id` is the **accountable** agent or human principal whose action or workflow step caused the emission: the human who submitted the degenerate entry; the agent whose completion emitted the evidence. The review invariant binds that accountable principal. The system principal that performed the assembly is recorded as producer in the provenance trail, and the assembly event itself is Audit data.

The review invariant's data-plane expression: every accepted Management artifact carries an agent or human author, a distinct agent or human reviewer, and a completed review record. Reviewer identity alone is not review completion: a Management artifact persists as `draft` — working state with no authority — until its review record completes (decision, reviewer principal, `accepted_at`); only then does it become accepted and authoritative. The author/reviewer pair's `model` values distinguish heterogeneous from homogeneous review.

### Artifact lifecycle: invalidate, amend, supersede

Accepted artifacts are immutable. Three operations cover change, split by when and how much:

- **Invalidation (before acceptance only).** Fundamental flaws found in review — wrong approach, wrong design — invalidate the draft; a replacement artifact starts a fresh chain (optionally linked via `replaces_artifact_id` for traceability). Invalidated drafts are retained as history with no authority. Never amend a draft into acceptability: consumers read effective views as agent seeds, and a flawed base plus corrective amendments is context bloat delivered as input.
- **Amendment (after acceptance, small changes).** A mid-flight tweak — a requirements adjustment during a Workbench loop, a Coder/Architect-agreed fix discovered in implementation — is an **amendment record**: a new artifact of type `amendment` whose `amends_artifact_id` links the original, carrying its own author, reviewer, and reason. The review invariant applies to amendments exactly as to originals (ADR 0020).
- **Supersession (after acceptance, fundamental changes).** When an accepted artifact is proven fundamentally wrong (e.g. by UAT), it is not amended into a new shape — a new artifact with `supersedes_artifact_id` goes through full review, and the old artifact is marked `superseded`. The effective view never spans a supersession. This mirrors the ADR lifecycle itself (Accepted → Superseded).

Effective-view semantics are deterministic:

- Amendments target the original artifact only — the chain is flat; there are no amendments of amendments. Correcting an earlier amendment means a later amendment.
- On acceptance, each amendment receives `accepted_at` and a monotonic per-original sequence number; the sequence is the total order.
- Consumers apply accepted amendments in sequence order; where amendments conflict, the later prevails.
- The effective view is original plus accepted amendments in sequence; auditors read the full chain, including rejected amendments and superseded lineage.

The minimal `status` vocabulary is fixed here so Phase 2 does not invent it: Management artifacts move `draft` → (`invalidated` | `accepted`) → (`superseded` | `archived`). `accepted` is the only authoritative state (it corresponds to `live` in the document vocabulary, ADR 0017), and amendments never change the original's status — they change its effective view. Audit artifacts are born final and have no lifecycle; retention pinning is a property, not a status. Phase 2 may extend this vocabulary, never repurpose it.

### The MPH signature

Each artifact's provenance is its **MPH signature**, binding all three factory levers plus the data that flowed through them: the author principal instance; **M** — the model; **P** — the prompt pack and prompt hash; **H** — the Maestro version and the harness config hash (the app is the harness, so the binary's version belongs in H); the input artifact digests (the seeding set); and the output payload digest — plus the reviewer's, when reviewed. Content digests, not cryptographic signing — the roadmap defers cryptographic signatures until a concrete compliance requirement appears.

### Evidence references and retention

Evidence packages prove by pointer, and pointers must not rot. An evidence reference binds `artifact_id` plus the referenced payload's digest (and version, for amended artifacts: the effective-view sequence point it cites). While an evidence package is authoritative (accepted, and its Epic's acceptance stands), every Audit artifact and binary attachment it references is **retention-pinned**: Audit retention and compaction may prune only unpinned records. Digest binding means even a retention bug is detectable — a dangling or altered reference fails verification rather than silently weakening the proof.

### Where artifacts live

Per roadmap D5: the database is canonical for artifacts, relationships, instances, and metrics; binary attachments are content-addressed in the data plane/object storage; repo files are project artifacts only.

Management and Audit artifacts live in **separate storage families** (Phase 2: separate tables): Audit volume dwarfs Management volume, and the two have opposite retention postures. Audit storage is truncatable by design — subject to retention pins, which are the one deliberate inbound dependency (evidence references), and whose violation the digest check detects loudly. Management storage is durable long-term. Exact schema families and DDL are Phase 2 work — this ADR fixes shapes and invariants, not tables.

## Consequences

- Phase 2's core schema derives mechanically from this ADR plus 0018's hierarchy; the golden story runner's results can be imported as `benchmark`-scoped artifacts.
- Evidence packages become Management artifacts composed of references to Audit artifacts — proof by pointer, not by copy.
- The UI's artifact timeline and one-line-row pattern fall out of `summary` + `artifact_type` + `status`.
- Cost/metrics analysis joins through principal instances, which is also where MPH comparisons (prompt hash, harness hash, model) anchor.
- The `artifact_type` vocabulary needs the same governance as doc types (ADR 0017): prefer reuse; add a type only for a repeated class.

## Related Documents

- [ADR 0018](0018-v2-work-taxonomy.md) (hierarchy, lineage, amendment obligation), [ADR 0020](0020-review-invariant-reviewer-vs-partner.md) (principal-based invariant, heterogeneity record), [ADR 0017](0017-v2-documentation-authority-and-lifecycle.md) (type/summary conventions).
- [Roadmap](../v2/plan_roadmap.md) pillars 2–3 (artifact categories, signatures, agent instances), D5 (repo vs database), risk notes on artifact volume and reasoning capture.
- Historical note [0005](0005-sqlite-session-persistence-and-resume.md) (v1 persistence; superseded for v2 design intent by this ADR and the forthcoming data-plane ADR).
