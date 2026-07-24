+++
title = "ADR 0028: Artifact Envelopes And Payload Schemas"
edit_date = "2026-07-24"
status = "draft"
summary = "The encoding layer under ADR 0021's artifact model: a fixed relational envelope plus a typed JSON payload, digested with RFC 8785 JCS under a numeric-range constraint that keeps large integers and exact decimals in strings; a code-resident payload type registry validated at the persistence seam on write; additive-within-version schema evolution where the reader is the only compatibility layer, because accepted artifacts are immutable; amendments encoded as RFC 7386 merge patches whose resulting effective payload is validated on write and again at acceptance, materialized on read and never stored; and review records bound to a digest of the whole reviewable projection, not the payload alone."
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

**Digests are computed over RFC 8785 (JCS) canonical JSON**, then SHA-256, rendered lowercase 64-hex. Every digest ADR 0021 requires — the MPH input-artifact seeding set, the output payload digest, evidence-reference payload digests — uses this one function, from a real JCS implementation, not a hand-rolled approximation. Canonicalization is not optional polish: ADR 0021's evidence references detect tampering and retention bugs by digest comparison, and a digest that moves when a serializer reorders keys detects nothing.

RFC 8785 carries a numeric constraint that this ADR adopts explicitly rather than discovering later. JCS serializes numbers per ECMAScript `Number::toString` — IEEE 754 doubles — so integers beyond 2^53 and exact decimals do not survive canonicalization. **Payload schemas therefore may not carry JSON numbers outside the exactly-representable range**: large integers (nanosecond timestamps are already ~1.8×10^18, two orders of magnitude past 2^53), identifiers, and exact monetary decimals are encoded as **strings**. This is a registry-enforced validation rule (§2), which converts a silent precision-loss trap into a write-time error.

**Phase 1's runner uses a different function, and the two must never be conflated.** `benchmark/internal/contenthash.CanonicalJSON` marshals, re-decodes with `UseNumber`, and re-marshals through Go's `encoding/json`: keys land in Go's byte-wise order rather than JCS's UTF-16 code-unit order, HTML escaping is on, and numbers are preserved as literal text — which is precisely the arbitrary-precision behavior JCS does not have. It is not an RFC 8785 implementation and is not becoming one: its outputs are pinned by `TestV1HashesArePinned`, and changing them would move the story content hashes the Phase 1 baseline was recorded against. The runner keeps its scheme; the data plane uses JCS; they are separate hash domains. Runner-produced identities entering the plane through the Phase 2 import are stored as **opaque identifier strings** — data to be preserved and compared for equality, never re-derived or verified with the plane's digest function.

### 2. Payload type registry and validation

`artifact_type` selects the payload schema. The mapping is a **registry resident in code**, not in data: a Go registry associating each `artifact_type` with its current `schema_version`, its validator, and its category. A type that is not registered cannot be written.

Code-resident is the deliberate choice. ADR 0021 requires `artifact_type` to be a *governed* vocabulary — "prefer reuse; add a type only for a repeated class" — and governance means a reviewed pull request, not a runtime insert. A data-resident registry would let an agent mint an artifact type through a tool call, which is precisely the unreviewed side door ADR 0022's guardrail closes for state transitions.

**Validation happens at the persistence seam, on write, before the row commits** — the single choke point ADR 0022's access discipline already establishes. Not in the agent, not in the tool, not asynchronously: an invalid payload must never reach storage, because immutability means it can never be cleaned up afterward. Validation failure is an error returned to the caller, and the write does not happen.

Validation is two checks, both mandatory. First, the payload conforms to its registered schema for its `schema_version`. Second, **the canonicalization constraint from §1**: no JSON number outside the IEEE-754 exactly-representable integer range, so nothing in a stored payload can lose precision under JCS. The second check is universal — it belongs to the encoding, not to any one schema — and it fails loudly at write time rather than silently at digest time.

For amendments, the unit validated is the resulting effective payload, not the patch (§4).

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

**Always applying is not the same as always producing a valid payload, and the difference is load-bearing.** A merge patch can delete a required field or replace a value with one of the wrong type, and the result would be an *accepted, authoritative* effective view that violates its own schema — the one thing §2's write-time validation exists to prevent, arriving through a side door. Therefore:

- The unit validated for an amendment is **the resulting effective payload**, not the patch document. The patch alone is not a valid instance of anything and must never be schema-checked in isolation.
- That validation runs against the **original artifact's `artifact_type` and `schema_version`** — an amendment cannot change either. A change that needs a different type or version is a supersession, not an amendment.
- Validation runs **twice: on write, and again at acceptance**, and both must pass. Re-validation at acceptance is not belt-and-braces — sequence numbers are assigned at acceptance, so an amendment drafted against one effective view can be accepted onto a different one if another amendment is accepted in between. The base it was written against is not necessarily the base it applies to.
- An amendment that fails either check cannot be accepted. Per ADR 0021, a draft with a fundamental flaw is invalidated rather than repaired.

