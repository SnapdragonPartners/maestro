-- Restore the restricting reference.
--
-- Reversible only in shape. A plane that has truncated an imported record
-- under CASCADE has already lost the ledger row that named it, and putting
-- the constraint back does not bring either one back -- which is the honest
-- reversal, since retention removing an unpinned record is the behaviour
-- this migration exists to allow.
BEGIN;

ALTER TABLE benchmark_attempts
    DROP CONSTRAINT benchmark_attempts_artifact_fkey;

ALTER TABLE benchmark_attempts
    ADD CONSTRAINT benchmark_attempts_artifact_fkey
        FOREIGN KEY (audit_artifact_id, organization_id)
        REFERENCES audit_artifacts (artifact_id, organization_id) ON DELETE RESTRICT;

COMMIT;
