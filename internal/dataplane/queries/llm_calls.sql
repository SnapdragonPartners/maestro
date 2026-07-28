-- LLM call records (ADR 0022: LLM calls are metrics and traces).
--
-- Calls are created OPEN and completed exactly once. There is no generic
-- update: a structural test parses this file and allows only creation with
-- the open state written as literals, plus the single named completion
-- statement carrying `WHERE finished_at IS NULL`. Without that rule a later
-- generated update makes Audit history silently mutable.
--
-- Organization-scoped throughout, like every statement in the seam.

-- Creation writes the REQUEST side only. finished_at, succeeded,
-- error_message and the token counters are absent from the column list, so
-- a caller cannot create an already-completed row and skip the once-only
-- guard. cost_usd is likewise absent: a cost exists only once a call ends.
-- name: CreateLLMCall :one
INSERT INTO llm_calls (
    llm_call_id, organization_id, user_id, principal_instance_id,
    product_id, feature_id, epic_id, story_id,
    provider, model, started_at
) VALUES (
    @llm_call_id, @organization_id, @user_id, @principal_instance_id,
    @product_id, @feature_id, @epic_id, @story_id,
    @provider, @model, COALESCE(sqlc.narg('started_at')::timestamptz, now())
)
RETURNING *;

-- Lock before completing. Completion is once-only (design D1) and a
-- rowcount carries no reason, so the seam locks, classifies in Go, then
-- writes conditionally -- the shape every once-only operation here uses.
-- name: LockLLMCall :one
SELECT * FROM llm_calls
WHERE llm_call_id     = @llm_call_id
  AND organization_id = @organization_id
FOR UPDATE;

-- The one permitted update. `WHERE finished_at IS NULL` is the once-only
-- guard: two paths can observe one call ending -- a normal return and a
-- supervisor's error handler -- and the first outcome is the true one.
--
-- succeeded is NOT NULL here by construction: the schema's completion check
-- pairs finished_at with succeeded, so a completion that left it null would
-- be refused by the column rather than by this statement.
-- name: CompleteLLMCall :execrows
UPDATE llm_calls
SET finished_at      = COALESCE(sqlc.narg('finished_at')::timestamptz, now()),
    succeeded        = @succeeded,
    error_message    = @error_message,
    input_tokens     = @input_tokens,
    output_tokens    = @output_tokens,
    reasoning_tokens = @reasoning_tokens,
    cached_tokens    = @cached_tokens,
    cost_usd         = @cost_usd
WHERE llm_call_id     = @llm_call_id
  AND organization_id = @organization_id
  AND finished_at IS NULL;

-- name: GetLLMCall :one
SELECT * FROM llm_calls
WHERE llm_call_id     = @llm_call_id
  AND organization_id = @organization_id;

-- Reads are bounded (design D8): every list takes a limit and a keyset
-- cursor of (started_at, llm_call_id). Never OFFSET, which degrades exactly
-- as the table grows -- and this is the largest table in the system.
--
-- The cursor is exclusive and compared as a ROW VALUE, so the tie-breaker
-- is applied by the same comparison rather than by a second predicate the
-- planner treats separately.
--
-- The IS NULL guard is load-bearing, not defensive. On the FIRST page the
-- cursor is absent, and `(started_at, id) > (NULL, NULL)` evaluates to NULL
-- rather than true -- so without the guard every list returns an empty
-- first page and looks like an empty table. Verified against the server.

-- name: ListLLMCallsByStory :many
SELECT * FROM llm_calls
WHERE organization_id = @organization_id
  AND story_id        = @story_id
  AND (sqlc.narg('after_time')::timestamptz IS NULL
       OR (started_at, llm_call_id) > (sqlc.narg('after_time')::timestamptz, sqlc.narg('after_id')::uuid))
ORDER BY started_at, llm_call_id
LIMIT @row_limit;

-- name: ListLLMCallsByPrincipal :many
SELECT * FROM llm_calls
WHERE organization_id       = @organization_id
  AND principal_instance_id = @principal_instance_id
  AND (sqlc.narg('after_time')::timestamptz IS NULL
       OR (started_at, llm_call_id) > (sqlc.narg('after_time')::timestamptz, sqlc.narg('after_id')::uuid))
ORDER BY started_at, llm_call_id
LIMIT @row_limit;

-- name: ListLLMCallsInWindow :many
SELECT * FROM llm_calls
WHERE organization_id = @organization_id
  AND started_at     >= @window_start
  AND started_at      < @window_end
  AND (sqlc.narg('after_time')::timestamptz IS NULL
       OR (started_at, llm_call_id) > (sqlc.narg('after_time')::timestamptz, sqlc.narg('after_id')::uuid))
ORDER BY started_at, llm_call_id
LIMIT @row_limit;

-- Cost and token aggregate over one cohort in one window.
--
-- It reports COMPLETENESS, not just totals. SUM skips nulls, so a partial
-- total is indistinguishable from a complete one; and a call still open has
-- no cost YET, which is a different fact from a completed call whose cost
-- is not knowable (paired-local's local models). Three states, counted
-- separately.
--
-- The totals cover completed calls only. open_calls is reported beside them
-- so a campaign cannot under-report its own cost while still running and
-- never correct itself.
-- name: AggregateLLMCost :one
SELECT
    COALESCE(SUM(cost_usd)         FILTER (WHERE finished_at IS NOT NULL), 0)::numeric AS total_cost_usd,
    COALESCE(SUM(input_tokens)     FILTER (WHERE finished_at IS NOT NULL), 0)::bigint  AS total_input_tokens,
    COALESCE(SUM(output_tokens)    FILTER (WHERE finished_at IS NOT NULL), 0)::bigint  AS total_output_tokens,
    COALESCE(SUM(reasoning_tokens) FILTER (WHERE finished_at IS NOT NULL), 0)::bigint  AS total_reasoning_tokens,
    COALESCE(SUM(cached_tokens)    FILTER (WHERE finished_at IS NOT NULL), 0)::bigint  AS total_cached_tokens,
    count(*) FILTER (WHERE finished_at IS NOT NULL AND cost_usd IS NOT NULL)::bigint   AS measured_calls,
    count(*) FILTER (WHERE finished_at IS NOT NULL AND cost_usd IS NULL)::bigint       AS unmeasured_calls,
    count(*) FILTER (WHERE finished_at IS NULL)::bigint                                AS open_calls,
    count(*) FILTER (WHERE succeeded IS TRUE)::bigint                                  AS succeeded_calls,
    count(*) FILTER (WHERE succeeded IS FALSE)::bigint                                 AS failed_calls
FROM llm_calls
WHERE organization_id = @organization_id
  AND provider        = @provider
  AND model           = @model
  AND started_at     >= @window_start
  AND started_at      < @window_end;
