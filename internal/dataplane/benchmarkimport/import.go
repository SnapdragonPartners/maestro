package benchmarkimport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/canonical"
	"orchestrator/internal/dataplane/store"
)

// decodeStrict decodes an artifact payload, refusing a field this build
// does not know.
//
// Strict on the way IN and on the way out. A payload carrying a field this
// build cannot interpret is one written by a schema this build does not
// read, and quietly dropping it would let a reader summarize an artifact
// while silently ignoring part of what it says.
func decodeStrict[T any](payload []byte, into *T) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return fmt.Errorf("decode payload: %w", err)
	}
	return nil
}

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

	// importToolName is the tool call the system importer makes, one per
	// suite (design D3). The suite report will name it through
	// produced_by_tool_call_id, which is how a reader tells an assembled
	// report from a hand-written one (design D5).
	importToolName = "benchmark.import"
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
	// Caps bounds the evidence one attempt may contribute. The zero value
	// is the default, never "unbounded" (design D8).
	Caps Caps
}

// AttemptOutcome is what the import did with one attempt.
type AttemptOutcome struct {
	RunID string
	// CallsUnavailable says why an imported attempt produced no call rows,
	// and is empty when they were read. It is a RECORDED ABSENCE: a
	// surface-v1 suite cannot yield calls at all, and an attempt whose
	// evidence was pruned cannot either — but neither of those is "this
	// attempt made no calls", and a zero would say exactly that (design D9).
	CallsUnavailable string
	// Calls is how many llm_calls rows the attempt produced.
	Calls int
	// Imported is false when the attempt was already ledgered with the same
	// digest — the no-op that makes re-import free.
	Imported bool
}

