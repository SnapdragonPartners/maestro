package benchmarkimport

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
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

// statusCompleted is the one attempt status that must name a run and be
// matched by a record.
const statusCompleted = "completed"

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

// manifestSuffix is how a suite announces itself in the results store.
const manifestSuffix = ".manifest.json"

// ListSuites names every suite run the results store holds.
//
// The store's own layout is the index: there is no catalogue file, and
// adding one would be a second thing to keep true. A name that is not a
// well-formed suite id is skipped rather than refused — the store is a
// directory an operator can put anything in, and one stray file should not
// make every suite in it unimportable.
func ListSuites(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read results store %s: %w", dir, err)
	}
	suites := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), manifestSuffix)
		if name == entry.Name() || !suiteRunIDPattern.MatchString(name) {
			continue
		}
		suites = append(suites, name)
	}
	sort.Strings(suites)
	return suites, nil
}

func readManifest(dir, suiteRunID string) (*Manifest, error) {
	path := filepath.Join(dir, suiteRunID+manifestSuffix)
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
	// Exhaustion proven by decoding AGAIN and requiring io.EOF, exactly as
	// the record path does. Decoder.More would not do: it answers "is there
	// another element in the array or object I am inside?", and after a
	// top-level value there is none, so it reports false for a stray `]` or
	// `}` — the one form of trailing garbage that looks like an ending. The
	// runner's json.Unmarshal rejects all of it, so accepting any here would
	// be a divergence nobody declared.
	var rest json.RawMessage
	if err := decoder.Decode(&rest); err == nil {
		return nil, fmt.Errorf("%w: manifest %s carries content after the object", ErrIncoherent, path)
	} else if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: manifest %s: %w", ErrIncoherent, path, err)
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
	// Keyed by run id to the RECORD, not to a bool: the manifest duplicates
	// each attempt's story and config, and membership alone would accept an
	// entry naming the right run and the wrong story — leaving the report's
	// matrix inconsistent with the record underneath it.
	byRunID := make(map[string]attemptSubject, len(s.Records))
	for i := range s.Records {
		record := &s.Records[i]
		if record.SuiteRunID != suiteRunID {
			return fmt.Errorf("%w: record %s names suite %q, file names %q",
				ErrIncoherent, record.RunID, record.SuiteRunID, suiteRunID)
		}
		if _, seen := byRunID[record.RunID]; seen {
			// A duplicate is how one attempt becomes two ledger rows, or one
			// rejected import: the second offer of the same identity would
			// be compared against the first.
			return fmt.Errorf("%w: run id %q appears twice", ErrIncoherent, record.RunID)
		}
		byRunID[record.RunID] = attemptSubject{story: record.StoryID, config: record.ConfigName}
	}
	return checkManifestCoherence(&s.Manifest, byRunID)
}

// attemptSubject is what a manifest entry and an attempt both claim: which
// story ran under which config.
//
// The manifest duplicates these, so they are the pair that has to agree.
// Keyed on them rather than on membership alone, because a manifest entry
// naming the right run and the wrong story leaves the report's matrix
// inconsistent with the attempt underneath it.
type attemptSubject struct {
	story  string
	config string
}

// checkManifestCoherence compares a manifest's account of the matrix against
// the attempts that claim to have completed under it.
//
// Shared by the SUITE READER, where the attempts are records on disk, and by
// the REPORT VALIDATOR, where they are the entries of a payload a caller
// handed the plane. Not a convenience: the report quotes the manifest, so a
// report whose manifest is incoherent is a Management claim that contradicts
// itself, and a rule enforced only on the way in from disk is no rule at all
// for a caller reaching the seam directly.
//
// Both directions matter and they fail differently: a completed entry with no
// attempt is a LOST attempt, and an attempt with no entry is a suite the
// manifest does not describe. Either makes the manifest fiction.
func checkManifestCoherence(manifest *Manifest, attempts map[string]attemptSubject) error {
	if manifest.SchemaVersion != ManifestSchemaVersion {
		return fmt.Errorf("%w: manifest is version %d, this build reads %d",
			ErrIncoherent, manifest.SchemaVersion, ManifestSchemaVersion)
	}
	if !knownStopReason[manifest.StopReason] {
		return fmt.Errorf("%w: manifest stop_reason %q", ErrIncoherent, manifest.StopReason)
	}
	completed := make(map[string]bool, len(manifest.Attempts))
	for i := range manifest.Attempts {
		attempt := &manifest.Attempts[i]
		if err := checkAttemptShape(attempt); err != nil {
			return err
		}
		if attempt.Status != statusCompleted {
			continue
		}
		if completed[attempt.RunID] {
			return fmt.Errorf("%w: manifest lists run id %q twice as completed",
				ErrIncoherent, attempt.RunID)
		}
		completed[attempt.RunID] = true
		subject, present := attempts[attempt.RunID]
		if !present {
			return fmt.Errorf("%w: manifest reports %q completed but no record exists; the attempt is lost",
				ErrIncoherent, attempt.RunID)
		}
		// The duplicated fields must agree. They are the matrix the report
		// quotes, and a disagreement here means the two accounts describe
		// different runs of the same identity.
		if attempt.Story != subject.story {
			return fmt.Errorf("%w: manifest says %q ran story %q, the record says %q",
				ErrIncoherent, attempt.RunID, attempt.Story, subject.story)
		}
		if attempt.Config != subject.config {
			return fmt.Errorf("%w: manifest says %q ran config %q, the record says %q",
				ErrIncoherent, attempt.RunID, attempt.Config, subject.config)
		}
	}
	for runID := range attempts {
		if !completed[runID] {
			return fmt.Errorf("%w: record %q has no completed manifest entry; the manifest does not "+
				"describe this suite", ErrIncoherent, runID)
		}
	}
	return nil
}

