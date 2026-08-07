-- The report claim becomes a DURABLE INTENT, recorded before the artifact it
-- names exists (docs/v2/phase_2/design_slice_import.md, D7b as amended).
--
-- The claim was written AFTER AttachEvidence committed, because a foreign key
-- required the artifact to exist first. That left a window with no owner: a
-- process death between the two commits leaves a live, fully pinned draft
-- that no claim names, and the retry writes and claims a second one. Both are
-- drafts of the same suite, both independently acceptable, and the reviewer
-- of the second has no way to know the first exists.
--
-- Reversing the order closes the window instead of narrowing it. The importer
-- preallocates the artifact's identifier, records the claim, and only then
-- writes the artifact under that exact id. The one inconsistent state that
-- remains -- a claim naming an artifact that does not exist yet -- is
-- SELF-HEALING rather than ambiguous: the next import finds the claim, sees
-- no artifact, and writes it under the id already claimed. There is never a
-- second artifact to choose between.
--
-- The cost is the foreign key, which cannot survive an intent recorded before
-- its referent. This is the same shape the object module already uses for
-- destructive work: ADR 0022's deletion claim is a durable intent naming
-- storage that may already be gone, precisely because a crash between
-- intending and acting must leave a record of the intention. Referential
-- integrity moves to the seam, which refuses a claimed artifact that is not a
-- benchmark.suite_report scoped to this run -- a check the foreign key could
-- not make either, since it constrained only the tenant.
--
-- The uniqueness constraints stay. They are what make the claim an arbiter:
-- one report per suite, and one suite per report.
--
-- ADR and consumer, per the plan's reserved-by-name rule: ADR 0027
-- (shared-state concurrency; recovery must not depend on a process surviving)
-- and ADR 0020 (why a second acceptable report is a defect); consumed by item
-- 9's report assembly.
BEGIN;

ALTER TABLE benchmark_reports
    DROP CONSTRAINT benchmark_reports_artifact_fkey;

COMMIT;
