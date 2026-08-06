//go:build integration

package benchmarkimport_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/benchmarkimport"
	"orchestrator/internal/dataplane/objects"
	"orchestrator/internal/dataplane/planetest"
	"orchestrator/internal/dataplane/registry"
	"orchestrator/internal/dataplane/secret"
	"orchestrator/internal/dataplane/store"
	"orchestrator/internal/dataplane/store/postgres"
)

// The tenant every test imports into. Resolved by the importer, never
// created by it, so the fixture bootstraps both before any import runs.
const (
	testOrgSlug       = "conformance"
	testOperator      = "operator"
	testSuiteRunID    = "golden-all-probe"
	measuredMetrics   = 9  // the corpus control's metrics, less the absent one
	registeredMetrics = 10 // the whole registry, for contrast
)

// absentMetric is the corpus control's one unmeasured metric. Its status is
// not_applicable, so it has no value and must produce no event.
const absentMetric = "human_attention_seconds"

// plane is a disposable data plane with the importer's registry and a
// bootstrapped tenant.
type plane struct {
	store *postgres.Store

	// The pieces a second store over the SAME plane is built from. A
	// registry is fixed at construction, so a test that needs the plane to
	// start refusing — or to stop — reopens it rather than starting over
	// with an empty database, which would prove nothing about what the
	// first pass left behind.
	dsn     string
	blob    *objects.Blob
	rootKey secret.RootKeyProvider

	organization store.Organization
	operator     store.User
}

// newPlane builds the plane an import needs, with the importer's OWN
// registry entries.
//
// The registry is the package's rather than a test double, so registration
// is exercised on the path that matters: a run-record payload the seam
// refuses is an import that cannot happen, and a double would hide it.
func newPlane(t *testing.T) *plane {
	t.Helper()
	return newPlaneWith(t, benchmarkimport.RegistryEntries())
}

func newPlaneWith(t *testing.T, entries map[registry.Type]registry.Entry) *plane {
	t.Helper()
	ctx := context.Background()

	blob, _ := planetest.Blob(t, "import")
	p := &plane{dsn: planetest.DSN(t, "import"), blob: blob, rootKey: planetest.RootKey(t)}
	p.store = p.open(t, entries)

	organization, err := p.store.BootstrapOrganization(ctx, store.BootstrapOrganizationInput{
		Slug: testOrgSlug, DisplayName: "Conformance",
	})
	if err != nil {
		t.Fatalf("bootstrap organization: %v", err)
	}
	operator, err := p.store.BootstrapUser(ctx, store.BootstrapUserInput{
		Handle: testOperator, DisplayName: "Operator",
		OrganizationID: organization.Record.OrganizationID,
	})
	if err != nil {
		t.Fatalf("bootstrap operator: %v", err)
	}
	p.organization, p.operator = organization.Record, operator.Record
	return p
}

// open builds a store over this plane's database and bucket.
func (p *plane) open(t *testing.T, entries map[registry.Type]registry.Entry) *postgres.Store {
	t.Helper()
	types, err := registry.New(entries)
	if err != nil {
		t.Fatalf("build registry: %v", err)
	}
	built, err := postgres.New(planetest.Pool(t, p.dsn), types, p.blob, p.rootKey)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	return built
}

// reopen returns the SAME plane behind a store carrying a different
// registry, so a test can change what the seam accepts without changing
// what the database already holds.
func (p *plane) reopen(t *testing.T, entries map[registry.Type]registry.Entry) *plane {
	t.Helper()
	next := *p
	next.store = p.open(t, entries)
	return &next
}

// importFrom runs one import against the plane.
func (p *plane) importFrom(t *testing.T, dir string) (*benchmarkimport.Result, error) {
	t.Helper()
	return benchmarkimport.New(p.store).Import(context.Background(), benchmarkimport.Options{
		OrganizationSlug: testOrgSlug,
		OperatorHandle:   testOperator,
		Dir:              dir,
		SuiteRunID:       testSuiteRunID,
	})
}

// mustImport runs an import that is expected to succeed.
func (p *plane) mustImport(t *testing.T, dir string) *benchmarkimport.Result {
	t.Helper()
	result, err := p.importFrom(t, dir)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	return result
}

// recordWith returns the corpus control with the named fields replaced, so a
// test varies exactly what it is about.
func recordWith(t *testing.T, fields map[string]any) map[string]any {
	t.Helper()
	record := baseRecord(t)
	for key, value := range fields {
		record[key] = value
	}
	return record
}

// runIDsOf reads the run ids a result reports, in order.
func runIDsOf(result *benchmarkimport.Result) []string {
	ids := make([]string, 0, len(result.Attempts))
	for _, attempt := range result.Attempts {
		ids = append(ids, attempt.RunID)
	}
	return ids
}

