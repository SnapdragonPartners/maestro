package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"orchestrator/internal/dataplane/gen"
	"orchestrator/internal/dataplane/store"
)

// serializationFailure is SQLSTATE 40001, "could not serialize access".
//
// At REPEATABLE READ Postgres raises it when a row this transaction wants
// to delete was changed by a transaction that committed after its snapshot.
// Under concurrent truncation that is a NORMAL outcome, not a defect: both
// passes are correct and one has to start over.
const serializationFailure = "40001"

// truncationAttempts bounds whole-operation retry.
//
// Whole-operation rather than per-statement, because the dependency order
// and the retention guards are only meaningful evaluated against one
// snapshot -- and a transaction that has raised 40001 cannot execute
// anything further anyway. The bound exists so a pathological loop surfaces
// as a typed error instead of spinning.
const truncationAttempts = 3

// TruncateAuditBefore removes Audit history older than the horizon.
//
// This is the entry point that owns isolation: it opens its own REPEATABLE
// READ transaction so all five tables are judged against one snapshot, and
// retries the whole pass on a serialization failure.
func (s *Store) TruncateAuditBefore(ctx context.Context, organizationID uuid.UUID, before time.Time) (store.TruncationResult, error) {
	result, err := retryOnSerializationFailure(truncationAttempts, func() (store.TruncationResult, error) {
		return s.truncateOnce(ctx, organizationID, before)
	})
	if err != nil {
		return store.TruncationResult{}, fmt.Errorf("truncate audit history for organization %s: %w",
			organizationID, err)
	}
	return result, nil
}

// retryOnSerializationFailure re-runs an operation that lost a snapshot
// race, to a bound, and translates exhaustion into the seam's typed error.
//
// Two conditions qualify, and they are different failures with the same
// remedy: a lost snapshot (40001), and the restrict violation a pin created
// concurrently with the pass raises on the attachment key (23001). Both are
// resolved by the next attempt reading a snapshot that sees the pin.
//
// Separated from the operation itself so the retry contract can be tested
// without provoking a real 40001: exhaustion and the error mapping are
// decidable from a stub, while the live path proves only that a conflict
// arises and is survived. Testing both through the database would mean the
// bound was never exercised at all.
//
// Any other error is returned immediately. A retry loop that swallowed the
// distinction would turn a constraint violation into three identical
// failures and then a misleading concurrency error.
func retryOnSerializationFailure[T any](attempts int, run func() (T, error)) (T, error) {
	var (
		result T
		last   error
	)
	for attempt := 1; attempt <= attempts; attempt++ {
		result, last = run()
		if last == nil {
			return result, nil
		}
		if !isSerializationFailure(last) && !isRetriablePinRestriction(last) {
			var zero T
			return zero, last
		}
	}
	var zero T
	return zero, fmt.Errorf("%w after %d attempts: %w", store.ErrConcurrentTruncation, attempts, last)
}

// truncateOnce runs one pass in its own REPEATABLE READ transaction.
//
// It does not go through WithTx, which begins at the pool's default
// isolation. That difference is the whole point of design D7's correction:
// at READ COMMITTED every statement takes a fresh snapshot, so the five
// guards would be evaluated against five different instants and a row
// protected when the pass began could still be deleted.
func (s *Store) truncateOnce(ctx context.Context, organizationID uuid.UUID, before time.Time) (store.TruncationResult, error) {
	pgxTx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return store.TruncationResult{}, fmt.Errorf("begin truncation transaction: %w", err)
	}
	defer func() { _ = pgxTx.Rollback(ctx) }()

	handle := &tx{queries: s.queries.WithTx(pgxTx), registry: s.registry, keys: s.keys}
	result, err := handle.TruncateAuditBefore(ctx, organizationID, before)
	if err != nil {
		return store.TruncationResult{}, err
	}
	// The commit can raise 40001 too, so it belongs inside the retried
	// operation rather than after it.
	if commitErr := pgxTx.Commit(ctx); commitErr != nil {
		return store.TruncationResult{}, fmt.Errorf("commit truncation: %w", commitErr)
	}
	return result, nil
}

