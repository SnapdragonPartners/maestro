-- Tool call records: ADR 0022's atomic Audit action unit.
--
-- Same lifecycle as llm_calls -- created open, completed once -- and the
-- same structural rule: creation with the open state literal, one named
-- completion carrying WHERE finished_at IS NULL, nothing else.

-- Provenance stays ONE atomic write. llm_call_id references
-- llm_calls_provenance_key, so a tool call may only claim an LLM call made
-- by the same principal for the same work; the seam maps that constraint's
-- violation to a generic ErrInvalidProvenance rather than reading the row
-- first, which would add a round trip on the hottest path and prove nothing
-- the key was not already proving.
-- name: CreateToolCall :one
INSERT INTO tool_calls (
    tool_call_id, organization_id, user_id, principal_instance_id,
    llm_call_id, product_id, feature_id, epic_id, story_id,
    tool_name, arguments, started_at
) VALUES (
    @tool_call_id, @organization_id, @user_id, @principal_instance_id,
    @llm_call_id, @product_id, @feature_id, @epic_id, @story_id,
    @tool_name, @arguments, COALESCE(sqlc.narg('started_at')::timestamptz, now())
)
RETURNING *;

-- name: LockToolCall :one
SELECT * FROM tool_calls
WHERE tool_call_id    = @tool_call_id
  AND organization_id = @organization_id
FOR UPDATE;

-- name: CompleteToolCall :execrows
UPDATE tool_calls
SET finished_at   = COALESCE(sqlc.narg('finished_at')::timestamptz, now()),
    succeeded     = @succeeded,
    result        = @result,
    error_message = @error_message
WHERE tool_call_id    = @tool_call_id
  AND organization_id = @organization_id
  AND finished_at IS NULL;

-- name: GetToolCall :one
SELECT * FROM tool_calls
WHERE tool_call_id    = @tool_call_id
  AND organization_id = @organization_id;

-- name: ListToolCallsByStory :many
SELECT * FROM tool_calls
WHERE organization_id = @organization_id
  AND story_id        = @story_id
  AND (sqlc.narg('after_time')::timestamptz IS NULL
       OR (started_at, tool_call_id) > (sqlc.narg('after_time')::timestamptz, sqlc.narg('after_id')::uuid))
ORDER BY started_at, tool_call_id
LIMIT @row_limit;

-- name: ListToolCallsByPrincipal :many
SELECT * FROM tool_calls
WHERE organization_id       = @organization_id
  AND principal_instance_id = @principal_instance_id
  AND (sqlc.narg('after_time')::timestamptz IS NULL
       OR (started_at, tool_call_id) > (sqlc.narg('after_time')::timestamptz, sqlc.narg('after_id')::uuid))
ORDER BY started_at, tool_call_id
LIMIT @row_limit;

-- name: ListToolCallsInWindow :many
SELECT * FROM tool_calls
WHERE organization_id = @organization_id
  AND started_at     >= @window_start
  AND started_at      < @window_end
  AND (sqlc.narg('after_time')::timestamptz IS NULL
       OR (started_at, tool_call_id) > (sqlc.narg('after_time')::timestamptz, sqlc.narg('after_id')::uuid))
ORDER BY started_at, tool_call_id
LIMIT @row_limit;