// importedCount is how many attempts the import actually wrote.
func importedCount(result *benchmarkimport.Result) int {
	count := 0
	for _, attempt := range result.Attempts {
		if attempt.Imported {
			count++
		}
	}
	return count
}

// twoAttemptSuite writes a suite of two accepted attempts on one story.
func twoAttemptSuite(t *testing.T) string {
	t.Helper()
	records := []map[string]any{
		recordWith(t, map[string]any{"run_id": "story-a--config--r1--aaaa1111"}),
		recordWith(t, map[string]any{"run_id": "story-a--config--r2--bbbb2222"}),
	}
	return writeSuite(t, testSuiteRunID, records, completedManifest(testSuiteRunID, records))
}

// attempt reads one ledger row, failing if it is absent.
func (p *plane) attempt(t *testing.T, runID string, benchmarkRunID uuid.UUID) *store.BenchmarkAttempt {
	t.Helper()
	ledgered, err := p.store.GetBenchmarkAttempt(context.Background(),
		p.organization.OrganizationID, benchmarkRunID, runID)
	if err != nil {
		t.Fatalf("read ledger for %s: %v", runID, err)
	}
	return ledgered
}

// storedRecord reads the run record back out of its Audit artifact.
func (p *plane) storedRecord(t *testing.T, artifactID uuid.UUID) benchmarkimport.Record {
	t.Helper()
	artifact, err := p.store.GetAuditArtifact(context.Background(),
		p.organization.OrganizationID, artifactID)
	if err != nil {
		t.Fatalf("read audit artifact %s: %v", artifactID, err)
	}
	var payload benchmarkimport.RunRecordPayload
	if err := json.Unmarshal(artifact.Payload, &payload); err != nil {
		t.Fatalf("decode run-record payload: %v", err)
	}
	return payload.Record
}

// TestImportWritesTheAttemptWhole is the control: everything one attempt
// produces is present and refers to the same attempt.
//
// Without it every assertion below could be passing because nothing was
// written at all.
func TestImportWritesTheAttemptWhole(t *testing.T) {
	p := newPlane(t)
	dir := twoAttemptSuite(t)

	result := p.mustImport(t, dir)
	if len(result.Attempts) != 2 || importedCount(result) != 2 {
		t.Fatalf("imported %d of %d attempts, want 2 of 2", importedCount(result), len(result.Attempts))
	}
	if !result.Terminal {
		t.Error("a suite whose manifest stop_reason is 'completed' is terminal")
	}

	for _, runID := range runIDsOf(result) {
		ledgered := p.attempt(t, runID, result.BenchmarkRunID)
		if ledgered.RecordDigest == "" {
			t.Errorf("%s: ledger row carries no digest", runID)
		}
		stored := p.storedRecord(t, ledgered.AuditArtifactID)
		if stored.RunID != runID {
			t.Errorf("artifact for %s carries record %s", runID, stored.RunID)
		}
		if stored.Verdict != "accepted" || stored.SuiteRunID != testSuiteRunID {
			t.Errorf("%s round-tripped as verdict %q of suite %q", runID, stored.Verdict, stored.SuiteRunID)
		}
	}

	run, err := p.store.GetBenchmarkRunBySuite(context.Background(),
		p.organization.OrganizationID, testSuiteRunID)
	if err != nil {
		t.Fatalf("read benchmark run: %v", err)
	}
	if run.BenchmarkRunID != result.BenchmarkRunID {
		t.Errorf("result names run %s, the plane stores %s", result.BenchmarkRunID, run.BenchmarkRunID)
	}
}

// TestReimportIsANoOp covers D6's first rule: the same bytes offered again
// write nothing and are not an error.
func TestReimportIsANoOp(t *testing.T) {
	p := newPlane(t)
	dir := twoAttemptSuite(t)

	first := p.mustImport(t, dir)
	before := p.artifactCount(t, first.BenchmarkRunID)

	second := p.mustImport(t, dir)
	if importedCount(second) != 0 {
		t.Errorf("re-import wrote %d attempts; the same bytes are a no-op", importedCount(second))
	}
	if len(second.Attempts) != 2 {
		t.Errorf("re-import reported %d attempts, want the suite's 2", len(second.Attempts))
	}
	if after := p.artifactCount(t, first.BenchmarkRunID); after != before {
		t.Errorf("re-import left %d artifacts, was %d; a no-op writes none", after, before)
	}
	for _, runID := range runIDsOf(first) {
		if p.attempt(t, runID, first.BenchmarkRunID).RecordDigest !=
			p.attempt(t, runID, second.BenchmarkRunID).RecordDigest {
			t.Errorf("%s: the ledger digest moved across a no-op re-import", runID)
		}
	}
}

