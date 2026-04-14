package benchmark

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Result holds the outcome of a single benchmark instance run.
type Result struct {
	Patch        string  `json:"model_patch"`
	ArtifactsDir string  `json:"artifacts_dir,omitempty"`
	InstanceID   string  `json:"instance_id"`
	Outcome      Outcome `json:"outcome"`
	ElapsedSecs  float64 `json:"elapsed_seconds"`
}

// WritePreds writes the SWE-bench compatible predictions file.
// Format: {"instance_id": {"model_patch": "..."}, ...}.
func WritePreds(results []Result, path string) error {
	preds := make(map[string]map[string]string, len(results))
	for i := range results {
		preds[results[i].InstanceID] = map[string]string{
			"model_patch": results[i].Patch,
		}
	}

	data, err := json.MarshalIndent(preds, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal preds: %w", err)
	}
	if writeErr := os.WriteFile(path, data, 0644); writeErr != nil {
		return fmt.Errorf("write preds file: %w", writeErr)
	}
	return nil
}

// WriteFullResults writes the detailed results file with all outcome data.
func WriteFullResults(results []Result, path string) error {
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal results: %w", err)
	}
	if writeErr := os.WriteFile(path, data, 0644); writeErr != nil {
		return fmt.Errorf("write results file: %w", writeErr)
	}
	return nil
}

// ArchiveArtifacts copies the DB, logs, config, and forge_state from a completed
// benchmark run into an archive directory for later analysis.
func ArchiveArtifacts(projectDir, archiveDir, instanceID string) error {
	destDir := filepath.Join(archiveDir, instanceID)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("create archive dir: %w", err)
	}

	type artifact struct {
		src  string
		dest string
	}

	artifacts := []artifact{
		{filepath.Join(projectDir, ".maestro", "maestro.db"), "maestro.db"},
		{filepath.Join(projectDir, ".maestro", "config.json"), "config.json"},
		{filepath.Join(projectDir, ".maestro", "forge_state.json"), "forge_state.json"},
		{filepath.Join(projectDir, "logs", "events.jsonl"), "events.jsonl"},
		{filepath.Join(projectDir, "logs", "run.log"), "run.log"},
	}

	var errs []error
	for i := range artifacts {
		if copyErr := copyFileIfExists(artifacts[i].src, filepath.Join(destDir, artifacts[i].dest)); copyErr != nil {
			errs = append(errs, copyErr)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("archive artifacts: %d file(s) failed: %w", len(errs), errs[0])
	}

	return nil
}

func copyFileIfExists(src, dest string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // File doesn't exist — skip silently.
		}
		return fmt.Errorf("open source: %w", err)
	}
	defer func() { _ = srcFile.Close() }()

	destFile, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create dest: %w", err)
	}
	defer func() { _ = destFile.Close() }()

	if _, err = io.Copy(destFile, srcFile); err != nil {
		return fmt.Errorf("copy file: %w", err)
	}
	return nil
}
