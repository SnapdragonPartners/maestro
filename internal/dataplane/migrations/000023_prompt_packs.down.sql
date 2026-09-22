-- Reverse the prompt-pack family -- and REFUSE rather than discard.
--
-- The pre-000023 schema cannot hold a plane-owned pack: it has no content
-- table for a selector to name and no reference for a resolved principal to
-- carry. A reversal that dropped them would leave dangling selectors and
-- principals whose P silently reverted to a name. So this refuses when any
-- plane-owned state exists:
--
--   1. a principal with origin 'resolved';
--   2. any prompt_pack_installations row;
--   3. any dispatch_prompt_resolutions row (which, by the reciprocal key, is
--      every story_dispatches row);
--   4. any configuration_records row for the prompt.pack key (design D7);
--   5. any prompt_pack_contents row. The design names the four above; content
--      is added because DROP TABLE would discard it, and an unreferenced
--      content row is still a pack somebody imported.
--
-- With none present the foreign principals collapse back into prompt_pack_id
-- (their name; the digest bytes were never rewritten), the tables and the
-- reciprocal column go, and the three functions go with them -- a function
-- left behind is the reversal's own residue.
--
-- LOCK ORDER is the family's fixed order (see the up migration), restricted
-- to what this direction scans, alters or drops:
--
--     prompt_pack_contents
--     prompt_pack_installations
--     configuration_records
--     story_dispatches
--     dispatch_prompt_resolutions
--     principal_instances
--
-- Lock first, THEN scan: without it a prompt.pack configuration write can
-- commit between the scan and the DROP, and the reversal deletes a selector
-- it already decided did not exist.
--
-- RECOVERING FROM A REFUSAL: the recorded version is 22 and DIRTY while the
-- schema is really at 23. Force it back --
--
--     make dataplane-force-version VERSION=23 FORCE=1
--
-- -- then either remove the plane-owned state deliberately or stop reversing.
BEGIN;

LOCK TABLE prompt_pack_contents       IN ACCESS EXCLUSIVE MODE;
LOCK TABLE prompt_pack_installations  IN ACCESS EXCLUSIVE MODE;
LOCK TABLE configuration_records      IN ACCESS EXCLUSIVE MODE;
LOCK TABLE story_dispatches           IN ACCESS EXCLUSIVE MODE;
LOCK TABLE dispatch_prompt_resolutions IN ACCESS EXCLUSIVE MODE;
LOCK TABLE principal_instances        IN ACCESS EXCLUSIVE MODE;

DO $$
DECLARE
    offending bigint;
BEGIN
    SELECT count(*) INTO offending FROM principal_instances WHERE prompt_pack_origin = 'resolved';
    IF offending > 0 THEN
        RAISE EXCEPTION 'cannot reverse 000023: % principal(s) carry a resolved prompt pack, which the '
            'old shape can only record as a name. Force the version forward (make '
            'dataplane-force-version VERSION=23 FORCE=1), then remove them deliberately or stop '
            'reversing.', offending;
    END IF;

    SELECT count(*) INTO offending FROM dispatch_prompt_resolutions;
    IF offending > 0 THEN
        RAISE EXCEPTION 'cannot reverse 000023: % dispatch(es) carry a prompt-pack resolution the old '
            'schema cannot hold. Force the version forward (make dataplane-force-version '
            'VERSION=23 FORCE=1), then remove them deliberately or stop reversing.', offending;
    END IF;

    SELECT count(*) INTO offending FROM configuration_records WHERE key = 'prompt.pack';
    IF offending > 0 THEN
        RAISE EXCEPTION 'cannot reverse 000023: % prompt.pack selector(s) name content the old schema '
            'cannot hold; reversing would leave them dangling. Force the version forward (make '
            'dataplane-force-version VERSION=23 FORCE=1), then remove them deliberately or stop '
            'reversing.', offending;
    END IF;

    SELECT count(*) INTO offending FROM prompt_pack_installations;
    IF offending > 0 THEN
        RAISE EXCEPTION 'cannot reverse 000023: % prompt pack installation(s) exist and would be '
            'discarded. Force the version forward (make dataplane-force-version VERSION=23 FORCE=1), '
            'then remove them deliberately or stop reversing.', offending;
    END IF;

    SELECT count(*) INTO offending FROM prompt_pack_contents;
    IF offending > 0 THEN
        RAISE EXCEPTION 'cannot reverse 000023: % prompt pack content record(s) exist and would be '
            'discarded. Force the version forward (make dataplane-force-version VERSION=23 FORCE=1), '
            'then remove them deliberately or stop reversing.', offending;
    END IF;
END $$;

-- The split, collapsed. Only foreign and non-agent shapes survive the guard.
ALTER TABLE principal_instances ADD COLUMN prompt_pack_id text;

UPDATE principal_instances
   SET prompt_pack_id = prompt_pack_name
 WHERE prompt_pack_origin = 'foreign';

DROP INDEX principal_instances_prompt_identity_idx;

ALTER TABLE principal_instances
    DROP CONSTRAINT principal_instances_prompt_pack_installation_fkey,
    DROP CONSTRAINT principal_instances_prompt_pack_content_fkey,
    DROP CONSTRAINT principal_instances_prompt_pack_snapshot_check,
    DROP CONSTRAINT principal_instances_prompt_pack_revision_check,
    DROP CONSTRAINT principal_instances_prompt_pack_name_check,
    DROP CONSTRAINT principal_instances_prompt_pack_scheme_check,
    DROP CONSTRAINT principal_instances_prompt_pack_shape_check,
    DROP CONSTRAINT principal_instances_prompt_pack_origin_check,
    DROP COLUMN prompt_pack_metadata_snapshot,
    DROP COLUMN prompt_pack_installation_revision,
    DROP COLUMN prompt_pack_installation_id,
    DROP COLUMN prompt_pack_content_id,
    DROP COLUMN prompt_pack_scheme,
    DROP COLUMN prompt_pack_name,
    DROP COLUMN prompt_pack_origin;

-- The reciprocal reference, then the resolutions it targets, then the
-- lineage key the resolutions targeted.
ALTER TABLE story_dispatches
    DROP CONSTRAINT story_dispatches_prompt_resolution_fkey,
    DROP COLUMN prompt_resolution_id;

DROP TABLE dispatch_prompt_resolutions;

ALTER TABLE story_dispatches DROP CONSTRAINT story_dispatches_full_lineage_key;

DROP TABLE prompt_pack_installations;

DROP TRIGGER prompt_pack_contents_immutable ON prompt_pack_contents;
DROP TABLE prompt_pack_contents;

DROP FUNCTION prompt_pack_contents_refuse_update();
DROP FUNCTION prompt_pack_entries_canonical(jsonb);
DROP FUNCTION prompt_pack_roles_canonical(jsonb);

COMMIT;
