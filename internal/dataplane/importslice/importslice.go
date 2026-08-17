// Package importslice runs one benchmark-import vertical slice against any
// store composition.
//
// It exists for the first acceptance criterion of #286: the IDENTICAL
// benchmark-import vertical slice must pass against local Docker and one
// managed cloud configuration. "Identical" has to mean one body of code called
// twice rather than two suites that resemble each other, because two copies
// drift — and they would drift exactly where the portability claim lives.
//
// It deliberately does NOT duplicate the importer's own suite. That suite pins
// the importer's behaviour in detail against the local plane, and most of what
// it covers has no provider surface at all: manifest coherence, path
// containment, evidence absence and digest identity are all decided before a
// store is touched. What has to be shown twice is that the same slice over a
// different composition writes the same thing and then declines to duplicate it.
// Running the whole suite twice would spend managed-service time re-proving
// logic that Postgres and the object store never see.
//
// The slice is a WRITE followed by a RE-OFFER, because those are the two halves
// that actually reach the store. The write has to land an attempt whole across
// the ledger, the Audit artifact and the benchmark run; the re-offer has to find
// that prior work and add no duplicate of it. A composition that stored the
// record digest differently, or committed in a different order, passes the first
// half and fails the second — which is why a write-only slice would not be
// enough.
//
// # What the re-offer does NOT claim
//
// It is not "re-import writes nothing", and the difference is worth stating
// because the stronger sentence is the one a later reader would rely on. Every
// import invocation opens its own importer principal instance and its own tool
// call, and completes them, whether or not a single attempt turns out to be new —
// that is the audit trail of the invocation itself, and recording it is correct.
//
// What is asserted is narrower and is what idempotence actually means here: no
// duplicate ATTEMPT ledger rows, no duplicate run-record artifacts in the suite's
// scope, no second report, no re-uploaded evidence, and no movement in the record
// digests already stored. A slice claiming global write-freedom would be false
// against the local plane too, so it would not even be a portability statement.
package importslice

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/benchmarkimport"
	"orchestrator/internal/dataplane/store"
)

// The tenant, operator and suite this slice owns.
//
// Its own rather than the importer suite's, so that running the slice against a
// plane that suite has already used cannot make either depend on the other's
// rows. Bootstrapping is idempotent by natural key, so a repeat run against a
// surviving plane resolves rather than conflicts.
const (
	organizationSlug = "portability"
	organizationName = "Portability"
	operatorHandle   = "portability-operator"
	operatorName     = "Portability Operator"
	suiteRunID       = "portability-probe"
)

// The two attempts this slice imports.
const (
	firstRunID  = "story-a--config--r1--aaaa1111"
	secondRunID = "story-a--config--r2--bbbb2222"
)

// corpusControl is the accepted control case of the two-sided import corpus,
// relative to the repository root.
//
// The corpus is used rather than a record invented here because both this
// module's validator and the runner's must agree on it. A record written for
// this test alone could be one neither validator would accept in production,
// and the slice would then be proving portability of a shape nothing emits.
const corpusControl = "benchmark/testdata/import_corpus/accept_minimal.json"