// TruncateAuditBefore runs ONE truncation pass in the caller's transaction.
//
// It owns the dependency order and the retention accounting; it does not
// own isolation or retry, because a pass cannot re-open a transaction it
// did not begin. Store supplies both.
//
// It is NOT part of store.Tx. The seam offers truncation on Store alone,
// since WithTx opens at the pool's default isolation and gives a caller no
// way to ask for another — so a pass reachable through Tx could only ever
// refuse. The isolation check below therefore guards this internal path
// rather than a public one, and is the reason changing truncateOnce to use
// WithTx would fail loudly instead of quietly evaluating five retention
// guards against five instants.
func (t *tx) TruncateAuditBefore(ctx context.Context, organizationID uuid.UUID, before time.Time) (store.TruncationResult, error) {
	if before.IsZero() {
		return store.TruncationResult{}, errors.New("truncation needs an explicit horizon; there is no " +
			"'delete all', because an unbounded destructive operation should not be reachable by accident")
	}
	if err := t.requireSnapshotIsolation(ctx); err != nil {
		return store.TruncationResult{}, err
	}

	steps := truncationSteps()
	result := store.TruncationResult{PerTable: make(map[string]store.TableTruncation, len(steps))}
	organization, horizon := toUUID(organizationID), toTimestamptz(before)
	for _, step := range steps {
		if err := step(t, ctx, &result, organization, horizon); err != nil {
			return store.TruncationResult{}, err
		}
	}
	return result, nil
}

// truncationStep is one table's contribution to a pass.
type truncationStep func(*tx, context.Context, *store.TruncationResult, pgtype.UUID, pgtype.Timestamptz) error

// truncationSteps is the deletion order, and it is not a preference.
//
// Every foreign key here is ON DELETE RESTRICT: audit_artifacts references
// tool_calls and tool_calls references llm_calls, so a referent deleted
// before its referrer does not quietly survive -- it ABORTS the statement
// and takes the pass with it. The referents therefore go last.
//
// Counting is interleaved with deleting rather than done up front, because
// "referenced by a SURVIVING tool call" is a question about the state after
// the tool calls have gone, and inside one transaction a later statement
// sees this transaction's own deletes.
//
// Declared as a list so the order is one reviewable line rather than a
// property of how a long function happens to be written.
func truncationSteps() []truncationStep {
	return []truncationStep{
		(*tx).truncateAuditEvents,
		(*tx).truncateMetricEvents,
		(*tx).truncateAuditArtifacts,
		(*tx).truncateAttachments,
		(*tx).truncateToolCalls,
		(*tx).truncateLLMCalls,
	}
}

// truncateAuditEvents removes audit events past the horizon. Nothing
// depends on them and nothing retains them.
//
// The four per-table steps deliberately repeat one shape -- count, delete,
// record -- against four distinct generated parameter and row types. What
// is shared between them is the accounting, which lives in record; folding
// the plumbing into a generic over five pairs of generated types would
// make the destructive path harder to read for no invariant gained.
//
//nolint:dupl // one shape, distinct generated types; see the note above
func (t *tx) truncateAuditEvents(ctx context.Context, result *store.TruncationResult,
	organizationID pgtype.UUID, before pgtype.Timestamptz,
) error {
	candidates, err := t.queries.CountAuditEventCandidates(ctx, gen.CountAuditEventCandidatesParams{
		OrganizationID: organizationID,
		Before:         before,
	})
	if err != nil {
		return fmt.Errorf("count audit event candidates: %w", err)
	}
	deleted, err := t.queries.TruncateAuditEvents(ctx, gen.TruncateAuditEventsParams{
		OrganizationID: organizationID,
		Before:         before,
	})
	if err != nil {
		return fmt.Errorf("truncate audit events: %w", err)
	}
	return record(result, store.TableAuditEvents, store.TableTruncation{Candidates: candidates}, deleted)
}

// truncateMetricEvents removes metric events past the horizon.
//
//nolint:dupl // one shape, distinct generated types; see truncateAuditEvents
func (t *tx) truncateMetricEvents(ctx context.Context, result *store.TruncationResult,
	organizationID pgtype.UUID, before pgtype.Timestamptz,
) error {
	candidates, err := t.queries.CountMetricEventCandidates(ctx, gen.CountMetricEventCandidatesParams{
		OrganizationID: organizationID,
		Before:         before,
	})
	if err != nil {
		return fmt.Errorf("count metric event candidates: %w", err)
	}
	deleted, err := t.queries.TruncateMetricEvents(ctx, gen.TruncateMetricEventsParams{
		OrganizationID: organizationID,
		Before:         before,
	})
	if err != nil {
		return fmt.Errorf("truncate metric events: %w", err)
	}
	return record(result, store.TableMetricEvents, store.TableTruncation{Candidates: candidates}, deleted)
}

