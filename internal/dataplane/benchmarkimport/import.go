package benchmarkimport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/canonical"
	"orchestrator/internal/dataplane/store"
)

// newStrictReader wraps payload bytes for a strict decoder.
func newStrictReader(payload []byte) io.Reader { return bytes.NewReader(payload) }

// Principal identities the importer writes.
const (
	// importerModel is the system principal that performs the import. ADR
	// 0021's convention for a system component, and the reason it can author
	// only Audit artifacts: per ADR 0019 the Orchestrator performs no
	// inference, so there is no judgment to review.
	importerModel = "system-benchmark-importer"

	// targetAgentType labels the principal representing the configuration
	// under test.
	targetAgentType = "benchmark-target"

	// The import's own tool call is NOT written yet. It belongs with the
	// suite report, where design D5 needs produced_by_tool_call_id to name
	// the machinery behind an operator-authored claim — and a constant
	// declared here ahead of its writer is a promise the code does not keep,
	// which is why it is not sitting here unused.
)

// Options configures one import.
type Options struct {
	// OrganizationSlug and OperatorHandle are RESOLVED, never created. An
	// import that silently provisions a tenant is a defect waiting for team
	// mode; provisioning is the bootstrap command's job.
	OrganizationSlug string
	OperatorHandle   string
	// Dir is the results store, and SuiteRunID the suite within it.
	Dir        string
	SuiteRunID string
}

// AttemptOutcome is what the import did with one attempt.
type AttemptOutcome struct {
	RunID string
	// Imported is false when the attempt was already ledgered with the same
	// digest — the no-op that makes re-import free.
	Imported bool
}

// Result is what one import produced.
type Result struct {
	Attempts       []AttemptOutcome
	BenchmarkRunID uuid.UUID
	// Terminal reports whether the suite had stopped. A non-terminal suite
	// imports its attempts and gets no report (design D7); the report and its
	// evidence arrive on the later import that finds the suite finished.
	Terminal bool
}

// Importer writes runner records into the plane through the persistence seam.
type Importer struct {
	store store.Store
}

// New returns an importer over the given seam.
func New(seam store.Store) *Importer { return &Importer{store: seam} }

// Import reads one suite run and writes what is not already there.
//
// Append-only and idempotent by (suite, attempt) identity: re-importing is a
// no-op, and a conflicting payload for an existing identity is REJECTED
// rather than overwritten — run records are append-only on disk and never
// rewritten, so a differing digest means the file changed, and overwriting
// would erase the evidence of exactly that.
//
// Evidence is deliberately NOT uploaded here. Attachments written during a
// partial import would be held by no artifact — the report is the only pin
// holder and does not exist until the suite is terminal — so truncation and
// the object sweep could legitimately reclaim them, and the terminal import
// would skip the ledgered attempt and never put them back. They belong to
// report assembly, which rescans every attempt (design D7).
func (i *Importer) Import(ctx context.Context, options Options) (result *Result, err error) {
	suite, err := ReadSuite(options.Dir, options.SuiteRunID)
	if err != nil {
		return nil, err
	}
	organization, err := i.store.GetOrganizationBySlug(ctx, options.OrganizationSlug)
	if err != nil {
		return nil, fmt.Errorf("resolve organization %q (bootstrap it first): %w",
			options.OrganizationSlug, err)
	}
	operator, err := i.store.GetUserByHandle(ctx, organization.OrganizationID, options.OperatorHandle)
	if err != nil {
		return nil, fmt.Errorf("resolve operator %q (bootstrap it first): %w",
			options.OperatorHandle, err)
	}
	run, err := i.store.EnsureBenchmarkRun(ctx, organization.OrganizationID, options.SuiteRunID)
	if err != nil {
		return nil, fmt.Errorf("ensure benchmark run %q: %w", options.SuiteRunID, err)
	}

	importer, err := i.systemPrincipal(ctx, organization.OrganizationID)
	if err != nil {
		return nil, err
	}
	// The importer's instance is one INVOCATION's lifetime (design D4), so it
	// is closed on the way out — including the failing way out, where the
	// instance has stopped acting just as certainly. Left open it would make
	// every import ever run look like an import still running, which is the
	// same wrong answer as an attempt's principal never stopping.
	defer func() {
		// Joined only when it fails, so a successful import returns exactly
		// the error it produced rather than one wrapped for no reason.
		if stopErr := i.stopImporter(ctx, organization.OrganizationID, importer, err); stopErr != nil {
			err = errors.Join(err, stopErr)
		}
	}()

	result = &Result{
		BenchmarkRunID: run.Record.BenchmarkRunID,
		Terminal:       suite.Manifest.Terminal(),
	}
	for index := range suite.Records {
		outcome, attemptErr := i.importAttempt(ctx, &attemptContext{
			suite:          suite,
			record:         &suite.Records[index],
			organizationID: organization.OrganizationID,
			userID:         operator.UserID,
			benchmarkRunID: run.Record.BenchmarkRunID,
			importerID:     importer,
		})
		if attemptErr != nil {
			// A failed attempt leaves every earlier one ledgered and
			// committed. The import is resumable by construction: run it
			// again and the ledgered attempts are skipped.
			return result, attemptErr
		}
		result.Attempts = append(result.Attempts, outcome)
	}
	return result, nil
}

