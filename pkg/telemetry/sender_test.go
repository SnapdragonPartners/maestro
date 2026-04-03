package telemetry

import (
	"encoding/json"
	"strings"
	"testing"

	"orchestrator/pkg/persistence"
)

func TestBuildReportBasic(t *testing.T) {
	summary := &persistence.SessionSummary{
		StoriesTotal:     3,
		StoriesCompleted: 2,
		StoriesFailed:    1,
	}
	records := []*persistence.FailureRecord{
		{ID: "f1", Kind: "test_failure", Explanation: "test failed"},
		{ID: "f2", Kind: "build_failure", Explanation: "build failed"},
	}

	report := BuildReport("install-1", "session-1", summary, records)

	if report.InstallationID != "install-1" {
		t.Errorf("expected install-1, got %s", report.InstallationID)
	}
	if report.SessionID != "session-1" {
		t.Errorf("expected session-1, got %s", report.SessionID)
	}
	if len(report.Failures) != 2 {
		t.Errorf("expected 2 failures, got %d", len(report.Failures))
	}
	if report.Truncated {
		t.Error("expected not truncated")
	}
}

func TestBuildReportCapsEntries(t *testing.T) {
	summary := &persistence.SessionSummary{}
	records := make([]*persistence.FailureRecord, 150)
	for i := range records {
		records[i] = &persistence.FailureRecord{
			ID:   "f" + strings.Repeat("0", 3),
			Kind: "test_failure",
		}
	}

	report := BuildReport("install-1", "session-1", summary, records)

	if len(report.Failures) != maxFailureEntries {
		t.Errorf("expected %d failures, got %d", maxFailureEntries, len(report.Failures))
	}
	if !report.Truncated {
		t.Error("expected truncated=true")
	}
	if report.OverflowCounts["test_failure"] != 50 {
		t.Errorf("expected 50 overflow for test_failure, got %d", report.OverflowCounts["test_failure"])
	}
}

func TestBuildReportSizeCap(t *testing.T) {
	summary := &persistence.SessionSummary{}
	// Create records with large explanations to exceed size cap
	records := make([]*persistence.FailureRecord, 50)
	for i := range records {
		records[i] = &persistence.FailureRecord{
			ID:          "f1",
			Kind:        "test_failure",
			Explanation: strings.Repeat("x", 5000),
		}
	}

	report := BuildReport("install-1", "session-1", summary, records)

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(data) > maxReportBytes {
		t.Errorf("report size %d exceeds cap %d", len(data), maxReportBytes)
	}
}

func TestBuildReportCanTrimToZero(t *testing.T) {
	summary := &persistence.SessionSummary{}
	// Create 100 records each with max-length explanations + large evidence.
	// After sanitization, explanations are capped at 2000 chars each,
	// so 100 entries × ~2KB = ~200KB of explanations alone.
	// Add large evidence to push over the 256KB limit.
	largeEvidence := `[{"kind":"output","summary":"` + strings.Repeat("a", 4000) + `","snippet":"` + strings.Repeat("b", 4000) + `"}]`
	records := make([]*persistence.FailureRecord, maxFailureEntries)
	for i := range records {
		records[i] = &persistence.FailureRecord{
			ID:          "f1",
			Kind:        "huge_failure",
			Explanation: strings.Repeat("x", 5000),
			Evidence:    largeEvidence,
		}
	}

	report := BuildReport("install-1", "session-1", summary, records)

	// Should have trimmed some failures to fit under size cap
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(data) > maxReportBytes {
		t.Errorf("report size %d exceeds cap %d", len(data), maxReportBytes)
	}
	if !report.Truncated {
		t.Error("expected truncated=true when entries are trimmed for size")
	}
	if len(report.Failures) >= maxFailureEntries {
		t.Errorf("expected fewer than %d failures after size trimming, got %d", maxFailureEntries, len(report.Failures))
	}
}

func TestSanitizeEvidenceCapsEntries(t *testing.T) {
	// Build JSON with more than maxEvidenceEntries
	entries := make([]map[string]string, 20)
	for i := range entries {
		entries[i] = map[string]string{
			"kind":    "test_output",
			"summary": "test",
			"snippet": "code",
		}
	}
	data, _ := json.Marshal(entries)

	result := sanitizeEvidence(string(data))

	if len(result) != maxEvidenceEntries {
		t.Errorf("expected %d evidence entries, got %d", maxEvidenceEntries, len(result))
	}
}

func TestSanitizeEvidenceInvalidJSON(t *testing.T) {
	result := sanitizeEvidence("not json")
	if result != nil {
		t.Errorf("expected nil for invalid JSON, got %v", result)
	}
}
