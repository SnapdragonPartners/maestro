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
-- Recovery, if it does fire. golang-migrate records the target version with
-- dirty = true BEFORE executing this file, and the BEGIN/COMMIT here rolls
-- back the DDL but not that metadata -- so the database is left at version
-- 11, dirty, and every later run refuses until the flag is cleared.
-- "Delete the rows and re-run" therefore does NOT work on its own.
--
-- Either:
--   `make dataplane-reset FORCE=1`                          (destroys the plane), or
--   delete the offending rows, then
--   `make dataplane-force-version VERSION=10` and `make dataplane-migrate`.
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
       OR (succeeded IS FALSE AND (error_message IS NULL OR btrim(error_message, E' \t\n\r\f\v') = ''))
       OR (finished_at IS NULL AND error_message IS NOT NULL);

    IF stale_llm > 0 OR stale_tool > 0 THEN
        RAISE EXCEPTION
            'migration 000011 found % completed llm_calls and % incoherent tool_calls predating this item; '
            'their outcome is unrecoverable and this migration will not invent one. '
            'These rows predate any writer, so they are disposable. NOTE that this failure leaves the '
            'schema version recorded as 11 and dirty, so re-running alone will not work: either run '
            '`make dataplane-reset FORCE=1`, or delete the rows and run '
            '`make dataplane-force-version VERSION=10` followed by `make dataplane-migrate`.',
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
            AND NOT (succeeded IS FALSE AND (error_message IS NULL OR btrim(error_message, E' \t\n\r\f\v') = ''))
            -- An open call has no error yet.
            AND NOT (finished_at IS NULL AND error_message IS NOT NULL)
        );

-- Row-local invariants belong in SQL.
--
-- The live schema design says the database enforces facts true of ONE row;
-- an earlier draft of item 5 assigned these to the seam alone, which puts
-- the only guard in the place a direct write goes around.
--
-- Note the explicit character list on btrim: with one argument it strips
-- SPACES ONLY, so a tab- or newline-only "diagnostic" satisfied the
-- coherence check above while being blank to any reader. Verified against
-- the running server, not assumed.
ALTER TABLE llm_calls
    ADD CONSTRAINT llm_calls_names_nonblank_check
        CHECK (btrim(provider, E' \t\n\r\f\v') <> '' AND btrim(model, E' \t\n\r\f\v') <> ''),
    -- numeric admits 'NaN', and NaN = NaN is TRUE in Postgres, so the
    -- usual self-comparison trick does not detect it. Inequality does.
    ADD CONSTRAINT llm_calls_cost_finite_check
        CHECK (cost_usd IS NULL OR cost_usd <> 'NaN'::numeric),
    ADD CONSTRAINT llm_calls_interval_check
        CHECK (finished_at IS NULL OR finished_at >= started_at);

ALTER TABLE tool_calls
    ADD CONSTRAINT tool_calls_name_nonblank_check
        CHECK (btrim(tool_name, E' \t\n\r\f\v') <> ''),
    ADD CONSTRAINT tool_calls_interval_check
        CHECK (finished_at IS NULL OR finished_at >= started_at);

-- metric_events.value is double precision, which admits NaN and both
-- infinities. A non-finite metric poisons every aggregate that touches it.
ALTER TABLE metric_events
    ADD CONSTRAINT metric_events_name_nonblank_check
        CHECK (btrim(metric_name, E' \t\n\r\f\v') <> ''),
    ADD CONSTRAINT metric_events_value_finite_check
        CHECK (value <> 'NaN'::float8 AND value <> 'Infinity'::float8 AND value <> '-Infinity'::float8);

ALTER TABLE audit_events
    ADD CONSTRAINT audit_events_type_nonblank_check
        CHECK (btrim(event_type, E' \t\n\r\f\v') <> '');

ALTER TABLE tool_calls
    ADD CONSTRAINT tool_calls_outcome_coherence_check
        CHECK (
            NOT (succeeded IS TRUE AND error_message IS NOT NULL)
            AND NOT (succeeded IS FALSE AND (error_message IS NULL OR btrim(error_message, E' \t\n\r\f\v') = ''))
            AND NOT (finished_at IS NULL AND error_message IS NOT NULL)
        );

COMMIT;
