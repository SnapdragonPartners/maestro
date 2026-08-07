package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/configkeys"
	"orchestrator/internal/dataplane/secret"
	"orchestrator/internal/dataplane/store"
)

// Store's own methods delegate to the transactional implementation through
// inTx, so calling one outside an explicit transaction is not a second,
// weaker code path.
//
// This matters most for the operations whose correctness IS the
// transaction: acceptance under a row lock, an instance written with its
// seeding set, supersession pairing two rows. If Store implemented those
// against the pool directly, the guarantee would silently depend on which
// entry point a caller happened to use.

// CreateManagementArtifact writes a draft Management artifact.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface (see artifacts.go)
func (s *Store) CreateManagementArtifact(ctx context.Context, input store.CreateManagementArtifactInput) (*store.ManagementArtifact, error) {
	return inTx(ctx, s, func(t *tx) (*store.ManagementArtifact, error) {
		return t.CreateManagementArtifact(ctx, input)
	})
}

// CreateAuditArtifact writes an Audit artifact, which is born final.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface (see artifacts.go)
func (s *Store) CreateAuditArtifact(ctx context.Context, input store.CreateAuditArtifactInput) (*store.AuditArtifact, error) {
	return inTx(ctx, s, func(t *tx) (*store.AuditArtifact, error) {
		return t.CreateAuditArtifact(ctx, input)
	})
}

// CreateReview records a review decision as the reviewer saw it.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface (see artifacts.go)
func (s *Store) CreateReview(ctx context.Context, input store.CreateReviewInput) (*store.Review, error) {
	return inTx(ctx, s, func(t *tx) (*store.Review, error) {
		return t.CreateReview(ctx, input)
	})
}

// AcceptArtifact accepts an original against a named review.
func (s *Store) AcceptArtifact(ctx context.Context, organizationID, artifactID, reviewID uuid.UUID) error {
	return s.WithTx(ctx, func(t store.Tx) error {
		return t.AcceptArtifact(ctx, organizationID, artifactID, reviewID)
	})
}

// AcceptAmendment accepts an amendment, checking its reviewed base.
func (s *Store) AcceptAmendment(ctx context.Context, organizationID, amendmentID, reviewID uuid.UUID) error {
	return s.WithTx(ctx, func(t store.Tx) error {
		return t.AcceptAmendment(ctx, organizationID, amendmentID, reviewID)
	})
}

// InvalidateArtifact invalidates a draft.
func (s *Store) InvalidateArtifact(ctx context.Context, organizationID, artifactID uuid.UUID) error {
	return s.WithTx(ctx, func(t store.Tx) error {
		return t.InvalidateArtifact(ctx, organizationID, artifactID)
	})
}

// SupersedeArtifact accepts a replacement and retires its target together.
func (s *Store) SupersedeArtifact(ctx context.Context, organizationID, targetID, supersedingID, reviewID uuid.UUID) error {
	return s.WithTx(ctx, func(t store.Tx) error {
		return t.SupersedeArtifact(ctx, organizationID, targetID, supersedingID, reviewID)
	})
}

// ArchiveArtifact archives an accepted or superseded original.
func (s *Store) ArchiveArtifact(ctx context.Context, organizationID, artifactID uuid.UUID) error {
	return s.WithTx(ctx, func(t store.Tx) error {
		return t.ArchiveArtifact(ctx, organizationID, artifactID)
	})
}

// CreatePrincipalInstance writes an instance with its seeding set.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface (see artifacts.go)
func (s *Store) CreatePrincipalInstance(ctx context.Context, input store.CreatePrincipalInstanceInput) (*store.PrincipalInstance, error) {
	return inTx(ctx, s, func(t *tx) (*store.PrincipalInstance, error) {
		return t.CreatePrincipalInstance(ctx, input)
	})
}

// StopPrincipalInstance records a stop, once only.
func (s *Store) StopPrincipalInstance(ctx context.Context, organizationID, instanceID uuid.UUID, reason string) (store.StopOutcome, error) {
	return inTx(ctx, s, func(t *tx) (store.StopOutcome, error) {
		return t.StopPrincipalInstance(ctx, organizationID, instanceID, reason)
	})
}