// truncateAuditArtifacts removes Audit artifacts past the horizon, keeping
// the pinned ones. This is the only pinnable family in item 5's scope;
// binary attachments are item 6's, where deleting a row whose bytes live in
// object storage is that item's commit-order problem.
//
//nolint:dupl // one shape as binary_attachments, distinct generated types; see truncateAuditEvents
func (t *tx) truncateAuditArtifacts(ctx context.Context, result *store.TruncationResult,
	organizationID pgtype.UUID, before pgtype.Timestamptz,
) error {
	counted, err := t.queries.CountAuditArtifactTruncation(ctx, gen.CountAuditArtifactTruncationParams{
		OrganizationID: organizationID,
		Before:         before,
	})
	if err != nil {
		return fmt.Errorf("count audit artifact candidates: %w", err)
	}
	deleted, err := t.queries.TruncateAuditArtifacts(ctx, gen.TruncateAuditArtifactsParams{
		OrganizationID: organizationID,
		Before:         before,
	})
	if err != nil {
		return fmt.Errorf("truncate audit artifacts: %w", err)
	}
	return record(result, store.TableAuditArtifacts, store.TableTruncation{
		Candidates:     counted.Candidates,
		RetainedPinned: counted.RetainedPinned,
	}, deleted)
}

// truncateAttachments removes unpinned attachment rows past the horizon
// (design D6a).
//
// Its position is not forced by a foreign key -- nothing but pins
// references attachments -- so it sits beside audit_artifacts, the other
// pinnable family, because that is where a reader will look for it.
//
// Deleting the row does NOT delete the object. It makes the object
// unreachable, and the object sweep reclaims it afterwards under the digest
// lock. Attempting both here would put a remote call inside a snapshot that
// cannot contain it.
//
//nolint:dupl // one shape as audit_artifacts, distinct generated types; see truncateAuditEvents
func (t *tx) truncateAttachments(ctx context.Context, result *store.TruncationResult,
	organizationID pgtype.UUID, before pgtype.Timestamptz,
) error {
	counted, err := t.queries.CountAttachmentTruncation(ctx, gen.CountAttachmentTruncationParams{
		OrganizationID: organizationID,
		Before:         before,
	})
	if err != nil {
		return fmt.Errorf("count attachment candidates: %w", err)
	}
	deleted, err := t.queries.TruncateAttachments(ctx, gen.TruncateAttachmentsParams{
		OrganizationID: organizationID,
		Before:         before,
	})
	if err != nil {
		return fmt.Errorf("truncate attachments: %w", err)
	}
	return record(result, store.TableAttachments, store.TableTruncation{
		Candidates:     counted.Candidates,
		RetainedPinned: counted.RetainedPinned,
	}, deleted)
}

// truncateToolCalls removes completed, unreferenced tool calls.
//
// Retained while OPEN -- an old open call is an operational signal, and
// deleting an in-progress record is what ADR 0027 forbids -- and while
// cited by an artifact. management_artifacts is never truncated, so a tool
// call cited by one is retained permanently: it is provenance for
// reviewable work product.
//
//nolint:dupl // one shape, distinct generated types; see truncateAuditEvents
func (t *tx) truncateToolCalls(ctx context.Context, result *store.TruncationResult,
	organizationID pgtype.UUID, before pgtype.Timestamptz,
) error {
	counted, err := t.queries.CountToolCallTruncation(ctx, gen.CountToolCallTruncationParams{
		OrganizationID: organizationID,
		Before:         before,
	})
	if err != nil {
		return fmt.Errorf("count tool call candidates: %w", err)
	}
	deleted, err := t.queries.TruncateToolCalls(ctx, gen.TruncateToolCallsParams{
		OrganizationID: organizationID,
		Before:         before,
	})
	if err != nil {
		return fmt.Errorf("truncate tool calls: %w", err)
	}
	return record(result, store.TableToolCalls, store.TableTruncation{
		Candidates:         counted.Candidates,
		RetainedOpen:       counted.RetainedOpen,
		RetainedReferenced: counted.RetainedReferenced,
	}, deleted)
}

// truncateLLMCalls removes completed LLM calls no surviving tool call
// claims. It runs last, so "surviving" means after the tool calls above.
//
//nolint:dupl // one shape, distinct generated types; see truncateAuditEvents
func (t *tx) truncateLLMCalls(ctx context.Context, result *store.TruncationResult,
	organizationID pgtype.UUID, before pgtype.Timestamptz,
) error {
	counted, err := t.queries.CountLLMCallTruncation(ctx, gen.CountLLMCallTruncationParams{
		OrganizationID: organizationID,
		Before:         before,
	})
	if err != nil {
		return fmt.Errorf("count llm call candidates: %w", err)
	}
	deleted, err := t.queries.TruncateLLMCalls(ctx, gen.TruncateLLMCallsParams{
		OrganizationID: organizationID,
		Before:         before,
	})
	if err != nil {
		return fmt.Errorf("truncate llm calls: %w", err)
	}
	return record(result, store.TableLLMCalls, store.TableTruncation{
		Candidates:         counted.Candidates,
		RetainedOpen:       counted.RetainedOpen,
		RetainedReferenced: counted.RetainedReferenced,
	}, deleted)
}

