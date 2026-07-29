-- Audit truncation (item 5 design D6).
--
-- The one destructive operation here, and the one place this phase can
-- destroy what it promised to keep. Every statement is ORGANIZATION-SCOPED:
-- a pin or a reference in one organization can neither protect nor fail to
-- protect a row in another.
--
-- Callers run these in DEPENDENCY ORDER inside one REPEATABLE READ
-- transaction -- audit events, metric events, audit artifacts, tool calls,
-- LLM calls -- because artifacts reference tool calls and tool calls
-- reference LLM calls, all ON DELETE RESTRICT.
--
-- RESTRICT ERRORS rather than skipping: a referenced row does not quietly
-- survive a DELETE, it aborts the statement and takes the whole batch with
-- it. So every referenced row is excluded in the WHERE, never discovered at
-- commit. That is the single most important thing about this file.
--
-- Retention reasons are assigned by PRECEDENCE -- pinned, then open, then
-- referenced -- so a row counted once is not counted twice and
-- `candidates = deleted + pinned + open + referenced` reconciles exactly.
-- Open outranks referenced deliberately: a call open long past the horizon
-- is an operational problem, and being referenced does not make it less so.

-- The isolation level of the transaction this pass is running in.
--
-- Truncation's retention guards are only sound evaluated against ONE
-- snapshot, and READ COMMITTED gives every STATEMENT a fresh one -- so a
-- pass run at the default would evaluate its five guards against five
-- instants and could delete a row that was protected when it began. The
-- seam therefore asks the server rather than trusting that whoever opened
-- the transaction knew, since a pass reached through a caller's own
-- transaction inherits that caller's isolation and nothing else would say
-- so.
-- name: CurrentIsolationLevel :one
SELECT current_setting('transaction_isolation')::text;

-- --- audit events: no dependents, no retention -------------------------

-- name: CountAuditEventCandidates :one
SELECT count(*)::bigint FROM audit_events
WHERE organization_id = @organization_id
  AND occurred_at     < @before;

-- name: TruncateAuditEvents :execrows
DELETE FROM audit_events
WHERE organization_id = @organization_id
  AND occurred_at     < @before;

-- --- metric events: no dependents, no retention ------------------------

-- name: CountMetricEventCandidates :one
SELECT count(*)::bigint FROM metric_events
WHERE organization_id = @organization_id
  AND recorded_at     < @before;

-- name: TruncateMetricEvents :execrows
DELETE FROM metric_events
WHERE organization_id = @organization_id
  AND recorded_at     < @before;

-- --- audit artifacts: pinnable -----------------------------------------
--
-- The only pinnable family in item 5's scope. Binary attachments are also
-- pinnable but belong to item 6, where deleting a row whose bytes live in
-- object storage is that item's commit-order problem.
--
-- retention_pins references audit_artifacts ON DELETE RESTRICT, so a pinned
-- artifact is protected by the foreign key as well as by this predicate.
-- The predicate is what turns that protection from an aborted batch into a
-- counted, reported outcome.

-- name: CountAuditArtifactTruncation :one
SELECT
    count(*)::bigint                                        AS candidates,
    count(*) FILTER (WHERE EXISTS (
        SELECT 1 FROM retention_pins p
        WHERE p.pinned_audit_artifact_id = a.artifact_id
          AND p.organization_id          = a.organization_id))::bigint AS retained_pinned
FROM audit_artifacts a
WHERE a.organization_id = @organization_id
  AND a.created_at      < @before;

-- name: TruncateAuditArtifacts :execrows
DELETE FROM audit_artifacts a
WHERE a.organization_id = @organization_id
  AND a.created_at      < @before
  AND NOT EXISTS (
      SELECT 1 FROM retention_pins p
      WHERE p.pinned_audit_artifact_id = a.artifact_id
        AND p.organization_id          = a.organization_id);

-- --- binary attachments: pinned rows are retained ----------------------
--
-- Deliberately left out of item 5's pass, because deleting a row whose
-- bytes live in object storage is item 6's problem (design D6a).
--
-- Deleting the row does NOT delete the object. It makes the object
-- unreachable -- the sweep's reachable set is exactly these rows -- and the
-- sweep reclaims it afterwards under the digest lock. The two steps are
-- separate on purpose: this pass runs under one REPEATABLE READ snapshot,
-- and object deletion cannot participate in a snapshot.
--
-- Pinned rows are excluded in the WHERE and never discovered at commit.
-- retention_pins references attachments ON DELETE RESTRICT, which ABORTS
-- the statement rather than skipping the row, and an aborted DELETE takes
-- the whole pass with it.

