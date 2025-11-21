package architect

import (
	"testing"
	"time"

	"orchestrator/pkg/persistence"
	"orchestrator/pkg/proto"
)

func TestBuildAgentRequestFromMsg_QuestionRequest(t *testing.T) {
	now := time.Now()
	msg := &proto.AgentMsg{
		ID:          "req-123",
		FromAgent:   "coder-001",
		ToAgent:     "architect",
		Type:        proto.MsgTypeREQUEST,
		Timestamp:   now,
		ParentMsgID: "parent-456",
		Metadata: map[string]string{
			proto.KeyStoryID:       "story-789",
			proto.KeyCorrelationID: "corr-abc",
		},
	}

	// Add question request payload
	questionPayload := &proto.QuestionRequestPayload{
		Text: "How should I implement this feature?",
	}
	msg.SetTypedPayload(proto.NewQuestionRequestPayload(questionPayload))

	result := buildAgentRequestFromMsg(msg)

	// Verify basic fields
	if result.ID != "req-123" {
		t.Errorf("Expected ID 'req-123', got '%s'", result.ID)
	}
	if result.FromAgent != "coder-001" {
		t.Errorf("Expected FromAgent 'coder-001', got '%s'", result.FromAgent)
	}
	if result.ToAgent != "architect" {
		t.Errorf("Expected ToAgent 'architect', got '%s'", result.ToAgent)
	}
	if !result.CreatedAt.Equal(now) {
		t.Errorf("Expected CreatedAt %v, got %v", now, result.CreatedAt)
	}

	// Verify request type
	if result.RequestType != persistence.RequestTypeApproval {
		t.Errorf("Expected RequestType '%s', got '%s'", persistence.RequestTypeApproval, result.RequestType)
	}

	// Verify content extraction
	if result.Content != "How should I implement this feature?" {
		t.Errorf("Expected Content 'How should I implement this feature?', got '%s'", result.Content)
	}

	// Verify metadata fields
	if result.StoryID == nil || *result.StoryID != "story-789" {
		t.Errorf("Expected StoryID 'story-789', got %v", result.StoryID)
	}
	if result.CorrelationID == nil || *result.CorrelationID != "corr-abc" {
		t.Errorf("Expected CorrelationID 'corr-abc', got %v", result.CorrelationID)
	}
	if result.ParentMsgID == nil || *result.ParentMsgID != "parent-456" {
		t.Errorf("Expected ParentMsgID 'parent-456', got %v", result.ParentMsgID)
	}

	// Verify approval-specific fields are not set
	if result.ApprovalType != nil {
		t.Errorf("Expected ApprovalType to be nil for question request, got %v", result.ApprovalType)
	}
	if result.Reason != nil {
		t.Errorf("Expected Reason to be nil for question request, got %v", result.Reason)
	}
}

func TestBuildAgentRequestFromMsg_ApprovalRequest(t *testing.T) {
	now := time.Now()
	msg := &proto.AgentMsg{
		ID:        "req-456",
		FromAgent: "coder-002",
		ToAgent:   "architect",
		Type:      proto.MsgTypeREQUEST,
		Timestamp: now,
		Metadata: map[string]string{
			proto.KeyStoryID:    "story-xyz",
			proto.KeyApprovalID: "approval-123",
		},
	}

	// Add approval request payload
	approvalPayload := &proto.ApprovalRequestPayload{
		ApprovalType: proto.ApprovalTypePlan,
		Content:      "Here is my plan for the feature",
		Reason:       "Initial plan submission",
	}
	msg.SetTypedPayload(proto.NewApprovalRequestPayload(approvalPayload))

	result := buildAgentRequestFromMsg(msg)

	// Verify basic fields
	if result.ID != "req-456" {
		t.Errorf("Expected ID 'req-456', got '%s'", result.ID)
	}

	// Verify content extraction
	if result.Content != "Here is my plan for the feature" {
		t.Errorf("Expected Content 'Here is my plan for the feature', got '%s'", result.Content)
	}

	// Verify approval-specific fields
	if result.ApprovalType == nil || *result.ApprovalType != "plan" {
		t.Errorf("Expected ApprovalType 'plan', got %v", result.ApprovalType)
	}
	if result.Reason == nil || *result.Reason != "Initial plan submission" {
		t.Errorf("Expected Reason 'Initial plan submission', got %v", result.Reason)
	}

	// Verify correlation ID fallback (approval_id used when correlation_id not present)
	if result.CorrelationID == nil || *result.CorrelationID != "approval-123" {
		t.Errorf("Expected CorrelationID 'approval-123', got %v", result.CorrelationID)
	}
}