// TestReimportFromADifferentPathIsANoOp is the regression guard for identity
// carrying a local filesystem path.
//
// The results store is PORTABLE (design D8) and the paths that reach it are
// not: the same unchanged store is reached through a symlink, through a
// redundant `.` component, and — the case that matters operationally — from
// a different directory after it is moved or copied. An importer that put
// the store's location inside the digested payload would answer every one of
// those with ImportConflict, reporting tampering on bytes that never
// changed, which is the exact opposite of what D6 built the digest for.
func TestReimportFromADifferentPathIsANoOp(t *testing.T) {
	p := newPlane(t)
	dir := twoAttemptSuite(t)
	first := p.mustImport(t, dir)

	link := filepath.Join(t.TempDir(), "linked-store")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatalf("link the store: %v", err)
	}
	moved := filepath.Join(t.TempDir(), "moved-store")
	copyTree(t, dir, moved)

	for _, spelling := range []struct {
		name string
		dir  string
	}{
		// Concatenated rather than joined: filepath.Join cleans, so a
		// "different spelling" built with it is the same string and the case
		// could never fail.
		{"a redundant path component", dir + string(filepath.Separator) + "."},
		{"a symlink to the store", link},
		{"the store copied elsewhere", moved},
	} {
		t.Run(spelling.name, func(t *testing.T) {
			result, err := p.importFrom(t, spelling.dir)
			if err != nil {
				t.Fatalf("import through %s: %v", spelling.name, err)
			}
			if importedCount(result) != 0 {
				t.Errorf("%s imported %d attempts; the records are unchanged, so this is a no-op",
					spelling.name, importedCount(result))
			}
			for _, runID := range runIDsOf(first) {
				if got := p.attempt(t, runID, result.BenchmarkRunID).RecordDigest; got !=
					p.attempt(t, runID, first.BenchmarkRunID).RecordDigest {
					t.Errorf("%s: digest through %s is %s, differing from the first import's",
						runID, spelling.name, got)
				}
			}
		})
	}
}

// TestChangedRecordIsRejected covers D6's second rule. Run records are
// append-only on disk, so a differing digest for a ledgered identity is
// corruption or tampering — and overwriting would erase the evidence of it.
func TestChangedRecordIsRejected(t *testing.T) {
	p := newPlane(t)
	first := p.mustImport(t, twoAttemptSuite(t))
	before := p.artifactCount(t, first.BenchmarkRunID)

	// The same two run ids, one of them carrying a different verdict: the
	// shape a hand-edited or truncated-and-rewritten file has.
	tampered := []map[string]any{
		recordWith(t, map[string]any{"run_id": "story-a--config--r1--aaaa1111"}),
		recordWith(t, map[string]any{
			"run_id": "story-a--config--r2--bbbb2222", "verdict": "invalid",
			"invalid_reason": "rewritten after the fact", "solution_commit": "",
			"terminal_state_reached": false,
		}),
	}
	dir := writeSuite(t, testSuiteRunID, tampered, completedManifest(testSuiteRunID, tampered))

	_, err := p.importFrom(t, dir)
	if !errors.Is(err, store.ErrImportConflict) {
		t.Fatalf("import of a changed record returned %v, want ErrImportConflict", err)
	}
	var conflict *store.ImportConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("error %v carries no ImportConflict detail; the operator's next question is which side is wrong", err)
	}
	if conflict.RunID != "story-a--config--r2--bbbb2222" {
		t.Errorf("conflict names %q, want the changed attempt", conflict.RunID)
	}
	if conflict.StoredDigest == conflict.OfferedDigest || conflict.StoredDigest == "" || conflict.OfferedDigest == "" {
		t.Errorf("conflict reports stored %q and offered %q; both must be present and different",
			conflict.StoredDigest, conflict.OfferedDigest)
	}
	if after := p.artifactCount(t, first.BenchmarkRunID); after != before {
		t.Errorf("a rejected import left %d artifacts, was %d; nothing is overwritten and nothing is added",
			after, before)
	}
}

