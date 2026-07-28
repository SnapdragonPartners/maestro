-- LLM call outcome, and outcome coherence for both call tables.
-- Item 5 schema correction.
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
BEGIN;

-- Refuse rather than invent.
--
-- An earlier version of this migration backfilled succeeded = true for
-- already-completed rows and warned in a comment that the value was not
-- evidence. A comment cannot constrain a query: once written, that value
-- IS canonical success data to every later reader, and the warning lives
-- somewhere nobody joins against.
--
-- There is no writer for these tables yet -- item 5 adds the first one --
-- so any row here arrived by a path that does not exist, and the right
-- response to an impossible row is to stop rather than to guess what it
-- meant. In every real environment this assertion is a no-op.
--
-- If it does fire, the data is pre-item-5 and disposable: clear the rows,
-- or `make dataplane-reset FORCE=1`, and re-run.
DO $$
DECLARE
    stale_llm  bigint;
    stale_tool bigint;
BEGIN
    SELECT count(*) INTO stale_llm
    FROM llm_calls
    WHERE finished_at IS NOT NULL;

    SELECT count(*) INTO stale_tool
    FROM tool_calls
    WHERE (succeeded IS TRUE  AND error_message IS NOT NULL)
       OR (succeeded IS FALSE AND (error_message IS NULL OR btrim(error_message) = ''))
       OR (finished_at IS NULL AND error_message IS NOT NULL);

    IF stale_llm > 0 OR stale_tool > 0 THEN
        RAISE EXCEPTION
            'migration 000011 found % completed llm_calls and % incoherent tool_calls predating this item; '
            'their outcome is unrecoverable and this migration will not invent one. '
            'These rows predate any writer, so they are disposable: delete them or run '
            '`make dataplane-reset FORCE=1`, then re-run.',
            stale_llm, stale_tool;
    END IF;
END $$;

ALTER TABLE llm_calls
    ADD COLUMN succeeded     boolean,
    ADD COLUMN error_message text;

-- Completion pairing, mirroring the one tool_calls already carries: a call
-- is finished exactly when it has an outcome.
ALTER TABLE llm_calls
    ADD CONSTRAINT llm_calls_completion_check
        CHECK ((finished_at IS NULL) = (succeeded IS NULL));

-- Outcome coherence, in the SCHEMA rather than only at the seam.
--
-- An earlier version of this design claimed SQL could not express which
-- pairings are meaningful. That was simply wrong -- these are ordinary
-- CHECK constraints -- and leaving them to the seam would have meant the
-- only guard sat in the one place a direct write bypasses.
--
-- The seam keeps its own checks so callers get a diagnostic naming the
-- field; these are the backstop that holds when something writes around it.
ALTER TABLE llm_calls
    ADD CONSTRAINT llm_calls_outcome_coherence_check
        CHECK (
            -- A success carries no error.
            NOT (succeeded IS TRUE AND error_message IS NOT NULL)
            -- A failure carries a non-blank diagnostic; the failure path is
            -- exactly when someone reads the record.
            AND NOT (succeeded IS FALSE AND (error_message IS NULL OR btrim(error_message) = ''))
            -- An open call has no error yet.
            AND NOT (finished_at IS NULL AND error_message IS NOT NULL)
        );

ALTER TABLE tool_calls
    ADD CONSTRAINT tool_calls_outcome_coherence_check
        CHECK (
            NOT (succeeded IS TRUE AND error_message IS NOT NULL)
            AND NOT (succeeded IS FALSE AND (error_message IS NULL OR btrim(error_message) = ''))
            AND NOT (finished_at IS NULL AND error_message IS NOT NULL)
        );

COMMIT;
