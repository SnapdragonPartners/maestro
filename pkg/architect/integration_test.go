package architect

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"orchestrator/pkg/proto"
	"orchestrator/pkg/state"
)

func TestFullWorkflowIntegration(t *testing.T) {
	tmpDir := "/tmp/architect_integration_test"
	defer os.RemoveAll(tmpDir)

	// Create workspace directories
	workDir := tmpDir + "/work"
	storiesDir := tmpDir + "/stories"
	err := os.MkdirAll(storiesDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create directories: %v", err)
	}

	// Use actual test specification file
	specFile := "../../tests/fixtures/test_spec.md"

	// Create state store
	stateStore, err := state.NewStore(tmpDir + "/state")
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	// Create architect driver
	driver := NewDriver("test-architect", stateStore, workDir, storiesDir)

	ctx := context.Background()
	err = driver.Initialize(ctx)
	if err != nil {
		t.Fatalf("Failed to initialize driver: %v", err)
	}

	// Run the full workflow
	err = driver.ProcessWorkflow(ctx, specFile)
	if err != nil {
		t.Fatalf("Workflow failed: %v", err)
	}

	// Verify final state
	if driver.GetCurrentState() != StateCompleted {
		t.Errorf("Expected final state COMPLETED, got %s", driver.GetCurrentState())
	}

	// Verify stories were generated
	queue := driver.GetQueue()
	allStories := queue.GetAllStories()
	if len(allStories) == 0 {
		t.Error("Expected stories to be generated")
	}

	// Verify state persistence
	stateData := driver.GetStateData()
	if stateData["current_state"] != string(StateCompleted) {
		t.Error("State not persisted correctly")
	}

	t.Logf("✅ Full workflow integration test passed with %d stories generated", len(allStories))
}

func TestBusinessQuestionEscalationIntegration(t *testing.T) {
	tmpDir := "/tmp/architect_escalation_integration_test"
	defer os.RemoveAll(tmpDir)

	// Create workspace directories
	workDir := tmpDir + "/work"
	storiesDir := tmpDir + "/stories"
	err := os.MkdirAll(storiesDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create directories: %v", err)
	}

	// Create state store
	stateStore, err := state.NewStore(tmpDir + "/state")
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	// Create architect driver
	driver := NewDriver("test-architect", stateStore, workDir, storiesDir)

	ctx := context.Background()
	err = driver.Initialize(ctx)
	if err != nil {
		t.Fatalf("Failed to initialize driver: %v", err)
	}

	// Set up a test story
	driver.queue.stories["001"] = &QueuedStory{
		ID:              "001",
		Title:           "Test Story",
		Status:          StatusInProgress,
		EstimatedPoints: 2,
		FilePath:        storiesDir + "/001.md",
	}

	// Test business question handling
	businessQuestionMsg := proto.NewAgentMsg(
		proto.MsgTypeQUESTION,
		"test-agent",
		"architect",
	)
	businessQuestionMsg.Payload["story_id"] = "001"
	businessQuestionMsg.Payload["question"] = "What are the compliance requirements for this feature?"

	err = driver.questionHandler.HandleQuestion(ctx, businessQuestionMsg)
	if err != nil {
		t.Fatalf("Failed to handle business question: %v", err)
	}

	// Verify escalation was created
	escalationHandler := driver.GetEscalationHandler()
	summary := escalationHandler.GetEscalationSummary()
	if summary.TotalEscalations == 0 {
		t.Error("Expected escalation to be created for business question")
	}

	if summary.PendingEscalations == 0 {
		t.Error("Expected pending escalation for business question")
	}

	// Verify story status changed to await human feedback
	story, exists := driver.queue.GetStory("001")
	if !exists {
		t.Fatal("Test story not found")
	}

	if story.Status != StatusAwaitHumanFeedback {
		t.Errorf("Expected story status %s, got %s", StatusAwaitHumanFeedback, story.Status)
	}

	// Test escalation file logging
	logFile := filepath.Join(workDir, "logs", "escalations.jsonl")
	if _, err := os.Stat(logFile); os.IsNotExist(err) {
		t.Error("Escalation log file was not created")
	}

	t.Log("✅ Business question escalation integration test passed")
}

