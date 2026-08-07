package benchmarkimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/store"
)

// ErrReportStale reports that a suite already has a report, and that the
// plane no longer holds the attempts the report accounts for.
//
// It is not a conflict in D6's sense — no payload was rewritten — and it is
// not a no-op. The report is an operator's claim about a set of attempts,
// and a set that has since changed makes the claim wrong in the one
// direction a conformance record must never be wrong: quietly.
//
// The remedy is an amendment, which is acceptance's neighbour and arrives
// with it. Until then the import refuses rather than leaving a decayed
// claim in place or silently writing a second report beside it.
var ErrReportStale = errors.New("the suite's report no longer accounts for the attempts in the plane")

// ReportStale carries both sides, because the operator's next question is
// which attempts moved.
type ReportStale struct {
	SuiteRunID string
	// Unreported are ledgered attempts the report does not account for:
	// evidence imported after the report was written. Missing are attempts
	// the report accounts for that the ledger no longer holds at that
	// digest.
	Unreported []string
	Missing    []string
	ArtifactID uuid.UUID
}

func (e *ReportStale) Error() string {
	detail := make([]string, 0, 2)
	if len(e.Unreported) > 0 {
		detail = append(detail, "not accounted for: "+strings.Join(e.Unreported, ", "))
	}
	if len(e.Missing) > 0 {
		detail = append(detail, "accounted for but not in the plane: "+strings.Join(e.Missing, ", "))
	}
	return fmt.Sprintf("%s: suite %s report %s (%s)",
		ErrReportStale, e.SuiteRunID, e.ArtifactID, strings.Join(detail, "; "))
}

// Is lets callers match the sentinel without unwrapping the detail.
func (e *ReportStale) Is(target error) bool { return target == ErrReportStale }

// ErrLedgerDiverged reports a ledgered attempt with no record in the file
// the import just read.
//
// The report quotes the manifest and accounts for every ledgered attempt,
// so it can only be assembled when the plane's account of the suite and the
// store's agree. A ledger row whose record is no longer on disk means the
// JSONL was rewritten — the one thing D6 establishes never happens to an
// append-only file — and assembling a report over it would sign a claim
// about records nobody can now read.
var ErrLedgerDiverged = errors.New("the plane holds attempts the results store does not")

// ReportOutcome is what the import did about the suite report.
type ReportOutcome struct {
	ArtifactID uuid.UUID
	// Attachments and SkippedEvidence count what the report's evidence
	// rescan produced across every attempt in the suite.
	Attachments     int
	SkippedEvidence int
	// Created is false when the suite already had a report and this import
	// confirmed it still accounts for what the plane holds.
	Created bool
}

// reportContext carries what assembly needs.
type reportContext struct {
	suite          *Suite
	outcomes       []AttemptOutcome
	caps           Caps
	organizationID uuid.UUID
	userID         uuid.UUID
	benchmarkRunID uuid.UUID
	toolCallID     uuid.UUID
}

