+++
title = "Phase 2 Item 6 Design: The Object Module"
edit_date = "2026-07-28"
status = "draft"
summary = "Design for the object module: a blob adapter separated from the persistence seam that owns pins, content proven by a local hash with the server checksum kept to transport, an amended cross-store commit order whose expected evidence set is extracted from the reviewed payload and assembled from the locked base for amendments, pins mutable only while their holder is a draft, and reclamation fenced by owner-token leases and durable deletion claims rather than by timers."
type = "design"
+++

# Phase 2 Item 6 Design: The Object Module

Status: **draft** — revised after review rounds 1, 2 and 3 (four P1s, then five, then four, all upheld). The ADR 0022 amendment (D5) is approved by Codex; DR's approval is outstanding, and implementation waits on it.

Delivers ADR 0022's object module: put/get by content digest, existence check, pin/unpin, delete-unpinned, with an S3-compatible adapter over the MinIO container item 2 composes. The seam and its conventions are items 4 and 5's; this records only what differs.

Naming follows the phase directory (`design_local_stack.md`, `design_queries_artifacts.md`, `design_calls_family.md`) rather than ADR 0017's kebab-case slug rule, which those three already diverge from. Renaming all four is a separate decision, not this item's.

## What is inherited, and what is missing

Item 2 composed MinIO, bind-mounted its data directory under the Maestro data root, derived its credentials from the root of trust, and probes `/minio/health/live` at startup. Item 3 created `binary_attachments` and `retention_pins`. Item 4 built the seam. Item 5 left `binary_attachments` out of truncation deliberately, because deleting a row whose bytes live in object storage is this item's problem.

**Nothing creates the bucket.** `stack.Config` names it, `Bootstrap()` publishes it, and no code has ever issued a create — so `dataplane-up` reports a ready plane that cannot store an object. D3 closes this.

**ADR 0022's commit order cannot be implemented as written**, so this item proposes an amendment to the ADR rather than quietly restating it in a phase document (D5). Implementation waits on that amendment being accepted.

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
| `Pin` / `Unpin` | `retention_pins` rows — relational, transactional, foreign-keyed, and permitted **only while the holding artifact is a draft** (D5) |
| `DeleteUnpinned` | The coordinated sweep (D6) |

Pin and unpin stay relational because that is what they are: rows with foreign keys, an exclusive-arc target and a digest binding, which ADR 0021 requires to fail verification when dangling. A blob store tracking pins would hold a second, unconstrained copy of that state, and the two would disagree the first time a write failed between them. The ADR's requirement is satisfied by the **module**; it was never a requirement on the S3 client.

There is no public raw delete at either layer. `Delete` is a Layer 1 primitive with two callers inside the module, and D8's rejection stands without contradicting D1.

## D2. Three facts about the bytes, three mechanisms

Round 1 hashed the source and called it verification, which proves what the client read and nothing about what arrived. Round 2 then swung too far and treated the server checksum as proof of the address. Both were one mistake: **one check was asked to answer three different questions.**

**The server checksum is transport integrity, not the address.** Round 2 called `PutObjectOptions.Checksum` a server verification of our digest; it is not. It selects an *algorithm* — the client computes the value — and for a multipart upload the SHA-256 it carries is a **composite** checksum-of-checksums, which is not the full-object SHA-256 this design uses as the key. Comparing it to a digest would compare two different numbers and pass whenever they happened to be equal, which for a small single-part object they are. That is the worst kind of check: correct on the easy case, silently absent on the large evidence media this module exists for.

Three separate facts are needed, and each has its own mechanism:

| Fact | How |
| --- | --- |
| The **source** hashes to the claimed digest | Hash the complete stream **locally** while it is read, and compare |
| The source is the **stated length** | Count bytes; after `size` bytes, one more read must return EOF |
| The bytes **arrived** as sent | The server checksum, retained as transport verification |
| The **promotion** landed intact | Read the promoted object back and hash it |

