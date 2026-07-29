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

-- BinaryAttachmentExists answers without transferring the object.
--
-- name: BinaryAttachmentExists :one
SELECT EXISTS (
    SELECT 1 FROM binary_attachments
    WHERE attachment_id = @attachment_id
      AND organization_id = @organization_id
);

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
    now() + make_interval(secs => @term_seconds::double precision)
)
RETURNING *;

-- RenewStagingLease extends a lease the caller still holds.
--
-- Conditional on BOTH halves: the row still carries this token, and it has
-- not expired. Zero rows updated means the lease is lost, and the writer
-- aborts -- there is no re-insert, so an actor that lost its lease can
-- never resurrect it. `now()` is the transaction timestamp, which is the
-- same instant the schema's own defaults use.
--
-- name: RenewStagingLease :one
UPDATE staging_leases
SET expires_at = now() + make_interval(secs => @term_seconds::double precision)
WHERE organization_id = @organization_id
  AND staging_key = @staging_key
  AND owner_token = @owner_token
  AND expires_at > now()
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
-- name: LockStagingLease :one
SELECT sqlc.embed(staging_leases), now()::timestamptz AS locked_at
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
