package integration

import (
	"context"
	"testing"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/coder"
	"orchestrator/pkg/proto"
)

// TestStory5UnknownApprovalTypeFallback tests handling of unknown approval_type values
func TestStory5UnknownApprovalTypeFallback(t *testing.T) {
	SetupTestEnvironment(t)

	// Create test harness
	harness := NewTestHarness(t)
	timeouts := GetTestTimeouts()
	timeouts.Global = 15 * time.Second // Allow time for fallback handling
	harness.SetTimeouts(timeouts)

	responseCount := 0

	// Create architect that sends unknown approval_type first, then valid response
	architect := NewMalformedResponseMockArchitect("architect", func(msg *proto.AgentMsg) *proto.AgentMsg {
		responseCount++
		response := proto.NewAgentMsg(proto.MsgTypeRESULT, "architect", msg.FromAgent)
		response.ParentMsgID = msg.ID

		if responseCount == 1 {
			// First response: unknown approval_type
			response.SetPayload(proto.KeyStatus, "approved")
			response.SetPayload(proto.KeyRequestType, proto.RequestApproval.String())
			response.SetPayload(proto.KeyApprovalType, "YEP") // Invalid approval type
			response.SetPayload(proto.KeyFeedback, "This should be handled gracefully")
			return response
		}

		// Subsequent responses: valid approval
		response.SetPayload(proto.KeyStatus, "approved")
		response.SetPayload(proto.KeyRequestType, proto.RequestApproval.String())
		response.SetPayload(proto.KeyApprovalType, "plan") // Valid approval type
		response.SetPayload(proto.KeyFeedback, "Plan approved!")

		return response
	})
	harness.SetArchitect(architect)

	// Create coder
	coderID := "coder-unknown-approval"
	coderDriver := CreateTestCoder(t, coderID)
	harness.AddCoder(coderID, coderDriver)

	// Start with task
	taskContent := `Create a function that validates user input.

Requirements:
- Check for null/empty inputs
- Validate email format if applicable
- Return appropriate error messages`

	StartCoderWithTask(t, harness, coderID, taskContent)

	// Verify initial state
	RequireState(t, harness, coderID, coder.StatePlanning)

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

	// Verify that multiple requests were made due to unknown approval_type handling
	messages := architect.GetReceivedMessages()
	requestCount := 0

	for _, msg := range messages {
		if msg.Message.Type == proto.MsgTypeREQUEST {
			requestCount++
		}
	}

	// Should have at least 2 requests due to the unknown approval_type fallback
	if requestCount < 2 {
		t.Errorf("Expected at least 2 REQUEST messages due to unknown approval_type fallback, got %d", requestCount)
	}

	t.Logf("Unknown approval_type fallback test completed successfully")
	t.Logf("Total REQUEST messages: %d (expected >= 2 due to fallback)", requestCount)
}

// TestStory5MultipleUnknownApprovalTypes tests various unknown approval_type values
func TestStory5MultipleUnknownApprovalTypes(t *testing.T) {
	unknownTypes := []string{
		"YEP",
		"NOPE",
		"MAYBE",
		"SURE_THING",
		"123",
		"",
		"plan_but_not_really",
	}

	for _, unknownType := range unknownTypes {
		t.Run("unknown_type_"+unknownType, func(t *testing.T) {
			SetupTestEnvironment(t)

			// Create test harness
			harness := NewTestHarness(t)
			timeouts := GetTestTimeouts()
			timeouts.Global = 10 * time.Second
			harness.SetTimeouts(timeouts)

			responseCount := 0

			// Create architect that sends the specific unknown type
			architect := NewMalformedResponseMockArchitect("architect", func(msg *proto.AgentMsg) *proto.AgentMsg {
				responseCount++
				response := proto.NewAgentMsg(proto.MsgTypeRESULT, "architect", msg.FromAgent)
				response.ParentMsgID = msg.ID

				if responseCount == 1 {
					// First response: unknown approval_type
					response.SetPayload(proto.KeyStatus, "approved")
					response.SetPayload(proto.KeyRequestType, proto.RequestApproval.String())
					response.SetPayload(proto.KeyApprovalType, unknownType)
					response.SetPayload(proto.KeyFeedback, "Testing unknown type")
					return response
				}

				// Valid response
				response.SetPayload(proto.KeyStatus, "approved")
				response.SetPayload(proto.KeyRequestType, proto.RequestApproval.String())
				response.SetPayload(proto.KeyApprovalType, "plan")
				response.SetPayload(proto.KeyFeedback, "Plan approved!")

				return response
			})
			harness.SetArchitect(architect)

			// Create coder
			coderID := "coder-unknown-" + unknownType
			coderDriver := CreateTestCoder(t, coderID)
			harness.AddCoder(coderID, coderDriver)

			// Start with simple task
			StartCoderWithTask(t, harness, coderID, "Create a simple logging function")

			// Run until completion or timeout
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()

			_ = harness.Run(ctx, func(h *TestHarness) bool {
				state := h.GetCoderState(coderID)
				return state == agent.StateDone || state == agent.StateError
			})

			// The coder should handle unknown types gracefully
			finalState := harness.GetCoderState(coderID)

			// Should not crash or enter error state due to unknown approval type
			if finalState == agent.StateError {
				t.Errorf("Coder entered error state for unknown approval_type '%s'", unknownType)
			}

			// Verify messages were exchanged
			messages := architect.GetReceivedMessages()
			if len(messages) == 0 {
				t.Errorf("No messages received for unknown approval_type '%s'", unknownType)
			}

			t.Logf("Unknown approval_type '%s' handled with final state: %s", unknownType, finalState)
		})
	}
}