The write path:

1. **Staging upload**, streaming through a hashing, counting reader, with a SHA-256 upload checksum enabled for transport. The local hash is the authority on content; the server checksum guards the wire.
2. **Length and digest check.** A source longer than the stated size fails — the check is an explicit read past `size` that must return EOF, not merely "we stopped at `size`", which silently truncates. A source shorter than stated fails on the count. Then the local hash must equal the claimed digest.
3. **Promote** by server-side copy to the digest key, then **read the promoted object back and hash it**. A composite checksum cannot answer this, and a copy landing intact is a claim like any other. The cost is one read per genuinely new object, which the idempotent shortcut already avoids paying twice.
4. **Staging release** — the lease row and the staging object (D6), whose failure is logged rather than failing a completed write.

**The idempotent shortcut verifies too.** An object already at the digest key is *not* proof of correct content — it is exactly where a previously corrupted or partially promoted object would sit, and returning success would bless it into a new attachment row. So the shortcut reads the object back and hashes it. That costs one read of an object whose upload it skips, so the shortcut still wins; correctness is not the thing being traded for speed.

**The transport claim is proven, not cited.** A test corrupts the body between hashing and transmission against the **pinned MinIO image** and asserts the PUT fails. A server that silently ignored the checksum header would otherwise leave transport unverified with the suite green — and because content correctness now rests on the local hash rather than on the header, this test measures exactly what the header is claimed to add and nothing more.

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

Round 1 restated the order in this document. That was the wrong instrument: a phase design does not get to reinterpret an accepted ADR's invariant. **An amendment to ADR 0022 is proposed in this item, before implementation** — proposed, not accepted, and its status line says so until Codex and DR act on it — stating the order in terms that can be built:

> Object first; the attachment row next; the referencing artifact and its retention pins in one transaction; and the artifact becomes authoritative only on acceptance, which verifies that every referenced object exists and every pin matches its attachment's digest.

The invariant that matters survives intact: **no accepted artifact ever references an object that is missing or unpinned.** Steps before acceptance leave only removable garbage, never a dangling authoritative reference.

**Description is not enforcement.** Round 1 described an order while leaving the existing `CreateManagementArtifact` and `AcceptArtifact` reachable with no pins at all, so any caller could produce exactly the state the invariant forbids. Two additions close that:

- **A composite seam operation**, `AttachEvidence`, performing object write → attachment rows → artifact draft **and** its pins in one transaction. It is the supported path, and it cannot be half-done.
- **Acceptance preconditions**, in the same classified, locked shape item 4 uses for every transition.

**The expected reference set comes from the reviewed payload, not from the pins.** Round 2 required "a pin per referenced attachment" without saying what *referenced* meant, and the only available answer was the pins themselves — which is circular. Pins cannot simultaneously define which evidence was reviewed and prove that evidence is pinned: payload B could be reviewed while attachment A is pinned, and Audit references could be omitted entirely.

So the expected set is **extracted from review-bound content**. ADR 0028's `review_digest` covers the whole reviewable envelope including the payload, so a set derived from the payload is a set the reviewer saw. The registry gains an optional **reference extractor per type and version**, beside the validator it already holds:

| Registry entry | Meaning at acceptance |
| --- | --- |
| Extractor registered | The expected set is what it returns from the payload |
| No extractor | The type carries no evidence; acceptance requires **zero** pins |

Acceptance then compares the **complete expected set against the pins as sets**, requiring equality rather than containment:

- every expected Audit-artifact and attachment reference is pinned;
- every pin's `pinned_digest` equals its target's digest, since a pin recording a different digest protects nothing the artifact cites;
- every referenced object exists in the store;
- **no pin exists that the reviewed payload does not name** — an extra pin is an unreviewed retention claim, and set equality is the only comparison that catches it.

The extractor is versioned with the type because a v2 payload may name evidence differently from a v1; item 4's rule that a v1 artifact stays v1 for life applies unchanged.

