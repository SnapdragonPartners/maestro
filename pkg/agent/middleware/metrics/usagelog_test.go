package metrics

import (
	"bufio"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fanoutSpy records forwarded observations.
type fanoutSpy struct {
	calls int
	story string
}

func (f *fanoutSpy) ObserveCall(observation *Observation) {
	f.calls++
	f.story = observation.StoryID
}

// validObservation is a complete, coherent successful call. Tests that mean
// to exercise something OTHER than validation start from this, so a failure
// they assert cannot have come from a malformed observation instead of from
// the thing under test.
func validObservation() *Observation {
	cost := 0.01
	return &Observation{
		FinishedAt: time.Now().UTC(),
		Tokens:     &TokenAxes{Input: 100, Output: 40, Reasoning: 10, CacheRead: 5, CacheWrite: 2},
		Cost:       &cost,
		Provider:   "anthropic",
		Model:      "model-x",
		StoryID:    "story-1",
		AgentID:    "coder-001",
		Latency:    1500 * time.Millisecond,
		Success:    true,
	}
}

// failedObservation is the coherent shape of a failure: an error text and NO
// token measurement, because the toolkit reports none for a failed call.
func failedObservation() *Observation {
	return &Observation{
		FinishedAt: time.Now().UTC(),
		Provider:   "anthropic",
		Model:      "model-x",
		StoryID:    "story-1",
		AgentID:    "coder-001",
		Error:      "provider returned 500",
		Latency:    900 * time.Millisecond,
		Success:    false,
	}
}

func readEntries(t *testing.T, path string) (UsageHeader, []UsageEntry) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer file.Close() //nolint:errcheck // test read
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		t.Fatalf("missing header line")
	}
	var header UsageHeader
	if err := json.Unmarshal(scanner.Bytes(), &header); err != nil {
		t.Fatalf("header decode: %v", err)
	}
	var entries []UsageEntry
	for scanner.Scan() {
		var entry UsageEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatalf("entry decode: %v", err)
		}
		entries = append(entries, entry)
	}
	return header, entries
}

func TestUsageLogRecorderFanOutAndFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	spy := &fanoutSpy{}
	recorder, err := NewUsageLogRecorder(path, spy)
	if err != nil {
		t.Fatalf("new usage log: %v", err)
	}
	recorder.ObserveCall(validObservation())
	recorder.ObserveCall(failedObservation())
	if closeErr := recorder.Close(); closeErr != nil {
		t.Fatalf("close: %v", closeErr)
	}
	if spy.calls != 2 || spy.story != "story-1" {
		t.Fatalf("wrapped recorder must receive every observation: %+v", spy)
	}

	header, entries := readEntries(t, path)
	if header.UsageSurfaceVersion != UsageSurfaceVersion {
		t.Fatalf("header must carry the surface version: %d", header.UsageSurfaceVersion)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}

	ok := entries[0]
	switch {
	case ok.Provider != "anthropic":
		t.Fatalf("provider must be recorded, not defaulted: %q", ok.Provider)
	case ok.InputTokens == nil || *ok.InputTokens != 100:
		t.Fatalf("input tokens wrong: %v", ok.InputTokens)
	case ok.OutputTokens == nil || *ok.OutputTokens != 40:
		t.Fatalf("output must be VISIBLE output only, unfolded: %v", ok.OutputTokens)
	case ok.ReasoningTokens == nil || *ok.ReasoningTokens != 10:
		t.Fatalf("reasoning must be its own axis: %v", ok.ReasoningTokens)
	case ok.CacheReadTokens == nil || *ok.CacheReadTokens != 5:
		t.Fatalf("cache read wrong: %v", ok.CacheReadTokens)
	case ok.CacheWriteTokens == nil || *ok.CacheWriteTokens != 2:
		t.Fatalf("cache write must be kept apart from cache read: %v", ok.CacheWriteTokens)
	case ok.LatencyNS != int64(1500*time.Millisecond):
		t.Fatalf("latency must round-trip exactly in nanoseconds: %d", ok.LatencyNS)
	}

	// The failed entry must carry its diagnosis and NO fabricated measurement.
	bad := entries[1]
	if bad.Success || bad.Error == "" {
		t.Fatalf("failed entry must record its error: %+v", bad)
	}
	if bad.InputTokens != nil || bad.OutputTokens != nil || bad.ReasoningTokens != nil ||
		bad.CacheReadTokens != nil || bad.CacheWriteTokens != nil {
		t.Fatalf("a failed call has no token measurement; zeros would be fabricated: %+v", bad)
	}
}

