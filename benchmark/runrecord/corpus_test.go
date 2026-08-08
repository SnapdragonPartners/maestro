package runrecord_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SnapdragonPartners/maestro/benchmark/runrecord"
)

// The runner's half of the two-sided conformance corpus.
//
// The orchestrator's importer cannot call this package — it reads the results
// store as FILES, deliberately, so that the runner stays a standalone module
// versioned against targets that do not exist yet (ADR 0025, design D1). The
// cost of that is a second implementation of these semantics, and this is the
// alarm: both validators run every case in
// benchmark/testdata/import_corpus and must reach the same verdict.
//
// A case may DECLARE that the two differ, with a reason. That is what makes
// an undeclared disagreement a failure — silence means they must agree, so a
// rule tightened on one side and not the other turns a case red immediately.
const corpusDir = "../testdata/import_corpus"

type corpusCase struct {
	Record json.RawMessage `json:"record"`
	// RawLine is for cases about the LINE rather than the record — trailing
	// content, which no JSON object can express. It is fed through the same
	// decoding as any other case.
	RawLine string `json:"raw_line,omitempty"`

	Expect       string `json:"expect"`
	Reason       string `json:"reason,omitempty"`
	RunnerExpect string `json:"runner_expect,omitempty"`
	Divergence   string `json:"divergence,omitempty"`
}

// expected returns the verdict THIS side must reach.
func (c corpusCase) expected() string {
	if c.RunnerExpect != "" {
		return c.RunnerExpect
	}
	return c.Expect
}

func TestCorpusAgreesWithTheRunner(t *testing.T) {
	entries, err := os.ReadDir(corpusDir)
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	ran := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		t.Run(name, func(t *testing.T) {
			raw, readErr := os.ReadFile(filepath.Join(corpusDir, entry.Name()))
			if readErr != nil {
				t.Fatalf("read: %v", readErr)
			}
			var testCase corpusCase
			if decodeErr := json.Unmarshal(raw, &testCase); decodeErr != nil {
				t.Fatalf("decode case: %v", decodeErr)
			}
			// RawLine cases go through the runner's REAL decoding, which is
			// json.Unmarshal over one complete scanner line
			// (results.ReadSuite). That rejects trailing content, so a
			// trailing-object or stray-delimiter case is refused by both
			// sides and is NOT a divergence — an earlier version of this
			// test skipped these cases and declared one that did not exist.
			// Unknown-field handling is the only genuine decoding divergence:
			// encoding/json ignores what strict decoding refuses.
			line := testCase.Record
			if testCase.RawLine != "" {
				line = json.RawMessage(testCase.RawLine)
			}

			var record runrecord.RunRecord
			if unmarshalErr := json.Unmarshal(line, &record); unmarshalErr != nil {
				if testCase.expected() == "reject" {
					return // malformed or over-long JSON is a rejection either way
				}
				t.Fatalf("must be accepted, but did not decode: %v", unmarshalErr)
			}
			validateErr := record.Validate()
			if testCase.expected() == "accept" {
				if validateErr != nil {
					t.Fatalf("must be accepted by the runner, got: %v", validateErr)
				}
				return
			}
			if validateErr == nil {
				t.Fatalf("must be refused by the runner (importer rule: %q), got no error", testCase.Reason)
			}
		})
		ran++
	}
	if ran == 0 {
		t.Fatal("no corpus cases ran; this test would pass however far the two sides had drifted")
	}
}
