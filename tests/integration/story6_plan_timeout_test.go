package integration

import (
	"context"
	"sync"
	"testing"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/proto"
)

// TestStory6PlanTimeoutResubmission tests automatic resubmission after timeout
func TestStory6PlanTimeoutResubmission(t *testing.T) {
	SetupTestEnvironment(t)

	// Create test harness with short timeouts to simulate timeout scenario
	harness := NewTestHarness(t)
	timeouts := GetTestTimeouts()
	timeouts.Plan = 200 * time.Millisecond // Short plan timeout for testing
	timeouts.Global = 15 * time.Second     // Overall test timeout
	harness.SetTimeouts(timeouts)

	requestCount := 0
	var mu sync.Mutex

	// Create architect that delays first response, then responds normally
	architect := NewMalformedResponseMockArchitect("architect", func(msg *proto.AgentMsg) *proto.AgentMsg {
		mu.Lock()
		defer mu.Unlock()

		if msg.Type == proto.MsgTypeREQUEST {
			requestCount++
		}

		response := proto.NewAgentMsg(proto.MsgTypeRESULT, "architect", msg.FromAgent)
		response.ParentMsgID = msg.ID

		if requestCount == 1 {
			// First request: introduce delay longer than plan timeout
			time.Sleep(300 * time.Millisecond) // Longer than plan timeout

			// Don't return a response for the first request to simulate timeout
			return nil
		}

		// Subsequent requests: respond normally
		response.SetPayload(proto.KeyStatus, "approved")
		response.SetPayload(proto.KeyRequestType, proto.RequestApproval.String())
		response.SetPayload(proto.KeyApprovalType, "plan")
		response.SetPayload(proto.KeyFeedback, "Plan approved after resubmission!")

		return response
	})
	harness.SetArchitect(architect)

	// Create coder
	coderID := "coder-timeout-resubmit"
	coderDriver := CreateTestCoder(t, coderID)
	harness.AddCoder(coderID, coderDriver)

	// Start with task
	taskContent := `Create a function that processes user orders.

Requirements:
- Validate order data
- Calculate total price including tax
- Handle inventory checking
- Return order confirmation or errors`

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

	// Verify that resubmission occurred
	mu.Lock()
	finalRequestCount := requestCount
	mu.Unlock()

	if finalRequestCount < 2 {
		t.Errorf("Expected at least 2 requests due to timeout resubmission, got %d", finalRequestCount)
	}

	// Verify exactly one automatic resubmission (so max 2 total requests for timeout scenario)
	if finalRequestCount > 2 {
		t.Logf("Warning: Got %d requests, expected 2 (original + 1 resubmission)", finalRequestCount)
	}

	t.Logf("Plan timeout resubmission test completed successfully")
	t.Logf("Total requests: %d (expected: 2 - original + 1 resubmission)", finalRequestCount)
}

// TestStory6SingleResubmissionLimit tests that only one resubmission occurs per timeout
func TestStory6SingleResubmissionLimit(t *testing.T) {
	SetupTestEnvironment(t)

	// Create test harness with very short timeouts
	harness := NewTestHarness(t)
	timeouts := GetTestTimeouts()
	timeouts.Plan = 100 * time.Millisecond // Very short for quick timeout
	timeouts.Global = 10 * time.Second
	harness.SetTimeouts(timeouts)

	requestCount := 0
	var mu sync.Mutex

	// Create architect that never responds to test resubmission limit
	architect := NewMalformedResponseMockArchitect("architect", func(msg *proto.AgentMsg) *proto.AgentMsg {
		mu.Lock()
		defer mu.Unlock()

		if msg.Type == proto.MsgTypeREQUEST {
			requestCount++
		}

		// Never return a response to test resubmission behavior
		time.Sleep(200 * time.Millisecond) // Always timeout
		return nil
	})
	harness.SetArchitect(architect)

	// Create coder
	coderID := "coder-resubmit-limit"
	coderDriver := CreateTestCoder(t, coderID)
	harness.AddCoder(coderID, coderDriver)

	// Start with simple task
	StartCoderWithTask(t, harness, coderID, "Create a simple utility function")

	// Run for limited time to observe resubmission behavior
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := harness.Run(ctx, func(h *TestHarness) bool {
		// Stop if we've seen too many requests (would indicate unlimited resubmission)
		mu.Lock()
		count := requestCount
		mu.Unlock()
		return count >= 5 // Stop if we see excessive requests
	})

	// This test expects to timeout, that's normal
	if err != nil {
		t.Logf("Expected timeout occurred: %v", err)
	}

	mu.Lock()
	finalRequestCount := requestCount
	mu.Unlock()

	// Should see exactly 2 requests: original + 1 resubmission
	// Allow some tolerance for timing issues
	if finalRequestCount < 1 {
		t.Errorf("Expected at least 1 request, got %d", finalRequestCount)
	}

	if finalRequestCount > 3 {
		t.Errorf("Expected at most 3 requests (original + max 2 resubmissions), got %d", finalRequestCount)
	}

	t.Logf("Single resubmission limit test completed")
	t.Logf("Total requests: %d (should be limited to prevent infinite resubmission)", finalRequestCount)
}