**An amendment's payload is a patch, so extracting from it gives the wrong set.** ADR 0028 stores an amendment as an RFC 7386 merge patch; running the extractor over it returns whatever the patch happens to mention, which is neither the complete reviewed set nor a subset with any useful meaning. At amendment acceptance the seam therefore **assembles the effective payload against the locked reviewed base** — item 4 already holds that base under the original's lock and compares both its digest and its sequence — and extracts from the assembled result, which is what the reviewer actually read.

Each artifact holds pins for what **it** introduced, so the comparison decomposes:

| Artifact | Expected pin set |
| --- | --- |
| Original | `extract(payload)` |
| Amendment | `extract(effective) − extract(base)` — the references it adds |

**Amendments may add references and may not remove them.** Exact set equality over the effective payload would otherwise drop a pin the original still needs, and ADR 0021 preserves accepted history immutably — history without its evidence is not preserved. So acceptance additionally requires `extract(base) ⊆ extract(effective)`, and an amendment that drops a reference is refused with that specific reason.

The alternative — tracking historical pins separately so a removal keeps the old evidence alive under a different holder — buys a case nobody has yet, at the cost of a second retention concept. Additive-only matches ADR 0028's additive-within-version evolution and needs no new table.

The object-existence check is the one precondition that reaches outside the database. It is safe in this order because the attachment row already exists, and the sweep's reachable set is exactly the attachment rows (D6) — so between the check and the commit there is nothing that may delete the object.

**Pins follow the lifecycle that justifies them, in the transition's own transaction.** Round 1 created pins and never removed them, so a draft that was invalidated pinned its evidence forever.

| Transition | Pins |
| --- | --- |
| `draft` → `invalidated` | **Removed.** The artifact never became authoritative; nothing justifies holding its evidence. |
| `draft` → `accepted` | Retained, and verified as a precondition. |
| `accepted` → `superseded` | **Retained.** ADR 0021 preserves accepted history immutably, and history without its evidence is not preserved. |
| `accepted`/`superseded` → `archived` | **Removed** (approved in round 2: `archived` is terminal and non-authoritative, so it is a reasonable explicit retention boundary). Without it nothing ever releases a retired artifact's hold on storage and retention has no terminal state. |

**Pins are mutable only while the holder is a draft.** Otherwise acceptance verifies a set that any later `Unpin` can dismantle, and the invariant holds for an instant rather than for the artifact's life. So `Pin` and `Unpin` lock the holding artifact and classify it in Go — item 4's shape — and permit the change only in `draft`; `accepted` and `superseded` refuse with the specific reason.

Lifecycle transitions do their own pin removal through **internal queries**, not through the public `Unpin`, so the draft-only rule is not something a transition has to be exempted from. That also keeps removal in the transition's own transaction, which is what makes it atomic with the status change.

## D6. The sweep is coordinated, not timed

Round 1 protected in-flight writes with a one-hour grace period. **A grace period is not concurrency control** — a paused writer or a slow upload can exceed any constant, and age alone cannot prove abandonment. Correctly rejected.

The window is real: between the object landing at its digest key and the attachment row committing, the object is legitimately unreferenced, and a sweep running then deletes the bytes of a commit in progress.

**Writers and the sweep serialise per `(organization, digest)`** on a Postgres advisory lock, keyed by the first eight bytes of `sha256(organization_id + "/" + digest)`. Collisions serialise unrelated digests and cost nothing but concurrency, which is the correct failure direction for a lock.

| Actor | Sequence |
| --- | --- |
| Writer, new object | Upload to staging **without** the lock (the long part); then in one transaction: take the lock, promote, read back and verify, insert the attachment row, commit; then release the staging lease and object |
| Writer, **existing object** | In one transaction: take the lock, verify the object by read-back, insert the attachment row, commit |
| Sweep | Per candidate digest, in one transaction: take the lock, **recheck** that no attachment row references it, delete the object, commit |

