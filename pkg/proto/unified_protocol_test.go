package proto

import (
	"testing"
)

func TestGetRequestKind_IncidentAction(t *testing.T) {
	msg := NewAgentMsg(MsgTypeREQUEST, "pm-001", "architect")
	msg.SetTypedPayload(NewIncidentActionPayload(&IncidentActionPayload{
		IncidentID: "inc-042",
		Action:     "resume",
		Reason:     "Environment repaired",
	}))

	kind, ok := GetRequestKind(msg)
	if !ok {
		t.Fatal("GetRequestKind returned false for incident_action REQUEST")
	}
	if kind != RequestKindExecution {
		t.Errorf("expected RequestKindExecution, got %q", kind)
	}
}

func TestGetRequestKind_NonRequest(t *testing.T) {
	msg := NewAgentMsg(MsgTypeRESPONSE, "architect", "pm-001")
	msg.SetTypedPayload(NewIncidentActionResultPayload(&IncidentActionResultPayload{
		IncidentID: "inc-042",
		Action:     "resume",
		Success:    true,
		Message:    "done",
	}))

	_, ok := GetRequestKind(msg)
	if ok {
		t.Error("GetRequestKind should return false for RESPONSE message type")
	}
}

func TestGetRequestKind_NilPayload(t *testing.T) {
	msg := NewAgentMsg(MsgTypeREQUEST, "pm-001", "architect")
	// No payload set

	_, ok := GetRequestKind(msg)
	if ok {
		t.Error("GetRequestKind should return false when payload is nil")
	}
}

func TestGetRequestKind_KnownKinds(t *testing.T) {
	tests := []struct {
		name         string
		payload      *MessagePayload
		expectedKind RequestKind
	}{
		{
			name: "question_request",
			payload: NewQuestionRequestPayload(&QuestionRequestPayload{
				Text: "How should I approach this?",
			}),
			expectedKind: RequestKindQuestion,
		},
		{
			name: "approval_request",
			payload: NewApprovalRequestPayload(&ApprovalRequestPayload{
				ApprovalType: ApprovalTypePlan,
				Content:      "implementation plan",
			}),
			expectedKind: RequestKindApproval,
		},
		{
			name: "merge_request",
			payload: NewMergeRequestPayload(&MergeRequestPayload{
				StoryID:    "story-1",
				BranchName: "feature/test",
			}),
			expectedKind: RequestKindMerge,
		},
		{
			name: "requeue_request",
			payload: NewRequeueRequestPayload(&RequeueRequestPayload{
				StoryID: "story-1",
				AgentID: "coder-001",
				Reason:  "error",
			}),
			expectedKind: RequestKindRequeue,
		},
		{
			name: "hotfix_request",
			payload: NewHotfixRequestPayload(&HotfixRequestPayload{
				Analysis: "urgent fix",
				Platform: "go",
			}),
			expectedKind: RequestKindHotfix,
		},
		{
			name: "incident_action",
			payload: NewIncidentActionPayload(&IncidentActionPayload{
				IncidentID: "inc-001",
				Action:     "resume",
				Reason:     "fixed",
			}),
			expectedKind: RequestKindExecution,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := NewAgentMsg(MsgTypeREQUEST, "sender", "receiver")
			msg.SetTypedPayload(tt.payload)

			kind, ok := GetRequestKind(msg)
			if !ok {
				t.Fatalf("GetRequestKind returned false for %s", tt.name)
			}
			if kind != tt.expectedKind {
				t.Errorf("expected %q, got %q", tt.expectedKind, kind)
			}
		})
	}
}

func TestGetResponseKind_KnownKinds(t *testing.T) {
	tests := []struct {
		name         string
		payload      *MessagePayload
		expectedKind ResponseKind
	}{
		{
			name: "question_response",
			payload: NewQuestionResponsePayload(&QuestionResponsePayload{
				AnswerText: "Do X then Y",
			}),
			expectedKind: ResponseKindQuestion,
		},
		{
			name: "approval_response",
			payload: NewApprovalResponsePayload(&ApprovalResult{
				Status: ApprovalStatusApproved,
			}),
			expectedKind: ResponseKindApproval,
		},
		{
			name: "merge_response",
			payload: NewMergeResponsePayload(&MergeResponsePayload{
				Status: "merged",
			}),
			expectedKind: ResponseKindMerge,
		},
		{
			name: "requeue_response",
			payload: NewRequeueResponsePayload(&RequeueResponsePayload{
				Accepted: true,
			}),
			expectedKind: ResponseKindRequeue,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := NewAgentMsg(MsgTypeRESPONSE, "sender", "receiver")
			msg.SetTypedPayload(tt.payload)

			kind, ok := GetResponseKind(msg)
			if !ok {
				t.Fatalf("GetResponseKind returned false for %s", tt.name)
			}
			if kind != tt.expectedKind {
				t.Errorf("expected %q, got %q", tt.expectedKind, kind)
			}
		})
	}
}