// checkAttemptShape validates one manifest entry on its own terms.
//
// Every entry, not only the completed ones: a planned or skipped cell is
// still part of the matrix the report quotes, and one naming no story is a
// cell nobody can interpret.
func checkAttemptShape(attempt *ManifestAttempt) error {
	switch {
	case !knownAttemptStatus[attempt.Status]:
		return fmt.Errorf("%w: manifest attempt status %q", ErrIncoherent, attempt.Status)
	case strings.TrimSpace(attempt.Story) == "":
		return fmt.Errorf("%w: a manifest attempt names no story", ErrIncoherent)
	case strings.TrimSpace(attempt.Config) == "":
		return fmt.Errorf("%w: a manifest attempt names no config", ErrIncoherent)
	case attempt.Repeat < 1:
		// Repeats are 1-based in the runner's own planner; a zero is an
		// unset field rather than a cell.
		return fmt.Errorf("%w: manifest attempt for %q/%q has repeat %d",
			ErrIncoherent, attempt.Story, attempt.Config, attempt.Repeat)
	case attempt.Status == statusCompleted && attempt.RunID == "":
		return fmt.Errorf("%w: a completed manifest attempt names no run id", ErrIncoherent)
	case attempt.Status != statusCompleted && attempt.RunID != "":
		// A cell that did not complete has no run to name, and naming one
		// would make it look like a record went missing.
		return fmt.Errorf("%w: a %s manifest attempt names run id %q",
			ErrIncoherent, attempt.Status, attempt.RunID)
	}
	return nil
}

// EvidenceDir resolves an attempt's evidence directory INSIDE the results
// store, and proves the resolved path stays there.
//
// ABSENCE IS NOT AN ERROR. present=false means this attempt has no evidence
// on disk, which D8 makes an ordinary outcome: the attempt imports with zero
// attachments. Not every attempt produces evidence, an interrupted suite may
// never have written any, and a store can be pruned — none of which should
// cost an operator their import. The distinction matters because the two
// outcomes want opposite responses: absence is reported and moved past, while
// a directory that exists but cannot be trusted must stop the import.
//
// The record's own EvidencePointer locations are absolute paths recorded on
// the machine that ran the attempt: faithful provenance and a poor locator,
// since the store is portable and those paths are not. So the directory is
// derived from the store's own layout, and the value that names it is
// untrusted input.
//
// The anchor is the RESULTS-STORE root, not the evidence root. An earlier
// version compared against the resolved evidence directory, which meant an
// `evidence` symlink pointing somewhere else made every candidate beneath it
// look safely contained — the check compared an escaped root against itself.
//
// And symlinks are refused rather than followed, even when their target
// stays inside the store. A run directory that is a link to ANOTHER run's
// directory resolves to a legitimate in-store path and passes any containment
// test, while attributing one attempt's evidence to another — a
// misattribution containment was never able to see.
func (s *Suite) EvidenceDir(runID string) (dir string, present bool, err error) {
	if !runIDPattern.MatchString(runID) {
		return "", false, reject(runID, ReasonRunID, "cannot be used as a path component")
	}
	root, rootErr := filepath.EvalSymlinks(s.Dir)
	if rootErr != nil {
		return "", false, fmt.Errorf("resolve results store root: %w", rootErr)
	}
	evidenceRoot := filepath.Join(root, "evidence")
	switch found, linkErr := inspectDir(evidenceRoot, "the evidence root"); {
	case linkErr != nil:
		return "", false, linkErr
	case !found:
		// A store with no evidence tree at all. Every attempt in it imports
		// without attachments; that is a fact about the store, not a fault.
		return "", false, nil
	}
	candidate := filepath.Join(evidenceRoot, runID)
	switch found, linkErr := inspectDir(candidate, "an evidence directory"); {
	case linkErr != nil:
		return "", false, linkErr
	case !found:
		return "", false, nil
	}
	// Resolved and compared anyway. The link checks above cover the two
	// components this function builds; this covers everything else on the
	// path, and costs one syscall.
	resolved, evalErr := filepath.EvalSymlinks(candidate)
	if evalErr != nil {
		return "", false, fmt.Errorf("resolve evidence dir: %w", evalErr)
	}
	// The separator is part of the prefix, so a sibling named
	// "evidence-other" cannot pass a test for "evidence".
	if !strings.HasPrefix(resolved, evidenceRoot+string(filepath.Separator)) {
		return "", false, fmt.Errorf("%w: evidence for %q resolves to %s, outside %s",
			ErrIncoherent, runID, resolved, evidenceRoot)
	}
	return resolved, true, nil
}

// inspectDir reports whether path is a usable directory, refusing one that is
// a symlink or not a directory at all.
//
// Lstat rather than Stat: Stat follows the link and reports the target, which
// is the question this is not asking. A missing path is reported as absent
// rather than as an error — the caller decides what absence means, and for
// evidence it means zero attachments.
func inspectDir(path, what string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", what, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("%w: %s (%s) is a symbolic link; evidence is read from the store's own "+
			"layout, and a link can attribute one attempt's files to another even when its target "+
			"is inside the store", ErrIncoherent, what, path)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("%w: %s (%s) is not a directory", ErrIncoherent, what, path)
	}
	return true, nil
}
