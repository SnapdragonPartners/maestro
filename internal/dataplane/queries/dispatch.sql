-- The dispatch family (design D10): governing pointers, dispatch creation
-- with its basis, the named conditional transitions, and executions.
--
-- The three disposition transitions are the ONLY statements that write
-- `disposition`, and each names its destination as a literal and guards on
-- `disposition = 'pending'`. Zero rows affected is a rejected transition,
-- reported by the seam as a typed reason. There is no generic setter, on
-- purpose: a generic setter is what makes terminal immutability
-- unenforceable.

-- name: SetStoryGoverningArtifact :execrows
UPDATE stories
SET governing_artifact_id = @artifact_id, governing_is_amendment = false
WHERE organization_id = @organization_id AND story_id = @story_id;

-- name: SetEpicGoverningArtifact :execrows
UPDATE epics
SET governing_artifact_id = @artifact_id, governing_is_amendment = false
WHERE organization_id = @organization_id AND epic_id = @epic_id;

-- name: InsertStoryDispatch :one
INSERT INTO story_dispatches (
    story_dispatch_id, organization_id, product_id, feature_id, epic_id, story_id, work_group_id,
    disposition,
    story_version_artifact_id, story_version_effective_digest, story_version_effective_sequence,
    epic_version_artifact_id, epic_version_effective_digest, epic_version_effective_sequence
) VALUES (
    @story_dispatch_id, @organization_id, @product_id, @feature_id, @epic_id, @story_id, @work_group_id,
    'pending',
    @story_version_artifact_id, @story_version_effective_digest, @story_version_effective_sequence,
    @epic_version_artifact_id, @epic_version_effective_digest, @epic_version_effective_sequence
)
RETURNING *;

-- name: InsertDispatchBasisDependency :exec
INSERT INTO dispatch_basis_dependencies (
    story_dispatch_id, organization_id, product_id, feature_id, epic_id, predecessor_story_id,
    completion_artifact_id, completion_effective_digest, completion_effective_sequence
) VALUES (
    @story_dispatch_id, @organization_id, @product_id, @feature_id, @epic_id, @predecessor_story_id,
    @completion_artifact_id, @completion_effective_digest, @completion_effective_sequence
);

-- name: GetStoryDispatch :one
SELECT * FROM story_dispatches WHERE organization_id = $1 AND story_dispatch_id = $2;

-- name: ListDispatchBasisDependencies :many
SELECT predecessor_story_id, completion_artifact_id, completion_effective_digest, completion_effective_sequence
FROM dispatch_basis_dependencies
WHERE story_dispatch_id = $1 AND organization_id = $2
ORDER BY predecessor_story_id;

-- name: ListStoryDispatchesByDisposition :many
SELECT * FROM story_dispatches
WHERE organization_id = $1 AND disposition = $2
ORDER BY story_dispatch_id;

-- name: AcceptStoryDispatch :execrows
UPDATE story_dispatches
SET disposition = 'accepted', settled_at = now()
WHERE organization_id = @organization_id AND story_dispatch_id = @story_dispatch_id
  AND disposition = 'pending';

-- name: FailStoryDispatch :execrows
UPDATE story_dispatches
SET disposition = 'failed', settled_at = now(), failure_code = @failure_code, failure_detail = @failure_detail
WHERE organization_id = @organization_id AND story_dispatch_id = @story_dispatch_id
  AND disposition = 'pending';

-- name: InvalidateStoryDispatch :execrows
UPDATE story_dispatches
SET disposition = 'invalidated', settled_at = now()
WHERE organization_id = @organization_id AND story_dispatch_id = @story_dispatch_id
  AND disposition = 'pending';

-- name: InsertExecution :one
INSERT INTO executions (
    execution_id, organization_id, product_id, feature_id, epic_id, story_id, story_dispatch_id
) VALUES (
    @execution_id, @organization_id, @product_id, @feature_id, @epic_id, @story_id, @story_dispatch_id
)
RETURNING *;

-- name: GetExecutionByDispatch :one
SELECT * FROM executions WHERE organization_id = $1 AND story_dispatch_id = $2;
