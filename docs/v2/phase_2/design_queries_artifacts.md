+++
title = "Design: Artifact And Principal Queries (Item 4)"
edit_date = "2026-07-26"
status = "draft"
summary = "Mini-plan for Phase 2 item 4: the persistence interface and its local module — registry and safe-integer validation at the seam, JCS digest construction, version-bounded reads, transitions classified under a row lock with their preconditions also in the UPDATE, amendment acceptance serialized per original, effective views assembled in Go against the RFC's own vectors, and MPH capture and query."
type = "design"
+++

# Design: Artifact And Principal Queries (Item 4)

Status: **draft** — for Codex review before the queries land. Revised after round 1 (five P1s).

Implements [Phase 2 plan](plan_scope.md) item 4 over the schema from [item 3](design_schema_core.md), under [ADR 0028](../../adr/0028-artifact-envelopes-and-payload-schemas.md) (encoding, validation, acceptance, amendments), [ADR 0021](../../adr/0021-artifacts-and-principal-instances.md) (lifecycle, MPH) and [ADR 0022](../../adr/0022-v2-data-plane.md) (persistence interface, access discipline).

**Item 4 discharges the two obligations ADR 0028 accepted in exchange for keeping the acceptance rule at the seam rather than in a trigger**: no generic status update is exposed, and every transition is tested individually for its own precondition. If this item does not honour them, that trade was wrong in retrospect.

## What item 4 owes

The first draft of this document covered transitions and effective views and silently omitted most of the write/read surface. In full:

**Write path** — create artifact (Management and Audit), create review record, create principal instance with its MPH seeding set, stop a principal instance.

**Validation at the seam** (ADR 0028 §2, which places it here and nowhere else) — payload conforms to its registered schema for its `schema_version`; the universal safe-integer rule; for amendments, the resulting *effective payload* rather than the patch.

**Digest construction** — `payload_digest` and `review_digest` computed at the seam over JCS canonical JSON, never supplied by callers.

**Read path** — artifact by id, by scope, by lineage; effective view; review records; principal instance with inputs; MPH queries (by model, prompt hash, harness hash). Reads are **version-bounded**: the registry declares which `schema_version` range a consumer can read, and an out-of-range payload is an error rather than a partial parse.

**Transitions** — accept, invalidate, supersede, archive, each named and each carrying its own preconditions.

## Decisions

### D1. The persistence interface is built now

**Reversed from round 1**, which proposed deferring it until a second implementation existed. That contradicted the approved plan, which says in its own words: *"Phase 2 builds the interface and its local implementations standing alone."* ADR 0022 makes it the seam where deployment modules swap, and local-versus-cloud is an established multi-backend need rather than a speculative one — which is exactly the case `CLAUDE.md`'s anti-abstraction rule admits.

Shape:

- `internal/dataplane/store` defines a **narrow interface** covering the surface above — not a mirror of every generated method, since an interface that grows with the schema is one nobody can implement twice.
- `internal/dataplane/store/postgres` is the local module: sqlc `gen` plus the seam logic here.
- Queries stay in `internal/dataplane/queries/*.sql`, one file per family.

The interface is what the Phase 3 Orchestrator and item 9's importer depend on. Item 6 adds the object module behind the same interface.

### D2. Transaction composition belongs to item 4

Not deferrable to item 6, because item 4 already needs it in two places: the principal instance and its seeding set must be created atomically (D7), and amendment acceptance must lock, assemble, validate and update in one transaction (D6).

`store` exposes a `WithTx(ctx, func(Tx) error)` style facility; every multi-statement operation runs inside one. Item 6 builds the cross-store commit order (object first, pin recorded, row last) **on** this rather than inventing its own.

### D3. Validation and digests at the seam

On every artifact write, in this order:

1. **Registry lookup** by `artifact_type`, which is the *only* one of these a caller supplies. ADR 0028 §2 defines the registry as type → **category, current `schema_version`, validator**, so the seam takes all three from it. A caller choosing its own category could write a Management artifact into the Audit family; a caller choosing its own version could claim one whose validator does not match the payload. Unregistered types cannot be written at all.
2. **Schema validation of the instance that will be stored.** For an original that is the payload itself. **For an amendment it is the merged effective payload, never the patch** — a merge patch is not an instance of the artifact's schema and validating it as one would reject every legitimate amendment while accepting patches that break the result. The patch is what gets stored; the merged result is what gets validated (and validated again at acceptance, D6).
3. **The universal safe-integer rule** — no JSON number outside ±(2^53 − 1) anywhere in the payload, because JCS serializes numbers as IEEE-754 binary64 and a larger integer would not survive canonicalization. Checked for every type, since it belongs to the encoding rather than to any one schema.
4. **Digests computed here** — `payload_digest` over the canonical payload, `review_digest` over the reviewable projection of ADR 0028 §5. Callers never supply either: a caller-supplied digest is a caller-asserted one, and the point is that it is derived.

