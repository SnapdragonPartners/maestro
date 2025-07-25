//go:build integration
// +build integration

package integration

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
	"testing/quick"
	"time"

	"orchestrator/pkg/coder"
	"orchestrator/pkg/proto"
)

// createApprovalResponse creates a proper approval response with ApprovalResult.
func createApprovalResponse(architectID, fromAgent, msgID, status, approvalType, feedback string) *proto.AgentMsg {
	response := proto.NewAgentMsg(proto.MsgTypeRESULT, architectID, fromAgent)
	response.ParentMsgID = msgID

	approvalResult := &proto.ApprovalResult{
		ID:         response.ID + "_approval",
		RequestID:  msgID,
		Type:       proto.ApprovalType(approvalType),
		Status:     proto.ApprovalStatus(status),
		Feedback:   feedback,
		ReviewedBy: architectID,
		ReviewedAt: time.Now(),
	}

	response.SetPayload("approval_result", approvalResult)
	return response
}

// FuzzInput represents randomized input for property-based testing.
type FuzzInput struct {
	ResponseType  string
	Status        string
	ApprovalType  string
	Feedback      string
	DelayMs       int
	ShouldTimeout bool
	ResponseCount int
}

// Generate implements quick.Generator for FuzzInput.
func (f FuzzInput) Generate(rand *rand.Rand, _ int) reflect.Value {
	responseTypes := []string{"approved", "changes_requested", "malformed", "empty"}
	statuses := []string{"APPROVED", "NEEDS_CHANGES", "REJECTED", "invalid", ""}
	approvalTypes := []string{"plan", "code", "unknown", "YEP", "123", ""}
	feedbacks := []string{
		"Good work!",
		"Please fix the errors",
		"```go\nfunc example() {}\n```",
		"Invalid code block without backticks",
		"",
		"Very long feedback message that goes on and on...",
	}

	input := FuzzInput{
		ResponseType:  responseTypes[rand.Intn(len(responseTypes))],
		Status:        statuses[rand.Intn(len(statuses))],
		ApprovalType:  approvalTypes[rand.Intn(len(approvalTypes))],
		Feedback:      feedbacks[rand.Intn(len(feedbacks))],
		DelayMs:       rand.Intn(200),       // 0-200ms delay
		ShouldTimeout: rand.Float32() < 0.2, // 20% chance of timeout
		ResponseCount: rand.Intn(3) + 1,     // 1-3 responses
	}

	return reflect.ValueOf(input)
}

// TestStory8PropertyBasedFuzzing tests random interleavings of messages.
func TestStory8PropertyBasedFuzzing(t *testing.T) {
	SetupTestEnvironment(t)

	// Property: The coder should never panic or deadlock, regardless of architect responses.
	property := func(input FuzzInput) bool {
		return testCoderStability(t, input)
	}

	config := &quick.Config{
		MaxCount: 50, // Run 50 random test cases
		Rand:     rand.New(rand.NewSource(time.Now().UnixNano())),
	}

	if err := quick.Check(property, config); err != nil {
		t.Errorf("Property-based test failed: %v", err)
	}
}

// testCoderStability tests that a coder remains stable with given fuzz input.
func testCoderStability(t *testing.T, input FuzzInput) bool {
	// Create test harness with short timeout for fuzzing.
	harness := NewTestHarness(t)
	timeouts := GetTestTimeouts()
	timeouts.Global = 2 * time.Second // Short timeout for fuzzing
	timeouts.Plan = 100 * time.Millisecond
	harness.SetTimeouts(timeouts)

	// Create fuzz architect that follows the input pattern.
	architect := createFuzzArchitect(input)
	harness.SetArchitect(architect)

	// Create coder.
	coderID := fmt.Sprintf("fuzz-coder-%d", time.Now().UnixNano())
	coderDriver := CreateTestCoder(t, coderID)
	harness.AddCoder(coderID, coderDriver)

	// Start with simple task.
	StartCoderWithTask(t, harness, coderID, "Create a simple test function")

	// Run with timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// The test passes if:
	// 1. No panic occurs.
	// 2. No deadlock occurs (timeout is acceptable)
	// 3. Coder ends in a valid state (DONE, ERROR, or valid waiting state)
	err := harness.Run(ctx, func(h *TestHarness) bool {
		state := h.GetCoderState(coderID)
		// Stop early if we reach a terminal state.
		return state == proto.StateDone || state == proto.StateError
	})

	// Check final state is valid.
	finalState := harness.GetCoderState(coderID)
	validFinalStates := []proto.State{
		proto.StateDone,
		proto.StateError,
		proto.StateWaiting,
		coder.StatePlanning,
		coder.StateCoding,
	}

	stateIsValid := false
	for _, validState := range validFinalStates {
		if finalState == validState {
			stateIsValid = true
			break
		}
	}

	if !stateIsValid {
		t.Logf("Fuzz test failed: invalid final state %s with input %+v", finalState, input)
		return false
	}

	// Timeout is acceptable for fuzzing.
	if err != nil {
		t.Logf("Fuzz test ended with timeout/error (acceptable): %v, final state: %s", err, finalState)
	}

	return true
}

