package integration

import (
	"context"
	"testing"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/proto"
)

// TestStory3InvalidCodeBlockRecovery tests recovery from invalid code blocks
func TestStory3InvalidCodeBlockRecovery(t *testing.T) {
	SetupTestEnvironment(t)
	
	// Create test harness
	harness := NewTestHarness(t)
	timeouts := GetTestTimeouts()
	timeouts.Global = 15 * time.Second // Allow time for error handling and recovery
	harness.SetTimeouts(timeouts)
	
	responseCount := 0
	
	// Create custom malformed response architect
	architect := NewMalformedResponseMockArchitect("architect", func(msg *proto.AgentMsg) *proto.AgentMsg {
		responseCount++
		response := proto.NewAgentMsg(proto.MsgTypeRESULT, "architect", msg.FromAgent)
		response.ParentMsgID = msg.ID
		
		if msg.Type == proto.MsgTypeREQUEST {
			if responseCount == 1 {
				// First response: invalid code block (no backticks)
				response.SetPayload(proto.KeyStatus, "changes_requested")
				response.SetPayload(proto.KeyRequestType, proto.RequestApproval.String())
				response.SetPayload(proto.KeyApprovalType, "plan")
				response.SetPayload(proto.KeyFeedback, `Please implement this function:

func AddNumbers(a, b int) int {
    return a + b
}

Make sure to include proper error handling.`)
			} else {
				// Subsequent responses: valid approval
				response.SetPayload(proto.KeyStatus, "approved")
				response.SetPayload(proto.KeyRequestType, proto.RequestApproval.String())
				response.SetPayload(proto.KeyApprovalType, "plan")
				response.SetPayload(proto.KeyFeedback, "Plan looks good!")
			}
		}
		
		return response
	})
	harness.SetArchitect(architect)
	
	// Create coder
	coderID := "coder-invalid-recovery"
	coderDriver := CreateTestCoder(t, coderID)
	harness.AddCoder(coderID, coderDriver)
	
	// Start with task
	taskContent := `Create a function that adds two numbers safely.

Requirements:
- Function should handle integer overflow
- Include appropriate error messages
- Return meaningful errors for invalid inputs`
	
	StartCoderWithTask(t, harness, coderID, taskContent)
	
	// Run until completion or timeout
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
	
	// Verify that the coder recovered from the invalid code block
	messages := architect.GetReceivedMessages()
	
	// Should have multiple requests due to error recovery
	requestCount := 0
	for _, msg := range messages {
		if msg.Message.Type == proto.MsgTypeREQUEST {
			requestCount++
		}
	}
	
	if requestCount < 2 {
		t.Errorf("Expected at least 2 REQUEST messages (initial + recovery), got %d", requestCount)
	}
	
	t.Logf("Invalid code block recovery test completed successfully")
	t.Logf("Total REQUEST messages: %d", requestCount)
}