// Run imports one two-attempt suite through seam, requires the plane to hold it
// whole, and then requires the same bytes offered again to add no duplicate
// records.
//
// The seam MUST have been opened with benchmarkimport.RegistryEntries(). The
// importer's payloads are refused by a registry that does not know its types, so
// a seam carrying any other vocabulary fails here in a way that looks like an
// import defect rather than the configuration error it is.
//
// claimObjects is called with the organization as soon as it exists and BEFORE
// anything can write an object. It is a callback rather than a return value on
// purpose: a returned organization arrives only when the slice SUCCEEDS, and the
// run that needs cleanup most is the one that dies partway through, having
// written objects that nothing has claimed. A caller whose object store outlives
// its database — the managed case — installs its purge here, because dropping
// that database removes the only rows naming the digests this slice wrote. Use
// NoCleanup where both stores are disposed together.
func Run(t *testing.T, seam store.Store, claimObjects func(organizationID uuid.UUID)) {
	t.Helper()
	ctx := context.Background()

	organizationID := bootstrapTenant(ctx, t, seam)
	// Before twoAttemptSuite and before the first import: everything below this
	// line can fail, and some of it fails after writing to the object store.
	claimObjects(organizationID)

	dir := twoAttemptSuite(t)

	first := mustImport(ctx, t, seam, dir)
	assertWroteTheSuiteWhole(ctx, t, seam, organizationID, first)
	assertPrincipalFamily(ctx, t, seam, organizationID)
	assertReportCarriesItsEvidence(ctx, t, seam, organizationID, first)
	assertReimportAddsNoDomainRecords(ctx, t, seam, organizationID, dir, first)
}

// NoCleanup is the claim callback for a caller that needs none.
//
// Correct only where the object store is disposed with the database — planetest
// gives each local test its own bucket and removes it, so nothing outlives the
// run. It is a named function rather than an inline empty closure so that a call
// site states this rather than looking like an oversight.
func NoCleanup(uuid.UUID) {}

// assertWroteTheSuiteWhole requires every family one attempt produces to be
// present and to refer to the same attempt.
func assertWroteTheSuiteWhole(
	ctx context.Context, t *testing.T, seam store.Store,
	organizationID uuid.UUID, result *benchmarkimport.Result,
) {
	t.Helper()
	if len(result.Attempts) != 2 || importedCount(result) != 2 {
		t.Fatalf("imported %d of %d attempts, want 2 of 2", importedCount(result), len(result.Attempts))
	}
	if !result.Terminal {
		t.Error("a suite whose manifest stop_reason is 'completed' must read as terminal")
	}

	for _, runID := range runIDsOf(result) {
		ledgered, err := seam.GetBenchmarkAttempt(ctx, organizationID, result.BenchmarkRunID, runID)
		if err != nil {
			t.Fatalf("read the ledger row for %s: %v", runID, err)
		}
		if ledgered.RecordDigest == "" {
			t.Errorf("%s: the ledger row carries no record digest", runID)
		}
		// The artifact as well as the ledger row. The row is a reference; the
		// record itself lives in the artifact, and a composition that wrote one
		// without the other would satisfy every count above.
		stored := storedRecord(ctx, t, seam, organizationID, ledgered.AuditArtifactID)
		if stored.RunID != runID {
			t.Errorf("the artifact for %s carries record %s", runID, stored.RunID)
		}
		if stored.Verdict != "accepted" || stored.SuiteRunID != suiteRunID {
			t.Errorf("%s round-tripped as verdict %q of suite %q, want \"accepted\" of %q",
				runID, stored.Verdict, stored.SuiteRunID, suiteRunID)
		}
	}

	run, err := seam.GetBenchmarkRunBySuite(ctx, organizationID, suiteRunID)
	if err != nil {
		t.Fatalf("read the benchmark run back by suite id: %v", err)
	}
	if run.BenchmarkRunID != result.BenchmarkRunID {
		t.Errorf("the import reports run %s, the plane stores %s",
			result.BenchmarkRunID, run.BenchmarkRunID)
	}
}

// assertPrincipalFamily requires one target principal per attempt.
//
// Each attempt's target stands for the configuration under test, and every record
// in this suite names the same MPH — so an import that wrote no principals, or
// collapsed two attempts onto one instance, is visible here and nowhere else in
// this slice.
func assertPrincipalFamily(
	ctx context.Context, t *testing.T, seam store.Store, organizationID uuid.UUID,
) {
	t.Helper()
	model := corpusModel(t)
	targets, err := seam.FindPrincipalInstances(ctx, store.MPHQuery{
		OrganizationID: organizationID, Model: &model,
	})
	if err != nil {
		t.Fatalf("find the target principal instances: %v", err)
	}
	if len(targets) != 2 {
		t.Errorf("the plane holds %d target instances for model %s, want one per attempt (2)",
			len(targets), model)
	}
}

