-- The object module's relational half (item 6 design, D1 Layer 2).
--
-- The bytes live in object storage; these are the rows that make them
-- reachable, the leases that make staging cleanup safe, and the lock that
-- serialises a writer against the sweep.

-- TakeDigestLock serialises writers and the sweep on one (organization,
-- digest) for the rest of the transaction (design D6).
--
-- Transaction-scoped, not session-scoped: a session lock outlives the
-- commit and has to be released by name, so any path that returned early
-- would leak it. The key is computed by the caller from
-- sha256(organization + "/" + digest) -- collisions serialise unrelated
-- digests, which costs concurrency and nothing else, and that is the
-- correct direction for a lock to fail in.
--
-- name: TakeDigestLock :exec
SELECT pg_advisory_xact_lock(@lock_key::bigint);

-- CreateBinaryAttachment records a stored object.
--
-- The row is what makes the object REACHABLE: the sweep's reachable set is
-- exactly the attachment rows, so this insert is the commit that ends the
-- window in which a new object is legitimately unreferenced.
--
-- name: CreateBinaryAttachment :one
INSERT INTO binary_attachments (
    attachment_id, organization_id, object_digest, media_type, size_bytes
) VALUES (
    @attachment_id, @organization_id, @object_digest, @media_type, @size_bytes
)
RETURNING *;

-- name: GetBinaryAttachment :one
SELECT * FROM binary_attachments
WHERE attachment_id = @attachment_id
  AND organization_id = @organization_id;

-- CreateStagingLease claims a staging key before the first byte is sent.
--
-- The term is an interval, not an instant: every judgement about this
-- lease -- renewal, the ownership check under the row lock, and cleanup's
-- decision that it is abandoned -- is made against the SERVER's clock, so
-- the row must be written with it too. A client-computed expiry would put
-- one participant's clock skew into a decision every other participant
-- makes with a different clock.
--
-- name: CreateStagingLease :one
INSERT INTO staging_leases (
    staging_lease_id, organization_id, staging_key, owner_token, expires_at
) VALUES (
    @staging_lease_id, @organization_id, @staging_key, @owner_token,
    clock_timestamp() + make_interval(secs => @term_seconds::double precision)
)
RETURNING *;

-- RenewStagingLease extends a lease the caller still holds.
--
-- Conditional on BOTH halves: the row still carries this token, and it has
-- not expired. Zero rows updated means the lease is lost, and the writer
-- aborts -- there is no re-insert, so an actor that lost its lease can
-- never resurrect it.
--
-- clock_timestamp() throughout, for the reason spelled out under
-- LockStagingLease: `now()` freezes at the transaction's start, and any
-- statement that can wait -- this one waits for the row lock -- may then
-- judge expiry against an instant that has already passed.
--
-- name: RenewStagingLease :one
UPDATE staging_leases
SET expires_at = clock_timestamp() + make_interval(secs => @term_seconds::double precision)
WHERE organization_id = @organization_id
  AND staging_key = @staging_key
  AND owner_token = @owner_token
  AND expires_at > clock_timestamp()
RETURNING *;

-- LockStagingLease takes the row lock a promotion is performed under, and
-- reports the server's own clock alongside it.
--
-- The lock is held for the WHOLE promotion -- the ownership check, the
-- server-side copy, the read-back and the attachment insert -- rather than
-- merely checked before it. Those are remote calls, the lease can expire
-- while they run, and cleanup taking this same lock would otherwise delete
-- the staging object out from under an authorised promotion. Under the
-- lock, cleanup either waits or finds the lease alive.
--
-- The clock comes from the server so that ownership and expiry are judged
-- against the same instant the row was written with, not against a client
-- whose clock may differ.
--
-- It is clock_timestamp(), NOT now(). `now()` is the TRANSACTION's start
-- timestamp, and this transaction takes the digest lock before it reaches
-- this statement: after a long wait behind another writer, `now()` reports
-- an instant that may precede an expiry which has since passed, and the
-- ownership check would authorise a promotion for a lease that is already
-- gone. The whole point of reading the clock here is to read it AFTER the
-- waiting is done.
--
-- name: LockStagingLease :one
SELECT sqlc.embed(staging_leases), clock_timestamp()::timestamptz AS locked_at
FROM staging_leases
WHERE organization_id = @organization_id
  AND staging_key = @staging_key
FOR UPDATE;

-- DeleteStagingLease releases a lease the caller holds.
--
-- Fenced by the token: a writer must not release a lease it has lost, or
-- it would remove the record protecting a successor's staging object.
--
-- name: DeleteStagingLease :execrows
DELETE FROM staging_leases
WHERE organization_id = @organization_id
  AND staging_key = @staging_key
  AND owner_token = @owner_token;

