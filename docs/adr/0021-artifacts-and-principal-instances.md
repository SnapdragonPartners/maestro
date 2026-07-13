+++
title = "ADR 0021: Artifacts And Principal Instances"
edit_date = "2026-07-13"
status = "draft"
summary = "Defines the v2 artifact model: Management vs Audit categories, the scope/lineage signature, principal instances (agents and humans uniformly), amendment records, and lightweight provenance signatures."
+++

# 0021. Artifacts And Principal Instances

Status: Proposed

## Context

Artifacts are the unit of handoff in v2: chat feeds artifacts, every significant workflow node emits one, and memory lives in the data plane rather than in any agent's conversation (roadmap pillars 2–3). Three Accepted ADRs have queued obligations for this model: non-null lineage at every level of the work hierarchy (0018), amendment records for mid-flight changes to accepted artifacts (0018), and principal-generic author/reviewer references with human principals as first-class model identities (0020). This ADR fixes the conceptual shape; Phase 2 turns it into DDL.

## Decision

### Two artifact categories

- **Management artifacts** are for human review and comprehension, and carry the review invariant (ADR 0020): feature briefs, requirements, Epic plans, Story lists and plans, evidence packages, acceptance decisions, incident summaries, postmortems.
- **Audit artifacts** are durable, queryable records for debugging, reconstruction, and bulk analysis: tool calls, LLM call summaries, traces, metric events, checkpoints, message events (QUESTION/ANSWER and REQUEST/RESPONSE traffic is Audit data), compaction inputs/outputs.

Humans may inspect Audit artifacts; the UI summarizes and routes attention through Management artifacts. Model commentary and provider reasoning summaries are preserved as Audit data and never automatically reinjected into future context (roadmap risk: reasoning capture as context poison).

### The artifact signature

Every artifact carries:

- `artifact_id`
- `artifact_type` — from a governed vocabulary; aligns with the doc `type` convention (ADR 0017)
- `artifact_category` — `management` or `audit`
- `status`
- `scope_type` (`organization`, `product`, `feature`, `epic`, `story`, `benchmark`, ...) and `scope_id` — artifacts attach to a scope, never assume an Epic
- Denormalized lineage for querying: `product_id`, `feature_id`, `epic_id`, `story_id`, populated as far up the hierarchy as the scope implies; per ADR 0018, lineage is non-null at every level the scope covers (wrapper Features and the default Product guarantee this)
- `author_instance_id` and `reviewer_instance_id` — principal-generic references (see below); reviewer nullable only for Audit artifacts, which are not review-bearing
- `created_at`, `payload`, `schema_version`

Payloads are JSON with schema/version fields; Markdown is a rendering format, never the substrate. A one-line `summary` field serves triage and the artifact-row UI, mirroring the doc front-matter convention.

### Principal instances

The v1 notion of agent instance generalizes to the **principal instance**: one record type for anything that can author or review.

- Agent principals carry `agent_type`, `model`, `prompt_pack_id`, `prompt_hash`, `harness_config_hash`, `start_time`/`stop_time`/`stop_reason`, and scope lineage (`organization_id`; nullable `feature_id`/`epic_id`/`story_id` for scoped instances).
- Human principals are user accounts: each gets an instance record whose `model` is `human-<user_id>` — two distinct humans are two distinct models (ADR 0020), so authorship, review, and the heterogeneity record are uniformly checkable with no nulls or side channels.

The review invariant's data-plane expression: every Management artifact's `author_instance_id` ≠ `reviewer_instance_id`, and the pair's `model` values distinguish heterogeneous from homogeneous review.

### Amendments

Accepted artifacts are immutable. A mid-flight change — a requirements tweak during a Workbench loop, a Coder/Architect-agreed fix in factory mode — is an **amendment record**: a new artifact of type `amendment` whose `amends_artifact_id` links the original, carrying its own author, reviewer, and reason. The review invariant applies to amendments exactly as to originals (ADR 0020). The effective content of an artifact is its original plus accepted amendments in order; consumers read the effective view, auditors read the chain.

### Lightweight provenance signatures

Each artifact's provenance binds: the author principal instance, model, prompt hash, harness config hash, input artifact digests, and the output payload digest (plus the reviewer's, when reviewed). Content digests, not cryptographic signing — the roadmap defers cryptographic signatures until a concrete compliance requirement appears.

### Where artifacts live

Per roadmap D5: the database is canonical for artifacts, relationships, instances, and metrics; binary attachments are content-addressed in the data plane/object storage; repo files are project artifacts only. Exact schema families and DDL are Phase 2 work — this ADR fixes shapes and invariants, not tables.

## Consequences

- Phase 2's core schema derives mechanically from this ADR plus 0018's hierarchy; the golden story runner's results can be imported as `benchmark`-scoped artifacts.
- Evidence packages become Management artifacts composed of references to Audit artifacts — proof by pointer, not by copy.
- The UI's artifact timeline and one-line-row pattern fall out of `summary` + `artifact_type` + `status`.
- Cost/metrics analysis joins through principal instances, which is also where MPH comparisons (prompt hash, harness hash, model) anchor.
- The `artifact_type` vocabulary needs the same governance as doc types (ADR 0017): prefer reuse; add a type only for a repeated class.

## Related Documents

- [ADR 0018](0018-v2-work-taxonomy.md) (hierarchy, lineage, amendment obligation), [ADR 0020](0020-review-invariant-reviewer-vs-partner.md) (principal-based invariant, heterogeneity record), [ADR 0017](0017-v2-documentation-authority-and-lifecycle.md) (type/summary conventions).
- [Roadmap](../v2/roadmap.md) pillars 2–3 (artifact categories, signatures, agent instances), D5 (repo vs database), risk notes on artifact volume and reasoning capture.
- Historical note [0005](0005-sqlite-session-persistence-and-resume.md) (v1 persistence; superseded for v2 design intent by this ADR and the forthcoming data-plane ADR).