// started_at is derived by subtraction at import, so the pair written here
// must recover it exactly. Milliseconds would have rounded this away.
func TestUsageEntryLatencyIsReversible(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	recorder, err := NewUsageLogRecorder(path, Nop())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	observation := validObservation()
	observation.Latency = 1234567891 * time.Nanosecond // not a whole millisecond
	started := observation.FinishedAt.Add(-observation.Latency)
	recorder.ObserveCall(observation)
	if closeErr := recorder.Close(); closeErr != nil {
		t.Fatalf("close: %v", closeErr)
	}
	_, entries := readEntries(t, path)
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	recovered := entries[0].FinishedAt.Add(-time.Duration(entries[0].LatencyNS))
	if !recovered.Equal(started) {
		t.Fatalf("started_at must be recoverable exactly: want %s, got %s", started, recovered)
	}
}

// An unpriced model records NO cost. Zero would say the call was free, which
// is a different fact and the one paired-local runs would have reported.
func TestUsageEntryUnpricedCostIsAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	recorder, err := NewUsageLogRecorder(path, Nop())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	observation := validObservation()
	observation.Cost = nil
	recorder.ObserveCall(observation)
	if closeErr := recorder.Close(); closeErr != nil {
		t.Fatalf("close: %v", closeErr)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(raw), "cost_usd") {
		t.Fatalf("an unpriced call must omit cost_usd entirely, not write 0: %s", raw)
	}
	_, entries := readEntries(t, path)
	if entries[0].CostUSD != nil {
		t.Fatalf("cost must decode as absent: %v", *entries[0].CostUSD)
	}
}

func TestObservationValidateRejects(t *testing.T) {
	negativeCost := -0.5
	nanCost := math.NaN()
	infCost := math.Inf(1)
	cases := []struct {
		name  string
		build func() *Observation
	}{
		{"blank provider", func() *Observation { o := validObservation(); o.Provider = "  "; return o }},
		{"blank model", func() *Observation { o := validObservation(); o.Model = ""; return o }},
		{"zero finished_at", func() *Observation { o := validObservation(); o.FinishedAt = time.Time{}; return o }},
		{"negative latency", func() *Observation { o := validObservation(); o.Latency = -time.Second; return o }},
		{"negative input", func() *Observation { o := validObservation(); o.Tokens.Input = -1; return o }},
		{"negative output", func() *Observation { o := validObservation(); o.Tokens.Output = -1; return o }},
		{"negative reasoning", func() *Observation { o := validObservation(); o.Tokens.Reasoning = -1; return o }},
		{"negative cache read", func() *Observation { o := validObservation(); o.Tokens.CacheRead = -1; return o }},
		{"negative cache write", func() *Observation { o := validObservation(); o.Tokens.CacheWrite = -1; return o }},
		{"token sum overflows", func() *Observation {
			o := validObservation()
			o.Tokens = &TokenAxes{Input: math.MaxInt64 - 1, Output: 2}
			return o
		}},
		{"negative cost", func() *Observation { o := validObservation(); o.Cost = &negativeCost; return o }},
		{"NaN cost", func() *Observation { o := validObservation(); o.Cost = &nanCost; return o }},
		{"infinite cost", func() *Observation { o := validObservation(); o.Cost = &infCost; return o }},
		{"success carrying an error", func() *Observation { o := validObservation(); o.Error = "boom"; return o }},
		{"success with no measurement", func() *Observation { o := validObservation(); o.Tokens = nil; return o }},
		{"failure with no error text", func() *Observation { o := failedObservation(); o.Error = " "; return o }},
		{"failure carrying tokens", func() *Observation {
			o := failedObservation()
			o.Tokens = &TokenAxes{Input: 10}
			return o
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			observation := testCase.build()
			err := observation.Validate()
			if err == nil {
				t.Fatalf("must be refused: %+v", observation)
			}
			if !errors.Is(err, ErrInvalidObservation) {
				t.Fatalf("must be an ErrInvalidObservation: %v", err)
			}
		})
	}
	// The control: the fixtures the rejections are derived from must pass, or
	// every case above could be passing for the wrong reason.
	for name, observation := range map[string]*Observation{
		"success": validObservation(), "failure": failedObservation(),
	} {
		if err := observation.Validate(); err != nil {
			t.Fatalf("%s fixture must be valid: %v", name, err)
		}
	}
}

