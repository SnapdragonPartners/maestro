package results

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ManifestSchemaVersion is the current suite-manifest schema.
const ManifestSchemaVersion = 1

// manifestExtension is the manifest file suffix; one per suite run.
const manifestExtension = ".manifest.json"

// Attempt statuses in a manifest.
const (
	// AttemptPlanned means the attempt has not started yet.
	AttemptPlanned = "planned"
	// AttemptCompleted means a run record exists for the attempt.
	AttemptCompleted = "completed"
	// AttemptSkipped means the attempt was deliberately not started.
	AttemptSkipped = "skipped"
)

// Suite stop reasons.
const (
	// StopCompleted means every planned attempt ran.
	StopCompleted = "completed"
	// StopSuiteBudgetExhausted means the suite cost cap stopped admission.
	StopSuiteBudgetExhausted = "suite-budget-exhausted"
	// StopInterrupted means the suite ended before finishing for another
	// reason (signal, fatal error).
	StopInterrupted = "interrupted"
	// StopRunning means the suite is still in progress.
	StopRunning = "running"
)

// ManifestAttempt is one planned cell of the suite matrix and its outcome.
type ManifestAttempt struct {
	Story  string `json:"story"`
	Config string `json:"config"`
	Status string `json:"status"`
	RunID  string `json:"run_id,omitempty"`
	Reason string `json:"reason,omitempty"`
	Repeat int    `json:"repeat"`
}

// Manifest records what a suite run planned and what actually happened, so
// a deliberately partial suite is distinguishable from a corrupt results
// file (design_engine.md).
type Manifest struct {
	UpdatedAt     time.Time         `json:"updated_at"`
	SuiteRunID    string            `json:"suite_run_id"`
	StopReason    string            `json:"stop_reason"`
	Attempts      []ManifestAttempt `json:"attempts"`
	CapUSD        float64           `json:"cap_usd"`
	ChargedUSD    float64           `json:"charged_usd"`
	ObservedUSD   float64           `json:"observed_usd"`
	SchemaVersion int               `json:"manifest_schema_version"`
}

// WriteManifest persists the manifest for its suite run, atomically
// replacing any prior version (manifests are status, not append-only
// records).
func (s *Store) WriteManifest(m *Manifest) error {
	if m == nil {
		return fmt.Errorf("nil manifest")
	}
	if err := validSuiteRunID(m.SuiteRunID); err != nil {
		return err
	}
	m.SchemaVersion = ManifestSchemaVersion
	m.UpdatedAt = time.Now().UTC()
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest %s: %w", m.SuiteRunID, err)
	}
	path := s.manifestPath(m.SuiteRunID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return fmt.Errorf("write manifest %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace manifest %s: %w", path, err)
	}
	return nil
}

// ReadManifest loads the manifest for a suite run.
func (s *Store) ReadManifest(suiteRunID string) (*Manifest, error) {
	if err := validSuiteRunID(suiteRunID); err != nil {
		return nil, err
	}
	path := s.manifestPath(suiteRunID)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest %s: %w", path, err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("decode manifest %s: %w", path, err)
	}
	if m.SchemaVersion != ManifestSchemaVersion {
		return nil, fmt.Errorf("manifest %s schema version %d: this runner knows only version %d", path, m.SchemaVersion, ManifestSchemaVersion)
	}
	return &m, nil
}

func (s *Store) manifestPath(suiteRunID string) string {
	return filepath.Join(s.dir, suiteRunID+manifestExtension)
}

// EvidenceDir creates (if needed) and returns the durable evidence
// directory for one run — evidence files must outlive workspace cleanup,
// so they live under the results store.
func (s *Store) EvidenceDir(runID string) (string, error) {
	dir := filepath.Join(s.dir, "evidence", runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("evidence dir %s: %w", dir, err)
	}
	return dir, nil
}
