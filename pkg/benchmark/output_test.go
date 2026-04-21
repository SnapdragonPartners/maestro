package benchmark

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWritePreds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "preds.json")

	results := []Result{
		{InstanceID: "inst-1", Outcome: OutcomeSuccess, Patch: "diff --git a/foo.py b/foo.py\n+fixed"},
		{InstanceID: "inst-2", Outcome: OutcomeTerminalFailure, Patch: ""},
	}

	if err := WritePreds(results, path); err != nil {
		t.Fatalf("WritePreds() error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read preds file: %v", err)
	}

	var preds map[string]map[string]string
	if err := json.Unmarshal(data, &preds); err != nil {
		t.Fatalf("parse preds JSON: %v", err)
	}

	if preds["inst-1"]["model_patch"] != "diff --git a/foo.py b/foo.py\n+fixed" {
		t.Errorf("unexpected patch for inst-1: %q", preds["inst-1"]["model_patch"])
	}
	if preds["inst-2"]["model_patch"] != "" {
		t.Errorf("expected empty patch for inst-2, got %q", preds["inst-2"]["model_patch"])
	}
}

func TestWriteFullResults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "results.json")

	results := []Result{
		{
			InstanceID:  "inst-1",
			Outcome:     OutcomeSuccess,
			Patch:       "patch-data",
			ElapsedSecs: 123.45,
		},
	}

	if err := WriteFullResults(results, path); err != nil {
		t.Fatalf("WriteFullResults() error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read results file: %v", err)
	}

	var loaded []Result
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("parse results JSON: %v", err)
	}

	if len(loaded) != 1 {
		t.Fatalf("expected 1 result, got %d", len(loaded))
	}
	if loaded[0].InstanceID != "inst-1" {
		t.Errorf("expected instance_id 'inst-1', got %q", loaded[0].InstanceID)
	}
	if loaded[0].Outcome != OutcomeSuccess {
		t.Errorf("expected outcome success, got %s", loaded[0].Outcome)
	}
	if loaded[0].ElapsedSecs != 123.45 {
		t.Errorf("expected elapsed 123.45, got %f", loaded[0].ElapsedSecs)
	}
}

func TestArchiveArtifacts(t *testing.T) {
	projectDir := t.TempDir()
	archiveDir := t.TempDir()

	// Create some fake artifacts.
	maestroDir := filepath.Join(projectDir, ".maestro")
	logsDir := filepath.Join(projectDir, "logs")
	if err := os.MkdirAll(maestroDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(maestroDir, "config.json"), []byte(`{"test": true}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logsDir, "run.log"), []byte("log data"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := ArchiveArtifacts(projectDir, archiveDir, "inst-1"); err != nil {
		t.Fatalf("ArchiveArtifacts() error: %v", err)
	}

	destDir := filepath.Join(archiveDir, "inst-1")
	if _, statErr := os.Stat(destDir); os.IsNotExist(statErr) {
		t.Fatal("archive directory not created")
	}

	configData, readErr := os.ReadFile(filepath.Join(destDir, "config.json"))
	if readErr != nil {
		t.Fatalf("archived config.json not found: %v", readErr)
	}
	if string(configData) != `{"test": true}` {
		t.Errorf("unexpected config content: %s", string(configData))
	}

	logData, readErr := os.ReadFile(filepath.Join(destDir, "run.log"))
	if readErr != nil {
		t.Fatalf("archived run.log not found: %v", readErr)
	}
	if string(logData) != "log data" {
		t.Errorf("unexpected log content: %s", string(logData))
	}

	// Missing files (maestro.db, forge_state.json, events.jsonl) should be skipped without error.
}
