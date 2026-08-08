-- Restore the referencing constraint, discarding the intents it cannot hold.
--
-- A claim naming an artifact that does not exist yet is VALID under the
-- forward protocol -- it is the whole reason the constraint was dropped, and
-- the state a caller is in between reserving an identifier and writing under
-- it. So the constraint cannot simply be re-added: reversing would fail
-- against a plane in a state the forward migration deliberately permits,
-- which is a down migration that only works on planes that never used the
-- feature.
--
-- The pending intents are therefore discarded first, and discarding is the
-- honest reversal. Reverting means asserting that a claim always names an
-- existing artifact; an intent that names nothing cannot be expressed under
-- that assertion, and there is nothing else to turn it into. What is lost is
-- a reservation, not a report: the suite has no report either way, and the
-- next import reserves a fresh identifier.
BEGIN;

DELETE FROM benchmark_reports r
WHERE NOT EXISTS (
    SELECT 1 FROM management_artifacts a
    WHERE a.artifact_id      = r.report_artifact_id
      AND a.organization_id  = r.organization_id);

ALTER TABLE benchmark_reports
    ADD CONSTRAINT benchmark_reports_artifact_fkey
        FOREIGN KEY (report_artifact_id, organization_id)
        REFERENCES management_artifacts (artifact_id, organization_id) ON DELETE RESTRICT;

COMMIT;
