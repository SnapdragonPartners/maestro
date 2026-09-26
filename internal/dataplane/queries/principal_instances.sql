-- Principal instances and their MPH seeding set (ADR 0021).

-- Creation covers both a lifetime that STARTS NOW and one that ALREADY RAN.
--
-- stop_time and stop_reason are settable here, rather than only through
-- StopPrincipalInstance, because an instance reconstructed from a record of
-- something that already finished has no open phase to represent. Written as
-- a create-then-stop pair it would exist open for the width of a statement,
-- and a reader inside that window sees a live agent that stopped before the
-- import began. The schema's stop check -- stop_time and stop_reason null
-- together -- means a half-supplied pair is refused by the database as well
-- as by the seam.
--
-- Three writers, one per shape of the prompt-pack columns, and the ORIGIN is
-- a literal in each statement rather than a parameter of any (item 4 design,
-- D5): a discriminator a caller can set is one a caller can set wrong. The
-- schema's shape constraint refuses a row that names a shape its writer does
-- not produce, so each statement below can only ever write its own.

-- The general path: humans and system principals. No agent_type and no pack
-- columns, so an agent cannot be written here even by a seam that forgot to
-- refuse it -- the shape constraint requires all four identity columns on an
-- agent row and this statement supplies none.
-- name: CreatePrincipalInstance :one
INSERT INTO principal_instances (
    principal_instance_id, organization_id, kind, model,
    harness_config_hash, maestro_version, user_id,
    product_id, feature_id, epic_id, story_id,
    start_time, stop_time, stop_reason
) VALUES (
    @principal_instance_id, @organization_id, @kind, @model,
    @harness_config_hash, @maestro_version, @user_id,
    @product_id, @feature_id, @epic_id, @story_id,
    COALESCE(sqlc.narg('start_time')::timestamptz, now()),
    sqlc.narg('stop_time')::timestamptz,
    sqlc.narg('stop_reason')
)
RETURNING *;

-- The import path: an agent that ran outside the plane. Its lifetime is
-- already over, so start, stop and reason are all required here, and its
-- pack is a name and a legacy-scheme digest with no plane-owned reference.
-- The scheme is a literal like the origin: the one legacy scheme is the
-- only one a foreign row may carry, and a caller is not asked to say so.
-- name: RecordForeignAgentPrincipal :one
INSERT INTO principal_instances (
    principal_instance_id, organization_id, kind, model, agent_type,
    prompt_pack_origin, prompt_pack_name, prompt_pack_scheme, prompt_hash,
    harness_config_hash, maestro_version,
    product_id, feature_id, epic_id, story_id,
    start_time, stop_time, stop_reason
) VALUES (
    @principal_instance_id, @organization_id, 'agent', @model, @agent_type,
    'foreign', @prompt_pack_name, 'v1-manifest-sha256', @prompt_hash,
    @harness_config_hash, @maestro_version,
    @product_id, @feature_id, @epic_id, @story_id,
    @start_time, @stop_time, @stop_reason
)
RETURNING *;

-- The live path: an agent starting under an execution. The pack columns and
-- the lineage are SELECTED from the execution and its dispatch's persisted
-- resolution, never supplied -- the copy is a fact of this statement, so no
-- caller and no seam code holds a pack field it could substitute. Name,
-- revision and snapshot come from the RESOLUTION row and never from the
-- installation: they record what the installation said when the dispatch was
-- decided, and after a later installation update they legitimately differ
-- from it (design D5).
--
-- Zero rows means the execution is absent from the organization, or has no
-- resolution; the seam reads the execution first so it can tell which.
-- name: CreateDispatchedPrincipalInstance :one
INSERT INTO principal_instances (
    principal_instance_id, organization_id, kind, model, agent_type,
    prompt_pack_origin, prompt_pack_name, prompt_pack_scheme, prompt_hash,
    prompt_pack_content_id, prompt_pack_installation_id,
    prompt_pack_installation_revision, prompt_pack_metadata_snapshot,
    harness_config_hash, maestro_version,
    product_id, feature_id, epic_id, story_id
)
SELECT @principal_instance_id, e.organization_id, 'agent', @model, @agent_type,
       'resolved', r.resolved_name, r.scheme, r.digest,
       r.content_id, r.installation_id,
       r.installation_revision, r.metadata_snapshot,
       @harness_config_hash, @maestro_version,
       e.product_id, e.feature_id, e.epic_id, e.story_id
  FROM executions e
  JOIN dispatch_prompt_resolutions r
    ON r.story_dispatch_id = e.story_dispatch_id
   AND r.organization_id   = e.organization_id
 WHERE e.execution_id    = @execution_id
   AND e.organization_id = @organization_id
RETURNING *;

-- name: GetPrincipalInstance :one
SELECT * FROM principal_instances
WHERE principal_instance_id = @principal_instance_id
  AND organization_id       = @organization_id;

-- Lock before stopping. Stopping is once-only (design D7) and a rowcount
-- carries no reason, so the seam locks, classifies in Go, then writes
-- conditionally -- the same shape as the artifact transitions.
--
-- The lock is what makes the race safe. Two paths finalise one agent
-- lifecycle about a millisecond apart (ADR 0027 P-6), and a read-committed
-- statement that did not take the lock would still see the pre-stop
-- snapshot after the winner committed, reporting a null stop time for an
-- instance that has one.
-- name: LockPrincipalInstance :one
SELECT * FROM principal_instances
WHERE principal_instance_id = @principal_instance_id
  AND organization_id       = @organization_id
FOR UPDATE;

-- name: StopPrincipalInstance :execrows
UPDATE principal_instances
SET stop_time   = COALESCE(sqlc.narg('stop_time')::timestamptz, now()),
    stop_reason = @stop_reason
WHERE principal_instance_id = @principal_instance_id
  AND organization_id       = @organization_id
  AND stop_time IS NULL;

-- name: AddPrincipalInstanceInput :one
INSERT INTO principal_instance_inputs (
    principal_instance_id, artifact_id, organization_id, seeded_digest
) VALUES (
    @principal_instance_id, @artifact_id, @organization_id, @seeded_digest
)
RETURNING *;

-- name: ListPrincipalInstanceInputs :many
SELECT * FROM principal_instance_inputs
WHERE principal_instance_id = @principal_instance_id
  AND organization_id       = @organization_id
ORDER BY seeded_at, artifact_id;

-- The MPH reads. ADR 0021 says cost and comparison analysis anchors on
-- these three axes, so each is a query rather than a filter applied after
-- fetching everything.

-- name: ListPrincipalInstancesByModel :many
SELECT * FROM principal_instances
WHERE organization_id = @organization_id
  AND model = @model
ORDER BY start_time DESC, principal_instance_id;

-- The P axis filters on the SCHEME and the digest, never the digest alone:
-- a v1-manifest identity and a pack identity that share their hex are
-- unrelated (ADR 0031 section 1; design D4). The supporting index is
-- principal_instances_prompt_identity_idx.
-- name: ListPrincipalInstancesByPromptIdentity :many
SELECT * FROM principal_instances
WHERE organization_id    = @organization_id
  AND prompt_pack_scheme = @prompt_pack_scheme
  AND prompt_hash        = @prompt_hash
ORDER BY start_time DESC, principal_instance_id;

-- name: ListPrincipalInstancesByHarnessConfigHash :many
SELECT * FROM principal_instances
WHERE organization_id = @organization_id
  AND harness_config_hash = @harness_config_hash
ORDER BY start_time DESC, principal_instance_id;
