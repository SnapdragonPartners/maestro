# Reviewer instructions for Maestro

- Authority is set out in `CLAUDE.md` under *Project And Authority*, and
  `docs/v2/process_build.md` wins over it. For v2 work, Accepted ADRs
  (`status = "live"` in `docs/adr/`) and the live phase plan and designs in
  `docs/v2/phase_x/` bind the review. Text of an ADR, plan or design changed in
  the commit under review is under review, not binding; verify a status line
  yourself rather than taking the branch notes' word for it.
- v1 is frozen. Do not raise findings against v1 code unless the defect blocks
  the v2 work under review.
- Severity is `CLAUDE.md`'s: P0 and P1 block; everything else is a suggestion.
- `process_build.md`'s *Defect-Shaped Verification* and *Reachability Claims*
  are binding. A regression test or guard that cannot fail for the defect it
  names is a finding; so is a mutant reported as killed that died at a
  neighbouring check, and a universal claim wider than its evidence.
- Weight findings toward `CLAUDE.md`'s *Durable Engineering Invariants*: the
  Orchestrator boundary (ADR 0019), persistence only through the seam
  (ADR 0022), shared-state concurrency (ADR 0027), artifact immutability and
  the review invariant (ADRs 0020, 0021, 0028), and the exact-set closure
  guards in `internal/dataplane/store` and `internal/orchestrator`.
- In a build-capable review, run from the checkout with
  `GOCACHE=$COUNTERPOINT_CACHE_DIR/go-build GOPROXY=off`: `go build ./...`,
  `go vet ./...`, and `go test` on the packages the commit touches. Do not run
  the `integration` build tag — those suites need Docker, Postgres and MinIO —
  and report them as not run; the author runs them and records the outcome in
  the notes. golangci-lint and `make sqlc-check` may be unavailable offline;
  report them as not run and let CI cover them.
- Never run golden configurations (`golden-minimal`, `golden-all`) or anything
  that needs credentials or a paid model API.
- A finding settled by argument in an earlier round is recorded in the
  governing design's *Points Resolved In Review* or an ADR; re-raising it needs
  a new reason, not a restatement.