// An invalid observation must not reach the WRAPPED recorder either.
//
// The internal aggregator is a consumer like the log is, and its story totals
// are not covered by the sentinel: a negative axis folded into them would
// corrupt figures nothing describes as suspect, while only the durable path
// reported a problem. Validation therefore precedes the fan-out.
func TestInvalidObservationNeverReachesTheInnerRecorder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	spy := &fanoutSpy{}
	recorder, err := NewUsageLogRecorder(path, spy)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	for name, build := range map[string]func() *Observation{
		"negative axis":   func() *Observation { o := validObservation(); o.Tokens.Output = -5; return o },
		"non-finite cost": func() *Observation { o := validObservation(); nan := math.NaN(); o.Cost = &nan; return o },
		"overflowing tuple": func() *Observation {
			o := validObservation()
			o.Tokens = &TokenAxes{Input: math.MaxInt64, Output: 1}
			return o
		},
	} {
		t.Run(name, func(t *testing.T) {
			before := spy.calls
			recorder.ObserveCall(build())
			if spy.calls != before {
				t.Fatalf("an invalid observation reached the wrapped recorder and mutated its aggregates")
			}
		})
	}
	// The control: a valid observation still fans out, so the assertions
	// above are not passing because nothing ever reaches the inner recorder.
	before := spy.calls
	recorder.ObserveCall(validObservation())
	if spy.calls != before+1 {
		t.Fatal("a valid observation must still reach the wrapped recorder")
	}
	if closeErr := recorder.Close(); closeErr != nil {
		t.Fatalf("close: %v", closeErr)
	}
}

// An observation that cannot be recorded takes the same path as a failed
// write: sticky error plus the machine-observable sentinel. Dropping it
// quietly is the one outcome nothing downstream could detect.
func TestInvalidObservationRaisesTheSentinel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	recorder, err := NewUsageLogRecorder(path, Nop())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	invalid := validObservation()
	invalid.Provider = ""
	recorder.ObserveCall(invalid)
	if recorder.Err() == nil {
		t.Fatal("an unrecordable observation must be surfaced, not discarded")
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(path), UsageErrorFileName)); statErr != nil {
		t.Fatalf("expected the %s sentinel: %v", UsageErrorFileName, statErr)
	}
	if closeErr := recorder.Close(); closeErr != nil {
		t.Fatalf("close: %v", closeErr)
	}
	_, entries := readEntries(t, path)
	if len(entries) != 0 {
		t.Fatalf("the invalid line must not be written: %+v", entries)
	}
}

func TestUsageLogRecorderAppendsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	first, err := NewUsageLogRecorder(path, Nop())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	first.ObserveCall(validObservation())
	if closeErr := first.Close(); closeErr != nil {
		t.Fatalf("close: %v", closeErr)
	}
	second, err := NewUsageLogRecorder(path, Nop())
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	second.ObserveCall(validObservation())
	if closeErr := second.Close(); closeErr != nil {
		t.Fatalf("close: %v", closeErr)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Count(string(raw), "\n")
	if lines != 3 { // one header + two entries, no second header
		t.Fatalf("append across reopen must not duplicate the header: %d lines\n%s", lines, raw)
	}
}

