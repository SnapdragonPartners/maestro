+++
title = "Phase 2 Item 6 Design: The Object Module"
edit_date = "2026-07-29"
status = "live"
summary = "Design for the object module: a blob adapter separated from the persistence seam that owns pins, content proven by a local hash with the server checksum kept to transport, an amended cross-store commit order whose expected evidence set is extracted from the reviewed payload and assembled from the locked base for amendments, pins mutable only while their holder is a draft, and reclamation fenced by owner-token leases whose expiry is one of three mechanisms rather than the only one, and by durable claims over version-specific deletes, with abandoned staging discovered by prefix scan where lease absence is the licence to delete."
type = "design"
+++

# Phase 2 Item 6 Design: The Object Module

Status: **live** — Accepted by Codex and DR after nine review rounds (four P1s, then five, four, three, four, one, one, one and one; all upheld). The pin-race contract is **measured and asserted**, not predicted (D6a). The ADR 0022 amendment (D5) is **accepted** by Codex and DR (2026-07-29).

**D1a, the D2 measurement table and D6b are amendments made during implementation, and all three are approved by DR (2026-07-29).** D1a and D2 are **Accepted**: Codex approved their substance the same day. **D6b awaits Codex's review of its wording** — Codex called for the amendment and has not yet read it — so the sweep, which rests on the same lease-absence reasoning, waits on that.

D1a and D2 correct claims this document made about the object store, from measurement against the pinned image: one primitive became two because the server's multipart listing does not accept a prefix, and the transport rejection is enforced by the chunk signature rather than by the checksum header that D2 credited. The reasoning either supported is unchanged; the mechanisms are not what was written. D6b is different in kind — it adds a capability the design lacked rather than correcting a mechanism it named.

Delivers ADR 0022's object module: put/get by content digest, existence check, pin/unpin, delete-unpinned, with an S3-compatible adapter over the MinIO container item 2 composes. The seam and its conventions are items 4 and 5's; this records only what differs.

Naming follows the phase directory (`design_local_stack.md`, `design_queries_artifacts.md`, `design_calls_family.md`) rather than ADR 0017's kebab-case slug rule, which those three already diverge from. Renaming all four is a separate decision, not this item's.

## What is inherited, and what is missing

Item 2 composed MinIO, bind-mounted its data directory under the Maestro data root, derived its credentials from the root of trust, and probes `/minio/health/live` at startup. Item 3 created `binary_attachments` and `retention_pins`. Item 4 built the seam. Item 5 left `binary_attachments` out of truncation deliberately, because deleting a row whose bytes live in object storage is this item's problem.

**Nothing creates the bucket.** `stack.Config` names it, `Bootstrap()` publishes it, and no code has ever issued a create — so `dataplane-up` reports a ready plane that cannot store an object. D3 closes this.

**ADR 0022's commit order cannot be implemented as written**, so this item amended the ADR rather than quietly restating it in a phase document (D5).

## D1. Two layers, because they answer to different authorities

Round 1 collapsed the blob store and the object module into one interface, dropped pin/unpin from it — which ADR 0022 and the plan both require — and then exposed a raw `Delete` that the same document said was not offered. Two mistakes with one cause: **the S3 client and the persistence module are not the same thing**, and naming them both "the object module" made the contract incoherent.

**Layer 1 — `objects.Blob`, the adapter.** Knows bytes and keys, nothing about artifacts, pins or organizations beyond a key prefix. Its surface is internal to the module; nothing above imports it.

**Versioning is part of this contract, not a bucket setting behind it.** D6 fences late deletes with version-specific deletion, and turning versioning on changes what the obvious primitives mean: `Delete(key)` no longer removes bytes — it writes a **delete marker** and leaves the stored version — and a prefix listing does not necessarily enumerate versions or delete markers at all. An adapter that hid versioning would offer operations whose names no longer describe what they do.

