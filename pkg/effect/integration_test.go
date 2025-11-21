package effect

import (
	"context"
	"testing"
	"time"

	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
)

// TestQuestionEffect_Integration tests AwaitQuestionEffect end-to-end with BaseRuntime.
func TestQuestionEffect_Integration(t *testing.T) {
	// Setup
	dispatcher := &mockDispatcher{}
	logger := logx.NewLogger("test")
	replyCh := make(chan *proto.AgentMsg, 1)
	runtime := NewBaseRuntime(dispatcher, logger, "coder-001", "coder", replyCh)

	// Create question effect
	effect := NewQuestionEffect(
		"How should I implement feature X?",
		"Working on story-123",
		"medium",
		"PLANNING",
	)
	effect.StoryID = "story-123"
	effect.Timeout = 1 * time.Second

	// Simulate architect sending response in background
	go func() {
		time.Sleep(50 * time.Millisecond) // Simulate processing time

		// Create response message with typed payload
		responseMsg := proto.NewAgentMsg(proto.MsgTypeRESPONSE, "architect", "coder-001")

		// Create question response payload
		questionResp := &proto.QuestionResponsePayload{
			AnswerText: "You should implement it using pattern Y",
		}
		responseMsg.SetTypedPayload(proto.NewQuestionResponsePayload(questionResp))

		replyCh <- responseMsg
	}()

	// Execute effect
	ctx := context.Background()
	result, err := effect.Execute(ctx, runtime)

	// Assert
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	questionResult, ok := result.(*QuestionResult)
	if !ok {
		t.Fatalf("Expected QuestionResult, got %T", result)
	}

	if questionResult.Answer != "You should implement it using pattern Y" {
		t.Errorf("Expected answer 'You should implement it using pattern Y', got '%s'", questionResult.Answer)
	}

	// Verify REQUEST message was sent
	if len(dispatcher.messages) != 1 {
		t.Fatalf("Expected 1 dispatched message, got %d", len(dispatcher.messages))
	}

	requestMsg := dispatcher.messages[0]
	if requestMsg.Type != proto.MsgTypeREQUEST {
		t.Errorf("Expected message type REQUEST, got %s", requestMsg.Type)
	}
	if requestMsg.FromAgent != "coder-001" {
		t.Errorf("Expected from agent 'coder-001', got '%s'", requestMsg.FromAgent)
	}
	if requestMsg.ToAgent != "architect" {
		t.Errorf("Expected to agent 'architect', got '%s'", requestMsg.ToAgent)
	}
}

// TestBudgetReviewEffect_Integration tests BudgetReviewEffect end-to-end with BaseRuntime.
func TestBudgetReviewEffect_Integration(t *testing.T) {
	// Setup
	dispatcher := &mockDispatcher{}
	logger := logx.NewLogger("test")
	replyCh := make(chan *proto.AgentMsg, 1)
	runtime := NewBaseRuntime(dispatcher, logger, "coder-001", "coder", replyCh)

	// Create budget review effect
	effect := NewBudgetReviewEffect(
		"Planning iteration limit reached (10 iterations)",
		"Need guidance on whether to continue",
		"PLANNING",
	)
	effect.StoryID = "story-123"
	effect.Timeout = 1 * time.Second

	// Simulate architect sending approval in background
	go func() {
		time.Sleep(50 * time.Millisecond) // Simulate processing time

		// Create response message with typed payload
		responseMsg := proto.NewAgentMsg(proto.MsgTypeRESPONSE, "architect", "coder-001")

		// Create approval response payload
		approvalResp := &proto.ApprovalResult{
			ID:         "approval-123",
			RequestID:  "request-123",
			Type:       proto.ApprovalTypeBudgetReview,
			Status:     proto.ApprovalStatusApproved,
			Feedback:   "Approved - continue with current approach",
			ReviewedBy: "architect",
			ReviewedAt: time.Now(),
		}
		responseMsg.SetTypedPayload(proto.NewApprovalResponsePayload(approvalResp))

		replyCh <- responseMsg
	}()

	// Execute effect
	ctx := context.Background()
	result, err := effect.Execute(ctx, runtime)

	// Assert
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	budgetResult, ok := result.(*BudgetReviewResult)
	if !ok {
		t.Fatalf("Expected BudgetReviewResult, got %T", result)
	}

	if budgetResult.Status != proto.ApprovalStatusApproved {
		t.Errorf("Expected status APPROVED, got %s", budgetResult.Status)
	}
	if budgetResult.Feedback != "Approved - continue with current approach" {
		t.Errorf("Expected feedback 'Approved - continue with current approach', got '%s'", budgetResult.Feedback)
	}
	if budgetResult.OriginState != "PLANNING" {
		t.Errorf("Expected origin state 'PLANNING', got '%s'", budgetResult.OriginState)
	}

	// Verify REQUEST message was sent
	if len(dispatcher.messages) != 1 {
		t.Fatalf("Expected 1 dispatched message, got %d", len(dispatcher.messages))
	}

	requestMsg := dispatcher.messages[0]
	if requestMsg.Type != proto.MsgTypeREQUEST {
		t.Errorf("Expected message type REQUEST, got %s", requestMsg.Type)
	}
}