// The mixed-version hole: appending v2 lines beneath a v1 header would leave
// every reader trusting the header and mis-parsing the lines. Refuse, and
// leave the file exactly as found — a refusal that truncated or rotated would
// be the loss this check exists to prevent.
func TestUsageLogRecorderRefusesForeignHeader(t *testing.T) {
	for _, testCase := range []struct{ name, first string }{
		{"older version", `{"usage_surface_version":1}`},
		{"newer version", `{"usage_surface_version":99}`},
		{"unreadable header", `not json at all`},
		{"truncated header", `{"usage_surface_version":1`}, // no newline, no closing brace
		// The discriminating case: a header that is syntactically perfect AND
		// carries the CURRENT version, but was never terminated. The version
		// check passes, so only the missing newline can refuse it — and it
		// must, because appending here concatenates the next entry onto the
		// header line and corrupts the log from the second line onward.
		{"valid current header with no newline", `{"usage_surface_version":2}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "usage.jsonl")
			original := testCase.first + "\n{\"model\":\"m\"}\n"
			if !strings.HasSuffix(testCase.first, "}") || testCase.name == "valid current header with no newline" {
				original = testCase.first // deliberately unterminated
			}
			if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
				t.Fatalf("seed: %v", err)
			}
			recorder, err := NewUsageLogRecorder(path, Nop())
			if err == nil {
				_ = recorder.Close() //nolint:errcheck // cleanup on the failure path
				t.Fatal("must refuse to append beneath a header this build did not write")
			}
			if !errors.Is(err, ErrSurfaceVersionMismatch) {
				t.Fatalf("want ErrSurfaceVersionMismatch, got %v", err)
			}
			after, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatalf("read back: %v", readErr)
			}
			if string(after) != original {
				t.Fatalf("the refused file must be left byte-for-byte unchanged:\nwant %q\ngot  %q", original, after)
			}
		})
	}
}

// A log this build DID write is reopened normally; without this the refusal
// above could be passing by refusing everything.
func TestUsageLogRecorderAcceptsItsOwnHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	first, err := NewUsageLogRecorder(path, Nop())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if closeErr := first.Close(); closeErr != nil {
		t.Fatalf("close: %v", closeErr)
	}
	second, err := NewUsageLogRecorder(path, Nop())
	if err != nil {
		t.Fatalf("a log written by this build must reopen: %v", err)
	}
	if closeErr := second.Close(); closeErr != nil {
		t.Fatalf("close: %v", closeErr)
	}
}

// P-1 hardening: creation failure must be an error, not a silent degrade —
// the factory treats it as fatal because -version advertises the surface.
func TestUsageLogRecorderCreationFailure(t *testing.T) {
	dir := t.TempDir() // a directory at the log path makes open fail
	if _, err := NewUsageLogRecorder(dir, Nop()); err == nil {
		t.Fatal("expected error when the usage log path is unwritable")
	}
}

// P-1 hardening: append failures are sticky and surfaced via Err(), while
// the wrapped recorder still receives every observation.
func TestUsageLogRecorderWriteFailureSurfaced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	inner := NewInternalRecorder()
	rec, err := NewUsageLogRecorder(path, inner)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Sentinel write succeeds here (dir intact), so the process must NOT
	// be aborted — assert onFatal is never reached.
	fatalCalled := false
	rec.onFatal = func(error) { fatalCalled = true }
	// Force write failure by closing the underlying file out from under it.
	if closeErr := rec.file.Close(); closeErr != nil {
		t.Fatalf("close: %v", closeErr)
	}
	// A VALID observation, so the error under test can only be the write.
	rec.ObserveCall(validObservation())
	if fatalCalled {
		t.Fatal("onFatal must not fire while the sentinel write succeeds")
	}
	if rec.Err() == nil {
		t.Fatal("expected sticky write error after append to closed file")
	}
	rec.ObserveCall(validObservation())
	if rec.Err() == nil {
		t.Fatal("write error must remain sticky")
	}
	// The failure must be machine-observable: the sentinel file appears
	// next to the log so the benchmark adapter can fail the run.
	raw, readErr := os.ReadFile(filepath.Join(filepath.Dir(path), UsageErrorFileName))
	if readErr != nil {
		t.Fatalf("expected %s sentinel next to the log: %v", UsageErrorFileName, readErr)
	}
	if len(raw) == 0 {
		t.Fatal("sentinel must carry the error text")
	}
}

// P-1 hardening (Codex round 3): when the append fails AND the sentinel
// write also fails (correlated filesystem failure), the failure cannot be
// signaled on disk, so the recorder must escalate to process abort — the
// one channel independent of the failing disk.
func TestUsageLogRecorderCorrelatedFailureAborts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.jsonl")
	rec, err := NewUsageLogRecorder(path, NewInternalRecorder())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var fatalErr error
	rec.onFatal = func(e error) { fatalErr = e }
	// Break both write paths at once: close the log file (append fails)
	// and remove the directory (the sentinel WriteFile fails too).
	if closeErr := rec.file.Close(); closeErr != nil {
		t.Fatalf("close: %v", closeErr)
	}
	if rmErr := os.RemoveAll(dir); rmErr != nil {
		t.Fatalf("rm dir: %v", rmErr)
	}
	rec.ObserveCall(validObservation())
	if fatalErr == nil {
		t.Fatal("correlated append+sentinel failure must escalate to onFatal")
	}
	if rec.Err() == nil {
		t.Fatal("the underlying write error must still be sticky")
	}
}
