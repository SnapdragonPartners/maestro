+++
title = "Phase 2 Item 6 Design: The Object Module"
edit_date = "2026-07-28"
status = "draft"
summary = "Design for the digest-addressed object module and its S3-compatible adapter: a narrow owned interface, organization-scoped keys, a staging-then-promote write that never leaves wrong bytes at a digest key, verification on read, the cross-store commit order stated in terms of the tables that actually exist, and a grace-period sweep for orphans."
type = "design"
+++

# Phase 2 Item 6 Design: The Object Module

Status: **draft** — first review round.

Delivers ADR 0022's object module: put/get by content digest, existence check, pin/unpin, delete-unpinned, behind Maestro's own narrow interface with an S3-compatible adapter over the MinIO container item 2 already composes. The seam and its conventions are items 4 and 5's; this records only what differs.

Naming follows the phase directory (`design_local_stack.md`, `design_queries_artifacts.md`, `design_calls_family.md`) rather than ADR 0017's kebab-case slug rule, which those three already diverge from. Renaming all four is a separate decision, not this item's.

## What is inherited, and what is missing

Item 2 composed MinIO, bind-mounted its data directory under the Maestro data root, derived its credentials from the root of trust, and probes `/minio/health/live` at startup. Item 3 created `binary_attachments` (digest, media type, size) and `retention_pins`. Item 4 built the seam and preallocated UUIDv7 identifiers. Item 5 left `binary_attachments` out of truncation deliberately, because deleting a row whose bytes live in object storage is this item's problem.

**One thing is missing and was not noticed until now: nothing creates the bucket.** `stack.Config` names it, `Bootstrap()` publishes it, and no code has ever issued a create. Every object test would fail on a clean machine, and `dataplane-up` reports a ready plane that cannot store an object. D3 closes this.

## D1. The interface is ours, and it is narrow

ADR 0022 fixes the contract as Maestro's own module rather than an S3 interface, because content-addressed needs are small and provider interchangeability is easy to overpromise — GCS's S3 interoperability is partial, not a drop-in. So the module offers exactly:

| Operation | Contract |
| --- | --- |
| `Put` | Store bytes under a caller-stated digest, verified; idempotent |
| `Get` | Stream bytes back, verified against the digest that addressed them |
| `Exists` | Whether an object is present, without transferring it |
| `Delete` | Remove one object, used by the sweep and never offered as retention policy |
| `Sweep` | Delete unreferenced objects older than a grace period |

Pin and unpin are **not** object-store operations, and putting them here would be a design error worth naming. A pin is a relational fact — `retention_pins` rows with foreign keys, an exclusive-arc target and a digest binding — and ADR 0021 requires a dangling pin to fail verification. An object store that also tracked pins would hold a second, unconstrained copy of that state, and the two would disagree the first time a write failed between them. **Pinning stays in the relational plane; the object module only ever answers "is this reachable?" by being told.**

There is no `List`. A caller that wants to enumerate objects wants the attachment table, which is the authority on what exists; enumerating the bucket answers a different question — what is *lying around* — and only the sweep asks it.

## D2. Writes stage, verify, then promote

The digest is the address, so the key cannot be known until the bytes are hashed. Three shapes were considered:

| Shape | Why not |
| --- | --- |
| Module hashes, buffering in memory | Evidence media is exactly what this exists for; an unbounded buffer is a memory limit disguised as an interface |
| Module hashes, spooling to a temp file | Adds a disk dependency and a cleanup path to every write, on a machine that may have less free disk than the object store |
| Caller states the digest, module streams straight to the digest key and verifies at the end | On mismatch, wrong bytes have already landed **at a digest key**, and a concurrent reader gets them |

So: **the caller states the digest; the module streams to a staging key while hashing; on match it promotes with a server-side copy; on mismatch it deletes the staging object and fails.** Wrong bytes never occupy a digest key, and nothing is buffered.

The caller stating the digest is not the seam trusting it. Item 4's rule — digests are derived at the seam, never caller-supplied — is about the *artifact* digest, which is an assertion about content the seam itself canonicalises. Here the digest is the **address**, and the module recomputes it over the bytes it actually received; a caller cannot store anything under a digest that is not its content's. What the caller supplies is a claim that gets checked, which is the opposite of a claim that gets trusted.

`Put` is idempotent by construction: identical bytes produce the same key. It checks existence first and skips the upload entirely when the object is already present — the common case for re-imported runner records — which also makes a retried import cheap rather than merely correct.