// assembleReport writes the suite's draft report, or confirms the one that
// is already there.
//
// A DRAFT, and that is the item's whole outcome: the report makes a claim,
// the plane requires an author who could be reviewed, and acceptance is a
// second explicit act by a DIFFERENT human. Shipping acceptance as an
// automatic step here would have had to manufacture a reviewer, which is
// the precise thing ADR 0020 exists to prevent (design D5).
//
// The evidence rescan covers EVERY ledgered attempt, not only the ones this
// import wrote. A partial import writes no attachments at all — they would
// be held by no artifact, and truncation could legitimately reclaim them
// before the terminal import skipped the ledgered attempt and never put
// them back — so the attempts imported earlier have had no evidence until
// now (design D7).
func (i *Importer) assembleReport(ctx context.Context, report *reportContext) (outcome *ReportOutcome, err error) {
	ledger, err := i.store.ListBenchmarkAttempts(ctx, report.organizationID, report.benchmarkRunID)
	if err != nil {
		return nil, fmt.Errorf("list ledgered attempts: %w", err)
	}
	if ledgerErr := report.checkLedgerAgrees(ledger); ledgerErr != nil {
		return nil, ledgerErr
	}

	existing, err := i.findReport(ctx, report)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if coverErr := checkReportCovers(existing, ledger, report.suite.Manifest.SuiteRunID); coverErr != nil {
			return nil, coverErr
		}
		return &ReportOutcome{ArtifactID: existing.ArtifactID}, nil
	}
	// Past here this import intends to WRITE a report, and another import of
	// the same terminal suite may be doing the same. The read above cannot
	// prevent that — it is a separate statement — so the claim below decides
	// which of them counts, and this one may still lose.

	payload, bodies, scanned, err := report.build(ledger)
	// The bodies are closed whatever happens next, including the paths that
	// never open one: a lazily-opened file is still a descriptor once
	// AttachEvidence has read it, and a failure between here and there
	// leaves them to the garbage collector otherwise.
	defer func() {
		for index := range bodies {
			if closeErr := bodies[index].Close(); closeErr != nil {
				err = errors.Join(err, closeErr)
			}
		}
	}()
	if err != nil {
		return nil, fmt.Errorf("assemble the report for suite %s: %w", report.suite.Manifest.SuiteRunID, err)
	}

	// Derived from the SAME serialized payload the artifact will carry, by
	// the SAME extractor acceptance will run. Building the pin set by
	// walking the assembled structures instead would be a second
	// implementation of the extractor, and acceptance compares the two as
	// sets — so the day they disagreed, a correct report would be refused
	// and nothing would say why.
	pins, err := pinsFor(payload)
	if err != nil {
		return nil, err
	}

	operator, err := i.operatorPrincipal(ctx, report)
	if err != nil {
		return nil, err
	}
	defer func() {
		if stopErr := i.stopPrincipal(ctx, report.organizationID, operator, "report assembled"); stopErr != nil {
			err = errors.Join(err, stopErr)
		}
	}()

	written, err := i.store.AttachEvidence(ctx, store.AttachEvidenceInput{
		Pins:        pins,
		Attachments: report.attachmentInputs(scanned, bodies),
		Artifact: store.CreateManagementArtifactInput{
			Type:    TypeSuiteReport,
			Summary: report.summary(scanned),
			Payload: payload,
			Scope: store.Scope{
				Type: store.ScopeBenchmark,
				ID:   report.benchmarkRunID,
			},
			// The machinery link, and how a reader tells an ASSEMBLED
			// report from a hand-written one: the operator is the author
			// because the claim needs one who could be reviewed, and the
			// tool call names the system importer that put it together.
			ProducedByToolCallID: &report.toolCallID,
			OrganizationID:       report.organizationID,
			UserID:               report.userID,
			AuthorInstanceID:     operator,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("write suite report for %s: %w", report.suite.Manifest.SuiteRunID, err)
	}
	return i.claimReport(ctx, report, written.Artifact.ArtifactID, ledger, &ReportOutcome{
		ArtifactID:      written.Artifact.ArtifactID,
		Attachments:     len(written.Attachments),
		SkippedEvidence: scanned.skipped(),
		Created:         true,
	})
}

// claimReport decides whether the report just written is THE report of this
// suite, and withdraws it when another import got there first.
//
// The claim cannot be part of the transaction that created the artifact —
// AttachEvidence owns its own, because attachments are remote writes that
// must land before the rows referencing them — so the order is write, then
// claim, and losing is a compensating path rather than a rollback.
//
// The loser INVALIDATES its draft rather than leaving it. Two drafts for one
// suite are two claims about one conformance run, both independently
// acceptable, and the second reviewer would have no way to know they were
// accepting a duplicate. Invalidation releases its pins in the same
// transition, which leaves its attachments unreferenced — the residue the
// object sweep already collects, and the same residue a failed import
// leaves.
func (i *Importer) claimReport(ctx context.Context, report *reportContext, artifactID uuid.UUID,
	ledger []store.BenchmarkAttempt, outcome *ReportOutcome,
) (*ReportOutcome, error) {
	claim, err := i.store.ClaimSuiteReport(ctx, store.ClaimSuiteReportInput{
		OrganizationID:   report.organizationID,
		BenchmarkRunID:   report.benchmarkRunID,
		ReportArtifactID: artifactID,
	})
	if err != nil {
		return nil, fmt.Errorf("claim the report of suite %s: %w", report.suite.Manifest.SuiteRunID, err)
	}
	if claim.Created {
		return outcome, nil
	}

	if withdrawErr := i.store.InvalidateArtifact(ctx, report.organizationID, artifactID); withdrawErr != nil {
		// Reported rather than swallowed: the plane now holds a draft
		// report that is not the suite's report and that nothing will
		// withdraw, which an operator has to know about even though the
		// suite itself is correctly reported.
		return nil, fmt.Errorf("another import claimed the report of suite %s first, and this "+
			"import's draft %s could not be withdrawn: %w",
			report.suite.Manifest.SuiteRunID, artifactID, withdrawErr)
	}

	// The winner's report still has to account for what the plane holds, by
	// the same rule that governs finding one already there.
	winner, err := i.store.GetManagementArtifact(ctx, report.organizationID, claim.Record.ReportArtifactID)
	if err != nil {
		return nil, fmt.Errorf("read the report another import wrote for suite %s: %w",
			report.suite.Manifest.SuiteRunID, err)
	}
	if coverErr := checkReportCovers(winner, ledger, report.suite.Manifest.SuiteRunID); coverErr != nil {
		return nil, coverErr
	}
	return &ReportOutcome{ArtifactID: winner.ArtifactID}, nil
}

// findReport returns the suite's existing report, or nil.
//
// Through the CLAIM, not by scanning the scope for an artifact of the right
// type. The scope holds every benchmark-scoped Management artifact of this
// run, which now includes the withdrawn draft of any import that lost a
// race — so a scan would sometimes answer with an artifact that is
// explicitly not the suite's report. The claim is the only thing that says
// which one is.
func (i *Importer) findReport(ctx context.Context, report *reportContext) (*store.ManagementArtifact, error) {
	claim, err := i.store.GetSuiteReport(ctx, report.organizationID, report.benchmarkRunID)
	if errors.Is(err, store.ErrNotFound) {
		// No report yet, which is the ordinary state of a suite whose first
		// terminal import is happening now.
		return nil, nil //nolint:nilnil // absence, with no report to describe
	}
	if err != nil {
		return nil, fmt.Errorf("read the report claim for suite %s: %w", report.suite.Manifest.SuiteRunID, err)
	}
	artifact, err := i.store.GetManagementArtifact(ctx, report.organizationID, claim.ReportArtifactID)
	if err != nil {
		return nil, fmt.Errorf("read the report of suite %s: %w", report.suite.Manifest.SuiteRunID, err)
	}
	return artifact, nil
}

// checkLedgerAgrees requires every ledgered attempt to be present in the
// file this import just read.
//
// The other direction needs no check here: every record on disk went
// through importAttempt, which either ledgered it or proved its digest
// matched the row already there.
func (r *reportContext) checkLedgerAgrees(ledger []store.BenchmarkAttempt) error {
	onDisk := make(map[string]struct{}, len(r.suite.Records))
	for index := range r.suite.Records {
		onDisk[r.suite.Records[index].RunID] = struct{}{}
	}
	var absent []string
	for index := range ledger {
		if _, present := onDisk[ledger[index].RunID]; !present {
			absent = append(absent, ledger[index].RunID)
		}
	}
	if len(absent) > 0 {
		sort.Strings(absent)
		return fmt.Errorf("%w: suite %s is ledgered with %s, which the records file does not contain",
			ErrLedgerDiverged, r.suite.Manifest.SuiteRunID, strings.Join(absent, ", "))
	}
	return nil
}

// checkReportCovers compares an existing report's account against the
// ledger.
//
// This is design D7's re-import rule, and NOT the payload-digest comparison
// D7 states. That comparison cannot be made: the payload names the
// attachment rows it pins, those identifiers are minted at assembly, and a
// second assembly of an unchanged suite therefore produces a different
// digest every time. D7 would have made the ordinary re-import of a
// reported suite a permanent conflict.
//
// What it was protecting is still protected, by the identities the plane
// already keeps. An attempt's identity is its record digest, and D6 has
// already refused any attempt offered a second time with different bytes,
// so by the time assembly runs, disagreement can only be about WHICH
// attempts the report covers. That is what this compares — and it is a
// comparison over values that do not change when the store is moved, which
// is the property D6a required of anything used as an identity.
func checkReportCovers(existing *store.ManagementArtifact, ledger []store.BenchmarkAttempt, suiteRunID string) error {
	body, err := decodeSuiteReport(existing.Payload)
	if err != nil {
		return fmt.Errorf("read the report already written for suite %s: %w", suiteRunID, err)
	}
	reported := make(map[string]string, len(body.Attempts))
	for index := range body.Attempts {
		reported[body.Attempts[index].RunID] = body.Attempts[index].RecordDigest
	}

	stale := &ReportStale{SuiteRunID: suiteRunID, ArtifactID: existing.ArtifactID}
	for index := range ledger {
		attempt := &ledger[index]
		digest, accounted := reported[attempt.RunID]
		// The digest is compared as well as the run id. An attempt covered
		// under one digest and ledgered under another is not a covered
		// attempt: it is a different record wearing the same name, and the
		// report's verdicts describe the one it was written from.
		if !accounted || digest != attempt.RecordDigest {
			stale.Unreported = append(stale.Unreported, attempt.RunID)
			continue
		}
		delete(reported, attempt.RunID)
	}
	for runID := range reported {
		stale.Missing = append(stale.Missing, runID)
	}
	if len(stale.Unreported) == 0 && len(stale.Missing) == 0 {
		return nil
	}
	sort.Strings(stale.Unreported)
	sort.Strings(stale.Missing)
	return stale
}

// scannedEvidence is one attempt's evidence, resolved to the attachment ids
// the payload will name.
type scannedEvidence struct {
	scan         *EvidenceScan
	runID        string
	dir          string
	attachmentID []uuid.UUID
}

// suiteEvidence is the whole suite's rescan, in ledger order.
type suiteEvidence []scannedEvidence

// skipped counts every file the caps and the link rule left out.
func (s suiteEvidence) skipped() int {
	total := 0
	for index := range s {
		total += len(s[index].scan.Skips)
	}
	return total
}

// build produces the report payload, the bodies its attachments will be
// read from, and the scan they came from.
//
// Everything is assembled BEFORE anything is written, which is what lets
// the attachment identifiers appear in the payload at all: a pin names a
// row, the payload names the pins, and an id allocated during the write is
// one nothing could have referenced.
func (r *reportContext) build(ledger []store.BenchmarkAttempt) (
	json.RawMessage, []*evidenceBody, suiteEvidence, error,
) {
	records := make(map[string]*Record, len(r.suite.Records))
	for index := range r.suite.Records {
		records[r.suite.Records[index].RunID] = &r.suite.Records[index]
	}
	// Ledger order is the plane's, and the plane's order is by import. The
	// payload is sorted by run id instead, so two assemblies of one suite
	// describe it identically however the rows came back.
	ordered := make([]store.BenchmarkAttempt, len(ledger))
	copy(ordered, ledger)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].RunID < ordered[j].RunID })

	unavailable := r.callsUnavailable()
	scanned := make(suiteEvidence, 0, len(ordered))
	bodies := make([]*evidenceBody, 0, len(ordered))
	payload := SuiteReportPayload{
		SuiteRunID: r.suite.Manifest.SuiteRunID,
		Manifest:   r.suite.Manifest,
		Attempts:   make([]ReportAttempt, 0, len(ordered)),
	}
	for index := range ordered {
		attempt := &ordered[index]
		record, present := records[attempt.RunID]
		if !present {
			// checkLedgerAgrees ran first and refuses exactly this, so
			// reaching it means the two disagree about what they checked.
			return nil, bodies, scanned, fmt.Errorf("%w: no record for ledgered attempt %s",
				ErrLedgerDiverged, attempt.RunID)
		}
		evidence, err := r.scanAttempt(attempt.RunID)
		if err != nil {
			return nil, bodies, scanned, err
		}
		scanned = append(scanned, *evidence)
		for fileIndex := range evidence.scan.Files {
			// Back from the payload's slash-separated name to a path this
			// platform can open. The two are deliberately different: the
			// payload records what the file WAS, portably, and only the
			// walk's own directory can say where it is.
			bodies = append(bodies, &evidenceBody{
				path: filepath.Join(evidence.dir,
					filepath.FromSlash(evidence.scan.Files[fileIndex].Path)),
			})
		}
		payload.Attempts = append(payload.Attempts, ReportAttempt{
			RunID:               attempt.RunID,
			StoryID:             record.StoryID,
			ConfigName:          record.ConfigName,
			Verdict:             record.Verdict,
			FailureKind:         record.FailureKind,
			RecordDigest:        attempt.RecordDigest,
			RunRecordArtifactID: attempt.AuditArtifactID.String(),
			Evidence:            evidence.reported(),
			SkippedEvidence:     evidence.reportedSkips(),
			CallsUnavailable:    unavailable[attempt.RunID],
		})
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, bodies, scanned, fmt.Errorf("encode suite report payload: %w", err)
	}
	return encoded, bodies, scanned, nil
}