| Primitive | Returns / used by |
| --- | --- |
| `PutStaged` | Uploads to a staging key, **returning its version id**, and failing if the store returns one that cannot fence a delete — none at all, or the reused `null` slot |
| `Promote` | Server-side copy of a **named staged version** to the digest key, **returning the new version id**; multipart above the single-request copy limit (D1a) |
| `Get`, `Stat`, `Exists` | Reads and verification |
| `ListVersions` | Enumerates every version of a key or prefix, **including delete markers and the `null` version** left by anything written before versioning was enabled |
| `DeleteVersion` | Removes **exactly one** named version, and nothing else |
| `ListUploadsForKey` | Enumerates the incomplete multipart uploads on **exactly one key**, with their upload ids |
| `ListUploadsUnder` | Enumerates the incomplete multipart uploads **under a prefix** (D1a) |
| `AbortUpload` | Aborts **exactly one** upload id on one key |
| `EnsureBucket` | `dataplane-up`: creates the bucket, enables versioning, and **verifies on every run that it is still enabled** |

There is no `Delete(key)` and no `ListPrefix`. Both are the version-unaware shapes whose behaviour changes silently under a versioned bucket, and neither has a caller once deletion is version-specific: the sweep enumerates versions and removes them one by one, and staging cleanup deletes the versions it finds.

**Incomplete multipart uploads are a third storage state, invisible to both of the others.** A process that dies partway through a multipart upload leaves uploaded parts that are not an object version — `ListVersions` does not show them and `DeleteVersion` cannot remove them — so a lease expires, its cleanup finds nothing to delete, and the parts occupy storage forever. Neither the object nor the version vocabulary can express that state, which is why the adapter needs its own pair of primitives for it rather than a cleverer query over the first two.

**Abort is upload-id-specific, and the convenient key-scoped form is refused.** The pinned client's high-level `RemoveIncompleteUpload` aborts *every* upload in progress on a key. Round 7 used it on digest keys as well as staging keys, which quietly reintroduced the unfenced-delete class that versioning exists to close: **digest keys are reused**, so an abort issued by a sweep whose lock has since been released — or by a reconciler finishing an old claim — kills a *newer* writer's promotion. The key is the same; the upload is not.

So the adapter is built on the client's lower-level pair, which is upload-id-specific at the pinned version: `Core.ListMultipartUploads` returns each upload's id, and `Core.AbortMultipartUpload` takes one. Key-scoped abort remains correct for **staging** keys, which are unique per upload — but it is expressed as "enumerate this key, abort the ids found" rather than as a key-level call, so no operation in this module can abort an upload nobody named.

`EnsureBucket` **verifies** rather than assuming, because versioning can be turned off after the fact — by an operator, a restored backup, or a hand-run `mc` command — and every fence in D6 silently stops fencing if it is. A plane that cannot prove versioning is on refuses to start rather than running unprotected.

## D1a. What the pinned server actually does with a multipart listing

Amended during implementation, from measurement rather than review. Two
things the design assumed about `ListMultipartUploads` are not true of the
pinned MinIO image (`RELEASE.2025-09-07T16-13-09Z`), and each fails silently
rather than loudly, which is the class this document has spent nine rounds
closing.

**The prefix parameter is an exact object key.** Measured:

| Argument | Result |
| --- | --- |
| `""` | Every incomplete upload in the bucket |
| `staging/org/upload-7` | That key's uploads |
| `staging/` | **Nothing, with no error** |
| `staging/org/uploa` | **Nothing, with no error** |

This is deliberate upstream and long-standing, not a defect in this
deployment — `minio/minio#11686` was closed with "list multipart uploads only
return values for exact object name", and `#20989` tracks the S3 divergence.
So the sweep's candidate discovery, which asks for one organization's residue
by prefix, would have been told there is none and would have reclaimed
nothing. A single prefix-taking primitive is therefore not implementable
against this server, and the design's one row becomes two: **`ListUploadsUnder`
enumerates the whole bucket and filters in Go**, and `ListUploadsForKey` uses
the exact-key form the server does answer.

Both filter client-side, which is also what makes them correct on a store with
real S3 semantics: there, the exact-key form would return every key the given
one prefixes, and staging cleanup would abort a different writer's upload —
the unfenced abort of D1.

