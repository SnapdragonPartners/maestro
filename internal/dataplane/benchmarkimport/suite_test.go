package benchmarkimport_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/dataplane/benchmarkimport"
)

// baseRecord returns the corpus's accepted control, which both validators
// agree on, so a coherence failure below cannot be a per-record failure in
// disguise.
func baseRecord(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(corpusDir, "accept_minimal.json"))
	if err != nil {
		t.Fatalf("read control case: %v", err)
	}
	var testCase struct {
		Record map[string]any `json:"record"`
	}
	if err := json.Unmarshal(raw, &testCase); err != nil {
		t.Fatalf("decode control case: %v", err)
	}
	return testCase.Record
}

// writeSuite materialises one suite run on disk.
//
//nolint:unparam // the suite id is a parameter because the cases below vary it in the manifest
func writeSuite(t *testing.T, suiteRunID string, records []map[string]any, manifest map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	var lines []string
	for _, record := range records {
		encoded, err := json.Marshal(record)
		if err != nil {
			t.Fatalf("encode record: %v", err)
		}
		lines = append(lines, string(encoded))
	}
	if err := os.WriteFile(filepath.Join(dir, suiteRunID+".jsonl"),
		[]byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write records: %v", err)
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("encode manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, suiteRunID+".manifest.json"), encoded, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return dir
}

// completedManifest describes exactly the records given.
func completedManifest(suiteRunID string, records []map[string]any) map[string]any {
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

// attemptsOf reads the manifest's attempt list without a bare assertion.
func attemptsOf(t *testing.T, manifest map[string]any) []map[string]any {
	t.Helper()
	attempts, ok := manifest["attempts"].([]map[string]any)
	if !ok {
		t.Fatalf("manifest attempts are %T, not a list", manifest["attempts"])
	}
	return attempts
}

// TestReadSuiteAcceptsACoherentSuite is the control. Every rejection below is
// one mutation away from this, so without it they could all be failing for an
// unrelated reason.
func TestReadSuiteAcceptsACoherentSuite(t *testing.T) {
	records := []map[string]any{baseRecord(t)}
	dir := writeSuite(t, "golden-all-probe", records, completedManifest("golden-all-probe", records))

	suite, err := benchmarkimport.ReadSuite(dir, "golden-all-probe")
	if err != nil {
		t.Fatalf("a coherent suite must be accepted: %v", err)
	}
	if len(suite.Records) != 1 {
		t.Fatalf("read %d records, want 1", len(suite.Records))
	}
	if !suite.Manifest.Terminal() {
		t.Error("stop_reason completed must read as terminal")
	}
}

// TestReadSuiteRefusesIncoherence drives each row of D1's table.
//
// These are the checks NO SINGLE RECORD can perform: a record knows its own
// suite id but not which file it was found in, and certainly not whether the
// manifest agrees that it happened. The suite is refused WHOLE — a partial
// import of a file that describes no consistent set of attempts is worse than
// none, because the report quotes the manifest.
func TestReadSuiteRefusesIncoherence(t *testing.T) {
	const suiteRunID = "golden-all-probe"

	second := func(t *testing.T) map[string]any {
		record := baseRecord(t)
		record["run_id"] = "story-b--config--r1--beef0001"
		record["story_id"] = "story-b"
		return record
	}

	for name, build := range map[string]func(*testing.T) (string, string){
		"manifest names another suite": func(t *testing.T) (string, string) {
			records := []map[string]any{baseRecord(t)}
			manifest := completedManifest("some-other-suite", records)
			return writeSuite(t, suiteRunID, records, manifest), suiteRunID
		},
		"a record names another suite": func(t *testing.T) (string, string) {
			record := baseRecord(t)
			record["suite_run_id"] = "some-other-suite"
			records := []map[string]any{record}
			manifest := completedManifest(suiteRunID, records)
			return writeSuite(t, suiteRunID, records, manifest), suiteRunID
		},
		"duplicate run id": func(t *testing.T) (string, string) {
			records := []map[string]any{baseRecord(t), baseRecord(t)}
			return writeSuite(t, suiteRunID, records, completedManifest(suiteRunID, records)), suiteRunID
		},
		"unknown manifest status": func(t *testing.T) (string, string) {
			records := []map[string]any{baseRecord(t)}
			manifest := completedManifest(suiteRunID, records)
			attemptsOf(t, manifest)[0]["status"] = "half-done"
			return writeSuite(t, suiteRunID, records, manifest), suiteRunID
		},
		"unknown stop reason": func(t *testing.T) (string, string) {
			records := []map[string]any{baseRecord(t)}
			manifest := completedManifest(suiteRunID, records)
			manifest["stop_reason"] = "gave-up"
			return writeSuite(t, suiteRunID, records, manifest), suiteRunID
		},
		"completed entry with no record": func(t *testing.T) (string, string) {
			records := []map[string]any{baseRecord(t)}
			manifest := completedManifest(suiteRunID, append(records, second(t)))
			return writeSuite(t, suiteRunID, records, manifest), suiteRunID
		},
		"record with no manifest entry": func(t *testing.T) (string, string) {
			records := []map[string]any{baseRecord(t), second(t)}
			manifest := completedManifest(suiteRunID, records[:1])
			return writeSuite(t, suiteRunID, records, manifest), suiteRunID
		},
		"manifest entry names the wrong run id": func(t *testing.T) (string, string) {
			records := []map[string]any{baseRecord(t)}
			manifest := completedManifest(suiteRunID, records)
			attemptsOf(t, manifest)[0]["run_id"] = "story-z--config--r1--0000ffff"
			return writeSuite(t, suiteRunID, records, manifest), suiteRunID
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir, id := build(t)
			_, err := benchmarkimport.ReadSuite(dir, id)
			if !errors.Is(err, benchmarkimport.ErrIncoherent) {
				t.Fatalf("must be refused as incoherent, got: %v", err)
			}
		})
	}
}

// TestEvidenceDirStaysInsideTheStore covers the containment rule.
//
// run_id is untrusted input used as a path component, and it reaches here
// from a file. The shape check refuses the obvious escapes; the containment
// check exists because a string check is where this class of bug lives.
func TestEvidenceDirStaysInsideTheStore(t *testing.T) {
	records := []map[string]any{baseRecord(t)}
	dir := writeSuite(t, "golden-all-probe", records, completedManifest("golden-all-probe", records))
	runID, _ := records[0]["run_id"].(string)
	if err := os.MkdirAll(filepath.Join(dir, "evidence", runID), 0o750); err != nil {
		t.Fatalf("seed evidence: %v", err)
	}
	suite, err := benchmarkimport.ReadSuite(dir, "golden-all-probe")
	if err != nil {
		t.Fatalf("read suite: %v", err)
	}

	// The control: a legitimate run id resolves inside the store.
	resolved, err := suite.EvidenceDir(runID)
	if err != nil {
		t.Fatalf("a legitimate evidence dir must resolve: %v", err)
	}
	if !strings.Contains(resolved, "evidence") {
		t.Fatalf("resolved to %s, which is not under the evidence root", resolved)
	}

	for _, hostile := range []string{"../escape", "..", ".", "a/b", "", "/etc"} {
		if _, err := suite.EvidenceDir(hostile); err == nil {
			t.Errorf("run id %q must be refused as a path component", hostile)
		}
	}

	// A symlink whose NAME is a valid run id but which points out of the
	// store. The shape check passes it, so only the post-resolution
	// containment check can catch it.
	outside := t.TempDir()
	link := filepath.Join(dir, "evidence", "escapee")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := suite.EvidenceDir("escapee"); err == nil {
		t.Error("a symlink pointing outside the results store must be refused")
	}
}