**Amendments take their type, category and version from the target original, not from the registry.** This is the one exception, and it is required rather than convenient. ADR 0028 says an amendment validates against *"the original artifact's `artifact_type` and `schema_version` — an amendment cannot change either"*, and stored payloads are never rewritten, so an artifact created at v1 stays v1 for life. If the registry has since advanced to v2, taking the current version would stamp the amendment v2 and validate the merged result against the v2 validator — silently migrating an immutable artifact's version, and checking it against a schema its own payload was never written for.

So on an amendment write the seam reads the target original (already locked in the same transaction) and uses its `artifact_type`, `artifact_category` and `schema_version`. The registry is still consulted for the *validator* to apply — but the validator **for that version**, which is why the registry declares a readable range rather than only a current version (D3, reads). A change genuinely needing a different type or version is a supersession, not an amendment.

Reads validate the reverse direction: a payload whose `schema_version` is outside the registry's readable range is an error naming the version, never a best-effort parse.

### D3a. Review records store what the reviewer saw, not what is current

Creating a review record persists the `review_digest` — and, for an amendment, the `base_digest` and `base_sequence` — **exactly as observed by the reviewer**, passed in by the caller that presented the content. The seam does **not** recompute "current" values when recording the decision.

The distinction is the whole mechanism. If the artifact changed between the reviewer reading it and the decision being recorded, recomputing would bind the review to content the reviewer never saw — manufacturing exactly the false attestation ADR 0028's digest binding exists to prevent, and doing it silently at the moment of record. Recording the observed digest means a stale review simply fails to match at acceptance, which is the correct outcome.

The seam still validates the shape it is given: the digest is well-formed, the review names an artifact that exists, and the reviewer is a principal in the same organization. It does not validate that the digest is *current*, because a non-current digest is a legitimate thing to record.

### D4. No generic status update, enforced rather than intended

There is no `UpdateArtifactStatus`. A test parses `queries/*.sql`, finds every statement writing `status`, and fails unless the query's sqlc name is a known transition — so a future `SetArtifactStatus` fails the build rather than passing review on a quiet day. Mutation-verified by adding such a query.

### D5. Transitions: locked, classified, and conditionally written

Round 1 claimed zero-rows-affected could be turned into an error naming the failed precondition. It cannot — a rowcount carries no reason. Every transition therefore runs in a transaction:

1. `SELECT … FOR UPDATE` the artifact row.
2. **Classify in Go** against the locked row and its review records, producing a specific outcome: not in the required source status, no accepted review for the current `review_digest`, reviewer is the author, reviewer is a system principal, base moved (amendments), and so on.
3. **Conditionally update**, with the preconditions *also* in the `WHERE`.

Step 3's conditions are redundant under the lock and kept deliberately: they are the backstop that stops a classification bug from writing a transition the rules forbid. Zero rows affected there is an internal invariant failure, not a user-facing outcome.

**`AcceptArtifact` names a specific `review_id`.** Multiple accepted reviews can exist for one digest, and round 1's join would have chosen the reviewer nondeterministically — then written that arbitrary choice into `reviewer_instance_id`. The caller passes the review it is acting on; the seam verifies that review belongs to this artifact, matches the current `review_digest`, has decision `accepted`, and comes from a non-author principal of kind agent or human.

The digest match is what makes ADR 0028's binding real: a review of superseded content cannot license acceptance, because the row's current `review_digest` no longer equals the reviewed one.

**Rejection matrix:**

| Transition | Allowed from | Additional preconditions |
| --- | --- | --- |
| Accept | `draft` | Named review: same artifact, matches current `review_digest`, decision `accepted`, reviewer ≠ author, reviewer kind ∈ {agent, human} |
| Invalidate | `draft` only | None. Invalidation is pre-acceptance by definition (ADR 0021) |
| Supersede | `accepted` | Target is an **original**, not an amendment. The superseding artifact exists, is in the same organization, is being accepted in the same transaction, and its reviewed `supersedes_artifact_id` **equals this target** |
| Archive | `accepted`, `superseded` | Target is an **original**, not an amendment |