func TestBuildAgentRequestFromMsg_NoOptionalFields(t *testing.T) {
	now := time.Now()
	msg := &proto.AgentMsg{
		ID:        "req-789",
		FromAgent: "coder-003",
		ToAgent:   "architect",
		Type:      proto.MsgTypeREQUEST,
		Timestamp: now,
		Metadata:  map[string]string{}, // No metadata
	}

	// Add question request with no parent
	questionPayload := &proto.QuestionRequestPayload{
		Text: "Simple question",
	}
	msg.SetTypedPayload(proto.NewQuestionRequestPayload(questionPayload))

	result := buildAgentRequestFromMsg(msg)

	// Verify basic fields are set
	if result.ID != "req-789" {
		t.Errorf("Expected ID 'req-789', got '%s'", result.ID)
	}

	// Verify optional fields are nil
	if result.StoryID != nil {
		t.Errorf("Expected StoryID to be nil, got %v", result.StoryID)
	}
	if result.CorrelationID != nil {
		t.Errorf("Expected CorrelationID to be nil, got %v", result.CorrelationID)
	}
	if result.ParentMsgID != nil {
		t.Errorf("Expected ParentMsgID to be nil, got %v", result.ParentMsgID)
	}
}

func TestBuildAgentResponseFromMsg_QuestionResponse(t *testing.T) {
	now := time.Now()

	request := &proto.AgentMsg{
		ID: "req-111",
		Metadata: map[string]string{
			proto.KeyStoryID:    "story-abc",
			proto.KeyQuestionID: "question-xyz",
		},
	}

	response := &proto.AgentMsg{
		ID:        "resp-222",
		FromAgent: "architect",
		ToAgent:   "coder-001",
		Type:      proto.MsgTypeRESPONSE,
		Timestamp: now,
		Metadata: map[string]string{
			proto.KeyCorrelationID: "corr-999",
		},
	}

	// Add question response payload
	questionResp := &proto.QuestionResponsePayload{
		AnswerText: "You should implement it this way",
	}
	response.SetTypedPayload(proto.NewQuestionResponsePayload(questionResp))

	result := buildAgentResponseFromMsg(request, response)

	// Verify basic fields
	if result.ID != "resp-222" {
		t.Errorf("Expected ID 'resp-222', got '%s'", result.ID)
	}
	if result.FromAgent != "architect" {
		t.Errorf("Expected FromAgent 'architect', got '%s'", result.FromAgent)
	}
	if result.ToAgent != "coder-001" {
		t.Errorf("Expected ToAgent 'coder-001', got '%s'", result.ToAgent)
	}

	// Verify request linkage
	if result.RequestID == nil || *result.RequestID != "req-111" {
		t.Errorf("Expected RequestID 'req-111', got %v", result.RequestID)
	}

	// Verify response type
	if result.ResponseType != persistence.ResponseTypeAnswer {
		t.Errorf("Expected ResponseType '%s', got '%s'", persistence.ResponseTypeAnswer, result.ResponseType)
	}

	// Verify content extraction
	if result.Content != "You should implement it this way" {
		t.Errorf("Expected Content 'You should implement it this way', got '%s'", result.Content)
	}

	// Verify story ID from response (not fallback)
	if result.StoryID == nil || *result.StoryID != "story-abc" {
		t.Errorf("Expected StoryID 'story-abc', got %v", result.StoryID)
	}

	// Verify correlation ID from response
	if result.CorrelationID == nil || *result.CorrelationID != "corr-999" {
		t.Errorf("Expected CorrelationID 'corr-999', got %v", result.CorrelationID)
	}
}

func TestBuildAgentResponseFromMsg_ApprovalResponse(t *testing.T) {
	now := time.Now()

	request := &proto.AgentMsg{
		ID: "req-333",
		Metadata: map[string]string{
			proto.KeyStoryID: "story-def",
		},
	}

	response := &proto.AgentMsg{
		ID:        "resp-444",
		FromAgent: "architect",
		ToAgent:   "coder-002",
		Type:      proto.MsgTypeRESPONSE,
		Timestamp: now,
		Metadata:  map[string]string{},
	}

	// Add approval response payload (using ApprovalResult struct)
	approvalResp := &proto.ApprovalResult{
		ID:         "resp-444",
		Status:     proto.ApprovalStatusApproved,
		Feedback:   "Looks good, approved!",
		ReviewedBy: "architect",
		ReviewedAt: now,
	}
	response.SetTypedPayload(proto.NewApprovalResponsePayload(approvalResp))

	result := buildAgentResponseFromMsg(request, response)

	// Verify response type
	if result.ResponseType != persistence.ResponseTypeResult {
		t.Errorf("Expected ResponseType '%s', got '%s'", persistence.ResponseTypeResult, result.ResponseType)
	}

	// Verify content extraction from feedback
	if result.Content != "Looks good, approved!" {
		t.Errorf("Expected Content 'Looks good, approved!', got '%s'", result.Content)
	}

	// Verify status extraction
	if result.Status == nil || *result.Status != "APPROVED" {
		t.Errorf("Expected Status 'APPROVED', got %v", result.Status)
	}
}

