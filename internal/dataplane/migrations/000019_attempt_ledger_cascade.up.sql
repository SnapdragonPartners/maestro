-- The attempt ledger follows the record it names
-- (docs/v2/phase_2/design_slice_import.md, D7c as amended).
--
-- `benchmark_attempts.audit_artifact_id` referenced `audit_artifacts` ON
-- DELETE RESTRICT, and item 9's first attempt at the resulting truncation
-- abort added a predicate excluding those rows from the pass. That fixed the
-- abort by making every imported run record PERMANENT: nothing ever deletes a
-- ledger row, so nothing could ever delete the artifact it named. Releasing a
-- report's pins -- by invalidating or archiving it -- released the pins and
-- not the records, and Audit retention no longer applied to any record that
-- had ever been imported.
--
-- The ledger row is a claim that an attempt was imported. When retention
-- removes the artifact, the claim is about a row that no longer exists, and
-- keeping it would make the next import a no-op that never restores what was
-- pruned -- a tombstone asserting the presence of something gone.
--
-- So the reference CASCADES. Truncation deletes an unpinned record past the
-- horizon and takes its ledger row with it, and a later import recreates
-- both. A record a report pins is excluded from the pass before any of this
-- applies, so a reported suite is unaffected: pinning is what makes retention
-- skip it, exactly as ADR 0021 intends.
--
-- retention_pins keeps ON DELETE RESTRICT, and that asymmetry is deliberate.
-- A pin is a live retention CLAIM by an artifact that cites the evidence, and
-- silently dropping it would delete the thing a Management artifact says it
-- holds. The truncation predicate is what keeps that restriction from ever
-- being reached. A ledger row is bookkeeping about an import, and follows
-- what it describes.
--
-- ADR and consumer, per the plan's reserved-by-name rule: ADR 0021 (Audit
-- artifacts are truncatable unless pinned); consumed by item 9's importer and
-- by item 5's truncation pass.
BEGIN;

ALTER TABLE benchmark_attempts
    DROP CONSTRAINT benchmark_attempts_artifact_fkey;

ALTER TABLE benchmark_attempts
    ADD CONSTRAINT benchmark_attempts_artifact_fkey
        FOREIGN KEY (audit_artifact_id, organization_id)
        REFERENCES audit_artifacts (artifact_id, organization_id) ON DELETE CASCADE;

COMMIT;
