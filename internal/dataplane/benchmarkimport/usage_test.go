package benchmarkimport_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"orchestrator/internal/dataplane/benchmarkimport"
)

// usageCorpusDir is the importer's half of the two-sided usage corpus. The
// budget tail runs the same cases from the benchmark module.
const usageCorpusDir = "../../../benchmark/testdata/usage_corpus"

// usageCase is one case as it appears on disk.
type usageCase struct {
	Line json.RawMessage `json:"line"`
	// RawLine is for cases about the LINE rather than the entry — trailing
	// content and non-objects, which no entry object can express.
	RawLine string `json:"raw_line,omitempty"`

	Expect string `json:"expect"`
	Why    string `json:"why,omitempty"`
	// TailExpect and Divergence let a case DECLARE that the two readers
	// differ. Silence means they must agree, which is what makes an
	// undeclared divergence a failure rather than a discovery.
	TailExpect string `json:"tail_expect,omitempty"`
	Divergence string `json:"divergence,omitempty"`
}

// text returns the bytes this case feeds the decoder.
func (c usageCase) text(t *testing.T) string {
	t.Helper()
	if c.RawLine != "" {
		return c.RawLine
	}
	return string(c.Line)
}

func loadUsageCorpus(t *testing.T) map[string]usageCase {
	t.Helper()
	entries, err := os.ReadDir(usageCorpusDir)
	if err != nil {
		t.Fatalf("read usage corpus: %v", err)
	}
	cases := make(map[string]usageCase, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, readErr := os.ReadFile(filepath.Join(usageCorpusDir, entry.Name()))
		if readErr != nil {
			t.Fatalf("read %s: %v", entry.Name(), readErr)
		}
		var testCase usageCase
		if err := json.Unmarshal(raw, &testCase); err != nil {
			t.Fatalf("decode %s: %v", entry.Name(), err)
		}
		if testCase.Expect != "accept" && testCase.Expect != "reject" {
			t.Fatalf("%s declares expect %q", entry.Name(), testCase.Expect)
		}
		if len(testCase.Line) == 0 && testCase.RawLine == "" {
			t.Fatalf("%s carries neither a line nor a raw_line", entry.Name())
		}
		cases[strings.TrimSuffix(entry.Name(), ".json")] = testCase
	}
	if len(cases) == 0 {
		t.Fatal("the usage corpus is empty; every assertion below would pass vacuously")
	}
	return cases
}

// TestUsageCorpusAgreesWithTheImporter runs every case through this side.
//
// The corpus is the drift alarm for a mirror the design requires: the budget
// tail and this importer both read the usage surface, and design D9 makes
// their rules the same rules. A rule tightened on one side and not the other
// turns a case red here immediately.
func TestUsageCorpusAgreesWithTheImporter(t *testing.T) {
	cases := loadUsageCorpus(t)
	accepted, rejected := 0, 0
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			entry, err := benchmarkimport.DecodeUsageLine(testCase.text(t))
			if err == nil {
				err = entry.Validate()
			}
			switch testCase.Expect {
			case "accept":
				if err != nil {
					t.Fatalf("the importer refused a case the corpus accepts (%s): %v", testCase.Why, err)
				}
			case "reject":
				if err == nil {
					t.Fatalf("the importer accepted a case the corpus rejects (%s)", testCase.Why)
				}
			}
		})
		if testCase.Expect == "accept" {
			accepted++
		} else {
			rejected++
		}
	}
	// Both verdicts must be represented, or the suite could be passing
	// because one whole direction is missing.
	if accepted == 0 || rejected == 0 {
		t.Errorf("the corpus holds %d accepted and %d rejected cases; it needs both", accepted, rejected)
	}
}

