package benchmarkimport

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// maxRecordBytes bounds one JSONL line, mirroring the runner's own limit.
const maxRecordBytes = 16 * 1024 * 1024

// Attempt statuses and stop reasons the manifest may carry.
//
//nolint:gochecknoglobals // Package-level sets, immutable after init.
var (
	knownAttemptStatus = map[string]bool{
		"planned": true, "completed": true, "skipped": true,
	}
	knownStopReason = map[string]bool{
		"completed": true, "suite-budget-exhausted": true,
		"interrupted": true, "running": true,
	}
)

// ManifestSchemaVersion is the manifest contract this build reads.
const ManifestSchemaVersion = 2

// ManifestAttempt is one planned cell of the suite matrix and its outcome.
type ManifestAttempt struct {
	Story  string `json:"story"`
	Config string `json:"config"`
	Status string `json:"status"`
	RunID  string `json:"run_id,omitempty"`
	Reason string `json:"reason,omitempty"`
	Repeat int    `json:"repeat"`
}

// BudgetAccount is one config's suite-budget accounting.
type BudgetAccount struct {
	Config    string  `json:"config"`
	Dimension string  `json:"dimension"`
	Cap       float64 `json:"cap"`
	Charged   float64 `json:"charged"`
	Observed  float64 `json:"observed"`
}

// Manifest records what a suite planned and what happened.
type Manifest struct {
	UpdatedAt      time.Time         `json:"updated_at"`
	SuiteRunID     string            `json:"suite_run_id"`
	StopReason     string            `json:"stop_reason"`
	Attempts       []ManifestAttempt `json:"attempts"`
	BudgetAccounts []BudgetAccount   `json:"budget_accounts"`
	SchemaVersion  int               `json:"manifest_schema_version"`
}

// Terminal reports whether the suite has stopped.
//
// Only a terminal suite gets a report (design D7). The manifest is STATUS,
// rewritten on every update, so its content is not an identity — and a suite
// imported mid-flight will legitimately have a different manifest next time.
func (m *Manifest) Terminal() bool { return m.StopReason != "running" }

// Suite is one suite run as it was read from disk, already validated whole.
type Suite struct {
	// Dir is the results store the suite was read from, so evidence can be
	// resolved against it rather than against the absolute paths the records
	// carry (design D8).
	Dir      string
	Records  []Record
	Manifest Manifest
}

// ErrIncoherent reports that a suite's files disagree with each other.
//
// Distinguished from a per-record rejection because the fault is not in any
// one record: the suite as a whole describes no consistent set of attempts,
// and the operator-authored report is signed on the strength of that
// consistency.
var ErrIncoherent = errors.New("suite files disagree")

// ReadSuite loads one suite run and validates it AS A WHOLE, refusing it
// entirely rather than importing part of it.
//
// The two-sided fixture proves this package and the runner agree about the
// record CONTRACT. It says nothing about whether a particular file on disk is
// coherent, which is what this adds.
func ReadSuite(dir, suiteRunID string) (*Suite, error) {
	if !suiteRunIDPattern.MatchString(suiteRunID) {
		return nil, fmt.Errorf("%w: suite run id %q", ErrIncoherent, suiteRunID)
	}
	manifest, err := readManifest(dir, suiteRunID)
	if err != nil {
		return nil, err
	}
	records, err := readRecords(dir, suiteRunID)
	if err != nil {
		return nil, err
	}
	suite := &Suite{Manifest: *manifest, Records: records, Dir: dir}
	if err := suite.checkCoherence(suiteRunID); err != nil {
		return nil, err
	}
	return suite, nil
}

func readManifest(dir, suiteRunID string) (*Manifest, error) {
	path := filepath.Join(dir, suiteRunID+".manifest.json")
	raw, err := os.ReadFile(path) //nolint:gosec // suiteRunID is pattern-checked above
	if err != nil {
		return nil, fmt.Errorf("read manifest %s: %w", path, err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decode manifest %s: %w", path, err)
	}
	if manifest.SchemaVersion != ManifestSchemaVersion {
		return nil, fmt.Errorf("%w: manifest %s is version %d, this build reads %d",
			ErrIncoherent, path, manifest.SchemaVersion, ManifestSchemaVersion)
	}
	return &manifest, nil
}

func readRecords(dir, suiteRunID string) ([]Record, error) {
	path := filepath.Join(dir, suiteRunID+".jsonl")
	file, err := os.Open(path) //nolint:gosec // suiteRunID is pattern-checked above
	if err != nil {
		return nil, fmt.Errorf("open records %s: %w", path, err)
	}
	defer file.Close() //nolint:errcheck // read-only
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), maxRecordBytes)
	var records []Record
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		record, err := DecodeRecord(text)
		if err != nil {
			return nil, fmt.Errorf("%s line %d: %w", path, line, err)
		}
		if err := record.Validate(); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", path, line, err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}
	return records, nil
}