-- LiveDeletionClaimExists reports whether a digest is condemned.
--
-- A claim's EXISTENCE is what makes it live, because clearing one deletes
-- the row. A writer meeting one may still proceed by the full path -- a
-- fresh upload creates a new version the pending delete cannot name -- but
-- it may not take the existing-object shortcut, because the current
-- version may vanish at any moment.
--
-- name: LiveDeletionClaimExists :one
SELECT EXISTS (
    SELECT 1 FROM deletion_claims
    WHERE organization_id = @organization_id
      AND object_digest = @object_digest
);

-- CreateDeletionClaim records what the sweep is about to remove, BEFORE it
-- removes any of it (design D6).
--
-- It RETURNS the row, and the sweep issues its deletes from what came back
-- rather than from what it enumerated. That is not a convenience: the whole
-- point of the claim is that the ids are durable before the first remote
-- call, so an id that failed to commit must not be deletable. Reading them
-- back from the database is what makes "deleted only what was recorded" a
-- property of the data flow instead of a discipline a later edit can drop.
--
-- The unique constraint on (organization_id, object_digest) is what stops
-- two sweeps condemning one digest at once: the loser's insert fails rather
-- than adding a second claim over the same storage.
--
-- name: CreateDeletionClaim :one
INSERT INTO deletion_claims (
    deletion_claim_id, organization_id, object_digest, version_ids, upload_ids
) VALUES (
    @deletion_claim_id, @organization_id, @object_digest,
    @version_ids::text[], @upload_ids::text[]
)
RETURNING *;

-- ClearDeletionClaim completes one claim, fenced by its own identity.
--
-- By claim id, never by digest. A claim is cleared only by the actor that
-- owns it or by the reconciler finishing that same row, and clearing "the
-- claim on this digest" would let one actor complete another's -- which
-- reports storage as reclaimed while the original delete may still be in
-- flight. Intent is not a fence, so the row that records the intent is the
-- thing that must be named.
--
-- name: ClearDeletionClaim :execrows
DELETE FROM deletion_claims
WHERE organization_id = @organization_id
  AND deletion_claim_id = @deletion_claim_id;

-- ListDeletionClaims enumerates every surviving claim, across every
-- organization, for the reconciler at `dataplane-up`.
--
-- The one read in this file that is NOT organization-scoped, and the reason
-- is that its caller is not a tenant. A claim is the plane's own record of
-- an unfinished intention: nobody asked for it, no tenant can see it, and
-- the recovery it drives is exactly what the tenancy rule exists to keep
-- tenants from doing to each other. Every lock and delete the reconciler
-- then issues is scoped by the claim's OWN organization_id, so the boundary
-- holds where it matters -- at the mutation, not at the enumeration.
--
-- Unbounded, deliberately. This table is empty whenever nothing is
-- mid-delete, and every row in it is storage already condemned whose claim
-- also forbids the existing-object shortcut for its digest until it clears.
-- A limit here would defer that cost to the next `up`, which may be days
-- away.
--
-- Oldest first, so a claim that has survived longest is finished first.
--
-- name: ListDeletionClaims :many
SELECT * FROM deletion_claims
ORDER BY claimed_at, deletion_claim_id;

-- DigestIsReferenced is the sweep's recheck, and the reason its decision is
-- sound (design D6).
--
-- The reachable set is exactly the attachment rows: an object is reclaimable
-- when nothing references its digest. Asked under the digest lock, this
-- establishes "unreferenced" in mutual exclusion with the commit that would
-- make it referenced -- so a writer that has not yet taken the lock has not
-- yet promoted, and there is nothing at the digest key to delete.
--
-- name: DigestIsReferenced :one
SELECT EXISTS (
    SELECT 1 FROM binary_attachments
    WHERE organization_id = @organization_id
      AND object_digest = @object_digest
);

-- ListClaimedDigests reports which of the given digests an unfinished claim
-- already condemns.
--
-- The same shape as ListReferencedDigests and for a sharper reason. A claimed
-- digest is not this pass's to touch, and it stays a candidate for as long as
-- its claim survives -- so classifying it only under the lock means it
-- consumes a slot of the per-pass budget every pass, forever. Enough of them
-- sorting ahead of an ordinary unreferenced digest and reclamation never
-- reaches it at all.
--
-- This is the pre-filter, not the decision. A claim inserted between this
-- read and the lock is caught by the locked recheck, which is the same
-- division of labour references have.
--
-- name: ListClaimedDigests :many
SELECT object_digest FROM deletion_claims
WHERE organization_id = @organization_id
  AND object_digest = ANY(@object_digests::text[]);

-- ListReferencedDigests reports which of the given digests are still
-- reachable, in one query for the whole candidate set.
--
-- A pre-filter, not the decision: the decision is DigestIsReferenced under
-- the lock. This exists so a sweep over an organization whose objects are
-- nearly all referenced -- the normal case -- does not take a lock and a
-- transaction per digest to discover it.
--
-- name: ListReferencedDigests :many
SELECT DISTINCT object_digest FROM binary_attachments
WHERE organization_id = @organization_id
  AND object_digest = ANY(@object_digests::text[]);