// TestQuestionEffect_Timeout tests that question effect times out gracefully.
func TestQuestionEffect_Timeout(t *testing.T) {
	// Setup
	dispatcher := &mockDispatcher{}
	logger := logx.NewLogger("test")
	replyCh := make(chan *proto.AgentMsg, 1) // No response will be sent
	runtime := NewBaseRuntime(dispatcher, logger, "coder-001", "coder", replyCh)

	// Create question effect with very short timeout
	effect := NewQuestionEffect(
		"Test question",
		"Test context",
		"low",
		"PLANNING",
	)
	effect.StoryID = "story-123"
	effect.Timeout = 50 * time.Millisecond // Very short timeout

	// Execute effect - should timeout
	ctx := context.Background()
	result, err := effect.Execute(ctx, runtime)

	// Assert
	if err == nil {
		t.Fatal("Expected timeout error, got nil")
	}
	if result != nil {
		t.Errorf("Expected nil result on timeout, got %+v", result)
	}

	// Error should mention timeout or receive failure
	if err.Error() == "" {
		t.Error("Expected non-empty error message")
	}
}

// TestBudgetReviewEffect_WrongResponseType tests handling of wrong message type.
func TestBudgetReviewEffect_WrongResponseType(t *testing.T) {
	// Setup
	dispatcher := &mockDispatcher{}
	logger := logx.NewLogger("test")
	replyCh := make(chan *proto.AgentMsg, 1)
	runtime := NewBaseRuntime(dispatcher, logger, "coder-001", "coder", replyCh)

	// Create budget review effect
	effect := NewBudgetReviewEffect(
		"Test content",
		"Test reason",
		"PLANNING",
	)
	effect.StoryID = "story-123"
	effect.Timeout = 1 * time.Second

	// Simulate wrong message type being sent
	go func() {
		time.Sleep(50 * time.Millisecond)

		// Send ERROR instead of RESPONSE
		wrongMsg := proto.NewAgentMsg(proto.MsgTypeERROR, "architect", "coder-001")
		replyCh <- wrongMsg
	}()

	// Execute effect - should fail due to wrong type
	ctx := context.Background()
	result, err := effect.Execute(ctx, runtime)

	// Assert
	if err == nil {
		t.Fatal("Expected error for wrong message type, got nil")
	}
	if result != nil {
		t.Errorf("Expected nil result on error, got %+v", result)
	}
}

// TestMultipleAgents_CanUseBlockingEffects tests that all agents can use blocking effects.
func TestMultipleAgents_CanUseBlockingEffects(t *testing.T) {
	testCases := []struct {
		name      string
		agentID   string
		agentRole string
	}{
		{"Coder", "coder-001", "coder"},
		{"Architect", "architect", "architect"},
		{"PM", "pm", "pm"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup
			dispatcher := &mockDispatcher{}
			logger := logx.NewLogger("test")
			replyCh := make(chan *proto.AgentMsg, 1)
			runtime := NewBaseRuntime(dispatcher, logger, tc.agentID, tc.agentRole, replyCh)

			// Create question effect
			effect := NewQuestionEffect(
				"Test question from "+tc.agentRole,
				"Test context",
				"medium",
				"WORKING",
			)
			effect.StoryID = "story-123"
			effect.Timeout = 1 * time.Second

			// Simulate response
			go func() {
				time.Sleep(50 * time.Millisecond)

				responseMsg := proto.NewAgentMsg(proto.MsgTypeRESPONSE, "architect", tc.agentID)
				questionResp := &proto.QuestionResponsePayload{
					AnswerText: "Answer for " + tc.agentRole,
				}
				responseMsg.SetTypedPayload(proto.NewQuestionResponsePayload(questionResp))

				replyCh <- responseMsg
			}()

			// Execute effect
			ctx := context.Background()
			result, err := effect.Execute(ctx, runtime)

			// Assert
			if err != nil {
				t.Fatalf("Expected no error for %s, got: %v", tc.agentRole, err)
			}

			questionResult, ok := result.(*QuestionResult)
			if !ok {
				t.Fatalf("Expected QuestionResult for %s, got %T", tc.agentRole, result)
			}

			if questionResult.Answer != "Answer for "+tc.agentRole {
				t.Errorf("Expected answer 'Answer for %s', got '%s'", tc.agentRole, questionResult.Answer)
			}

			// Verify request was sent
			if len(dispatcher.messages) != 1 {
				t.Fatalf("Expected 1 message from %s, got %d", tc.agentRole, len(dispatcher.messages))
			}

			if dispatcher.messages[0].FromAgent != tc.agentID {
				t.Errorf("Expected from agent '%s', got '%s'", tc.agentID, dispatcher.messages[0].FromAgent)
			}
		})
	}
}
