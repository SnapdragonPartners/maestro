-- Reverse the work-hierarchy execution families.
--
-- This down migration is LOSSLESS in the sense that matters: everything it
-- drops was created by 000021, so reversing destroys the work-hierarchy
-- execution records but restores exactly the schema 000020 left. That is
-- unlike 000022's, which must refuse rather than reverse, because that one
-- projects new state onto a column that cannot hold it.
--
-- Order is forced by the dependency direction, and dropping in the wrong one
-- fails rather than silently cascading -- every foreign key here is
-- ON DELETE RESTRICT, and none of the drops below uses CASCADE. A CASCADE
-- would let a mistake in this file remove a constraint in a table 000021
-- never touched.
BEGIN;

-- The pointer columns first: they reference management_artifacts, whose keys
-- are dropped at the bottom of this file.
ALTER TABLE epics
    DROP CONSTRAINT epics_governing_fkey,
    DROP CONSTRAINT epics_governing_original_check,
    DROP COLUMN governing_is_amendment,
    DROP COLUMN governing_artifact_id;

ALTER TABLE stories
    DROP CONSTRAINT stories_governing_fkey,
    DROP CONSTRAINT stories_governing_original_check,
    DROP COLUMN governing_is_amendment,
    DROP COLUMN governing_artifact_id;

-- Tables, most-dependent first.
--
-- dispatch_basis_dependencies references story_dispatches and stories;
-- executions references story_dispatches; story_dispatches references
-- work_groups. Nothing references the two dependency graphs.
DROP TABLE dispatch_basis_dependencies;
DROP TABLE executions;
DROP TABLE story_dispatches;
DROP TABLE work_groups;
DROP TABLE story_dependencies;
DROP TABLE epic_dependencies;

-- Last: the referencable keys, now that every referring constraint is gone.
-- Dropping these while a reference survived would fail, which is the correct
-- outcome and the reason they are last rather than first.
ALTER TABLE management_artifacts
    DROP CONSTRAINT management_artifacts_epic_scope_key,
    DROP CONSTRAINT management_artifacts_story_scope_key;

COMMIT;
