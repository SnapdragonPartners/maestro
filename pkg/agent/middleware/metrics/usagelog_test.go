package metrics

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// fanoutSpy records forwarded observations.
type fanoutSpy struct {
	calls int
	story string
}

func (f *fanoutSpy) ObserveRequest(storyID, _, _ string, _, _ int, _ float64, _ bool) {
	f.calls++
	f.story = storyID
}

func TestUsageLogRecorderFanOutAndFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	spy := &fanoutSpy{}
	recorder, err := NewUsageLogRecorder(path, spy)
	if err != nil {
		t.Fatalf("new usage log: %v", err)
	}
	recorder.ObserveRequest("story-1", "coder-001", "model-x", 100, 50, 0.01, true)
	// Failed calls are recorded too: their tokens were spent.
	recorder.ObserveRequest("story-1", "coder-001", "model-x", 10, 0, 0, false)
	if closeErr := recorder.Close(); closeErr != nil {
		t.Fatalf("close: %v", closeErr)
	}
	if spy.calls != 2 || spy.story != "story-1" {
		t.Fatalf("wrapped recorder must receive every observation: %+v", spy)
	}

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
	if err := json.Unmarshal(scanner.Bytes(), &header); err != nil || header.UsageSurfaceVersion != UsageSurfaceVersion {
		t.Fatalf("header must carry the surface version: %s (%v)", scanner.Text(), err)
	}
	var entries []UsageEntry
	for scanner.Scan() {
		var entry UsageEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatalf("entry decode: %v", err)
		}
		entries = append(entries, entry)
	}
	if len(entries) != 2 || entries[0].Model != "model-x" || entries[0].PromptTokens != 100 || entries[1].Success {
		t.Fatalf("entries wrong: %+v", entries)
	}
}

func TestUsageLogRecorderAppendsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	first, err := NewUsageLogRecorder(path, Nop())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	first.ObserveRequest("s", "a", "m", 1, 1, 0.001, true)
	if closeErr := first.Close(); closeErr != nil {
		t.Fatalf("close: %v", closeErr)
	}
	second, err := NewUsageLogRecorder(path, Nop())
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	second.ObserveRequest("s", "a", "m", 2, 2, 0.002, true)
	if closeErr := second.Close(); closeErr != nil {
		t.Fatalf("close: %v", closeErr)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := 0
	for _, b := range raw {
		if b == '\n' {
			lines++
		}
	}
	if lines != 3 { // one header + two entries, no second header
		t.Fatalf("append across reopen must not duplicate the header: %d lines\n%s", lines, raw)
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
	rec.ObserveRequest("s", "a", "m", 1, 1, 0.001, true)
	if fatalCalled {
		t.Fatal("onFatal must not fire while the sentinel write succeeds")
	}
	if rec.Err() == nil {
		t.Fatal("expected sticky write error after append to closed file")
	}
	rec.ObserveRequest("s", "a", "m", 2, 2, 0.002, true)
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
	rec.ObserveRequest("s", "a", "m", 1, 1, 0.001, true)
	if fatalErr == nil {
		t.Fatal("correlated append+sentinel failure must escalate to onFatal")
	}
	if rec.Err() == nil {
		t.Fatal("the underlying write error must still be sticky")
	}
}
