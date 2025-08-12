package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"orchestrator/pkg/effect"
	"orchestrator/pkg/proto"
)

// MockEffectRuntime for testing.
type MockEffectRuntime struct {
	returnError     error
	messageToReturn *proto.AgentMsg
	sentMessages    []*proto.AgentMsg
}

func (m *MockEffectRuntime) SendMessage(msg *proto.AgentMsg) error {
	m.sentMessages = append(m.sentMessages, msg)
	return nil
}

func (m *MockEffectRuntime) ReceiveMessage(_ context.Context, _ proto.MsgType) (*proto.AgentMsg, error) {
	if m.returnError != nil {
		return nil, m.returnError
	}
	return m.messageToReturn, nil
}

func (m *MockEffectRuntime) Info(_ string, _ ...any)  {}
func (m *MockEffectRuntime) Debug(_ string, _ ...any) {}
func (m *MockEffectRuntime) Error(_ string, _ ...any) {}

func (m *MockEffectRuntime) GetAgentID() string {
	return "test-coder"
}

func (m *MockEffectRuntime) GetAgentRole() string {
	return "coder"
}

func TestApprovalEffect_Execute_Success(t *testing.T) {
	// Create the RESPONSE message as the architect would send it
	resultMsg := proto.NewAgentMsg(proto.MsgTypeRESPONSE, "architect-001", "test-coder")
	resultMsg.SetPayload("status", "APPROVED")
	resultMsg.SetPayload("feedback", "Plan looks good")
	resultMsg.SetPayload("approval_id", "approval-123")

	// Mock runtime that returns this message
	mockRuntime := &MockEffectRuntime{
		messageToReturn: resultMsg,
	}

	// Create the effect
	eff := effect.NewApprovalEffect("test plan content", "Plan requires approval", proto.ApprovalTypePlan)
	eff.TargetAgent = "architect-001"
	eff.Timeout = 1 * time.Minute

	// Execute the effect
	ctx := context.Background()
	result, err := eff.Execute(ctx, mockRuntime)

	// Verify success
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify the result
	approvalResult, ok := result.(*effect.ApprovalResult)
	if !ok {
		t.Fatalf("Expected *ApprovalResult, got: %T", result)
	}

	if approvalResult.Status != proto.ApprovalStatusApproved {
		t.Errorf("Expected status APPROVED, got: %s", approvalResult.Status)
	}

	if approvalResult.Feedback != "Plan looks good" {
		t.Errorf("Expected feedback 'Plan looks good', got: %s", approvalResult.Feedback)
	}

	if approvalResult.ApprovalID != "approval-123" {
		t.Errorf("Expected approval ID 'approval-123', got: %s", approvalResult.ApprovalID)
	}

	// Verify a REQUEST message was sent
	if len(mockRuntime.sentMessages) != 1 {
		t.Fatalf("Expected 1 sent message, got: %d", len(mockRuntime.sentMessages))
	}

	sentMsg := mockRuntime.sentMessages[0]
	if sentMsg.Type != proto.MsgTypeREQUEST {
		t.Errorf("Expected REQUEST message type, got: %s", sentMsg.Type)
	}

	if sentMsg.ToAgent != "architect-001" {
		t.Errorf("Expected message to architect-001, got: %s", sentMsg.ToAgent)
	}
}

func TestApprovalEffect_Execute_Rejected(t *testing.T) {
	// Create the RESPONSE message
	resultMsg := proto.NewAgentMsg(proto.MsgTypeRESPONSE, "architect-001", "test-coder")
	resultMsg.SetPayload("status", "REJECTED")
	resultMsg.SetPayload("feedback", "Plan needs improvements")
	resultMsg.SetPayload("approval_id", "approval-123")

	mockRuntime := &MockEffectRuntime{
		messageToReturn: resultMsg,
	}

	eff := effect.NewApprovalEffect("test plan content", "Plan requires approval", proto.ApprovalTypePlan)
	eff.TargetAgent = "architect-001"
	eff.Timeout = 1 * time.Minute

	ctx := context.Background()
	result, err := eff.Execute(ctx, mockRuntime)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	approvalResult, ok := result.(*effect.ApprovalResult)
	if !ok {
		t.Fatalf("Expected *ApprovalResult, got: %T", result)
	}

	if approvalResult.Status != proto.ApprovalStatusRejected {
		t.Errorf("Expected status REJECTED, got: %s", approvalResult.Status)
	}

	if approvalResult.Feedback != "Plan needs improvements" {
		t.Errorf("Expected feedback 'Plan needs improvements', got: %s", approvalResult.Feedback)
	}
}

func TestApprovalEffect_Execute_MissingStatus(t *testing.T) {
	// Create a RESPONSE message without status payload (should fail)
	resultMsg := proto.NewAgentMsg(proto.MsgTypeRESPONSE, "architect-001", "test-coder")
	resultMsg.SetPayload("feedback", "some feedback") // Missing status field

	mockRuntime := &MockEffectRuntime{
		messageToReturn: resultMsg,
	}

	eff := effect.NewApprovalEffect("test plan content", "Plan requires approval", proto.ApprovalTypePlan)
	eff.TargetAgent = "architect-001"
	eff.Timeout = 1 * time.Minute

	ctx := context.Background()
	_, err := eff.Execute(ctx, mockRuntime)

	if err == nil {
		t.Fatal("Expected error for missing status, got nil")
	}

	expectedError := "approval response missing status field"
	if err.Error() != expectedError {
		t.Errorf("Expected error '%s', got: '%s'", expectedError, err.Error())
	}
}