**Deferred, with a trigger:** S3 supports `x-amz-checksum-sha256`, which would have the server reject a mismatched upload and remove the staging step. MinIO implements it; SeaweedFS's support is not verified and GCS's is different. Revisit when a second adapter is actually built, and verify it against that adapter rather than against documentation.

## D3. Keys are organization-scoped, and the bucket is provisioned by `up`

**Key layout:** `<organization_id>/<first two hex>/<next two hex>/<digest>`.

Organization-first is the significant choice, and it costs deduplication across tenants. That is the right trade:

- every other operation in this seam is organization-scoped, and an object store that is not would be the one place tenancy is decided by luck;
- **a shared blob makes deletion unanswerable.** With global keys, removing an organization's last reference must not delete bytes another organization still references, so every delete becomes a cross-tenant reference count — the query most likely to be wrong and least likely to be noticed;
- global keys also leak existence: an organization can learn whether given bytes are already stored, which is a disclosure no one asked for.

Storage duplication is the price, and it is small: the duplicated case is two organizations holding byte-identical evidence, which is rare and not worth a reference counter.

The two-level hex fan-out is for the filesystem-backed stores this contract admits, where a single directory of every object is a known pathology. It is derived from the digest, so it costs nothing to compute and never needs recording.

**The bucket is created by `dataplane-up`, through the module.** The S3 client stays in the module and the orchestration stays in `stack`, which is where every other readiness step lives. `up` is idempotent already and bucket creation joins it as another idempotent step; the module tolerates an existing bucket rather than treating it as an error, because the second `up` is the normal case, not the exception.

## D4. Reads verify, because a retention bug must be loud

`Get` hashes what it streams and fails if the result does not match the digest that addressed it. The scope row requires this and ADR 0021 explains why: a dangling or altered reference must **fail verification rather than silently weakening a proof**. An evidence package whose bytes have been replaced is worse than one whose bytes are missing, because it still reads as evidence.

The verification is of the whole stream, so it can only fail at the end — after a caller may have consumed bytes. The interface therefore hands back a reader that returns the verification failure as a **read error at EOF**, and the module's own helpers never write a caller's destination file into place before that error can surface. This is stated because the obvious implementation, "copy then check", corrupts a file and then reports it.

**One trap, already paid for once.** MinIO inlines small objects into `xl.meta`, so a host-side digest of the on-disk file is not the object-body digest — verified while demonstrating durability in item 5. Every test here reads bytes back **through S3**, never from the bind mount.

## D5. The cross-store commit order, in terms of tables that exist

ADR 0022: *object first, pin recorded, row last.* Taken literally that is **impossible**, and the impossibility is worth stating rather than discovering in code: `retention_pins.pinned_by_artifact_id` is a foreign key to `management_artifacts`, so the pin cannot precede every row — its holder is one.

The invariant is about the **authoritative** reference. ADR 0021 makes an artifact authoritative on acceptance, which gives the real order:

1. **Object** — put and verified; nothing references it yet, so a failure here leaves only an orphan (D6).
2. **Attachment row** — `binary_attachments`, recording digest, media type and size.
3. **Artifact and its pins, in ONE transaction** — the evidence package as a draft, plus a `retention_pins` row per referenced attachment. Split across two transactions, a reader between them sees an artifact whose evidence is already prunable.
4. **Acceptance** — the existing transition, unchanged. Nothing here weakens it.

So the invariant holds in the form that matters: **no accepted artifact ever references an object that is missing or unpinned.** Steps 1–3 are individually retryable and leave only removable garbage on failure, never a dangling reference.

**Preallocated identifiers are not load-bearing here, and item 4's comment says otherwise.** It anticipated "the object written first, under a key derived from this id" — but ADR 0022 makes keys digest-addressed, so the object never names the artifact. Preallocation remains useful (a caller can know the id before the transaction), and the comment should be corrected rather than the design bent to match it.

## D6. Deletion, and the grace period that makes a sweep safe

Two different removals, and conflating them is how a sweep deletes live data:

**Attachment truncation** — an unpinned `binary_attachments` row past the horizon. It joins item 5's truncation pass under the same rules: organization-scoped, dependency-ordered, precedence-assigned buckets, `ON DELETE RESTRICT` meaning pinned rows are excluded in the `WHERE` rather than discovered at commit. Deleting the row does **not** delete the object; it makes it unreachable.

**The sweep** — objects no attachment row references. This is the only operation that deletes bytes, and it is where the design can destroy an in-flight write: between step 1 and step 2 of D5, an object is legitimately unreferenced. A sweep that ran then would delete the bytes of a commit in progress, and the caller would go on to write a row pointing at nothing.

