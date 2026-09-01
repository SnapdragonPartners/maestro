-- The tool-call record gains an explicit state and a six-value outcome, and
-- LOSES tool_calls_finished_check (docs/v2/phase_3/design_work-hierarchy.md
-- D10 and D11).
--
-- ADR 0030 section 8 requires the record to distinguish a healthy operator
-- wait, a healthy resource wait, and an interrupted attempt, "because the
-- watchdog cannot act correctly without that distinction". Migration 000005
-- gave the record two positions -- in flight and finished -- carried by
--
--   CONSTRAINT tool_calls_finished_check
--       CHECK ((finished_at IS NULL) = (succeeded IS NULL))
--
-- so settling an attempt requires a BOOLEAN. Section 8's own reconciliation
-- outcome, `attempted, outcome unknown`, is neither true nor false, which is
-- why this migration REPLACES that constraint rather than adding to it. ADR
-- 0030 line 648, ADR 0032 line 1578 and ADR 0019 line 174 all record it.
--
-- The outcome vocabulary is a UNION and no single ADR carries all six.
-- ADR 0032 line 841 gives succeeded, failed, denied, blocked and unknown;
-- `stale` comes from ADR 0030 line 443, "the action terminates as stale, and
-- a fresh action is required". Stated here because a reviewer checking either
-- ADR alone will find five.
--
-- STATEMENT ORDER IS PART OF THE DESIGN, not a detail. The constraints below
-- cannot precede the backfill: every existing finished row takes state='open'
-- from the column default with a null outcome, so the settled equivalence
-- would be violated on creation and ADD CONSTRAINT would fail against any
-- non-empty database. Five steps, and steps 3 and 4 stay in this order so the
-- record is never governed by neither equivalence.
BEGIN;

-- ---------------------------------------------------------------------------
-- Step 1: the columns.
-- ---------------------------------------------------------------------------
ALTER TABLE tool_calls
    ADD COLUMN state        text NOT NULL DEFAULT 'open',
    ADD COLUMN outcome      text,
    -- The execution that admitted this action. Nullable: Orchestrator-initiated
    -- work is recorded and is not an agent action under an execution (ADR 0030
    -- section 10), and every pre-migration row predates executions entirely.
    ADD COLUMN execution_id uuid,
    -- The canonical requirement set, and its digest for gate 3's set equality.
    -- An OBJECT keyed by requirement identity, never an array: the plane's
    -- canonicaliser is RFC 8785 JCS, which sorts object keys and leaves array
    -- order untouched, so an array-encoded set would digest differently under
    -- reordering and the comparison ADR 0030 line 211 requires would fail
    -- spuriously. The identity vocabulary is item 5's; item 2 constrains
    -- structure only and treats the keys as opaque.
    ADD COLUMN requirement_set        jsonb,
    ADD COLUMN requirement_set_digest text;

-- ---------------------------------------------------------------------------
-- Step 2: the backfill, which sets STATE and not only outcome.
--
-- Without this the column default leaves every historical finished row
-- settled-in-fact and open-in-column, violating the equivalence added below
-- on its first read.
--
-- Rows with a null finished_at keep the default `open`. They are historical
-- in-flight attempts whose process is gone, and they are NOT settled as
-- `unknown`: doing so would assert a reconciliation nobody performed, and an
-- open attempt in no declared wait is exactly what ADR 0030 section 8 says a
-- reconciler reads as `attempted, outcome unknown`.
-- ---------------------------------------------------------------------------
UPDATE tool_calls
   SET state   = 'settled',
       outcome = CASE succeeded WHEN true THEN 'succeeded' ELSE 'failed' END
 WHERE finished_at IS NOT NULL;