// scanAttempt walks one attempt's evidence and allocates an identifier for
// every file that will be stored.
func (r *reportContext) scanAttempt(runID string) (*scannedEvidence, error) {
	evidence := &scannedEvidence{runID: runID, scan: &EvidenceScan{}}
	dir, present, err := r.suite.EvidenceDir(runID)
	if err != nil {
		return nil, fmt.Errorf("locate evidence for %s: %w", runID, err)
	}
	if !present {
		// An attempt with no evidence directory is reported with none. Not
		// every attempt produces evidence and a store can be pruned; the
		// item requires at least one object write per import, which every
		// real suite satisfies, and an import producing none is reported
		// as such rather than failing (design D8).
		return evidence, nil
	}
	evidence.dir = dir
	scan, err := ScanEvidence(dir, r.caps)
	if err != nil {
		return nil, fmt.Errorf("scan evidence for %s: %w", runID, err)
	}
	evidence.scan = scan
	evidence.attachmentID = make([]uuid.UUID, len(scan.Files))
	for index := range scan.Files {
		// UUIDv7, which AttachEvidence requires of a preallocated id: the
		// object module orders attachment rows by identifier, and a v4
		// would order them at random forever after.
		id, err := uuid.NewV7()
		if err != nil {
			return nil, fmt.Errorf("allocate attachment id for %s: %w", runID, err)
		}
		evidence.attachmentID[index] = id
	}
	return evidence, nil
}