// Reads run without an explicit transaction: each is a single statement, so
// a transaction would add a round trip and guarantee nothing. So do the
// call family's single-row writes, by design D7 -- a call record is one
// INSERT on the hottest path in the system.

// direct returns a handle bound to the POOL rather than to a transaction.
// Every statement it issues autocommits on its own.
func (s *Store) direct() *tx {
	return &tx{queries: s.queries, registry: s.registry, keys: s.keys, rootKey: s.rootKey}
}

// GetManagementArtifact reads one Management artifact.
func (s *Store) GetManagementArtifact(ctx context.Context, organizationID, artifactID uuid.UUID) (*store.ManagementArtifact, error) {
	return s.direct().GetManagementArtifact(ctx, organizationID, artifactID)
}

// GetAuditArtifact reads one Audit artifact.
func (s *Store) GetAuditArtifact(ctx context.Context, organizationID, artifactID uuid.UUID) (*store.AuditArtifact, error) {
	return s.direct().GetAuditArtifact(ctx, organizationID, artifactID)
}

// EffectiveView is the exception among reads: it issues two statements, and
// an amendment accepted between them would produce a view assembled from
// two different instants. It therefore runs in a transaction.
func (s *Store) EffectiveView(ctx context.Context, organizationID, artifactID uuid.UUID) (json.RawMessage, error) {
	return inTx(ctx, s, func(t *tx) (json.RawMessage, error) {
		return t.EffectiveView(ctx, organizationID, artifactID)
	})
}

// AmendmentBase runs in a transaction: it reads the view and the sequence,
// and an amendment accepted between them would produce a base that never
// existed at any single instant -- precisely the base a review must not be
// bound to.
func (s *Store) AmendmentBase(ctx context.Context, organizationID, originalID uuid.UUID) (store.AmendmentBase, error) {
	return inTx(ctx, s, func(t *tx) (store.AmendmentBase, error) {
		return t.AmendmentBase(ctx, organizationID, originalID)
	})
}

// ListManagementArtifactsByScope reads a scope's Management artifacts.
func (s *Store) ListManagementArtifactsByScope(ctx context.Context, organizationID uuid.UUID, scope store.Scope) ([]store.ManagementArtifact, error) {
	return s.direct().ListManagementArtifactsByScope(ctx, organizationID, scope)
}

// ListManagementArtifactsByStory reads a Story's Management artifacts.
func (s *Store) ListManagementArtifactsByStory(ctx context.Context, organizationID, storyID uuid.UUID) ([]store.ManagementArtifact, error) {
	return s.direct().ListManagementArtifactsByStory(ctx, organizationID, storyID)
}

// ListAuditArtifactsByScope reads a scope's Audit artifacts.
func (s *Store) ListAuditArtifactsByScope(ctx context.Context, organizationID uuid.UUID, scope store.Scope) ([]store.AuditArtifact, error) {
	return s.direct().ListAuditArtifactsByScope(ctx, organizationID, scope)
}

// ListReviews reads an artifact's review records.
func (s *Store) ListReviews(ctx context.Context, organizationID, artifactID uuid.UUID) ([]store.Review, error) {
	return s.direct().ListReviews(ctx, organizationID, artifactID)
}

// GetPrincipalInstance reads one principal instance.
func (s *Store) GetPrincipalInstance(ctx context.Context, organizationID, instanceID uuid.UUID) (*store.PrincipalInstance, error) {
	return s.direct().GetPrincipalInstance(ctx, organizationID, instanceID)
}

// ListSeededInputs reads an instance's MPH seeding set.
func (s *Store) ListSeededInputs(ctx context.Context, organizationID, instanceID uuid.UUID) ([]store.SeededInput, error) {
	return s.direct().ListSeededInputs(ctx, organizationID, instanceID)
}