**The idempotent shortcut takes the same lock, and holds it through the insert.** Round 2 left it outside the protocol entirely, so a sweep could delete the object between the shortcut's verification and its attachment row — producing exactly the dangling reference this section exists to prevent, on the path most likely to be taken. Verification and the row that makes the object reachable must be one critical section.

The recheck under the lock is what makes the sweep's decision sound: "unreferenced" is established in mutual exclusion with the commit that would make it referenced. A writer that has not yet taken the lock has not yet promoted, so there is nothing at the digest key to delete.

The lock is held across a server-side copy and a read-back, which are remote calls inside a database transaction and worth stating rather than hiding. Both are bounded by a context timeout, and the alternative is the race. The upload — the genuinely long operation — stays outside.

### The lock cannot fence a remote call it does not control

An advisory lock lives in a Postgres connection. If that connection dies while the delete is **in flight at the object store**, the lock is released and the delete is not cancelled. A writer can then take the lock, verify or promote the object, insert its attachment row, commit — and the delayed delete arrives afterwards and removes an object a committed row references. The lock protocol above is necessary and not sufficient, and no amount of ordering inside Postgres fixes it, because the operation being ordered is outside Postgres.

So the intent to delete is made **durable before the delete is issued**, and the claim outlives the connection:

| Step | Under the digest lock | Durable state |
| --- | --- | --- |
| 1 | Recheck unreferenced, insert a **deletion claim**, commit | Claim exists |
| 2 | — | Remote delete issued |
| 3 | Clear the claim, commit | Claim gone |

A crash between 1 and 3 leaves a claim, which is exactly the state a later actor must not ignore. **Every writer checks for a live claim under the digest lock**, and on finding one it finishes the claim rather than working around it: re-issue the delete (idempotent), clear the claim, and then take the **full upload path** — never the existing-object shortcut, because the object under a claim may be deleted at any moment by a delete already in flight. `PutAttachment` always receives the source bytes, so re-uploading is always available; a writer is never stuck.

A reconciler run at `dataplane-up` finishes claims left by processes that died, so a claim cannot outlive the machine that made it.

This is the same shape as the staging lease below: a durable record of an intention that a crash cannot silently abandon.

**Staging is leased, not swept by age.** Round 2 argued that deleting a live staging object was safe because the writer's promote would then fail. That is precisely the reasoning ADR 0027 forbids: **destructive recovery must never remove another actor's in-progress work**, and "the victim finds out" is not a mitigation.

Round 3 answered with a renewable lease, which is still a timer: a writer paused past its term has its staging object deleted and then resumes, promoting an object that no longer exists — or worse, one another writer has since created at the same staging key. **Expiry alone cannot decide whether a writer is alive.**

The lease is therefore **fenced by an owner token**, and the fence is checked where it matters:

- a writer generates a token and inserts the lease — organization, staging key, token, expiry — before the first byte;
- **renewal is conditional**: `expires_at` moves only where the row still carries *this* token and has not expired. Zero rows updated means the lease is lost, and the writer aborts. There is no re-insert, so an expired lease can never be resurrected by the actor that lost it;
- **ownership is verified immediately before promotion**, under the same digest lock that guards the promote. A writer that lost its lease — paused, descheduled, or partitioned — cannot promote, whatever it believes about its own liveness;
- cleanup deletes the staging object and its lease row together, only where the lease is absent or expired.

The fence is what makes the timer safe rather than the timer making the fence unnecessary: expiry decides when cleanup *may* act, and the token decides whether a writer *may still* promote. Both are needed, and round 3 had only the first.

The alternative is a session-level advisory lock held for the whole upload, which fences correctly but pins a connection per concurrent upload for as long as the upload runs. The lease keeps connections free, at the cost of a table.

The final-object sweep never considers the staging prefix, and staging cleanup never considers digest keys.