// reported renders one attempt's uploaded evidence for the payload.
func (e *scannedEvidence) reported() []ReportEvidence {
	if len(e.scan.Files) == 0 {
		return nil
	}
	entries := make([]ReportEvidence, 0, len(e.scan.Files))
	for index := range e.scan.Files {
		file := &e.scan.Files[index]
		entries = append(entries, ReportEvidence{
			Path:         file.Path,
			Digest:       file.Digest,
			MediaType:    file.MediaType,
			AttachmentID: e.attachmentID[index].String(),
			SizeBytes:    file.SizeBytes,
		})
	}
	return entries
}

// reportedSkips renders what the walk left out.
func (e *scannedEvidence) reportedSkips() []ReportSkip {
	if len(e.scan.Skips) == 0 {
		return nil
	}
	skips := make([]ReportSkip, 0, len(e.scan.Skips))
	for index := range e.scan.Skips {
		skip := &e.scan.Skips[index]
		skips = append(skips, ReportSkip{
			Path: skip.Path, Reason: string(skip.Reason), Detail: skip.Detail,
		})
	}
	return skips
}

// callsUnavailable indexes the recorded absences this import observed.
//
// Only this import's, and that is a real limit worth stating: an attempt
// ledgered by an EARLIER import carries its absence on that import's tool
// call and outcome, not in the plane, so a report assembled later cannot
// recover it. What the report says is therefore "this import could not read
// calls for these attempts", which is true, rather than a claim about every
// attempt that never got any.
func (r *reportContext) callsUnavailable() map[string]string {
	reasons := make(map[string]string, len(r.outcomes))
	for index := range r.outcomes {
		outcome := &r.outcomes[index]
		if outcome.CallsUnavailable != "" {
			reasons[outcome.RunID] = outcome.CallsUnavailable
		}
	}
	return reasons
}