func TestBuildAgentResponseFromMsg_InvalidApprovalStatus(t *testing.T) {
	now := time.Now()

	request := &proto.AgentMsg{
		ID: "req-555",
	}

	response := &proto.AgentMsg{
		ID:        "resp-666",
		FromAgent: "architect",
		ToAgent:   "coder-003",
		Type:      proto.MsgTypeRESPONSE,
		Timestamp: now,
		Metadata:  map[string]string{},
	}

	// Add approval response with invalid status
	approvalResp := &proto.ApprovalResult{
		ID:         "resp-666",
		Status:     proto.ApprovalStatus("INVALID_STATUS"),
		Feedback:   "Some feedback",
		ReviewedBy: "architect",
		ReviewedAt: now,
	}
	response.SetTypedPayload(proto.NewApprovalResponsePayload(approvalResp))

	result := buildAgentResponseFromMsg(request, response)

	// Verify status is nil when invalid (silently ignored)
	if result.Status != nil {
		t.Errorf("Expected Status to be nil for invalid status, got %v", result.Status)
	}

	// Content should still be extracted
	if result.Content != "Some feedback" {
		t.Errorf("Expected Content 'Some feedback', got '%s'", result.Content)
	}
}

func TestBuildAgentResponseFromMsg_StoryIDFallback(t *testing.T) {
	now := time.Now()

	request := &proto.AgentMsg{
		ID: "req-777",
		Metadata: map[string]string{
			proto.KeyStoryID: "story-fallback",
		},
	}

	response := &proto.AgentMsg{
		ID:        "resp-888",
		FromAgent: "architect",
		ToAgent:   "coder-004",
		Type:      proto.MsgTypeRESPONSE,
		Timestamp: now,
		Metadata:  map[string]string{}, // No story_id in response metadata
	}

	result := buildAgentResponseFromMsg(request, response)

	// Verify story ID falls back to request
	if result.StoryID == nil || *result.StoryID != "story-fallback" {
		t.Errorf("Expected StoryID to fall back to 'story-fallback', got %v", result.StoryID)
	}
}

func TestBuildAgentResponseFromMsg_MultipleResponseKinds(t *testing.T) {
	testCases := []struct {
		name             string
		payloadKind      proto.PayloadKind
		expectedRespType string
	}{
		{
			name:             "Question response",
			payloadKind:      proto.PayloadKindQuestionResponse,
			expectedRespType: persistence.ResponseTypeAnswer,
		},
		{
			name:             "Approval response",
			payloadKind:      proto.PayloadKindApprovalResponse,
			expectedRespType: persistence.ResponseTypeResult,
		},
		{
			name:             "Merge response",
			payloadKind:      proto.PayloadKindMergeResponse,
			expectedRespType: persistence.ResponseTypeResult,
		},
		{
			name:             "Requeue response",
			payloadKind:      proto.PayloadKindRequeueResponse,
			expectedRespType: persistence.ResponseTypeResult,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			request := &proto.AgentMsg{ID: "req-test"}
			response := &proto.AgentMsg{
				ID:        "resp-test",
				FromAgent: "architect",
				ToAgent:   "coder-test",
				Type:      proto.MsgTypeRESPONSE,
				Timestamp: time.Now(),
				Metadata:  map[string]string{},
			}

			// Set different payload kinds for testing
			switch tc.payloadKind {
			case proto.PayloadKindQuestionResponse:
				response.SetTypedPayload(proto.NewQuestionResponsePayload(&proto.QuestionResponsePayload{
					AnswerText: "test answer",
				}))
			default:
				// For other response types, set a simple payload
				response.SetTypedPayload(&proto.MessagePayload{
					Kind: tc.payloadKind,
					Data: []byte("{}"),
				})
			}

			result := buildAgentResponseFromMsg(request, response)

			if result.ResponseType != tc.expectedRespType {
				t.Errorf("Expected ResponseType '%s', got '%s'", tc.expectedRespType, result.ResponseType)
			}
		})
	}
}

func TestBuildAgentResponseFromMsg_NoResponseKind(t *testing.T) {
	request := &proto.AgentMsg{ID: "req-999"}
	response := &proto.AgentMsg{
		ID:        "resp-999",
		FromAgent: "architect",
		ToAgent:   "coder-999",
		Type:      proto.MsgTypeRESPONSE,
		Timestamp: time.Now(),
		Metadata:  map[string]string{}, // No response_kind
	}

	result := buildAgentResponseFromMsg(request, response)

	// Should default to ResponseTypeResult when response_kind is missing
	if result.ResponseType != persistence.ResponseTypeResult {
		t.Errorf("Expected ResponseType '%s' when no response_kind, got '%s'", persistence.ResponseTypeResult, result.ResponseType)
	}
}
