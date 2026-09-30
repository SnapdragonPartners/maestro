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

-- Locks and returns the transaction timestamp, for the reason LockLLMCall
-- documents: the seam materialises the completion default so the instant it
-- validates is the instant it stores.
-- name: LockToolCall :one
SELECT sqlc.embed(tool_calls), now()::timestamptz AS locked_at
FROM tool_calls
WHERE tool_call_id    = @tool_call_id
  AND organization_id = @organization_id
FOR UPDATE;

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

-- ===========================================================================
-- The mediated attempt (Phase 3 item 5 design, D4-D12). Every statement below
-- is a NAMED transition bound to one state it moves from, enforced by the
-- structure test beside these files: there is no generic update on an
-- attempt, for the same reason there is none on an artifact's status.
-- ===========================================================================

-- Registration (D5, D9): the row is inserted OPEN with its claim, family,
-- digests and keys, inside a transaction that holds the execution row FOR
-- SHARE and has checked admission is open -- the seam does both around this
-- statement. ON CONFLICT DO NOTHING followed by a read is what makes a
-- transport retry with the same id a classification rather than a second
-- attempt; a zero row count says the id was taken. The lineage and the
-- accountable user are the execution's, copied by the seam from the row it
-- locked, never supplied.
-- name: RegisterToolCall :execrows
INSERT INTO tool_calls (
    tool_call_id, organization_id, user_id, principal_instance_id, llm_call_id,
    product_id, feature_id, epic_id, story_id,
    tool_name, arguments,
    execution_id, family, request_digest, arguments_digest, caller_ref,
    target_key, mutation_key, claimed_by
) VALUES (
    @tool_call_id, @organization_id, @user_id, @principal_instance_id, @llm_call_id,
    @product_id, @feature_id, @epic_id, @story_id,
    @tool_name, @arguments,
    @execution_id, @family, @request_digest, @arguments_digest, @caller_ref,
    @target_key, @mutation_key, @claimed_by
)
ON CONFLICT (tool_call_id) DO NOTHING;

-- A denial is opened and completed together (ADR 0030 section 8; D4): one
-- insert of a row already settled, so the record exists after admission has
-- closed -- which is exactly when a request refused for closed admission
-- arrives. Not registration: no execution lock, no admission check, no claim.
-- The outcome and disposition are LITERALS, which the structure test holds.
-- name: RecordDeniedToolCall :execrows
INSERT INTO tool_calls (
    tool_call_id, organization_id, user_id, principal_instance_id, llm_call_id,
    product_id, feature_id, epic_id, story_id,
    tool_name, arguments,
    execution_id, family, request_digest, arguments_digest, caller_ref,
    target_key,
    state, outcome, finished_at, reason_code, drain_disposition
) VALUES (
    @tool_call_id, @organization_id, @user_id, @principal_instance_id, @llm_call_id,
    @product_id, @feature_id, @epic_id, @story_id,
    @tool_name, @arguments,
    @execution_id, @family, @request_digest, @arguments_digest, @caller_ref,
    @target_key,
    'settled', 'denied', now(), @reason_code, 'stopped_before_commit'
)
ON CONFLICT (tool_call_id) DO NOTHING;

-- Gate 2's entry (D7): the requirement set and the transition in one
-- statement, from open only.
-- name: EnterOperatorWait :execrows
UPDATE tool_calls
SET state                  = 'operator_waiting',
    requirement_set        = @requirement_set,
    requirement_set_digest = @requirement_set_digest
WHERE tool_call_id    = @tool_call_id
  AND organization_id = @organization_id
  AND state           = 'open'
  AND finished_at IS NULL;

-- The resource wait's transitions exist here so item 7 adds a producer, not
-- a column (D8 step 4).
-- name: EnterResourceWait :execrows
UPDATE tool_calls
SET state = 'resource_waiting'
WHERE tool_call_id    = @tool_call_id
  AND organization_id = @organization_id
  AND state           = 'open'
  AND finished_at IS NULL;

-- name: LeaveResourceWait :execrows
UPDATE tool_calls
SET state = 'open'
WHERE tool_call_id    = @tool_call_id
  AND organization_id = @organization_id
  AND state           = 'resource_waiting'
  AND finished_at IS NULL;

-- An approval is DURABLE and leaves the row waiting (D7): an approved attempt
-- that has not started is a different thing from an interrupted one.
-- name: RecordOperatorApproval :execrows
UPDATE tool_calls
SET operator_decision   = 'approve_once',
    operator_decided_by = @decided_by,
    operator_decided_at = now()