**Two new tables in this item** — staging leases and deletion claims — each a new migration, on item 3's rule that a family is added by the item that first needs it.

**The grace period stays as defence in depth, and cannot be zero.** It supplements the lock rather than replacing it, and D8's rejection of sweeping without one stands. Age is tested by **injecting a clock**, not by disabling the rule — a test that switches the guard off proves the guard is switchable.

## D6a. Attachment truncation, which is what makes the sweep able to reclaim anything

Round 2 approved adding this and then omitted it from the design, which would have left every object referenced forever and `DeleteUnpinned` reclaiming nothing. `binary_attachments` joins item 5's truncation pass under exactly its rules:

- **organization-scoped and horizon-bounded**, on `created_at`;
- **pinned rows excluded in the `WHERE`**, never discovered at commit — `retention_pins` references attachments `ON DELETE RESTRICT`, which errors rather than skipping;
- **reported with the same reconciling buckets**: `candidates = deleted + retained_pinned`, enforced by the pass's own accounting rather than only asserted;
- placed **beside `audit_artifacts`** in the deletion order. Nothing but pins references attachments, so its position is not forced by a foreign key — it sits with the other pinnable family because that is where a reader will look for it.

**Deleting the row does not delete the object.** It makes the object unreachable, and the sweep reclaims it afterwards under D6's lock. The two steps are deliberately separate: the relational pass runs under one snapshot at `REPEATABLE READ`, and object deletion cannot participate in that snapshot.

**Concurrency with pin creation is safe by the schema, but the operational outcome is measured rather than asserted.** The foreign key guarantees the thing that matters: no interleaving leaves a pin pointing at a deleted attachment. It does **not** by itself tell either party which error it sees — under `REPEATABLE READ` the loser may surface `40001` or the named foreign-key violation depending on lock acquisition and commit order, and item 5's retry handles only `40001`.

Round 3 stated both outcomes as though they were known. They are not, so this item **runs both orderings under a barrier** — pin-then-truncate and truncate-then-pin, each with the competitor committing at a controlled point — records the SQLSTATEs actually observed against the pinned Postgres image, and handles what it finds: a serialization failure joins the existing retry, and a foreign-key violation is mapped at the seam to a diagnostic naming the attachment rather than surfacing a constraint name. The observed codes are written into this section once measured.

Tested with a pinned row that survives, an unpinned row that goes, and a second organization's rows untouched, as item 5 requires of every truncation.

## D7. Failure injection is the point of the tests

The invariant is entirely about what happens when a step fails, so a happy-path test that asserts the rows exist proves nothing. The adapter is fronted by a fault-injecting decorator, and each step is failed in turn.

