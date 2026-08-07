package benchmarkimport

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/registry"
)

// digestPattern is the bare lowercase SHA-256 the plane addresses content
// by, for both an artifact's canonical digest and an attachment's content
// digest. Distinct from validate.go's contentIDPattern, which is the
// RUNNER's `sha256:`-prefixed content identity — two vocabularies that look
// alike and are produced by different systems.
var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`) //nolint:gochecknoglobals // immutable after init

// SuiteReportPayload is the body of a benchmark.suite_report artifact: the
// operator's claim that this suite ran, and what it showed.
//
// It quotes the manifest whole and names every attempt with the artifact
// that holds its record and the attachments that hold its evidence. Those
// names are what the extractor turns into the pin set, which is why the
// payload carries attachment identifiers rather than only digests: a pin
// targets a row, and a digest cannot name one.
//
// What it deliberately does NOT carry is the results-store directory it was
// assembled from, or anything else that varies without the suite varying.
// D6a made that rule for the run record's payload, whose digest is an
// identity; the same rule binds here because the report quotes what the
// records say and adding a local path would put the importing machine
// inside the operator's claim.
type SuiteReportPayload struct {
	SuiteRunID string `json:"suite_run_id"`

	// Attempts is one entry per LEDGERED attempt, in run-id order.
	Attempts []ReportAttempt `json:"attempts"`

	// Manifest is the runner's own account of the matrix, quoted rather
	// than summarized. It is what makes a deliberately partial suite
	// legible: the stop reason and the per-cell statuses are the whole
	// distinction between "this is what ran" and "this is what was
	// planned", and a summary would drop exactly that.
	Manifest Manifest `json:"manifest"`
}

// ReportAttempt is one attempt as the report accounts for it.
type ReportAttempt struct {
	RunID       string `json:"run_id"`
	StoryID     string `json:"story_id"`
	ConfigName  string `json:"config_name"`
	Verdict     string `json:"verdict"`
	FailureKind string `json:"failure_kind,omitempty"`

	// RecordDigest is the ledger's identity for this attempt, and
	// RunRecordArtifactID the Audit artifact holding the record itself.
	// The artifact is pinned: Audit is truncatable by design, and a
	// conformance claim whose underlying records can be pruned is a claim
	// that decays.
	RecordDigest        string `json:"record_digest"`
	RunRecordArtifactID string `json:"run_record_artifact_id"`

	// CallsUnavailable says why an attempt contributed no call rows, and
	// is empty when they were read. A recorded absence, never a zero: a
	// surface-v1 suite cannot yield calls at all, and "no calls were
	// recorded" and "this attempt made no calls" are different claims.
	CallsUnavailable string `json:"calls_unavailable,omitempty"`

	Evidence []ReportEvidence `json:"evidence,omitempty"`

	// SkippedEvidence names what the caps and the link rule left out. In
	// the PAYLOAD and not only in the import summary, because the summary
	// is read once by whoever ran the import and the artifact is read by
	// everyone afterwards — and a conformance record that silently omits
	// evidence reads exactly like one with nothing to omit.
	SkippedEvidence []ReportSkip `json:"skipped_evidence,omitempty"`
}

// ReportEvidence is one uploaded evidence file.
type ReportEvidence struct {
	Path         string `json:"path"`
	Digest       string `json:"digest"`
	MediaType    string `json:"media_type"`
	AttachmentID string `json:"attachment_id"`
	SizeBytes    int64  `json:"size_bytes"`
}

// ReportSkip is one evidence file the import did not take.
type ReportSkip struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
}

// knownSkipReasons is the closed vocabulary a report's skips are held to.
//
// Closed for the reason every other vocabulary here is: this field is read
// by grouping — how much evidence is the cap costing us? — and a free-text
// reason answers nothing a query can ask.
//
//nolint:gochecknoglobals // Package-level set, immutable after init.
var knownSkipReasons = map[string]bool{
	string(SkipSymlink):    true,
	string(SkipIrregular):  true,
	string(SkipFileCap):    true,
	string(SkipAttemptCap): true,
}

// validateSuiteReportPayload checks a payload is a well-formed suite report.
//
// Run at the seam on whatever a caller hands the plane, which need not be
// the bytes assembly produced and on a read of an older artifact certainly
// is not.
func validateSuiteReportPayload(payload []byte) error {
	body, err := decodeSuiteReport(payload)
	if err != nil {
		return err
	}
	if !suiteRunIDPattern.MatchString(body.SuiteRunID) {
		return fmt.Errorf("benchmark.suite_report payload: suite_run_id %q is missing or malformed",
			body.SuiteRunID)
	}
	if body.Manifest.SuiteRunID != body.SuiteRunID {
		return fmt.Errorf("benchmark.suite_report payload: names suite %q and quotes a manifest for %q",
			body.SuiteRunID, body.Manifest.SuiteRunID)
	}
	// Only a TERMINAL suite gets a report (design D7). A report over a
	// running suite is a claim about a thing still happening, and the
	// manifest it quotes will legitimately differ from the one on disk by
	// the time anyone reads it.
	if !body.Manifest.Terminal() {
		return fmt.Errorf("benchmark.suite_report payload: manifest stop_reason is %q; only a terminal "+
			"suite gets a report", body.Manifest.StopReason)
	}

	subjects := make(map[string]attemptSubject, len(body.Attempts))
	seenAttachment := make(map[string]bool)
	for index := range body.Attempts {
		attempt := &body.Attempts[index]
		if err := validateReportAttempt(attempt); err != nil {
			return err
		}
		if _, seen := subjects[attempt.RunID]; seen {
			// Two entries for one attempt would double-count every verdict
			// the report is read for, and would offer the same pin twice
			// under a set comparison that cannot see the difference.
			return fmt.Errorf("benchmark.suite_report payload: attempt %q appears twice", attempt.RunID)
		}
		subjects[attempt.RunID] = attemptSubject{story: attempt.StoryID, config: attempt.ConfigName}
		if err := validateReportEvidence(attempt, seenAttachment); err != nil {
			return err
		}
	}

	// The QUOTED MANIFEST is held to the same rules the suite reader holds
	// the file to, and to the same agreement with the attempts.
	//
	// Not redundant with the reader. The reader validates what came off
	// disk; this validates what a caller hands the plane, and a caller
	// reaching the seam directly never passed the reader at all. Without
	// it, a Management artifact could be written whose manifest names an
	// unknown schema version, carries a status nothing can interpret,
	// reports an attempt as completed that the report does not account for,
	// or accounts for an attempt the manifest never planned — a claim that
	// contradicts itself, stored as though it were reviewed.
	if err := checkManifestCoherence(&body.Manifest, subjects); err != nil {
		return fmt.Errorf("benchmark.suite_report payload: %w", err)
	}
	return nil
}

// decodeSuiteReport strictly decodes a report payload.
func decodeSuiteReport(payload []byte) (*SuiteReportPayload, error) {
	var body SuiteReportPayload
	if err := decodeStrict(payload, &body); err != nil {
		return nil, fmt.Errorf("benchmark.suite_report payload: %w", err)
	}
	return &body, nil
}

// validateReportAttempt checks one attempt entry's own fields.
func validateReportAttempt(attempt *ReportAttempt) error {
	switch {
	case !runIDPattern.MatchString(attempt.RunID):
		return fmt.Errorf("benchmark.suite_report payload: run_id %q is missing or malformed", attempt.RunID)
	case strings.TrimSpace(attempt.StoryID) == "":
		return fmt.Errorf("benchmark.suite_report payload: attempt %q names no story", attempt.RunID)
	case strings.TrimSpace(attempt.ConfigName) == "":
		return fmt.Errorf("benchmark.suite_report payload: attempt %q names no config", attempt.RunID)
	case !knownVerdicts[attempt.Verdict]:
		return fmt.Errorf("benchmark.suite_report payload: attempt %q has verdict %q",
			attempt.RunID, attempt.Verdict)
	case attempt.FailureKind != "" && !knownFailureKinds[attempt.FailureKind]:
		return fmt.Errorf("benchmark.suite_report payload: attempt %q has failure_kind %q",
			attempt.RunID, attempt.FailureKind)
	case !digestPattern.MatchString(attempt.RecordDigest):
		return fmt.Errorf("benchmark.suite_report payload: attempt %q has record_digest %q",
			attempt.RunID, attempt.RecordDigest)
	}
	if err := requireIdentifier(attempt.RunRecordArtifactID); err != nil {
		return fmt.Errorf("benchmark.suite_report payload: attempt %q run_record_artifact_id: %w",
			attempt.RunID, err)
	}
	for index := range attempt.SkippedEvidence {
		skip := &attempt.SkippedEvidence[index]
		if err := checkEvidencePath(skip.Path); err != nil {
			return fmt.Errorf("benchmark.suite_report payload: attempt %q skipped evidence: %w",
				attempt.RunID, err)
		}
		if !knownSkipReasons[skip.Reason] {
			return fmt.Errorf("benchmark.suite_report payload: attempt %q skipped %q for reason %q",
				attempt.RunID, skip.Path, skip.Reason)
		}
	}
	return nil
}

// validateReportEvidence checks one attempt's evidence entries, and that no
// attachment is claimed by two attempts.
//
// The cross-attempt check matters because the pin set is compared as a SET:
// one attachment named twice collapses to one member, so a payload naming
// it under two attempts would describe evidence the pins cannot distinguish
// and an attempt whose evidence is really another's.
func validateReportEvidence(attempt *ReportAttempt, seenAttachment map[string]bool) error {
	seenPath := make(map[string]bool, len(attempt.Evidence))
	for index := range attempt.Evidence {
		evidence := &attempt.Evidence[index]
		if err := checkEvidencePath(evidence.Path); err != nil {
			return fmt.Errorf("benchmark.suite_report payload: attempt %q: %w", attempt.RunID, err)
		}
		if seenPath[evidence.Path] {
			return fmt.Errorf("benchmark.suite_report payload: attempt %q names evidence %q twice",
				attempt.RunID, evidence.Path)
		}
		seenPath[evidence.Path] = true
		switch {
		case !digestPattern.MatchString(evidence.Digest):
			return fmt.Errorf("benchmark.suite_report payload: evidence %q of %q has digest %q",
				evidence.Path, attempt.RunID, evidence.Digest)
		case strings.TrimSpace(evidence.MediaType) == "":
			return fmt.Errorf("benchmark.suite_report payload: evidence %q of %q has no media type",
				evidence.Path, attempt.RunID)
		case evidence.SizeBytes < 0:
			return fmt.Errorf("benchmark.suite_report payload: evidence %q of %q has size %d",
				evidence.Path, attempt.RunID, evidence.SizeBytes)
		}
		if err := requireIdentifier(evidence.AttachmentID); err != nil {
			return fmt.Errorf("benchmark.suite_report payload: evidence %q of %q: %w",
				evidence.Path, attempt.RunID, err)
		}
		if seenAttachment[evidence.AttachmentID] {
			return fmt.Errorf("benchmark.suite_report payload: attachment %s is named more than once",
				evidence.AttachmentID)
		}
		seenAttachment[evidence.AttachmentID] = true
	}
	return nil
}

// checkEvidencePath holds an evidence path to what the walk can produce: a
// relative, slash-separated name inside the attempt's directory.
//
// It is not a locator — the bytes are addressed by digest — but it is read
// by humans deciding what a file was, and one that climbs out of the
// directory describes a file the import could not have taken.
func checkEvidencePath(path string) error {
	switch {
	case strings.TrimSpace(path) == "":
		return fmt.Errorf("an evidence path is empty")
	case strings.HasPrefix(path, "/"):
		return fmt.Errorf("evidence path %q is absolute", path)
	case strings.Contains(path, `\`):
		return fmt.Errorf("evidence path %q is not slash-separated", path)
	}
	for _, element := range strings.Split(path, "/") {
		if element == "." || element == ".." || element == "" {
			return fmt.Errorf("evidence path %q is not a clean relative path", path)
		}
	}
	return nil
}

// requireIdentifier parses one of the payload's UUID-valued fields.
//
// String-typed in the payload rather than left to a JSON number or a raw
// byte array, per ADR 0028: identifiers are strings, and the nil UUID is
// refused because it parses cleanly and names nothing.
func requireIdentifier(value string) error {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return fmt.Errorf("%q is not a UUID: %w", value, err)
	}
	if parsed == uuid.Nil {
		return fmt.Errorf("the nil UUID names nothing")
	}
	return nil
}

// extractSuiteReportReferences reports the evidence a report payload names.
//
// This is what acceptance compares the pins against, and it is why the
// extractor had to ship with the type rather than after it: a type with NO
// extractor is not "cannot tell", it is "carries no evidence", so acceptance
// would require exactly zero pins and refuse a fully-pinned report. The
// pins are DERIVED from this same function at assembly, over the same
// serialized payload, so the two sets agree by construction rather than by
// two implementations happening to walk the payload the same way.
//
// Every attempt contributes its run-record artifact and every attachment it
// names. The run record is included because Audit artifacts are truncatable
// unless pinned, which is exactly the point of a pin that targets one.
func extractSuiteReportReferences(payload []byte) ([]registry.Reference, error) {
	body, err := decodeSuiteReport(payload)
	if err != nil {
		return nil, err
	}
	references := make([]registry.Reference, 0, len(body.Attempts))
	for index := range body.Attempts {
		attempt := &body.Attempts[index]
		record, err := uuid.Parse(attempt.RunRecordArtifactID)
		if err != nil {
			return nil, fmt.Errorf("attempt %q run_record_artifact_id %q: %w",
				attempt.RunID, attempt.RunRecordArtifactID, err)
		}
		references = append(references, registry.Reference{AuditArtifactID: &record})
		for evidenceIndex := range attempt.Evidence {
			evidence := &attempt.Evidence[evidenceIndex]
			attachment, err := uuid.Parse(evidence.AttachmentID)
			if err != nil {
				return nil, fmt.Errorf("evidence %q of %q attachment_id %q: %w",
					evidence.Path, attempt.RunID, evidence.AttachmentID, err)
			}
			references = append(references, registry.Reference{AttachmentID: &attachment})
		}
	}
	return references, nil
}
