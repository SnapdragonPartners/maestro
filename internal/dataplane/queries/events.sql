-- Metric events and audit events: born final.
--
-- No lifecycle, so no completion and no updates at all -- the asymmetry
-- with the call tables is the point. A row here is written once and only
-- ever read or truncated.
--
-- audit_events carries NO work lineage: no product, feature, epic or story
-- column exists, so it cannot be listed by Story. Offering that would mean
-- inventing it, and joining through the principal answers a different
-- question ("events by the agent that also worked on this Story") that must
-- not be presented as the same one.

-- name: CreateMetricEvent :one
INSERT INTO metric_events (
    metric_event_id, organization_id, user_id, principal_instance_id,
    product_id, feature_id, epic_id, story_id,
    metric_name, labels, value, recorded_at
) VALUES (
    @metric_event_id, @organization_id, @user_id, @principal_instance_id,
    @product_id, @feature_id, @epic_id, @story_id,
    @metric_name, @labels, @value, COALESCE(sqlc.narg('recorded_at')::timestamptz, now())
)
RETURNING *;

-- name: ListMetricEventsInWindow :many
SELECT * FROM metric_events
WHERE organization_id = @organization_id
  AND recorded_at    >= @window_start
  AND recorded_at     < @window_end
  AND (sqlc.narg('after_time')::timestamptz IS NULL
       OR (recorded_at, metric_event_id) > (sqlc.narg('after_time')::timestamptz, sqlc.narg('after_id')::uuid))
ORDER BY recorded_at, metric_event_id
LIMIT @row_limit;

-- name: CreateAuditEvent :one
INSERT INTO audit_events (
    audit_event_id, organization_id, user_id, principal_instance_id,
    event_type, detail, occurred_at
) VALUES (
    @audit_event_id, @organization_id, @user_id, @principal_instance_id,
    @event_type, @detail, COALESCE(sqlc.narg('occurred_at')::timestamptz, now())
)
RETURNING *;

-- name: ListAuditEventsInWindow :many
SELECT * FROM audit_events
WHERE organization_id = @organization_id
  AND occurred_at    >= @window_start
  AND occurred_at     < @window_end
  AND (sqlc.narg('after_time')::timestamptz IS NULL
       OR (occurred_at, audit_event_id) > (sqlc.narg('after_time')::timestamptz, sqlc.narg('after_id')::uuid))
ORDER BY occurred_at, audit_event_id
LIMIT @row_limit;
