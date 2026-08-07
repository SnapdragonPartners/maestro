//go:build integration

package benchmarkimport_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/benchmarkimport"
	"orchestrator/internal/dataplane/planetest"
	"orchestrator/internal/dataplane/store"
)

// evidenceFor writes one attempt's evidence into a results store, returning
// the directory it wrote to.
//
// The layout is the store's OWN — <results>/evidence/<run-id>/ — because that
// is what the importer walks. The absolute paths the records carry are
// provenance and are never used to locate anything (design D8), so a helper
// that wrote somewhere else and recorded it would be testing a mechanism the
// importer does not have.
func evidenceFor(t *testing.T, dir, runID string, files map[string]string) string {
	t.Helper()
	attemptDir := filepath.Join(dir, "evidence", runID)
	for name, content := range files {
		path := filepath.Join(attemptDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("create evidence dir for %s: %v", runID, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write evidence %s: %v", name, err)
		}
	}
	return attemptDir
}

// suiteWithEvidence is the control shape for every report test: two accepted
// attempts, each with evidence on disk.
func suiteWithEvidence(t *testing.T) string {
	t.Helper()
	dir := twoAttemptSuite(t)
	evidenceFor(t, dir, "story-a--config--r1--aaaa1111", map[string]string{
		"pr.json":      `{"number":1}`,
		"logs/run.log": "first attempt\n",
		"events.jsonl": "{}\n",
	})
	evidenceFor(t, dir, "story-a--config--r2--bbbb2222", map[string]string{
		"pr.json": `{"number":2}`,
	})
	return dir
}

// otherSuiteWithEvidence is a second, unrelated suite, so a test can plant a
// claim that names a real report belonging to something else.
func otherSuiteWithEvidence(t *testing.T) (dir, suiteRunID string) {
	t.Helper()
	const suite = "golden-all-other"
	records := []map[string]any{
		recordWith(t, map[string]any{
			"run_id": "story-b--config--r1--cccc3333", "suite_run_id": suite,
		}),
	}
	dir = writeSuite(t, suite, records, completedManifest(suite, records))
	evidenceFor(t, dir, "story-b--config--r1--cccc3333", map[string]string{"pr.json": `{"number":3}`})
	return dir, suite
}

// reportOf reads the suite's report artifact through the claim, which is the
// only thing that says WHICH benchmark-scoped artifact is the report.
func (p *plane) reportOf(t *testing.T, benchmarkRunID uuid.UUID) *store.ManagementArtifact {
	t.Helper()
	ctx := context.Background()
	claim, err := p.store.GetSuiteReport(ctx, p.organization.OrganizationID, benchmarkRunID)
	if err != nil {
		t.Fatalf("read the report claim: %v", err)
	}
	artifact, err := p.store.GetManagementArtifact(ctx, p.organization.OrganizationID, claim.ReportArtifactID)
	if err != nil {
		t.Fatalf("read the report artifact: %v", err)
	}
	return artifact
}

// reportPayload decodes a report artifact's payload.
func reportPayload(t *testing.T, artifact *store.ManagementArtifact) benchmarkimport.SuiteReportPayload {
	t.Helper()
	var payload benchmarkimport.SuiteReportPayload
	if err := decodeJSON(artifact.Payload, &payload); err != nil {
		t.Fatalf("decode report payload: %v", err)
	}
	return payload
}

// decodeJSON strictly decodes a stored payload.
func decodeJSON(payload []byte, into any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	return decoder.Decode(into)
}

// rewriteSuite replaces a suite's files in place, which is what the runner
// does as a suite progresses: the JSONL grows and the manifest is replaced.
func rewriteSuite(t *testing.T, dir string, records []map[string]any, manifest map[string]any) {
	t.Helper()
	lines := make([]string, 0, len(records))
	for _, record := range records {
		encoded, err := json.Marshal(record)
		if err != nil {
			t.Fatalf("encode record: %v", err)
		}
		lines = append(lines, string(encoded))
	}
	if err := os.WriteFile(filepath.Join(dir, testSuiteRunID+".jsonl"),
		[]byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("rewrite records: %v", err)
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("encode manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, testSuiteRunID+".manifest.json"), encoded, 0o600); err != nil {
		t.Fatalf("rewrite manifest: %v", err)
	}
}

// attachmentCount counts every attachment row in the organization.
//
// Counted through the pool rather than the seam, because the seam offers no
// listing of attachments — deliberately, since nothing in the product needs
// one. A test asking "did anything get stored?" does.
func (p *plane) attachmentCount(t *testing.T) int {
	t.Helper()
	var count int
	if err := planetest.Pool(t, p.dsn).QueryRow(context.Background(),
		"SELECT count(*) FROM binary_attachments WHERE organization_id = $1",
		p.organization.OrganizationID).Scan(&count); err != nil {
		t.Fatalf("count attachments: %v", err)
	}
	return count
}

// managementArtifacts lists every Management artifact scoped to one suite,
// INCLUDING the withdrawn drafts of imports that lost a race.
func (p *plane) managementArtifacts(t *testing.T, benchmarkRunID uuid.UUID) []store.ManagementArtifact {
	t.Helper()
	artifacts, err := p.store.ListManagementArtifactsByScope(context.Background(),
		p.organization.OrganizationID, store.Scope{Type: store.ScopeBenchmark, ID: benchmarkRunID})
	if err != nil {
		t.Fatalf("list management artifacts: %v", err)
	}
	return artifacts
}

// managementArtifactCount is how many the suite's scope holds.
func (p *plane) managementArtifactCount(t *testing.T, benchmarkRunID uuid.UUID) int {
	t.Helper()
	return len(p.managementArtifacts(t, benchmarkRunID))
}

// reviewsOf reads an artifact's reviews, so a test can assert there are none.
func (p *plane) reviewsOf(t *testing.T, artifactID uuid.UUID) []store.Review {
	t.Helper()
	reviews, err := p.store.ListReviews(context.Background(), p.organization.OrganizationID, artifactID)
	if err != nil {
		t.Fatalf("list reviews: %v", err)
	}
	return reviews
}

// TestTerminalImportWritesADraftReportHoldingItsEvidence is the control.
//
// Every assertion below it is one mutation away from this, so without it
// they could all be passing because no report was written at all.
func TestTerminalImportWritesADraftReportHoldingItsEvidence(t *testing.T) {
	p := newPlane(t)
	result := p.mustImport(t, suiteWithEvidence(t))

	if result.Report == nil {
		t.Fatal("a terminal suite produced no report")
	}
	if !result.Report.Created {
		t.Error("the first import of a terminal suite reports the report as already present")
	}
	artifact := p.reportOf(t, result.BenchmarkRunID)

	// A DRAFT. Item 9 ships assembly and not acceptance, and an assembled
	// report that arrived accepted would mean a reviewer was manufactured.
	if artifact.Status != store.StatusDraft {
		t.Errorf("report status is %q, want %q", artifact.Status, store.StatusDraft)
	}
	if artifact.Type != benchmarkimport.TypeSuiteReport {
		t.Errorf("report type is %q", artifact.Type)
	}

	// The machinery link: authored by the OPERATOR, produced by the system
	// importer's tool call. Both, because either alone tells a reader the
	// wrong story — the author is who stands behind the claim, and the tool
	// call is how you know it was assembled rather than written by hand.
	author, err := p.store.GetPrincipalInstance(context.Background(),
		p.organization.OrganizationID, artifact.AuthorInstanceID)
	if err != nil {
		t.Fatalf("read the report's author: %v", err)
	}
	if author.Kind != store.PrincipalHuman {
		t.Errorf("the report is authored by a %s principal; only a human may author a Management "+
			"artifact that makes a claim", author.Kind)
	}
	if want := "human-" + p.operator.UserID.String(); author.Model != want {
		t.Errorf("author model is %q, want %q: acceptance compares this identity to refuse a human "+
			"reviewing their own artifact through a second instance", author.Model, want)
	}
	if artifact.ProducedByToolCallID == nil || *artifact.ProducedByToolCallID != result.ToolCallID {
		t.Errorf("report names tool call %v, want the import's own %s",
			artifact.ProducedByToolCallID, result.ToolCallID)
	}

	// Four evidence files across two attempts, plus two run records.
	payload := reportPayload(t, artifact)
	if len(payload.Attempts) != 2 {
		t.Fatalf("the report accounts for %d attempts, want 2", len(payload.Attempts))
	}
	if result.Report.Attachments != 4 {
		t.Errorf("the report stored %d evidence files, want 4", result.Report.Attachments)
	}

	pins, err := p.store.ListPins(context.Background(), p.organization.OrganizationID, artifact.ArtifactID)
	if err != nil {
		t.Fatalf("list pins: %v", err)
	}
	if len(pins) != 6 {
		t.Fatalf("the report holds %d pins, want 6 (two run records and four evidence files)", len(pins))
	}
}

// The pin set is EXACTLY the suite's run records and its stored evidence.
//
// Not "at least": acceptance compares the payload's references against the
// pins as SETS, so a missing pin is unpinned evidence and an extra one is a
// retention claim nobody reviewed.
//
// The expected set is built from the LEDGER and from the payload's own
// evidence entries, NOT from the extractor. Deriving it from the extractor
// would compare the pins against the function that produced them — a
// circular check that passes for any extractor, including one that returns
// nothing at all. The extractor is asserted separately, below, where the
// question is whether ACCEPTANCE will agree with what was pinned.
func TestTheReportPinsExactlyTheRecordsAndEvidence(t *testing.T) {
	p := newPlane(t)
	result := p.mustImport(t, suiteWithEvidence(t))
	artifact := p.reportOf(t, result.BenchmarkRunID)
	payload := reportPayload(t, artifact)

	want := map[uuid.UUID]string{}
	ledger, err := p.store.ListBenchmarkAttempts(context.Background(),
		p.organization.OrganizationID, result.BenchmarkRunID)
	if err != nil {
		t.Fatalf("list ledgered attempts: %v", err)
	}
	if len(ledger) == 0 {
		t.Fatal("no attempts were ledgered; there would be nothing to pin")
	}
	for index := range ledger {
		// Audit is truncatable by design, so a conformance claim whose
		// underlying records can be pruned is a claim that decays. Every
		// ledgered attempt's record must be held.
		want[ledger[index].AuditArtifactID] = "run record of " + ledger[index].RunID
	}
	for _, attempt := range payload.Attempts {
		for _, evidence := range attempt.Evidence {
			attachmentID, parseErr := uuid.Parse(evidence.AttachmentID)
			if parseErr != nil {
				t.Fatalf("attachment id %q: %v", evidence.AttachmentID, parseErr)
			}
			want[attachmentID] = "evidence " + evidence.Path + " of " + attempt.RunID
		}
	}

	held := map[uuid.UUID]bool{}
	pins, err := p.store.ListPins(context.Background(), p.organization.OrganizationID, artifact.ArtifactID)
	if err != nil {
		t.Fatalf("list pins: %v", err)
	}
	for index := range pins {
		pin := &pins[index]
		switch {
		case pin.AuditArtifactID != nil:
			held[*pin.AuditArtifactID] = true
		case pin.AttachmentID != nil:
			held[*pin.AttachmentID] = true
		}
	}

	for target, what := range want {
		if !held[target] {
			t.Errorf("nothing pins the %s: it is truncatable, and the report cites it", what)
		}
	}
	for target := range held {
		if _, wanted := want[target]; !wanted {
			t.Errorf("a pin holds %s, which is neither a run record of this suite nor evidence the "+
				"payload names: an unreviewed retention claim", target)
		}
	}
}

// And the REGISTERED extractor reads exactly that set out of the payload.
//
// This is the half that decides whether acceptance will agree. The
// extractor is reached through the registry rather than called directly,
// because the registry is what acceptance consults: an extractor that
// existed but was never registered would leave the expected set EMPTY, and
// a fully-pinned report would be refused for holding six unreviewed pins.
func TestTheRegisteredExtractorAgreesWithWhatWasPinned(t *testing.T) {
	p := newPlane(t)
	result := p.mustImport(t, suiteWithEvidence(t))
	artifact := p.reportOf(t, result.BenchmarkRunID)

	entry, registered := benchmarkimport.RegistryEntries()[benchmarkimport.TypeSuiteReport]
	if !registered {
		t.Fatal("benchmark.suite_report is not registered")
	}
	extractor, present := entry.Extractors[benchmarkimport.PayloadVersion]
	if !present {
		t.Fatalf("benchmark.suite_report registers no extractor for version %d: acceptance reads a "+
			"missing extractor as \"this type carries no evidence\", requires exactly zero pins, "+
			"and would refuse every report this importer writes",
			benchmarkimport.PayloadVersion)
	}
	references, err := extractor.References(artifact.Payload)
	if err != nil {
		t.Fatalf("extract references: %v", err)
	}

	named := map[uuid.UUID]bool{}
	for _, reference := range references {
		switch {
		case reference.AuditArtifactID != nil:
			named[*reference.AuditArtifactID] = true
		case reference.AttachmentID != nil:
			named[*reference.AttachmentID] = true
		}
	}
	pins, err := p.store.ListPins(context.Background(), p.organization.OrganizationID, artifact.ArtifactID)
	if err != nil {
		t.Fatalf("list pins: %v", err)
	}
	if len(named) != len(pins) {
		t.Fatalf("the extractor names %d references and the report holds %d pins: acceptance compares "+
			"these as sets and would refuse", len(named), len(pins))
	}
	for index := range pins {
		pin := &pins[index]
		target := pin.AuditArtifactID
		if target == nil {
			target = pin.AttachmentID
		}
		if target == nil || !named[*target] {
			t.Errorf("pin %s holds something the extractor does not name; acceptance would refuse it "+
				"as an unreviewed retention claim", pin.PinID)
		}
	}
}

// Every stored evidence file is retrievable, and the bytes verify.
func TestTheReportsEvidenceReadsBack(t *testing.T) {
	p := newPlane(t)
	dir := suiteWithEvidence(t)
	result := p.mustImport(t, dir)
	payload := reportPayload(t, p.reportOf(t, result.BenchmarkRunID))

	want := map[string]string{
		"story-a--config--r1--aaaa1111/pr.json":      `{"number":1}`,
		"story-a--config--r1--aaaa1111/logs/run.log": "first attempt\n",
		"story-a--config--r1--aaaa1111/events.jsonl": "{}\n",
		"story-a--config--r2--bbbb2222/pr.json":      `{"number":2}`,
	}
	seen := 0
	for _, attempt := range payload.Attempts {
		for _, evidence := range attempt.Evidence {
			attachmentID, err := uuid.Parse(evidence.AttachmentID)
			if err != nil {
				t.Fatalf("attachment id %q: %v", evidence.AttachmentID, err)
			}
			body, stored, err := p.store.GetAttachment(context.Background(),
				p.organization.OrganizationID, attachmentID)
			if err != nil {
				t.Fatalf("read evidence %s of %s: %v", evidence.Path, attempt.RunID, err)
			}
			// Read to EOF, which is where verification completes: a digest
			// mismatch surfaces as a read error there rather than as a
			// complete-looking file.
			read, err := io.ReadAll(body)
			if closeErr := body.Close(); closeErr != nil {
				t.Errorf("close evidence: %v", closeErr)
			}
			if err != nil {
				t.Fatalf("stream evidence %s: %v", evidence.Path, err)
			}
			key := attempt.RunID + "/" + evidence.Path
			if want[key] != string(read) {
				t.Errorf("evidence %s reads %q, want %q", key, read, want[key])
			}
			if stored.Digest != evidence.Digest {
				t.Errorf("evidence %s is stored at %s and the payload names %s",
					key, stored.Digest, evidence.Digest)
			}
			seen++
		}
	}
	if seen != len(want) {
		t.Errorf("the report carries %d evidence files, want %d", seen, len(want))
	}
}

// A running suite gets its attempts and NO report, and stores no evidence.
//
// The second half is the point. Attachments written during a partial import
// would be held by no artifact — the report is the only pin holder and does
// not exist yet — so truncation and the sweep could legitimately reclaim
// them, and the terminal import would skip the ledgered attempt and never
// put them back (design D7).
func TestARunningSuiteGetsNoReportAndStoresNoEvidence(t *testing.T) {
	p := newPlane(t)
	records := []map[string]any{
		recordWith(t, map[string]any{"run_id": "story-a--config--r1--aaaa1111"}),
	}
	dir := runningSuite(t, records)
	evidenceFor(t, dir, "story-a--config--r1--aaaa1111", map[string]string{"pr.json": `{"number":1}`})

	result := p.mustImport(t, dir)
	if result.Terminal {
		t.Fatal("a suite whose stop_reason is running was imported as terminal")
	}
	if result.Report != nil {
		t.Errorf("a running suite acquired a report (%s)", result.Report.ArtifactID)
	}
	if _, err := p.store.GetSuiteReport(context.Background(),
		p.organization.OrganizationID, result.BenchmarkRunID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a running suite has a report claim (%v)", err)
	}
	// No attachment rows at all, which is what makes the evidence safe: what
	// does not exist cannot be reclaimed while nothing holds it.
	if count := p.attachmentCount(t); count != 0 {
		t.Errorf("a partial import stored %d attachments; they would be held by no artifact", count)
	}
}

// The terminal import rescans EVERY attempt, including the ones an earlier
// import already ledgered.
//
// A ledgered attempt is skipped for its artifact and call rows, which are
// append-only and already correct. It is never skipped for its evidence,
// which has had no holder until now. Conflating the two skip rules is what
// caused the defect this test exists for.
func TestTheTerminalImportRescansEvidenceForAlreadyLedgeredAttempts(t *testing.T) {
	p := newPlane(t)
	first := recordWith(t, map[string]any{"run_id": "story-a--config--r1--aaaa1111"})
	dir := runningSuite(t, []map[string]any{first})
	evidenceFor(t, dir, "story-a--config--r1--aaaa1111", map[string]string{"pr.json": `{"number":1}`})

	partial := p.mustImport(t, dir)
	if importedCount(partial) != 1 {
		t.Fatalf("the partial import wrote %d attempts, want 1", importedCount(partial))
	}

	// The suite finishes: a second attempt lands and the manifest stops.
	second := recordWith(t, map[string]any{"run_id": "story-a--config--r2--bbbb2222"})
	records := []map[string]any{first, second}
	rewriteSuite(t, dir, records, completedManifest(testSuiteRunID, records))
	evidenceFor(t, dir, "story-a--config--r2--bbbb2222", map[string]string{"pr.json": `{"number":2}`})

	terminal := p.mustImport(t, dir)
	if importedCount(terminal) != 1 {
		t.Errorf("the terminal import wrote %d attempts, want 1: the first was already ledgered",
			importedCount(terminal))
	}
	if terminal.Report == nil {
		t.Fatal("the terminal import produced no report")
	}
	// TWO files: one from each attempt. One would mean the rescan covered
	// only what this import wrote, and the first attempt's evidence would be
	// in the plane nowhere at all.
	if terminal.Report.Attachments != 2 {
		t.Errorf("the report holds %d evidence files, want 2: the attempt ledgered by the earlier "+
			"import has had no evidence holder until now", terminal.Report.Attachments)
	}
	payload := reportPayload(t, p.reportOf(t, terminal.BenchmarkRunID))
	for _, attempt := range payload.Attempts {
		if len(attempt.Evidence) != 1 {
			t.Errorf("attempt %s carries %d evidence files, want 1", attempt.RunID, len(attempt.Evidence))
		}
	}
}

// What the caps and the link rule leave out is named in the PAYLOAD, not
// only in the summary.
//
// The summary is read once by whoever ran the import; the artifact is read by
// everyone afterwards. A conformance record that silently omits evidence
// reads exactly like one with nothing to omit.
func TestSkippedEvidenceIsNamedInTheReport(t *testing.T) {
	p := newPlane(t)
	records := []map[string]any{recordWith(t, map[string]any{"run_id": "story-a--config--r1--aaaa1111"})}
	dir := writeSuite(t, testSuiteRunID, records, completedManifest(testSuiteRunID, records))
	attemptDir := evidenceFor(t, dir, "story-a--config--r1--aaaa1111", map[string]string{
		"pr.json": `{"number":1}`,
		"big.log": strings.Repeat("x", 4096),
	})
	// A link INSIDE a legitimate evidence directory, pointing at a file that
	// is itself perfectly readable. It must still be skipped: containment
	// says nothing about a link three levels down, and one pointing back into
	// the store attributes one attempt's evidence to another.
	if err := os.Symlink(filepath.Join(attemptDir, "pr.json"), filepath.Join(attemptDir, "link.json")); err != nil {
		t.Fatalf("plant symlink: %v", err)
	}

	result, err := benchmarkimport.New(p.store).Import(context.Background(), &benchmarkimport.Options{
		OrganizationSlug: testOrgSlug, OperatorHandle: testOperator,
		Dir: dir, SuiteRunID: testSuiteRunID,
		// A cap that admits pr.json and refuses big.log, so the skip is the
		// cap's doing rather than an absent file.
		Caps: benchmarkimport.Caps{FileBytes: 1024},
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if result.Report.Attachments != 1 {
		t.Errorf("the report stored %d files, want 1: pr.json alone", result.Report.Attachments)
	}
	if result.Report.SkippedEvidence != 2 {
		t.Fatalf("the import reports %d skips, want 2", result.Report.SkippedEvidence)
	}

	payload := reportPayload(t, p.reportOf(t, result.BenchmarkRunID))
	skipped := map[string]string{}
	for _, attempt := range payload.Attempts {
		for _, skip := range attempt.SkippedEvidence {
			skipped[skip.Path] = skip.Reason
		}
	}
	if got := skipped["big.log"]; got != string(benchmarkimport.SkipFileCap) {
		t.Errorf("big.log is recorded as %q, want %q", got, benchmarkimport.SkipFileCap)
	}
	if got := skipped["link.json"]; got != string(benchmarkimport.SkipSymlink) {
		t.Errorf("link.json is recorded as %q, want %q", got, benchmarkimport.SkipSymlink)
	}
	// And the skipped bytes really are absent: two files on disk, one stored.
	if count := p.attachmentCount(t); count != 1 {
		t.Errorf("%d attachments were stored, want 1", count)
	}
}

// Re-importing a reported suite is a no-op, not a second report.
//
// The payload cannot be compared for this: it names the attachment rows it
// pins, those identifiers are minted at assembly, and a second assembly of
// an unchanged suite therefore produces different bytes every time. What is
// compared is the set of attempts the report accounts for (design D7a).
func TestReimportingAReportedSuiteIsANoOp(t *testing.T) {
	p := newPlane(t)
	dir := suiteWithEvidence(t)
	first := p.mustImport(t, dir)

	second := p.mustImport(t, dir)
	if second.Report == nil {
		t.Fatal("the re-import reports no report at all")
	}
	if second.Report.Created {
		t.Error("the re-import wrote a second report")
	}
	if second.Report.ArtifactID != first.Report.ArtifactID {
		t.Errorf("the re-import reports %s, want the existing %s",
			second.Report.ArtifactID, first.Report.ArtifactID)
	}
	if count := p.managementArtifactCount(t, first.BenchmarkRunID); count != 1 {
		t.Errorf("the suite has %d Management artifacts, want 1: two reports for one suite are two "+
			"claims about one conformance run", count)
	}
	// And no evidence was stored twice.
	if count := p.attachmentCount(t); count != 4 {
		t.Errorf("%d attachments exist after two imports, want 4", count)
	}
}

// A report that no longer accounts for what the plane holds is refused.
//
// This is the case D7's payload-digest comparison was reaching for and could
// not express. An attempt imported after the report was written leaves the
// report describing a suite that is no longer the suite.
func TestAReportThatNoLongerCoversTheLedgerIsRefused(t *testing.T) {
	p := newPlane(t)
	first := recordWith(t, map[string]any{"run_id": "story-a--config--r1--aaaa1111"})
	records := []map[string]any{first}
	dir := writeSuite(t, testSuiteRunID, records, completedManifest(testSuiteRunID, records))
	reported := p.mustImport(t, dir)
	if reported.Report == nil {
		t.Fatal("the first import produced no report")
	}

	// A late attempt lands in a suite that has already been reported.
	late := recordWith(t, map[string]any{"run_id": "story-a--config--r2--bbbb2222"})
	records = []map[string]any{first, late}
	rewriteSuite(t, dir, records, completedManifest(testSuiteRunID, records))

	_, err := p.importFrom(t, dir)
	if !errors.Is(err, benchmarkimport.ErrReportStale) {
		t.Fatalf("import of a suite whose report no longer covers it returned %v, want ErrReportStale", err)
	}
	if !strings.Contains(err.Error(), "story-a--config--r2--bbbb2222") {
		t.Errorf("the error does not name the attempt the report misses: %v", err)
	}
}

// Two imports of one terminal suite produce ONE report.
//
// Reading for an existing report and writing one are two statements, so
// nothing in the read prevents a second writer. The claim is what decides,
// and the loser withdraws its draft rather than leaving a second
// independently-acceptable account of the same run (ADR 0027).
func TestConcurrentTerminalImportsProduceOneReport(t *testing.T) {
	p := newPlane(t)
	dir := suiteWithEvidence(t)

	const importers = 4
	results := make([]*benchmarkimport.Result, importers)
	failures := make([]error, importers)
	var wait sync.WaitGroup
	start := make(chan struct{})
	for index := range importers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			results[index], failures[index] = p.importFrom(t, dir)
		}()
	}
	close(start)
	wait.Wait()

	created, reported := 0, map[uuid.UUID]bool{}
	for index := range importers {
		if failures[index] != nil {
			t.Fatalf("importer %d failed: %v", index, failures[index])
		}
		if results[index].Report == nil {
			t.Fatalf("importer %d produced no report", index)
		}
		if results[index].Report.Created {
			created++
		}
		reported[results[index].Report.ArtifactID] = true
	}
	if created != 1 {
		t.Errorf("%d importers claim to have created the report, want exactly 1", created)
	}
	if len(reported) != 1 {
		t.Errorf("the importers report %d different report artifacts, want 1: %v", len(reported), reported)
	}

	// The winner is a draft, and every loser's draft is withdrawn. Not
	// merely "one claim exists": an abandoned draft that stayed a draft
	// would be acceptable by a reviewer who had no way to know it was a
	// duplicate.
	claimed := p.reportOf(t, results[0].BenchmarkRunID)
	if claimed.Status != store.StatusDraft {
		t.Errorf("the claimed report is %q, want %q", claimed.Status, store.StatusDraft)
	}
	// And there is exactly ONE report artifact for the suite, not a claimed
	// one beside the withdrawn drafts of everyone who lost.
	//
	// Asserted as a count rather than by checking the losers' statuses. An
	// earlier version claimed AFTER writing, so a loser had a draft to
	// withdraw and the test looked at its status; claiming first means a
	// loser never writes one at all, and that assertion could no longer fail
	// — it iterated an empty set and passed. What survives is the property
	// that mattered all along.
	artifacts := p.managementArtifacts(t, results[0].BenchmarkRunID)
	if len(artifacts) != 1 {
		t.Errorf("the suite holds %d Management artifacts, want 1: every one of them is a claim "+
			"about the same conformance run", len(artifacts))
	}
	if len(artifacts) == 1 && artifacts[0].ArtifactID != claimed.ArtifactID {
		t.Errorf("the suite's one artifact is %s and the claim names %s",
			artifacts[0].ArtifactID, claimed.ArtifactID)
	}
}

// An import that died between claiming and writing is COMPLETED by the next
// one, under the same identifier.
//
// This is the window the claim exists to close, entered from the surviving
// side. Recording the claim first means the only inconsistent state is a
// claim whose artifact does not exist yet — recoverable, because there is
// exactly one identifier to write under and no second artifact to choose
// between. Writing first and claiming after had the opposite property: a
// death left a live, fully pinned draft that no claim named, and the retry
// wrote another.
func TestAClaimWithoutItsArtifactIsCompletedByTheNextImport(t *testing.T) {
	p := newPlane(t)
	dir := suiteWithEvidence(t)
	ctx := context.Background()

	// Stand in for the import that claimed and then died: the run row and
	// the claim exist, and nothing was ever written under the claimed id.
	run, err := p.store.EnsureBenchmarkRun(ctx, p.organization.OrganizationID, testSuiteRunID)
	if err != nil {
		t.Fatalf("ensure benchmark run: %v", err)
	}
	abandoned := uuid.Must(uuid.NewV7())
	claim, err := p.store.ClaimSuiteReport(ctx, store.ClaimSuiteReportInput{
		OrganizationID:   p.organization.OrganizationID,
		BenchmarkRunID:   run.Record.BenchmarkRunID,
		ReportArtifactID: abandoned,
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if !claim.Created {
		t.Fatal("the claim already existed; the test needs to plant it")
	}

	result := p.mustImport(t, dir)
	if result.Report == nil {
		t.Fatal("the import produced no report")
	}
	if result.Report.ArtifactID != abandoned {
		t.Errorf("the import wrote report %s, and the claim it found named %s: the claim is the "+
			"identifier, and writing a different one is how a second report comes to exist",
			result.Report.ArtifactID, abandoned)
	}
	if count := p.managementArtifactCount(t, run.Record.BenchmarkRunID); count != 1 {
		t.Errorf("the suite holds %d Management artifacts, want 1", count)
	}
	// And it is a real report: readable, draft, holding its evidence.
	artifact := p.reportOf(t, run.Record.BenchmarkRunID)
	if artifact.Status != store.StatusDraft {
		t.Errorf("the completed report is %q, want %q", artifact.Status, store.StatusDraft)
	}
	if result.Report.Attachments != 4 {
		t.Errorf("the completed report holds %d evidence files, want 4", result.Report.Attachments)
	}
}

// One artifact cannot be two suites' report.
//
// The mirror of one report per suite, and the schema is the arbiter for both.
// This is the case dropping the foreign key does NOT weaken: the uniqueness
// on (organization, artifact) still refuses it, and the seam translates the
// refusal instead of passing a constraint name to somebody holding a suite id.
func TestAClaimNamingAForeignArtifactIsRefused(t *testing.T) {
	p := newPlane(t)
	dir := suiteWithEvidence(t)
	ctx := context.Background()

	// A real, well-formed report — for a DIFFERENT suite.
	otherDir, otherSuite := otherSuiteWithEvidence(t)
	other, err := benchmarkimport.New(p.store).Import(ctx, &benchmarkimport.Options{
		OrganizationSlug: testOrgSlug, OperatorHandle: testOperator,
		Dir: otherDir, SuiteRunID: otherSuite,
	})
	if err != nil {
		t.Fatalf("import the other suite: %v", err)
	}
	if other.Report == nil {
		t.Fatal("the other suite produced no report")
	}

	run, err := p.store.EnsureBenchmarkRun(ctx, p.organization.OrganizationID, testSuiteRunID)
	if err != nil {
		t.Fatalf("ensure benchmark run: %v", err)
	}
	_, err = p.store.ClaimSuiteReport(ctx, store.ClaimSuiteReportInput{
		OrganizationID:   p.organization.OrganizationID,
		BenchmarkRunID:   run.Record.BenchmarkRunID,
		ReportArtifactID: other.Report.ArtifactID,
	})
	if !errors.Is(err, store.ErrReportAlreadyClaimed) {
		t.Fatalf("claiming another suite's report returned %v, want ErrReportAlreadyClaimed", err)
	}
	// A typed error, not the driver's. A raw 23505 at the seam describes a
	// constraint name to somebody holding a suite id.
	if strings.Contains(err.Error(), "SQLSTATE") {
		t.Errorf("the refusal leaks a driver error: %v", err)
	}

	// And the suite is still reportable: the refused claim wrote nothing.
	result := p.mustImport(t, dir)
	if result.Report == nil || !result.Report.Created {
		t.Errorf("the suite could not be reported after a refused claim: %+v", result.Report)
	}
	_ = dir
}

// Truncation cannot remove what a DRAFT report holds.
//
// This is the premise the whole item rests on: a draft is a legitimate
// outcome, and a draft's pins have to hold or the evidence behind an
// unreviewed conformance claim decays while it waits for a reviewer.
// Truncation asks only whether a row is pinned, never what the holder's
// status is — which is exactly the property that is easy to lose and
// impossible to notice, since every other test in the plane pins from an
// artifact nobody has looked at either.
//
// Stated rather than implied, because the two halves are protected by
// different things and only one of them is this test's subject:
//
//   - The ATTACHMENTS are protected by the pins alone. Nothing else in the
//     schema references an attachment row, so a report that failed to pin
//     one would lose its bytes to truncation and the sweep.
//   - The RUN RECORDS are protected by the pins AND by the ledger's own
//     ON DELETE RESTRICT reference. Either would keep them, so this test
//     cannot tell which one did — it asserts that they survive, not why.
func TestTruncationCannotRemoveWhatADraftReportHolds(t *testing.T) {
	p := newPlane(t)
	result := p.mustImport(t, suiteWithEvidence(t))
	artifact := p.reportOf(t, result.BenchmarkRunID)
	if artifact.Status != store.StatusDraft {
		t.Fatalf("the report is %q; this test is about what a DRAFT holds", artifact.Status)
	}
	if len(p.reviewsOf(t, artifact.ArtifactID)) != 0 {
		t.Fatal("the report has a review; the premise is that nobody has reviewed it")
	}

	payload := reportPayload(t, artifact)
	ctx := context.Background()

	// A horizon in the future, so EVERY Audit row and attachment is a
	// candidate. A horizon that excluded them would make this pass without
	// the pins doing anything.
	truncated, err := p.store.TruncateAuditBefore(ctx, p.organization.OrganizationID,
		time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
	for table, outcome := range truncated.PerTable {
		if !outcome.Reconciles() {
			t.Errorf("%s truncation does not reconcile: %+v", table, outcome)
		}
	}
	if audit := truncated.PerTable["audit_artifacts"]; audit.Candidates == 0 {
		t.Fatal("no Audit artifact was a truncation candidate; the pass had nothing to protect them from")
	}

	// Every pinned run record survives, and its payload still reads.
	for _, attempt := range payload.Attempts {
		artifactID, parseErr := uuid.Parse(attempt.RunRecordArtifactID)
		if parseErr != nil {
			t.Fatalf("run record id %q: %v", attempt.RunRecordArtifactID, parseErr)
		}
		stored, getErr := p.store.GetAuditArtifact(ctx, p.organization.OrganizationID, artifactID)
		if getErr != nil {
			t.Fatalf("the pinned run record of %s did not survive truncation: %v", attempt.RunID, getErr)
		}
		if stored.ArtifactID != artifactID {
			t.Errorf("read back %s for %s", stored.ArtifactID, artifactID)
		}

		// And the attachment's BYTES remain readable, which is the half a
		// surviving row does not prove: truncation removes attachment rows
		// and the sweep then reclaims the objects they made unreachable.
		for _, evidence := range attempt.Evidence {
			attachmentID, idErr := uuid.Parse(evidence.AttachmentID)
			if idErr != nil {
				t.Fatalf("attachment id %q: %v", evidence.AttachmentID, idErr)
			}
			body, _, readErr := p.store.GetAttachment(ctx, p.organization.OrganizationID, attachmentID)
			if readErr != nil {
				t.Fatalf("the pinned evidence %s of %s is not readable after truncation: %v",
					evidence.Path, attempt.RunID, readErr)
			}
			read, streamErr := io.ReadAll(body)
			if closeErr := body.Close(); closeErr != nil {
				t.Errorf("close: %v", closeErr)
			}
			if streamErr != nil {
				t.Fatalf("the pinned evidence %s streams an error after truncation: %v",
					evidence.Path, streamErr)
			}
			if int64(len(read)) != evidence.SizeBytes {
				t.Errorf("evidence %s reads %d bytes, want %d", evidence.Path, len(read), evidence.SizeBytes)
			}
		}
	}
}

// A recorded absence survives the import that did not observe it.
//
// An attempt ledgered while the suite was still running short-circuits on
// every later import: it is skipped for its artifact and call rows, which
// are already correct. It contributes an empty outcome, so a report built
// from THIS invocation's outcomes would say nothing about why that attempt
// produced no calls — and "nothing" reads as "its calls were read", which
// is the zero D9 exists to prevent arriving by a different route.
func TestTheReportKeepsAnAbsenceTheTerminalImportNeverObserved(t *testing.T) {
	p := newPlane(t)
	first := recordWith(t, map[string]any{"run_id": "story-a--config--r1--aaaa1111"})
	dir := runningSuite(t, []map[string]any{first})
	// Evidence, but no usage log: a real absence with a reason, and one that
	// the partial import DOES observe.
	evidenceFor(t, dir, "story-a--config--r1--aaaa1111", map[string]string{"pr.json": `{"number":1}`})

	partial := p.mustImport(t, dir)
	if len(partial.Attempts) != 1 || partial.Attempts[0].CallsUnavailable == "" {
		t.Fatalf("the partial import did not record the absence: %+v", partial.Attempts)
	}
	observed := partial.Attempts[0].CallsUnavailable

	// The suite finishes with a second attempt, so the terminal import has
	// its own outcome to be distracted by.
	second := recordWith(t, map[string]any{"run_id": "story-a--config--r2--bbbb2222"})
	records := []map[string]any{first, second}
	rewriteSuite(t, dir, records, completedManifest(testSuiteRunID, records))
	evidenceFor(t, dir, "story-a--config--r2--bbbb2222", map[string]string{"pr.json": `{"number":2}`})

	terminal := p.mustImport(t, dir)
	if terminal.Attempts[0].Imported {
		t.Fatal("the ledgered attempt was re-imported; then it never short-circuited and this test " +
			"would pass without exercising the defect")
	}

	payload := reportPayload(t, p.reportOf(t, terminal.BenchmarkRunID))
	for _, attempt := range payload.Attempts {
		if attempt.CallsUnavailable == "" {
			t.Errorf("attempt %s is reported as having had its calls read; neither attempt has a "+
				"usage log, and an unrecorded absence reads exactly like a measurement", attempt.RunID)
		}
	}
	for _, attempt := range payload.Attempts {
		if attempt.RunID == "story-a--config--r1--aaaa1111" && attempt.CallsUnavailable != observed {
			t.Errorf("the ledgered attempt is reported as %q, and the import that actually read the "+
				"store said %q", attempt.CallsUnavailable, observed)
		}
	}
}

// A terminal manifest that changed under a written report makes it stale,
// even when every attempt is identical.
//
// This is the case the attempt-set comparison could not see. The report
// quotes the manifest whole — the stop reason and the per-cell statuses are
// the whole distinction between "this is what ran" and "this is what was
// planned" — so a manifest that changed leaves the stored report describing
// a suite that is no longer the suite, with nothing about the attempts to
// give it away.
func TestAChangedManifestMakesTheReportStale(t *testing.T) {
	p := newPlane(t)
	records := []map[string]any{recordWith(t, map[string]any{"run_id": "story-a--config--r1--aaaa1111"})}
	dir := writeSuite(t, testSuiteRunID, records, completedManifest(testSuiteRunID, records))
	if p.mustImport(t, dir).Report == nil {
		t.Fatal("the first import produced no report")
	}

	// Same attempts, same records, same digests. Only the manifest moves:
	// the suite is now reported as having stopped for a different reason.
	manifest := completedManifest(testSuiteRunID, records)
	manifest["stop_reason"] = "suite-budget-exhausted"
	rewriteSuite(t, dir, records, manifest)

	_, err := p.importFrom(t, dir)
	if !errors.Is(err, benchmarkimport.ErrReportStale) {
		t.Fatalf("re-import over a changed manifest returned %v, want ErrReportStale", err)
	}
	// And it says WHICH part moved, because a refusal that only reports
	// "something changed" leaves the operator nothing to look at.
	if !strings.Contains(err.Error(), "the attempts are unchanged") {
		t.Errorf("the refusal does not say the attempts were not what moved: %v", err)
	}
}

// So does an evidence file that changed under it.
func TestChangedEvidenceMakesTheReportStale(t *testing.T) {
	p := newPlane(t)
	records := []map[string]any{recordWith(t, map[string]any{"run_id": "story-a--config--r1--aaaa1111"})}
	dir := writeSuite(t, testSuiteRunID, records, completedManifest(testSuiteRunID, records))
	evidenceFor(t, dir, "story-a--config--r1--aaaa1111", map[string]string{"pr.json": `{"number":1}`})
	if p.mustImport(t, dir).Report == nil {
		t.Fatal("the first import produced no report")
	}

	// The bytes behind a pinned digest change. The report's claim about what
	// it holds is now false, and no attempt moved.
	evidenceFor(t, dir, "story-a--config--r1--aaaa1111", map[string]string{"pr.json": `{"number":2}`})

	_, err := p.importFrom(t, dir)
	if !errors.Is(err, benchmarkimport.ErrReportStale) {
		t.Fatalf("re-import over changed evidence returned %v, want ErrReportStale", err)
	}
}

// Reading one suite back out of the plane: the exit criterion's second half.
func TestDescribeReadsTheSuiteBack(t *testing.T) {
	p := newPlane(t)
	result := p.mustImport(t, suiteWithEvidence(t))

	view, err := benchmarkimport.Describe(context.Background(), p.store, testOrgSlug, testSuiteRunID)
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if view.SuiteRunID != testSuiteRunID {
		t.Errorf("view names suite %q", view.SuiteRunID)
	}
	if len(view.Attempts) != 2 {
		t.Fatalf("view carries %d attempts, want 2", len(view.Attempts))
	}
	for _, attempt := range view.Attempts {
		// Read from the ARTIFACT's payload, not from the ledger, which
		// carries identity and nothing else. A verdict here is the field
		// having survived the envelope.
		if attempt.Verdict != "accepted" {
			t.Errorf("attempt %s reads verdict %q", attempt.RunID, attempt.Verdict)
		}
		if attempt.StoryID == "" || attempt.ConfigName == "" {
			t.Errorf("attempt %s came back without its story or config", attempt.RunID)
		}
	}
	if view.Report == nil {
		t.Fatal("the view carries no report")
	}
	if !view.Report.Draft() {
		t.Errorf("the report reads as %q; item 9 produces drafts only", view.Report.Status)
	}
	if len(view.Report.Pins) != 6 {
		t.Errorf("the view lists %d pins, want 6", len(view.Report.Pins))
	}
	evidence := 0
	for _, pin := range view.Report.Pins {
		if pin.Path != "" {
			evidence++
			if pin.SizeBytes == 0 {
				t.Errorf("pinned evidence %s reports no size", pin.Path)
			}
		}
	}
	if evidence != 4 {
		t.Errorf("the view describes %d evidence pins, want 4", evidence)
	}
	if result.Report.ArtifactID != view.Report.ArtifactID {
		t.Errorf("the view shows report %s, the import wrote %s",
			view.Report.ArtifactID, result.Report.ArtifactID)
	}
}
