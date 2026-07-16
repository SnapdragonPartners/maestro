// Package results implements the runner's self-contained results store:
// append-only, schema-versioned JSONL, one file per suite run, with zero
// dependency on the Phase 2 data plane (ADR 0025). Record shapes are
// designed for later import as benchmark-scoped artifacts.
package results

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/SnapdragonPartners/maestro/benchmark/runrecord"
)

// fileExtension is the results file suffix; one file per suite run.
const fileExtension = ".jsonl"

// maxRecordBytes bounds a single JSONL line on read (a run record with
// generous evidence stays far below this).
const maxRecordBytes = 16 * 1024 * 1024

//nolint:gochecknoglobals // Package-level compiled regex for performance.
var suiteRunIDPattern = regexp.MustCompile(`^[a-z0-9_-]+$`)

// Store is an append-only results directory.
type Store struct {
	dir string
}

// Open creates the directory if needed and returns the store.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("open results store %s: %w", dir, err)
	}
	return &Store{dir: dir}, nil
}

// Append validates rec and appends it to its suite's file. Files are only
// ever appended to — never truncated or rewritten.
func (s *Store) Append(rec *runrecord.RunRecord) error {
	if rec == nil {
		return fmt.Errorf("append rejected: nil run record")
	}
	if err := rec.Validate(); err != nil {
		return fmt.Errorf("append rejected: %w", err)
	}
	if err := validSuiteRunID(rec.SuiteRunID); err != nil {
		return err
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal run record %s: %w", rec.RunID, err)
	}
	path := s.suitePath(rec.SuiteRunID)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open results file %s: %w", path, err)
	}
	_, writeErr := file.Write(append(line, '\n'))
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return fmt.Errorf("append to %s: %w", path, err)
	}
	return nil
}

// ReadSuite returns every record of one suite run, validating each and
// rejecting unknown record schema versions loudly.
func (s *Store) ReadSuite(suiteRunID string) ([]runrecord.RunRecord, error) {
	if err := validSuiteRunID(suiteRunID); err != nil {
		return nil, err
	}
	path := s.suitePath(suiteRunID)
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open results file %s: %w", path, err)
	}
	defer file.Close() //nolint:errcheck // read-only descriptor
	var records []runrecord.RunRecord
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), maxRecordBytes)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		var rec runrecord.RunRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", path, lineNo, err)
		}
		if err := rec.Validate(); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", path, lineNo, err)
		}
		records = append(records, rec)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}
	return records, nil
}

// SuiteRunIDs lists the suite runs present in the store, sorted.
func (s *Store) SuiteRunIDs() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("read results store %s: %w", s.dir, err)
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, fileExtension) {
			continue
		}
		ids = append(ids, strings.TrimSuffix(name, fileExtension))
	}
	sort.Strings(ids)
	return ids, nil
}

func (s *Store) suitePath(suiteRunID string) string {
	return filepath.Join(s.dir, suiteRunID+fileExtension)
}

// validSuiteRunID keeps suite run IDs filename-safe and portable: lowercase
// only, because case-insensitive filesystems (macOS default) would collide
// IDs differing only by case onto one .jsonl file.
func validSuiteRunID(id string) error {
	if !suiteRunIDPattern.MatchString(id) {
		return fmt.Errorf("suite run id %q must match [a-z0-9_-]+", id)
	}
	return nil
}