WHERE tool_call_id      = @tool_call_id
  AND organization_id   = @organization_id
  AND state             = 'operator_waiting'
  AND operator_decision IS NULL
  AND finished_at IS NULL;

-- A denial settles the row in the same statement that records it, releasing
-- the claim and the Story guard together.
-- name: RecordOperatorDenial :execrows
UPDATE tool_calls
SET operator_decision   = 'deny_once',
    operator_decided_by = @decided_by,
    operator_decided_at = now(),
    state               = 'settled',
    outcome             = 'denied',
    finished_at         = now(),
    reason_code         = 'operator/denied',
    drain_disposition   = 'stopped_before_commit',
    claimed_by          = NULL
WHERE tool_call_id      = @tool_call_id
  AND organization_id   = @organization_id
  AND state             = 'operator_waiting'
  AND operator_decision IS NULL
  AND finished_at IS NULL;

-- Gate 3's first act (D7): ONE conditional update that consumes the
-- approval, moves the row back to open, records the revalidation and
-- transfers the claim to the consuming instance (D5). The predicates on
-- state AND consumption are both load-bearing -- exactly one of two
-- concurrent re-presentations succeeds -- and the seam's test removes both to
-- show the property is theirs.
-- name: ConsumeOperatorDecision :execrows
UPDATE tool_calls
SET state                         = 'open',
    operator_decision_consumed_at = now(),
    operator_decision_consumed_by = tool_call_id,
    revalidated_at                = now(),
    claimed_by                    = @claimed_by
WHERE tool_call_id                  = @tool_call_id
  AND organization_id               = @organization_id
  AND state                         = 'operator_waiting'
  AND operator_decision             = 'approve_once'
  AND operator_decision_consumed_at IS NULL
  AND finished_at IS NULL;

-- A re-request inheriting a stale attempt's unconsumed approval (D5): the
-- decision is marked consumed on the STALE row with the new attempt's id,
-- once. The stale row is settled, so the guard is its state, not finished_at.
-- name: InheritOperatorDecision :execrows
UPDATE tool_calls
SET operator_decision_consumed_at = now(),
    operator_decision_consumed_by = @consumed_by
WHERE tool_call_id                  = @tool_call_id
  AND organization_id               = @organization_id
  AND state                         = 'settled'
  AND outcome                       = 'stale'
  AND operator_decision             = 'approve_once'
  AND operator_decision_consumed_at IS NULL;

-- The row a re-request may inherit from: same execution, family, substituted
-- digest and target, stale, approved, unconsumed. The most recent, if several.
-- name: FindInheritableDecision :one
SELECT * FROM tool_calls
WHERE organization_id  = @organization_id
  AND execution_id     = @execution_id
  AND family           = @family
  AND arguments_digest = @arguments_digest
  AND target_key       = @target_key
  AND state            = 'settled'
  AND outcome          = 'stale'
  AND operator_decision = 'approve_once'
  AND operator_decision_consumed_at IS NULL
ORDER BY finished_at DESC, tool_call_id DESC
LIMIT 1;

-- D8's T2 for the allow path: the attempt was current when checked.
-- name: MarkRevalidated :execrows
UPDATE tool_calls
SET revalidated_at = now()
WHERE tool_call_id    = @tool_call_id
  AND organization_id = @organization_id
  AND state           = 'open'
  AND finished_at IS NULL;