// FindPrincipalInstances serves the MPH reads along one axis.
func (s *Store) FindPrincipalInstances(ctx context.Context, query store.MPHQuery) ([]store.PrincipalInstance, error) {
	return s.direct().FindPrincipalInstances(ctx, query)
}

// The call family, reached through the seam.
//
// Creates go DIRECT rather than through inTx: design D7 keeps single-row
// writes out of a transaction, because a call record is one INSERT on the
// hottest path in the system and wrapping it would add two round trips for
// a guarantee one statement already has.
//
// Completions do take one: they are lock, classify, write conditionally,
// and the lock means nothing outside a transaction.

// CreateLLMCall opens an LLM call.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (s *Store) CreateLLMCall(ctx context.Context, input store.CreateLLMCallInput) (*store.LLMCall, error) {
	return s.direct().CreateLLMCall(ctx, input)
}

// CreateToolCall opens a tool call, optionally claiming its parent.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (s *Store) CreateToolCall(ctx context.Context, input store.CreateToolCallInput) (*store.ToolCall, error) {
	return s.direct().CreateToolCall(ctx, input)
}

// CompleteLLMCall records an LLM call's outcome, once only.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (s *Store) CompleteLLMCall(ctx context.Context, input store.CompleteLLMCallInput) (store.LLMCompletion, error) {
	return inTx(ctx, s, func(t *tx) (store.LLMCompletion, error) {
		return t.CompleteLLMCall(ctx, input)
	})
}

// CompleteToolCall records a tool call's outcome, once only.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (s *Store) CompleteToolCall(ctx context.Context, input store.CompleteToolCallInput) (store.ToolCompletion, error) {
	return inTx(ctx, s, func(t *tx) (store.ToolCompletion, error) {
		return t.CompleteToolCall(ctx, input)
	})
}

// CreateMetricEvent records a measurement.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (s *Store) CreateMetricEvent(ctx context.Context, event store.MetricEvent) (*store.MetricEvent, error) {
	return s.direct().CreateMetricEvent(ctx, event)
}

// CreateAuditEvent records something that happened.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (s *Store) CreateAuditEvent(ctx context.Context, event store.AuditEvent) (*store.AuditEvent, error) {
	return s.direct().CreateAuditEvent(ctx, event)
}

// GetLLMCall reads one LLM call.
func (s *Store) GetLLMCall(ctx context.Context, organizationID, callID uuid.UUID) (*store.LLMCall, error) {
	return s.direct().GetLLMCall(ctx, organizationID, callID)
}

// GetToolCall reads one tool call.
func (s *Store) GetToolCall(ctx context.Context, organizationID, callID uuid.UUID) (*store.ToolCall, error) {
	return s.direct().GetToolCall(ctx, organizationID, callID)
}

// ListLLMCallsByStory reads a Story's LLM calls, one bounded page.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (s *Store) ListLLMCallsByStory(ctx context.Context, organizationID, storyID uuid.UUID, page store.Page) ([]store.LLMCall, error) {
	return s.direct().ListLLMCallsByStory(ctx, organizationID, storyID, page)
}

// ListLLMCallsByPrincipal reads one principal's LLM calls.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (s *Store) ListLLMCallsByPrincipal(ctx context.Context, organizationID, instanceID uuid.UUID, page store.Page) ([]store.LLMCall, error) {
	return s.direct().ListLLMCallsByPrincipal(ctx, organizationID, instanceID, page)
}

// ListLLMCallsInWindow reads an organization's LLM calls in a window.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (s *Store) ListLLMCallsInWindow(ctx context.Context, organizationID uuid.UUID, from, to time.Time, page store.Page) ([]store.LLMCall, error) {
	return s.direct().ListLLMCallsInWindow(ctx, organizationID, from, to, page)
}

// ListToolCallsByStory reads a Story's tool calls.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (s *Store) ListToolCallsByStory(ctx context.Context, organizationID, storyID uuid.UUID, page store.Page) ([]store.ToolCall, error) {
	return s.direct().ListToolCallsByStory(ctx, organizationID, storyID, page)
}