-- ---------------------------------------------------------------------------
-- Step 3: the constraints, valid only now that no row contradicts them.
-- ---------------------------------------------------------------------------
ALTER TABLE tool_calls
    ADD CONSTRAINT tool_calls_state_check
        CHECK (state IN ('open', 'operator_waiting', 'resource_waiting', 'settled')),
    ADD CONSTRAINT tool_calls_outcome_check
        CHECK (outcome IS NULL OR outcome IN
               ('succeeded', 'failed', 'denied', 'blocked', 'stale', 'unknown')),

    -- The replacement for tool_calls_finished_check, in both directions.
    -- Settled is one fact with three witnesses, and any two disagreeing is
    -- the ambiguity the old constraint existed to prevent.
    ADD CONSTRAINT tool_calls_settled_outcome_check
        CHECK ((state = 'settled') = (outcome IS NOT NULL)),
    ADD CONSTRAINT tool_calls_settled_finished_check
        CHECK ((state = 'settled') = (finished_at IS NOT NULL)),

    -- The requirement set is a NON-EMPTY OBJECT or absent. `{}` is an object
    -- and passes jsonb_typeof, so without the emptiness check an
    -- operator_waiting row could satisfy "has a requirement set" while
    -- recording no requirement at all.
    ADD CONSTRAINT tool_calls_requirement_pairing_check
        CHECK ((requirement_set IS NULL) = (requirement_set_digest IS NULL)),
    ADD CONSTRAINT tool_calls_requirement_object_check
        CHECK (requirement_set IS NULL OR jsonb_typeof(requirement_set) = 'object'),
    ADD CONSTRAINT tool_calls_requirement_nonempty_check
        CHECK (requirement_set IS NULL OR requirement_set <> '{}'::jsonb),
    ADD CONSTRAINT tool_calls_requirement_digest_check
        CHECK (requirement_set_digest IS NULL OR requirement_set_digest ~ '^[0-9a-f]{64}$'),

    -- Applicability. A wait on an operator and a headless block both carry
    -- the requirement, and ADR 0032 line 775 requires a blocked attempt to
    -- preserve it so the execution's result can reference it.
    ADD CONSTRAINT tool_calls_operator_wait_requirement_check
        CHECK (state <> 'operator_waiting' OR requirement_set IS NOT NULL),
    ADD CONSTRAINT tool_calls_blocked_requirement_check
        CHECK (outcome IS DISTINCT FROM 'blocked' OR requirement_set IS NOT NULL),

    -- Correlation travels the WHOLE lineage. On this table the lineage
    -- columns are NULLABLE, so MATCH SIMPLE would skip the foreign key
    -- whenever any is null -- and a partially-filled row is exactly how a
    -- tool call comes to name another Story's execution. Migration 000005
    -- documents the same trap against its own lineage columns.
    ADD CONSTRAINT tool_calls_execution_lineage_check
        CHECK (execution_id IS NULL
               OR (story_id   IS NOT NULL AND epic_id    IS NOT NULL
               AND feature_id IS NOT NULL AND product_id IS NOT NULL)),
    ADD CONSTRAINT tool_calls_execution_fkey
        FOREIGN KEY (execution_id, story_id, epic_id, feature_id, product_id, organization_id)
        REFERENCES executions (execution_id, story_id, epic_id, feature_id, product_id, organization_id)
        ON DELETE RESTRICT;

-- ---------------------------------------------------------------------------
-- Step 4: drop the old constraint, AFTER the new equivalence is in force.
--
-- It stays satisfied throughout steps 1-3, since nothing before this touches
-- succeeded or finished_at.
-- ---------------------------------------------------------------------------
ALTER TABLE tool_calls DROP CONSTRAINT tool_calls_finished_check;

-- ---------------------------------------------------------------------------
-- Step 5: drop the boolean the vocabulary replaced.
--
-- llm_calls keeps ITS succeeded column: an LLM call is not a mediated action
-- (ADR 0030 section 10) and none of this applies to it.
--
-- THIS DROP TAKES A CONSTRAINT WITH IT. Migration 000011 added
-- tool_calls_outcome_coherence_check, whose predicate reads `succeeded`, and
-- Postgres drops a CHECK that depends on a dropped column silently and
-- without warning. Step 6 restores it over the new vocabulary; without that
-- step this migration would quietly delete a rule nothing announced was
-- going, which is what 000011's own test exists to catch.
-- ---------------------------------------------------------------------------
ALTER TABLE tool_calls DROP COLUMN succeeded;

-- ---------------------------------------------------------------------------
-- Step 6: restore outcome coherence, re-expressed over `outcome`.
--
-- Same name, so the guard that watches for its absence keeps watching the
-- same rule. The three clauses are 000011's, with the boolean replaced:
--
--   * a SUCCEEDED action carries no error message at all -- absence is null,
--     never an empty string, or the row is one no reader can interpret;
--   * a FAILED action carries a non-blank diagnostic, because the failure
--     path is exactly when someone reads the record;
--   * an UNFINISHED action carries no error message, which needs no
--     vocabulary and is unchanged.
--
-- The four other outcomes are deliberately unconstrained here. Whether a
-- denial's reason code, a block's requirement or a stale attempt belongs in
-- error_message is the execution boundary's to settle (item 5), and inventing
-- the rule now would bind a producer that does not exist.
-- ---------------------------------------------------------------------------
ALTER TABLE tool_calls
    ADD CONSTRAINT tool_calls_outcome_coherence_check
        CHECK (
            NOT (outcome = 'succeeded' AND error_message IS NOT NULL)
            AND NOT (outcome = 'failed'
                     AND (error_message IS NULL
                          OR btrim(error_message, E' \t\n\r\f\v') = ''))
            AND NOT (finished_at IS NULL AND error_message IS NOT NULL)
        );

CREATE INDEX tool_calls_state_idx        ON tool_calls (state);
CREATE INDEX tool_calls_execution_id_idx ON tool_calls (execution_id);

COMMIT;