// checkCoherence enforces the rules no single record can see.
//
// Each is a fact about the RELATIONSHIP between the three files: a record
// knows its own suite id but not which file it was found in, and it certainly
// does not know whether the manifest agrees that it happened.
func (s *Suite) checkCoherence(suiteRunID string) error {
	if s.Manifest.SuiteRunID != suiteRunID {
		return fmt.Errorf("%w: file names suite %q, manifest names %q",
			ErrIncoherent, suiteRunID, s.Manifest.SuiteRunID)
	}
	if !knownStopReason[s.Manifest.StopReason] {
		return fmt.Errorf("%w: manifest stop_reason %q", ErrIncoherent, s.Manifest.StopReason)
	}
	seen := make(map[string]bool, len(s.Records))
	for i := range s.Records {
		record := &s.Records[i]
		if record.SuiteRunID != suiteRunID {
			return fmt.Errorf("%w: record %s names suite %q, file names %q",
				ErrIncoherent, record.RunID, record.SuiteRunID, suiteRunID)
		}
		if seen[record.RunID] {
			// A duplicate is how one attempt becomes two ledger rows, or one
			// rejected import: the second offer of the same identity would
			// be compared against the first.
			return fmt.Errorf("%w: run id %q appears twice", ErrIncoherent, record.RunID)
		}
		seen[record.RunID] = true
	}
	return s.checkManifestAgreement(seen)
}

// checkManifestAgreement compares the manifest's account of the matrix
// against the records that actually exist.
//
// Both directions matter and they fail differently: a completed entry with no
// record is a LOST attempt, and a record with no entry is a suite the
// manifest does not describe. Either makes the manifest fiction, and the
// report quotes the manifest.
func (s *Suite) checkManifestAgreement(records map[string]bool) error {
	completed := make(map[string]bool, len(s.Manifest.Attempts))
	for i := range s.Manifest.Attempts {
		attempt := &s.Manifest.Attempts[i]
		if !knownAttemptStatus[attempt.Status] {
			return fmt.Errorf("%w: manifest attempt status %q", ErrIncoherent, attempt.Status)
		}
		if attempt.Status != "completed" {
			continue
		}
		if attempt.RunID == "" {
			return fmt.Errorf("%w: a completed manifest attempt names no run id", ErrIncoherent)
		}
		if completed[attempt.RunID] {
			return fmt.Errorf("%w: manifest lists run id %q twice as completed",
				ErrIncoherent, attempt.RunID)
		}
		completed[attempt.RunID] = true
		if !records[attempt.RunID] {
			return fmt.Errorf("%w: manifest reports %q completed but no record exists; the attempt is lost",
				ErrIncoherent, attempt.RunID)
		}
	}
	for runID := range records {
		if !completed[runID] {
			return fmt.Errorf("%w: record %q has no completed manifest entry; the manifest does not "+
				"describe this suite", ErrIncoherent, runID)
		}
	}
	return nil
}

// EvidenceDir resolves an attempt's evidence directory INSIDE the results
// store, and proves the resolved path stays there.
//
// The record's own EvidencePointer locations are absolute paths recorded on
// the machine that ran the attempt: faithful provenance and a poor locator,
// since the store is portable and those paths are not. So the directory is
// derived from the store's own layout, and the value that names it is
// untrusted input.
func (s *Suite) EvidenceDir(runID string) (string, error) {
	if !runIDPattern.MatchString(runID) {
		return "", reject(runID, ReasonRunID, "cannot be used as a path component")
	}
	root, err := filepath.EvalSymlinks(filepath.Join(s.Dir, "evidence"))
	if err != nil {
		return "", fmt.Errorf("resolve evidence root: %w", err)
	}
	dir := filepath.Join(root, runID)
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("resolve evidence dir: %w", err)
	}
	// Compared AFTER symlink resolution, so the check sees what the
	// filesystem will actually open, and with the separator included so
	// "evidence-other" cannot pass a prefix test for "evidence".
	if resolved != root && !strings.HasPrefix(resolved, root+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: evidence for %q resolves to %s, outside %s",
			ErrIncoherent, runID, resolved, root)
	}
	return resolved, nil
}