| Case | Required outcome |
| --- | --- |
| Object put fails | No attachment row, no pin, no artifact |
| Put succeeds, attachment insert fails | Orphan object, sweepable; nothing references it |
| Attachment exists, artifact+pin transaction fails | Neither artifact nor pin — asserted by reading both tables |
| Source does not hash to the claimed digest | Rejected locally before promotion; nothing at the digest key |
| Body corrupted in transit | The **server** rejects the upload on the transport checksum; nothing at staging |
| Observed size differs from the stated size | Rejected before any row is written |
| `Put` where a **corrupt object already occupies the digest key** | The idempotent shortcut verifies, fails, and does not write an attachment row |
| Acceptance with a missing object | Refused, with the specific reason, and the artifact stays `draft` |
| Acceptance with a missing or digest-mismatched pin | Refused likewise |
| Invalidate a draft | Pins removed in the transition's own transaction |
| Archive an accepted artifact | Pins removed likewise — and separately from invalidation, since an invalidated artifact cannot be archived at all |
| Supersede an accepted artifact | Pins **retained**; accepted history without its evidence is not preserved |
| `Unpin` against an accepted artifact | Refused with the specific reason; the pin set survives |
| Acceptance where a pin names evidence the payload does not | Refused: an extra pin is an unreviewed retention claim |
| Acceptance where the payload names evidence with no pin | Refused, naming the missing reference |
| Idempotent shortcut racing the sweep, under a **barrier** | The sweep blocks on the digest lock and then finds the attachment row; the object survives |
| Staging cleanup against a **live lease** | The staging object survives; only an absent or expired lease permits deletion |
| Attachment truncation | Pinned row survives, unpinned row goes, another organization untouched |
| Amendment **adding** a reference | Accepted; the amendment holds a pin for the addition, the original keeps its own |
| Amendment **removing** a reference | Refused, naming the dropped reference; the original's pins are untouched |
| Amendment acceptance generally | The expected set comes from the effective payload assembled against the locked base, not from the patch |
| Writer **whose lease expired while it was still running** | Promotion refused at the ownership check, whatever the writer believed; no object at the digest key |
| Remote delete succeeds, then the claim-clearing transaction fails | The claim survives; the next writer finishes it, re-uploads, and commits a row whose object exists |
| Crash between claim and delete | The reconciler finishes the claim; no object is left that a row references |
| Pin racing attachment truncation, **both orderings**, under a barrier | No dangling pin either way; the observed SQLSTATEs are recorded and handled |
| Sweep racing the relational commit, under a **barrier** | With the writer holding the lock, the sweep blocks and then finds the reference; the object survives |
| Sweep inside the grace period | A fresh unreferenced object is not deleted |
| Corrupted object read | `GetAttachment` fails at EOF, and no destination file is left in place |

The barrier-controlled race follows item 5's recipe: launching a sweep and a writer concurrently and hoping they collide is flaky when it fails and vacuous when it passes. Each guard is mutation-verified — this item has more backstops than most, and item 5's lesson was that a backstop behind a working guard is untestable through the normal path.

## D8. Rejected cases

| Rejected | Why |
| --- | --- |
| A digest that is not 64 lowercase hex | The schema's `CHECK` shape; refused at the seam so the caller reads the field |
| A source whose local hash differs from the claimed digest | The whole contract: the digest is the address |
| A body the server's transport checksum rejects | Corruption on the wire, distinct from a wrong claim about content |
| A body whose length differs from the stated size | `size_bytes` would misreport storage forever |
| Negative or absent size | Schema check, refused early |
| Idempotent success over an unverified existing object | Blesses corruption into a new row |
| `GetAttachment` of a missing object | `ErrNotFound`, indistinguishable from another organization's, as everywhere in the seam |
| `GetAttachment` whose bytes do not match | `ErrInvariant`: the store contradicts itself |
| Blank or missing media type | An attachment nothing can interpret |
| Public raw object delete | Not offered at either layer; the sweep computes its own candidate set |
| Sweeping without the advisory lock, or without a grace period | Deletes in-flight writes |
| Deleting a staging object under a live lease | ADR 0027: destructive recovery must never remove in-progress work |
| Promoting after losing the lease | The ownership fence; expiry alone cannot decide whether a writer is alive |
| Taking the existing-object shortcut while a deletion claim is live | The object may vanish under an in-flight delete the lock cannot cancel |
| An amendment that removes an evidence reference | Accepted history would lose its evidence |
| Extracting an amendment's references from its stored patch | A patch is not the reviewed set |
| `Pin` or `Unpin` against an accepted or superseded artifact | Acceptance's verification would hold for an instant rather than for the artifact's life |
| Treating a composite multipart checksum as the object digest | Two different numbers, equal only for small single-part objects |
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

Resolved in round 2:

5. **Archival removes pins, approved** — `archived` is terminal and non-authoritative, so it is a reasonable explicit retention boundary.

Open for round 3: nothing. The ADR 0022 amendment (D5) is **proposed, not accepted**; its status line says so, and `plan_scope.md`'s two statements of the original commit order are synced only once Codex and DR accept it.
