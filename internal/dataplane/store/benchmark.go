package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// The benchmark family: the vertical slice's scope target and its import
// ledger (design_slice_import.md, D2, D6 and D10).
//
// Everything here is append-only. A benchmark run carries nothing a later
// import would change, and an attempt's ledger row IS the record that the
// import happened — so the seam offers no way to update either, and the
// structural test refuses a statement that would.

// Sentinel errors. Each names a different thing a caller does about it.
var (
	// ErrImportConflict reports that an identity already present in the
	// ledger was offered a DIFFERENT payload.
	//
	// It is not a retryable failure and not a no-op: run records are
	// append-only on disk and never rewritten, so a differing digest means
	// the file changed under us — corruption, a partial write, or tampering.
	// Overwriting would erase the evidence of exactly that, which is why the
	// import refuses instead.
	ErrImportConflict = errors.New("a different payload is already imported for this identity")

	// ErrBootstrapConflict reports that a natural key already exists carrying
	// different display data.
	//
	// Distinguished from a plain "already exists" because the outcomes differ:
	// matching data is a successful no-op, and differing data is a request
	// this command will not honour. Silently ignoring the difference would
	// make `bootstrap --org acme --org-name "Acme Ltd"` appear to succeed
	// while the plane still said "Acme Inc".
	ErrBootstrapConflict = errors.New("the record exists with different display data")
)

// ImportConflict carries both sides of a rejected re-import, because the
// operator's next question is always which one is wrong.
type ImportConflict struct {
	SuiteRunID    string
	RunID         string
	StoredDigest  string
	OfferedDigest string
}

func (e *ImportConflict) Error() string {
	return fmt.Sprintf("%s: suite %s attempt %s is stored with digest %s but the file offers %s",
		ErrImportConflict, e.SuiteRunID, e.RunID, e.StoredDigest, e.OfferedDigest)
}

// Is lets callers match the sentinel without unwrapping the detail.
func (e *ImportConflict) Is(target error) bool { return target == ErrImportConflict }

// BootstrapConflict carries the stored and supplied display data.
type BootstrapConflict struct {
	Kind     string
	Key      string
	Stored   string
	Supplied string
}

func (e *BootstrapConflict) Error() string {
	return fmt.Sprintf("%s: %s %q is named %q, not %q; renaming is a separate operation",
		ErrBootstrapConflict, e.Kind, e.Key, e.Stored, e.Supplied)
}

// Is lets callers match the sentinel without unwrapping the detail.
func (e *BootstrapConflict) Is(target error) bool { return target == ErrBootstrapConflict }

// Organization is a tenant.
type Organization struct {
	CreatedAt      time.Time
	Slug           string
	DisplayName    string
	OrganizationID uuid.UUID
}

// User is an accountable human. Local mode has no authentication; this is an
// identity, not a credential.
type User struct {
	CreatedAt      time.Time
	Handle         string
	DisplayName    string
	UserID         uuid.UUID
	OrganizationID uuid.UUID
}

// BenchmarkRun is one imported suite run, and the entity benchmark-scoped
// artifacts scope to.
type BenchmarkRun struct {
	FirstImportedAt time.Time
	SuiteRunID      string
	BenchmarkRunID  uuid.UUID
	OrganizationID  uuid.UUID
}

// BenchmarkAttempt is one ledgered attempt: the proof that this run record
// was imported, and the digest that decides whether a re-offer is a no-op or
// a conflict.
type BenchmarkAttempt struct {
	ImportedAt         time.Time
	RunID              string
	RecordDigest       string
	BenchmarkAttemptID uuid.UUID
	OrganizationID     uuid.UUID
	BenchmarkRunID     uuid.UUID
	AuditArtifactID    uuid.UUID
}

// SuiteReportClaim is the record that one Management artifact IS a suite
// run's report.
//
// It exists because "at most one report per suite" needed something to
// enforce it. Assembly reads for an existing report and writes one when it
// finds none, and those are two statements: two imports of one terminal
// suite can both read nothing and both write. The uniqueness on
// (organization, benchmark run) is the arbiter, per ADR 0027's rule that
// shared state is serialized on a key matching the resource.
type SuiteReportClaim struct {
	ClaimedAt        time.Time
	ClaimID          uuid.UUID
	OrganizationID   uuid.UUID
	BenchmarkRunID   uuid.UUID
	ReportArtifactID uuid.UUID
}

// ClaimSuiteReportInput names the artifact a suite's report is.
type ClaimSuiteReportInput struct {
	OrganizationID   uuid.UUID
	BenchmarkRunID   uuid.UUID
	ReportArtifactID uuid.UUID
}

// BootstrapOrganizationInput provisions a tenant.
type BootstrapOrganizationInput struct {
	Slug        string
	DisplayName string
}

// BootstrapUserInput provisions an accountable human within one tenant.
type BootstrapUserInput struct {
	Handle         string
	DisplayName    string
	OrganizationID uuid.UUID
}