**The requested page limit is ignored.** Asked for one upload with four in
the bucket, the pinned server returns all four and reports `IsTruncated`
false. Nothing here establishes what it does at a scale where it might
truncate on its own; what is measured is that it does not truncate on
request, which is the only lever a test has. The two-marker paging of round
8 is therefore unexercised against this server. It stays, because a store that honours
the protocol truncates at a thousand and answers the rest only to a correct
marker pair, and ADR 0022 names other backends as a later choice — but it is
tested against canned responses, since no real-server test in this suite can
fail when it is broken. Both guards were confirmed unexercisable by mutation:
they survive every real-server test in the package.

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

## D2. Four facts about the bytes, and the mechanisms that answer them

Round 1 hashed the source and called it verification, which proves what the client read and nothing about what arrived. Round 2 then swung too far and treated the server checksum as proof of the address. Both were one mistake: **one check was asked to answer three different questions** — what the source is, what arrived, and what landed.

**The server checksum is transport integrity at best, and never the address.** Round 2 called `PutObjectOptions.Checksum` a server verification of our digest; it is not. It selects an *algorithm* — the client computes the value — and for a multipart upload the SHA-256 it carries is a **composite** checksum-of-checksums, which is not the full-object SHA-256 this design uses as the key. Comparing it to a digest would compare two different numbers and pass whenever they happened to be equal, which for a small single-part object they are. That is the worst kind of check: correct on the easy case, silently absent on the large evidence media this module exists for.

Four separate facts are needed, and each has its own mechanism:

| Fact | How |
| --- | --- |
| The **source** hashes to the claimed digest | Hash the complete stream **locally** while it is read, and compare |
| The source is the **stated length** | Count bytes; after `size` bytes, one more read must return EOF |
| The bytes **arrived** as sent | The wire mechanisms, retained as transport verification: the client signs each chunk, and the upload checksum is enforced where the payload is unsigned (measured below) |
| The **promotion** landed intact | Read the promoted object back and hash it |

The write path:

1. **Staging upload**, streaming through a hashing, counting reader, with a SHA-256 upload checksum enabled for transport. The local hash is the authority on content; the wire is guarded by the chunk signature and that checksum together, in that order (measured below).
2. **Length and digest check.** A source longer than the stated size fails — the check is an explicit read past `size` that must return EOF, not merely "we stopped at `size`", which silently truncates. A source shorter than stated fails on the count. Then the local hash must equal the claimed digest.
3. **Promote** by server-side copy of the **named staged version** to the digest key — multipart above the protocol's single-request copy limit (D1a) — then **read the promoted object back and hash it**. A composite checksum cannot answer this, and a copy landing intact is a claim like any other. The cost is one read per genuinely new object, which the idempotent shortcut already avoids paying twice.
4. **Staging release** — the lease row and the staging object (D6), whose failure is logged rather than failing a completed write.

**The idempotent shortcut verifies too.** An object already at the digest key is *not* proof of correct content — it is exactly where a previously corrupted or partially promoted object would sit, and returning success would bless it into a new attachment row. So the shortcut reads the object back and hashes it. That costs one read of an object whose upload it skips, so the shortcut still wins; correctness is not the thing being traded for speed.

**The transport claim is proven, not cited** — and proving it corrected which mechanism gets the credit. A test flips one byte of the payload *at the transport*, after the client has read, hashed and signed it, against the **pinned MinIO image**. Measured, with every length left intact:

| Configuration | Outcome |
| --- | --- |
| Signed chunks + checksum — **what ships** | Refused, `SignatureDoesNotMatch` |
| Unsigned payload + checksum | Refused, `XAmzContentChecksumMismatch` |
| Neither | **Accepted; the corruption is stored** |

So on the shipped path the per-chunk signature refuses the body before the checksum is ever consulted; the checksum is enforced too, and is what would catch it if the payload were ever unsigned. The design's earlier wording gave the header sole credit for the wire, which the measurement does not support. Nothing else changes: both values are computed by the client from the same buffer, so neither says anything about content, and the local hash remains the only proof of the address.

