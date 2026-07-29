+++
title = "Phase 2 Item 6 Design: The Object Module"
edit_date = "2026-07-28"
status = "draft"
summary = "Design for the object module: a blob adapter separated from the persistence seam that owns pins, writes verified by a server checksum rather than by hashing what the client read, an amended cross-store commit order enforced by a composite operation and by acceptance preconditions, pins tied to the lifecycle that justifies them, and a sweep coordinated by an advisory lock rather than by a timer."
type = "design"
+++

# Phase 2 Item 6 Design: The Object Module

Status: **draft** — revised after review round 1 (four P1s, all upheld).

Delivers ADR 0022's object module: put/get by content digest, existence check, pin/unpin, delete-unpinned, with an S3-compatible adapter over the MinIO container item 2 composes. The seam and its conventions are items 4 and 5's; this records only what differs.

Naming follows the phase directory (`design_local_stack.md`, `design_queries_artifacts.md`, `design_calls_family.md`) rather than ADR 0017's kebab-case slug rule, which those three already diverge from. Renaming all four is a separate decision, not this item's.

## What is inherited, and what is missing

Item 2 composed MinIO, bind-mounted its data directory under the Maestro data root, derived its credentials from the root of trust, and probes `/minio/health/live` at startup. Item 3 created `binary_attachments` and `retention_pins`. Item 4 built the seam. Item 5 left `binary_attachments` out of truncation deliberately, because deleting a row whose bytes live in object storage is this item's problem.

**Nothing creates the bucket.** `stack.Config` names it, `Bootstrap()` publishes it, and no code has ever issued a create — so `dataplane-up` reports a ready plane that cannot store an object. D3 closes this.

**ADR 0022's commit order cannot be implemented as written**, and this item amends the ADR rather than quietly restating it in a phase document (D5).

## D1. Two layers, because they answer to different authorities

Round 1 collapsed the blob store and the object module into one interface, dropped pin/unpin from it — which ADR 0022 and the plan both require — and then exposed a raw `Delete` that the same document said was not offered. Two mistakes with one cause: **the S3 client and the persistence module are not the same thing**, and naming them both "the object module" made the contract incoherent.

**Layer 1 — `objects.Blob`, the adapter.** Knows bytes and keys, nothing about artifacts, pins or organizations beyond a key prefix. Its surface is internal to the module; nothing above imports it.

| Primitive | Used by |
| --- | --- |
| `PutStaged` | The write path, before a digest key is claimed |
| `Promote` | Server-side copy from staging to the digest key |
| `Get`, `Stat`, `Exists` | Reads and verification |
| `Delete` | Staging cleanup and the sweep — never retention policy |
| `ListPrefix` | The sweep's candidate set only |
| `EnsureBucket` | `dataplane-up` |

**Layer 2 — the object module in the seam**, which is what ADR 0022 describes. It owns the relational half and offers the ADR's vocabulary:

| Seam operation | What it does |
| --- | --- |
| `PutAttachment` | Verified object write plus its `binary_attachments` row |
| `GetAttachment` | Verified read by attachment id |
| `AttachmentExists` | Existence without transfer |
| `Pin` / `Unpin` | `retention_pins` rows — relational, transactional, foreign-keyed |
| `DeleteUnpinned` | The coordinated sweep (D6) |

Pin and unpin stay relational because that is what they are: rows with foreign keys, an exclusive-arc target and a digest binding, which ADR 0021 requires to fail verification when dangling. A blob store tracking pins would hold a second, unconstrained copy of that state, and the two would disagree the first time a write failed between them. The ADR's requirement is satisfied by the **module**; it was never a requirement on the S3 client.

There is no public raw delete at either layer. `Delete` is a Layer 1 primitive with two callers inside the module, and D8's rejection stands without contradicting D1.

## D2. Writes are verified by the server, not by the client's own reading

Round 1 hashed the source stream and called that verification. It is not: **it proves what the client read, not what arrived.** A truncated or altered upload passes a client-side hash and then gets promoted to a digest key, which is exactly the corruption the digest exists to prevent.

The write path:

1. **Staging upload** with a **SHA-256 upload checksum** the server verifies. `minio-go` exposes this today — `PutObjectOptions.Checksum` accepts a `ChecksumType`, and `ObjectInfo.ChecksumSHA256` reads it back — so a mismatched body is rejected by MinIO and never lands. Round 1 deferred this to "when a second adapter is built"; that was deferring the only thing that made the write trustworthy.
2. **Size check.** The observed byte count must equal the stated size, which is also what `binary_attachments.size_bytes` records. A body that hashes correctly but is a different length than claimed means the caller's metadata is wrong, and that row would misreport storage forever.
3. **Promote** by server-side copy to the digest key, then verify the promoted object's checksum via `Stat` before the attachment row is written. A copy is not a second upload, but "the copy landed intact" is a claim, and this item's whole subject is not trusting claims about bytes.
4. **Staging cleanup**, whose failure is logged and left to the staging sweep rather than failing a completed write.

**The idempotent shortcut verifies too.** An object already at the digest key is *not* proof of correct content — it is exactly where a previously corrupted or partially promoted object would sit, and returning success would bless it into a new attachment row. So the shortcut reads the object back and hashes it. That costs one read of an object whose upload it skips, so the shortcut still wins; correctness is not the thing being traded for speed.

**The checksum claim is proven, not cited.** A test uploads a body with a deliberately wrong declared checksum against the **pinned MinIO image** and asserts the PUT fails. Without it, the design rests on documentation, and a server that silently ignored the header would leave every write unverified with the suite green.

## D3. Keys are organization-scoped, and the bucket is provisioned by `up`

Approved in round 1. `<organization_id>/<first two hex>/<next two hex>/<digest>`, with staging under a separate `staging/<organization_id>/<uuid>` prefix that the object sweep never considers (D6).

Organization-first costs deduplication across tenants, and that is the right trade: every other operation in this seam is organization-scoped; a shared blob makes deletion a cross-tenant reference count, the query most likely to be wrong and least likely to be noticed; and global keys let one organization learn whether given bytes are already stored. The two-level fan-out is for filesystem-backed stores, where one directory of every object is a known pathology.

**The bucket is created by `dataplane-up`, through the module.** The S3 client stays in the module and the orchestration stays in `stack`, where every other readiness step lives; `EnsureBucket` tolerates an existing bucket, because the second `up` is the normal case.

## D4. Reads verify, because a retention bug must be loud

`GetAttachment` hashes what it streams and fails if the result does not match the digest that addressed it. ADR 0021 requires a dangling or altered reference to **fail verification rather than silently weaken a proof**: evidence whose bytes have been replaced is worse than evidence that is missing, because it still reads as evidence.

Verification can only complete at the end of the stream, so the reader returns it as a **read error at EOF** and the module's helpers never move a destination file into place before that error can surface. The obvious implementation — copy, then check — corrupts a file and then reports it.

**MinIO inlines small objects into `xl.meta`**, so a host-side digest of the on-disk file is not the object-body digest (verified in item 5). Every test here reads back **through S3**, never from the bind mount.

## D5. The commit order, amended in the ADR and enforced by the seam

ADR 0022 says *object first, pin recorded, row last*. Read literally that is impossible: `retention_pins.pinned_by_artifact_id` is a foreign key to `management_artifacts`, so a pin cannot precede every row — its holder is one.

Round 1 restated the order in this document. That was the wrong instrument: a phase design does not get to reinterpret an accepted ADR's invariant. **ADR 0022 is amended in this item, before implementation**, to state the order in terms that can be built:

> Object first; the attachment row next; the referencing artifact and its retention pins in one transaction; and the artifact becomes authoritative only on acceptance, which verifies that every referenced object exists and every pin matches its attachment's digest.

The invariant that matters survives intact: **no accepted artifact ever references an object that is missing or unpinned.** Steps before acceptance leave only removable garbage, never a dangling authoritative reference.

**Description is not enforcement.** Round 1 described an order while leaving the existing `CreateManagementArtifact` and `AcceptArtifact` reachable with no pins at all, so any caller could produce exactly the state the invariant forbids. Two additions close that:

- **A composite seam operation**, `AttachEvidence`, performing object write → attachment rows → artifact draft **and** its pins in one transaction. It is the supported path, and it cannot be half-done.
- **Acceptance preconditions**, in the same classified, locked shape item 4 uses for every transition. For an artifact referencing attachments, acceptance additionally requires: a pin per referenced attachment, each pin's `pinned_digest` equal to the attachment's `object_digest`, and each object present in the store. Digest equality matters because a pin recording a *different* digest is a pin that protects nothing the artifact cites.

The object-existence check is the one precondition that reaches outside the database. It is safe in this order because the attachment row already exists, and the sweep's reachable set is exactly the attachment rows (D6) — so between the check and the commit there is nothing that may delete the object.

**Pins follow the lifecycle that justifies them, in the transition's own transaction.** Round 1 created pins and never removed them, so a draft that was invalidated pinned its evidence forever.

| Transition | Pins |
| --- | --- |
| `draft` → `invalidated` | **Removed.** The artifact never became authoritative; nothing justifies holding its evidence. |
| `draft` → `accepted` | Retained, and verified as a precondition. |
| `accepted` → `superseded` | **Retained.** ADR 0021 preserves accepted history immutably, and history without its evidence is not preserved. |
| `accepted`/`superseded` → `archived` | **Removed.** Archival is how an operator releases a retired artifact's hold on storage; without this there is no path to reclaim anything, and retention has no terminal state. |

The archival row is the one with a policy consequence rather than a mechanical answer, so it is called out for reviewers below.

## D6. The sweep is coordinated, not timed

Round 1 protected in-flight writes with a one-hour grace period. **A grace period is not concurrency control** — a paused writer or a slow upload can exceed any constant, and age alone cannot prove abandonment. Correctly rejected.

The window is real: between the object landing at its digest key and the attachment row committing, the object is legitimately unreferenced, and a sweep running then deletes the bytes of a commit in progress.

**Writers and the sweep serialise per `(organization, digest)`** on a Postgres advisory lock, keyed by the first eight bytes of `sha256(organization_id + "/" + digest)`. Collisions serialise unrelated digests and cost nothing but concurrency, which is the correct failure direction for a lock.

| Actor | Sequence |
| --- | --- |
| Writer | Upload to staging **without** the lock (the long part); then in one transaction: take the lock, promote, verify, insert the attachment row, commit; then delete the staging object |
| Sweep | Per candidate digest, in one transaction: take the lock, **recheck** that no attachment row references it, delete the object, commit |

The recheck under the lock is what makes the decision sound: "unreferenced" is established in mutual exclusion with the commit that would make it referenced. A writer that has not yet taken the lock has not yet promoted, so there is nothing at the digest key to delete.

The lock is held across a server-side copy, which is a remote call inside a database transaction and worth stating rather than hiding. It is bounded by a context timeout on the copy, and the alternative is the race. The upload — the genuinely long operation — is outside the lock.

**Staging is swept separately, and safely by failure rather than by timing.** Staging objects are never referenced by design and are keyed per upload, so deleting one under a slow writer cannot corrupt anything: that writer's promote then fails and reports it. The final-object sweep never considers the staging prefix.

**The grace period stays as defence in depth, and cannot be zero.** It supplements the lock rather than replacing it, and D8's rejection of sweeping without one stands. Age is tested by **injecting a clock**, not by disabling the rule — a test that switches the guard off proves the guard is switchable.

## D7. Failure injection is the point of the tests

The invariant is entirely about what happens when a step fails, so a happy-path test that asserts the rows exist proves nothing. The adapter is fronted by a fault-injecting decorator, and each step is failed in turn.

