package results_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/SnapdragonPartners/maestro/benchmark/results"
	"github.com/SnapdragonPartners/maestro/benchmark/runrecord"
	"github.com/SnapdragonPartners/maestro/benchmark/runrecord/recordtest"
)

func openStore(t *testing.T) (*results.Store, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := results.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return store, dir
}

func TestAppendReadRoundTrip(t *testing.T) {
	store, _ := openStore(t)
	first := recordtest.Accepted()
	second := recordtest.Accepted()
	second.RunID = "run-0002"
	second.Verdict = runrecord.VerdictFailed
	second.FailureKind = runrecord.FailureBudgetOverrun
	second.TerminalStateReached = false
	for _, rec := range []*runrecord.RunRecord{first, second} {
		if err := store.Append(rec); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	back, err := store.ReadSuite(first.SuiteRunID)
	if err != nil {
		t.Fatalf("read suite: %v", err)
	}
	if len(back) != 2 {
		t.Fatalf("expected 2 records, got %d", len(back))
	}
	if !reflect.DeepEqual(&back[0], first) || !reflect.DeepEqual(&back[1], second) {
		t.Fatalf("round trip mismatch:\n%+v\n%+v", back[0], back[1])
	}
}

func TestAppendNeverTruncates(t *testing.T) {
	store, dir := openStore(t)
	rec := recordtest.Accepted()
	if err := store.Append(rec); err != nil {
		t.Fatalf("append: %v", err)
	}
	// Reopening the same directory and appending again must extend the file.
	reopened, err := results.Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	rec2 := recordtest.Accepted()
	rec2.RunID = "run-0002"
	if appendErr := reopened.Append(rec2); appendErr != nil {
		t.Fatalf("append after reopen: %v", appendErr)
	}
	back, readErr := reopened.ReadSuite(rec.SuiteRunID)
	if readErr != nil {
		t.Fatalf("read suite: %v", readErr)
	}
	if len(back) != 2 {
		t.Fatalf("reopen must append, not truncate: got %d records", len(back))
	}
}

func TestAppendRejectsInvalidRecords(t *testing.T) {
	store, _ := openStore(t)
	rec := recordtest.Accepted()
	rec.Verdict = "sideways"
	if err := store.Append(rec); err == nil || !strings.Contains(err.Error(), "append rejected") {
		t.Fatalf("invalid record must be rejected, got %v", err)
	}
}

func TestAppendRejectsUnsafeSuiteIDs(t *testing.T) {
	store, _ := openStore(t)
	// Uppercase is rejected too: case-insensitive filesystems would collide
	// IDs differing only by case onto one file.
	for _, id := range []string{"../escape", "Suite-0001"} {
		rec := recordtest.Accepted()
		rec.SuiteRunID = id
		if err := store.Append(rec); err == nil || !strings.Contains(err.Error(), "suite run id") {
			t.Fatalf("unsafe suite id %q must be rejected, got %v", id, err)
		}
	}
}

func TestReadSuiteRejectsUnknownSchemaVersion(t *testing.T) {
	store, dir := openStore(t)
	rec := recordtest.Accepted()
	if err := store.Append(rec); err != nil {
		t.Fatalf("append: %v", err)
	}
	path := filepath.Join(dir, rec.SuiteRunID+".jsonl")
	line := `{"record_schema_version": 99, "run_id": "run-0099"}` + "\n"
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := file.WriteString(line); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := store.ReadSuite(rec.SuiteRunID); err == nil || !strings.Contains(err.Error(), "schema version") {
		t.Fatalf("unknown version must fail loudly, got %v", err)
	}
}

func TestSuiteRunIDs(t *testing.T) {
	store, _ := openStore(t)
	first := recordtest.Accepted()
	second := recordtest.Accepted()
	second.SuiteRunID = "another-suite"
	for _, rec := range []*runrecord.RunRecord{first, second} {
		if err := store.Append(rec); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	ids, err := store.SuiteRunIDs()
	if err != nil {
		t.Fatalf("suite ids: %v", err)
	}
	want := []string{"another-suite", "suite-0001"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("expected %v, got %v", want, ids)
	}
}