func TestNewPlanApprovalEffectWithStoryID(t *testing.T) {
	eff := effect.NewPlanApprovalEffectWithStoryID("my plan content", "my task content", "story-123")

	if eff.ApprovalType != proto.ApprovalTypePlan {
		t.Errorf("Expected ApprovalTypePlan, got: %s", eff.ApprovalType)
	}

	if eff.TargetAgent != "architect" {
		t.Errorf("Expected target agent 'architect', got: %s", eff.TargetAgent)
	}

	if !strings.Contains(eff.Content, "my plan content") {
		t.Errorf("Expected content to contain plan content, got: %s", eff.Content)
	}

	if !strings.Contains(eff.Content, "my task content") {
		t.Errorf("Expected content to contain task content, got: %s", eff.Content)
	}

	if !strings.Contains(eff.Content, "story-123") {
		t.Errorf("Expected content to contain story ID, got: %s", eff.Content)
	}

	if eff.StoryID != "story-123" {
		t.Errorf("Expected StoryID to be 'story-123', got: %s", eff.StoryID)
	}
}

func TestNewCompletionApprovalEffectWithStoryID(t *testing.T) {
	eff := effect.NewCompletionApprovalEffectWithStoryID("completion summary", "file1.go, file2.go", "story-123")

	if eff.ApprovalType != proto.ApprovalTypeCompletion {
		t.Errorf("Expected ApprovalTypeCompletion, got: %s", eff.ApprovalType)
	}

	if !strings.Contains(eff.Content, "completion summary") {
		t.Errorf("Expected content to contain summary, got: %s", eff.Content)
	}

	if !strings.Contains(eff.Content, "file1.go, file2.go") {
		t.Errorf("Expected content to contain files, got: %s", eff.Content)
	}

	if !strings.Contains(eff.Content, "story-123") {
		t.Errorf("Expected content to contain story ID, got: %s", eff.Content)
	}

	if eff.StoryID != "story-123" {
		t.Errorf("Expected StoryID to be 'story-123', got: %s", eff.StoryID)
	}
}

func TestAwaitQuestionEffect_Execute_Success(t *testing.T) {
	// Create mock runtime that returns an ANSWER message
	answerMsg := proto.NewAgentMsg(proto.MsgTypeRESPONSE, "architect-001", "test-coder")
	answerMsg.SetPayload("answer", "Use the existing UserService pattern")

	mockRuntime := &MockEffectRuntime{
		messageToReturn: answerMsg,
	}

	// Create the question effect
	eff := &effect.AwaitQuestionEffect{
		Question:    "How should I implement user authentication?",
		Context:     "Found existing auth patterns",
		Urgency:     string(proto.PriorityHigh),
		OriginState: "CODING", // This should use StateCoding but it's in coder package
		TargetAgent: "architect",
		Timeout:     1 * time.Minute,
	}

	// Execute the effect
	ctx := context.Background()
	result, err := eff.Execute(ctx, mockRuntime)

	// Verify success
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify the result
	questionResult, ok := result.(*effect.QuestionResult)
	if !ok {
		t.Fatalf("Expected *QuestionResult, got: %T", result)
	}

	if questionResult.Answer != "Use the existing UserService pattern" {
		t.Errorf("Expected answer 'Use the existing UserService pattern', got: %s", questionResult.Answer)
	}

	// Verify a QUESTION message was sent
	if len(mockRuntime.sentMessages) != 1 {
		t.Fatalf("Expected 1 sent message, got: %d", len(mockRuntime.sentMessages))
	}

	sentMsg := mockRuntime.sentMessages[0]
	if sentMsg.Type != proto.MsgTypeREQUEST {
		t.Errorf("Expected QUESTION message type, got: %s", sentMsg.Type)
	}

	if sentMsg.ToAgent != "architect" {
		t.Errorf("Expected message to architect, got: %s", sentMsg.ToAgent)
	}
}

func TestAwaitQuestionEffect_Execute_EmptyAnswer(t *testing.T) {
	// Create mock runtime that returns an ANSWER message with empty content
	answerMsg := proto.NewAgentMsg(proto.MsgTypeRESPONSE, "architect-001", "test-coder")
	answerMsg.SetPayload("answer", "")

	mockRuntime := &MockEffectRuntime{
		messageToReturn: answerMsg,
	}

	eff := &effect.AwaitQuestionEffect{
		Question:    "Test question?",
		OriginState: "CODING", // This should use StateCoding but it's in coder package
		TargetAgent: "architect",
		Timeout:     1 * time.Minute,
	}

	ctx := context.Background()
	_, err := eff.Execute(ctx, mockRuntime)

	if err == nil {
		t.Fatal("Expected error for empty answer, got nil")
	}

	expectedError := "received empty answer content"
	if err.Error() != expectedError {
		t.Errorf("Expected error '%s', got: '%s'", expectedError, err.Error())
	}
}

func TestNewQuestionEffect(t *testing.T) {
	eff := effect.NewQuestionEffect("How to implement this?", "Some context", string(proto.PriorityMedium), "PLANNING")

	if eff.Question != "How to implement this?" {
		t.Errorf("Expected question 'How to implement this?', got: %s", eff.Question)
	}

	if eff.Context != "Some context" {
		t.Errorf("Expected context 'Some context', got: %s", eff.Context)
	}

	if eff.Urgency != string(proto.PriorityMedium) {
		t.Errorf("Expected urgency '%s', got: %s", proto.PriorityMedium, eff.Urgency)
	}

	if eff.OriginState != "PLANNING" {
		t.Errorf("Expected origin state 'PLANNING', got: %s", eff.OriginState)
	}

	if eff.TargetAgent != "architect" {
		t.Errorf("Expected target agent 'architect', got: %s", eff.TargetAgent)
	}
}