// attachmentInputs pairs every scanned file with the body it will be read
// from, in the order build appended them.
func (r *reportContext) attachmentInputs(scanned suiteEvidence, bodies []*evidenceBody) []store.PutAttachmentInput {
	inputs := make([]store.PutAttachmentInput, 0, len(bodies))
	position := 0
	for index := range scanned {
		evidence := &scanned[index]
		for fileIndex := range evidence.scan.Files {
			file := &evidence.scan.Files[fileIndex]
			inputs = append(inputs, store.PutAttachmentInput{
				Body:           bodies[position],
				Digest:         file.Digest,
				MediaType:      file.MediaType,
				SizeBytes:      file.SizeBytes,
				OrganizationID: r.organizationID,
				AttachmentID:   evidence.attachmentID[fileIndex],
			})
			position++
		}
	}
	return inputs
}

// summary is the one-line description stored beside the report.
func (r *reportContext) summary(scanned suiteEvidence) string {
	return fmt.Sprintf("suite %s: %d attempts, %s, %d evidence files",
		r.suite.Manifest.SuiteRunID, len(r.suite.Records), r.suite.Manifest.StopReason,
		scanned.stored())
}

// stored counts the files the rescan will upload.
func (s suiteEvidence) stored() int {
	total := 0
	for index := range s {
		total += len(s[index].scan.Files)
	}
	return total
}