func TestReviewFailureEscalationIntegration(t *testing.T) {
	tmpDir := "/tmp/architect_review_escalation_test"
	defer os.RemoveAll(tmpDir)

	// Create workspace directories
	workDir := tmpDir + "/work"
	storiesDir := tmpDir + "/stories"
	err := os.MkdirAll(storiesDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create directories: %v", err)
	}

	// Create state store
	stateStore, err := state.NewStore(tmpDir + "/state")
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	// Create architect driver
	driver := NewDriver("test-architect", stateStore, workDir, storiesDir)

	ctx := context.Background()
	err = driver.Initialize(ctx)
	if err != nil {
		t.Fatalf("Failed to initialize driver: %v", err)
	}

	// Set up a test story
	driver.queue.stories["002"] = &QueuedStory{
		ID:              "002",
		Title:           "Review Test Story",
		Status:          StatusWaitingReview,
		EstimatedPoints: 3,
		FilePath:        storiesDir + "/002.md",
	}

	// Simulate 3 failed review attempts by directly calling the escalation handler
	err = driver.escalationHandler.EscalateReviewFailure(ctx, "002", "test-agent", 3, "Code does not meet quality standards")
	if err != nil {
		t.Fatalf("Failed to escalate review failure: %v", err)
	}

	// After 3 failures, should have escalation
	escalationHandler := driver.GetEscalationHandler()
	summary := escalationHandler.GetEscalationSummary()

	if summary.TotalEscalations == 0 {
		t.Error("Expected escalation to be created after 3 review failures")
	}

	// Check escalation type
	escalations := escalationHandler.GetEscalations("")
	found := false
	for _, escalation := range escalations {
		if escalation.Type == "review_failure" && escalation.Priority == "high" {
			found = true
			break
		}
	}

	if !found {
		t.Error("Expected high-priority review_failure escalation")
	}

	// Verify story status changed to await human feedback
	story, exists := driver.queue.GetStory("002")
	if !exists {
		t.Fatal("Test story not found")
	}

	if story.Status != StatusAwaitHumanFeedback {
		t.Errorf("Expected story status %s, got %s", StatusAwaitHumanFeedback, story.Status)
	}

	t.Log("✅ Review failure escalation integration test passed")
}

func TestWorkflowStatePersistence(t *testing.T) {
	tmpDir := "/tmp/architect_persistence_test"
	defer os.RemoveAll(tmpDir)

	// Create workspace directories
	workDir := tmpDir + "/work"
	storiesDir := tmpDir + "/stories"
	err := os.MkdirAll(storiesDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create directories: %v", err)
	}

	// Use actual test specification file
	specFile := "../../tests/fixtures/test_spec.md"

	// Create state store
	stateStore, err := state.NewStore(tmpDir + "/state")
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	// First workflow run
	driver1 := NewDriver("persistence-test", stateStore, workDir, storiesDir)

	ctx := context.Background()
	err = driver1.Initialize(ctx)
	if err != nil {
		t.Fatalf("Failed to initialize first driver: %v", err)
	}

	err = driver1.ProcessWorkflow(ctx, specFile)
	if err != nil {
		t.Fatalf("First workflow failed: %v", err)
	}

	finalState1 := driver1.GetCurrentState()
	stateData1 := driver1.GetStateData()

	// Second workflow run (should resume from saved state)
	driver2 := NewDriver("persistence-test", stateStore, workDir, storiesDir)

	err = driver2.Initialize(ctx)
	if err != nil {
		t.Fatalf("Failed to initialize second driver: %v", err)
	}

	initialState2 := driver2.GetCurrentState()
	stateData2 := driver2.GetStateData()

	// Verify state was restored correctly
	if initialState2 != finalState1 {
		t.Errorf("State not restored correctly: expected %s, got %s", finalState1, initialState2)
	}

	if stateData2["current_state"] != stateData1["current_state"] {
		t.Error("State data not restored correctly")
	}

	// Run workflow again (should be no-op since already completed)
	err = driver2.ProcessWorkflow(ctx, specFile)
	if err != nil {
		t.Fatalf("Second workflow failed: %v", err)
	}

	if driver2.GetCurrentState() != StateCompleted {
		t.Error("Resumed workflow did not maintain completed state")
	}

	t.Log("✅ Workflow state persistence integration test passed")
}