// TestTargetPrincipalCarriesTheAttemptLifetime is the guard for design D4's
// lifecycle requirement.
//
// The instance stands for the CONFIGURATION UNDER TEST during one attempt,
// which ran at the time the record says and stopped for the reason its
// verdict gives. Dated at import time instead, every attempt ever imported
// would appear to have happened at once and to be running still — so the MPH
// query below would answer a question about the importer rather than about
// the runs it imported.
func TestTargetPrincipalCarriesTheAttemptLifetime(t *testing.T) {
	p := newPlane(t)

	started := time.Date(2026, 8, 4, 1, 0, 0, 0, time.UTC)
	finished := time.Date(2026, 8, 4, 1, 42, 0, 0, time.UTC)
	records := []map[string]any{
		recordWith(t, map[string]any{
			"run_id": "story-a--config--r1--cccc3333", "started_at": started.Format(time.RFC3339),
			"finished_at": finished.Format(time.RFC3339),
		}),
		recordWith(t, map[string]any{
			"run_id": "story-a--config--r2--dddd4444", "verdict": "failed",
			"failure_kind": "checks-failed", "solution_commit": "",
			"terminal_state_reached": false,
		}),
	}
	dir := writeSuite(t, testSuiteRunID, records, completedManifest(testSuiteRunID, records))
	p.mustImport(t, dir)

	if found := p.targets(t); len(found) != len(records) {
		t.Fatalf("found %d target principals, want one per attempt", len(found))
	}

	for _, want := range []struct {
		runID      string
		start      time.Time
		stop       time.Time
		stopReason string
	}{
		{"story-a--config--r1--cccc3333", started, finished, "accepted"},
		// The failure kind rides along, because "failed" alone does not say
		// what failed and the column is read by grouping.
		{"story-a--config--r2--dddd4444",
			time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC),
			time.Date(2026, 8, 4, 0, 10, 0, 0, time.UTC), "failed: checks-failed"},
	} {
		instance := p.targetFor(t, want.runID)
		if !instance.StartTime.Equal(want.start) {
			t.Errorf("%s: start_time is %s, want the record's %s", want.runID, instance.StartTime, want.start)
		}
		switch {
		case instance.StopTime == nil:
			t.Errorf("%s: the instance is still open; the attempt it stands for finished before the import began",
				want.runID)
		case !instance.StopTime.Equal(want.stop):
			t.Errorf("%s: stop_time is %s, want the record's %s", want.runID, instance.StopTime, want.stop)
		}
		switch {
		case instance.StopReason == nil:
			t.Errorf("%s: the instance carries no stop reason", want.runID)
		case *instance.StopReason != want.stopReason:
			t.Errorf("%s: stop_reason is %q, want %q", want.runID, *instance.StopReason, want.stopReason)
		}
	}
}

// TestImporterPrincipalIsClosedWhenTheImportEnds covers the other half of
// D4's lifecycle: the importer's own instance is one INVOCATION's lifetime.
// Left open, every import ever run reads as an import still running.
func TestImporterPrincipalIsClosedWhenTheImportEnds(t *testing.T) {
	p := newPlane(t)
	p.mustImport(t, twoAttemptSuite(t))

	importers := p.instancesByModel(t, "system-benchmark-importer")
	if len(importers) != 1 {
		t.Fatalf("found %d importer principals, want one per invocation", len(importers))
	}
	switch {
	case importers[0].StopTime == nil:
		t.Error("the importer's instance is still open after the import returned")
	case importers[0].StopReason == nil || *importers[0].StopReason != "import complete":
		t.Errorf("importer stopped with reason %v, want it to say the import completed", importers[0].StopReason)
	}
}

// TestImporterPrincipalIsClosedWhenTheContextIsCancelled covers the exit the
// closing exists for and the caller's context cannot serve.
//
// Cancellation and deadline expiry are the most likely ways an import fails,
// and they are exactly the cases where the caller's context can no longer
// carry a write. A cleanup reusing it leaves the instance open in precisely
// that situation while passing every test that does not cancel — so this one
// cancels AFTER the principal exists, from inside the seam's own validation
// gate, which is reached mid-transaction and well after creation.
func TestImporterPrincipalIsClosedWhenTheContextIsCancelled(t *testing.T) {
	records := []map[string]any{recordWith(t, map[string]any{"run_id": "story-a--config--r1--aaaa1111"})}
	dir := writeSuite(t, testSuiteRunID, records, completedManifest(testSuiteRunID, records))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := newPlaneWith(t, cancellingRegistry(cancel))

	_, err := benchmarkimport.New(p.store).Import(ctx, benchmarkimport.Options{
		OrganizationSlug: testOrgSlug, OperatorHandle: testOperator,
		Dir: dir, SuiteRunID: testSuiteRunID,
	})
	if err == nil {
		t.Fatal("the import reported success after its context was cancelled")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("import failed with %v, want the cancellation; a different failure would mean this "+
			"test is exercising some other path", err)
	}

	importers := p.instancesByModel(t, "system-benchmark-importer")
	if len(importers) != 1 {
		t.Fatalf("found %d importer principals, want one per invocation", len(importers))
	}
	switch {
	case importers[0].StopTime == nil:
		t.Error("the importer's instance is still open after a cancelled import; the cleanup cannot " +
			"run on the context that was cancelled")
	case importers[0].StopReason == nil || *importers[0].StopReason != "import failed":
		t.Errorf("importer stopped with reason %v, want it to say the import failed", importers[0].StopReason)
	}
}

