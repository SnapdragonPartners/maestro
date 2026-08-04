-- Reverse the token-axis split and availability change.
--
-- This is lossy in one direction and says so: an unmeasured (null) row
-- becomes zeros again, which is exactly the conflation the up migration
-- exists to remove, and cache-write counts are discarded because the old
-- shape has nowhere to put them. That is what reverting to the old contract
-- means, not a defect in the reversal.
BEGIN;

ALTER TABLE llm_calls
    DROP CONSTRAINT llm_calls_token_availability_check;

ALTER TABLE llm_calls
    DROP CONSTRAINT llm_calls_input_tokens_nonnegative_check,
    DROP CONSTRAINT llm_calls_output_tokens_nonnegative_check,
    DROP CONSTRAINT llm_calls_reasoning_tokens_nonnegative_check,
    DROP CONSTRAINT llm_calls_cache_read_tokens_nonnegative_check,
    DROP CONSTRAINT llm_calls_cache_write_tokens_nonnegative_check;

ALTER TABLE llm_calls DROP COLUMN cache_write_tokens;

-- Unmeasured rows must carry a value before NOT NULL can be restored.
UPDATE llm_calls
SET input_tokens      = COALESCE(input_tokens, 0),
    output_tokens     = COALESCE(output_tokens, 0),
    reasoning_tokens  = COALESCE(reasoning_tokens, 0),
    cache_read_tokens = COALESCE(cache_read_tokens, 0);

ALTER TABLE llm_calls
    ALTER COLUMN input_tokens      SET NOT NULL,
    ALTER COLUMN output_tokens     SET NOT NULL,
    ALTER COLUMN reasoning_tokens  SET NOT NULL,
    ALTER COLUMN cache_read_tokens SET NOT NULL;

ALTER TABLE llm_calls
    ALTER COLUMN input_tokens      SET DEFAULT 0,
    ALTER COLUMN output_tokens     SET DEFAULT 0,
    ALTER COLUMN reasoning_tokens  SET DEFAULT 0,
    ALTER COLUMN cache_read_tokens SET DEFAULT 0;

ALTER TABLE llm_calls RENAME COLUMN cache_read_tokens TO cached_tokens;

ALTER TABLE llm_calls
    ADD CONSTRAINT llm_calls_tokens_nonnegative_check
    CHECK (input_tokens >= 0 AND output_tokens >= 0
           AND reasoning_tokens >= 0 AND cached_tokens >= 0);

COMMIT;
