package results_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/SnapdragonPartners/maestro/benchmark/results"
)

func TestManifestRoundTrip(t *testing.T) {
	store, _ := openStore(t)
	manifest := &results.Manifest{
		SuiteRunID: "suite-0001",
		StopReason: results.StopSuiteBudgetExhausted,
		CapUSD:     10,
		ChargedUSD: 9.5,
		Attempts: []results.ManifestAttempt{
			{Story: "a", Config: "c", Repeat: 1, Status: results.AttemptCompleted, RunID: "r1"},
			{Story: "a", Config: "c", Repeat: 2, Status: results.AttemptSkipped, Reason: "suite budget"},
		},
	}
	if err := store.WriteManifest(manifest); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	// Overwrite is the contract: manifests are status, not append-only.
	manifest.StopReason = results.StopCompleted
	if err := store.WriteManifest(manifest); err != nil {
		t.Fatalf("rewrite manifest: %v", err)
	}
	back, err := store.ReadManifest("suite-0001")
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if back.StopReason != results.StopCompleted || !reflect.DeepEqual(back.Attempts, manifest.Attempts) {
		t.Fatalf("round trip mismatch: %+v", back)
	}
	if back.UpdatedAt.IsZero() {
		t.Fatalf("every write must stamp updated_at")
	}
}

func TestManifestRejections(t *testing.T) {
	store, dir := openStore(t)
	if err := store.WriteManifest(nil); err == nil {
		t.Fatalf("nil manifest must be rejected")
	}
	if err := store.WriteManifest(&results.Manifest{SuiteRunID: "../escape"}); err == nil {
		t.Fatalf("unsafe suite id must be rejected")
	}
	// Unknown schema versions fail loudly on read.
	path := filepath.Join(dir, "old.manifest.json")
	if err := os.WriteFile(path, []byte(`{"manifest_schema_version": 99, "suite_run_id": "old"}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := store.ReadManifest("old"); err == nil || !strings.Contains(err.Error(), "schema version") {
		t.Fatalf("unknown manifest version must fail loudly, got %v", err)
	}
}
