-- Benchmark runs and attempts: the vertical slice's scope target and its
-- import ledger (design_slice_import.md, D2 and D6).
--
-- Both tables are BORN FINAL. A benchmark run carries nothing a later import
-- would change, and an attempt's ledger row is the record that the import
-- happened -- rewriting either would make a re-import a write, which is
-- exactly what "append-only and idempotent" forbids. There is no update
-- statement here and the structural test refuses one.

-- Insert-or-nothing, then read. The suite identity is unique, so two
-- importers racing on the same suite converge on one row instead of one of
-- them receiving a uniqueness violation the seam would have to decode.
-- name: InsertBenchmarkRunIfAbsent :execrows
INSERT INTO benchmark_runs (benchmark_run_id, organization_id, suite_run_id)
VALUES (@benchmark_run_id, @organization_id, @suite_run_id)
ON CONFLICT ON CONSTRAINT benchmark_runs_org_suite_key DO NOTHING;

-- name: GetBenchmarkRunBySuite :one
SELECT * FROM benchmark_runs
WHERE organization_id = @organization_id
  AND suite_run_id    = @suite_run_id;

-- name: GetBenchmarkRun :one
SELECT * FROM benchmark_runs
WHERE benchmark_run_id = @benchmark_run_id
  AND organization_id  = @organization_id;

-- Attempts insert the same way, for the same reason. The digest comparison
-- that decides no-op versus conflict happens in Go against the row this
-- returns, never in the statement: a caller needs to know WHICH digest
-- disagreed, and an ON CONFLICT clause cannot say.
-- name: InsertBenchmarkAttemptIfAbsent :execrows
INSERT INTO benchmark_attempts (benchmark_attempt_id, organization_id, benchmark_run_id,
                                run_id, record_digest, audit_artifact_id)
VALUES (@benchmark_attempt_id, @organization_id, @benchmark_run_id,
        @run_id, @record_digest, @audit_artifact_id)
ON CONFLICT ON CONSTRAINT benchmark_attempts_identity_key DO NOTHING;

-- name: GetBenchmarkAttempt :one
SELECT * FROM benchmark_attempts
WHERE organization_id  = @organization_id
  AND benchmark_run_id = @benchmark_run_id
  AND run_id           = @run_id;

-- name: ListBenchmarkAttempts :many
SELECT * FROM benchmark_attempts
WHERE organization_id  = @organization_id
  AND benchmark_run_id = @benchmark_run_id
ORDER BY run_id;