So the sweep deletes only objects that are **both unreferenced and older than a grace period**, and the grace period is a documented multiple of the longest plausible write, not a guess dressed as a constant. This is ADR 0027's rule in its own domain: destructive recovery must never remove another actor's in-progress work.

The sweep is organization-scoped like everything else, which D3's key layout is what makes possible: the reachable set is one organization's attachment digests, and the candidate set is one key prefix.

## D7. Failure injection is the point of the tests

The scope row asks for failure-injection tests at each step "proving no row ever references a missing or prunable blob". A test that exercises the happy path and asserts the rows exist proves nothing about this: the invariant is entirely about what happens when a step fails.

So the adapter is fronted by a fault-injecting decorator in tests, and each step of D5 is failed in turn:

| Injected failure | Required outcome |
| --- | --- |
| Object put fails | No attachment row, no pin, no artifact |
| Object put succeeds, attachment insert fails | Orphan object, sweepable after the grace period; no row references it |
| Attachment row exists, artifact+pin transaction fails | No artifact and no pin — the transaction is the unit, asserted by reading both tables |
| Everything succeeds | Acceptance works, `Get` verifies, and the sweep leaves every referenced object alone |

Plus the two that catch the guards rather than the path: a **sweep run inside the grace period must not delete a fresh unreferenced object**, and a **corrupted object must fail `Get`** — produced by writing different bytes at a digest key through the raw client, since no seam operation can create that state.

Each guard is mutation-verified: item 5's lesson is that a backstop behind a working guard is untestable through the normal path, and this item has more backstops than most.

## D8. Rejected cases

Enumerated before the code, per `CLAUDE.md`:

| Rejected | Why |
| --- | --- |
| A digest that is not 64 lowercase hex | The schema's `CHECK` shape; refused at the seam so the caller reads the field, not a constraint |
| `Put` whose stream does not hash to the stated digest | The whole contract; staging object deleted, nothing promoted |
| `Put` with a negative or absent size | `binary_attachments.size_bytes` is checked non-negative, and a size nobody stated cannot be reconciled later |
| `Get` of a digest with no object | `ErrNotFound`, indistinguishable from another organization's object, as everywhere else in the seam |
| `Get` whose bytes do not match the digest | An `ErrInvariant`-class failure: the store contradicts itself, and no caller should paper over it |
| A blank or missing media type | An attachment nothing can interpret; the column is `NOT NULL` and blankness is the seam's to catch |
| Deleting an object that is still referenced | Not offered. `Delete` is the sweep's, and the sweep computes its own candidate set |
| Sweeping without a grace period | Deletes in-flight writes; there is no "sweep everything now" |
| Any cross-organization read, write or sweep | Multi-tenant boundary, as items 4 and 5 |

## D9. What this item does not do

- **No object migration or rewriting.** Content-addressed storage has no update.
- **No multipart tuning, no lifecycle rules, no bucket policies.** The adapter uses the client's defaults until a measurement says otherwise.
- **No second adapter.** GCS and SeaweedFS are named in ADR 0022 as *later* choices; building one now would be an abstraction validated against a hypothesis. The interface is narrow enough to make the second adapter cheap, which is the actual insurance.
- **No integration with the Orchestrator.** That is Phase 3, as with the rest of the seam.

## Delegated decisions for reviewers

1. **S3 client: `minio-go/v7` or `aws-sdk-go-v2/s3`.** Proposed: `minio-go/v7`. It speaks generic S3 (so SeaweedFS and AWS both work), its dependency tree is a fraction of the AWS SDK's, and this binary is cross-compiled and embedded per ADR 0026, where dependency weight is a real cost. The counter-argument is that `aws-sdk-go-v2` is the more standard choice and the likelier fit if the cloud adapter is S3 rather than GCS; ADR 0022's stated GCP bias makes that less likely.
2. **Organization-scoped keys over cross-tenant deduplication** (D3). Proposed as above; the trade is storage duplication in a rare case against a reference count in every delete.
3. **Grace period value.** Proposed: **one hour**, with the module taking it as configuration rather than a constant so the sweep can be tested at zero. An hour is far longer than any write and far shorter than any retention horizon.
4. **Attachment truncation joins item 5's pass now, or waits for a caller.** Proposed: now — the pass already enumerates the tables, `binary_attachments` was deliberately deferred *to this item*, and leaving it out means the one growing table with bytes behind it is the one with no retention story.
