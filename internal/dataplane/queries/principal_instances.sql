-- Principal instances and their MPH seeding set (ADR 0021).

-- name: CreatePrincipalInstance :one
INSERT INTO principal_instances (
    principal_instance_id, organization_id, kind, model,
    agent_type, prompt_pack_id, prompt_hash, harness_config_hash,
    maestro_version, user_id,
    product_id, feature_id, epic_id, story_id,
    start_time
) VALUES (
    @principal_instance_id, @organization_id, @kind, @model,
    @agent_type, @prompt_pack_id, @prompt_hash, @harness_config_hash,
    @maestro_version, @user_id,
    @product_id, @feature_id, @epic_id, @story_id,
    COALESCE(sqlc.narg('start_time')::timestamptz, now())
)
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

-- name: ListPrincipalInstancesByPromptHash :many
SELECT * FROM principal_instances
WHERE organization_id = @organization_id
  AND prompt_hash = @prompt_hash
ORDER BY start_time DESC, principal_instance_id;

-- name: ListPrincipalInstancesByHarnessConfigHash :many
SELECT * FROM principal_instances
WHERE organization_id = @organization_id
  AND harness_config_hash = @harness_config_hash
ORDER BY start_time DESC, principal_instance_id;