The third row is the control. Without it the first two are unfalsifiable — a server ignoring both would fail no assertion, because nothing else in the module ever sends bytes it did not mean to send.

**Enabling the checksum is not free of configuration.** The pinned client sends it as a trailing header and **refuses the request outright** unless the client was built with trailing headers enabled — every upload fails with `Checksum requires Client with TrailingHeaders enabled` rather than falling back to an unchecked one. The first integration test written against this adapter is what found it; nothing in the module had ever completed an upload.

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

Round 1 restated the order in this document. That was the wrong instrument: a phase design does not get to reinterpret an accepted ADR's invariant. **ADR 0022 is amended by this item, before implementation** — accepted by Codex and DR on 2026-07-29 — stating the order in terms that can be built:

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

**Every pin in a chain is held by the ORIGINAL**, including those an amendment introduces. Round 4 had each artifact hold what it introduced, which leaks: item 4 refuses to archive an amendment at all, so archiving the original would remove only the original's own pins and every amendment-held pin would survive forever, with no artifact able to release them. One holder, one lifecycle.

| Acceptance | Expected pin set, all held by the original |
| --- | --- |
| Original | `extract(payload)` |
| Amendment | `extract(effective)` — the base's references plus the ones it adds |

Amendment acceptance therefore extends the original's pin set in the same transaction that accepts the amendment, and it is an **internal** write, not the public draft-only `Pin`: the original is `accepted` by then, and the draft-only rule exists to stop callers dismantling a verified set, not to stop the seam maintaining one.

**Amendments may add references and may not remove them.** Exact set equality over the effective payload would otherwise drop a pin the original still needs, and ADR 0021 preserves accepted history immutably — history without its evidence is not preserved. So acceptance additionally requires `extract(base) ⊆ extract(effective)`, and an amendment that drops a reference is refused with that specific reason.

This is **this design's retention rule**, not an application of ADR 0028: that ADR's additive-within-version evolution is about schema compatibility, and citing it here would borrow authority it does not grant. The rule stands on ADR 0021's immutable accepted history alone.

The alternative — tracking historical pins separately so a removal keeps the old evidence alive under a different holder — buys a case nobody has yet, at the cost of a second retention concept.

The object-existence check is the one precondition that reaches outside the database. It is safe in this order because the attachment row already exists, and the sweep's reachable set is exactly the attachment rows (D6) — so between the check and the commit there is nothing that may delete the object.

**Pins follow the lifecycle that justifies them, in the transition's own transaction.** Round 1 created pins and never removed them, so a draft that was invalidated pinned its evidence forever.

| Transition | Pins |
| --- | --- |
| `draft` → `invalidated` | **Removed.** The artifact never became authoritative; nothing justifies holding its evidence. |
| `draft` → `accepted` | Retained, and verified as a precondition. |
| `accepted` → `superseded` | **Retained.** ADR 0021 preserves accepted history immutably, and history without its evidence is not preserved. |
| `accepted`/`superseded` → `archived` | **Removed** (approved in round 2: `archived` is terminal and non-authoritative, so it is a reasonable explicit retention boundary). Without it nothing ever releases a retired artifact's hold on storage and retention has no terminal state. |

**Pins are mutable only while the holder is a draft ORIGINAL.** Otherwise acceptance verifies a set that any later `Unpin` can dismantle, and the invariant holds for an instant rather than for the artifact's life. So `Pin` and `Unpin` lock the holding artifact and classify it in Go — item 4's shape — and permit the change only for an original in `draft`; `accepted` and `superseded` refuse with the specific reason.

**A draft amendment may not use them at all**, which is a rule the "any draft" version of this missed. All chain pins are held by the original, so a draft amendment calling `Pin` would mutate the **already-accepted original's verified set** before anyone reviewed the amendment — and invalidating that draft afterwards could not identify which of the original's pins came from it, since a pin records its holder and not its proposer. An amendment's additions are therefore written **only by amendment acceptance**, internally and atomically, where the reviewed effective payload is what decides them.

