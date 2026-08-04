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

// identifierPattern is the shape a slug, handle or suite id must take.
//
// Enforced in Go so an operator's mistake is reported in the vocabulary of
// the flag they typed rather than as a constraint name, and enforced again in
// the schema so a caller that skips this path cannot write a value the rest
// of the system assumes is safe. These become URL and filename components
// later, which is why the rule is narrow now rather than after something has
// depended on it.
//
//nolint:gochecknoglobals // Package-level compiled regex for performance.
var identifierPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// checkIdentifier validates a natural key before any statement is issued.
func checkIdentifier(kind, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is blank", kind)
	}
	if !identifierPattern.MatchString(value) {
		return fmt.Errorf("%s %q must match %s: it becomes a URL and filename component",
			kind, value, identifierPattern)
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

func organizationFromRow(row *gen.Organization) store.Organization {
	return store.Organization{
		CreatedAt:      fromTimestamptz(row.CreatedAt),
		Slug:           row.Slug,
		DisplayName:    row.DisplayName,
		OrganizationID: fromUUID(row.OrganizationID),
	}
}

func userFromRow(row *gen.User) store.User {
	return store.User{
		CreatedAt:      fromTimestamptz(row.CreatedAt),
		Handle:         row.Handle,
		DisplayName:    row.DisplayName,
		UserID:         fromUUID(row.UserID),
		OrganizationID: fromUUID(row.OrganizationID),
	}
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
		BenchmarkAttemptID: fromUUID(row.BenchmarkAttemptID),
		OrganizationID:     fromUUID(row.OrganizationID),
		BenchmarkRunID:     fromUUID(row.BenchmarkRunID),
		AuditArtifactID:    fromUUID(row.AuditArtifactID),
	}
}

// GetOrganizationBySlug resolves a tenant by its slug.
func (t *tx) GetOrganizationBySlug(ctx context.Context, slug string) (*store.Organization, error) {
	row, err := t.queries.GetOrganizationBySlug(ctx, slug)
	if err != nil {
		return nil, notFoundByName(err, "organization", slug)
	}
	organization := organizationFromRow(&row)
	return &organization, nil
}

// GetUserByHandle resolves an accountable human within one tenant.
func (t *tx) GetUserByHandle(ctx context.Context, organizationID uuid.UUID, handle string) (*store.User, error) {
	row, err := t.queries.GetUserByHandle(ctx, gen.GetUserByHandleParams{
		OrganizationID: toUUID(organizationID),
		Handle:         handle,
	})
	if err != nil {
		return nil, notFoundByName(err, "user", handle)
	}
	user := userFromRow(&row)
	return &user, nil
}

// BootstrapOrganization provisions a tenant, idempotently.
//
// Insert-or-nothing THEN read, never check-then-insert: two operators running
// this at once would both observe no row, both insert, and one would receive
// a raw uniqueness violation — an outcome that is neither "created" nor
// "already existed" and that leaks a driver error through the seam. Here the
// unique constraint decides who wins and the read that follows is what both
// callers compare against, so they converge (ADR 0027: serialize on a key
// matching the resource, never last-writer-wins).
func (t *tx) BootstrapOrganization(ctx context.Context, input store.BootstrapOrganizationInput) (store.Bootstrapped[store.Organization], error) {
	var empty store.Bootstrapped[store.Organization]
	if err := checkIdentifier("organization slug", input.Slug); err != nil {
		return empty, err
	}
	if err := checkDisplayName("organization", input.DisplayName); err != nil {
		return empty, err
	}
	identifier, err := newIdentifier(uuid.Nil)
	if err != nil {
		return empty, err
	}
	inserted, err := t.queries.InsertOrganizationIfAbsent(ctx, gen.InsertOrganizationIfAbsentParams{
		OrganizationID: toUUID(identifier),
		Slug:           input.Slug,
		DisplayName:    input.DisplayName,
	})
	if err != nil {
		return empty, fmt.Errorf("insert organization %q: %w", input.Slug, err)
	}
	stored, err := t.GetOrganizationBySlug(ctx, input.Slug)
	if err != nil {
		return empty, err
	}
	// Compared against the STORED row rather than against our own insert:
	// the row that is there may be the other racer's, and it is that one the
	// caller must be told about.
	if stored.DisplayName != input.DisplayName {
		return empty, &store.BootstrapConflict{
			Kind: "organization", Key: input.Slug,
			Stored: stored.DisplayName, Supplied: input.DisplayName,
		}
	}
	return store.Bootstrapped[store.Organization]{Record: *stored, Created: inserted == 1}, nil
}

// BootstrapUser provisions an accountable human, idempotently. Same shape and
// same reasoning as BootstrapOrganization.
func (t *tx) BootstrapUser(ctx context.Context, input store.BootstrapUserInput) (store.Bootstrapped[store.User], error) {
	var empty store.Bootstrapped[store.User]
	if err := checkIdentifier("user handle", input.Handle); err != nil {
		return empty, err
	}
	if err := checkDisplayName("user", input.DisplayName); err != nil {
		return empty, err
	}
	identifier, err := newIdentifier(uuid.Nil)
	if err != nil {
		return empty, err
	}
	inserted, err := t.queries.InsertUserIfAbsent(ctx, gen.InsertUserIfAbsentParams{
		UserID:         toUUID(identifier),
		OrganizationID: toUUID(input.OrganizationID),
		Handle:         input.Handle,
		DisplayName:    input.DisplayName,
	})
	if err != nil {
		return empty, fmt.Errorf("insert user %q: %w", input.Handle, err)
	}
	stored, err := t.GetUserByHandle(ctx, input.OrganizationID, input.Handle)
	if err != nil {
		return empty, err
	}
	if stored.DisplayName != input.DisplayName {
		return empty, &store.BootstrapConflict{
			Kind: "user", Key: input.Handle,
			Stored: stored.DisplayName, Supplied: input.DisplayName,
		}
	}
	return store.Bootstrapped[store.User]{Record: *stored, Created: inserted == 1}, nil
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
	if err := checkIdentifier("run id", input.RunID); err != nil {
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