// TestStartedAtIsDerivedFromTheLatency covers the one field the importer
// computes rather than reads.
//
// The latency is the whole logical call including retries (design D9), so
// this interval is deliberately wider than any single provider round trip.
// Nanoseconds are what make it exact: milliseconds would round, and
// started_at could not be recovered from what was written.
func TestStartedAtIsDerivedFromTheLatency(t *testing.T) {
	entry, err := benchmarkimport.DecodeUsageLine(`{"finished_at":"2026-08-04T00:05:00.123456789Z",` +
		`"latency_ns":1500000000,"provider":"anthropic","model":"m","input_tokens":1,"output_tokens":1,` +
		`"reasoning_tokens":0,"cache_read_tokens":0,"cache_write_tokens":0,"success":true}`)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	started := entry.StartedAt()
	if got := entry.FinishedAt.Sub(started); got.Nanoseconds() != 1500000000 {
		t.Errorf("the derived interval is %s, want exactly the recorded latency", got)
	}
	if want := "2026-08-04T00:04:58.623456789Z"; started.UTC().Format("2006-01-02T15:04:05.999999999Z") != want {
		t.Errorf("started_at = %s, want %s", started.UTC(), want)
	}
}

// writeUsageLog materialises an attempt's usage log inside a store's evidence
// layout, which is where the importer looks for it (design D8).
func writeUsageLog(t *testing.T, dir, runID string, lines ...string) {
	t.Helper()
	evidence := filepath.Join(dir, "evidence", runID)
	if err := os.MkdirAll(evidence, 0o750); err != nil {
		t.Fatalf("create evidence dir: %v", err)
	}
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(evidence, "usage.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatalf("write usage log: %v", err)
	}
}

// usageHeaderLine is the log's first line at a given surface version.
func usageHeaderLine(version int) string {
	return `{"usage_surface_version":` + strconv.Itoa(version) + `}`
}

// aCall is a valid v2 entry, varied by the caller.
const aCall = `{"finished_at":"2026-08-04T00:05:00Z","latency_ns":1000000,"provider":"anthropic",` +
	`"model":"claude-opus-5","input_tokens":10,"output_tokens":5,"reasoning_tokens":0,` +
	`"cache_read_tokens":0,"cache_write_tokens":0,"cost_usd":0.01,"success":true}`

// TestReadUsageLogReportsWhyThereAreNoCalls covers the absences.
//
// Each is a different fact about the store and only one of them is "this
// attempt made no calls". A reader that collapsed them would record zero
// calls for an attempt whose evidence was pruned, which is a measurement
// nobody made — the same confusion the token axes were fixed for.
func TestReadUsageLogReportsWhyThereAreNoCalls(t *testing.T) {
	const runID = "story-a--config--r1--abcd1234"
	records := []map[string]any{recordWithRunID(t, runID)}

	for _, testCase := range []struct {
		name    string
		write   func(t *testing.T, dir string)
		reason  string
		usable  bool
		lineLen int
	}{
		{
			name:   "no evidence directory at all",
			write:  func(*testing.T, string) {},
			reason: "no evidence directory",
		},
		{
			name: "evidence without a usage log",
			write: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Join(dir, "evidence", runID), 0o750); err != nil {
					t.Fatalf("create evidence dir: %v", err)
				}
			},
			reason: "carries no usage log",
		},
		{
			name: "an empty log states no surface version",
			write: func(t *testing.T, dir string) {
				t.Helper()
				writeUsageLog(t, dir, runID, "")
			},
			reason: "empty",
		},
		{
			name: "a surface-v1 log cannot yield call rows",
			write: func(t *testing.T, dir string) {
				t.Helper()
				writeUsageLog(t, dir, runID, usageHeaderLine(1),
					`{"ts":"2026-08-04T00:05:00Z","model":"m","prompt_tokens":1,"completion_tokens":1,`+
						`"cost_usd":0.01,"success":true}`)
			},
			reason: "surface v1",
		},
		{
			name: "a v2 log with a header and no calls",
			write: func(t *testing.T, dir string) {
				t.Helper()
				writeUsageLog(t, dir, runID, usageHeaderLine(2))
			},
			usable: true,
		},
		{
			name: "a v2 log with calls",
			write: func(t *testing.T, dir string) {
				t.Helper()
				writeUsageLog(t, dir, runID, usageHeaderLine(2), aCall, aCall)
			},
			usable:  true,
			lineLen: 2,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			dir := writeSuite(t, "golden-all-probe", records, completedManifest("golden-all-probe", records))
			testCase.write(t, dir)

			suite, err := benchmarkimport.ReadSuite(dir, "golden-all-probe")
			if err != nil {
				t.Fatalf("read suite: %v", err)
			}
			log, err := suite.ReadUsageLog(runID)
			if err != nil {
				t.Fatalf("read usage log: %v", err)
			}
			if log.Available() != testCase.usable {
				t.Fatalf("available = %t (reason %q), want %t", log.Available(), log.Reason, testCase.usable)
			}
			if !testCase.usable && !strings.Contains(log.Reason, testCase.reason) {
				t.Errorf("reason %q does not say %q; an operator has to be able to tell the absences apart",
					log.Reason, testCase.reason)
			}
			if len(log.Lines) != testCase.lineLen {
				t.Errorf("read %d lines, want %d", len(log.Lines), testCase.lineLen)
			}
		})
	}
}

