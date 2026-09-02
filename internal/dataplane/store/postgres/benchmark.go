package postgres

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"orchestrator/internal/dataplane/gen"
	"orchestrator/internal/dataplane/store"
)

// Two patterns, because they answer to two different accepted contracts and
// collapsing them into one made this layer reject values the schema accepts.
//
//nolint:gochecknoglobals // Package-level compiled regexes for performance.
var (
	// identifierPattern governs organization slugs, user handles and suite
	// run ids. It is the runner's own suite-id rule verbatim
	// (benchmark/results.validSuiteRunID) and what design D10 adopted for
	// slugs and handles, so a suite id the runner produced is a suite id this
	// accepts. An earlier version required an alphanumeric first character
	// and therefore refused valid suite ids beginning with `_` or `-` —
	// values migration 000017's own CHECK admits.
	//
	// Lowercase only, because these become filename and URL components and a
	// case-insensitive filesystem would collide two that differ only by case.
	identifierPattern = regexp.MustCompile(`^[a-z0-9_-]+$`)

	// runIDPattern is design D8's accepted shape for an attempt identity,
	// and matches migration 000017's benchmark_attempts_run_id_check exactly.
	// It is stricter than identifierPattern by one character class: a run id
	// must begin with an alphanumeric.
	//
	// Path escape is not what the extra restriction buys — neither pattern
	// admits `.` or a separator, so `..` and `../x` cannot be spelled by
	// either. D8 fixed this shape and the schema enforces it; that is the
	// authority, and inventing a further rationale for it would be a claim
	// about a caller that does not exist.
	runIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
)

// Where each rule is enforced, stated precisely because it differs:
//
//   - suite run id and run id: HERE and in the schema (migration 000017's
//     CHECK constraints), so a caller bypassing this path still cannot write
//     a value the importer's path resolution assumes is safe.
//   - organization slug and user handle: HERE ONLY. Those tables carry no
//     format CHECK, so this layer is the whole of the rule for them.
//
// Validated in Go regardless so an operator's mistake is reported in the
// vocabulary of the flag they typed rather than as a constraint name.

// checkIdentifier validates a slug, handle or suite id before any statement.
func checkIdentifier(kind, value string) error {
	return checkAgainst(kind, value, identifierPattern)
}

// checkRunID validates an attempt identity, which is additionally used as a
// path component and a command-line argument.
func checkRunID(value string) error {
	return checkAgainst("run id", value, runIDPattern)
}

func checkAgainst(kind, value string, pattern *regexp.Regexp) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is blank", kind)
	}
	if !pattern.MatchString(value) {
		return fmt.Errorf("%s %q must match %s", kind, value, pattern)
	}
	return nil
}

// checkDisplayName refuses a blank display name before SQL sees it.
func checkDisplayName(kind, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s display name is blank", kind)
	}
	return nil
}

func benchmarkRunFromRow(row *gen.BenchmarkRun) store.BenchmarkRun {
	return store.BenchmarkRun{
		FirstImportedAt: fromTimestamptz(row.FirstImportedAt),
		SuiteRunID:      row.SuiteRunID,
		BenchmarkRunID:  fromUUID(row.BenchmarkRunID),
		OrganizationID:  fromUUID(row.OrganizationID),
	}
}

func benchmarkAttemptFromRow(row *gen.BenchmarkAttempt) store.BenchmarkAttempt {
	return store.BenchmarkAttempt{
		ImportedAt:         fromTimestamptz(row.ImportedAt),
		RunID:              row.RunID,
		RecordDigest:       row.RecordDigest,
		CallsUnavailable:   row.CallsUnavailable,
		BenchmarkAttemptID: fromUUID(row.BenchmarkAttemptID),
		OrganizationID:     fromUUID(row.OrganizationID),
		BenchmarkRunID:     fromUUID(row.BenchmarkRunID),
		AuditArtifactID:    fromUUID(row.AuditArtifactID),
	}
}

// EnsureBenchmarkRun returns the suite's row, creating it if absent.
func (t *tx) EnsureBenchmarkRun(ctx context.Context, organizationID uuid.UUID, suiteRunID string) (store.Bootstrapped[store.BenchmarkRun], error) {
	var empty store.Bootstrapped[store.BenchmarkRun]
	if err := checkIdentifier("suite run id", suiteRunID); err != nil {
		return empty, err
	}
	identifier, err := newIdentifier(uuid.Nil)
	if err != nil {
		return empty, err
	}
	inserted, err := t.queries.InsertBenchmarkRunIfAbsent(ctx, gen.InsertBenchmarkRunIfAbsentParams{
		BenchmarkRunID: toUUID(identifier),
		OrganizationID: toUUID(organizationID),
		SuiteRunID:     suiteRunID,
	})
	if err != nil {
		return empty, fmt.Errorf("insert benchmark run %q: %w", suiteRunID, err)
	}
	stored, err := t.GetBenchmarkRunBySuite(ctx, organizationID, suiteRunID)
	if err != nil {
		return empty, err
	}
	// No display data to disagree about: the row carries only its identity
	// and when it was first seen, which is what makes re-import a read.
	return store.Bootstrapped[store.BenchmarkRun]{Record: *stored, Created: inserted == 1}, nil
}

