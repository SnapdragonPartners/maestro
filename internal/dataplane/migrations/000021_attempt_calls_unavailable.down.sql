-- Drop the recorded absence.
--
-- Reversible in shape and lossy in fact: the reason an attempt's calls were
-- unavailable exists nowhere else, and a report assembled afterwards can only
-- reconstruct it from a store that may have changed. That loss is the point
-- of the forward migration.
BEGIN;

ALTER TABLE benchmark_attempts DROP COLUMN calls_unavailable;

COMMIT;