// TestConcurrentImportersWriteOneAttemptOnce covers the loser's rollback.
//
// Two importers can both find an attempt unledgered — the check before the
// transaction is exactly that, a check before the transaction — and both then
// write a principal, an artifact and a metric set before either reaches the
// ledger. Only the winner's may survive: the loser rolls its whole
// transaction back rather than leaving a second artifact for one identity,
// and reports the attempt as imported, because it genuinely is.
//
// Ordered by a barrier rather than by racing, so the loser is a fact rather
// than a probability: the first importer blocks inside its transaction at the
// seam's validation gate, the second runs to completion, and only then is the
// first released to meet a ledger row that was not there when it looked.
func TestConcurrentImportersWriteOneAttemptOnce(t *testing.T) {
	const runID = "story-a--config--r1--aaaa1111"
	records := []map[string]any{recordWith(t, map[string]any{"run_id": runID})}
	dir := writeSuite(t, testSuiteRunID, records, completedManifest(testSuiteRunID, records))

	entered, release := make(chan struct{}), make(chan struct{})
	p := newPlaneWith(t, barrierRegistry(entered, release))
	loser := benchmarkimport.New(p.store)
	winner := p.reopen(t, benchmarkimport.RegistryEntries())

	type outcome struct {
		result *benchmarkimport.Result
		err    error
	}
	loserDone := make(chan outcome, 1)
	go func() {
		result, err := loser.Import(context.Background(), benchmarkimport.Options{
			OrganizationSlug: testOrgSlug, OperatorHandle: testOperator,
			Dir: dir, SuiteRunID: testSuiteRunID,
		})
		loserDone <- outcome{result, err}
	}()

	select {
	case <-entered:
	case <-time.After(30 * time.Second):
		t.Fatal("the first importer never reached the seam's validation gate")
	}
	// Committed while the first importer holds an open transaction it has not
	// yet ledgered.
	winnerResult := winner.mustImport(t, dir)
	if importedCount(winnerResult) != 1 {
		t.Fatalf("the winning import wrote %d attempts, want 1", importedCount(winnerResult))
	}
	close(release)

	var got outcome
	select {
	case got = <-loserDone:
	case <-time.After(30 * time.Second):
		t.Fatal("the first importer never returned after being released")
	}
	if got.err != nil {
		t.Fatalf("losing the ledger race is not an error — the attempt IS imported: %v", got.err)
	}
	if importedCount(got.result) != 0 {
		t.Errorf("the losing import reported %d attempts written; the winner's are the ones that count",
			importedCount(got.result))
	}
	if len(got.result.Attempts) != 1 {
		t.Errorf("the losing import reported %d attempts, want the suite's 1", len(got.result.Attempts))
	}

	// One of everything the transaction holds. The artifact is the invariant
	// the ledger exists for; the principal and the metric events are what a
	// rollback that reached only the ledger row would leave behind.
	//
	// These counts are also what proves the BARRIER landed. A loser that had
	// short-circuited at the check before the transaction would leave one of
	// each as well — and would leave one of each with the rollback deleted,
	// so the test would be asserting something adjacent to the contract
	// rather than the contract. Disabling the rollback makes these two, two
	// and eighteen, which it can only do if the loser got inside.
	if got := p.artifactCount(t, winnerResult.BenchmarkRunID); got != 1 {
		t.Errorf("%d run-record artifacts exist for one attempt", got)
	}
	if got := len(p.targets(t)); got != 1 {
		t.Errorf("%d target principals exist for one attempt", got)
	}
	if got := len(p.metricEvents(t)); got != measuredMetrics {
		t.Errorf("%d metric events exist, want the one attempt's %d", got, measuredMetrics)
	}
	ledgered := p.attempt(t, runID, winnerResult.BenchmarkRunID)
	if ledgered.AuditArtifactID == uuid.Nil {
		t.Error("the ledger row names no artifact")
	}
	// The surviving artifact is the one the LEDGER names: a loser whose
	// artifact committed while the winner's row pointed elsewhere would leave
	// exactly one of each and still be wrong.
	if stored := p.storedRecord(t, ledgered.AuditArtifactID); stored.RunID != runID {
		t.Errorf("the ledgered artifact carries record %s, want %s", stored.RunID, runID)
	}
	// Both importers ran, so both have an instance, and both are closed.
	for _, instance := range p.instancesByModel(t, "system-benchmark-importer") {
		if instance.StopTime == nil {
			t.Errorf("importer instance %s is still open", instance.PrincipalInstanceID)
		}
	}
}