// GetBenchmarkRunBySuite resolves a suite run by the runner's own identity.
func (t *tx) GetBenchmarkRunBySuite(ctx context.Context, organizationID uuid.UUID, suiteRunID string) (*store.BenchmarkRun, error) {
	row, err := t.queries.GetBenchmarkRunBySuite(ctx, gen.GetBenchmarkRunBySuiteParams{
		OrganizationID: toUUID(organizationID),
		SuiteRunID:     suiteRunID,
	})
	if err != nil {
		return nil, notFoundByName(err, "benchmark run", suiteRunID)
	}
	run := benchmarkRunFromRow(&row)
	return &run, nil
}

// GetBenchmarkAttempt resolves one ledgered attempt.
func (t *tx) GetBenchmarkAttempt(ctx context.Context, organizationID, benchmarkRunID uuid.UUID, runID string) (*store.BenchmarkAttempt, error) {
	row, err := t.queries.GetBenchmarkAttempt(ctx, gen.GetBenchmarkAttemptParams{
		OrganizationID: toUUID(organizationID),
		BenchmarkRunID: toUUID(benchmarkRunID),
		RunID:          runID,
	})
	if err != nil {
		return nil, notFoundByName(err, "benchmark attempt", runID)
	}
	attempt := benchmarkAttemptFromRow(&row)
	return &attempt, nil
}

// ListBenchmarkAttempts returns every ledgered attempt of one suite run.
//
// Unbounded, unlike the call-family reads: this table holds one row per
// benchmark attempt, and a suite is tens of rows rather than the millions
// that made paging mandatory there.
func (t *tx) ListBenchmarkAttempts(ctx context.Context, organizationID, benchmarkRunID uuid.UUID) ([]store.BenchmarkAttempt, error) {
	rows, err := t.queries.ListBenchmarkAttempts(ctx, gen.ListBenchmarkAttemptsParams{
		OrganizationID: toUUID(organizationID),
		BenchmarkRunID: toUUID(benchmarkRunID),
	})
	if err != nil {
		return nil, fmt.Errorf("list benchmark attempts: %w", err)
	}
	attempts := make([]store.BenchmarkAttempt, 0, len(rows))
	for i := range rows {
		attempts = append(attempts, benchmarkAttemptFromRow(&rows[i]))
	}
	return attempts, nil
}

// RecordBenchmarkAttempt ledgers an attempt, or reports what is already
// there.
//
// The digest comparison happens in Go against the stored row, not in the
// statement: the caller needs to know WHICH digest disagreed, and an ON
// CONFLICT clause cannot say. A conflict writes nothing.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface (see artifacts.go)
func (t *tx) RecordBenchmarkAttempt(ctx context.Context, input store.RecordBenchmarkAttemptInput) (store.Bootstrapped[store.BenchmarkAttempt], error) {
	var empty store.Bootstrapped[store.BenchmarkAttempt]
	if err := checkRunID(input.RunID); err != nil {
		return empty, err
	}
	if !digestPattern.MatchString(input.RecordDigest) {
		return empty, fmt.Errorf("record digest %q is not a 64-hex digest", input.RecordDigest)
	}
	identifier, err := newIdentifier(uuid.Nil)
	if err != nil {
		return empty, err
	}
	inserted, err := t.queries.InsertBenchmarkAttemptIfAbsent(ctx, gen.InsertBenchmarkAttemptIfAbsentParams{
		BenchmarkAttemptID: toUUID(identifier),
		OrganizationID:     toUUID(input.OrganizationID),
		BenchmarkRunID:     toUUID(input.BenchmarkRunID),
		RunID:              input.RunID,
		RecordDigest:       input.RecordDigest,
		AuditArtifactID:    toUUID(input.AuditArtifactID),
		CallsUnavailable:   input.CallsUnavailable,
	})
	if err != nil {
		return empty, fmt.Errorf("insert benchmark attempt %q: %w", input.RunID, err)
	}
	stored, err := t.GetBenchmarkAttempt(ctx, input.OrganizationID, input.BenchmarkRunID, input.RunID)
	if err != nil {
		return empty, err
	}
	if stored.RecordDigest != input.RecordDigest {
		run, runErr := t.getRunForConflict(ctx, &input)
		if runErr != nil {
			return empty, runErr
		}
		return empty, &store.ImportConflict{
			SuiteRunID: run, RunID: input.RunID,
			StoredDigest: stored.RecordDigest, OfferedDigest: input.RecordDigest,
		}
	}
	return store.Bootstrapped[store.BenchmarkAttempt]{Record: *stored, Created: inserted == 1}, nil
}

