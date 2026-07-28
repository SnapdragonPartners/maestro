-- LLM call outcome: item 5 schema correction.
--
-- llm_calls could record that a call ENDED but not whether it succeeded.
-- The proposed workaround -- an audit_event naming the call -- does not
-- work: audit_events has no llm_call_id column and no foreign key, so the
-- link would be an unenforced identifier buried in `detail` JSON. An
-- unenforced pointer is not a schema gap closed; it is the same gap with a
-- convention on top.
--
-- Without these columns a completed zero-token call and a failed call are
-- indistinguishable on the row, which corrupts exactly the cost and
-- reliability aggregates this family exists to serve.
--
-- Mirrors tool_calls, which already pairs finished_at with succeeded.
BEGIN;

ALTER TABLE llm_calls
    ADD COLUMN succeeded     boolean,
    ADD COLUMN error_message text;

-- Backfill BEFORE the constraint, so the migration is correct on a
-- non-empty table rather than only on an empty one.
--
-- An already-completed row has no recorded outcome and no way to recover
-- one, so it is presumed successful. That is an assumption, and it is
-- recorded here rather than left implicit: rows written before this
-- migration cannot distinguish success from failure, and no later analysis
-- should read `succeeded = true` on them as evidence of anything.
UPDATE llm_calls
SET succeeded = true
WHERE finished_at IS NOT NULL
  AND succeeded IS NULL;

-- The same completion pairing tool_calls uses: a call is finished exactly
-- when it has an outcome. Coherence BETWEEN succeeded and error_message --
-- a success carrying an error, a failure carrying none -- is the seam's
-- rule, because it is about meaning rather than shape.
ALTER TABLE llm_calls
    ADD CONSTRAINT llm_calls_completion_check
        CHECK ((finished_at IS NULL) = (succeeded IS NULL));

COMMIT;