// TestMPHQueryFindsTheImportedRuns is the question ADR 0021 built the MPH
// columns for, asked here for the first time against real data.
func TestMPHQueryFindsTheImportedRuns(t *testing.T) {
	p := newPlane(t)
	result := p.mustImport(t, twoAttemptSuite(t))

	promptHash := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	found, err := p.store.FindPrincipalInstances(context.Background(), store.MPHQuery{
		OrganizationID: p.organization.OrganizationID, PromptHash: &promptHash,
	})
	if err != nil {
		t.Fatalf("MPH query: %v", err)
	}
	if len(found) != len(result.Attempts) {
		t.Fatalf("prompt hash names %d instances, want one per imported attempt (%d)",
			len(found), len(result.Attempts))
	}
	for i := range found {
		if found[i].Kind != store.PrincipalAgent {
			t.Errorf("instance %s is a %s principal; the configuration under test is an agent",
				found[i].PrincipalInstanceID, found[i].Kind)
		}
		if found[i].AgentType == nil || *found[i].AgentType != "benchmark-target" {
			t.Errorf("instance %s carries agent type %v, want benchmark-target",
				found[i].PrincipalInstanceID, found[i].AgentType)
		}
	}
	// The importer answers a different question and must not be swept in by
	// this one: a system principal has no prompt to hash.
	unrelated := "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	other, err := p.store.FindPrincipalInstances(context.Background(), store.MPHQuery{
		OrganizationID: p.organization.OrganizationID, PromptHash: &unrelated,
	})
	if err != nil {
		t.Fatalf("MPH query for an unused hash: %v", err)
	}
	if len(other) != 0 {
		t.Errorf("an unused prompt hash matched %d instances", len(other))
	}
}

// TestOnlyMeasuredMetricsBecomeEvents guards the rule that an unmeasured
// metric produces no event.
//
// A metric reported unsupported, not_applicable or unavailable has no value,
// and writing a zero for it would put a measurement in the plane that nobody
// made — indistinguishable, afterwards, from a real zero. The status stays
// recoverable from the run-record artifact, which carries the whole metrics
// map.
func TestOnlyMeasuredMetricsBecomeEvents(t *testing.T) {
	p := newPlane(t)
	records := []map[string]any{recordWith(t, map[string]any{"run_id": "story-a--config--r1--eeee5555"})}
	dir := writeSuite(t, testSuiteRunID, records, completedManifest(testSuiteRunID, records))
	p.mustImport(t, dir)

	events := p.metricEvents(t)
	if len(events) != measuredMetrics {
		t.Errorf("import wrote %d metric events, want %d — one per MEASURED metric, of %d registered",
			len(events), measuredMetrics, registeredMetrics)
	}
	byName := make(map[string]float64, len(events))
	for i := range events {
		byName[events[i].MetricName] = events[i].Value
	}
	if _, present := byName[absentMetric]; present {
		t.Errorf("%s is reported not_applicable and has no value, but an event was written for it", absentMetric)
	}
	// A measured zero IS a measurement and must survive; it is the case a
	// "skip the falsy values" implementation gets wrong.
	if value, present := byName["human_interventions"]; !present || value != 0 {
		t.Errorf("human_interventions is measured at 0; got %v (present=%t)", value, present)
	}
	if value := byName["tokens_total"]; value != 12000 {
		t.Errorf("tokens_total stored as %v, want the record's 12000", value)
	}
	// The events belong to the attempt's principal, not to the importer.
	targets := p.targets(t)
	if len(targets) != 1 {
		t.Fatalf("found %d target principals, want one", len(targets))
	}
	for i := range events {
		if events[i].PrincipalInstanceID == nil || *events[i].PrincipalInstanceID != targets[0].PrincipalInstanceID {
			t.Errorf("metric %s is attributed to %v, not to the configuration under test",
				events[i].MetricName, events[i].PrincipalInstanceID)
		}
	}
}