// record files one table's contribution, refusing a set of counts that
// describes no consistent set of rows.
//
// The reconciliation identity is checked here rather than only in a test,
// because it is the property that makes the four numbers mean anything: a
// caller adding them up is entitled to get the candidate count back. If it
// fails, the counting query and the delete disagree about which rows were
// in scope, and reporting either number would be reporting a guess.
//
//nolint:gocritic // hugeParam: TableTruncation is five counters, by value for clarity
func record(result *store.TruncationResult, table string, counted store.TableTruncation, deleted int64) error {
	counted.Deleted = deleted
	if !counted.Reconciles() {
		return fmt.Errorf("%w: truncating %s counted %d candidates but reported %d deleted, %d pinned, "+
			"%d open and %d referenced, which do not sum to the candidates",
			store.ErrInvariant, table, counted.Candidates, counted.Deleted,
			counted.RetainedPinned, counted.RetainedOpen, counted.RetainedReferenced)
	}
	result.PerTable[table] = counted
	return nil
}

// requireSnapshotIsolation refuses to run a pass whose guards would be
// evaluated against more than one instant.
//
// One extra round trip on a maintenance operation, buying the guarantee
// that the destructive path cannot silently run at READ COMMITTED because
// it was reached through a caller's own transaction. Truncation is not a
// hot path; being wrong here deletes data that was protected.
func (t *tx) requireSnapshotIsolation(ctx context.Context) error {
	level, err := t.queries.CurrentIsolationLevel(ctx)
	if err != nil {
		return fmt.Errorf("read transaction isolation level: %w", err)
	}
	// SERIALIZABLE is stronger than required and equally sound here, so it
	// is admitted rather than insisting on exactly one value.
	switch strings.ToLower(level) {
	case "repeatable read", "serializable":
		return nil
	}
	return fmt.Errorf("%w: truncation is running at %q, but its retention guards are only sound under one "+
		"snapshot -- at READ COMMITTED each statement takes a fresh one, so a row protected when the pass "+
		"began can still be deleted. Call it on the Store, or open a REPEATABLE READ transaction",
		store.ErrInvariant, level)
}

// isSerializationFailure reports SQLSTATE 40001, by code rather than by
// message text.
func isSerializationFailure(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == serializationFailure
}

// The measured contract for a pin created concurrently with this pass
// (design D6a). Both halves were run against the pinned Postgres image
// before the design was finished, and neither was guessable:
//
//	truncate first, pin second -> the PIN fails, 23503, foreign_key_violation
//	pin first, truncate second -> the TRUNCATION fails, 23001,
//	                              restrict_violation
//
// 23001 is restrict_violation, NOT 23503. They are different codes, and a
// handler matching only foreign-key violations misses the one this pass
// actually sees. Nothing here raises 40001, so the serialization retry
// above does not cover it -- left alone, one concurrent pin would
// intermittently kill an entire truncation pass.
const restrictViolation = "23001"

// The two constraints a concurrent pin can raise this on. A pin targets an
// Audit artifact or an attachment, and BOTH families are truncated with the
// same NOT EXISTS plus ON DELETE RESTRICT shape -- so both races exist, and
// the audit one has existed since item 5 with no handler at all.
//
// Measured separately rather than assumed to mirror each other
// (pinrace_integration_test.go): both orderings on audit_artifacts behave
// exactly as the attachment pair does, 23503 to the pin and 23001 to the
// truncation, each naming its own constraint.
func isRetriablePinConstraint(name string) bool {
	return name == attachmentPinConstraint || name == auditPinConstraint
}

// isRetriablePinRestriction reports the restriction violations this pass may
// retry through.
//
// BOTH halves, not the code alone. 23001 is a generic restriction
// violation, and retrying every one of them would take a PERSISTENT
// RESTRICT failure -- a genuine dependency the pass must not delete
// through -- and turn it into three identical attempts followed by
// ErrConcurrentTruncation, describing concurrency that was never the
// problem. Every other restriction propagates unchanged.
//
// A retry is the right answer for this one: the next attempt's snapshot
// sees the pin, the NOT EXISTS excludes the row, and the pass completes
// with that attachment correctly retained.
func isRetriablePinRestriction(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) &&
		pgErr.Code == restrictViolation &&
		isRetriablePinConstraint(pgErr.ConstraintName)
}
