-- Reverse the execution boundary's columns -- and REFUSE rather than discard.
--
-- The pre-000024 schema cannot hold a mediated attempt, a configured
-- execution, a live principal's execution binding or a forge binding: the
-- columns do not exist. A reversal that dropped them would turn a settled
-- attempt's reason code, decision and drainage into nothing, a terminal
-- execution back into an open one, and a bound repository into an unbound
-- one -- each a row the old shape then reads as a different fact. So this
-- refuses when any boundary-owned state exists:
--
--   1. any executions row -- every execution since 000024 carries a resolved
--      configuration, which the old shape cannot record;
--   2. any principal with an execution binding;
--   3. any execution-bound tool call (execution_id IS NOT NULL), which is
--      every attempt the boundary wrote;
--   4. any repository_forge_bindings row.
--
-- With none present the columns, constraints, triggers and functions go --
-- a function left behind is the reversal's own residue. Importer rows on
-- tool_calls, which never carried any of this, survive unchanged.
--
-- LOCK ORDER is the up migration's, restricted to what this direction scans
-- or alters: executions, principal_instances, tool_calls,
-- repository_forge_bindings.
--
-- RECOVERING FROM A REFUSAL: the recorded version is 23 and DIRTY while the
-- schema is really at 24. Force it forward --
--
--     make dataplane-force-version VERSION=24 FORCE=1
--
-- -- then either remove the boundary-owned state deliberately or stop
-- reversing.
BEGIN;

LOCK TABLE executions               IN ACCESS EXCLUSIVE MODE;
LOCK TABLE principal_instances      IN ACCESS EXCLUSIVE MODE;
LOCK TABLE tool_calls               IN ACCESS EXCLUSIVE MODE;
LOCK TABLE repository_forge_bindings IN ACCESS EXCLUSIVE MODE;

DO $$
DECLARE
    offending bigint;
BEGIN
    SELECT count(*) INTO offending FROM executions;
    IF offending > 0 THEN
        RAISE EXCEPTION 'cannot reverse 000024: % execution(s) carry a resolved configuration, which the '
            'old shape cannot record. Force the version forward (make dataplane-force-version '
            'VERSION=24 FORCE=1), then remove them deliberately or stop reversing.', offending;
    END IF;
    SELECT count(*) INTO offending FROM principal_instances WHERE execution_id IS NOT NULL;
    IF offending > 0 THEN
        RAISE EXCEPTION 'cannot reverse 000024: % principal(s) are bound to an execution. Force the '
            'version forward (make dataplane-force-version VERSION=24 FORCE=1), then remove them '
            'deliberately or stop reversing.', offending;
    END IF;
    SELECT count(*) INTO offending FROM tool_calls WHERE execution_id IS NOT NULL;
    IF offending > 0 THEN
        RAISE EXCEPTION 'cannot reverse 000024: % mediated attempt(s) carry a reason code, decision or '
            'drainage the old shape cannot hold. Force the version forward (make '
            'dataplane-force-version VERSION=24 FORCE=1), then remove them deliberately or stop '
            'reversing.', offending;
    END IF;
    SELECT count(*) INTO offending FROM repository_forge_bindings;
    IF offending > 0 THEN
        RAISE EXCEPTION 'cannot reverse 000024: % repository forge binding(s) exist and would be '
            'discarded. Force the version forward (make dataplane-force-version VERSION=24 '
            'FORCE=1), then remove them deliberately or stop reversing.', offending;
    END IF;
END $$;

DROP INDEX principal_instances_execution_idx;
ALTER TABLE principal_instances
    DROP CONSTRAINT principal_instances_live_agent_execution_check,
    DROP CONSTRAINT principal_instances_execution_fkey,
    DROP COLUMN execution_id;

DROP TRIGGER executions_blocked_attempt_is_blocked ON executions;
DROP FUNCTION executions_require_blocked_attempt();
ALTER TABLE executions DROP CONSTRAINT executions_blocked_tool_call_fkey;

