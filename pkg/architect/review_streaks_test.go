package architect

import (
	"testing"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/tools"
)

func TestReviewStreakIncrement(t *testing.T) {
	d := &Driver{
		reviewStreaks: make(map[string]map[string]int),
	}

	// First increment should return 1
	count := d.incrementReviewStreak("coder-001", ReviewTypeBudget)
	if count != 1 {
		t.Errorf("expected streak 1, got %d", count)
	}

	// Second increment should return 2
	count = d.incrementReviewStreak("coder-001", ReviewTypeBudget)
	if count != 2 {
		t.Errorf("expected streak 2, got %d", count)
	}

	// Third increment should return 3
	count = d.incrementReviewStreak("coder-001", ReviewTypeBudget)
	if count != 3 {
		t.Errorf("expected streak 3, got %d", count)
	}
}

func TestReviewStreakReset(t *testing.T) {
	d := &Driver{
		reviewStreaks: make(map[string]map[string]int),
	}

	// Build up a streak
	d.incrementReviewStreak("coder-001", ReviewTypeBudget)
	d.incrementReviewStreak("coder-001", ReviewTypeBudget)
	d.incrementReviewStreak("coder-001", ReviewTypeBudget)

	// Reset
	d.resetReviewStreak("coder-001", ReviewTypeBudget)

	// Should be 0
	count := d.getReviewStreak("coder-001", ReviewTypeBudget)
	if count != 0 {
		t.Errorf("expected streak 0 after reset, got %d", count)
	}
}

func TestReviewStreakResetOnUnknownCoder(t *testing.T) {
	d := &Driver{
		reviewStreaks: make(map[string]map[string]int),
	}

	// Resetting an unknown coder should not panic
	d.resetReviewStreak("unknown-coder", ReviewTypeBudget)

	count := d.getReviewStreak("unknown-coder", ReviewTypeBudget)
	if count != 0 {
		t.Errorf("expected streak 0 for unknown coder, got %d", count)
	}
}

func TestReviewStreakGetUnknownCoder(t *testing.T) {
	d := &Driver{
		reviewStreaks: make(map[string]map[string]int),
	}

	// Getting streak for unknown coder should return 0
	count := d.getReviewStreak("unknown-coder", ReviewTypeBudget)
	if count != 0 {
		t.Errorf("expected streak 0 for unknown coder, got %d", count)
	}
}

func TestReviewStreakClearAll(t *testing.T) {
	d := &Driver{
		reviewStreaks: make(map[string]map[string]int),
	}

	// Build streaks across multiple review types
	d.incrementReviewStreak("coder-001", ReviewTypeBudget)
	d.incrementReviewStreak("coder-001", ReviewTypeBudget)
	d.incrementReviewStreak("coder-001", ReviewTypeCode)

	// Clear all streaks for coder-001
	d.clearReviewStreaks("coder-001")

	// All should be 0
	if d.getReviewStreak("coder-001", ReviewTypeBudget) != 0 {
		t.Error("expected budget streak 0 after clear")
	}
	if d.getReviewStreak("coder-001", ReviewTypeCode) != 0 {
		t.Error("expected code streak 0 after clear")
	}

	// The coder should be completely removed from the map
	if _, exists := d.reviewStreaks["coder-001"]; exists {
		t.Error("expected coder-001 to be removed from reviewStreaks map")
	}
}

func TestReviewStreakMultipleCoders(t *testing.T) {
	d := &Driver{
		reviewStreaks: make(map[string]map[string]int),
	}

	// Build streaks for different coders
	d.incrementReviewStreak("coder-001", ReviewTypeBudget)
	d.incrementReviewStreak("coder-001", ReviewTypeBudget)
	d.incrementReviewStreak("coder-002", ReviewTypeBudget)

	// Verify independent tracking
	if d.getReviewStreak("coder-001", ReviewTypeBudget) != 2 {
		t.Errorf("expected coder-001 streak 2, got %d", d.getReviewStreak("coder-001", ReviewTypeBudget))
	}
	if d.getReviewStreak("coder-002", ReviewTypeBudget) != 1 {
		t.Errorf("expected coder-002 streak 1, got %d", d.getReviewStreak("coder-002", ReviewTypeBudget))
	}

	// Clear coder-001 should not affect coder-002
	d.clearReviewStreaks("coder-001")
	if d.getReviewStreak("coder-002", ReviewTypeBudget) != 1 {
		t.Errorf("coder-002 streak should be unaffected after clearing coder-001, got %d", d.getReviewStreak("coder-002", ReviewTypeBudget))
	}
}

func TestReviewStreakSoftLimitThreshold(t *testing.T) {
	d := &Driver{
		reviewStreaks: make(map[string]map[string]int),
	}

	// Build up to soft limit
	for i := 0; i < BudgetReviewSoftLimit; i++ {
		d.incrementReviewStreak("coder-001", ReviewTypeBudget)
	}

	streak := d.getReviewStreak("coder-001", ReviewTypeBudget)
	if streak != BudgetReviewSoftLimit {
		t.Errorf("expected streak %d at soft limit, got %d", BudgetReviewSoftLimit, streak)
	}
	if streak < BudgetReviewSoftLimit {
		t.Error("streak should be >= soft limit")
	}
}

func TestReviewStreakHardLimitThreshold(t *testing.T) {
	d := &Driver{
		reviewStreaks: make(map[string]map[string]int),
	}

	// Build up to hard limit
	for i := 0; i < BudgetReviewHardLimit; i++ {
		d.incrementReviewStreak("coder-001", ReviewTypeBudget)
	}

	streak := d.getReviewStreak("coder-001", ReviewTypeBudget)
	if streak != BudgetReviewHardLimit {
		t.Errorf("expected streak %d at hard limit, got %d", BudgetReviewHardLimit, streak)
	}
	if streak < BudgetReviewHardLimit {
		t.Error("streak should be >= hard limit")
	}
}

func TestExtractStoryEdit_WithNotes(t *testing.T) {
	calls := []agent.ToolCall{
		{
			Name: tools.ToolStoryEdit,
			Parameters: map[string]any{
				"implementation_notes": "Use isolated template sets",
			},
		},
	}
	result, err := ExtractStoryEdit(calls, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Notes != "Use isolated template sets" {
		t.Errorf("expected notes 'Use isolated template sets', got %q", result.Notes)
	}
}

func TestExtractStoryEdit_EmptyNotes(t *testing.T) {
	calls := []agent.ToolCall{
		{
			Name: tools.ToolStoryEdit,
			Parameters: map[string]any{
				"implementation_notes": "",
			},
		},
	}
	result, err := ExtractStoryEdit(calls, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Notes != "" {
		t.Errorf("expected empty notes, got %q", result.Notes)
	}
}

func TestExtractStoryEdit_NoToolCall(t *testing.T) {
	calls := []agent.ToolCall{
		{
			Name:       tools.ToolReviewComplete,
			Parameters: map[string]any{"status": "APPROVED"},
		},
	}
	_, err := ExtractStoryEdit(calls, nil)
	if err == nil {
		t.Fatal("expected error for missing story_edit tool call")
	}
}

func TestOrdinalSuffix(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{1, "st"},
		{2, "nd"},
		{3, "rd"},
		{4, "th"},
		{5, "th"},
		{11, "th"},
		{12, "th"},
		{13, "th"},
		{21, "st"},
		{22, "nd"},
		{23, "rd"},
		{100, "th"},
		{101, "st"},
		{111, "th"},
	}

	for _, tc := range tests {
		got := ordinalSuffix(tc.n)
		if got != tc.want {
			t.Errorf("ordinalSuffix(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}