-- Retention pins (item 6 design, D5).
--
-- Pins are relational because that is what they are: rows with foreign
-- keys, an exclusive-arc target and a digest binding, which ADR 0021
-- requires to fail verification when dangling. A blob store tracking them
-- would hold a second, unconstrained copy of the same state.

-- CreatePin records one artifact's hold on one piece of evidence.
--
-- The digest is bound here, not looked up later: a pin recording a
-- different digest from its target protects nothing the artifact cites,
-- and acceptance compares the two precisely to catch that.
--
-- name: CreatePin :one
INSERT INTO retention_pins (
    retention_pin_id, organization_id, pinned_by_artifact_id,
    pinned_audit_artifact_id, pinned_attachment_id, pinned_digest
) VALUES (
    @retention_pin_id, @organization_id, @pinned_by_artifact_id,
    @pinned_audit_artifact_id, @pinned_attachment_id, @pinned_digest
)
RETURNING *;

-- ListPinsByArtifact returns everything one artifact holds.
--
-- Ordered so that two reads of an unchanged set compare equal without the
-- caller sorting: acceptance compares SETS, and a stable order makes the
-- diagnostic it produces on failure stable too.
--
-- name: ListPinsByArtifact :many
SELECT * FROM retention_pins
WHERE organization_id = @organization_id
  AND pinned_by_artifact_id = @pinned_by_artifact_id
ORDER BY pinned_audit_artifact_id NULLS LAST, pinned_attachment_id NULLS LAST, retention_pin_id;

-- DeletePin removes one pin by identity.
--
-- name: DeletePin :execrows
DELETE FROM retention_pins
WHERE organization_id = @organization_id
  AND retention_pin_id = @retention_pin_id
  AND pinned_by_artifact_id = @pinned_by_artifact_id;

-- DeletePinsByArtifact releases everything an artifact holds.
--
-- Used by the lifecycle transitions that end an artifact's claim on its
-- evidence -- invalidation and archival -- through INTERNAL queries rather
-- than the public Unpin, so the draft-only rule is not something a
-- transition has to be exempted from, and so removal happens in the
-- transition's own transaction and is atomic with the status change.
--
-- name: DeletePinsByArtifact :execrows
DELETE FROM retention_pins
WHERE organization_id = @organization_id
  AND pinned_by_artifact_id = @pinned_by_artifact_id;

-- GetAuditArtifactDigest reads the digest a pin on an Audit artifact must
-- bind, scoped to the organization like every other read here.
--
-- name: GetAuditArtifactDigest :one
SELECT payload_digest FROM audit_artifacts
WHERE artifact_id = @artifact_id
  AND organization_id = @organization_id;

-- GetAttachmentDigest reads the digest a pin on an attachment must bind.
--
-- name: GetAttachmentDigest :one
SELECT object_digest FROM binary_attachments
WHERE attachment_id = @attachment_id
  AND organization_id = @organization_id;

-- ListExpiredStagingLeases finds leases cleanup may CONSIDER abandoned.
--
-- Expiry decides only that: whether a writer still holds the lease is the
-- owner token's question, and who acts first when both are live is the row
-- lock's. This read is the first of the three and the weakest -- everything
-- it returns is rechecked under the lock before anything is deleted.
--
-- clock_timestamp(), not now(): a pass that has been running for a while
-- would otherwise judge expiry against the instant it started.
--
-- name: ListExpiredStagingLeases :many
SELECT * FROM staging_leases
WHERE organization_id = @organization_id
  AND expires_at <= clock_timestamp()
ORDER BY expires_at, staging_lease_id
LIMIT @row_limit;

-- DeleteExpiredStagingLease removes a lease cleanup has finished with.
--
-- Fenced by expiry, because cleanup holds no owner token -- that is the
-- writer's. The caller has already locked this row and rechecked expiry
-- under the lock; the condition here is the backstop that makes a delete
-- issued outside that discipline impossible rather than merely wrong.
--
-- name: DeleteExpiredStagingLease :execrows
DELETE FROM staging_leases
WHERE organization_id = @organization_id
  AND staging_key = @staging_key
  AND expires_at <= clock_timestamp();

-- ListOwnedStagingKeys reports which of the given keys still have leases.
--
-- Ownership is what orphan discovery turns on, and its ABSENCE is what
-- licenses deletion: a writer inserts its lease before the first byte and a
-- lost lease can never be resurrected, so a staging object with no lease
-- belongs to a writer that provably cannot promote.
--
-- One query for a whole candidate set, rather than one per key. Orphan
-- discovery examines every key under an organization's staging prefix, and
-- asking per key made the number of round trips a function of how much
-- legitimate work was in flight -- unbounded in exactly the case where
-- there is nothing to collect.
--
-- name: ListOwnedStagingKeys :many
SELECT staging_key FROM staging_leases
WHERE organization_id = @organization_id
  AND staging_key = ANY(@staging_keys::text[]);
