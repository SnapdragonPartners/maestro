-- Reverse the tool-call state vocabulary -- and REFUSE rather than lie.
--
-- This down migration is unlike 000021's. That one drops what it created and
-- restores the schema exactly. This one projects a six-value vocabulary onto
-- a boolean, and four classes of row have no boolean image at all. A down
-- migration that corrupts is worse than one that refuses, and refusal puts
-- the decision in front of an operator instead of behind a backfill.
--
-- The policy is IDENTITY-PRESERVING, so it aborts on everything the old shape
-- cannot express -- not only the new outcomes. Four classes:
--
--   1. outcome in (denied, blocked, stale, unknown). `denied` round-trips as
--      `failed`, asserting that an action the boundary REFUSED was attempted
--      and failed; `unknown` has no boolean image at all.
--   2. state in (operator_waiting, resource_waiting). Both collapse to an
--      indistinguishable legacy in-flight row -- the healthy wait and the
--      dead process become the same two nulls, which is precisely the
--      ambiguity ADR 0030 section 8 created this migration to remove.
--   3. a non-null requirement_set. Dropping the column erases it, and this
--      bites hardest on a SUCCEEDED row, which the outcome guard waves
--      through while the record of what an operator was asked disappears.
--   4. a non-null execution_id. Same shape once more: a succeeded or failed
--      row passes every guard above and still loses the correlation binding
--      ADR 0032 line 1250 requires persisted.
--
-- The coarse-projection alternative is coherent and is rejected: denied,
-- blocked and stale could all map to succeeded=false, leaving only `unknown`
-- unrepresentable. That loses the distinction silently on the way back up and
-- fabricates history in the family ADR 0021 treats as evidence.
--
-- ORDER MATTERS HERE TOO, for the mirror-image reason: re-adding
-- tool_calls_finished_check before `succeeded` exists and is populated would
-- violate it on creation. Refuse, add, backfill, re-add, drop.
BEGIN;

-- ---------------------------------------------------------------------------
-- Step 1: refuse. Everything after this may assume only succeeded and failed
-- outcomes, no declared waits, and no new-only state -- which is what makes
-- the backfill total rather than best-effort.
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    offending bigint;
BEGIN
    SELECT count(*) INTO offending FROM tool_calls
     WHERE outcome IN ('denied', 'blocked', 'stale', 'unknown');
    IF offending > 0 THEN
        RAISE EXCEPTION 'cannot reverse 000022: % tool call(s) hold an outcome no boolean can '
            'express (denied, blocked, stale or unknown). Reversing would record them as failed, '
            'asserting an attempt that in some cases never happened. Resolve or delete these rows '
            'first.', offending;
    END IF;

    SELECT count(*) INTO offending FROM tool_calls
     WHERE state IN ('operator_waiting', 'resource_waiting');
    IF offending > 0 THEN
        RAISE EXCEPTION 'cannot reverse 000022: % tool call(s) are in a declared wait. The old shape '
            'cannot distinguish a healthy wait from a dead process, which is the ambiguity this '
            'migration removed.', offending;
    END IF;

    SELECT count(*) INTO offending FROM tool_calls WHERE requirement_set IS NOT NULL;
    IF offending > 0 THEN
        RAISE EXCEPTION 'cannot reverse 000022: % tool call(s) carry a requirement set, which '
            'dropping the column would erase.', offending;
    END IF;

    SELECT count(*) INTO offending FROM tool_calls WHERE execution_id IS NOT NULL;
    IF offending > 0 THEN
        RAISE EXCEPTION 'cannot reverse 000022: % tool call(s) carry an execution correlation, '
            'which dropping the column would erase.', offending;
    END IF;
END $$;

-- ---------------------------------------------------------------------------
-- Steps 2 and 3: restore the boolean and populate it.
--
-- Only `succeeded` and `failed` survive step 1, so the mapping is total. Rows
-- still `open` have no outcome and keep a null succeeded beside their null
-- finished_at, which is the pre-000022 encoding of an unfinished call.
-- ---------------------------------------------------------------------------
ALTER TABLE tool_calls ADD COLUMN succeeded boolean;

UPDATE tool_calls
   SET succeeded = (outcome = 'succeeded')
 WHERE outcome IS NOT NULL;

-- ---------------------------------------------------------------------------
-- Step 4: re-add the old constraint, now satisfiable by construction.
-- ---------------------------------------------------------------------------
ALTER TABLE tool_calls
    ADD CONSTRAINT tool_calls_finished_check
        CHECK ((finished_at IS NULL) = (succeeded IS NULL));

-- ---------------------------------------------------------------------------
-- Step 5: put outcome coherence back on the boolean.
--
-- The forward migration re-expressed 000011's rule over `outcome`; reversing
-- has to re-express it over `succeeded` again, and the two cannot coexist
-- under one name, so the new one is dropped first. Dropping the `outcome`
-- column in step 6 would remove it anyway -- doing it explicitly here is what
-- lets the original be added back under the same name.
--
-- It holds by construction on the rows that survive step 1: a succeeded row
-- carries no error message and a failed one carries a non-blank diagnostic,
-- both enforced by the constraint being replaced.
-- ---------------------------------------------------------------------------
ALTER TABLE tool_calls DROP CONSTRAINT tool_calls_outcome_coherence_check;

ALTER TABLE tool_calls
    ADD CONSTRAINT tool_calls_outcome_coherence_check
        CHECK (
            NOT (succeeded IS TRUE AND error_message IS NOT NULL)
            AND NOT (succeeded IS FALSE
                     AND (error_message IS NULL
                          OR btrim(error_message, E' \t\n\r\f\v') = ''))
            AND NOT (finished_at IS NULL AND error_message IS NOT NULL)
        );

-- ---------------------------------------------------------------------------
-- Step 6: drop what 000022 added.
-- ---------------------------------------------------------------------------
DROP INDEX tool_calls_execution_id_idx;
DROP INDEX tool_calls_state_idx;

ALTER TABLE tool_calls
    DROP CONSTRAINT tool_calls_execution_fkey,
    DROP CONSTRAINT tool_calls_execution_lineage_check,
    DROP CONSTRAINT tool_calls_blocked_requirement_check,
    DROP CONSTRAINT tool_calls_operator_wait_requirement_check,
    DROP CONSTRAINT tool_calls_requirement_digest_check,
    DROP CONSTRAINT tool_calls_requirement_nonempty_check,
    DROP CONSTRAINT tool_calls_requirement_object_check,
    DROP CONSTRAINT tool_calls_requirement_pairing_check,
    DROP CONSTRAINT tool_calls_settled_finished_check,
    DROP CONSTRAINT tool_calls_settled_outcome_check,
    DROP CONSTRAINT tool_calls_outcome_check,
    DROP CONSTRAINT tool_calls_state_check,
    DROP COLUMN requirement_set_digest,
    DROP COLUMN requirement_set,
    DROP COLUMN execution_id,
    DROP COLUMN outcome,
    DROP COLUMN state;

COMMIT;
