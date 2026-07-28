-- Reverses the LLM call outcome columns and both coherence constraints.
--
-- Dropping these DISCARDS the recorded success or failure of every LLM
-- call: there is nowhere else it is stored. Reversible in structure, lossy
-- in content -- the honest description of every down migration that
-- removes a column, and why down migrations are for development rather
-- than for recovery.
BEGIN;

ALTER TABLE tool_calls
    DROP CONSTRAINT IF EXISTS tool_calls_outcome_coherence_check;

ALTER TABLE llm_calls
    DROP CONSTRAINT IF EXISTS llm_calls_outcome_coherence_check,
    DROP CONSTRAINT IF EXISTS llm_calls_completion_check;

ALTER TABLE llm_calls
    DROP COLUMN IF EXISTS error_message,
    DROP COLUMN IF EXISTS succeeded;

COMMIT;