// TestImportResumesAfterAPartialFailure covers D6's last rule.
//
// The failure is injected at the seam's own validation gate, because that is
// a real refusal on the real path rather than a fault the importer was told
// about: the middle attempt's artifact is refused, its transaction rolls
// back, and the import stops there. What matters is what is left behind —
// the earlier attempt committed, the failing one having written NOTHING, and
// no manual repair needed to finish the job.
func TestImportResumesAfterAPartialFailure(t *testing.T) {
	const failing = "story-a--config--r2--ffff6666"
	records := []map[string]any{
		recordWith(t, map[string]any{"run_id": "story-a--config--r1--aaaa1111"}),
		recordWith(t, map[string]any{"run_id": failing}),
		recordWith(t, map[string]any{"run_id": "story-a--config--r3--99997777"}),
	}
	dir := writeSuite(t, testSuiteRunID, records, completedManifest(testSuiteRunID, records))

	p := newPlaneWith(t, refusingRegistry(failing))
	partial, err := p.importFrom(t, dir)
	if err == nil {
		t.Fatal("the import reported success while an attempt was refused")
	}
	if partial == nil {
		t.Fatal("a failed import reports no result; the attempts before the failure are committed and " +
			"the caller has to be able to see which")
	}
	if importedCount(partial) != 1 {
		t.Fatalf("%d attempts imported before the failure, want the one preceding it", importedCount(partial))
	}

	run, err := p.store.GetBenchmarkRunBySuite(context.Background(),
		p.organization.OrganizationID, testSuiteRunID)
	if err != nil {
		t.Fatalf("read benchmark run: %v", err)
	}
	// The failing attempt wrote nothing at all: no ledger row, and — the part
	// a ledger check alone would miss — no principal for an attempt that has
	// no artifact.
	if _, err := p.store.GetBenchmarkAttempt(context.Background(),
		p.organization.OrganizationID, run.BenchmarkRunID, failing); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the refused attempt is ledgered (%v); its transaction must have rolled back whole", err)
	}
	if got := len(p.targets(t)); got != 1 {
		t.Errorf("%d target principals exist, want only the committed attempt's; a principal written "+
			"outside the attempt's transaction survives its rollback", got)
	}
	if got := p.artifactCount(t, run.BenchmarkRunID); got != 1 {
		t.Errorf("%d run-record artifacts exist, want only the committed attempt's", got)
	}

	// Nothing is repaired by hand. The SAME plane, no longer refusing,
	// imports the same store again: the committed attempt is skipped and the
	// two that never landed are written.
	resumed := p.reopen(t, benchmarkimport.RegistryEntries())
	first := resumed.mustImport(t, dir)
	if importedCount(first) != len(records)-1 {
		t.Fatalf("the resuming import wrote %d attempts, want the %d that had not landed",
			importedCount(first), len(records)-1)
	}
	if len(first.Attempts) != len(records) {
		t.Errorf("the resuming import reported %d attempts, want the suite's %d",
			len(first.Attempts), len(records))
	}
	second := resumed.mustImport(t, dir)
	if importedCount(second) != 0 {
		t.Errorf("a third import wrote %d attempts; every one of them is ledgered", importedCount(second))
	}
}

// TestImportResolvesTenantsAndNeverCreatesThem guards the rule that an
// import provisions nothing. An import that silently creates a tenant is a
// defect waiting for team mode, and the operator would not learn they had
// typed the wrong organization until the rows were somewhere else.
func TestImportResolvesTenantsAndNeverCreatesThem(t *testing.T) {
	p := newPlane(t)
	dir := twoAttemptSuite(t)

	for _, unknown := range []struct {
		name    string
		options benchmarkimport.Options
	}{
		{"organization", benchmarkimport.Options{
			OrganizationSlug: "no-such-org", OperatorHandle: testOperator,
			Dir: dir, SuiteRunID: testSuiteRunID,
		}},
		{"operator", benchmarkimport.Options{
			OrganizationSlug: testOrgSlug, OperatorHandle: "no-such-operator",
			Dir: dir, SuiteRunID: testSuiteRunID,
		}},
	} {
		t.Run(unknown.name, func(t *testing.T) {
			if _, err := benchmarkimport.New(p.store).Import(context.Background(), unknown.options); err == nil {
				t.Fatalf("import with an unknown %s succeeded", unknown.name)
			}
		})
	}
	if _, err := p.store.GetOrganizationBySlug(context.Background(), "no-such-org"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the unknown organization exists after a failed import (%v)", err)
	}
	if _, err := p.store.GetUserByHandle(context.Background(),
		p.organization.OrganizationID, "no-such-operator"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the unknown operator exists after a failed import (%v)", err)
	}
}

// withRunRecordValidator returns the importer's registry with the run-record
// validator wrapped.
//
// The validator is the one hook the seam already calls from INSIDE the
// attempt's transaction, which is what makes it the place to stand a test in
// the middle of one. It runs after the principal is created and before the
// ledger row, so a wrapper here observes and interferes exactly where a real
// failure or a real race would land.
func withRunRecordValidator(wrap func(inner registry.Validator, payload []byte) error) map[registry.Type]registry.Entry {
	entries := benchmarkimport.RegistryEntries()
	entry := entries[benchmarkimport.TypeRunRecord]
	inner := entry.Validators[benchmarkimport.PayloadVersion]
	entry.Validators = map[int]registry.Validator{
		benchmarkimport.PayloadVersion: registry.ValidatorFunc(func(payload []byte) error {
			return wrap(inner, payload)
		}),
	}
	entries[benchmarkimport.TypeRunRecord] = entry
	return entries
}

// cancellingRegistry cancels the import's context mid-transaction, after the
// importer's principal exists.
func cancellingRegistry(cancel context.CancelFunc) map[registry.Type]registry.Entry {
	var once sync.Once
	return withRunRecordValidator(func(inner registry.Validator, payload []byte) error {
		once.Do(cancel)
		return inner.Validate(payload)
	})
}