// pinsFor runs the registered extractor over the serialized payload.
func pinsFor(payload json.RawMessage) ([]store.EvidenceRef, error) {
	references, err := extractSuiteReportReferences(payload)
	if err != nil {
		return nil, fmt.Errorf("derive the report's pins: %w", err)
	}
	pins := make([]store.EvidenceRef, 0, len(references))
	for index := range references {
		pins = append(pins, store.EvidenceRef(references[index]))
	}
	return pins, nil
}

// operatorPrincipal opens the human lifetime that authors the report.
//
// A HUMAN principal, because the artifact makes a claim and the plane
// requires an author who could be reviewed — and because a system principal
// may never author a Management artifact at all. The importer is machinery;
// what it cannot do is stand behind a conclusion.
func (i *Importer) operatorPrincipal(ctx context.Context, report *reportContext) (uuid.UUID, error) {
	instance, err := i.store.CreatePrincipalInstance(ctx, store.CreatePrincipalInstanceInput{
		Kind: store.PrincipalHuman,
		// ADR 0020's identity for a human principal. The seam compares it
		// at acceptance to refuse a human who authored an artifact from
		// reviewing it through a second instance.
		//
		// No agent_type, which the seam refuses on a human: the column says
		// what ROLE an agent was instantiated in, and a human is not
		// instantiated in one.
		Model:          "human-" + report.userID.String(),
		UserID:         &report.userID,
		OrganizationID: report.organizationID,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("create operator principal: %w", err)
	}
	return instance.PrincipalInstanceID, nil
}