// TestReadUsageLogRefusesWhatItCannotTrust covers the failures that are NOT
// absences.
//
// An unreadable header is not a legacy log: legacy is a version this build
// knows it cannot use, while an unparseable first line establishes nothing
// about the lines below it. A malformed entry is corruption in a file whose
// header claimed this build could read it.
func TestReadUsageLogRefusesWhatItCannotTrust(t *testing.T) {
	const runID = "story-a--config--r1--abcd1234"
	records := []map[string]any{recordWithRunID(t, runID)}

	for _, testCase := range []struct {
		name  string
		lines []string
	}{
		{"a header that is not JSON", []string{"not a header", aCall}},
		{"a header naming no version", []string{`{}`, aCall}},
		{"a header carrying an unknown field", []string{`{"usage_surface_version":2,"extra":1}`, aCall}},
		{"an entry that does not validate", []string{usageHeaderLine(2),
			`{"finished_at":"2026-08-04T00:05:00Z","latency_ns":1,"provider":"","model":"m","success":false,` +
				`"error":"x"}`}},
		{"an entry carrying an unknown field", []string{usageHeaderLine(2),
			`{"finished_at":"2026-08-04T00:05:00Z","latency_ns":1,"provider":"p","model":"m",` +
				`"success":false,"error":"x","prompt_tokens":3}`}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			dir := writeSuite(t, "golden-all-probe", records, completedManifest("golden-all-probe", records))
			writeUsageLog(t, dir, runID, testCase.lines...)

			suite, err := benchmarkimport.ReadSuite(dir, "golden-all-probe")
			if err != nil {
				t.Fatalf("read suite: %v", err)
			}
			log, err := suite.ReadUsageLog(runID)
			if err == nil {
				t.Fatalf("the reader accepted a log it cannot trust (reason %q, %d lines)",
					log.Reason, len(log.Lines))
			}
		})
	}
}

// TestUsageLogIsNotFollowedThroughASymlink covers the same rule the evidence
// walk enforces: a link can attribute one attempt's calls to another even
// when its target is inside the store, which containment cannot see.
func TestUsageLogIsNotFollowedThroughASymlink(t *testing.T) {
	const runID = "story-a--config--r1--abcd1234"
	const other = "story-a--config--r2--efgh5678"
	records := []map[string]any{recordWithRunID(t, runID)}
	dir := writeSuite(t, "golden-all-probe", records, completedManifest("golden-all-probe", records))

	writeUsageLog(t, dir, other, usageHeaderLine(2), aCall)
	if err := os.MkdirAll(filepath.Join(dir, "evidence", runID), 0o750); err != nil {
		t.Fatalf("create evidence dir: %v", err)
	}
	if err := os.Symlink(filepath.Join(dir, "evidence", other, "usage.jsonl"),
		filepath.Join(dir, "evidence", runID, "usage.jsonl")); err != nil {
		t.Fatalf("link the log: %v", err)
	}

	suite, err := benchmarkimport.ReadSuite(dir, "golden-all-probe")
	if err != nil {
		t.Fatalf("read suite: %v", err)
	}
	if log, err := suite.ReadUsageLog(runID); err == nil {
		t.Fatalf("a linked usage log was read (%d lines); it would attribute another attempt's calls "+
			"to this one", len(log.Lines))
	}
}

// recordWithRunID is the corpus control under a chosen run id.
func recordWithRunID(t *testing.T, runID string) map[string]any {
	t.Helper()
	record := baseRecord(t)
	record["run_id"] = runID
	return record
}