// TestStory5ApprovalTypeValidation tests the approval type validation logic
func TestStory5ApprovalTypeValidation(t *testing.T) {
	SetupTestEnvironment(t)

	// Test cases for approval type validation
	testCases := []struct {
		name         string
		approvalType string
		status       string
		expectResend bool
	}{
		{
			name:         "valid_plan_approved",
			approvalType: "plan",
			status:       "approved",
			expectResend: false,
		},
		{
			name:         "valid_code_approved",
			approvalType: "code",
			status:       "approved",
			expectResend: false,
		},
		{
			name:         "unknown_type_with_approval",
			approvalType: "unknown_type",
			status:       "approved",
			expectResend: true, // Should resend due to unknown type
		},
		{
			name:         "empty_type_with_approval",
			approvalType: "",
			status:       "approved",
			expectResend: true, // Should resend due to empty type
		},
		{
			name:         "numeric_type_with_approval",
			approvalType: "123",
			status:       "approved",
			expectResend: true, // Should resend due to invalid type
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create test harness
			harness := NewTestHarness(t)
			timeouts := GetTestTimeouts()
			timeouts.Global = 8 * time.Second
			harness.SetTimeouts(timeouts)

			requestCount := 0

			// Create architect that tracks requests and responds appropriately
			architect := NewMalformedResponseMockArchitect("architect", func(msg *proto.AgentMsg) *proto.AgentMsg {
				if msg.Type == proto.MsgTypeREQUEST {
					requestCount++
				}

				response := proto.NewAgentMsg(proto.MsgTypeRESULT, "architect", msg.FromAgent)
				response.ParentMsgID = msg.ID

				if requestCount == 1 {
					// First response uses the test case values
					response.SetPayload(proto.KeyStatus, tc.status)
					response.SetPayload(proto.KeyRequestType, proto.RequestApproval.String())
					response.SetPayload(proto.KeyApprovalType, tc.approvalType)
					response.SetPayload(proto.KeyFeedback, "Test response")
				} else {
					// Subsequent responses are valid
					response.SetPayload(proto.KeyStatus, "approved")
					response.SetPayload(proto.KeyRequestType, proto.RequestApproval.String())
					response.SetPayload(proto.KeyApprovalType, "plan")
					response.SetPayload(proto.KeyFeedback, "Valid response")
				}

				return response
			})
			harness.SetArchitect(architect)

			// Create coder
			coderID := "coder-validation-" + tc.name
			coderDriver := CreateTestCoder(t, coderID)
			harness.AddCoder(coderID, coderDriver)

			// Start with task
			StartCoderWithTask(t, harness, coderID, "Create a validation test function")

			// Run for limited time
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			defer cancel()

			_ = harness.Run(ctx, func(h *TestHarness) bool {
				state := h.GetCoderState(coderID)
				return state == agent.StateDone || requestCount >= 3 // Limit requests
			})

			// Check if behavior matches expectations
			if tc.expectResend {
				if requestCount < 2 {
					t.Errorf("Expected resend for %s (approval_type: '%s'), but only got %d requests",
						tc.name, tc.approvalType, requestCount)
				}
			} else {
				if requestCount > 1 {
					t.Logf("Got %d requests for %s (approval_type: '%s'), expected 1",
						requestCount, tc.name, tc.approvalType)
				}
			}

			t.Logf("Validation test %s completed with %d requests", tc.name, requestCount)
		})
	}
}
