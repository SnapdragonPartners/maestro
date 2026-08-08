-- Reverse the suite-report claim.
--
-- Dropping the table loses which artifact was a suite's report, and that is
-- the honest reversal: the claim IS the fact, and the artifacts it named
-- remain, scoped to their runs, exactly as they were before the rule
-- existed.
BEGIN;

DROP TABLE benchmark_reports;

COMMIT;