Lifecycle transitions do their own pin removal through **internal queries**, not through the public `Unpin`, so the draft-only rule is not something a transition has to be exempted from. That also keeps removal in the transition's own transaction, which is what makes it atomic with the status change.

## D6. The sweep is coordinated, not timed

Round 1 protected in-flight writes with a one-hour grace period. **A grace period is not concurrency control** — a paused writer or a slow upload can exceed any constant, and age alone cannot prove abandonment. Correctly rejected.

The window is real: between the object landing at its digest key and the attachment row committing, the object is legitimately unreferenced, and a sweep running then deletes the bytes of a commit in progress.

**Writers and the sweep serialise per `(organization, digest)`** on a Postgres advisory lock, keyed by the first eight bytes of `sha256(organization_id + "/" + digest)`. Collisions serialise unrelated digests and cost nothing but concurrency, which is the correct failure direction for a lock.

| Actor | Sequence |
| --- | --- |
| Writer, new object | Upload to staging **without** the lock (the long part); then in one transaction: take the digest lock, lock the lease row, verify ownership, promote, read back and verify, insert the attachment row, commit; then release the staging lease and object |
| Writer, **existing object** | In one transaction: take the digest lock, confirm no live deletion claim, verify the object by read-back, insert the attachment row, commit |
| Sweep | Three steps, **not one transaction**: (1) under the digest lock, recheck unreferenced, record a deletion claim naming the object **versions** it will remove, commit; (2) issue version-specific deletes; (3) under the digest lock, clear the claim, commit |

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

A crash between 1 and 3 leaves a claim, which is exactly the state a later actor must not ignore.

**A durable claim records intent, not completion — so the deletes are made version-specific.** Round 4 had a writer take over a live claim: re-issue the delete, clear the claim, and re-upload. That does not fence the *original* delete, which may still be in flight and arrive after the re-upload, removing the replacement. Intent is not a fence.

The bucket is therefore **versioned**, and every sweep delete names the version it removes:

- the claim records the object's **version ids** as observed under the lock;
- deletion is version-specific, so a delete issued against version *n* cannot touch a version *n+1* created afterwards. A late arrival removes something already condemned and nothing else;
- **writers never clear or take over another actor's claim.** They may proceed — a fresh upload creates a new version the pending delete cannot affect — but a live claim forbids the existing-object shortcut, because the current version may vanish at any moment. `PutAttachment` always receives the source bytes, so the full path is always available and a writer is never stuck;
- the claim's own completion is the **owner's** job, or the reconciler's at `dataplane-up`, which re-issues the version-specific deletes idempotently and clears the row. Repeating a version-specific delete is harmless by construction, which is what makes recovery safe to run at any time.

Versioning also means the sweep enumerates and removes **every** version of an unreferenced digest, not just the current one.

A promote is a server-side copy, which is multipart for large objects and can die halfway, so the digest key accumulates incomplete uploads too. The claim therefore records **both** what it observed under the lock — the version ids *and* the upload ids — and cleanup touches only those. Holding the lock is not enough on its own: the lock is released at commit, while the aborts happen afterwards, so a claim naming ids observed at a point in time is what keeps a delayed abort from reaching a later writer's upload.

**A digest key with only incomplete uploads is itself a sweep candidate.** It has no version, so version enumeration never discovers it, and nothing else would ever look at it — the residue of a promote that died before completing, which the reachability check cannot see because there is no object to be unreferenced. The sweep's candidate discovery therefore unions version enumeration with upload enumeration.

This is the same shape as the staging lease below: a durable record of an intention that a crash cannot silently abandon, plus a fence that makes a late arrival harmless.

**Staging is leased, not swept by age.** Round 2 argued that deleting a live staging object was safe because the writer's promote would then fail. That is precisely the reasoning ADR 0027 forbids: **destructive recovery must never remove another actor's in-progress work**, and "the victim finds out" is not a mitigation.

Round 3 answered with a renewable lease, which is still a timer: a writer paused past its term has its staging object deleted and then resumes, promoting an object that no longer exists — or worse, one another writer has since created at the same staging key. **Expiry alone cannot decide whether a writer is alive.**

