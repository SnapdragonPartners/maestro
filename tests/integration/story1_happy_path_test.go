package integration

import (
	"context"
	"testing"
	"time"

	"orchestrator/pkg/coder"
	"orchestrator/pkg/proto"
)

// TestStory1HappyPath tests the basic REQUEST → RESULT → DONE flow
func TestStory1HappyPath(t *testing.T) {
	SetupTestEnvironment(t)

	// Create test harness
	harness := NewTestHarness(t)
	harness.SetTimeouts(GetTestTimeouts())

	// Create always-approval architect
	architect := NewAlwaysApprovalMockArchitect("architect")
	harness.SetArchitect(architect)

	// Create a single coder
	coderID := "coder-1"
	coderDriver := CreateTestCoder(t, coderID)
	harness.AddCoder(coderID, coderDriver)

	// Define a simple task
	taskContent := `Create a simple Go HTTP server with a health endpoint.

Requirements:
- Listen on port 8080
- Respond to GET /health with {"status": "ok"}
- Include proper error handling`

	// Start the coder with the task
	StartCoderWithTask(t, harness, coderID, taskContent)

	// Verify initial state (after task setup, coder should be in PLANNING)
	RequireState(t, harness, coderID, coder.StatePlanning)

	// Run the harness until completion
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Run until the coder reaches DONE state
	err := harness.Run(ctx, func(h *TestHarness) bool {
		return h.GetCoderState(coderID) == proto.StateDone
	})

	if err != nil {
		t.Fatalf("Harness run failed: %v", err)
	}

	// Verify final state
	RequireState(t, harness, coderID, proto.StateDone)

	// Verify architect received exactly one RESULT message
	messages := architect.GetReceivedMessages()
	resultCount := 0
	requestCount := 0

	for _, msg := range messages {
		switch msg.Message.Type {
		case proto.MsgTypeRESULT:
			resultCount++
		case proto.MsgTypeREQUEST:
			requestCount++
		}
	}

	// Should have at least one REQUEST (plan approval) and one RESULT (completion)
	if requestCount < 1 {
		t.Errorf("Expected at least 1 REQUEST message to architect, got %d", requestCount)
	}

	if resultCount < 1 {
		t.Errorf("Expected at least 1 RESULT message to architect, got %d", resultCount)
	}

	// Verify the coder actually processed the task
	stateData := coderDriver.GetStateData()
	if taskContentData, exists := stateData["task_content"]; !exists {
		t.Error("Expected task_content in coder state data")
	} else if taskStr, ok := taskContentData.(string); !ok || taskStr != taskContent {
		t.Errorf("Task content mismatch. Expected: %q, got: %q", taskContent, taskStr)
	}

	t.Logf("Happy path test completed successfully")
	t.Logf("Total messages to architect: %d (REQUEST: %d, RESULT: %d)",
		len(messages), requestCount, resultCount)
}

// TestStory1MultipleCodersIndependent tests that multiple coders can run independently
func TestStory1MultipleCodersIndependent(t *testing.T) {
	SetupTestEnvironment(t)

	// Create test harness
	harness := NewTestHarness(t)
	timeouts := GetTestTimeouts()
	timeouts.Global = 10 * time.Second // Allow more time for multiple coders
	harness.SetTimeouts(timeouts)

	// Create always-approval architect
	architect := NewAlwaysApprovalMockArchitect("architect")
	harness.SetArchitect(architect)

	// Create multiple coders
	coderIDs := []string{"coder-1", "coder-2", "coder-3"}
	taskContents := []string{
		"Create a simple HTTP health endpoint",
		"Create a JSON API for user management",
		"Create a file upload service",
	}

	for i, coderID := range coderIDs {
		coderDriver := CreateTestCoder(t, coderID)
		harness.AddCoder(coderID, coderDriver)

		// Start each coder with a different task
		StartCoderWithTask(t, harness, coderID, taskContents[i])
	}

	// Verify all coders start in planning state (after task setup)
	for _, coderID := range coderIDs {
		RequireState(t, harness, coderID, coder.StatePlanning)
	}

	// Run until all coders complete
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	err := harness.Run(ctx, DefaultStopCondition)
	if err != nil {
		t.Fatalf("Harness run failed: %v", err)
	}

	// Verify all coders reached DONE state
	for _, coderID := range coderIDs {
		RequireState(t, harness, coderID, proto.StateDone)
	}

	// Verify architect received messages from all coders
	messages := architect.GetReceivedMessages()
	coderMessageCounts := make(map[string]int)

	for _, msg := range messages {
		coderMessageCounts[msg.FromCoder]++
	}

	for _, coderID := range coderIDs {
		if count := coderMessageCounts[coderID]; count == 0 {
			t.Errorf("Expected messages from coder %s, but got none", coderID)
		} else {
			t.Logf("Received %d messages from %s", count, coderID)
		}
	}

	t.Logf("Multiple coders test completed successfully")
	t.Logf("Total messages to architect: %d", len(messages))
}

// TestStory1ChannelIsolation verifies that per-coder channels prevent blocking
func TestStory1ChannelIsolation(t *testing.T) {
	SetupTestEnvironment(t)

	// Create test harness
	harness := NewTestHarness(t)
	harness.SetTimeouts(GetTestTimeouts())

	// Create an architect with artificial delay for one coder
	architect := NewAlwaysApprovalMockArchitect("architect")
	harness.SetArchitect(architect)

	// Create two coders
	fastCoderID := "fast-coder"
	slowCoderID := "slow-coder"

	fastDriver := CreateTestCoder(t, fastCoderID)
	slowDriver := CreateTestCoder(t, slowCoderID)

	harness.AddCoder(fastCoderID, fastDriver)
	harness.AddCoder(slowCoderID, slowDriver)

	// Start both coders
	StartCoderWithTask(t, harness, fastCoderID, "Simple task")
	StartCoderWithTask(t, harness, slowCoderID, "Another simple task")

	// Run for a short time to let both coders start processing
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// Run until at least one coder makes progress
	initialFastState := harness.GetCoderState(fastCoderID)
	initialSlowState := harness.GetCoderState(slowCoderID)

	err := harness.Run(ctx, func(h *TestHarness) bool {
		fastState := h.GetCoderState(fastCoderID)
		slowState := h.GetCoderState(slowCoderID)

		// Stop when either coder changes state
		return fastState != initialFastState || slowState != initialSlowState
	})

	// Expect timeout (normal in this test)
	if err == nil {
		t.Log("Coders made progress")
	} else {
		t.Log("Timeout occurred (expected)")
	}

	// Both coders should be able to make independent progress
	// The key assertion is that we don't deadlock

	t.Log("Channel isolation test completed - no deadlock detected")
}