**Amendments can be neither superseded nor archived**, which closes a hole in the first draft's matrix. Effective-view assembly loads only `accepted` amendments, so archiving one would silently drop its contribution from the effective view of an artifact nobody re-reviewed — mutating accepted content through a lifecycle side door. Amendments reach exactly two terminal states: `accepted`, or `invalidated` while still a draft. Correcting an accepted amendment is a later amendment, as ADR 0021 already says.

**The superseding artifact's reviewed target is checked.** Its `supersedes_artifact_id` must equal the artifact being marked superseded. Without that, an artifact reviewed and accepted as superseding A could be used to supersede B — the reviewer approved a replacement for one thing and it retired another.

**Supersession is one transaction.** Accepting the superseding artifact and marking its target `superseded` happen together, or a reader between the two statements observes **two authoritative artifacts** for the same subject — which is exactly the state the lifecycle exists to prevent. Both rows are locked, and the target is locked first so a concurrent supersession of the same target cannot deadlock against it.

### D6. Amendment acceptance is serialized per original

Round 1 proposed `max(sequence) + 1` with the unique index as a backstop and a retry on conflict. That is wrong in a way that matters: two amendments reviewed against the **same** base would both retry into distinct sequences and both be accepted, when ADR 0028 requires that a moved base forces re-review. The retry silently produced the outcome the ADR forbids.

The protocol, which is what ADR 0028 actually specifies:

1. `SELECT … FOR UPDATE` **the original** — ADR 0028's "serialize amendment acceptance per original `artifact_id`", so bases move one at a time.
2. Assemble the current effective view and compute its digest.
3. **Compare against the review's recorded base** (`base_digest`, `base_sequence`). A mismatch returns a distinct `ErrBaseMoved`, because "needs re-review" is operationally different from "precondition failed" and an operator acts differently on each.
4. Apply the amendment's patch, and **validate the resulting effective payload** against the original's `artifact_type` and `schema_version` — ADR 0028 requires validation at acceptance and not only at write, since the base may have moved since the patch was authored.
5. Allocate `amendment_sequence` as one more than the maximum over **every non-null historical sequence** for that original, whatever the amendment's current status. Given D5 now forbids superseding or archiving an amendment, accepted is the only status carrying a sequence today, so this is equivalent — it is written as the historical maximum so it stays correct if that matrix ever widens, rather than silently reusing a number.
6. Update conditionally.

Two amendments reviewed against the same base now yield exactly one acceptance and one `ErrBaseMoved`, which is the contract.

**Lock ordering is fixed** — original before amendment, everywhere — so concurrent acceptances cannot deadlock.

### D7. Principal instances, MPH, and the seeding set

Creating a principal instance writes its `principal_instance_inputs` rows **in the same transaction**. ADR 0021 promises that "what was this agent given to start?" is always a query; an instance observable without its inputs makes that false for exactly as long as the gap. Each row stores the artifact and the **digest as seeded**, so a later comparison against the artifact's current digest shows the seed has moved.

**Stopping an instance is once-only.** `StopPrincipalInstance` is a conditional update with `WHERE stop_time IS NULL`, so the first stop wins and later ones cannot overwrite it.

This is not hypothetical: it is the shape of ADR 0027's supervisor double-restart (P-6), where an agent death fires two independent paths — the ERROR state-notification and the `Run()`-exit handler — within about a millisecond. Both will call this. Without the condition, the second overwrites the first's `stop_time` and `stop_reason`, and the reason is the diagnostic that says *why* the agent died; losing it to a later, blander shutdown path is losing the incident.

Repeated and concurrent calls are **idempotent, not errors**: the call returns the recorded stop time and reason, and reports whether this caller was the one that recorded them. ADR 0027's premise is that two paths racing to finalise one lifecycle is normal rather than exceptional, so making the loser an error would turn correct shutdown into spurious failures — while a caller that genuinely cares (a supervisor deciding whether to requeue) can still tell from the flag.

`stop_time` and `stop_reason` are set together, which the schema already requires as a pair.

MPH queries read what the signature is for: instances by `model`, by `prompt_hash`, by `harness_config_hash`, and an instance's full signature including its seeding set. These are the joins ADR 0021 says cost and comparison analysis anchor on.

### D8. Effective views assemble in Go, against the RFC's own vectors