The lease is therefore **fenced by an owner token**, and the fence is checked where it matters:

- a writer generates a token and inserts the lease — organization, staging key, token, expiry — before the first byte;
- **renewal is conditional**: `expires_at` moves only where the row still carries *this* token and has not expired. Zero rows updated means the lease is lost, and the writer aborts. There is no re-insert, so an expired lease can never be resurrected by the actor that lost it;
- **the lease row is LOCKED for the whole promotion**, not merely checked before it. Round 4 verified ownership immediately before promoting, which leaves the window open: promotion and read-back are remote calls, the lease can expire while they run, and cleanup can then delete the staging object out from under an authorised promotion. So the promoting transaction holds `SELECT … FOR UPDATE` on the lease row across the ownership check, the promote, the read-back and the attachment insert, and **cleanup takes the same row lock and rechecks expiry under it** — so it either waits or finds the lease alive;
- cleanup runs against a lease that is absent or expired **as judged under that lock**, and it must handle **both** ways a writer can die, because they leave different residue:

    | Crash window | What survives | Cleanup step |
    | --- | --- | --- |
    | During the multipart upload | Uploaded **parts**, no version at all | Enumerate the staging key's upload ids and abort each |
    | After completion, before the version id is recorded | A **version** the lease does not name | Enumerate the staging key's versions and delete each |

    So cleanup **aborts the enumerated upload ids first, then enumerates versions, then deletes the lease row** — in that order, because an upload that completes between the two steps appears as a version the enumeration then finds. It never assumes the lease carries a version id: the staging key is unique per upload, so enumerating it is both safe and complete, and a lease whose writer died before recording anything is exactly the common case. A key-level delete would write a delete marker and reclaim nothing.

    Cleanup is idempotent and re-runnable by construction, so a version that appears after one pass is removed by the next.

Three mechanisms, each answering a different question: **expiry** decides when cleanup may consider a lease abandoned, the **token** decides whether a writer may still promote, and the **row lock** decides who acts first when both are live at once. Round 3 had only the first; round 4 added the second and left the third as a race.

### D6b. Orphan discovery, because the lease is the only record

Amended during implementation. **Awaiting review.**

The claim above that "cleanup is idempotent and re-runnable by construction, so a version that appears after one pass is removed by the next" was not true as designed, and the reason is structural: cleanup **deletes the lease row** when it finishes with a key, and that row is the only record by which the key can be found. The final-object sweep never considers the staging prefix, and nothing else looks there — so anything appearing after a pass was undiscoverable, which is to say permanent.

Two ways it appears:

- a writer **paused past its term** resumes and starts an upload. The owner token stops it *promoting*; nothing stops it writing to its own staging key;
- an upload **completes between** cleanup's abort step and its version enumeration, appearing as a version the pass has already looked past.

So cleanup gains a second half: enumerate the organization's staging prefix in **both** storage vocabularies, and empty every key **no lease owns**.

**The absence of a lease is the licence, and it is a strong one.** A writer inserts its lease before the first byte, and a lost lease can never be resurrected — there is no re-insert. A staging object with no lease therefore belongs to a writer that *provably cannot promote*, and removing it is not removing work that might still complete. ADR 0027 forbids destroying another actor's in-progress work; it does not require preserving work that has been made impossible. A key that still has a lease, in any state, is left to the lease-driven path above, which locks its row and rechecks expiry — the orphan pass does neither and must not act on it.

**The two outcomes are accounted for separately.** A released lease is an abandoned writer collected, which is routine. A collected orphan is residue that outlived its own discovery record, which says something went wrong earlier and is worth an operator's attention rather than being folded into a total.

**Revisit before a high-use cloud instance.** The unbounded discovery below is sized to local mode, where the staging prefix is near-empty and a full enumeration costs almost nothing. Whether that holds in a hosted deployment depends on how heavily object storage is actually used, which nothing yet measures — so paging the enumeration, or carrying a cursor between passes, is a decision to take when there is traffic to size it against rather than now (parked in `notes_parking-lot.md`).