-- Settling moves the state, the outcome and the disposition together (D11),
-- releases the claim, and -- for a headless block -- writes the requirement
-- set the block preserves in the same statement (D7). From OPEN only: a
-- wait leaves through its own transitions (decision, supersession,
-- interruption, the resource wait's exit) and never through a bare
-- settlement, or an unapproved wait could be settled succeeded (PR #383
-- review). The requirement columns prefer the RECORDED value, so a
-- settlement after consumption cannot rewrite the question that was
-- approved; the seam refuses requirement input on a row that has one.
-- Once-only on finished_at, as every completion is.
-- name: SettleToolCall :execrows
UPDATE tool_calls
SET finished_at            = COALESCE(sqlc.narg('finished_at')::timestamptz, now()),
    state                  = 'settled',
    outcome                = @outcome,
    result                 = @result,
    error_message          = @error_message,
    reason_code            = @reason_code,
    drain_disposition      = @drain_disposition,
    requirement_set        = COALESCE(requirement_set, sqlc.narg('requirement_set')::jsonb),
    requirement_set_digest = COALESCE(requirement_set_digest, sqlc.narg('requirement_set_digest')::text),
    claimed_by             = NULL
WHERE tool_call_id    = @tool_call_id
  AND organization_id = @organization_id
  AND state           = 'open'
  AND finished_at IS NULL;

-- Drainage moves once, from unresolved (D11); the trigger refuses anything
-- else, and this statement asks for nothing else.
-- name: ResolveDrainDisposition :execrows
UPDATE tool_calls
SET drain_disposition = @drain_disposition
WHERE tool_call_id      = @tool_call_id
  AND organization_id   = @organization_id
  AND state             = 'settled'
  AND drain_disposition = 'unresolved';

-- A wait interrupted by a restart goes stale, decision and requirement
-- preserved (D5; ADR 0032 section 6). Conditional on the FOREIGN claim: a
-- wait this instance holds is not interrupted.
-- name: StaleInterruptedWait :execrows
UPDATE tool_calls
SET state             = 'settled',
    outcome           = 'stale',
    finished_at       = now(),
    reason_code       = 'stale/interrupted_wait',
    drain_disposition = 'stopped_before_commit',
    claimed_by        = NULL
WHERE tool_call_id    = @tool_call_id
  AND organization_id = @organization_id
  AND state           IN ('operator_waiting', 'resource_waiting')
  AND claimed_by      = @claimed_by
  AND finished_at IS NULL;

-- Supersession settles every waiting attempt of the execution stale (D10),
-- decision intact, and returns them; the OPEN attempts are the drain list and
-- are read separately, because they are not settled here.
-- name: StaleSupersededWaits :many
UPDATE tool_calls
SET state             = 'settled',
    outcome           = 'stale',
    finished_at       = now(),
    reason_code       = 'stale/authority_superseded',
    drain_disposition = 'stopped_before_commit',
    claimed_by        = NULL
WHERE execution_id    = @execution_id
  AND organization_id = @organization_id
  AND state           IN ('operator_waiting', 'resource_waiting')
  AND finished_at IS NULL
RETURNING *;

-- Exactly one reconciler proceeds on a foreign-claimed open row (D5): the
-- claim is taken conditionally on who holds it.
-- name: TakeClaim :execrows
UPDATE tool_calls
SET claimed_by = @claimed_by
WHERE tool_call_id    = @tool_call_id
  AND organization_id = @organization_id
  AND state           = 'open'
  AND claimed_by      = @previous_claim
  AND finished_at IS NULL;

-- Recovery's two enumerations in one (D5, D12): unsettled boundary attempts,
-- and settled ones whose drainage is unresolved.
-- name: ListAttemptsForRecovery :many
SELECT * FROM tool_calls
WHERE organization_id = @organization_id
  AND execution_id IS NOT NULL
  AND (state <> 'settled' OR drain_disposition = 'unresolved')
ORDER BY started_at, tool_call_id;

-- name: ListExecutionAttempts :many
SELECT * FROM tool_calls
WHERE organization_id = @organization_id
  AND execution_id    = @execution_id
ORDER BY started_at, tool_call_id;

-- The drain list: registered and unsettled at this moment (D8).
-- name: ListUnsettledExecutionAttempts :many
SELECT * FROM tool_calls
WHERE organization_id = @organization_id
  AND execution_id    = @execution_id
  AND state          <> 'settled'
ORDER BY started_at, tool_call_id;

-- The receipt's action half (D11): an attempt that is unsettled, or settled
-- with unresolved drainage, is not drained.
-- name: CountUndrainedExecutionAttempts :one
SELECT count(*)::bigint FROM tool_calls
WHERE organization_id = @organization_id
  AND execution_id    = @execution_id
  AND (state <> 'settled' OR drain_disposition = 'unresolved');

-- The Story-scoped guard's read (D4), under the Story lock the seam takes
-- first.
-- name: ListStoryWaitingAttempts :many
SELECT * FROM tool_calls
WHERE organization_id = @organization_id
  AND story_id        = @story_id
  AND state           = 'operator_waiting'
ORDER BY started_at, tool_call_id;

-- The projection's read (D11's OpenWork extension): which wait, if any, an
-- execution's attempts are in.
-- name: ListExecutionWaits :many
SELECT * FROM tool_calls
WHERE organization_id = @organization_id
  AND execution_id    = @execution_id
  AND state           IN ('operator_waiting', 'resource_waiting')
ORDER BY started_at, tool_call_id;