// ListToolCallsByPrincipal reads one principal's tool calls.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (s *Store) ListToolCallsByPrincipal(ctx context.Context, organizationID, instanceID uuid.UUID, page store.Page) ([]store.ToolCall, error) {
	return s.direct().ListToolCallsByPrincipal(ctx, organizationID, instanceID, page)
}

// ListToolCallsInWindow reads an organization's tool calls in a window.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (s *Store) ListToolCallsInWindow(ctx context.Context, organizationID uuid.UUID, from, to time.Time, page store.Page) ([]store.ToolCall, error) {
	return s.direct().ListToolCallsInWindow(ctx, organizationID, from, to, page)
}

// ListMetricEventsInWindow reads metric events in a window.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (s *Store) ListMetricEventsInWindow(ctx context.Context, organizationID uuid.UUID, from, to time.Time, page store.Page) ([]store.MetricEvent, error) {
	return s.direct().ListMetricEventsInWindow(ctx, organizationID, from, to, page)
}

// ListAuditEventsInWindow reads audit events in a window.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (s *Store) ListAuditEventsInWindow(ctx context.Context, organizationID uuid.UUID, from, to time.Time, page store.Page) ([]store.AuditEvent, error) {
	return s.direct().ListAuditEventsInWindow(ctx, organizationID, from, to, page)
}

// AggregateCost totals one cohort in one window.
func (s *Store) AggregateCost(ctx context.Context, organizationID uuid.UUID, provider, model string,
	from, to time.Time,
) (store.CostAggregate, error) {
	return s.direct().AggregateCost(ctx, organizationID, provider, model, from, to)
}

// The configuration family, reached through the seam (item 7, design D1).
//
// Resolution, the identity read and creation are single statements and go
// DIRECT, matching the rule design D7 set for the call family: one
// statement already has the guarantee a transaction would add, and wrapping
// it buys two round trips and nothing else.
//
// Update and delete take one, because both are read-then-conditionally-write:
// the update reads the row to learn the key its new value must validate
// against, and the delete reads it to tell a version conflict from a missing
// record. Split across two transactions, either could classify against a row
// that no longer exists by the time the write runs.

// ResolveConfiguration returns the most specific record for a repository.
func (s *Store) ResolveConfiguration(
	ctx context.Context, organizationID, repositoryID uuid.UUID, key configkeys.Key,
) (*store.ConfigurationRecord, error) {
	return s.direct().ResolveConfiguration(ctx, organizationID, repositoryID, key)
}

// GetConfigurationRecord reads one record by identity.
func (s *Store) GetConfigurationRecord(
	ctx context.Context, organizationID, recordID uuid.UUID,
) (*store.ConfigurationRecord, error) {
	return s.direct().GetConfigurationRecord(ctx, organizationID, recordID)
}

// CreateConfigurationRecord validates against the key registry, then writes.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (s *Store) CreateConfigurationRecord(
	ctx context.Context, input store.CreateConfigurationRecordInput,
) (*store.ConfigurationRecord, error) {
	return s.direct().CreateConfigurationRecord(ctx, input)
}

// UpdateConfigurationRecord replaces a value under its expected version.
func (s *Store) UpdateConfigurationRecord(
	ctx context.Context, organizationID, recordID uuid.UUID, expectedVersion int, value json.RawMessage,
) (*store.ConfigurationRecord, error) {
	return inTx(ctx, s, func(t *tx) (*store.ConfigurationRecord, error) {
		return t.UpdateConfigurationRecord(ctx, organizationID, recordID, expectedVersion, value)
	})
}

// DeleteConfigurationRecord removes an override under its expected version.
func (s *Store) DeleteConfigurationRecord(
	ctx context.Context, organizationID, recordID uuid.UUID, expectedVersion int,
) error {
	_, err := inTx(ctx, s, func(t *tx) (struct{}, error) {
		return struct{}{}, t.DeleteConfigurationRecord(ctx, organizationID, recordID, expectedVersion)
	})
	return err
}

