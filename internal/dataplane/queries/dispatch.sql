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
    epic_version_artifact_id, epic_version_effective_digest, epic_version_effective_sequence,
    prompt_resolution_id
) VALUES (
    @story_dispatch_id, @organization_id, @product_id, @feature_id, @epic_id, @story_id, @work_group_id,
    'pending',
    @story_version_artifact_id, @story_version_effective_digest, @story_version_effective_sequence,
    @epic_version_artifact_id, @epic_version_effective_digest, @epic_version_effective_sequence,
    @prompt_resolution_id
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

-- The resolved configuration -- capability set, headless, acting user -- is
-- part of the INSERT (item 5 design, D12): migration 000024's anti-update
-- trigger leaves no other initialization path, which is the point.
-- name: InsertExecution :one
INSERT INTO executions (
    execution_id, organization_id, product_id, feature_id, epic_id, story_id, story_dispatch_id,
    capability_set, headless, acting_user_id
) VALUES (
    @execution_id, @organization_id, @product_id, @feature_id, @epic_id, @story_id, @story_dispatch_id,
    @capability_set, @headless, @acting_user_id
)
RETURNING *;

-- name: GetExecutionByDispatch :one
SELECT * FROM executions WHERE organization_id = $1 AND story_dispatch_id = $2;

-- name: GetExecution :one
SELECT * FROM executions WHERE organization_id = $1 AND execution_id = $2;

-- The resolution beside the dispatch (item 4 design, D8). Insertion is
-- parent-first: the dispatch names its resolution id first, under the
-- DEFERRED reciprocal key, and this row -- whose reference to the dispatch is
-- immediate -- follows in the same transaction.
-- name: InsertDispatchPromptResolution :one
INSERT INTO dispatch_prompt_resolutions (
    resolution_id, story_dispatch_id, organization_id, product_id, feature_id, epic_id, story_id,
    resolved_name, scheme, digest, content_id, installation_id, installation_revision,
    metadata_snapshot, validated_maestro_version, range_check
) VALUES (
    @resolution_id, @story_dispatch_id, @organization_id, @product_id, @feature_id, @epic_id, @story_id,
    @resolved_name, @scheme, @digest, @content_id, @installation_id, @installation_revision,
    @metadata_snapshot, @validated_maestro_version, @range_check
)
RETURNING *;

-- name: GetDispatchPromptResolution :one
SELECT * FROM dispatch_prompt_resolutions
WHERE organization_id   = @organization_id
  AND story_dispatch_id = @story_dispatch_id;

-- ---------------------------------------------------------------------------
-- The execution's boundary verbs (item 5 design, D9-D11). Each is a named
-- conditional transition on the row, taken under its lock.
-- ---------------------------------------------------------------------------

-- Registration holds the row FOR SHARE (D9): many attempts may register at
-- once, and closure -- FOR UPDATE -- waits for every one of them.
-- name: LockExecutionShared :one
SELECT * FROM executions
WHERE organization_id = @organization_id AND execution_id = @execution_id
FOR SHARE;

-- name: LockExecution :one
SELECT * FROM executions
WHERE organization_id = @organization_id AND execution_id = @execution_id
FOR UPDATE;

-- Idempotent: closing twice is the headless path followed by Terminate's
-- own closure (D7, D11), and zero rows is "already closed", not a refusal.
-- name: CloseExecutionAdmission :execrows
UPDATE executions
SET admission_closed_at = now()
WHERE organization_id = @organization_id
  AND execution_id    = @execution_id
  AND admission_closed_at IS NULL;

-- Supersession marks authority AND closes admission in one statement, which
-- is what executions_superseded_closes_admission_check requires (D10).
-- name: SupersedeExecution :execrows
UPDATE executions
SET authority_state     = 'superseded',
    admission_closed_at = COALESCE(admission_closed_at, now())
WHERE organization_id = @organization_id
  AND execution_id    = @execution_id
  AND authority_state = 'current';

-- The terminal result, at most once and only after closure (D11). The
-- schema refuses both as well; the predicates make the refusal a zero row
-- count the seam classifies rather than a constraint error it decodes.
-- name: RecordExecutionTerminalResult :execrows
UPDATE executions
SET status                 = @status,
    completion_disposition = @completion_disposition,
    cancellation_reason    = @cancellation_reason,
    failure_class          = @failure_class,
    blocked_tool_call_id   = @blocked_tool_call_id,
    error_message          = @error_message,
    terminated_at          = now()
WHERE organization_id = @organization_id
  AND execution_id    = @execution_id
  AND status IS NULL
  AND admission_closed_at IS NOT NULL;
