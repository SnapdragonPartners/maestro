-- Deletion claims: the durable record of an intent to delete, committed
-- BEFORE the delete is issued (item 6 design, D6).
--
-- An advisory lock lives in a Postgres connection. If that connection dies
-- while a delete is in flight AT THE OBJECT STORE, the lock is released and
-- the delete is not cancelled. A writer can then take the lock, promote,
-- read back, commit an attachment row -- and the delayed delete arrives
-- afterwards and removes an object a committed row references. No ordering
-- inside Postgres fixes this, because the operation being ordered is
-- outside Postgres.
--
-- So the claim is committed first and outlives the connection that made it.
-- A crash between claiming and clearing leaves a row, which is exactly the
-- state a later actor must not ignore:
--
--   * a live claim forbids the existing-object shortcut, because the
--     current version may vanish at any moment;
--   * writers never clear or take over another actor's claim. Intent is
--     not a fence, and the original delete may still be in flight;
--   * a writer may still proceed -- a fresh upload creates a NEW version
--     that a pending delete cannot name -- so no writer is ever stuck;
--   * the claim is completed by its owner, or by the reconciler at
--     `dataplane-up`, which re-issues the recorded deletes and clears the
--     row. Repeating a version-specific delete is harmless by
--     construction, which is what makes recovery safe to run at any time.
--
-- The fence is that every id recorded here is specific. A late arrival
-- removes something already condemned and nothing else.
BEGIN;

CREATE TABLE deletion_claims (
    deletion_claim_id uuid        PRIMARY KEY,
    organization_id   uuid        NOT NULL REFERENCES organizations (organization_id) ON DELETE RESTRICT,

    -- The digest whose storage is condemned. Same 64-hex shape as every
    -- other digest in this schema.
    --
    -- Deliberately NOT a foreign key to binary_attachments: a claim exists
    -- precisely when nothing references the digest any more, so a
    -- referential link would be to the row whose absence justifies it.
    object_digest     text        NOT NULL,

    -- Exactly what the sweep observed UNDER THE DIGEST LOCK, and the only
    -- storage it may remove.
    --
    -- Both are needed and neither implies the other: object versions and
    -- incomplete multipart uploads are separate storage states, invisible
    -- to each other's vocabulary, and a digest key can carry uploads with
    -- no version at all -- the residue of a promote that died before
    -- completing, which version enumeration would never discover.
    --
    -- Arrays rather than child tables. A claim is written once, read once
    -- and deleted; the only atomicity that matters is that the ids and the
    -- claim commit together, which one row gives without a second table.
    version_ids       text[]      NOT NULL,
    upload_ids        text[]      NOT NULL,

    claimed_at        timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT deletion_claims_digest_check
        CHECK (object_digest ~ '^[0-9a-f]{64}$'),

    -- A claim naming nothing condemns nothing, and would block the
    -- existing-object shortcut for as long as it survived.
    CONSTRAINT deletion_claims_names_something_check
        CHECK (cardinality(version_ids) + cardinality(upload_ids) > 0),

    -- Array columns admit NULL elements, and a NULL id is one nothing can
    -- delete by name -- the reconciler would skip it and clear the claim,
    -- reporting reclaimed storage that is still there.
    CONSTRAINT deletion_claims_ids_present_check
        CHECK (array_position(version_ids, NULL) IS NULL
           AND array_position(upload_ids, NULL) IS NULL),

    -- A blank id is worse than a missing one: a delete naming '' names the
    -- KEY rather than a version, which on a versioned bucket writes a
    -- delete marker and reclaims nothing, and an abort naming '' is the
    -- key-scoped abort this module refuses everywhere else.
    CONSTRAINT deletion_claims_ids_named_check
        CHECK (array_position(version_ids, '') IS NULL
           AND array_position(upload_ids, '') IS NULL),

    -- One live claim per digest. Clearing a claim DELETES the row, so the
    -- row's existence is what "live" means, and two sweeps cannot condemn
    -- one digest concurrently.
    CONSTRAINT deletion_claims_digest_unique UNIQUE (organization_id, object_digest)
);

-- No further index. The unique constraint serves the writer's and the
-- sweep's lookup by digest, and the reconciler enumerates every claim --
-- a table that is empty whenever nothing is mid-delete.

COMMIT;