// barrierRegistry holds an import inside its transaction until it is
// released, closing entered once it is there.
//
// Only the first artifact blocks: a second would deadlock a suite of more
// than one record against a test that releases once.
func barrierRegistry(entered chan<- struct{}, release <-chan struct{}) map[registry.Type]registry.Entry {
	var once sync.Once
	return withRunRecordValidator(func(inner registry.Validator, payload []byte) error {
		once.Do(func() {
			close(entered)
			<-release
		})
		return inner.Validate(payload)
	})
}

// refusingRegistry is the importer's registry with one named run id refused
// by the run-record validator.
//
// A stand-in for any mid-import failure, injected where the seam already
// calls out: the artifact write fails inside the attempt's transaction,
// which is where a real refusal would land.
func refusingRegistry(runID string) map[registry.Type]registry.Entry {
	return withRunRecordValidator(func(inner registry.Validator, payload []byte) error {
		var body struct {
			Record struct {
				RunID string `json:"run_id"`
			} `json:"record"`
		}
		if err := json.Unmarshal(payload, &body); err != nil {
			return fmt.Errorf("refusing registry: %w", err)
		}
		if body.Record.RunID == runID {
			return fmt.Errorf("refusing %s on purpose", runID)
		}
		return inner.Validate(payload)
	})
}

// artifactCount is how many run-record artifacts the suite's scope holds.
func (p *plane) artifactCount(t *testing.T, benchmarkRunID uuid.UUID) int {
	t.Helper()
	artifacts, err := p.store.ListAuditArtifactsByScope(context.Background(),
		p.organization.OrganizationID, store.Scope{Type: store.ScopeBenchmark, ID: benchmarkRunID})
	if err != nil {
		t.Fatalf("list audit artifacts: %v", err)
	}
	return len(artifacts)
}

// targets returns every principal standing for a configuration under test.
func (p *plane) targets(t *testing.T) []store.PrincipalInstance {
	t.Helper()
	return p.instancesByModel(t, "claude-opus-5")
}

func (p *plane) instancesByModel(t *testing.T, model string) []store.PrincipalInstance {
	t.Helper()
	found, err := p.store.FindPrincipalInstances(context.Background(), store.MPHQuery{
		OrganizationID: p.organization.OrganizationID, Model: &model,
	})
	if err != nil {
		t.Fatalf("find instances of %s: %v", model, err)
	}
	return found
}

// targetFor returns the target principal belonging to one attempt, found by
// following the plane rather than by position in a list.
//
// Through the metric events, because they are the only rows naming the
// attempt and its principal together: the run-record artifact is authored by
// the IMPORTER, whose instance is a different principal entirely. This works
// because every record here carries at least one measured metric; a record
// with none would have no such link, which is a fact about the plane worth
// knowing rather than a limitation of the helper.
func (p *plane) targetFor(t *testing.T, runID string) store.PrincipalInstance {
	t.Helper()
	for _, event := range p.metricEvents(t) {
		var labels struct {
			RunID string `json:"run_id"`
		}
		if err := json.Unmarshal(event.Labels, &labels); err != nil {
			t.Fatalf("decode metric labels: %v", err)
		}
		if labels.RunID != runID || event.PrincipalInstanceID == nil {
			continue
		}
		instance, err := p.store.GetPrincipalInstance(context.Background(),
			p.organization.OrganizationID, *event.PrincipalInstanceID)
		if err != nil {
			t.Fatalf("read principal instance for %s: %v", runID, err)
		}
		return *instance
	}
	t.Fatalf("no target principal is reachable from %s", runID)
	return store.PrincipalInstance{}
}

// metricEvents reads every metric event in the plane.
func (p *plane) metricEvents(t *testing.T) []store.MetricEvent {
	t.Helper()
	events, err := p.store.ListMetricEventsInWindow(context.Background(), p.organization.OrganizationID,
		time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC),
		store.Page{Limit: store.MaxPageLimit})
	if err != nil {
		t.Fatalf("list metric events: %v", err)
	}
	return events
}

// copyTree copies a results store to another directory, the way an operator
// moves one between machines.
func copyTree(t *testing.T, from, to string) {
	t.Helper()
	if err := os.MkdirAll(to, 0o750); err != nil {
		t.Fatalf("create %s: %v", to, err)
	}
	entries, err := os.ReadDir(from)
	if err != nil {
		t.Fatalf("read %s: %v", from, err)
	}
	for _, entry := range entries {
		source := filepath.Join(from, entry.Name())
		target := filepath.Join(to, entry.Name())
		if entry.IsDir() {
			copyTree(t, source, target)
			continue
		}
		body, err := os.ReadFile(source) //nolint:gosec // a path this test just created
		if err != nil {
			t.Fatalf("read %s: %v", source, err)
		}
		if err := os.WriteFile(target, body, 0o600); err != nil {
			t.Fatalf("write %s: %v", target, err)
		}
	}
}
