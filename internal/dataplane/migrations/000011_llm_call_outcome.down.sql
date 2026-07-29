-- Reverses the LLM call outcome columns and both coherence constraints.
--
-- Dropping these DISCARDS the recorded success or failure of every LLM
-- call: there is nowhere else it is stored. Reversible in structure, lossy
-- in content -- the honest description of every down migration that
-- removes a column, and why down migrations are for development rather
-- than for recovery.
BEGIN;

ALTER TABLE audit_events
    DROP CONSTRAINT IF EXISTS audit_events_type_nonblank_check;

ALTER TABLE metric_events
    DROP CONSTRAINT IF EXISTS metric_events_value_finite_check,
    DROP CONSTRAINT IF EXISTS metric_events_name_nonblank_check;

ALTER TABLE tool_calls
    DROP CONSTRAINT IF EXISTS tool_calls_outcome_coherence_check,
    DROP CONSTRAINT IF EXISTS tool_calls_interval_check,
    DROP CONSTRAINT IF EXISTS tool_calls_name_nonblank_check;

ALTER TABLE llm_calls
    DROP CONSTRAINT IF EXISTS llm_calls_outcome_coherence_check,
    DROP CONSTRAINT IF EXISTS llm_calls_interval_check,
    DROP CONSTRAINT IF EXISTS llm_calls_cost_finite_check,
    DROP CONSTRAINT IF EXISTS llm_calls_names_nonblank_check,
    DROP CONSTRAINT IF EXISTS llm_calls_completion_check;

ALTER TABLE llm_calls
    DROP COLUMN IF EXISTS error_message,
    DROP COLUMN IF EXISTS succeeded;

COMMIT;