DROP TRIGGER tool_calls_disposition_monotonic ON tool_calls;
DROP FUNCTION tool_calls_refuse_disposition_rewrite();

DROP INDEX tool_calls_story_wait_idx;
DROP INDEX tool_calls_recovery_idx;
DROP INDEX tool_calls_live_mutation_key;

ALTER TABLE tool_calls
    DROP CONSTRAINT tool_calls_id_execution_org_key,
    DROP CONSTRAINT tool_calls_drain_disposition_presence_check,
    DROP CONSTRAINT tool_calls_drain_disposition_check,
    DROP CONSTRAINT tool_calls_decision_requires_requirement_check,
    DROP CONSTRAINT tool_calls_consumed_by_fkey,
    DROP CONSTRAINT tool_calls_consumption_requires_approval_check,
    DROP CONSTRAINT tool_calls_consumption_shape_check,
    DROP CONSTRAINT tool_calls_operator_decided_by_fkey,
    DROP CONSTRAINT tool_calls_operator_decision_check,
    DROP CONSTRAINT tool_calls_operator_decision_shape_check,
    DROP CONSTRAINT tool_calls_reason_code_format_check,
    DROP CONSTRAINT tool_calls_reason_code_check,
    DROP CONSTRAINT tool_calls_claim_check,
    DROP CONSTRAINT tool_calls_revalidated_check,
    DROP CONSTRAINT tool_calls_caller_ref_check,
    DROP CONSTRAINT tool_calls_mutation_key_check,
    DROP CONSTRAINT tool_calls_target_key_check,
    DROP CONSTRAINT tool_calls_arguments_digest_check,
    DROP CONSTRAINT tool_calls_request_digest_check,
    DROP CONSTRAINT tool_calls_family_check,
    DROP CONSTRAINT tool_calls_boundary_identity_check,
    DROP COLUMN drain_disposition,
    DROP COLUMN operator_decision_consumed_by,
    DROP COLUMN operator_decision_consumed_at,
    DROP COLUMN operator_decided_at,
    DROP COLUMN operator_decided_by,
    DROP COLUMN operator_decision,
    DROP COLUMN reason_code,
    DROP COLUMN revalidated_at,
    DROP COLUMN claimed_by,
    DROP COLUMN mutation_key,
    DROP COLUMN target_key,
    DROP COLUMN caller_ref,
    DROP COLUMN arguments_digest,
    DROP COLUMN request_digest,
    DROP COLUMN family;

DROP TRIGGER executions_immutable ON executions;
DROP FUNCTION executions_refuse_rewrite();

ALTER TABLE executions
    DROP CONSTRAINT executions_terminal_closes_admission_check,
    DROP CONSTRAINT executions_terminated_at_check,
    DROP CONSTRAINT executions_error_message_check,
    DROP CONSTRAINT executions_blocked_axis_check,
    DROP CONSTRAINT executions_failure_axis_check,
    DROP CONSTRAINT executions_cancellation_axis_check,
    DROP CONSTRAINT executions_completion_axis_check,
    DROP CONSTRAINT executions_failure_class_check,
    DROP CONSTRAINT executions_cancellation_reason_check,
    DROP CONSTRAINT executions_completion_disposition_check,
    DROP CONSTRAINT executions_status_check,
    DROP COLUMN terminated_at,
    DROP COLUMN error_message,
    DROP COLUMN blocked_tool_call_id,
    DROP COLUMN failure_class,
    DROP COLUMN cancellation_reason,
    DROP COLUMN completion_disposition,
    DROP COLUMN status,
    DROP CONSTRAINT executions_id_org_key,
    DROP CONSTRAINT executions_acting_user_fkey,
    DROP CONSTRAINT executions_capability_set_check,
    DROP COLUMN acting_user_id,
    DROP COLUMN headless,
    DROP COLUMN capability_set;

DROP TABLE repository_forge_bindings;

DROP FUNCTION execution_capability_set_canonical(jsonb);

COMMIT;
