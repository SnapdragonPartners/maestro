-- The recorded absence becomes part of the ledger row
-- (docs/v2/phase_2/design_slice_import.md, D7e as amended).
--
-- Why an attempt contributed no call rows is a fact about the IMPORT, and it
-- was not being stored. The report re-read the usage log at assembly instead,
-- which is a different question with a different answer: an attempt whose
-- calls were read and whose evidence was later pruned re-reads as "no
-- evidence directory", so the report would claim its calls were unavailable
-- when they are sitting in llm_calls. The reverse is possible too -- evidence
-- restored after a pruned import reads as available for an attempt that
-- contributed nothing -- and the reconstruction also had to name filesystem
-- paths in its failure text, which is exactly what D6a keeps out of a
-- portable payload.
--
-- So the observation is written where the attempt is, in the same transaction
-- and from the same read that decided whether to write call rows at all. A
-- measurement and the reason it is absent belong to the moment of
-- measurement.
--
-- Empty means the calls WERE read. NOT NULL with an empty default, rather
-- than a nullable column, because the three-valued reading would immediately
-- need a convention for which of null and empty means "read" -- and the rows
-- that predate this column are exactly the ones whose calls were read, since
-- the importer refused an unreadable log before writing any of them.
--
-- ADR and consumer, per the plan's reserved-by-name rule: ADR 0022 (the plane
-- records what was measured, and what was not); consumed by item 9's importer
-- and its suite report.
BEGIN;

ALTER TABLE benchmark_attempts
    ADD COLUMN calls_unavailable text NOT NULL DEFAULT '';

COMMIT;