// The secrets vault, reached through the seam (item 7, design D5).
//
// All six go DIRECT. Five are one statement plus in-process sealing or
// opening. Replacement is a read and then a conditional update, which would
// normally argue for a transaction — but the configuration family's
// lock-and-classify pattern does not apply here: every field replacement
// reads is immutable, so the read cannot go stale, and both mutations
// deliberately collapse "moved" and "not yours" into one answer rather than
// classifying between them. See the note on ReplaceSecret for why the
// immutability holds and what enforces it.

// CreateIndividualSecret writes a credential owned by the acting user.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (s *Store) CreateIndividualSecret(
	ctx context.Context, input store.CreateSecretInput,
) (*store.Secret, error) {
	return s.direct().CreateIndividualSecret(ctx, input)
}

// CreateSharedSecret writes a credential held in common at its scope.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (s *Store) CreateSharedSecret(
	ctx context.Context, input store.CreateSecretInput,
) (*store.Secret, error) {
	return s.direct().CreateSharedSecret(ctx, input)
}

// ResolveSecret walks the six-step ladder for a repository.
func (s *Store) ResolveSecret(
	ctx context.Context, organizationID, repositoryID, actingUserID uuid.UUID, name string,
) (*store.Secret, error) {
	return s.direct().ResolveSecret(ctx, organizationID, repositoryID, actingUserID, name)
}

// GetSecret reads one secret by identity.
func (s *Store) GetSecret(
	ctx context.Context, organizationID, secretID, actingUserID uuid.UUID,
) (*store.Secret, error) {
	return s.direct().GetSecret(ctx, organizationID, secretID, actingUserID)
}

// RevealSecret decrypts one secret's plaintext.
func (s *Store) RevealSecret(
	ctx context.Context, organizationID, secretID, actingUserID uuid.UUID,
) (secret.Value, error) {
	return s.direct().RevealSecret(ctx, organizationID, secretID, actingUserID)
}

// ReplaceSecret rotates a credential in place.
func (s *Store) ReplaceSecret(
	ctx context.Context, organizationID, secretID, actingUserID uuid.UUID,
	expectedVersion int, plaintext secret.Value,
) (*store.Secret, error) {
	return s.direct().ReplaceSecret(ctx, organizationID, secretID, actingUserID, expectedVersion, plaintext)
}

// DeleteSecret removes a credential under its expected version.
func (s *Store) DeleteSecret(
	ctx context.Context, organizationID, secretID, actingUserID uuid.UUID, expectedVersion int,
) error {
	return s.direct().DeleteSecret(ctx, organizationID, secretID, actingUserID, expectedVersion)
}

// The benchmark family (item 9). Each of these is a read-then-compare over a
// row an insert-or-nothing may have just created, so each runs inside one
// transaction for the same reason the acceptance path does: outside one, the
// insert and the read that interprets it are two statements another writer
// can act between.

// GetOrganizationBySlug resolves a tenant by its slug.
func (s *Store) GetOrganizationBySlug(ctx context.Context, slug string) (*store.Organization, error) {
	return inTx(ctx, s, func(t *tx) (*store.Organization, error) {
		return t.GetOrganizationBySlug(ctx, slug)
	})
}

// GetUserByHandle resolves an accountable human within one tenant.
func (s *Store) GetUserByHandle(ctx context.Context, organizationID uuid.UUID, handle string) (*store.User, error) {
	return inTx(ctx, s, func(t *tx) (*store.User, error) {
		return t.GetUserByHandle(ctx, organizationID, handle)
	})
}

// GetBenchmarkRunBySuite resolves a suite run by the runner's own identity.
func (s *Store) GetBenchmarkRunBySuite(ctx context.Context, organizationID uuid.UUID, suiteRunID string) (*store.BenchmarkRun, error) {
	return inTx(ctx, s, func(t *tx) (*store.BenchmarkRun, error) {
		return t.GetBenchmarkRunBySuite(ctx, organizationID, suiteRunID)
	})
}

