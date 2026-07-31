-- Configuration records and the secrets vault (item 7 design, D1 and D5).
--
-- Two families that share a lineage and share nothing else. Configuration is
-- unencrypted and resolves by scope alone; a secret carries an encryption
-- envelope and resolves by scope AND ownership.
--
-- Both resolve in ONE statement rather than by walking levels in Go. Three
-- reads can disagree under concurrent writes, and a caller that reconciles
-- them is a second copy of the precedence rule that no test covers.

-- --- configuration records -------------------------------------------

-- CreateConfigurationRecord writes one value at one scope.
--
-- The key is validated against the code-resident registry before this runs
-- (design D1): the schema enforces shape and identity, the seam enforces
-- which keys exist and what their values may be.
--
-- name: CreateConfigurationRecord :one
INSERT INTO configuration_records (
    configuration_record_id, organization_id, key, scope_type,
    scope_organization_id, scope_product_id, scope_repository_id, value
) VALUES (
    @configuration_record_id, @organization_id, @key, @scope_type,
    @scope_organization_id, @scope_product_id, @scope_repository_id, @value
)
RETURNING *;

-- ResolveConfigurationForRepository is the most-specific-wins read.
--
-- One statement, returning the value AND the level it came from. The level
-- is not a nicety: a caller that cannot tell which level answered cannot
-- explain why a value is what it is, which is the question a settings screen
-- exists to answer.
--
-- The lineage is derived from the repository's PRIMARY Product, which is
-- what makes it a chain rather than a graph (ADR 0018). Membership in
-- further Products via product_repositories is deliberately not consulted:
-- a repository in three Products would have three competing parents with no
-- defined precedence between them.
--
-- The CASE in the ORDER BY is the precedence rule itself, and it is here
-- rather than in Go so that "most specific wins" is one thing the database
-- does rather than an ordering the caller reimplements.
--
-- name: ResolveConfigurationForRepository :one
WITH lineage AS (
    SELECT r.repository_id, r.primary_product_id, r.organization_id
    FROM repositories r
    WHERE r.repository_id = @repository_id
      AND r.organization_id = @organization_id
)
SELECT c.*
FROM configuration_records c, lineage l
WHERE c.organization_id = @organization_id
  AND c.key = @key
  AND (
       (c.scope_type = 'repository'   AND c.scope_repository_id   = l.repository_id)
    OR (c.scope_type = 'product'      AND c.scope_product_id      = l.primary_product_id)
    OR (c.scope_type = 'organization' AND c.scope_organization_id = l.organization_id)
  )
ORDER BY CASE c.scope_type
             WHEN 'repository'   THEN 1
             WHEN 'product'      THEN 2
             WHEN 'organization' THEN 3
         END
LIMIT 1;

