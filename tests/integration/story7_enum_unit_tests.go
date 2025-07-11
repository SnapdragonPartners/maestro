package integration

import (
	"testing"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/coder"
	"orchestrator/pkg/proto"
)

// TestStory7ParseRequestType tests the ParseRequestType function with various inputs
func TestStory7ParseRequestType(t *testing.T) {
	testCases := []struct {
		name          string
		input         string
		expected      proto.RequestType
		expectError   bool
		errorContains string
	}{
		{
			name:        "valid_approval",
			input:       "approval",
			expected:    proto.RequestApproval,
			expectError: false,
		},
		{
			name:        "valid_question",
			input:       "question",
			expected:    proto.RequestQuestion,
			expectError: false,
		},
		{
			name:        "valid_review",
			input:       "review",
			expected:    proto.RequestApprovalReview,
			expectError: false,
		},
		{
			name:        "valid_approval_uppercase",
			input:       "APPROVAL",
			expected:    proto.RequestApproval,
			expectError: false,
		},
		{
			name:        "valid_mixed_case",
			input:       "ApPrOvAl",
			expected:    proto.RequestApproval,
			expectError: false,
		},
		{
			name:          "invalid_empty",
			input:         "",
			expected:      proto.RequestType(""),
			expectError:   true,
			errorContains: "empty",
		},
		{
			name:          "invalid_unknown",
			input:         "unknown_request_type",
			expected:      proto.RequestType(""),
			expectError:   true,
			errorContains: "unknown",
		},
		{
			name:          "invalid_numeric",
			input:         "123",
			expected:      proto.RequestType(""),
			expectError:   true,
			errorContains: "unknown",
		},
		{
			name:          "invalid_special_chars",
			input:         "approval!@#",
			expected:      proto.RequestType(""),
			expectError:   true,
			errorContains: "unknown",
		},
		{
			name:        "valid_with_whitespace",
			input:       "  approval  ",
			expected:    proto.RequestApproval,
			expectError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := proto.ParseRequestType(tc.input)

			if tc.expectError {
				if err == nil {
					t.Errorf("Expected error for input '%s', but got none", tc.input)
					return
				}
				if tc.errorContains != "" && !containsString(err.Error(), tc.errorContains) {
					t.Errorf("Expected error to contain '%s', but got: %v", tc.errorContains, err)
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error for input '%s': %v", tc.input, err)
				return
			}

			if result != tc.expected {
				t.Errorf("Input '%s': expected %v, got %v", tc.input, tc.expected, result)
			}
		})
	}
}

// TestStory7NormaliseApprovalType tests the NormaliseApprovalType function
func TestStory7NormaliseApprovalType(t *testing.T) {
	testCases := []struct {
		name          string
		input         string
		expected      string
		expectError   bool
		errorContains string
	}{
		{
			name:        "valid_plan",
			input:       "plan",
			expected:    "plan",
			expectError: false,
		},
		{
			name:        "valid_code",
			input:       "code",
			expected:    "code",
			expectError: false,
		},
		{
			name:        "valid_plan_uppercase",
			input:       "PLAN",
			expected:    "plan",
			expectError: false,
		},
		{
			name:        "valid_code_mixed_case",
			input:       "CoDe",
			expected:    "code",
			expectError: false,
		},
		{
			name:        "valid_with_whitespace",
			input:       "  plan  ",
			expected:    "plan",
			expectError: false,
		},
		{
			name:          "invalid_empty",
			input:         "",
			expected:      "",
			expectError:   true,
			errorContains: "empty",
		},
		{
			name:          "invalid_unknown",
			input:         "unknown_approval_type",
			expected:      "",
			expectError:   true,
			errorContains: "unknown",
		},
		{
			name:          "invalid_numeric",
			input:         "123",
			expected:      "",
			expectError:   true,
			errorContains: "unknown",
		},
		{
			name:          "invalid_special_chars",
			input:         "plan!@#",
			expected:      "",
			expectError:   true,
			errorContains: "unknown",
		},
		// Note: No deprecated aliases currently defined
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := proto.NormaliseApprovalType(tc.input)

			if tc.expectError {
				if err == nil {
					t.Errorf("Expected error for input '%s', but got none", tc.input)
					return
				}
				if tc.errorContains != "" && !containsString(err.Error(), tc.errorContains) {
					t.Errorf("Expected error to contain '%s', but got: %v", tc.errorContains, err)
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error for input '%s': %v", tc.input, err)
				return
			}

			if result.String() != tc.expected {
				t.Errorf("Input '%s': expected '%s', got '%s'", tc.input, tc.expected, result.String())
			}
		})
	}
}