// Result is what one import produced.
type Result struct {
	// Report is what the import did about the suite report, and is nil for
	// a suite that has not stopped. A DRAFT is the expected outcome:
	// acceptance is a second explicit act by a principal that is not the
	// author, and item 9 deliberately does not ship it.
	Report   *ReportOutcome
	Attempts []AttemptOutcome

	BenchmarkRunID uuid.UUID
	// ToolCallID is the import's own tool call. The suite report names it
	// through produced_by_tool_call_id (design D5).
	ToolCallID uuid.UUID
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
// Append-only and idempotent by (suite, attempt) identity: re-importing writes
// no second copy of an attempt, and a conflicting payload for an existing
// identity is REJECTED rather than overwritten — run records are append-only on
// disk and never rewritten, so a differing digest means the file changed, and
// overwriting would erase the evidence of exactly that.
//
// That idempotence is over the ATTEMPTS and what they produce, not over
// everything the call writes. Every invocation opens and completes its own
// importer principal instance and its own tool call, whether or not a single
// attempt turns out to be new: that is the audit record of the invocation
// itself, and a re-import that left no trace of having been run would be the
// defect. Callers must not read this as "a re-import writes nothing".
//
// Evidence is deliberately NOT uploaded here. Attachments written during a
// partial import would be held by no artifact — the report is the only pin
// holder and does not exist until the suite is terminal — so truncation and
// the object sweep could legitimately reclaim them, and the terminal import
// would skip the ledgered attempt and never put them back. They belong to
// report assembly, which rescans every attempt (design D7).
func (i *Importer) Import(ctx context.Context, options *Options) (result *Result, err error) {
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

	toolCall, err := i.openToolCall(ctx, organization.OrganizationID, importer, options)
	if err != nil {
		return nil, err
	}
	// Registered AFTER the principal's stop, so it runs BEFORE it: defers
	// unwind last-first, and a tool call completed by a principal already
	// stopped would be a system component acting after its own lifetime.
	defer func() {
		if closeErr := i.closeToolCall(ctx, organization.OrganizationID, toolCall, result, err); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}()

	result = &Result{
		BenchmarkRunID: run.Record.BenchmarkRunID,
		Terminal:       suite.Manifest.Terminal(),
		ToolCallID:     toolCall,
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
	if !result.Terminal {
		// A suite still running imports its attempts and gets no report. It
		// acquires one on the later import that finds it finished, which is
		// what makes a suite re-importable at all: the manifest is status,
		// rewritten on every update, so storing its digest as an identity
		// would make the ordinary mid-flight re-import a conflict.
		return result, nil
	}
	report, err := i.assembleReport(ctx, &reportContext{
		suite:          suite,
		caps:           options.Caps,
		organizationID: organization.OrganizationID,
		userID:         operator.UserID,
		benchmarkRunID: run.Record.BenchmarkRunID,
		toolCallID:     toolCall,
	})
	if err != nil {
		return result, err
	}
	result.Report = report
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

	usage, err := attempt.calls()
	if err != nil {
		return outcome, err
	}

	err = i.store.WithTx(ctx, func(tx store.Tx) error {
		target, txErr := i.targetPrincipal(ctx, tx, attempt)
		if txErr != nil {
			return txErr
		}
		if callErr := i.writeCalls(ctx, tx, attempt, usage, target); callErr != nil {
			return callErr
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
			RunID:        attempt.record.RunID,
			RecordDigest: digest,
			// Written WITH the attempt, from the read that decided whether
			// to write call rows at all. A measurement and the reason it is
			// absent belong to the moment of measurement; reconstructing it
			// later asks a different question of a store that may have
			// changed (design D7e).
			CallsUnavailable: usage.Reason,
			OrganizationID:   attempt.organizationID,
			BenchmarkRunID:   attempt.benchmarkRunID,
			AuditArtifactID:  artifact.ArtifactID,
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
	outcome.Calls, outcome.CallsUnavailable = len(usage.Lines), usage.Reason
	return outcome, nil
}

// calls reads the attempt's usage log and checks it against the record.
//
// Both before the transaction opens: parsing a log is not work to hold one
// open for, and a log this build cannot trust must stop the attempt before
// any of it is written. The record and the log are two accounts of one
// attempt produced by the same run, so importing both when they disagree
// would put two contradicting authoritative accounts in the plane — the call
// rows saying one thing and the metric events beside them another, with
// nothing to say which is right (design D9a).
func (a *attemptContext) calls() (*UsageLog, error) {
	usage, err := a.suite.ReadUsageLog(a.record.RunID)
	if err != nil {
		return nil, fmt.Errorf("read calls for %s: %w", a.record.RunID, err)
	}
	if err := usage.Reconcile(a.record); err != nil {
		return nil, fmt.Errorf("attempt %s: %w", a.record.RunID, err)
	}
	return usage, nil
}

// writeCalls records one llm_calls row per usage line, opened and completed.
//
// Inside the ATTEMPT'S transaction (design D6), so calls cannot outlive a
// rolled-back attempt: a call row referring to a principal that no longer
// exists is not a partial import, it is a broken one.
//
// Attributed to the CONFIGURATION UNDER TEST, not to the importer. The
// importer moved these rows; the target made the calls, and the MPH question
// the target principal exists to answer is only meaningful if its cost sits
// with it.
func (i *Importer) writeCalls(ctx context.Context, tx store.Tx, attempt *attemptContext,
	usage *UsageLog, principal uuid.UUID,
) error {
	if !usage.Available() {
		// A recorded absence, already carried on the outcome. Nothing is
		// written here rather than zero rows being written to mean the same
		// thing, because a zero would say the attempt made no calls.
		return nil
	}
	for index := range usage.Lines {
		line := &usage.Lines[index]
		call, err := tx.CreateLLMCall(ctx, store.CreateLLMCallInput{
			Provider: *line.Provider,
			Model:    *line.Model,
			// Derived, never stored twice: the log records one instant and
			// one exact duration, so this is computed rather than read.
			StartedAt:           ptr(line.StartedAt()),
			UserID:              &attempt.userID,
			PrincipalInstanceID: principal,
			OrganizationID:      attempt.organizationID,
		})
		if err != nil {
			return fmt.Errorf("open call %d of %s: %w", index+1, attempt.record.RunID, err)
		}
		completion, err := completeCall(line, call.LLMCallID, attempt.organizationID)
		if err != nil {
			return fmt.Errorf("call %d of %s: %w", index+1, attempt.record.RunID, err)
		}
		if _, err := tx.CompleteLLMCall(ctx, completion); err != nil {
			return fmt.Errorf("complete call %d of %s: %w", index+1, attempt.record.RunID, err)
		}
	}
	return nil
}

// completeCall maps one usage line onto the seam's completion input.
//
// The seam requires tokens exactly when the call succeeded and forbids them
// otherwise, which is the same rule the line was validated against — so a
// line that got this far cannot produce a combination the seam refuses.
func completeCall(line *UsageLine, callID, organizationID uuid.UUID) (store.CompleteLLMCallInput, error) {
	completion := store.CompleteLLMCallInput{
		FinishedAt:     line.FinishedAt,
		OrganizationID: organizationID,
		LLMCallID:      callID,
		Succeeded:      *line.Success,
	}
	if *line.Success {
		completion.Tokens = &store.TokenCounts{
			Input: *line.InputTokens, Output: *line.OutputTokens,
			Reasoning: *line.ReasoningTokens, CacheRead: *line.CacheReadTokens,
			CacheWrite: *line.CacheWriteTokens,
		}
	} else {
		message := *line.Error
		completion.ErrorMessage = &message
	}
	if line.CostUSD != nil {
		cost, err := costOf(*line.CostUSD)
		if err != nil {
			return completion, err
		}
		completion.Cost = &cost
	}
	return completion, nil
}

// costOf converts the log's float64 to the seam's exact decimal.
//
// Formatted at the column's own scale rather than at the float's full
// precision: the destination is numeric(18,8), so a value carried at more
// precision than that would be rounded by the database instead of here,
// where the rounding can at least be seen. A cost too large for the column
// is refused rather than truncated — the alternative is storing a number
// that is not the one measured.
func costOf(cost float64) (store.USD, error) {
	parsed, err := store.ParseUSD(strconv.FormatFloat(cost, 'f', store.USDFractionalDigits, 64))
	if err != nil {
		return store.USD{}, fmt.Errorf("cost %v: %w", cost, err)
	}
	return parsed, nil
}

// ptr returns a pointer to a value the seam takes optionally.
func ptr[T any](value T) *T { return &value }

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

// importArguments is what the import was ASKED to do, recorded on its tool
// call.
//
// This is where the results-store directory belongs, and the only place it
// belongs: the tool call records an invocation and is not an identity, while
// an artifact payload is digested and a local path inside one turns a moved
// store into tampering (design D6a).
type importArguments struct {
	Organization string `json:"organization"`
	Operator     string `json:"operator"`
	Dir          string `json:"dir"`
	SuiteRunID   string `json:"suite_run_id"`
}

// importSummary is what the import DID, recorded as the tool call's result.
type importSummary struct {
	// The report half of the summary; see below for why the two evidence
	// counts sit beside each other.
	ReportArtifactID string `json:"report_artifact_id,omitempty"`

	Attempts int `json:"attempts"`
	Imported int `json:"imported"`
	Calls    int `json:"calls"`
	// CallsUnavailable counts imported attempts whose calls could not be
	// read. Reported rather than folded into a zero, for the reason D9 gives.
	CallsUnavailable int `json:"calls_unavailable"`

	// Attachments and SkippedEvidence are reported side by side
	// deliberately: a cap that drops work quietly reads as "there was
	// nothing more to import", and the two counts are the only place an
	// operator sees the difference at the moment they could still act on it.
	Attachments     int `json:"attachments"`
	SkippedEvidence int `json:"skipped_evidence"`

	ReportCreated bool `json:"report_created"`
	Terminal      bool `json:"terminal"`
}

// openToolCall records the invocation the importer is about to perform.
//
// One per suite, by the system importer (design D3). It is exhaust: an
// import that dies leaves an open tool call, which is a true statement about
// what happened rather than a row needing repair.
func (i *Importer) openToolCall(ctx context.Context, organizationID, importer uuid.UUID,
	options *Options,
) (uuid.UUID, error) {
	arguments, err := json.Marshal(importArguments{
		Organization: options.OrganizationSlug, Operator: options.OperatorHandle,
		Dir: options.Dir, SuiteRunID: options.SuiteRunID,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("encode import arguments: %w", err)
	}
	call, err := i.store.CreateToolCall(ctx, store.CreateToolCallInput{
		ToolName:            importToolName,
		Arguments:           arguments,
		PrincipalInstanceID: importer,
		OrganizationID:      organizationID,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("open import tool call: %w", err)
	}
	return call.ToolCallID, nil
}

// closeToolCall records what the import did, or how it failed.
//
// On the DETACHED context, for the reason D4a gives: this is a write whose
// purpose is to record how an operation ended, so it cannot depend on the
// context whose ending it is recording.
func (i *Importer) closeToolCall(ctx context.Context, organizationID, toolCall uuid.UUID,
	result *Result, importErr error,
) error {
	summary, err := json.Marshal(result.summarise())
	if err != nil {
		return fmt.Errorf("encode import summary: %w", err)
	}
	outcome := store.ToolOutcomeSucceeded
	if importErr != nil {
		outcome = store.ToolOutcomeFailed
	}
	completion := store.CompleteToolCallInput{
		Result: summary, OrganizationID: organizationID, ToolCallID: toolCall,
		Outcome: outcome,
	}
	if importErr != nil {
		message := importErr.Error()
		completion.ErrorMessage = &message
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stopImporterTimeout)
	defer cancel()
	if _, err := i.store.CompleteToolCall(cleanupCtx, completion); err != nil {
		return fmt.Errorf("complete import tool call %s: %w", toolCall, err)
	}
	return nil
}

// summarise counts what the import produced. A nil result is the import that
// failed before it had one, which is still a summary: nothing happened.
func (r *Result) summarise() importSummary {
	if r == nil {
		return importSummary{}
	}
	summary := importSummary{Attempts: len(r.Attempts), Terminal: r.Terminal}
	for index := range r.Attempts {
		attempt := &r.Attempts[index]
		if attempt.Imported {
			summary.Imported++
		}
		summary.Calls += attempt.Calls
		if attempt.Imported && attempt.CallsUnavailable != "" {
			summary.CallsUnavailable++
		}
	}
	if r.Report != nil {
		summary.ReportArtifactID = r.Report.ArtifactID.String()
		summary.ReportCreated = r.Report.Created
		summary.Attachments = r.Report.Attachments
		summary.SkippedEvidence = r.Report.SkippedEvidence
	}
	return summary
}

// stopImporterTimeout bounds the cleanup write. Detached from the caller's
// context, it needs a deadline of its own, or a cancelled import could hang
// on the one statement it has left to make.
const stopImporterTimeout = 10 * time.Second

// stopImporter closes the importer's instance, naming how the import ended.
//
// The reason distinguishes the two, because "stopped" alone would make a
// failed import indistinguishable from a complete one at exactly the moment
// someone is asking which it was. The import error itself is not folded in:
// the reason is a lifecycle diagnostic, and an arbitrarily long wrapped
// message is a poor one.
//
// The cleanup runs on a context DETACHED from the caller's. Cancellation and
// deadline expiry are the most likely ways an import fails, and they are
// exactly the cases where the caller's context can no longer carry a write —
// so reusing it would leave the instance open in precisely the situation the
// closing exists for, while looking correct in every test that does not
// cancel. Values are kept, so tracing and tenancy carried on the context
// survive; only the cancellation does not.
func (i *Importer) stopImporter(ctx context.Context, organizationID, importer uuid.UUID, importErr error) error {
	reason := "import complete"
	if importErr != nil {
		reason = "import failed"
	}
	return i.stopPrincipal(ctx, organizationID, importer, reason)
}

// stopPrincipal closes one of the import's own instances on a DETACHED
// context, bounded by its own deadline.
//
// Shared by the importer's instance and the operator's because the rule is
// the same for both and it is the rule that is easy to lose: a write whose
// purpose is to record how an operation ENDED cannot depend on the context
// whose ending it is recording. Cancellation and deadline expiry are the
// most likely ways an import fails, and they are exactly the cases where
// the caller's context can no longer carry a write — so a cleanup reusing
// it leaves the instance open in precisely the situation the closing exists
// for, while looking correct in every test that does not cancel. Values
// survive, so tracing and tenancy carried on the context do; only the
// cancellation does not.
func (i *Importer) stopPrincipal(ctx context.Context, organizationID, instance uuid.UUID, reason string) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stopImporterTimeout)
	defer cancel()
	if _, err := i.store.StopPrincipalInstance(cleanupCtx, organizationID, instance, reason); err != nil {
		return fmt.Errorf("stop principal %s: %w", instance, err)
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