// TestStory3MalformedResponseHandling tests various malformed responses
func TestStory3MalformedResponseHandling(t *testing.T) {
	SetupTestEnvironment(t)
	
	testCases := []struct {
		name         string
		responseFunc func(*proto.AgentMsg) *proto.AgentMsg
		expectError  bool
	}{
		{
			name: "missing_status",
			responseFunc: func(msg *proto.AgentMsg) *proto.AgentMsg {
				response := proto.NewAgentMsg(proto.MsgTypeRESULT, "architect", msg.FromAgent)
				response.ParentMsgID = msg.ID
				// Missing status field
				response.SetPayload(proto.KeyRequestType, proto.RequestApproval.String())
				response.SetPayload(proto.KeyApprovalType, "plan")
				return response
			},
			expectError: true,
		},
		{
			name: "invalid_approval_type",
			responseFunc: func(msg *proto.AgentMsg) *proto.AgentMsg {
				response := proto.NewAgentMsg(proto.MsgTypeRESULT, "architect", msg.FromAgent)
				response.ParentMsgID = msg.ID
				response.SetPayload(proto.KeyStatus, "approved")
				response.SetPayload(proto.KeyRequestType, proto.RequestApproval.String())
				response.SetPayload(proto.KeyApprovalType, "invalid_type")
				return response
			},
			expectError: false, // Should handle gracefully
		},
		{
			name: "empty_feedback",
			responseFunc: func(msg *proto.AgentMsg) *proto.AgentMsg {
				response := proto.NewAgentMsg(proto.MsgTypeRESULT, "architect", msg.FromAgent)
				response.ParentMsgID = msg.ID
				response.SetPayload(proto.KeyStatus, "changes_requested")
				response.SetPayload(proto.KeyRequestType, proto.RequestApproval.String())
				response.SetPayload(proto.KeyApprovalType, "plan")
				response.SetPayload(proto.KeyFeedback, "")
				return response
			},
			expectError: false, // Should handle empty feedback
		},
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create test harness
			harness := NewTestHarness(t)
			timeouts := GetTestTimeouts()
			timeouts.Global = 5 * time.Second
			harness.SetTimeouts(timeouts)
			
			// Create malformed response architect
			architect := NewMalformedResponseMockArchitect("architect", tc.responseFunc)
			harness.SetArchitect(architect)
			
			// Create coder
			coderID := "coder-malformed-" + tc.name
			coderDriver := CreateTestCoder(t, coderID)
			harness.AddCoder(coderID, coderDriver)
			
			// Start with simple task
			StartCoderWithTask(t, harness, coderID, "Create a simple hello world function")
			
			// Run for a limited time
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			
			err := harness.Run(ctx, func(h *TestHarness) bool {
				state := h.GetCoderState(coderID)
				// For malformed responses, we don't expect to reach DONE
				// Just run until timeout or until coder reaches a stable error state
				return state == agent.StateDone || state == agent.StateError
			})
			
			// Check if we got the expected outcome
			finalState := harness.GetCoderState(coderID)
			
			if tc.expectError {
				if finalState != agent.StateError && err == nil {
					t.Errorf("Expected error state or timeout for %s, but got state %s", tc.name, finalState)
				}
			} else {
				// Should handle gracefully and not crash
				if finalState == agent.StateError {
					t.Errorf("Expected graceful handling for %s, but got error state", tc.name)
				}
			}
			
			t.Logf("Malformed response test %s completed with state %s", tc.name, finalState)
		})
	}
}

// TestStory3ErrorRecoveryWithValidFollowup tests recovery when valid response follows invalid
func TestStory3ErrorRecoveryWithValidFollowup(t *testing.T) {
	SetupTestEnvironment(t)
	
	// Create test harness
	harness := NewTestHarness(t)
	timeouts := GetTestTimeouts()
	timeouts.Global = 15 * time.Second
	harness.SetTimeouts(timeouts)
	
	responseCount := 0
	
	// Create architect that sends invalid response first, then valid
	architect := NewMalformedResponseMockArchitect("architect", func(msg *proto.AgentMsg) *proto.AgentMsg {
		responseCount++
		response := proto.NewAgentMsg(proto.MsgTypeRESULT, "architect", msg.FromAgent)
		response.ParentMsgID = msg.ID
		
		if responseCount == 1 {
			// First response: malformed (missing required fields)
			response.SetPayload("invalid_field", "malformed_value")
			return response
		}
		
		// Subsequent responses: valid approval
		response.SetPayload(proto.KeyStatus, "approved")
		response.SetPayload(proto.KeyRequestType, proto.RequestApproval.String())
		response.SetPayload(proto.KeyApprovalType, "plan")
		response.SetPayload(proto.KeyFeedback, "Plan approved after recovery!")
		
		return response
	})
	harness.SetArchitect(architect)
	
	// Create coder
	coderID := "coder-error-recovery"
	coderDriver := CreateTestCoder(t, coderID)
	harness.AddCoder(coderID, coderDriver)
	
	// Start with task
	StartCoderWithTask(t, harness, coderID, "Create a function that validates email addresses")
	
	// Run until completion
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	
	err := harness.Run(ctx, func(h *TestHarness) bool {
		state := h.GetCoderState(coderID)
		return state == agent.StateDone || state == agent.StateError
	})
	
	if err != nil {
		t.Logf("Harness run ended with: %v", err)
	}
	
	// The coder should eventually succeed despite the initial malformed response
	finalState := harness.GetCoderState(coderID)
	
	// Verify that we sent multiple messages due to error recovery
	messages := architect.GetReceivedMessages()
	if len(messages) < 2 {
		t.Errorf("Expected at least 2 messages due to error recovery, got %d", len(messages))
	}
	
	t.Logf("Error recovery test completed with final state: %s", finalState)
	t.Logf("Total messages exchanged: %d", len(messages))
}