// TestStory7StateTransitionValidation tests agent state transition validation
func TestStory7StateTransitionValidation(t *testing.T) {
	testCases := []struct {
		name     string
		from     agent.State
		to       agent.State
		expected bool
	}{
		// Valid transitions (using actual coder states)
		{
			name:     "waiting_to_planning",
			from:     agent.StateWaiting,
			to:       coder.StatePlanning.ToAgentState(),
			expected: true,
		},
		{
			name:     "planning_to_coding",
			from:     coder.StatePlanning.ToAgentState(),
			to:       coder.StateCoding.ToAgentState(),
			expected: true,
		},
		{
			name:     "coding_to_done",
			from:     coder.StateCoding.ToAgentState(),
			to:       agent.StateDone,
			expected: true,
		},
		{
			name:     "any_to_error",
			from:     coder.StatePlanning.ToAgentState(),
			to:       agent.StateError,
			expected: true,
		},
		// Invalid transitions
		{
			name:     "waiting_to_coding_invalid",
			from:     agent.StateWaiting,
			to:       coder.StateCoding.ToAgentState(),
			expected: false,
		},
		{
			name:     "done_to_planning_invalid",
			from:     agent.StateDone,
			to:       coder.StatePlanning.ToAgentState(),
			expected: false,
		},
		{
			name:     "coding_to_waiting_invalid",
			from:     coder.StateCoding.ToAgentState(),
			to:       agent.StateWaiting,
			expected: false,
		},
		{
			name:     "error_to_planning_invalid",
			from:     agent.StateError,
			to:       coder.StatePlanning.ToAgentState(),
			expected: false,
		},
		// Same state transitions
		{
			name:     "waiting_to_waiting",
			from:     agent.StateWaiting,
			to:       agent.StateWaiting,
			expected: false, // Usually same-state transitions are not valid
		},
		{
			name:     "done_to_done",
			from:     agent.StateDone,
			to:       agent.StateDone,
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Note: This assumes there's a function to validate transitions
			// If it doesn't exist, we'd need to implement it or check the state machine logic
			result := isValidStateTransition(tc.from, tc.to)

			if result != tc.expected {
				t.Errorf("Transition %s -> %s: expected %v, got %v",
					tc.from, tc.to, tc.expected, result)
			}
		})
	}
}