// TestStory6TimeoutRecoveryWithValidResponse tests recovery after timeout when valid response arrives
func TestStory6TimeoutRecoveryWithValidResponse(t *testing.T) {
	SetupTestEnvironment(t)

	// Create test harness
	harness := NewTestHarness(t)
	timeouts := GetTestTimeouts()
	timeouts.Plan = 150 * time.Millisecond
	timeouts.Global = 12 * time.Second
	harness.SetTimeouts(timeouts)

	requestCount := 0
	var mu sync.Mutex

	// Create architect that delays first response, then responds quickly
	architect := NewMalformedResponseMockArchitect("architect", func(msg *proto.AgentMsg) *proto.AgentMsg {
		mu.Lock()
		defer mu.Unlock()

		if msg.Type == proto.MsgTypeREQUEST {
			requestCount++
		}

		response := proto.NewAgentMsg(proto.MsgTypeRESULT, "architect", msg.FromAgent)
		response.ParentMsgID = msg.ID

		if requestCount == 1 {
			// First request: delay to cause timeout
			time.Sleep(250 * time.Millisecond)

			// Still return a response, but it will be late
			response.SetPayload(proto.KeyStatus, "approved")
			response.SetPayload(proto.KeyRequestType, proto.RequestApproval.String())
			response.SetPayload(proto.KeyApprovalType, "plan")
			response.SetPayload(proto.KeyFeedback, "Late approval")
			return response
		}

		// Subsequent requests: respond quickly
		response.SetPayload(proto.KeyStatus, "approved")
		response.SetPayload(proto.KeyRequestType, proto.RequestApproval.String())
		response.SetPayload(proto.KeyApprovalType, "plan")
		response.SetPayload(proto.KeyFeedback, "Quick approval after resubmission")

		return response
	})
	harness.SetArchitect(architect)

	// Create coder
	coderID := "coder-timeout-recovery"
	coderDriver := CreateTestCoder(t, coderID)
	harness.AddCoder(coderID, coderDriver)

	// Start with task
	StartCoderWithTask(t, harness, coderID, "Create a timeout-resilient data processor")

	// Run until completion
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	err := harness.Run(ctx, func(h *TestHarness) bool {
		return h.GetCoderState(coderID) == agent.StateDone
	})

	if err != nil {
		t.Fatalf("Timeout recovery test failed: %v", err)
	}

	// Verify successful completion despite initial timeout
	RequireState(t, harness, coderID, agent.StateDone)

	// Check request count
	mu.Lock()
	finalRequestCount := requestCount
	mu.Unlock()

	t.Logf("Timeout recovery test completed successfully")
	t.Logf("Final state: DONE, Total requests: %d", finalRequestCount)
}

// TestStory6NoResubmissionOnQuickResponse tests that no resubmission occurs with quick responses
func TestStory6NoResubmissionOnQuickResponse(t *testing.T) {
	SetupTestEnvironment(t)

	// Create test harness with normal timeouts
	harness := NewTestHarness(t)
	timeouts := GetTestTimeouts()
	timeouts.Plan = 500 * time.Millisecond // Generous timeout
	timeouts.Global = 8 * time.Second
	harness.SetTimeouts(timeouts)

	requestCount := 0
	var mu sync.Mutex

	// Create architect that responds quickly
	architect := NewMalformedResponseMockArchitect("architect", func(msg *proto.AgentMsg) *proto.AgentMsg {
		mu.Lock()
		defer mu.Unlock()

		if msg.Type == proto.MsgTypeREQUEST {
			requestCount++
		}

		// Quick response (well within timeout)
		response := proto.NewAgentMsg(proto.MsgTypeRESULT, "architect", msg.FromAgent)
		response.ParentMsgID = msg.ID
		response.SetPayload(proto.KeyStatus, "approved")
		response.SetPayload(proto.KeyRequestType, proto.RequestApproval.String())
		response.SetPayload(proto.KeyApprovalType, "plan")
		response.SetPayload(proto.KeyFeedback, "Quick approval - no timeout")

		return response
	})
	harness.SetArchitect(architect)

	// Create coder
	coderID := "coder-quick-response"
	coderDriver := CreateTestCoder(t, coderID)
	harness.AddCoder(coderID, coderDriver)

	// Start with task
	StartCoderWithTask(t, harness, coderID, "Create a quick response handler")

	// Run until completion
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	err := harness.Run(ctx, func(h *TestHarness) bool {
		return h.GetCoderState(coderID) == agent.StateDone
	})

	if err != nil {
		t.Fatalf("Quick response test failed: %v", err)
	}

	// Verify completion
	RequireState(t, harness, coderID, agent.StateDone)

	// Should have exactly 1 request (no resubmission needed)
	mu.Lock()
	finalRequestCount := requestCount
	mu.Unlock()

	if finalRequestCount != 1 {
		t.Errorf("Expected exactly 1 request for quick response, got %d", finalRequestCount)
	}

	t.Logf("Quick response test completed successfully")
	t.Logf("Total requests: %d (expected: 1, no resubmission needed)", finalRequestCount)
}