**Bounded work, unbounded discovery, and the distinction is deliberate.** Owned keys are filtered out *before* the destructive budget applies, so they never consume it — otherwise a busy organization would starve its own orphan collection, and starve it repeatedly, since the same keys sort first every pass. Ownership is settled in **one** query for the whole candidate set: asking per key made the round trips a function of how much legitimate work was in flight, so an organization with many live writers and no residue at all did the most work and collected nothing. The prefix enumeration itself is not bounded, which is acceptable because a staging prefix is normally near-empty — writers release their own keys — and because an orphan is discovered by its own residue rather than by a record that can be lost, so a pass that defers the remainder loses nothing.

**The writer's own release runs the same protocol**, and originally did not: it deleted versions only, never aborting an incomplete multipart upload, and it deleted the lease whether or not that succeeded. Dropping the lease after a failed emptying is what converted recoverable residue into an orphan. The lease now goes only when the key is provably empty; left in place it expires and the next pass collects it under the lock.

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

Round 3 stated both outcomes as though they were known. They were not, so both orderings were **run under a barrier against the pinned Postgres image before this design was finished** (`pinrace_integration_test.go`, retained as the regression test). What the server actually does:

| Ordering | Who fails | SQLSTATE | Constraint named |
| --- | --- | --- | --- |
| Truncate first, pin second | The **pin** | `23503` foreign_key_violation | `retention_pins_attachment_fkey` |
| Pin first, truncate second | The **truncation** | `23001` **restrict_violation** | `retention_pins_attachment_fkey` |

In the second ordering the `DELETE` aborts and the surrounding **commit then fails as a rollback**, so the whole pass is lost — which is why the retry has to be whole-operation rather than per-statement.

Two things this measurement changes, neither of which was guessable:

- **`23001` is `restrict_violation`, not `23503`.** They are different codes, and a handler matching only foreign-key violations would miss the one the truncation pass actually sees. In the second ordering the `DELETE` aborts and the surrounding commit then fails as a rollback, so the whole pass is lost.
- **Nothing raises `40001` here**, so item 5's serialization retry does *not* cover this case. Left alone, a single concurrent pin would intermittently kill an entire truncation pass.

So the contract is: **`23001` on `retention_pins_attachment_fkey` joins the pass's whole-operation retry**, alongside `40001`. A retry is exactly right — the next attempt's snapshot sees the pin, the `NOT EXISTS` excludes the row, and the pass completes with that attachment correctly retained.

**The predicate is both halves, not the code alone.** `23001` is a generic restriction violation, and retrying every one of them would take a *persistent* `RESTRICT` failure — a genuine dependency the pass must not delete through — and turn it into three identical attempts followed by `ErrConcurrentTruncation`, which describes concurrency that was never the problem. So the predicate matches SQLSTATE `23001` **and** constraint name `retention_pins_attachment_fkey`; every other restriction propagates unchanged. Both halves are mutation-tested, since a predicate that ignored the constraint name would pass a test that only ever produces this one.

The constraint name is available to match on: `pgconn.PgError.ConstraintName` is populated for both codes, measured alongside them rather than assumed.

And `Pin` maps `23503` on the same constraint to a diagnostic naming the attachment as truncated, rather than surfacing a constraint name to a caller who cannot act on it.

