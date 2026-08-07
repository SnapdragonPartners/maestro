-- Restore the referencing constraint.
--
-- It will fail against a plane holding a claim whose artifact was never
-- written -- the very state the forward migration exists to make survivable.
-- That is the honest reversal: the constraint and the protocol cannot both
-- be true, and reverting means asserting the artifact always exists first.
BEGIN;

ALTER TABLE benchmark_reports
    ADD CONSTRAINT benchmark_reports_artifact_fkey
        FOREIGN KEY (report_artifact_id, organization_id)
        REFERENCES management_artifacts (artifact_id, organization_id) ON DELETE RESTRICT;

COMMIT;