-- GetConfigurationRecord reads exactly one record by identity.
--
-- Exists so the seam can tell a version conflict from a missing row: both
-- make a conditional update affect zero rows, and a rowcount carries no
-- reason (item 5's lesson). The classification is made in Go against this
-- read, not guessed from the write.
--
-- name: GetConfigurationRecord :one
SELECT * FROM configuration_records
WHERE organization_id = @organization_id
  AND configuration_record_id = @configuration_record_id;

-- LockConfigurationRecord takes the row lock every mutation classifies under.
--
-- GetConfigurationRecord is not enough for that job. The seam runs inside a
-- READ COMMITTED transaction, where each statement sees a fresh snapshot, so
-- a plain SELECT tells you what was true at the instant it ran and nothing
-- about what is true when the UPDATE lands. Between the two, a concurrent
-- writer can bump the version or delete the row, and the classification the
-- seam already made becomes a statement about the past: a missing record
-- reported as a conflict, or a conflict reported as a missing record.
--
-- FOR UPDATE holds the row until the transaction ends, so the version the
-- seam classified against is the version the write applies to.
--
-- name: LockConfigurationRecord :one
SELECT * FROM configuration_records
WHERE organization_id         = @organization_id
  AND configuration_record_id = @configuration_record_id
FOR UPDATE;

-- UpdateConfigurationRecord replaces a value, conditional on the version the
-- caller read.
--
-- The version predicate survives the lock-and-classify above as a BACKSTOP,
-- not as the primary check: once the row is locked and its version compared
-- in Go, zero rows here means the two disagree, which is an invariant
-- failure rather than a conflict to report to the caller.
--
-- ADR 0027 names bare last-writer-wins on shared state as a defect, and a
-- configuration value is reachable from more than one agent lifecycle, which
-- is why the predicate is here at all rather than trusted to the caller.
--
-- name: UpdateConfigurationRecord :one
UPDATE configuration_records
SET value      = @value,
    version    = version + 1,
    updated_at = now()
WHERE organization_id         = @organization_id
  AND configuration_record_id = @configuration_record_id
  AND version                 = @expected_version
RETURNING *;

-- DeleteConfigurationRecord removes an override, restoring inheritance.
--
-- Deletion is how a value set at a repository goes back to following its
-- Product — without it an override could only ever be overwritten with a
-- value that matches the parent today and diverges silently tomorrow.
--
-- Conditional on the version for the same reason updates are: an operator
-- removing what they believe is stale must not erase a value somebody set a
-- moment earlier, and an unconditional delete reports success either way.
--
-- name: DeleteConfigurationRecord :execrows
DELETE FROM configuration_records
WHERE organization_id         = @organization_id
  AND configuration_record_id = @configuration_record_id
  AND version                 = @expected_version;

-- --- secrets vault ----------------------------------------------------

-- CreateSecret writes one sealed secret.
--
-- owner_user_id is NULL for a shared secret and the ACTING user for an
-- individual one. The seam never accepts it as an input (design D5): an
-- owner the caller supplies is an owner the caller can lie about, and the
-- partial unique index means a secret created as somebody else occupies
-- their slot.
--
-- The acting user's MEMBERSHIP is checked here too, and shared creation is
-- why. An individual secret is tenant-bound already, because owner_user_id
-- carries the composite foreign key to (user_id, organization_id) — but a
-- shared secret has a NULL owner, so nothing about the row mentions the
-- caller at all. Without this, a caller could create a shared secret in any
-- organization whose id it could name.
--
-- INSERT ... SELECT rather than VALUES, so the check is part of the write:
-- no rows inserted when the acting user is not a member, which the seam maps
-- to a typed refusal.
--
-- name: CreateSecret :one
INSERT INTO secrets (
    secret_id, organization_id, name, owner_user_id, scope_type,
    scope_organization_id, scope_product_id, scope_repository_id,
    scheme, nonce, ciphertext
)
SELECT
    @secret_id, @organization_id, @name, @owner_user_id, @scope_type,
    @scope_organization_id, @scope_product_id, @scope_repository_id,
    @scheme, @nonce, @ciphertext
WHERE EXISTS (
    SELECT 1 FROM users u
    WHERE u.user_id = @acting_user_id
      AND u.organization_id = @organization_id
)
RETURNING *;

-- ResolveSecretForRepository is the six-step ladder (design D5).
--
--   1 repository / the caller      4 product      / shared
--   2 repository / shared          5 organization / the caller
--   3 product    / the caller      6 organization / shared
--
-- Specificity is the OUTER sort and ownership breaks ties within a level.
-- The reason is what a credential is for: scope says which resource it works
-- against, and a credential for the wrong resource does not function no
-- matter whose it is, while a shared credential for the right one does.
-- Preferring ownership across levels would reach past a repository deploy key
-- for a personal organization-wide token with no access to that repository.
--
-- Attribution is preserved by RETURNING which row answered — the level and
-- the owner — rather than by preferring a credential that may not work.
--
-- The ownership filter is in the WHERE, not applied afterwards: no user ever
-- resolves another user's secret, and a filter the database applies is one no
-- caller can forget.
--
-- name: ResolveSecretForRepository :one
WITH lineage AS (
    SELECT r.repository_id, r.primary_product_id, r.organization_id
    FROM repositories r
    WHERE r.repository_id = @repository_id
      AND r.organization_id = @organization_id
)
SELECT s.*
FROM secrets s, lineage l
WHERE s.organization_id = @organization_id
  AND s.name = @name
  AND (s.owner_user_id = @acting_user_id OR s.owner_user_id IS NULL)
  AND EXISTS (
      SELECT 1 FROM users u
      WHERE u.user_id = @acting_user_id
        AND u.organization_id = @organization_id
  )
  AND (
       (s.scope_type = 'repository'   AND s.scope_repository_id   = l.repository_id)
    OR (s.scope_type = 'product'      AND s.scope_product_id      = l.primary_product_id)
    OR (s.scope_type = 'organization' AND s.scope_organization_id = l.organization_id)
  )
ORDER BY CASE s.scope_type
             WHEN 'repository'   THEN 1
             WHEN 'product'      THEN 2
             WHEN 'organization' THEN 3
         END,
         CASE WHEN s.owner_user_id IS NOT NULL THEN 1 ELSE 2 END
LIMIT 1;

-- GetSecret reads one secret by identity, under the acting user's own
-- ownership filter.
--
-- The filter is here as well as on resolution because this read feeds
-- replacement: the seam reads the current version, seals the next one for
-- version+1, and writes it back. A read that ignored ownership would let a
-- caller learn the version of a credential they cannot use.
--
-- name: GetSecret :one
SELECT s.* FROM secrets s
WHERE s.organization_id = @organization_id
  AND s.secret_id       = @secret_id
  AND (s.owner_user_id = @acting_user_id OR s.owner_user_id IS NULL)
  AND EXISTS (
      SELECT 1 FROM users u
      WHERE u.user_id = @acting_user_id
        AND u.organization_id = @organization_id
  )
;

-- ReplaceSecret rewrites the envelope, conditional on version AND ownership.
--
-- It bumps the version, which is part of the key derivation context — so the
-- caller must seal for version+1 before calling, and every stored ciphertext
-- ends up with its own key. That is what makes nonce reuse across
-- replacements structurally impossible rather than improbable.
--
-- The ownership predicate is on the WRITE, not only the read. Enforcing it on
-- reads alone gives an access model where one user cannot SEE another's
-- credential but can freely destroy it — and a caller who cannot read a
-- secret cannot tell what they destroyed.
--
-- Zero rows is deliberately ambiguous between "somebody else moved first"
-- and "not yours": a caller learns their write did not apply, not whether
-- another user's credential exists.
--
-- name: ReplaceSecret :one
UPDATE secrets s
SET scheme     = @scheme,
    nonce      = @nonce,
    ciphertext = @ciphertext,
    version    = s.version + 1,
    updated_at = now()
WHERE s.organization_id = @organization_id
  AND s.secret_id       = @secret_id
  AND s.version         = @expected_version
  AND (s.owner_user_id = @acting_user_id OR s.owner_user_id IS NULL)
  AND EXISTS (
      SELECT 1 FROM users u
      WHERE u.user_id = @acting_user_id
        AND u.organization_id = @organization_id
  )
RETURNING *;

-- DeleteSecret removes one secret, under the same two conditions.
--
-- A plain delete races a replacement: an operator removing what they believe
-- is a stale credential can erase a rotation committed a moment earlier, and
-- the delete reports success either way.
--
-- name: DeleteSecret :execrows
DELETE FROM secrets s
WHERE s.organization_id = @organization_id
  AND s.secret_id       = @secret_id
  AND s.version         = @expected_version
  AND (s.owner_user_id = @acting_user_id OR s.owner_user_id IS NULL)
  AND EXISTS (
      SELECT 1 FROM users u
      WHERE u.user_id = @acting_user_id
        AND u.organization_id = @organization_id
  )
;