Postgres 18.4 has no merge-patch function — checked, not assumed — so the choice is PL/pgSQL or Go. Go, because it is the same language as its tests and because ADR 0028 requires the view be materialised on read, so no database-side consumer would benefit.

Assembly: load the original, load its accepted amendments ordered by `amendment_sequence`, apply each patch in order. Draft and rejected amendments are never applied. The schema guarantees the sequence is total and the chain is flat, so this relies on those rather than re-checking them.

The algorithm is ~25 lines and the specification publishes a **test-case table in Appendix A**; those vectors are the test suite. They are authored by the specification rather than derived from our implementation, so they cannot drift toward whatever we happen to build.

**Citation.** The specification is **[RFC 7396](https://www.rfc-editor.org/rfc/rfc7396)**, which obsoletes RFC 7386 with an identical algorithm and identical Appendix A vectors. ADR 0028 originally cited 7386 and was amended alongside this design, with Codex and DR approval, since a stale citation in an Accepted ADR is not something a design document corrects on its own authority.

### D9. Driver types stop at the seam

Round 1 said nullable scalars "remain pointers, matching what sqlc generates". That is right for scalars and **wrong for uuid and timestamptz**: with pgx those generate as `pgtype.UUID` and `pgtype.Timestamptz` with a `.Valid` flag whether nullable or not. I had corrected exactly this claim in `sqlc.yaml` two PRs ago and then repeated it here.

So the conversion is explicit rather than incidental. `store` exposes `uuid.UUID`, `*uuid.UUID`, `time.Time`, `*time.Time` and domain structs; `pgtype.*` does not escape the postgres module. The conversion lives in one file and is **tested in both directions for null**, since a `.Valid` flag dropped on the floor turns an absent reviewer into the zero UUID — a value that looks like data.

## Testing

- **RFC 7396 Appendix A vectors** for merge patch, verbatim.
- **Effective view**: conflicting amendments (later sequence prevails per field), out-of-order insertion, `null` deleting a field, and chains containing draft and rejected amendments that must not be applied.
- **Every transition individually, for its own precondition** — accept with no review, with a review of a *different* digest, with a review belonging to another artifact, by the author, by a system principal, from a non-`draft` status; and the corresponding matrix for invalidate, supersede and archive.
- **Supersession atomicity**: no observable state in which both artifacts are authoritative.
- **Amendment protocol**: two amendments against the same base yield one acceptance and one `ErrBaseMoved`; sequence allocation skips a number already used by an archived amendment; the effective payload is validated at acceptance and a patch that breaks the schema is refused there even though it passed on write.
- **Validation**: unregistered type, payload failing its schema, an unsafe integer, an out-of-range `schema_version` on read; an amendment whose *patch* would not validate as an instance but whose *merged result* does, which must be accepted, and the converse, which must not.
- **Registry authority**: a caller supplying its own `artifact_category` or `schema_version` does not get them honoured.
- **Amendment version inheritance**: with the registry advanced to v2, an amendment of a v1 original is stored as v1 and validated by the v1 validator — not stamped v2, and not validated against v2.
- **Stop is once-only**: two concurrent stops leave the first `stop_time` and `stop_reason` intact, the loser is told it did not record them, and neither call errors.
- **Review recording**: the stored digest is the one supplied, not a recomputation — a review recorded after the artifact changed still carries the observed digest and consequently fails at acceptance.
- **Amendments cannot be superseded or archived**, and a superseding artifact whose reviewed `supersedes_artifact_id` names a different target is refused.
- **Digests**: caller-supplied digests are ignored or refused; the stored digest matches an independent JCS computation.
- **Seeding set** written atomically with the instance — never observable without its inputs.
- **Null conversion** in both directions at the pgtype boundary.
- **The no-generic-status guard**, mutation-verified.

Integration-tagged, against item 3's disposable-database harness rather than the canonical database.

## Open items

1. ~~ADR 0028 cites the obsoleted RFC 7386.~~ **Resolved**: approved by Codex and DR, and applied as a citation-only amendment to ADR 0028 (status line records it; the ADR README row re-synced). Same algorithm, same Appendix A vectors, no decision changed.
2. **Interface breadth.** D1 commits to a narrow interface, but "narrow" is a judgement: too small and Phase 3 reaches around it, too wide and no second module can implement it. The listed surface is the proposal, and the honest test is whether a cloud module could implement it without inheriting Postgres semantics.