-- name: CountAttachmentTruncation :one
SELECT
    count(*)::bigint                                        AS candidates,
    count(*) FILTER (WHERE EXISTS (
        SELECT 1 FROM retention_pins p
        WHERE p.pinned_attachment_id = b.attachment_id
          AND p.organization_id      = b.organization_id))::bigint AS retained_pinned
FROM binary_attachments b
WHERE b.organization_id = @organization_id
  AND b.created_at      < @before;

-- name: TruncateAttachments :execrows
DELETE FROM binary_attachments b
WHERE b.organization_id = @organization_id
  AND b.created_at      < @before
  AND NOT EXISTS (
      SELECT 1 FROM retention_pins p
      WHERE p.pinned_attachment_id = b.attachment_id
        AND p.organization_id      = b.organization_id);

-- --- tool calls: open, or referenced by an artifact ---------------------
--
-- Completed calls age from finished_at, NOT started_at: ageing from the
-- start deletes a long-running call the instant it finishes, and the slow
-- calls are the ones worth keeping.
--
-- management_artifacts is never truncated -- it is durable by definition --
-- so a tool call cited by one is retained as referenced permanently. That
-- is correct: it is provenance for reviewable work product.

-- name: CountToolCallTruncation :one
SELECT
    count(*)::bigint AS candidates,
    count(*) FILTER (WHERE t.finished_at IS NULL)::bigint AS retained_open,
    count(*) FILTER (WHERE t.finished_at IS NOT NULL AND (
        EXISTS (SELECT 1 FROM management_artifacts m
                WHERE m.produced_by_tool_call_id = t.tool_call_id
                  AND m.organization_id          = t.organization_id)
     OR EXISTS (SELECT 1 FROM audit_artifacts u
                WHERE u.produced_by_tool_call_id = t.tool_call_id
                  AND u.organization_id          = t.organization_id)))::bigint AS retained_referenced
FROM tool_calls t
WHERE t.organization_id = @organization_id
  AND ((t.finished_at IS NOT NULL AND t.finished_at < @before)
    OR (t.finished_at IS NULL     AND t.started_at  < @before));

-- name: TruncateToolCalls :execrows
DELETE FROM tool_calls t
WHERE t.organization_id = @organization_id
  AND t.finished_at IS NOT NULL
  AND t.finished_at     < @before
  AND NOT EXISTS (
      SELECT 1 FROM management_artifacts m
      WHERE m.produced_by_tool_call_id = t.tool_call_id
        AND m.organization_id          = t.organization_id)
  AND NOT EXISTS (
      SELECT 1 FROM audit_artifacts u
      WHERE u.produced_by_tool_call_id = t.tool_call_id
        AND u.organization_id          = t.organization_id);

-- --- LLM calls: open, or referenced by a surviving tool call ------------

-- name: CountLLMCallTruncation :one
SELECT
    count(*)::bigint AS candidates,
    count(*) FILTER (WHERE l.finished_at IS NULL)::bigint AS retained_open,
    count(*) FILTER (WHERE l.finished_at IS NOT NULL AND EXISTS (
        SELECT 1 FROM tool_calls t
        WHERE t.llm_call_id     = l.llm_call_id
          AND t.organization_id = l.organization_id))::bigint AS retained_referenced
FROM llm_calls l
WHERE l.organization_id = @organization_id
  AND ((l.finished_at IS NOT NULL AND l.finished_at < @before)
    OR (l.finished_at IS NULL     AND l.started_at  < @before));

-- name: TruncateLLMCalls :execrows
DELETE FROM llm_calls l
WHERE l.organization_id = @organization_id
  AND l.finished_at IS NOT NULL
  AND l.finished_at     < @before
  AND NOT EXISTS (
      SELECT 1 FROM tool_calls t
      WHERE t.llm_call_id     = l.llm_call_id
        AND t.organization_id = l.organization_id);
