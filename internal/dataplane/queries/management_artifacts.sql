-- Management artifacts (ADR 0021 lifecycle, ADR 0028 envelope encoding).
--
-- There is deliberately NO generic status update. Every status write below
-- is a named transition carrying its own preconditions, and a test parses
-- this file to fail the build if an un-named one appears (design D4).
--
-- Each transition's WHERE repeats the preconditions the seam has already
-- checked against the locked row. That redundancy is intentional: it is the
-- backstop that stops a classification bug from writing a transition the
-- rules forbid. Zero rows affected there is an internal invariant failure,
-- not a user-facing outcome.

-- name: CreateManagementArtifact :one
INSERT INTO management_artifacts (
    artifact_id, organization_id, user_id,
    artifact_type, artifact_category, status, scope_type,
    scope_organization_id, scope_product_id, scope_feature_id,
    scope_epic_id, scope_story_id,
    product_id, feature_id, epic_id, story_id,
    author_instance_id, produced_by_tool_call_id,
    amends_artifact_id, supersedes_artifact_id, replaces_artifact_id,
    schema_version, summary, payload, payload_digest, review_digest
) VALUES (
    @artifact_id, @organization_id, @user_id,
    @artifact_type, @artifact_category, 'draft', @scope_type,
    @scope_organization_id, @scope_product_id, @scope_feature_id,
    @scope_epic_id, @scope_story_id,
    @product_id, @feature_id, @epic_id, @story_id,
    @author_instance_id, @produced_by_tool_call_id,
    @amends_artifact_id, @supersedes_artifact_id, @replaces_artifact_id,
    @schema_version, @summary, @payload, @payload_digest, @review_digest
)
RETURNING *;

-- name: GetManagementArtifact :one
SELECT * FROM management_artifacts
WHERE artifact_id = @artifact_id;

-- Lock before any transition. A rowcount carries no reason, so the seam
-- locks the row, classifies the failure in Go against what it read, and
-- only then writes conditionally (design D5).
-- name: LockManagementArtifact :one
SELECT * FROM management_artifacts
WHERE artifact_id = @artifact_id
FOR UPDATE;

-- name: ListManagementArtifactsByScope :many
SELECT * FROM management_artifacts
WHERE organization_id = @organization_id
  AND scope_type = @scope_type
  AND scope_id = @scope_id
ORDER BY created_at, artifact_id;

-- Lineage reads. Each takes the denormalised column rather than walking the
-- hierarchy, which is what the denormalisation is for.

-- name: ListManagementArtifactsByStory :many
SELECT * FROM management_artifacts
WHERE story_id = @story_id
ORDER BY created_at, artifact_id;

-- name: ListManagementArtifactsByEpic :many
SELECT * FROM management_artifacts
WHERE epic_id = @epic_id
ORDER BY created_at, artifact_id;

-- name: ListManagementArtifactsByProduct :many
SELECT * FROM management_artifacts
WHERE product_id = @product_id
ORDER BY created_at, artifact_id;

-- Effective-view assembly (design D8): the original plus its ACCEPTED
-- amendments in sequence order. Draft and rejected amendments are never
-- applied, which is why this filters on status rather than assembling
-- everything and letting the caller choose.
-- name: ListAcceptedAmendments :many
SELECT * FROM management_artifacts
WHERE amends_artifact_id = @amends_artifact_id
  AND status = 'accepted'
ORDER BY amendment_sequence;

-- The next amendment sequence is one more than the maximum over every
-- non-null HISTORICAL sequence, whatever the amendment's current status
-- (design D6 step 5). D5 forbids superseding or archiving an amendment, so
-- accepted is the only status carrying a sequence today and this is
-- currently equivalent to filtering on it -- written as the historical
-- maximum so it stays correct if that matrix ever widens, rather than
-- silently reusing a number.
-- name: MaxAmendmentSequence :one
SELECT COALESCE(MAX(amendment_sequence), 0)::int AS max_sequence
FROM management_artifacts
WHERE amends_artifact_id = @amends_artifact_id;

-- Transitions.

-- Accept an ORIGINAL. The review_digest condition is what makes ADR 0028's
-- binding real: a review of superseded content cannot license acceptance,
-- because the row's current review_digest no longer equals the reviewed one.
-- name: AcceptManagementArtifact :execrows
UPDATE management_artifacts
SET status               = 'accepted',
    reviewer_instance_id = @reviewer_instance_id,
    accepted_at          = now()
WHERE artifact_id   = @artifact_id
  AND status        = 'draft'
  AND is_amendment  = false
  AND review_digest = @review_digest;

-- Accept an AMENDMENT, assigning its sequence in the same statement. The
-- sequence is assigned on acceptance and retained thereafter: without a
-- stored total order the effective view is undefined.
-- name: AcceptManagementAmendment :execrows
UPDATE management_artifacts
SET status               = 'accepted',
    reviewer_instance_id = @reviewer_instance_id,
    accepted_at          = now(),
    amendment_sequence   = @amendment_sequence
WHERE artifact_id   = @artifact_id
  AND status        = 'draft'
  AND is_amendment  = true
  AND review_digest = @review_digest;

-- Invalidation is pre-acceptance by definition (ADR 0021), so draft is the
-- only source status and there are no further preconditions.
-- name: InvalidateManagementArtifact :execrows
UPDATE management_artifacts
SET status = 'invalidated'
WHERE artifact_id = @artifact_id
  AND status      = 'draft';

-- Amendments can be neither superseded nor archived, hence is_amendment =
-- false on both. Effective-view assembly loads only accepted amendments, so
-- archiving one would silently drop its contribution from the effective
-- view of an artifact nobody re-reviewed -- mutating accepted content
-- through a lifecycle side door.

-- name: SupersedeManagementArtifact :execrows
UPDATE management_artifacts
SET status = 'superseded'
WHERE artifact_id  = @artifact_id
  AND status       = 'accepted'
  AND is_amendment = false;

-- name: ArchiveManagementArtifact :execrows
UPDATE management_artifacts
SET status = 'archived'
WHERE artifact_id  = @artifact_id
  AND status       IN ('accepted', 'superseded')
  AND is_amendment = false;