// assertReportCarriesItsEvidence is the object half, and what makes this slice
// vertical rather than a Postgres-only exercise.
//
// A terminal suite gets a report, and the suite's evidence is attached to it and
// pinned by it — so the bytes went through the composed object store, and come
// back through the seam's VERIFYING path, which recomputes the digest over the
// whole stream.
func assertReportCarriesItsEvidence(
	ctx context.Context, t *testing.T, seam store.Store,
	organizationID uuid.UUID, result *benchmarkimport.Result,
) {
	t.Helper()
	if result.Report == nil {
		t.Fatal("a terminal suite must produce a report; without one no evidence was attached and " +
			"the object store was never reached")
	}
	if !result.Report.Created {
		t.Error("the first import of a suite must CREATE its report")
	}
	if result.Report.Attachments != 2 {
		t.Errorf("the report carries %d attachments, want one per attempt (2)",
			result.Report.Attachments)
	}
	if result.Report.SkippedEvidence != 0 {
		t.Errorf("the report skipped %d evidence files; this fixture writes none that any cap or "+
			"rule should exclude", result.Report.SkippedEvidence)
	}
	assertPinnedEvidenceReadsBack(ctx, t, seam, organizationID, result.Report.ArtifactID)
}

// assertReimportAddsNoDomainRecords requires the same bytes offered again to
// duplicate none of the suite's records.
//
// DOMAIN records specifically — the attempts, their run-record artifacts, the
// report and its evidence. A second invocation still opens and completes its own
// importer principal and tool call, which is the audit trail of the invocation
// rather than a duplicate of the suite's contents; see the package comment. The
// artifact count below is scoped to the benchmark run for exactly that reason,
// so it measures the suite's contents and not everything the plane holds.
//
// Counted before and after, because "added nothing" is a claim about the plane
// and not only about what the importer reported about itself.
func assertReimportAddsNoDomainRecords(
	ctx context.Context, t *testing.T, seam store.Store,
	organizationID uuid.UUID, dir string, first *benchmarkimport.Result,
) {
	t.Helper()
	before := auditArtifactCount(ctx, t, seam, organizationID, first.BenchmarkRunID)
	second := mustImport(ctx, t, seam, dir)

	if importedCount(second) != 0 {
		t.Errorf("re-import wrote %d attempts; the same bytes must re-ledger none of them",
			importedCount(second))
	}
	if len(second.Attempts) != 2 {
		t.Errorf("re-import reported %d attempts, want the suite's 2", len(second.Attempts))
	}
	if after := auditArtifactCount(ctx, t, seam, organizationID, first.BenchmarkRunID); after != before {
		t.Errorf("re-import left %d Audit artifacts in the suite's scope, was %d; the suite's contents "+
			"must not be written twice", after, before)
	}
	if second.Report == nil {
		t.Error("re-import must still report on the suite's report, which already exists")
	} else if second.Report.Created {
		t.Error("re-import CREATED a second report for a suite that already had one")
	}

	for _, runID := range runIDsOf(first) {
		reread, err := seam.GetBenchmarkAttempt(ctx, organizationID, second.BenchmarkRunID, runID)
		if err != nil {
			t.Fatalf("re-read the ledger row for %s: %v", runID, err)
		}
		original, err := seam.GetBenchmarkAttempt(ctx, organizationID, first.BenchmarkRunID, runID)
		if err != nil {
			t.Fatalf("read the original ledger row for %s: %v", runID, err)
		}
		if reread.RecordDigest != original.RecordDigest {
			t.Errorf("%s: the ledger digest moved across a no-op re-import, %s to %s",
				runID, original.RecordDigest, reread.RecordDigest)
		}
	}
}

