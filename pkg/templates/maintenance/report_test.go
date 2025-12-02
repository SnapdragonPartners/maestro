package maintenance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCycleReport_ToMarkdown(t *testing.T) {
	report := &CycleReport{
		CycleID:     "maintenance-2024-01-15-120000",
		StartedAt:   time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC),
		CompletedAt: time.Date(2024, 1, 15, 12, 30, 0, 0, time.UTC),
		Duration:    30 * time.Minute,
		BranchesDeleted: []string{
			"feature/old-branch",
			"fix/merged-pr",
		},
		CleanupErrors: []string{},
		Stories: []*StoryResult{
			{
				StoryID:     "story-1",
				Title:       "Update knowledge.md",
				Status:      "completed",
				PRNumber:    123,
				PRMerged:    true,
				CompletedAt: time.Date(2024, 1, 15, 12, 15, 0, 0, time.UTC),
				Summary:     "Added notes about new API patterns",
			},
			{
				StoryID:     "story-2",
				Title:       "Run test suite",
				Status:      "completed",
				PRNumber:    0,
				PRMerged:    false,
				CompletedAt: time.Date(2024, 1, 15, 12, 25, 0, 0, time.UTC),
			},
		},
		Metrics: CycleMetrics{
			StoriesTotal:     2,
			StoriesCompleted: 2,
			StoriesFailed:    0,
			PRsMerged:        1,
			BranchesDeleted:  2,
		},
	}

	markdown, err := report.ToMarkdown()
	if err != nil {
		t.Fatalf("ToMarkdown failed: %v", err)
	}

	// Verify key content is present
	checks := []string{
		"maintenance-2024-01-15-120000",
		"30 minutes",
		"feature/old-branch",
		"fix/merged-pr",
		"Update knowledge.md",
		"Run test suite",
		"#123",
		"Added notes about new API patterns",
		"| Stories Total | 2 |",
		"| PRs Merged | 1 |",
	}

	for _, check := range checks {
		if !strings.Contains(markdown, check) {
			t.Errorf("Expected markdown to contain %q", check)
		}
	}
}

func TestCycleReport_ToMarkdown_NoStories(t *testing.T) {
	report := &CycleReport{
		CycleID:         "maintenance-2024-01-15-120000",
		StartedAt:       time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC),
		CompletedAt:     time.Date(2024, 1, 15, 12, 5, 0, 0, time.UTC),
		Duration:        5 * time.Minute,
		BranchesDeleted: nil,
		Stories:         nil,
		Metrics:         CycleMetrics{},
	}

	markdown, err := report.ToMarkdown()
	if err != nil {
		t.Fatalf("ToMarkdown failed: %v", err)
	}

	if !strings.Contains(markdown, "No stale branches found") {
		t.Error("Expected 'No stale branches found' message")
	}

	if !strings.Contains(markdown, "No maintenance stories were executed") {
		t.Error("Expected 'No maintenance stories were executed' message")
	}
}

func TestCycleReport_SaveToFile(t *testing.T) {
	report := &CycleReport{
		CycleID:     "maintenance-test-save",
		StartedAt:   time.Now().Add(-10 * time.Minute),
		CompletedAt: time.Now(),
		Duration:    10 * time.Minute,
		Metrics:     CycleMetrics{StoriesTotal: 1},
	}

	// Create temp directory
	tmpDir := t.TempDir()

	savedPath, err := report.SaveToFile(tmpDir)
	if err != nil {
		t.Fatalf("SaveToFile failed: %v", err)
	}

	// Verify file was created
	if _, statErr := os.Stat(savedPath); os.IsNotExist(statErr) {
		t.Errorf("Report file was not created at %s", savedPath)
	}

	// Verify filename format
	expectedFilename := "maintenance-report-maintenance-test-save.md"
	if filepath.Base(savedPath) != expectedFilename {
		t.Errorf("Expected filename %q, got %q", expectedFilename, filepath.Base(savedPath))
	}

	// Verify content
	content, err := os.ReadFile(savedPath)
	if err != nil {
		t.Fatalf("Failed to read saved file: %v", err)
	}

	if !strings.Contains(string(content), "maintenance-test-save") {
		t.Error("Saved file doesn't contain cycle ID")
	}
}

func TestNewCycleReport(t *testing.T) {
	startedAt := time.Now().Add(-15 * time.Minute)
	branches := []string{"branch1", "branch2"}
	errors := []string{"error1"}
	stories := []*StoryResult{{StoryID: "s1", Title: "Test", Status: "completed"}}
	metrics := CycleMetrics{StoriesTotal: 1, StoriesCompleted: 1}

	report := NewCycleReport("cycle-123", startedAt, branches, errors, stories, metrics)

	if report.CycleID != "cycle-123" {
		t.Errorf("Expected CycleID 'cycle-123', got %q", report.CycleID)
	}
	if report.StartedAt != startedAt {
		t.Error("StartedAt mismatch")
	}
	if len(report.BranchesDeleted) != 2 {
		t.Errorf("Expected 2 branches, got %d", len(report.BranchesDeleted))
	}
	if len(report.CleanupErrors) != 1 {
		t.Errorf("Expected 1 error, got %d", len(report.CleanupErrors))
	}
	if len(report.Stories) != 1 {
		t.Errorf("Expected 1 story, got %d", len(report.Stories))
	}
	if report.Metrics.StoriesTotal != 1 {
		t.Errorf("Expected StoriesTotal 1, got %d", report.Metrics.StoriesTotal)
	}
	if report.Duration <= 0 {
		t.Error("Duration should be positive")
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		duration time.Duration
		expected string
	}{
		{30 * time.Second, "30 seconds"},
		{90 * time.Second, "1 minutes"},
		{30 * time.Minute, "30 minutes"},
		{90 * time.Minute, "1 hours 30 minutes"},
		{2 * time.Hour, "2 hours 0 minutes"},
	}

	formatDuration, ok := templateFuncs["formatDuration"].(func(time.Duration) string)
	if !ok {
		t.Fatal("formatDuration not found in templateFuncs")
	}

	for _, tt := range tests {
		result := formatDuration(tt.duration)
		if result != tt.expected {
			t.Errorf("formatDuration(%v) = %q, want %q", tt.duration, result, tt.expected)
		}
	}
}

func TestStatusEmoji(t *testing.T) {
	statusEmoji, ok := templateFuncs["statusEmoji"].(func(string) string)
	if !ok {
		t.Fatal("statusEmoji not found in templateFuncs")
	}

	tests := []struct {
		status   string
		expected string
	}{
		{"completed", "✅"},
		{"failed", "❌"},
		{"pending", "⏳"},
		{"unknown", "⏳"},
	}

	for _, tt := range tests {
		result := statusEmoji(tt.status)
		if result != tt.expected {
			t.Errorf("statusEmoji(%q) = %q, want %q", tt.status, result, tt.expected)
		}
	}
}