// attemptContext carries what one attempt's import needs.
type attemptContext struct {
	suite          *Suite
	record         *Record
	organizationID uuid.UUID
	userID         uuid.UUID
	benchmarkRunID uuid.UUID
	importerID     uuid.UUID
}

// importAttempt writes one attempt, or reports that it was already there.
//
// The principal, the artifact, the metric events and the LEDGER ROW all
// commit in ONE transaction. Split across two, a crash between them leaves an
// artifact with no ledger row — and the next import writes the artifact
// again, silently duplicating the record the ledger exists to make unique.
func (i *Importer) importAttempt(ctx context.Context, attempt *attemptContext) (AttemptOutcome, error) {
	outcome := AttemptOutcome{RunID: attempt.record.RunID}

	payload, digest, err := attempt.payload()
	if err != nil {
		return outcome, err
	}

	// Checked before opening the transaction so the common case — a re-import
	// of a suite whose attempts are all present — does no write work at all.
	// The transaction below repeats the check, because between here and there
	// another importer may have ledgered it.
	existing, err := i.store.GetBenchmarkAttempt(ctx, attempt.organizationID,
		attempt.benchmarkRunID, attempt.record.RunID)
	switch {
	case err == nil && existing.RecordDigest == digest:
		return outcome, nil // the no-op
	case err == nil:
		return outcome, &store.ImportConflict{
			SuiteRunID: attempt.suite.Manifest.SuiteRunID, RunID: attempt.record.RunID,
			StoredDigest: existing.RecordDigest, OfferedDigest: digest,
		}
	case !errors.Is(err, store.ErrNotFound):
		return outcome, fmt.Errorf("read ledger for %s: %w", attempt.record.RunID, err)
	}

	err = i.store.WithTx(ctx, func(tx store.Tx) error {
		target, txErr := i.targetPrincipal(ctx, tx, attempt)
		if txErr != nil {
			return txErr
		}
		artifact, txErr := tx.CreateAuditArtifact(ctx, store.CreateAuditArtifactInput{
			Type:    TypeRunRecord,
			Summary: attempt.summary(),
			Payload: payload,
			Scope: store.Scope{
				Type: store.ScopeBenchmark,
				ID:   attempt.benchmarkRunID,
			},
			UserID: &attempt.userID,
			// Authored by the SYSTEM importer. A system principal may produce
			// Audit artifacts and may never author a Management one, which is
			// exactly why the suite report is the operator's.
			AuthorInstanceID: attempt.importerID,
			OrganizationID:   attempt.organizationID,
		})
		if txErr != nil {
			return fmt.Errorf("create run-record artifact for %s: %w", attempt.record.RunID, txErr)
		}
		if metricErr := i.writeMetricEvents(ctx, tx, attempt, target); metricErr != nil {
			return metricErr
		}
		ledger, txErr := tx.RecordBenchmarkAttempt(ctx, store.RecordBenchmarkAttemptInput{
			RunID:           attempt.record.RunID,
			RecordDigest:    digest,
			OrganizationID:  attempt.organizationID,
			BenchmarkRunID:  attempt.benchmarkRunID,
			AuditArtifactID: artifact.ArtifactID,
		})
		if txErr != nil {
			return fmt.Errorf("ledger %s: %w", attempt.record.RunID, txErr)
		}
		if !ledger.Created {
			// Another importer ledgered this attempt between the check above
			// and this transaction. Its artifact is the one that counts, so
			// roll ours back rather than leaving two for one identity.
			return errConcurrentImport
		}
		return nil
	})
	switch {
	case errors.Is(err, errConcurrentImport):
		return outcome, nil // another importer won; the attempt IS imported
	case err != nil:
		return outcome, fmt.Errorf("import attempt %s: %w", attempt.record.RunID, err)
	}
	outcome.Imported = true
	return outcome, nil
}

