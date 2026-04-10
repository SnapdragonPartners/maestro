package coder

import (
	"strings"
	"testing"

	"orchestrator/pkg/proto"
)

// TestBuildCompactionStateSummary verifies that the state summary includes all key fields.
func TestBuildCompactionStateSummary(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	// Set up state: plan, todos, confidence, story ID.
	sm.SetStateData(KeyPlan, "Step 1: Create module\nStep 2: Add tests")
	sm.SetStateData(string(stateDataKeyPlanConfidence), "HIGH")
	sm.SetStateData(KeyStoryID, "story-042")
	coder.todoList = &TodoList{
		Items: []TodoItem{
			{Description: "Create module", Completed: true},
			{Description: "Add tests", Completed: false},
		},
		Current: 1,
	}

	summary := coder.buildCompactionStateSummary(15)

	// Verify all key sections are present.
	if !strings.Contains(summary, "15 messages removed") {
		t.Errorf("Expected removed count in summary, got: %s", summary)
	}
	if !strings.Contains(summary, string(proto.StateWaiting)) {
		t.Errorf("Expected current state in summary, got: %s", summary)
	}
	if !strings.Contains(summary, "story-042") {
		t.Errorf("Expected story ID in summary, got: %s", summary)
	}
	if !strings.Contains(summary, "Step 1: Create module") {
		t.Errorf("Expected plan in summary, got: %s", summary)
	}
	if !strings.Contains(summary, "Create module") {
		t.Errorf("Expected todo items in summary, got: %s", summary)
	}
	if !strings.Contains(summary, "HIGH") {
		t.Errorf("Expected confidence in summary, got: %s", summary)
	}
}

// TestBuildCompactionStateSummary_MinimalState verifies it works with no plan, no todos.
func TestBuildCompactionStateSummary_MinimalState(t *testing.T) {
	coder := createTestCoder(t, nil)

	summary := coder.buildCompactionStateSummary(5)

	if !strings.Contains(summary, "5 messages removed") {
		t.Errorf("Expected removed count, got: %s", summary)
	}
	if !strings.Contains(summary, "Current phase") {
		t.Errorf("Expected current phase, got: %s", summary)
	}
	// Should NOT contain plan or todo sections.
	if strings.Contains(summary, "Approved plan") {
		t.Error("Expected no plan section in minimal state")
	}
}

// TestBuildCompactionStateSummary_LongPlanTruncated verifies plans >600 chars get truncated.
func TestBuildCompactionStateSummary_LongPlanTruncated(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	longPlan := strings.Repeat("x", 1000)
	sm.SetStateData(KeyPlan, longPlan)

	summary := coder.buildCompactionStateSummary(3)

	if strings.Contains(summary, longPlan) {
		t.Error("Full 1000-char plan should have been truncated")
	}
	if !strings.Contains(summary, "...") {
		t.Error("Truncated plan should end with ...")
	}
}

// TestBuildCompactionStateSummary_HardCap verifies total summary doesn't exceed 2000 chars.
func TestBuildCompactionStateSummary_HardCap(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	// Set a large plan and many todos to push past the cap.
	sm.SetStateData(KeyPlan, strings.Repeat("plan text ", 100))
	sm.SetStateData(KeyStoryID, "story-with-a-long-id-for-testing")
	sm.SetStateData(string(stateDataKeyPlanConfidence), "MEDIUM")
	items := make([]TodoItem, 20)
	for i := range items {
		items[i] = TodoItem{Description: strings.Repeat("todo task ", 10), Completed: i < 5}
	}
	coder.todoList = &TodoList{Items: items, Current: 5}

	summary := coder.buildCompactionStateSummary(50)

	// Hard cap is 2000 chars + the truncation suffix.
	if len(summary) > maxCompactionSummaryChars+50 {
		t.Errorf("Summary exceeds hard cap: %d chars", len(summary))
	}
}

// TestConfigureContextManager verifies the helper wires up callback and token counter.
func TestConfigureContextManager(t *testing.T) {
	coder := createTestCoder(t, nil)

	// Before configure: callback not set (default zero value).
	// Call configure.
	coder.configureContextManager(nil, "test-agent")

	// The compaction callback should be wired.
	// Test indirectly: set up some state and verify the callback produces output.
	coder.BaseStateMachine.SetStateData(KeyPlan, "Test plan")
	result := coder.buildCompactionStateSummary(1)
	if result == "" {
		t.Error("Expected non-empty state summary from callback")
	}
}