// bootstrapTenant resolves the tenant the import needs, creating it if absent.
//
// Through the seam's bootstrap verbs rather than by inserting rows: the importer
// resolves a tenant and never provisions one, so a slice that faked the rows
// would be exercising a plane no bootstrap command could have produced.
func bootstrapTenant(ctx context.Context, t *testing.T, seam store.Store) uuid.UUID {
	t.Helper()
	organization, err := seam.BootstrapOrganization(ctx, store.BootstrapOrganizationInput{
		Slug: organizationSlug, DisplayName: organizationName,
	})
	if err != nil {
		t.Fatalf("bootstrap the organization: %v", err)
	}
	if _, err := seam.BootstrapUser(ctx, store.BootstrapUserInput{
		Handle: operatorHandle, DisplayName: operatorName,
		OrganizationID: organization.Record.OrganizationID,
	}); err != nil {
		t.Fatalf("bootstrap the operator: %v", err)
	}
	return organization.Record.OrganizationID
}

// mustImport runs one import that is expected to succeed.
func mustImport(
	ctx context.Context, t *testing.T, seam store.Store, dir string,
) *benchmarkimport.Result {
	t.Helper()
	result, err := benchmarkimport.New(seam).Import(ctx, &benchmarkimport.Options{
		OrganizationSlug: organizationSlug,
		OperatorHandle:   operatorHandle,
		Dir:              dir,
		SuiteRunID:       suiteRunID,
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	return result
}

// storedRecord reads a run record back out of its Audit artifact.
func storedRecord(
	ctx context.Context, t *testing.T, seam store.Store, organizationID, artifactID uuid.UUID,
) benchmarkimport.Record {
	t.Helper()
	artifact, err := seam.GetAuditArtifact(ctx, organizationID, artifactID)
	if err != nil {
		t.Fatalf("read Audit artifact %s: %v", artifactID, err)
	}
	var payload benchmarkimport.RunRecordPayload
	if err := json.Unmarshal(artifact.Payload, &payload); err != nil {
		t.Fatalf("decode the run-record payload of %s: %v", artifactID, err)
	}
	return payload.Record
}

// auditArtifactCount is how many Audit artifacts the suite's scope holds.
func auditArtifactCount(
	ctx context.Context, t *testing.T, seam store.Store, organizationID, benchmarkRunID uuid.UUID,
) int {
	t.Helper()
	artifacts, err := seam.ListAuditArtifactsByScope(ctx, organizationID,
		store.Scope{Type: store.ScopeBenchmark, ID: benchmarkRunID})
	if err != nil {
		t.Fatalf("list the suite's Audit artifacts: %v", err)
	}
	return len(artifacts)
}

// assertPinShape requires the report's evidence set to hold the right MIX of
// references.
//
// The set holds two kinds and the split matters: the run records the report
// cites are Audit artifacts living in Postgres, while the evidence files are
// attachments living in the object store. Only the second kind says anything
// about the object composition, so they are counted separately rather than as
// one total — a bare count of four would be satisfied by four artifact pins and
// no objects at all, which is exactly the result a broken object path produces.
func assertPinShape(t *testing.T, pins []store.Pin) {
	t.Helper()
	var attachmentPins, auditPins int
	for i := range pins {
		switch {
		case pins[i].AttachmentID != nil:
			attachmentPins++
		case pins[i].AuditArtifactID != nil:
			auditPins++
		default:
			t.Errorf("pin %s names neither an attachment nor an Audit artifact", pins[i].PinID)
		}
	}
	if attachmentPins != 2 {
		t.Errorf("the report pins %d attachments, want one per attempt (2); the object store is what "+
			"this count is about", attachmentPins)
	}
	if auditPins != 2 {
		t.Errorf("the report pins %d Audit artifacts, want the two run records it cites", auditPins)
	}
}

// assertPinnedEvidenceReadsBack requires every pin the report holds to resolve
// to bytes this fixture wrote.
//
// The pin and the object both, because they fail independently: a pin without
// its object is evidence the next sweep collects, and an object without its pin
// is storage nothing accounts for. Reading through GetAttachment drains the
// stream to EOF, which is where the seam's digest check can complete — so a
// provider that returned the wrong bytes fails here rather than being reported
// as present.
func assertPinnedEvidenceReadsBack(
	ctx context.Context, t *testing.T, seam store.Store, organizationID, reportID uuid.UUID,
) {
	t.Helper()
	pins, err := seam.ListPins(ctx, organizationID, reportID)
	if err != nil {
		t.Fatalf("list the report's pins: %v", err)
	}

	assertPinShape(t, pins)

	// Collected and compared as a SET. The pin order is the store's, not the
	// fixture's, and asserting an order would make this fail for a reason that
	// has nothing to do with portability.
	want := map[string]bool{}
	for _, runID := range []string{firstRunID, secondRunID} {
		want[string(evidenceFor(runID))] = true
	}
	for i := range pins {
		pin := &pins[i]
		if pin.AttachmentID == nil {
			continue
		}
		body, attachment, getErr := seam.GetAttachment(ctx, organizationID, *pin.AttachmentID)
		if getErr != nil {
			t.Errorf("open pinned attachment %s: %v", *pin.AttachmentID, getErr)
			continue
		}
		read, readErr := io.ReadAll(body)
		closeErr := body.Close()
		if readErr != nil {
			t.Errorf("read pinned attachment %s through the verifying reader: %v",
				*pin.AttachmentID, readErr)
			continue
		}
		if closeErr != nil {
			t.Errorf("close pinned attachment %s: %v", *pin.AttachmentID, closeErr)
		}
		if !want[string(read)] {
			t.Errorf("pinned attachment %s carries %q, which this fixture never wrote",
				*pin.AttachmentID, read)
			continue
		}
		delete(want, string(read))
		if pin.Digest != attachment.Digest {
			t.Errorf("pin is bound to %s while its attachment carries %s",
				pin.Digest, attachment.Digest)
		}
	}
	for unread := range want {
		t.Errorf("no pinned attachment carried %q, so that attempt's evidence did not survive", unread)
	}
}

// evidenceFor is the single evidence file one attempt contributes.
//
// Distinct per attempt on purpose. Objects are content-addressed, so two
// attempts sharing bytes would share one object — which would make "two
// attachments" the wrong expectation for a reason unrelated to portability.
func evidenceFor(runID string) []byte {
	return []byte("evidence for " + runID + " that has to survive the object store\n")
}

// importedCount is how many attempts an import actually wrote.
func importedCount(result *benchmarkimport.Result) int {
	count := 0
	for i := range result.Attempts {
		if result.Attempts[i].Imported {
			count++
		}
	}
	return count
}

// runIDsOf lists the attempts an import reported, in order.
func runIDsOf(result *benchmarkimport.Result) []string {
	ids := make([]string, 0, len(result.Attempts))
	for i := range result.Attempts {
		ids = append(ids, result.Attempts[i].RunID)
	}
	return ids
}

// twoAttemptSuite writes a results store holding two accepted attempts on one
// story, and returns its directory.
//
// Two rather than one, because a single attempt cannot distinguish an importer
// that writes the attempt it was given from one that writes whichever attempt it
// last read.
func twoAttemptSuite(t *testing.T) string {
	t.Helper()
	records := []map[string]any{
		recordWith(t, firstRunID),
		recordWith(t, secondRunID),
	}
	return writeSuite(t, records)
}

// controlRecord returns the corpus's accepted control case.
func controlRecord(t *testing.T) map[string]any {
	t.Helper()
	path := filepath.Join(repoRoot(t), corpusControl)
	raw, err := os.ReadFile(path) //nolint:gosec // a fixture path derived from the module root
	if err != nil {
		t.Fatalf("read the corpus control case at %s: %v", path, err)
	}
	var testCase struct {
		Record map[string]any `json:"record"`
	}
	if err := json.Unmarshal(raw, &testCase); err != nil {
		t.Fatalf("decode the corpus control case: %v", err)
	}
	if testCase.Record == nil {
		t.Fatalf("the corpus control case at %s carries no record", path)
	}
	return testCase.Record
}

// recordWith returns the corpus control under a given run id.
func recordWith(t *testing.T, runID string) map[string]any {
	t.Helper()
	record := controlRecord(t)
	record["run_id"] = runID
	record["suite_run_id"] = suiteRunID
	return record
}

// corpusModel returns the model named by the corpus control's MPH signature.
//
// Read from the fixture rather than restated as a constant: the principal
// assertion looks instances up BY this value, so a constant that drifted from
// the corpus would find none and report a missing principal family rather than
// its own staleness.
func corpusModel(t *testing.T) string {
	t.Helper()
	target, ok := controlRecord(t)["target"].(map[string]any)
	if !ok {
		t.Fatal("the corpus control carries no target object, so its MPH model cannot be read")
	}
	mph, ok := target["mph"].(map[string]any)
	if !ok {
		t.Fatal("the corpus control's target carries no mph object")
	}
	model, ok := mph["model"].(string)
	if !ok || model == "" {
		t.Fatal("the corpus control's MPH names no model")
	}
	return model
}

// writeSuite materialises one suite run on disk and returns its directory.
func writeSuite(t *testing.T, records []map[string]any) string {
	t.Helper()
	dir := t.TempDir()

	lines := make([]string, 0, len(records))
	for _, record := range records {
		encoded, err := json.Marshal(record)
		if err != nil {
			t.Fatalf("encode a record: %v", err)
		}
		lines = append(lines, string(encoded))
	}
	if err := os.WriteFile(filepath.Join(dir, suiteRunID+".jsonl"),
		[]byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write the records: %v", err)
	}

	// One evidence file per attempt, under the layout the importer walks. Without
	// these the import is legitimate but writes no attachments at all — which
	// would leave the object store, half of what a cloud composition changes,
	// entirely unexercised by this slice.
	for _, record := range records {
		runID, ok := record["run_id"].(string)
		if !ok {
			t.Fatalf("a record carries no run id, so its evidence has nowhere to go")
		}
		attemptDir := filepath.Join(dir, "evidence", runID)
		if err := os.MkdirAll(attemptDir, 0o750); err != nil {
			t.Fatalf("create the evidence directory for %s: %v", runID, err)
		}
		if err := os.WriteFile(filepath.Join(attemptDir, "maestro.log"),
			evidenceFor(runID), 0o600); err != nil {
			t.Fatalf("write the evidence for %s: %v", runID, err)
		}
	}

	encoded, err := json.Marshal(completedManifest(records))
	if err != nil {
		t.Fatalf("encode the manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, suiteRunID+".manifest.json"), encoded, 0o600); err != nil {
		t.Fatalf("write the manifest: %v", err)
	}
	return dir
}

// completedManifest describes exactly the records given, as a finished suite.
func completedManifest(records []map[string]any) map[string]any {
	attempts := make([]map[string]any, 0, len(records))
	for _, record := range records {
		attempts = append(attempts, map[string]any{
			"story": record["story_id"], "config": record["config_name"],
			"status": "completed", "run_id": record["run_id"], "repeat": 1,
		})
	}
	return map[string]any{
		"manifest_schema_version": 2,
		"suite_run_id":            suiteRunID,
		"stop_reason":             "completed",
		"attempts":                attempts,
		"budget_accounts":         []any{},
		"updated_at":              "2026-08-04T00:20:00Z",
	}
}

// repoRoot walks up from the working directory to the module root.
//
// The corpus sits at a fixed path from the repository root, and this package is
// called from more than one package's directory — a relative path that resolved
// for one caller would silently resolve to nothing for the other, and a missing
// corpus reads as a broken fixture rather than a broken path.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("locate the working directory: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above the working directory, so the import corpus cannot be located")
		}
		dir = parent
	}
}