// GetBenchmarkAttempt resolves one ledgered attempt.
func (s *Store) GetBenchmarkAttempt(ctx context.Context, organizationID, benchmarkRunID uuid.UUID, runID string) (*store.BenchmarkAttempt, error) {
	return inTx(ctx, s, func(t *tx) (*store.BenchmarkAttempt, error) {
		return t.GetBenchmarkAttempt(ctx, organizationID, benchmarkRunID, runID)
	})
}

// ListBenchmarkAttempts returns every ledgered attempt of one suite run.
func (s *Store) ListBenchmarkAttempts(ctx context.Context, organizationID, benchmarkRunID uuid.UUID) ([]store.BenchmarkAttempt, error) {
	return inTx(ctx, s, func(t *tx) ([]store.BenchmarkAttempt, error) {
		return t.ListBenchmarkAttempts(ctx, organizationID, benchmarkRunID)
	})
}

// BootstrapOrganization provisions a tenant, idempotently.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (s *Store) BootstrapOrganization(ctx context.Context, input store.BootstrapOrganizationInput) (store.Bootstrapped[store.Organization], error) {
	result, err := inTx(ctx, s, func(t *tx) (*store.Bootstrapped[store.Organization], error) {
		outcome, txErr := t.BootstrapOrganization(ctx, input)
		return &outcome, txErr
	})
	if err != nil {
		return store.Bootstrapped[store.Organization]{}, err
	}
	return *result, nil
}

// BootstrapUser provisions an accountable human, idempotently.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (s *Store) BootstrapUser(ctx context.Context, input store.BootstrapUserInput) (store.Bootstrapped[store.User], error) {
	result, err := inTx(ctx, s, func(t *tx) (*store.Bootstrapped[store.User], error) {
		outcome, txErr := t.BootstrapUser(ctx, input)
		return &outcome, txErr
	})
	if err != nil {
		return store.Bootstrapped[store.User]{}, err
	}
	return *result, nil
}

// EnsureBenchmarkRun returns the suite's row, creating it if absent.
func (s *Store) EnsureBenchmarkRun(ctx context.Context, organizationID uuid.UUID, suiteRunID string) (store.Bootstrapped[store.BenchmarkRun], error) {
	result, err := inTx(ctx, s, func(t *tx) (*store.Bootstrapped[store.BenchmarkRun], error) {
		outcome, txErr := t.EnsureBenchmarkRun(ctx, organizationID, suiteRunID)
		return &outcome, txErr
	})
	if err != nil {
		return store.Bootstrapped[store.BenchmarkRun]{}, err
	}
	return *result, nil
}

// RecordBenchmarkAttempt has NO Store delegate, deliberately. It is on Tx
// alone (store.BenchmarkTxWriter): a Store method would open a transaction of
// its own and commit the ledger row apart from the Audit artifact it names,
// which is the one thing the ledger exists to prevent. structure_test asserts
// the absence, so a future delegate cannot be added by habit.

// GetSuiteReport returns which artifact is a suite's report.
func (s *Store) GetSuiteReport(ctx context.Context, organizationID, benchmarkRunID uuid.UUID) (*store.SuiteReportClaim, error) {
	return inTx(ctx, s, func(t *tx) (*store.SuiteReportClaim, error) {
		return t.GetSuiteReport(ctx, organizationID, benchmarkRunID)
	})
}

// ClaimSuiteReport records which artifact is a suite's report, idempotently.
//
//nolint:gocritic // hugeParam: by value, matching the seam interface
func (s *Store) ClaimSuiteReport(ctx context.Context, input store.ClaimSuiteReportInput) (store.Bootstrapped[store.SuiteReportClaim], error) {
	result, err := inTx(ctx, s, func(t *tx) (*store.Bootstrapped[store.SuiteReportClaim], error) {
		outcome, txErr := t.ClaimSuiteReport(ctx, input)
		return &outcome, txErr
	})
	if err != nil {
		return store.Bootstrapped[store.SuiteReportClaim]{}, err
	}
	return *result, nil
}