// errConcurrentImport rolls back an attempt another importer ledgered first.
var errConcurrentImport = errors.New("attempt ledgered concurrently")

// payload builds the artifact body and its canonical digest.
//
// The digest is over the CANONICAL payload, computed by the same machinery
// the seam uses — not over the raw JSONL line. Whitespace is not content, and
// two byte-different serializations of one record are one record; digesting
// the line would make a reformatted file look like tampering.
//
// It takes nothing but the record for the same reason. Everything in this
// body is identity (D6), so anything that varies without the attempt varying
// — the results-store directory being the one that was here — turns a
// relocated store into a conflict.
func (a *attemptContext) payload() (json.RawMessage, string, error) {
	body := RunRecordPayload{Record: *a.record}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, "", fmt.Errorf("encode run record payload: %w", err)
	}
	digest, err := canonical.DigestJSON(encoded)
	if err != nil {
		return nil, "", fmt.Errorf("digest run record payload: %w", err)
	}
	return encoded, digest, nil
}

// summary is the one-line description stored beside the artifact.
func (a *attemptContext) summary() string {
	return fmt.Sprintf("%s / %s: %s", a.record.StoryID, a.record.ConfigName, a.record.Verdict)
}

// systemPrincipal creates the importer's own principal instance.
func (i *Importer) systemPrincipal(ctx context.Context, organizationID uuid.UUID) (uuid.UUID, error) {
	instance, err := i.store.CreatePrincipalInstance(ctx, store.CreatePrincipalInstanceInput{
		Kind:           store.PrincipalSystem,
		Model:          importerModel,
		OrganizationID: organizationID,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("create importer principal: %w", err)
	}
	return instance.PrincipalInstanceID, nil
}

// stopImporter closes the importer's instance, naming how the import ended.
//
// The reason distinguishes the two, because "stopped" alone would make a
// failed import indistinguishable from a complete one at exactly the moment
// someone is asking which it was. The import error itself is not folded in:
// the reason is a lifecycle diagnostic, and an arbitrarily long wrapped
// message is a poor one.
func (i *Importer) stopImporter(ctx context.Context, organizationID, importer uuid.UUID, importErr error) error {
	reason := "import complete"
	if importErr != nil {
		reason = "import failed"
	}
	if _, err := i.store.StopPrincipalInstance(ctx, organizationID, importer, reason); err != nil {
		return fmt.Errorf("stop importer principal %s: %w", importer, err)
	}
	return nil
}

// targetPrincipal creates the principal representing the CONFIGURATION UNDER
// TEST for one attempt, carrying its MPH signature.
//
// One instance per attempt, because an instance is one acting principal's
// lifetime and each attempt is a fresh invocation. This is what makes
// FindPrincipalInstances answer "which runs used this prompt hash?" — the
// question ADR 0021 built the MPH columns for, asked here for the first time.
//
// What is lost, stated rather than smoothed over: a v1 attempt runs an
// architect AND a coder, and the record carries one MPH signature for the
// configuration rather than one per internal agent. That is the runner's
// contract — the record has nowhere to put the second — so this is one
// instance per attempt, not per agent. A future adapter reporting per-agent
// MPH maps to per-agent instances with no schema change.
//
// The lifetime is the ATTEMPT'S OWN, not the import's: start and stop from
// the record's timestamps and the stop reason from its verdict (design D4).
// Dated at import time instead, every attempt ever run would appear to have
// happened at once, and every one of them would still be running — which is
// the same MPH query above answering a question about the importer rather
// than about the runs.
func (i *Importer) targetPrincipal(ctx context.Context, tx store.Tx, attempt *attemptContext) (uuid.UUID, error) {
	mph := attempt.record.Target.MPH
	agentType := targetAgentType
	input := store.CreatePrincipalInstanceInput{
		Kind:           store.PrincipalAgent,
		Model:          mph.Model,
		AgentType:      &agentType,
		PromptPackID:   &mph.PromptPack,
		PromptHash:     &mph.PromptHash,
		OrganizationID: attempt.organizationID,
		Recorded: &store.RecordedLifetime{
			// Non-nil on every validated record: validateTimestamps requires
			// both, refuses the zero time, and refuses a finish that precedes
			// its start.
			StartTime:  *attempt.record.StartedAt,
			StopTime:   *attempt.record.FinishedAt,
			StopReason: stopReason(attempt.record),
		},
	}
	if mph.HarnessHash != "" {
		input.HarnessConfigHash = &mph.HarnessHash
	}
	if mph.MaestroVersion != "" {
		input.MaestroVersion = &mph.MaestroVersion
	}
	instance, err := tx.CreatePrincipalInstance(ctx, input)
	if err != nil {
		return uuid.Nil, fmt.Errorf("create target principal for %s: %w", attempt.record.RunID, err)
	}
	return instance.PrincipalInstanceID, nil
}

// stopReason describes why an attempt's principal stopped, in the record's
// own closed vocabulary.
//
// Closed is the point. stop_reason sits beside the MPH columns, so it is read
// by grouping — "which runs on this prompt hash failed, and how?" — and a
// vocabulary that grouping cannot rely on answers nothing. Verdict and
// failure_kind are both closed (validateVerdict enforces each against its
// set); invalid_reason is free text written by whatever refused the attempt,
// so it stays out and is read from the run-record artifact, which carries it
// whole.
func stopReason(record *Record) string {
	if record.Verdict == "failed" && record.FailureKind != "" {
		return record.Verdict + ": " + record.FailureKind
	}
	return record.Verdict
}

// writeMetricEvents records every MEASURED metric.
//
// Only the measured ones. A metric reported unsupported, not_applicable or
// unavailable has no value, and writing a zero for it would put a
// measurement in the plane that nobody made — the same fabrication the token
// axes were fixed for one layer down. Their absence is recoverable from the
// run-record artifact, which carries the whole metrics map including the
// statuses.
func (i *Importer) writeMetricEvents(ctx context.Context, tx store.Tx, attempt *attemptContext, principal uuid.UUID) error {
	labels, err := json.Marshal(map[string]string{
		"story":  attempt.record.StoryID,
		"config": attempt.record.ConfigName,
		"run_id": attempt.record.RunID,
	})
	if err != nil {
		return fmt.Errorf("encode metric labels: %w", err)
	}
	for _, spec := range metricRegistry {
		metric, present := attempt.record.Metrics[spec.key]
		if !present || metric.Status != statusValue || metric.Value == nil {
			continue
		}
		if _, err := tx.CreateMetricEvent(ctx, store.MetricEvent{
			MetricName:          spec.key,
			Value:               *metric.Value,
			Labels:              labels,
			RecordedAt:          *attempt.record.FinishedAt,
			PrincipalInstanceID: &principal,
			UserID:              &attempt.userID,
			OrganizationID:      attempt.organizationID,
		}); err != nil {
			return fmt.Errorf("record metric %s for %s: %w", spec.key, attempt.record.RunID, err)
		}
	}
	return nil
}
