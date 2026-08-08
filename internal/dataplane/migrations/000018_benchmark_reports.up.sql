-- The suite-report claim: which Management artifact IS a suite run's report
-- (docs/v2/phase_2/design_slice_import.md, D5 and D7, as amended by D7a).
--
-- It exists because "at most one report per suite" was a rule with nothing
-- enforcing it. Assembly reads the scope for an existing report and writes
-- one if it finds none, and those are two statements: two imports of the
-- same terminal suite can both read nothing and both write, leaving two
-- draft reports for one suite. Both would then be independently acceptable,
-- and the plane would hold two authoritative accounts of one conformance
-- run — the state SupersedeArtifact exists to prevent for every other
-- subject.
--
-- ADR 0027's rule is that shared state is serialized on a key matching the
-- resource, and never left to last-writer-wins. The key here is the suite,
-- so the constraint is one row per (organization, benchmark run) and the
-- UNIQUENESS is the arbiter — the same shape as the attempt ledger, for the
-- same reason.
--
-- A separate table rather than a column on benchmark_runs, because that
-- table is BORN FINAL: item 9's own structural test refuses any statement
-- that would update it, on the argument that re-import must be a no-op
-- rather than a write. This table is born final too. Nothing here is ever
-- updated; a claim is made once and read thereafter.
--
-- ADR and consumer, per the plan's reserved-by-name rule: ADR 0027
-- (shared-state concurrency) and ADR 0020 (the review invariant, which is
-- what makes a second authoritative report a defect rather than clutter);
-- consumed by item 9's report assembly.
BEGIN;

CREATE TABLE benchmark_reports (
    benchmark_report_id uuid        PRIMARY KEY,
    organization_id     uuid        NOT NULL REFERENCES organizations (organization_id) ON DELETE RESTRICT,
    benchmark_run_id    uuid        NOT NULL,

    -- The Management artifact that IS this suite's report. Its own scope
    -- points back at the same run; this row is what makes ONE of them the
    -- report rather than one of possibly several artifacts that qualify.
    report_artifact_id  uuid        NOT NULL,

    claimed_at          timestamptz NOT NULL DEFAULT now(),

    -- One report per suite. The whole point of the table.
    CONSTRAINT benchmark_reports_run_key UNIQUE (organization_id, benchmark_run_id),

    -- And one suite per report, so a single artifact cannot be claimed as
    -- the report of two runs. Cheap, and it closes the mirror image of the
    -- rule above rather than leaving it to the caller.
    CONSTRAINT benchmark_reports_artifact_key UNIQUE (organization_id, report_artifact_id),

    CONSTRAINT benchmark_reports_run_fkey
        FOREIGN KEY (benchmark_run_id, organization_id)
        REFERENCES benchmark_runs (benchmark_run_id, organization_id) ON DELETE RESTRICT,

    -- The organization-aware form, so a claim cannot name another tenant's
    -- artifact.
    CONSTRAINT benchmark_reports_artifact_fkey
        FOREIGN KEY (report_artifact_id, organization_id)
        REFERENCES management_artifacts (artifact_id, organization_id) ON DELETE RESTRICT
);

COMMIT;