// Bootstrapped reports what a provisioning call did.
//
// Created distinguishes the two SUCCESSFUL outcomes, which a caller reports
// differently and which a conflict is neither of.
type Bootstrapped[T any] struct {
	Record  T
	Created bool
}

// RecordBenchmarkAttemptInput ledgers one imported attempt.
//
// It is written in the SAME transaction as the Audit artifact it names.
// Split across two, a crash between them leaves an artifact with no ledger
// row, and the next import writes the artifact again — silently duplicating
// the record the ledger exists to make unique.
type RecordBenchmarkAttemptInput struct {
	RunID           string
	RecordDigest    string
	OrganizationID  uuid.UUID
	BenchmarkRunID  uuid.UUID
	AuditArtifactID uuid.UUID
}

// BenchmarkReader is the benchmark family's read surface.
type BenchmarkReader interface {
	GetOrganizationBySlug(ctx context.Context, slug string) (*Organization, error)
	GetUserByHandle(ctx context.Context, organizationID uuid.UUID, handle string) (*User, error)

	GetBenchmarkRunBySuite(ctx context.Context, organizationID uuid.UUID, suiteRunID string) (*BenchmarkRun, error)
	GetBenchmarkAttempt(ctx context.Context, organizationID, benchmarkRunID uuid.UUID, runID string) (*BenchmarkAttempt, error)
	ListBenchmarkAttempts(ctx context.Context, organizationID, benchmarkRunID uuid.UUID) ([]BenchmarkAttempt, error)

	// GetSuiteReport returns which artifact is the suite's report, or
	// ErrNotFound when nothing has claimed it. Absence is the ordinary
	// state of a suite imported while it was still running.
	GetSuiteReport(ctx context.Context, organizationID, benchmarkRunID uuid.UUID) (*SuiteReportClaim, error)
}

// BenchmarkWriter is the benchmark family's write surface.
type BenchmarkWriter interface {
	// BootstrapOrganization and BootstrapUser are idempotent by natural key,
	// with exact conflict semantics: matching display data returns the
	// existing record with Created=false, and DIFFERING display data returns
	// ErrBootstrapConflict rather than silently ignoring the difference or
	// quietly renaming the record.
	//
	// Reachable only from the bootstrap command. The importer resolves with
	// Get* and never provisions: an import that silently creates a tenant is
	// a defect waiting for team mode.
	BootstrapOrganization(ctx context.Context, input BootstrapOrganizationInput) (Bootstrapped[Organization], error)
	BootstrapUser(ctx context.Context, input BootstrapUserInput) (Bootstrapped[User], error)

	// EnsureBenchmarkRun returns the suite's row, creating it if absent.
	// Idempotent by (organization, suite run id) and carrying nothing a
	// second call would change, so re-import reads rather than writes.
	EnsureBenchmarkRun(ctx context.Context, organizationID uuid.UUID, suiteRunID string) (Bootstrapped[BenchmarkRun], error)

	// ClaimSuiteReport records which artifact is a suite's report, and
	// reports whether THIS caller was the one that recorded it.
	//
	// Created=false with no error means another importer got there first,
	// and the returned claim is that importer's. The caller is then holding
	// a draft report nobody will ever accept, and must withdraw it: two
	// drafts for one suite are two claims about one conformance run, and
	// both would be independently acceptable.
	//
	// It cannot join the transaction that creates the artifact, because
	// AttachEvidence owns its own — so the artifact is written first and
	// claimed second, and losing the claim is a compensating path rather
	// than a rollback.
	ClaimSuiteReport(ctx context.Context, input ClaimSuiteReportInput) (Bootstrapped[SuiteReportClaim], error)
}

// BenchmarkTxWriter is the part of the benchmark family that exists ONLY
// inside a caller's transaction.
//
// It is separate from BenchmarkWriter, and not embedded by Writer, because
// Writer is embedded by Store — and a Store method opens a transaction of its
// own. Offering this there would put the forbidden operation on the public
// seam: the ledger row committed by itself, in its own transaction, apart
// from the artifact it names. The contract is that the two commit TOGETHER,
// and an interface that lets a caller do otherwise is not a contract.
//
// The same reasoning Maintenance uses to sit on Store alone, pointed the
// other way: there, an operation could not be honoured inside a caller's
// transaction; here, it must not be reachable outside one.
type BenchmarkTxWriter interface {
	// RecordBenchmarkAttempt ledgers an attempt, or reports what is already
	// there.
	//
	// Created=false with no error is the no-op: the same identity carrying
	// the same digest. A different digest is *ImportConflict, and nothing is
	// written.
	//
	// The caller MUST write the Audit artifact this names in the same
	// transaction. An artifact committed without its ledger row is imported
	// again on the next run and silently duplicated, which is the whole
	// failure the ledger exists to prevent.
	RecordBenchmarkAttempt(ctx context.Context, input RecordBenchmarkAttemptInput) (Bootstrapped[BenchmarkAttempt], error)
}
