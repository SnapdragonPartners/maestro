-- Reverses the call-family read indexes.
--
-- Losslessly reversible, unlike a column drop: an index carries no data of
-- its own, so this removes only the ability to serve those reads quickly.
BEGIN;

DROP INDEX IF EXISTS audit_artifacts_org_created_idx;
DROP INDEX IF EXISTS tool_calls_org_open_started_idx;
DROP INDEX IF EXISTS llm_calls_org_open_started_idx;
DROP INDEX IF EXISTS tool_calls_org_finished_idx;
DROP INDEX IF EXISTS llm_calls_org_finished_idx;
DROP INDEX IF EXISTS audit_events_org_time_idx;
DROP INDEX IF EXISTS metric_events_org_time_idx;
DROP INDEX IF EXISTS llm_calls_org_cohort_time_idx;
DROP INDEX IF EXISTS tool_calls_org_time_idx;
DROP INDEX IF EXISTS llm_calls_org_time_idx;
DROP INDEX IF EXISTS tool_calls_org_principal_time_idx;
DROP INDEX IF EXISTS llm_calls_org_principal_time_idx;
DROP INDEX IF EXISTS tool_calls_org_story_time_idx;
DROP INDEX IF EXISTS llm_calls_org_story_time_idx;

COMMIT;
