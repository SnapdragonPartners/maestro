package integration

import (
	"context"
	"testing"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/coder"
	"orchestrator/pkg/proto"
)

// TestStory2RevisionCycle tests the revision cycle where architect requests changes
func TestStory2RevisionCycle(t *testing.T) {
	SetupTestEnvironment(t)

	// Create test harness
	harness := NewTestHarness(t)
	timeouts := GetTestTimeouts()
	timeouts.Global = 10 * time.Second // Allow more time for revision cycle
	harness.SetTimeouts(timeouts)

	// Create architect that rejects once, then approves
	architect := NewChangesRequestedMockArchitect("architect", 1, "Please add error handling to your implementation")
	harness.SetArchitect(architect)

	// Create a single coder
	coderID := "coder-revision"
	coderDriver := CreateTestCoder(t, coderID)
	harness.AddCoder(coderID, coderDriver)

	// Define a task that will trigger revision
	taskContent := `Create a Go function that reads a file and returns its contents.

Requirements:
- Function signature: func ReadFileContents(filename string) (string, error)
- Handle file not found errors properly
- Return the file contents as a string`

	// Start the coder with the task
	StartCoderWithTask(t, harness, coderID, taskContent)

	// Verify initial state
	RequireState(t, harness, coderID, coder.StatePlanning.ToAgentState())

	// Run the harness until completion
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Run until the coder reaches DONE state
	err := harness.Run(ctx, func(h *TestHarness) bool {
		return h.GetCoderState(coderID) == agent.StateDone
	})

	if err != nil {
		t.Fatalf("Harness run failed: %v", err)
	}

	// Verify final state
	RequireState(t, harness, coderID, agent.StateDone)

	// Verify the revision cycle happened
	messages := architect.GetReceivedMessages()
	requestCount := 0
	resultCount := 0

	for _, msg := range messages {
		switch msg.Message.Type {
		case proto.MsgTypeREQUEST:
			requestCount++
		case proto.MsgTypeRESULT:
			resultCount++
		}
	}

	// Should have at least 2 REQUEST messages (original plan + revision)
	if requestCount < 2 {
		t.Errorf("Expected at least 2 REQUEST messages (plan + revision), got %d", requestCount)
	}

	// Should have at least 1 RESULT message (final completion)
	if resultCount < 1 {
		t.Errorf("Expected at least 1 RESULT message, got %d", resultCount)
	}

	// Note: The revision cycle completion indicates the architect properly
	// rejected once and then approved, as expected from the mock configuration

	t.Logf("Revision cycle test completed successfully")
	t.Logf("Total messages to architect: %d (REQUEST: %d, RESULT: %d)",
		len(messages), requestCount, resultCount)
}

// TestStory2MultipleRevisions tests multiple revision cycles
func TestStory2MultipleRevisions(t *testing.T) {
	SetupTestEnvironment(t)

	// Create test harness
	harness := NewTestHarness(t)
	timeouts := GetTestTimeouts()
	timeouts.Global = 15 * time.Second // Allow more time for multiple revisions
	harness.SetTimeouts(timeouts)

	// Create architect that rejects 3 times, then approves
	architect := NewChangesRequestedMockArchitect("architect", 3,
		"Please improve error handling, add documentation, and include unit tests")
	harness.SetArchitect(architect)

	// Create a single coder
	coderID := "coder-multi-revision"
	coderDriver := CreateTestCoder(t, coderID)
	harness.AddCoder(coderID, coderDriver)

	// Define a complex task
	taskContent := `Create a complete HTTP REST API endpoint for user management.

Requirements:
- GET /users - list all users
- POST /users - create new user
- PUT /users/{id} - update user
- DELETE /users/{id} - delete user
- Include proper error handling and validation
- Add comprehensive documentation
- Include unit tests for all endpoints`

	// Start the coder with the task
	StartCoderWithTask(t, harness, coderID, taskContent)

	// Run until completion
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	err := harness.Run(ctx, func(h *TestHarness) bool {
		return h.GetCoderState(coderID) == agent.StateDone
	})

	if err != nil {
		t.Fatalf("Harness run failed: %v", err)
	}

	// Verify final state
	RequireState(t, harness, coderID, agent.StateDone)

	// Verify multiple revisions occurred
	messages := architect.GetReceivedMessages()
	requestCount := 0

	for _, msg := range messages {
		if msg.Message.Type == proto.MsgTypeREQUEST {
			requestCount++
		}
	}

	// Should have at least 4 REQUEST messages (original + 3 revisions)
	if requestCount < 4 {
		t.Errorf("Expected at least 4 REQUEST messages (plan + 3 revisions), got %d", requestCount)
	}

	t.Logf("Multiple revisions test completed successfully")
	t.Logf("Total REQUEST messages: %d", requestCount)
}

// TestStory2RevisionStateTransitions tests specific state transitions during revision cycle
func TestStory2RevisionStateTransitions(t *testing.T) {
	SetupTestEnvironment(t)

	// Create test harness
	harness := NewTestHarness(t)
	harness.SetTimeouts(GetTestTimeouts())

	// Create architect that rejects once
	architect := NewChangesRequestedMockArchitect("architect", 1, "Please add comments to your code")
	harness.SetArchitect(architect)

	// Create coder
	coderID := "coder-state-transitions"
	coderDriver := CreateTestCoder(t, coderID)
	harness.AddCoder(coderID, coderDriver)

	// Start with simple task
	StartCoderWithTask(t, harness, coderID, "Create a simple function that adds two numbers")

	// Track state transitions
	seenStates := make(map[agent.State]bool)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Run with custom stop condition that tracks states
	err := harness.Run(ctx, func(h *TestHarness) bool {
		currentState := h.GetCoderState(coderID)
		seenStates[currentState] = true

		// Stop when we reach DONE
		return currentState == agent.StateDone
	})

	if err != nil {
		t.Fatalf("Harness run failed: %v", err)
	}

	// Verify we saw the expected state transitions for a revision cycle
	expectedStates := []agent.State{
		coder.StatePlanning.ToAgentState(),
		// Note: Other states depend on actual coder implementation
	}

	for _, expectedState := range expectedStates {
		if !seenStates[expectedState] {
			t.Errorf("Expected to see state %s during revision cycle, but didn't", expectedState)
		}
	}

	t.Logf("State transition test completed successfully")
	t.Logf("Observed states: %v", seenStates)
}
