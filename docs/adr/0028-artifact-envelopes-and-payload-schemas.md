+++
title = "ADR 0028: Artifact Envelopes And Payload Schemas"
edit_date = "2026-07-24"
status = "draft"
summary = "The encoding layer under ADR 0021's artifact model: a fixed relational envelope plus a typed JSON payload; a code-resident payload type registry validated at the persistence seam on write; additive-within-version schema evolution where the reader is the only compatibility layer, because accepted artifacts are immutable; amendments encoded as RFC 7386 merge patches applied in sequence order to materialize the effective view on read; and review records bound to the exact payload digest they reviewed."
+++

# 0028. Artifact Envelopes And Payload Schemas

Status: Draft — proposed 2026-07-24, Phase 2 item 1.

## Context

[ADR 0021](0021-artifacts-and-principal-instances.md) fixed the artifact *model*: the signature fields, the two categories, principal instances, the `draft` → (`invalidated` | `accepted`) → (`superseded` | `archived`) lifecycle, flat amendment chains with a monotonic per-original sequence, review records where reviewer identity alone is not review completion, and the MPH signature over input and output digests. It deliberately stopped at shapes and invariants, leaving the DDL to Phase 2.

Phase 2 cannot write that DDL yet, because a layer is missing between the model and the tables: how an artifact is actually *encoded*. What is a column and what is payload; how a payload's type is known and checked; how a payload schema changes without invalidating history; how a flat amendment chain becomes the single effective view consumers are seeded with; and how a review binds to the exact content it reviewed. These are the five decisions [ADR backlog candidate 1](../v2/notes_adr-backlog.md) carries, and the [Phase 2 plan](../v2/phase_2/plan_scope.md) schedules them as item 1, before any DDL merges.

The stakes are set by ADR 0021's immutability rule. Accepted artifacts never change, which means **no encoding mistake is ever fixable by a data migration** — the usual escape hatch is closed by design. Whatever this ADR gets wrong is carried forward in stored history for the life of the system, so the encoding is chosen to be small, boring, and standards-based rather than expressive.

## Decision

### 1. Envelope and payload

An artifact is a **fixed relational envelope** plus one **typed JSON payload**.

The envelope is exactly ADR 0021's signature, as real columns: `artifact_id`, `artifact_type`, `artifact_category`, `status`, `scope_type`, `scope_id`, the denormalized lineage columns, `author_instance_id`, `reviewer_instance_id`, `created_at`, `summary`, `schema_version`, and `payload`. Envelope fields are the ones the Orchestrator queries, joins, and enforces invariants on; they are never duplicated inside the payload, and the payload is never consulted to answer an envelope question. A field that the system must filter, join, or gate on is an envelope field by definition — payload is what only the artifact's consumers interpret.

`payload` is JSON (Postgres `jsonb`). JSON is the storage and API canonical format for every artifact of every type. Markdown is a **rendering** format produced from a payload for human display, never the substrate and never parsed back. TOML and YAML are permitted only for prompt-facing fragments authored by humans (story definitions, prompt pack fragments); they are converted to JSON at the seam and stored as JSON, so nothing downstream ever branches on source format.

**Digests are computed over RFC 8785 canonical JSON** (JCS: lexicographic key ordering, no insignificant whitespace, defined number serialization), then SHA-256, lowercase 64-hex. Every digest ADR 0021 requires — the MPH input-artifact seeding set, the output payload digest, evidence-reference payload digests — uses this one function. Canonicalization is not optional polish: ADR 0021's evidence references detect tampering and retention bugs by digest comparison, and a digest that moves when a serializer reorders keys detects nothing. Phase 1's runner already hashes this way, including integer-precision handling; Phase 2 uses the same discipline rather than a second one.

### 2. Payload type registry and validation

`artifact_type` selects the payload schema. The mapping is a **registry resident in code**, not in data: a Go registry associating each `artifact_type` with its current `schema_version`, its validator, and its category. A type that is not registered cannot be written.

Code-resident is the deliberate choice. ADR 0021 requires `artifact_type` to be a *governed* vocabulary — "prefer reuse; add a type only for a repeated class" — and governance means a reviewed pull request, not a runtime insert. A data-resident registry would let an agent mint an artifact type through a tool call, which is precisely the unreviewed side door ADR 0022's guardrail closes for state transitions.

**Validation happens at the persistence seam, on write, before the row commits** — the single choke point ADR 0022's access discipline already establishes. Not in the agent, not in the tool, not asynchronously: an invalid payload must never reach storage, because immutability means it can never be cleaned up afterward. Validation failure is an error returned to the caller, and the write does not happen.

Audit artifacts are validated identically. Their volume argues for cheap validation, not for skipped validation — an unparseable tool-call record is worthless exactly when it matters, during failure reconstruction.

### 3. Schema evolution

Each payload type carries an integer `schema_version`, stored on the envelope, set at write time from the registry.

Within a version, changes are **additive only**: new optional fields may be added; existing fields may never be removed, renamed, retyped, or have their meaning changed. Anything else is a **new version**.

Because accepted artifacts are immutable, **stored payloads are never rewritten to a newer version — there are no data migrations for artifact payloads, ever.** The consequence is unavoidable and is stated here so nobody designs against it later: **the reader is the only compatibility layer.** A consumer of an artifact type must handle every version of that type that has ever been written, or explicitly refuse the ones it cannot. Registry entries therefore declare the range of versions they can read, and reading an out-of-range payload is an error, never a silent partial parse.

