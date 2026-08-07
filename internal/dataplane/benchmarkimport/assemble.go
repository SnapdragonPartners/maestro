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

	"orchestrator/internal/dataplane/canonical"
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
	// Detail names what moved when the attempt lists agree and the report
	// still no longer describes the suite.
	Detail string
	// Unreported are ledgered attempts the report does not account for:
	// evidence imported after the report was written. Missing are attempts
	// the report accounts for that the ledger no longer holds at that
	// digest.
	Unreported []string
	Missing    []string
	ArtifactID uuid.UUID
}

func (e *ReportStale) Error() string {
	detail := make([]string, 0, 3)
	if e.Detail != "" {
		detail = append(detail, e.Detail)
	}
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
// second explicit act by a principal that is NOT THE AUTHOR — an agent or a
// human, since ADR 0020's invariant is non-authorship rather than humanity.
// Shipping acceptance as an automatic step here would have had to
// manufacture a reviewer, which is the precise thing ADR 0020 exists to
// prevent (design D5).
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

	// The candidate is assembled BEFORE the existing report is looked up,
	// because deciding whether a re-import is a no-op means comparing the
	// report the store would produce NOW against the one already written.
	// It costs a rescan on the no-op path — every evidence file is hashed —
	// and that is the price of noticing that an evidence file changed. The
	// caps bound it, and this is an operator command rather than a hot path.
	built, err := report.build(ledger)
	// The bodies are closed whatever happens next, including the paths that
	// never open one: a lazily-opened file is still a descriptor once
	// AttachEvidence has read it, and a failure between here and there
	// leaves them to the garbage collector otherwise.
	defer func() {
		for index := range built.bodies {
			if closeErr := built.bodies[index].Close(); closeErr != nil {
				err = errors.Join(err, closeErr)
			}
		}
	}()
	if err != nil {
		return nil, fmt.Errorf("assemble the report for suite %s: %w", report.suite.Manifest.SuiteRunID, err)
	}

	existing, err := i.findReport(ctx, report)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if staleErr := checkReportStillDescribes(existing, built.payload,
			report.suite.Manifest.SuiteRunID); staleErr != nil {
			return nil, staleErr
		}
		return &ReportOutcome{ArtifactID: existing.ArtifactID}, nil
	}
	// Past here this import intends to WRITE a report, and another import of
	// the same terminal suite may be doing the same. The read above cannot
	// prevent that — it is a separate statement — so the claim below decides
	// which of them counts, and this one may still lose.

	// Derived from the SAME serialized payload the artifact will carry, by
	// the SAME extractor acceptance will run. Building the pin set by
	// walking the assembled structures instead would be a second
	// implementation of the extractor, and acceptance compares the two as
	// sets — so the day they disagreed, a correct report would be refused
	// and nothing would say why.
	pins, err := pinsFor(built.payload)
	if err != nil {
		return nil, err
	}

	operator, err := i.operatorPrincipal(ctx, report)
	if err != nil {
		return nil, err
	}
	defer func() {
		// The reason distinguishes the two, for the same reason the
		// importer's does: "stopped" alone makes a failed assembly
		// indistinguishable from a complete one at exactly the moment
		// someone is asking which it was.
		reason := "report assembled"
		if err != nil {
			reason = "report assembly failed"
		}
		if stopErr := i.stopPrincipal(ctx, report.organizationID, operator, reason); stopErr != nil {
			err = errors.Join(err, stopErr)
		}
	}()

	written, err := i.store.AttachEvidence(ctx, store.AttachEvidenceInput{
		Pins:        pins,
		Attachments: built.attachments,
		Artifact: store.CreateManagementArtifactInput{
			Type:    TypeSuiteReport,
			Summary: report.summary(built.scanned),
			Payload: built.payload,
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
	return i.claimReport(ctx, report, written.Artifact.ArtifactID, built.payload, &ReportOutcome{
		ArtifactID:      written.Artifact.ArtifactID,
		Attachments:     len(written.Attachments),
		SkippedEvidence: built.scanned.skipped(),
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
	payload []byte, outcome *ReportOutcome,
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
	if staleErr := checkReportStillDescribes(winner, payload,
		report.suite.Manifest.SuiteRunID); staleErr != nil {
		return nil, staleErr
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

// checkReportStillDescribes compares the report already written against the
// one this import would write now.
//
// This is design D7's re-import rule as amended twice. D7 asked for
// "payload-digest agreement", which cannot be computed: the payload names
// the attachment ROWS it pins, those identifiers are minted at assembly, and
// a second assembly of an unchanged suite therefore digests differently
// every time (D7a). The first fix compared only the ATTEMPTS the report
// covered, and that was too little: the report also quotes the terminal
// manifest, so a stop reason, budget account, cell status or timestamp could
// change under a report that kept describing the old one, and the import
// would call it a no-op.
//
// So the comparison is over a STABLE PROJECTION: everything the payload
// claims, with only the minted attachment identifiers normalized away. The
// run-record artifact ids stay in — those are the ledger's, and they do not
// change when a suite is re-imported from a moved store, which is exactly
// the property D6a requires of anything used as an identity.
//
// The attempt-level diff is still computed, and only to say WHICH attempts
// moved. A digest can say that something changed; an operator needs to know
// what.
func checkReportStillDescribes(existing *store.ManagementArtifact, candidate []byte, suiteRunID string) error {
	stored, err := stableProjection(existing.Payload)
	if err != nil {
		return fmt.Errorf("read the report already written for suite %s: %w", suiteRunID, err)
	}
	offered, err := stableProjection(candidate)
	if err != nil {
		return fmt.Errorf("project the report for suite %s: %w", suiteRunID, err)
	}
	if stored == offered {
		return nil
	}

	return describeStaleness(existing, candidate, suiteRunID)
}

// describeStaleness says WHICH part of the report no longer describes the
// suite, once the projections are known to differ.
func describeStaleness(existing *store.ManagementArtifact, candidate []byte, suiteRunID string) error {
	storedBody, err := decodeSuiteReport(existing.Payload)
	if err != nil {
		return fmt.Errorf("read the report already written for suite %s: %w", suiteRunID, err)
	}
	offeredBody, err := decodeSuiteReport(candidate)
	if err != nil {
		return fmt.Errorf("project the report for suite %s: %w", suiteRunID, err)
	}

	stale := &ReportStale{SuiteRunID: suiteRunID, ArtifactID: existing.ArtifactID}
	reported := make(map[string]string, len(storedBody.Attempts))
	for index := range storedBody.Attempts {
		reported[storedBody.Attempts[index].RunID] = storedBody.Attempts[index].RecordDigest
	}
	for index := range offeredBody.Attempts {
		attempt := &offeredBody.Attempts[index]
		digest, accounted := reported[attempt.RunID]
		// The digest is compared as well as the run id. An attempt covered
		// under one digest and offered under another is not a covered
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
	sort.Strings(stale.Unreported)
	sort.Strings(stale.Missing)

	if len(stale.Unreported) == 0 && len(stale.Missing) == 0 {
		// The attempts agree and the projection does not, so what moved is
		// the manifest the report quotes or the evidence it describes. Named
		// rather than left as "something changed": those are the two things
		// a report claims besides its attempts, and an operator staring at a
		// refusal needs to know which of them to look at.
		stale.Detail = "the attempts are unchanged; the quoted manifest or the evidence they " +
			"describe is not"
	}
	return stale
}

// stableProjection is the digest of everything a report claims, with the
// minted attachment identifiers removed.
func stableProjection(payload []byte) (string, error) {
	body, err := decodeSuiteReport(payload)
	if err != nil {
		return "", err
	}
	for index := range body.Attempts {
		evidence := body.Attempts[index].Evidence
		for fileIndex := range evidence {
			// The ONE field that varies without the suite varying. The
			// digest beside it does not: it addresses the same bytes on
			// every assembly, so a changed evidence file still shows up.
			evidence[fileIndex].AttachmentID = ""
		}
	}
	digest, err := canonical.Digest(body)
	if err != nil {
		return "", fmt.Errorf("digest the report projection: %w", err)
	}
	return digest, nil
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

// assembled is everything one report needs before anything is written.
//
// The attachment inputs are built in the SAME pass that mints their
// identifiers and writes them into the payload, rather than by a second
// walk that has to agree with the first about order. A parallel-array
// pairing would be correct today and silently wrong the first time either
// walk changed — and "silently wrong" here means one file's bytes stored
// under another file's digest.
type assembled struct {
	payload     json.RawMessage
	attachments []store.PutAttachmentInput
	bodies      []*evidenceBody
	scanned     suiteEvidence
}

// build produces everything the report needs, before anything is written.
//
// Assembled first, which is what lets the attachment identifiers appear in
// the payload at all: a pin names a row, the payload names the pins, and an
// id allocated during the write is one nothing could have referenced.
func (r *reportContext) build(ledger []store.BenchmarkAttempt) (*assembled, error) {
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

	built := &assembled{scanned: make(suiteEvidence, 0, len(ordered))}
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
			return built, fmt.Errorf("%w: no record for ledgered attempt %s",
				ErrLedgerDiverged, attempt.RunID)
		}
		evidence, err := r.scanAttempt(attempt.RunID)
		if err != nil {
			return built, err
		}
		built.scanned = append(built.scanned, *evidence)
		for fileIndex := range evidence.scan.Files {
			file := &evidence.scan.Files[fileIndex]
			// Back from the payload's slash-separated name to a path this
			// platform can open. The two are deliberately different: the
			// payload records what the file WAS, portably, and only the
			// walk's own directory can say where it is.
			body := &evidenceBody{path: filepath.Join(evidence.dir, filepath.FromSlash(file.Path))}
			built.bodies = append(built.bodies, body)
			built.attachments = append(built.attachments, store.PutAttachmentInput{
				Body:           body,
				Digest:         file.Digest,
				MediaType:      file.MediaType,
				SizeBytes:      file.SizeBytes,
				OrganizationID: r.organizationID,
				AttachmentID:   evidence.attachmentID[fileIndex],
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
			CallsUnavailable:    r.callsUnavailable(attempt.RunID),
		})
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return built, fmt.Errorf("encode suite report payload: %w", err)
	}
	built.payload = encoded
	return built, nil
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

// callsUnavailable re-reads one attempt's usage log at ASSEMBLY time and
// reports why it yields no calls, or "" when it does.
//
// Re-read rather than carried over from this invocation's outcomes, which is
// what the first version did and which quietly lost the fact. An attempt
// ledgered by an EARLIER import short-circuits before the usage log is
// opened at all, so it contributes an empty outcome -- and a surface-v1
// attempt imported while the suite was still running would therefore appear
// in the terminal report as one whose calls were read. A recorded absence
// that disappears is worse than one that was never recorded: it is the zero
// D9 exists to prevent, arriving by a different route.
//
// What the rescan can and cannot know, stated rather than implied: it
// describes the STORE as it stands at report time. For an append-only
// evidence tree beside an append-only record that is the same fact the
// import saw, and the cases that matter -- a surface-v1 suite, a log that
// was never written, an attempt with no evidence directory -- do not change
// with time. A log that has since become unreadable is reported as an
// absence with the failure named, rather than failing the whole report: by
// then the attempt's calls are either in the plane or they are not, and
// refusing to describe the suite would not put them there.
func (r *reportContext) callsUnavailable(runID string) string {
	usage, err := r.suite.ReadUsageLog(runID)
	if err != nil {
		return "the usage log could not be read: " + err.Error()
	}
	return usage.Reason
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
