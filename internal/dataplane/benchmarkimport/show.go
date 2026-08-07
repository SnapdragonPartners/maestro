package benchmarkimport

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/store"
)

// DraftBanner is what a reader of an unaccepted report must be told.
//
// It is a constant, and it is in this package rather than in the command,
// because it is a contract rather than formatting: item 9 ships assembly
// and not acceptance, so every report it can produce is a draft nobody has
// reviewed. A draft is a legitimate outcome (design D5); what would not be
// legitimate is a reader mistaking one for a finding.
//
// "Nobody", not "no second human": ADR 0020 requires a reviewer who is a
// non-author agent OR human principal, and the reviewer this report is
// waiting for is expected to be an agent (GitHub #282). The invariant is
// non-authorship, not humanity.
const DraftBanner = "DRAFT — UNREVIEWED — NOT AUTHORITATIVE"

// View is the plane's own account of one imported suite, read back through
// the seam by scope.
//
// This is the "queried back" half of the item's exit criterion, and it is
// deliberately assembled from the PLANE rather than from the results store:
// a view that consulted the files on disk would prove the files exist, which
// nobody doubted.
type View struct {
	Report          *ViewReport
	FirstImportedAt time.Time
	SuiteRunID      string
	Attempts        []ViewAttempt
	BenchmarkRunID  uuid.UUID
}

// ViewAttempt is one ledgered attempt as the plane holds it.
type ViewAttempt struct {
	ImportedAt  time.Time
	RunID       string
	StoryID     string
	ConfigName  string
	Verdict     string
	FailureKind string
	ArtifactID  uuid.UUID
}

// ViewReport is the suite report and what it holds.
type ViewReport struct {
	CreatedAt time.Time
	Status    store.Status
	Summary   string
	Pins      []ViewPin
	// SkippedEvidence is what the import did not take, carried from the
	// payload. A report is read to find out what a suite showed, and the
	// evidence it does NOT contain is part of that answer.
	SkippedEvidence []ReportSkip
	ArtifactID      uuid.UUID
}

// Draft reports whether the report still awaits a reviewer.
func (r *ViewReport) Draft() bool { return r.Status == store.StatusDraft }

// ViewPin is one thing the report holds against truncation.
type ViewPin struct {
	// Kind is "run record" or "evidence", which is the distinction a reader
	// cares about; the schema's exclusive arc is the mechanism, not the
	// meaning.
	Kind   string
	Path   string
	Digest string
	// Description is the run id for a record, or the media type for an
	// evidence file.
	Description string
	Target      uuid.UUID
	SizeBytes   int64
}

// Pin kinds.
const (
	pinKindRunRecord = "run record"
	pinKindEvidence  = "evidence"
)

// Describe reads one suite back out of the plane.
func Describe(ctx context.Context, seam store.Store, organizationSlug, suiteRunID string) (*View, error) {
	organization, err := seam.GetOrganizationBySlug(ctx, organizationSlug)
	if err != nil {
		return nil, fmt.Errorf("resolve organization %q: %w", organizationSlug, err)
	}
	run, err := seam.GetBenchmarkRunBySuite(ctx, organization.OrganizationID, suiteRunID)
	if err != nil {
		return nil, fmt.Errorf("resolve suite %q: %w", suiteRunID, err)
	}
	view := &View{
		FirstImportedAt: run.FirstImportedAt,
		SuiteRunID:      run.SuiteRunID,
		BenchmarkRunID:  run.BenchmarkRunID,
	}

	ledger, err := seam.ListBenchmarkAttempts(ctx, organization.OrganizationID, run.BenchmarkRunID)
	if err != nil {
		return nil, fmt.Errorf("list attempts of suite %q: %w", suiteRunID, err)
	}
	for index := range ledger {
		attempt, attemptErr := describeAttempt(ctx, seam, organization.OrganizationID, &ledger[index])
		if attemptErr != nil {
			return nil, attemptErr
		}
		view.Attempts = append(view.Attempts, *attempt)
	}
	sort.Slice(view.Attempts, func(i, j int) bool { return view.Attempts[i].RunID < view.Attempts[j].RunID })

	report, err := describeReport(ctx, seam, organization.OrganizationID, run.BenchmarkRunID, suiteRunID)
	if err != nil {
		return nil, err
	}
	view.Report = report
	return view, nil
}