| Case | Required outcome |
| --- | --- |
| Object put fails | No attachment row, no pin, no artifact |
| Put succeeds, attachment insert fails | Orphan object, sweepable; nothing references it |
| Attachment exists, artifact+pin transaction fails | Neither artifact nor pin — asserted by reading both tables |
| Declared checksum does not match the body | The **server** rejects the upload; nothing at staging or the digest key |
| Observed size differs from the stated size | Rejected before any row is written |
| `Put` where a **corrupt object already occupies the digest key** | The idempotent shortcut verifies, fails, and does not write an attachment row |
| Acceptance with a missing object | Refused, with the specific reason, and the artifact stays `draft` |
| Acceptance with a missing or digest-mismatched pin | Refused likewise |
| Invalidate, then archive | Pins removed in the transition's transaction; superseded keeps its pins |
| Sweep racing the relational commit, under a **barrier** | With the writer holding the lock, the sweep blocks and then finds the reference; the object survives |
| Sweep inside the grace period | A fresh unreferenced object is not deleted |
| Corrupted object read | `GetAttachment` fails at EOF, and no destination file is left in place |

The barrier-controlled race follows item 5's recipe: launching a sweep and a writer concurrently and hoping they collide is flaky when it fails and vacuous when it passes. Each guard is mutation-verified — this item has more backstops than most, and item 5's lesson was that a backstop behind a working guard is untestable through the normal path.

## D8. Rejected cases

| Rejected | Why |
| --- | --- |
| A digest that is not 64 lowercase hex | The schema's `CHECK` shape; refused at the seam so the caller reads the field |
| A body whose server-verified checksum differs from the stated digest | The whole contract |
| A body whose length differs from the stated size | `size_bytes` would misreport storage forever |
| Negative or absent size | Schema check, refused early |
| Idempotent success over an unverified existing object | Blesses corruption into a new row |
| `GetAttachment` of a missing object | `ErrNotFound`, indistinguishable from another organization's, as everywhere in the seam |
| `GetAttachment` whose bytes do not match | `ErrInvariant`: the store contradicts itself |
| Blank or missing media type | An attachment nothing can interpret |
| Public raw object delete | Not offered at either layer; the sweep computes its own candidate set |
| Sweeping without the advisory lock, or without a grace period | Deletes in-flight writes |
| Accepting an artifact whose evidence is missing, unpinned or digest-mismatched | The invariant this item exists to enforce |
| Any cross-organization read, write, pin or sweep | Multi-tenant boundary, as items 4 and 5 |

## D9. What this item does not do

- **No object migration or rewriting.** Content-addressed storage has no update.
- **No multipart tuning, lifecycle rules or bucket policies.** Client defaults until a measurement says otherwise.
- **No second adapter.** GCS and SeaweedFS are ADR 0022's *later* choices; the narrow interface is the insurance, not a speculative implementation.
- **No Orchestrator integration.** Phase 3, as with the rest of the seam.

## Reviewer decisions

Resolved in round 1:

1. **`minio-go/v7`, approved** — and the reasoning I gave for it was wrong. I claimed its dependency tree was "a fraction of the AWS SDK's". Measured at `minio-go/v7 v7.2.1` against `aws-sdk-go-v2/service/s3 v1.106.1`, with a trivial program importing each:

    | | `minio-go/v7` | `aws-sdk-go-v2/s3` |
    | --- | --- | --- |
    | Modules contributing linked packages | 19 | 11 |
    | `go.sum` lines | 53 | 22 |
    | Trivially linked binary | 6.2 MiB | 5.5 MiB |

    The AWS SDK is **smaller** on every axis but linked package count. The choice stands on the grounds the reviewer gave — it targets S3-compatible stores directly, and GCS promises compatibility with only some S3 tooling, so a separate GCS adapter is the expectation either way — not on a size advantage that does not exist. Recorded here because the claim is now in a commit message, and an unverified dependency claim is exactly what `CLAUDE.md` requires checking against the pinned version's source.
2. **Organization-scoped keys, approved.**
3. **One hour as the safety mechanism, rejected.** Replaced by advisory-lock coordination with a reference recheck; the grace period remains only as defence in depth (D6).
4. **Attachment truncation joins item 5's pass now, approved.**

Open for round 2:

- **Archival removes pins** (D5). It is the only proposal here with a retention-policy consequence rather than a mechanical answer: without it nothing ever releases an artifact's hold on storage, but it does mean archiving is the irreversible step for evidence. The alternative is an explicit release operation separate from archival, which is more surface for a case nobody has yet.