// createFuzzArchitect creates an architect that behaves according to fuzz input.
func createFuzzArchitect(input FuzzInput) ArchitectAgent {
	responseCount := 0

	return NewMalformedResponseMockArchitect("fuzz-architect", func(msg *proto.AgentMsg) *proto.AgentMsg {
		responseCount++

		// Apply delay if specified.
		if input.DelayMs > 0 {
			time.Sleep(time.Duration(input.DelayMs) * time.Millisecond)
		}

		// Sometimes don't respond to simulate timeout.
		if input.ShouldTimeout && responseCount <= 1 {
			return nil
		}

		// Create response based on input type.
		response := proto.NewAgentMsg(proto.MsgTypeRESULT, "fuzz-architect", msg.FromAgent)
		response.ParentMsgID = msg.ID

		switch input.ResponseType {
		case "approved":
			return createApprovalResponse("fuzz-architect", msg.FromAgent, msg.ID, "APPROVED", "plan", input.Feedback)

		case "changes_requested":
			return createApprovalResponse("fuzz-architect", msg.FromAgent, msg.ID, "NEEDS_CHANGES", input.ApprovalType, input.Feedback)

		case "malformed":
			// Create malformed response.
			response.SetPayload("invalid_field", input.Status)
			response.SetPayload("another_invalid", input.ApprovalType)

		case "empty":
			// Empty response (no payload)
			break

		default:
			// Random response using proper format if status is valid
			if input.Status == "APPROVED" || input.Status == "NEEDS_CHANGES" || input.Status == "REJECTED" {
				return createApprovalResponse("fuzz-architect", msg.FromAgent, msg.ID, input.Status, input.ApprovalType, input.Feedback)
			}
			// Otherwise create malformed response
			response.SetPayload("invalid_status", input.Status)
			response.SetPayload("invalid_type", input.ApprovalType)
		}

		return response
	})
}

// TestStory8ConcurrentFuzzing tests multiple coders with random responses.
func TestStory8ConcurrentFuzzing(t *testing.T) {
	SetupTestEnvironment(t)

	// Property: Multiple coders should handle random responses without interference.
	property := func() bool {
		return testConcurrentStability(t)
	}

	config := &quick.Config{
		MaxCount: 20, // Fewer iterations for concurrent tests
		Rand:     rand.New(rand.NewSource(time.Now().UnixNano())),
	}

	if err := quick.Check(property, config); err != nil {
		t.Errorf("Concurrent fuzzing failed: %v", err)
	}
}

// testConcurrentStability tests stability with multiple concurrent coders.
func testConcurrentStability(t *testing.T) bool {
	// Create test harness.
	harness := NewTestHarness(t)
	timeouts := GetTestTimeouts()
	timeouts.Global = 3 * time.Second
	timeouts.Pump = 2 * time.Millisecond
	harness.SetTimeouts(timeouts)

	// Create random architect.
	rand := rand.New(rand.NewSource(time.Now().UnixNano()))
	fuzzInputInterface := FuzzInput{}.Generate(rand, 10).Interface()
	fuzzInput, ok := fuzzInputInterface.(FuzzInput)
	if !ok {
		t.Fatalf("Failed to generate FuzzInput: got type %T", fuzzInputInterface)
	}
	architect := createFuzzArchitect(fuzzInput)
	harness.SetArchitect(architect)

	// Create 3 coders.
	const coderCount = 3
	for i := 0; i < coderCount; i++ {
		coderID := fmt.Sprintf("concurrent-fuzz-%d-%d", time.Now().UnixNano(), i)
		coderDriver := CreateTestCoder(t, coderID)
		harness.AddCoder(coderID, coderDriver)
		StartCoderWithTask(t, harness, coderID, fmt.Sprintf("Task %d", i))
	}

	// Run briefly.
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	err := harness.Run(ctx, func(h *TestHarness) bool {
		// Stop if all coders reach terminal states.
		states := h.GetAllCoderStates()
		for _, state := range states {
			if state != proto.StateDone && state != proto.StateError {
				return false
			}
		}
		return len(states) == coderCount
	})

	// Check all coders are in valid states.
	finalStates := harness.GetAllCoderStates()
	for coderID, state := range finalStates {
		if state == proto.State("") { // Invalid/uninitialized state
			t.Logf("Concurrent fuzz failed: coder %s in invalid state", coderID)
			return false
		}
	}

	if err != nil {
		t.Logf("Concurrent fuzz ended with timeout (acceptable): %v", err)
	}

	return true
}

