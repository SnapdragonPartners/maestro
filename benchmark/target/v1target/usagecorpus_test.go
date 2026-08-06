package v1target

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The budget tail's half of the two-sided usage corpus.
//
// The orchestrator's importer cannot call this package — it reads the results
// store as FILES, deliberately, so that the runner stays a standalone module
// versioned against targets that do not exist yet (ADR 0025, design D1). The
// cost of that is a second implementation of the usage surface's rules, and
// design D9 makes them the SAME rules in both places, since the writer's
// guarantee is not evidence to a reader parsing a file some other build
// wrote. This is the alarm: both readers run every case in
// benchmark/testdata/usage_corpus and must reach the same verdict.
//
// A case may DECLARE that the two differ, with a reason. Silence means they
// must agree, so a rule tightened on one side turns a case red immediately.
const usageCorpusDir = "../../testdata/usage_corpus"

type usageCorpusCase struct {
	Line json.RawMessage `json:"line"`
	// RawLine is for cases about the LINE rather than the entry — trailing
	// content and non-objects, which no entry object can express.
	RawLine string `json:"raw_line,omitempty"`

	Expect string `json:"expect"`
	Why    string `json:"why,omitempty"`
	// TailExpect is the verdict THIS side must reach when it differs.
	TailExpect string `json:"tail_expect,omitempty"`
	Divergence string `json:"divergence,omitempty"`
}

// expected returns the verdict this side must reach.
func (c usageCorpusCase) expected() string {
	if c.TailExpect != "" {
		return c.TailExpect
	}
	return c.Expect
}

func (c usageCorpusCase) text() string {
	if c.RawLine != "" {
		return c.RawLine
	}
	return string(c.Line)
}

// TestUsageCorpusAgreesWithTheTail runs every case through the tail's own
// path.
//
// Decode, validate, AND budgetTokens — which is exactly what consume does,
// and the reason it matters: the overflow rule lives in the summing step
// rather than in validate, so a corpus that stopped at validate would let an
// overflowing line pass here while the importer refused it, and call the two
// sides agreed.
func TestUsageCorpusAgreesWithTheTail(t *testing.T) {
	entries, err := os.ReadDir(usageCorpusDir)
	if err != nil {
		t.Fatalf("read usage corpus: %v", err)
	}
	ran := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		t.Run(name, func(t *testing.T) {
			raw, readErr := os.ReadFile(filepath.Join(usageCorpusDir, entry.Name()))
			if readErr != nil {
				t.Fatalf("read %s: %v", entry.Name(), readErr)
			}
			var testCase usageCorpusCase
			if err := json.Unmarshal(raw, &testCase); err != nil {
				t.Fatalf("decode %s: %v", entry.Name(), err)
			}

			line, decodeErr := decodeUsageLine(testCase.text())
			verdictErr := decodeErr
			if verdictErr == nil {
				verdictErr = line.validate()
			}
			if verdictErr == nil {
				_, verdictErr = line.budgetTokens()
			}

			switch testCase.expected() {
			case "accept":
				if verdictErr != nil {
					t.Fatalf("the tail refused a case the corpus accepts (%s): %v", testCase.Why, verdictErr)
				}
			case "reject":
				if verdictErr == nil {
					t.Fatalf("the tail accepted a case the corpus rejects (%s)", testCase.Why)
				}
			default:
				t.Fatalf("%s declares expect %q", entry.Name(), testCase.expected())
			}
			if testCase.TailExpect != "" && testCase.Divergence == "" {
				t.Errorf("%s declares a different verdict for this side without saying why", entry.Name())
			}
		})
		ran++
	}
	if ran == 0 {
		t.Fatal("the usage corpus is empty; every assertion above would pass vacuously")
	}
}