The invariant the foreign key was there to protect held in both orderings: no interleaving produced a pin pointing at a deleted attachment, and the pinned row survived.

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
| Amendment **adding** a reference | Accepted; the pin for the addition is written **to the original's set**, in the acceptance transaction, and the original's existing pins are unchanged |
| Amendment **removing** a reference | Refused, naming the dropped reference; the original's pin set is exactly what it was |
| Amendment acceptance generally | The expected set comes from the effective payload assembled against the locked base, not from the patch |
| Writer **whose lease expired while it was still running** | Promotion refused at the ownership check, whatever the writer believed; no object at the digest key |
| Remote delete succeeds, then the claim-clearing transaction fails | The claim survives. A writer arriving meanwhile **leaves it alone**, takes the full path, and commits a row against a **new version** the condemned delete cannot touch; the owner or the reconciler clears the claim afterwards |
| Crash between claim and delete | The **reconciler** — not a writer — re-issues the claim's recorded version and upload ids and clears it; no object is left that a row references |
| A writer meeting a live claim | It never clears or completes the claim, and never takes the existing-object shortcut |
| Pin racing attachment truncation, **both orderings**, under a barrier | Measured: `23503` to the pin, `23001` to the truncation; no dangling pin either way. Retained as the regression test for the retry predicate |
| Original with **several accepted amendments**, then superseded | Every pin in the chain retained, including amendment-introduced ones |
| Original with **several accepted amendments**, then archived | **Every** pin in the chain removed — the case that leaks if amendments hold their own |
| Lease expiring **during** promotion, with a barrier after the ownership check | Cleanup blocks on the lease row; the promotion completes; the staging object is not deleted under it |
| Sweep's delete in flight while a writer re-uploads | The version-specific delete removes only the condemned version; the writer's new version survives |
| Reconciler re-running a claim's deletes | Idempotent; the claim clears and nothing else is removed |
| Writer dying **during** a multipart staging upload | Cleanup aborts the incomplete upload; no parts survive, and the bucket reports no storage for that key |
| Writer dying **after** completing the upload but **before** recording the version | Cleanup enumerates the staging key and deletes the version it never recorded |
| Promote dying partway | The sweep aborts the upload ids its claim recorded, and nothing else |
| A **delayed abort** from an old claim, against a **newer** promotion on the same digest key | The newer upload survives: only the claim's recorded ids are aborted |
| A digest key carrying **only** incomplete uploads | Discovered as a sweep candidate and reclaimed, though it has no version to find it by |
| Public `Pin` against a **draft amendment** | Refused; the original's verified set is untouched |
| Amendment acceptance that **fails** | The original's pin set is exactly what it was before |
| A **persistent** `RESTRICT` violation from another constraint | Propagated unchanged, never retried into a concurrency error |
| Versioning disabled behind the module's back | `EnsureBucket` refuses to start the plane — proven by rewriting the versioning read in transit, since a cooperating server re-enables it on request and then reports it enabled |
| Body corrupted in transit with **neither** wire mechanism | Stored, corruption and all — the control that makes the two rejections above falsifiable (D2) |
| An incomplete upload asked for **by prefix** | The server answers nothing; the prefix is applied in the adapter (D1a) |
| A multipart listing asked to truncate | The server ignores `max-uploads`; the paging path is exercised against canned responses (D1a) |
| A key-level delete where a version-specific one was meant | Not expressible: the adapter offers no key-level delete |
| Cleanup that only deletes versions | Leaves multipart parts, which are neither an object nor a version |
| Cleanup that trusts the lease to name the version | The writer may have died before recording it |
| Key-scoped abort on a digest key | Digest keys are reused; it would kill a newer writer's upload |
| A sweep candidate set built from versions alone | Misses keys whose only residue is incomplete uploads |
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
| Clearing or taking over another actor's deletion claim | Intent is not a fence; the original delete may still arrive |
| An unversioned delete in the sweep | Cannot be fenced against a later re-upload, and on a versioned bucket writes a delete marker rather than reclaiming anything |
| Leaving incomplete multipart uploads to a bucket lifecycle rule | The plane's own cleanup would depend on a server-side policy nothing here configures or verifies |
| Retrying every `23001` | A persistent restriction is a real dependency, and reporting it as exhausted concurrency describes a problem that never happened |
| Public `Pin`/`Unpin` from a draft amendment | It would mutate the accepted original's verified set before review, and invalidation could not identify which pins to remove |
| Promoting while holding only a checked, unlocked lease | The lease can expire during the remote calls that follow |
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

Open: nothing. The ADR 0022 amendment (D5) is **accepted** (Codex and DR, 2026-07-29), and `plan_scope.md`'s two statements of the original commit order are synced to match.
