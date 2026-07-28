-- Reverses the LLM call outcome columns.
--
-- Dropping these DISCARDS the recorded success or failure of every call:
-- there is nowhere else it is stored. Reversible in structure, lossy in
-- content -- which is the honest description of every down migration that
-- removes a column, and is why down migrations are for development rather
-- than for recovery.
BEGIN;

ALTER TABLE llm_calls
    DROP CONSTRAINT IF EXISTS llm_calls_completion_check;

ALTER TABLE llm_calls
    DROP COLUMN IF EXISTS error_message,
    DROP COLUMN IF EXISTS succeeded;

COMMIT;