// getRunForConflict names the suite in a conflict message. The suite id is
// what an operator greps for; the run's uuid is not something they have.
func (t *tx) getRunForConflict(ctx context.Context, input *store.RecordBenchmarkAttemptInput) (string, error) {
	row, err := t.queries.GetBenchmarkRun(ctx, gen.GetBenchmarkRunParams{
		BenchmarkRunID: toUUID(input.BenchmarkRunID),
		OrganizationID: toUUID(input.OrganizationID),
	})
	if err != nil {
		return "", fmt.Errorf("read benchmark run for conflict report: %w", err)
	}
	return row.SuiteRunID, nil
}

// notFoundByName wraps a missing row, keeping ErrNotFound matchable while
// naming what was looked for.
func notFoundByName(err error, kind, name string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %s %q", store.ErrNotFound, kind, name)
	}
	return fmt.Errorf("read %s %q: %w", kind, name, err)
}

// GetSuiteReport returns which artifact is the suite's report.
func (t *tx) GetSuiteReport(ctx context.Context, organizationID, benchmarkRunID uuid.UUID) (*store.SuiteReportClaim, error) {
	row, err := t.queries.GetBenchmarkReport(ctx, gen.GetBenchmarkReportParams{
		OrganizationID: toUUID(organizationID),
		BenchmarkRunID: toUUID(benchmarkRunID),
	})
	if err != nil {
		return nil, notFound(err, "suite report", benchmarkRunID)
	}
	claim := suiteReportClaimFromRow(&row)
	return &claim, nil
}

// ClaimSuiteReport records which artifact is a suite's report.
//
// Insert-or-nothing, then read, exactly as the attempt ledger does: the
// uniqueness on (organization, run) is the arbiter, so two importers racing
// to report one terminal suite converge on one row instead of one of them
// receiving a violation this seam would have to decode.
//
// The loser is told through Created=false and handed the WINNER's claim,
// because "you lost" without saying to whom leaves the caller unable to
// report what actually happened to the suite.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (t *tx) ClaimSuiteReport(
	ctx context.Context, organizationID, benchmarkRunID uuid.UUID,
) (store.Bootstrapped[store.SuiteReportClaim], error) {
	var empty store.Bootstrapped[store.SuiteReportClaim]
	identifier, err := newIdentifier(uuid.Nil)
	if err != nil {
		return empty, err
	}
	// The ARTIFACT's identifier is allocated here too, and that is the point
	// of the seam owning it. A caller-supplied id is a caller-supplied
	// invariant: the nil UUID is read by artifact creation as "allocate one
	// for me", which would produce an artifact the claim does not name and
	// let a retry create a second; a v4 is refused by artifact creation
	// outright, which strands the claim permanently on an id nothing can
	// ever be written under. Neither is a validation gap so much as a
	// question the caller should never have been asked.
	artifactID, err := newIdentifier(uuid.Nil)
	if err != nil {
		return empty, err
	}
	inserted, err := t.queries.InsertBenchmarkReportIfAbsent(ctx, gen.InsertBenchmarkReportIfAbsentParams{
		BenchmarkReportID: toUUID(identifier),
		OrganizationID:    toUUID(organizationID),
		BenchmarkRunID:    toUUID(benchmarkRunID),
		ReportArtifactID:  toUUID(artifactID),
	})
	if err != nil {
		return empty, fmt.Errorf("claim the report of benchmark run %s: %w", benchmarkRunID, err)
	}
	stored, err := t.GetSuiteReport(ctx, organizationID, benchmarkRunID)
	if err != nil {
		return empty, err
	}
	return store.Bootstrapped[store.SuiteReportClaim]{Record: *stored, Created: inserted == 1}, nil
}

// suiteReportClaimFromRow converts the generated row.
func suiteReportClaimFromRow(row *gen.BenchmarkReport) store.SuiteReportClaim {
	return store.SuiteReportClaim{
		ClaimedAt:        row.ClaimedAt.Time,
		ClaimID:          fromUUID(row.BenchmarkReportID),
		OrganizationID:   fromUUID(row.OrganizationID),
		BenchmarkRunID:   fromUUID(row.BenchmarkRunID),
		ReportArtifactID: fromUUID(row.ReportArtifactID),
	}
}