One residual limitation is recorded rather than solved: when an intervening amendment shifts the base, the reviewer's *judgment* was formed against a view that no longer exists, even though the result still validates. ADR 0021 resolves amendment conflicts by sequence order ("the later prevails") and does not require re-review, so this ADR does not impose one — schema validity is enforced mechanically, semantic staleness under concurrent amendment is not. If it proves real in practice, the fix belongs in 0021's conflict rule, not here.

Supersession needs no encoding beyond ADR 0021's link: a superseding artifact is a complete, independently reviewed payload, and per 0021 the effective view never spans a supersession.

### 5. Review linkage

A review record binds to **the artifact's `review_digest`** — a canonical digest over the whole *reviewable projection*, not over `payload` alone.

Binding to the payload alone is too narrow, because review-relevant facts live in the envelope. An artifact's `artifact_type`, `artifact_category`, `scope_type`/`scope_id`, lineage, `author_instance_id`, `summary`, and `schema_version` all change what a reviewer is agreeing to; a payload-only binding would let any of them move after review without detection. Re-scoping an accepted artifact to a different Epic, or reassigning its author, is exactly the kind of change the invariant should catch.

The reviewable projection is therefore **every envelope field except those the review process itself writes**, plus the payload:

- **Included**: `artifact_id`, `artifact_type`, `artifact_category`, `scope_type`, `scope_id`, the lineage columns, `author_instance_id`, `summary`, `schema_version`, `payload` (for an amendment, its merge-patch document).
- **Excluded**: `status`, `reviewer_instance_id`, and the review record's own timestamps. These are outputs of review, not inputs to it. Including them would be circular — accepting an artifact moves `status` from `draft` to `accepted`, which would change the digest and instantly invalidate the review that caused the change.

`review_digest` is computed with the same JCS function as every other digest (§1). An artifact reaches status `accepted` only when a review record with decision `accepted` exists **for its current `review_digest`**, authored by a principal distinct from the author and of kind agent or human (ADR 0021 — system principals can neither author nor review Management artifacts). Any mismatch fails the transition rather than warning.

This is what makes "reviewer identity alone is not review completion" (ADR 0021) mechanically true rather than aspirational: review of changed content is detectably absent by construction, across the whole reviewable surface rather than just its largest field.

Review records carry: the artifact reference and its `review_digest`, the reviewer principal instance, a decision from the fixed vocabulary `accepted` | `rejected` | `changes_requested`, a free-text rationale, and `decided_at`.

Amendments are artifacts, so they carry their own review records under the same rule, satisfying ADR 0020's invariant for each link in the chain independently. Audit artifacts have no review records and no reviewer; that is a property of the category, not a nullable field anyone should read meaning into.

## Consequences

- Phase 2's DDL is now fully determined: the envelope is a column list, `payload` is `jsonb`, and the review record's shape is fixed. Item 3 writes tables, not designs.
- **The reader-is-the-compatibility-layer rule is the sharpest constraint this ADR imposes.** Every consumer of an artifact type inherits a growing obligation as versions accumulate, which is intended pressure toward additive evolution and small payloads. It also means version proliferation is a design smell with a measurable cost, visible in registry entries.
- Digest-bound review closes the "reviewed a different draft" hole by construction, but it also means an editorial fix to an accepted artifact is impossible — it is an amendment, with its own review. Binding to the whole reviewable projection widens this: a `summary` typo or a scope correction is as unfixable-in-place as a payload change. This is ADR 0021's immutability working as designed, and it will feel heavy at typo scale.
- Because amendments are validated as *effective payloads* twice — on write and again at acceptance — an amendment's acceptance can fail for reasons that did not exist when it was written, if another amendment lands first. That is a real operational case Phase 2's queries must surface clearly, not a rare edge.
- The JCS numeric constraint pushes large integers and exact decimals into strings at the payload-schema level. Payload designers inherit that as a rule, and the registry enforces it, so the trap surfaces at write time in development rather than as silent precision loss in stored history.
- Merge Patch's array-replacement semantics mean list-shaped payloads are amended by restating the list. Payload designers should prefer keyed objects over positional arrays where per-item amendment is expected.
- The registry gives the `artifact_type` vocabulary a single enforceable home, so ADR 0021's governance requirement becomes a code-review checkpoint rather than a convention.
- TOML/YAML remain valid authoring formats at the edges without becoming storage formats, so ADR 0025's TOML story definitions and the prompt-pack fragments are unaffected.

## Related Documents

- [ADR 0021](0021-artifacts-and-principal-instances.md) — the model this encodes: signature, categories, lifecycle, amendment semantics, MPH, retention. Binding; where this ADR appears to diverge, 0021 wins.
- [ADR 0022](0022-v2-data-plane.md) (persistence seam as the validation choke point, access discipline, Phase 2 scope), [ADR 0020](0020-review-invariant-reviewer-vs-partner.md) (the review invariant this encodes), [ADR 0019](0019-orchestrator-boundary.md) (validation is Orchestrator machinery, not agent judgment), [ADR 0017](0017-v2-documentation-authority-and-lifecycle.md) (the `type` vocabulary `artifact_type` aligns with).
- [Phase 2 plan](../v2/phase_2/plan_scope.md) — item 1 is this ADR; items 3 and 4 consume it.
- [ADR backlog](../v2/notes_adr-backlog.md) — candidate 1, resolved by this ADR.
