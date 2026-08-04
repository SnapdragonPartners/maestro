-- LLM call token axes: split the cache axis, and make availability
-- expressible (docs/v2/phase_2/design_slice_import.md, D3).
--
-- Two corrections to item 5's shape, both found by trying to load real data
-- from the golden runner into these columns.
--
-- 1. `cached_tokens` did not say WHICH cache. Providers report cache READS
--    and cache WRITES separately and bill them differently, so one column
--    could only hold one of them and its name did not record which. Renamed
--    rather than documented: the ambiguity is in the name, there is no
--    deployed data to protect, and a column called `cached_tokens` sitting
--    beside `cache_write_tokens` would invite the wrong reading forever.
--
-- 2. `NOT NULL DEFAULT 0` made a failed call indistinguishable from a call
--    that used nothing. maestro-llms populates usage only when the error is
--    nil -- a partial response returned with an error is not trusted -- so a
--    FAILED call has no measurement at all, and writing zeros asserted a
--    measurement nobody made. Every aggregate then summed those zeros as
--    though they were real. This is the same "unknown versus zero" problem
--    `cost_usd` is nullable to solve, one column over, and it gets the same
--    answer.
--
-- What this does NOT fix: the tokens actually spent by provider attempts
-- inside a failed logical call are unrecoverable from the metrics event, so
-- budget enforcement still under-counts a failed call (issue #311). Null
-- records that we do not know, which is the honest state.
BEGIN;

ALTER TABLE llm_calls RENAME COLUMN cached_tokens TO cache_read_tokens;

ALTER TABLE llm_calls
    ADD COLUMN cache_write_tokens bigint;

-- Availability is a property of the OBSERVATION, not of each column: a call
-- either has a token measurement or it has none. Dropping the defaults first
-- so no future insert silently re-establishes zero-as-measurement.
ALTER TABLE llm_calls
    ALTER COLUMN input_tokens      DROP DEFAULT,
    ALTER COLUMN output_tokens     DROP DEFAULT,
    ALTER COLUMN reasoning_tokens  DROP DEFAULT,
    ALTER COLUMN cache_read_tokens DROP DEFAULT;

ALTER TABLE llm_calls
    ALTER COLUMN input_tokens      DROP NOT NULL,
    ALTER COLUMN output_tokens     DROP NOT NULL,
    ALTER COLUMN reasoning_tokens  DROP NOT NULL,
    ALTER COLUMN cache_read_tokens DROP NOT NULL;

-- EVERY existing row loses its counters, including completed successful
-- ones. This looks harsh and is the only honest option.
--
-- Availability is all-or-none, so a legacy row can keep its measurement only
-- if all five axes are provable. The fifth is not: no row in this table was
-- written by a surface that could report cache writes, so any value for it
-- would be invented -- and defaulting it to zero would assert "this call
-- wrote nothing to cache", which is a measurement, not an absence. Backfilling
-- the axis that never existed is the same fabrication this migration removes
-- from failed calls, arriving through the migration instead of the writer.
--
-- Legacy failed rows are worse still: they carry four zeros the old surface
-- wrote precisely because it could not tell "no usage reported" from "no
-- usage", which is exactly what D3 exists to stop. Keeping them would leave
-- the defect in the history while forbidding it in new data.
--
-- The cost of nulling everything is bounded and known: there is no deployed
-- installation, and these rows exist only in development planes. The cost of
-- the alternative is a permanent stratum of rows whose totals cannot be
-- trusted and whose age is the only clue.
UPDATE llm_calls
SET input_tokens       = NULL,
    output_tokens      = NULL,
    reasoning_tokens   = NULL,
    cache_read_tokens  = NULL,
    cache_write_tokens = NULL;

-- The single non-negative check becomes one per axis. It has to be
-- recreated anyway to cover the new column, and per-axis constraints make a
-- violation name the axis that was wrong instead of the tuple that contained
-- it -- the same reason the surface validates axes individually rather than
-- their sum. NULL >= 0 is NULL and a CHECK only fails on FALSE, so an
-- unmeasured row passes all five untouched.
ALTER TABLE llm_calls
    DROP CONSTRAINT llm_calls_tokens_nonnegative_check;

ALTER TABLE llm_calls
    ADD CONSTRAINT llm_calls_input_tokens_nonnegative_check
        CHECK (input_tokens IS NULL OR input_tokens >= 0),
    ADD CONSTRAINT llm_calls_output_tokens_nonnegative_check
        CHECK (output_tokens IS NULL OR output_tokens >= 0),
    ADD CONSTRAINT llm_calls_reasoning_tokens_nonnegative_check
        CHECK (reasoning_tokens IS NULL OR reasoning_tokens >= 0),
    ADD CONSTRAINT llm_calls_cache_read_tokens_nonnegative_check
        CHECK (cache_read_tokens IS NULL OR cache_read_tokens >= 0),
    ADD CONSTRAINT llm_calls_cache_write_tokens_nonnegative_check
        CHECK (cache_write_tokens IS NULL OR cache_write_tokens >= 0);

-- All five or none. Four axes present and one missing describes nothing: a
-- partial measurement would be summed as though it were complete, and the
-- missing axis would read as zero in every total.
ALTER TABLE llm_calls
    ADD CONSTRAINT llm_calls_token_availability_check
    CHECK (num_nonnulls(input_tokens, output_tokens, reasoning_tokens,
                        cache_read_tokens, cache_write_tokens) IN (0, 5));

COMMIT;