// describeAttempt reads one attempt's verdict from the artifact that holds
// its record, not from the ledger.
//
// The ledger carries identity and nothing else — deliberately, since a
// second copy of the verdict is a second thing to keep true. So the verdict
// is read from the record the plane actually stored, which is also what
// proves the round trip: the field came back out of the envelope.
func describeAttempt(
	ctx context.Context, seam store.Store, organizationID uuid.UUID, attempt *store.BenchmarkAttempt,
) (*ViewAttempt, error) {
	artifact, err := seam.GetAuditArtifact(ctx, organizationID, attempt.AuditArtifactID)
	if err != nil {
		return nil, fmt.Errorf("read run record %s of attempt %s: %w",
			attempt.AuditArtifactID, attempt.RunID, err)
	}
	var body RunRecordPayload
	if err := decodeStrict(artifact.Payload, &body); err != nil {
		return nil, fmt.Errorf("decode run record %s: %w", attempt.AuditArtifactID, err)
	}
	return &ViewAttempt{
		ImportedAt:  attempt.ImportedAt,
		RunID:       attempt.RunID,
		StoryID:     body.Record.StoryID,
		ConfigName:  body.Record.ConfigName,
		Verdict:     body.Record.Verdict,
		FailureKind: body.Record.FailureKind,
		ArtifactID:  attempt.AuditArtifactID,
	}, nil
}

// describeReport reads the suite's report and resolves what it pins.
func describeReport(
	ctx context.Context, seam store.Store, organizationID, benchmarkRunID uuid.UUID, suiteRunID string,
) (*ViewReport, error) {
	claim, err := seam.GetSuiteReport(ctx, organizationID, benchmarkRunID)
	if errors.Is(err, store.ErrNotFound) {
		// A suite imported while it was still running has attempts and no
		// report. Absence is an ordinary state here, not a missing row.
		return nil, nil //nolint:nilnil // there is no report, and no report to describe
	}
	if err != nil {
		return nil, fmt.Errorf("read the report claim of suite %q: %w", suiteRunID, err)
	}
	// Read through the claim rather than by scanning the scope for an
	// artifact of the right type. The scope also holds the withdrawn draft
	// of any import that lost a race to report this suite, and showing one
	// of those would be showing an artifact the plane has explicitly said is
	// not the report.
	artifact, err := seam.GetManagementArtifact(ctx, organizationID, claim.ReportArtifactID)
	if err != nil {
		return nil, fmt.Errorf("read the report of suite %q: %w", suiteRunID, err)
	}

	body, err := decodeSuiteReport(artifact.Payload)
	if err != nil {
		return nil, fmt.Errorf("decode the report of suite %q: %w", suiteRunID, err)
	}
	report := &ViewReport{
		CreatedAt:  artifact.CreatedAt,
		Status:     artifact.Status,
		Summary:    artifact.Summary,
		ArtifactID: artifact.ArtifactID,
	}
	for index := range body.Attempts {
		report.SkippedEvidence = append(report.SkippedEvidence, body.Attempts[index].SkippedEvidence...)
	}

	pins, err := seam.ListPins(ctx, organizationID, artifact.ArtifactID)
	if err != nil {
		return nil, fmt.Errorf("list what the report of suite %q holds: %w", suiteRunID, err)
	}
	report.Pins = describePins(pins, body)
	return report, nil
}

// describePins renders what the report holds, naming each target from the
// payload that cited it.
//
// The pins are the authority for WHAT is held; the payload is what says
// what each one was. A pin whose target the payload does not describe is
// still listed — an unreviewed retention claim is exactly the thing worth
// seeing, and acceptance is where it is refused.
func describePins(pins []store.Pin, body *SuiteReportPayload) []ViewPin {
	records := make(map[uuid.UUID]string, len(body.Attempts))
	evidence := make(map[uuid.UUID]*ReportEvidence)
	for index := range body.Attempts {
		attempt := &body.Attempts[index]
		if id, err := uuid.Parse(attempt.RunRecordArtifactID); err == nil {
			records[id] = attempt.RunID
		}
		for fileIndex := range attempt.Evidence {
			file := &attempt.Evidence[fileIndex]
			if id, err := uuid.Parse(file.AttachmentID); err == nil {
				evidence[id] = file
			}
		}
	}

	described := make([]ViewPin, 0, len(pins))
	for index := range pins {
		pin := &pins[index]
		switch {
		case pin.AuditArtifactID != nil:
			described = append(described, ViewPin{
				Kind:        pinKindRunRecord,
				Digest:      pin.Digest,
				Description: records[*pin.AuditArtifactID],
				Target:      *pin.AuditArtifactID,
			})
		case pin.AttachmentID != nil:
			view := ViewPin{
				Kind:   pinKindEvidence,
				Digest: pin.Digest,
				Target: *pin.AttachmentID,
			}
			if file, known := evidence[*pin.AttachmentID]; known {
				view.Path, view.Description, view.SizeBytes = file.Path, file.MediaType, file.SizeBytes
			}
			described = append(described, view)
		}
	}
	sort.Slice(described, func(i, j int) bool {
		if described[i].Kind != described[j].Kind {
			return described[i].Kind < described[j].Kind
		}
		if described[i].Path != described[j].Path {
			return described[i].Path < described[j].Path
		}
		return described[i].Description < described[j].Description
	})
	return described
}