This makes new versions genuinely expensive, which is the intent — the pressure is toward additive evolution, and toward getting a payload right before it accumulates history. Where a version boundary is truly needed, the honest move is often a **new artifact type** rather than a new version of an old one.

### 4. Amendment and effective-view encoding

ADR 0021 fixed the semantics: amendments target the original only (the chain is flat), receive a monotonic per-original sequence number on acceptance, apply in sequence order, and where they conflict the later prevails. This ADR fixes the encoding that makes "conflict" and "later prevails" mean something precise.

An amendment's payload is an **RFC 7386 JSON Merge Patch** against the original artifact's payload:

- Present keys replace; `null` removes; absent keys leave the target untouched. Arrays replace wholesale.
- The **effective view** is the original payload with each accepted amendment's merge patch applied in sequence order. "Later prevails" is therefore per-field and exact: two amendments touching different fields both survive; two touching the same field resolve to the higher sequence number.
- The effective view is **materialized on read, never stored.** Storing it would create a second copy of the truth that could drift from the chain, and would have to be rewritten on each acceptance — mutation of accepted state by the back door.
- Rejected amendments and amendments still in `draft` are never applied. Auditors read the full chain including them; consumers read only the effective view.

Merge Patch is chosen over RFC 6902 JSON Patch deliberately. JSON Patch is more expressive — it can address array elements — but its operations can *fail to apply* against a changed target and are order-sensitive in ways that make a stored patch a latent runtime error. Merge Patch always applies, deterministically, with no failure mode. The cost is that amending one element of a list means resubmitting the list; for the artifacts this model carries (requirements, story lists, plans) restating the list is the honest and reviewable thing to do anyway.

Supersession needs no encoding beyond ADR 0021's link: a superseding artifact is a complete, independently reviewed payload, and per 0021 the effective view never spans a supersession.

### 5. Review linkage

A review record binds to **the artifact and the exact payload digest it reviewed** — `artifact_id` plus `payload_digest`, not `artifact_id` alone.

This is what makes "reviewer identity alone is not review completion" (ADR 0021) mechanically true rather than aspirational. A review that named only an artifact would silently carry over to content the reviewer never saw. With the digest bound, review of changed content is detectably absent, and any attempt to accept an artifact whose current payload digest does not match a completed review record fails.

Review records carry: the artifact reference and payload digest, the reviewer principal instance, a decision from the fixed vocabulary `accepted` | `rejected` | `changes_requested`, a free-text rationale, and `decided_at`. An artifact reaches envelope status `accepted` only when a review record with decision `accepted` exists for its current payload digest, authored by a principal distinct from the author and of kind agent or human (ADR 0021 — system principals can neither author nor review Management artifacts).

Amendments are artifacts, so they carry their own review records under the same rule, satisfying ADR 0020's invariant for each link in the chain independently. Audit artifacts have no review records and no reviewer; that is a property of the category, not a nullable field anyone should read meaning into.

## Consequences

- Phase 2's DDL is now fully determined: the envelope is a column list, `payload` is `jsonb`, and the review record's shape is fixed. Item 3 writes tables, not designs.
- **The reader-is-the-compatibility-layer rule is the sharpest constraint this ADR imposes.** Every consumer of an artifact type inherits a growing obligation as versions accumulate, which is intended pressure toward additive evolution and small payloads. It also means version proliferation is a design smell with a measurable cost, visible in registry entries.
- Digest-bound review closes the "reviewed a different draft" hole by construction, but it also means an editorial fix to an accepted artifact's payload is impossible — it is an amendment, with its own review. This is ADR 0021's immutability working as designed, and it will feel heavy for typo-scale changes.
- Merge Patch's array-replacement semantics mean list-shaped payloads are amended by restating the list. Payload designers should prefer keyed objects over positional arrays where per-item amendment is expected.
- The registry gives the `artifact_type` vocabulary a single enforceable home, so ADR 0021's governance requirement becomes a code-review checkpoint rather than a convention.
- TOML/YAML remain valid authoring formats at the edges without becoming storage formats, so ADR 0025's TOML story definitions and the prompt-pack fragments are unaffected.

## Related Documents

- [ADR 0021](0021-artifacts-and-principal-instances.md) — the model this encodes: signature, categories, lifecycle, amendment semantics, MPH, retention. Binding; where this ADR appears to diverge, 0021 wins.
- [ADR 0022](0022-v2-data-plane.md) (persistence seam as the validation choke point, access discipline, Phase 2 scope), [ADR 0020](0020-review-invariant-reviewer-vs-partner.md) (the review invariant this encodes), [ADR 0019](0019-orchestrator-boundary.md) (validation is Orchestrator machinery, not agent judgment), [ADR 0017](0017-v2-documentation-authority-and-lifecycle.md) (the `type` vocabulary `artifact_type` aligns with).
- [Phase 2 plan](../v2/phase_2/plan_scope.md) — item 1 is this ADR; items 3 and 4 consume it.
- [ADR backlog](../v2/notes_adr-backlog.md) — candidate 1, resolved by this ADR.