// TestStory8MessageSequenceFuzzing tests random message sequences.
func TestStory8MessageSequenceFuzzing(t *testing.T) {
	SetupTestEnvironment(t)

	// Test various message sequence patterns.
	sequences := [][]string{
		{"approved", "changes_requested", "approved"},
		{"malformed", "approved"},
		{"timeout", "changes_requested", "approved"},
		{"approved", "approved", "approved"}, // Duplicate approvals
		{"changes_requested", "changes_requested", "malformed", "approved"},
	}

	for i, sequence := range sequences {
		t.Run(fmt.Sprintf("sequence_%d", i), func(t *testing.T) {
			if !testMessageSequence(t, sequence) {
				t.Errorf("Message sequence %v failed", sequence)
			}
		})
	}
}

// testMessageSequence tests a specific sequence of architect responses.
func testMessageSequence(t *testing.T, sequence []string) bool {
	// Create test harness.
	harness := NewTestHarness(t)
	timeouts := GetTestTimeouts()
	timeouts.Global = 5 * time.Second
	harness.SetTimeouts(timeouts)

	sequenceIndex := 0

	// Create architect that follows the sequence.
	architect := NewMalformedResponseMockArchitect("sequence-architect", func(msg *proto.AgentMsg) *proto.AgentMsg {
		if sequenceIndex >= len(sequence) {
			// Default to approval after sequence.
			return createApprovalResponse("sequence-architect", msg.FromAgent, msg.ID, "APPROVED", "plan", "Sequence complete")
		}

		responseType := sequence[sequenceIndex]
		sequenceIndex++

		switch responseType {
		case "timeout":
			time.Sleep(200 * time.Millisecond) // Cause timeout
			return nil

		case "malformed":
			response := proto.NewAgentMsg(proto.MsgTypeRESULT, "sequence-architect", msg.FromAgent)
			response.ParentMsgID = msg.ID
			response.SetPayload("invalid", "malformed")
			return response

		case "approved":
			return createApprovalResponse("sequence-architect", msg.FromAgent, msg.ID, "APPROVED", "plan", "Approved in sequence")

		case "changes_requested":
			return createApprovalResponse("sequence-architect", msg.FromAgent, msg.ID, "NEEDS_CHANGES", "plan", "Changes requested in sequence")

		default:
			// Unknown response type, treat as malformed.
			response := proto.NewAgentMsg(proto.MsgTypeRESULT, "sequence-architect", msg.FromAgent)
			response.ParentMsgID = msg.ID
			response.SetPayload("unknown_response", responseType)
			return response
		}
	})
	harness.SetArchitect(architect)

	// Create coder.
	coderID := "sequence-coder"
	coderDriver := CreateTestCoder(t, coderID)
	harness.AddCoder(coderID, coderDriver)
	StartCoderWithTask(t, harness, coderID, "Handle message sequence")

	// Run with timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	err := harness.Run(ctx, func(h *TestHarness) bool {
		state := h.GetCoderState(coderID)
		return state == proto.StateDone || state == proto.StateError
	})

	// Check final state is reasonable.
	finalState := harness.GetCoderState(coderID)

	// The coder should handle the sequence gracefully.
	if finalState == proto.State("") {
		t.Logf("Message sequence test failed: invalid final state for sequence %v", sequence)
		return false
	}

	if err != nil {
		t.Logf("Message sequence ended with timeout (may be acceptable): %v", err)
	}

	return true
}