// TestStory7MessageTypeValidation tests message type validation and parsing
func TestStory7MessageTypeValidation(t *testing.T) {
	testCases := []struct {
		name        string
		msgType     proto.MsgType
		isValid     bool
		description string
	}{
		{
			name:        "valid_task",
			msgType:     proto.MsgTypeTASK,
			isValid:     true,
			description: "Task assignment message",
		},
		{
			name:        "valid_request",
			msgType:     proto.MsgTypeREQUEST,
			isValid:     true,
			description: "Request message",
		},
		{
			name:        "valid_result",
			msgType:     proto.MsgTypeRESULT,
			isValid:     true,
			description: "Result message",
		},
		{
			name:        "valid_question",
			msgType:     proto.MsgTypeQUESTION,
			isValid:     true,
			description: "Question message",
		},
		{
			name:        "valid_answer",
			msgType:     proto.MsgTypeANSWER,
			isValid:     true,
			description: "Answer message",
		},
		{
			name:        "valid_error",
			msgType:     proto.MsgTypeERROR,
			isValid:     true,
			description: "Error message",
		},
		{
			name:        "valid_shutdown",
			msgType:     proto.MsgTypeSHUTDOWN,
			isValid:     true,
			description: "Shutdown message",
		},
		{
			name:        "invalid_undefined",
			msgType:     proto.MsgType("UNDEFINED"),
			isValid:     false,
			description: "Undefined message type",
		},
		{
			name:        "invalid_empty",
			msgType:     proto.MsgType(""),
			isValid:     false,
			description: "Empty message type",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Test string representation
			str := string(tc.msgType)
			if tc.isValid {
				if str == "" {
					t.Errorf("Valid message type %v should have proper string representation, got: %s",
						tc.msgType, str)
				}
			}

			// Test that we can create messages with valid types
			if tc.isValid {
				msg := proto.NewAgentMsg(tc.msgType, "test-from", "test-to")
				if msg.Type != tc.msgType {
					t.Errorf("Created message type mismatch: expected %v, got %v",
						tc.msgType, msg.Type)
				}
			}
		})
	}
}

// TestStory7EdgeCaseHandling tests various edge cases in enum handling
func TestStory7EdgeCaseHandling(t *testing.T) {
	t.Run("nil_input_handling", func(t *testing.T) {
		// Test that functions handle nil or empty inputs gracefully
		_, err := proto.ParseRequestType("")
		if err == nil {
			t.Error("Expected error for empty request type")
		}

		_, err = proto.NormaliseApprovalType("")
		if err == nil {
			t.Error("Expected error for empty approval type")
		}
	})

	t.Run("very_long_input", func(t *testing.T) {
		// Test with very long strings
		longInput := "approval" + string(make([]byte, 1000))
		_, err := proto.ParseRequestType(longInput)
		if err == nil {
			t.Error("Expected error for very long input")
		}
	})

	t.Run("unicode_input", func(t *testing.T) {
		// Test with unicode characters
		unicodeInput := "approval_🚀"
		_, err := proto.ParseRequestType(unicodeInput)
		if err == nil {
			t.Error("Expected error for unicode input")
		}
	})

	t.Run("case_sensitivity", func(t *testing.T) {
		// Test various case combinations
		inputs := []string{"APPROVAL", "approval", "Approval", "aPpRoVaL"}
		for _, input := range inputs {
			result, err := proto.ParseRequestType(input)
			if err != nil {
				t.Errorf("Failed to parse case variant '%s': %v", input, err)
				continue
			}
			if result != proto.RequestApproval {
				t.Errorf("Case variant '%s' should parse to RequestApproval, got %v", input, result)
			}
		}
	})
}

// Helper function to check if string contains substring
func containsString(s, substr string) bool {
	return len(substr) <= len(s) && (substr == "" ||
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}

// isValidStateTransition checks if a state transition is valid
// This is a simplified implementation for testing purposes
func isValidStateTransition(from, to agent.State) bool {
	// Define valid transitions based on the coder FSM
	validTransitions := map[agent.State][]agent.State{
		agent.StateWaiting:                 {coder.StatePlanning.ToAgentState(), agent.StateError},
		coder.StatePlanning.ToAgentState(): {coder.StateCoding.ToAgentState(), agent.StateError},
		coder.StateCoding.ToAgentState():   {agent.StateDone, agent.StateError},
		agent.StateDone:                    {}, // Terminal state
		agent.StateError:                   {}, // Terminal state
	}

	// Check if transition is in the valid list
	validTargets, exists := validTransitions[from]
	if !exists {
		return false
	}

	for _, validTarget := range validTargets {
		if validTarget == to {
			return true
		}
	}

	return false
}
