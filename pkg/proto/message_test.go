package proto

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNewAgentMsg(t *testing.T) {
	msg := NewAgentMsg(MsgTypeSTORY, "architect", "claude")

	if msg.Type != MsgTypeSTORY {
		t.Errorf("Expected type STORY, got %s", msg.Type)
	}
	if msg.FromAgent != "architect" {
		t.Errorf("Expected from_agent 'architect', got %s", msg.FromAgent)
	}
	if msg.ToAgent != "claude" {
		t.Errorf("Expected to_agent 'claude', got %s", msg.ToAgent)
	}
	if msg.ID == "" {
		t.Error("Expected non-empty ID")
	}
	if msg.Timestamp.IsZero() {
		t.Error("Expected non-zero timestamp")
	}
	if msg.Payload == nil {
		t.Error("Expected initialized payload map")
	}
	if msg.Metadata == nil {
		t.Error("Expected initialized metadata map")
	}
}

func TestAgentMsg_ToJSON_FromJSON(t *testing.T) {
	original := NewAgentMsg(MsgTypeSTORY, "architect", "claude")
	original.SetPayload("story_id", "001")
	original.SetPayload("content", "Implement health endpoint")
	original.SetMetadata("priority", "high")
	original.RetryCount = 1
	original.ParentMsgID = "parent_123"

	// Test ToJSON.
	jsonData, err := original.ToJSON()
	if err != nil {
		t.Fatalf("Failed to convert to JSON: %v", err)
	}

	// Test FromJSON.
	var restored AgentMsg
	err = restored.FromJSON(jsonData)
	if err != nil {
		t.Fatalf("Failed to restore from JSON: %v", err)
	}

	// Compare fields.
	if restored.ID != original.ID {
		t.Errorf("ID mismatch: expected %s, got %s", original.ID, restored.ID)
	}
	if restored.Type != original.Type {
		t.Errorf("Type mismatch: expected %s, got %s", original.Type, restored.Type)
	}
	if restored.FromAgent != original.FromAgent {
		t.Errorf("FromAgent mismatch: expected %s, got %s", original.FromAgent, restored.FromAgent)
	}
	if restored.ToAgent != original.ToAgent {
		t.Errorf("ToAgent mismatch: expected %s, got %s", original.ToAgent, restored.ToAgent)
	}
	if restored.RetryCount != original.RetryCount {
		t.Errorf("RetryCount mismatch: expected %d, got %d", original.RetryCount, restored.RetryCount)
	}
	if restored.ParentMsgID != original.ParentMsgID {
		t.Errorf("ParentMsgID mismatch: expected %s, got %s", original.ParentMsgID, restored.ParentMsgID)
	}

	// Test payload.
	storyID, exists := restored.GetPayload("story_id")
	if !exists || storyID != "001" {
		t.Errorf("Payload story_id mismatch: expected '001', got %v", storyID)
	}

	content, exists := restored.GetPayload("content")
	if !exists || content != "Implement health endpoint" {
		t.Errorf("Payload content mismatch: expected 'Implement health endpoint', got %v", content)
	}

	// Test metadata.
	priority, exists := restored.GetMetadata("priority")
	if !exists || priority != "high" {
		t.Errorf("Metadata priority mismatch: expected 'high', got %s", priority)
	}
}

func TestFromJSON(t *testing.T) {
	jsonStr := `{
		"id": "msg_123",
		"type": "RESULT",
		"from_agent": "claude",
		"to_agent": "architect",
		"timestamp": "2025-06-09T10:00:00Z",
		"payload": {
			"status": "success",
			"code": "fmt.Println(\"Hello\")"
		},
		"metadata": {
			"duration": "2.5s"
		},
		"retry_count": 0
	}`

	msg, err := FromJSON([]byte(jsonStr))
	if err != nil {
		t.Fatalf("Failed to parse JSON: %v", err)
	}

	if msg.ID != "msg_123" {
		t.Errorf("Expected ID 'msg_123', got %s", msg.ID)
	}
	if msg.Type != MsgTypeRESULT {
		t.Errorf("Expected type RESULT, got %s", msg.Type)
	}

	status, exists := msg.GetPayload("status")
	if !exists || status != "success" {
		t.Errorf("Expected payload status 'success', got %v", status)
	}

	duration, exists := msg.GetMetadata("duration")
	if !exists || duration != "2.5s" {
		t.Errorf("Expected metadata duration '2.5s', got %s", duration)
	}
}

func TestAgentMsg_SetGetPayload(t *testing.T) {
	msg := NewAgentMsg(MsgTypeSTORY, "test", "test")

	// Test setting and getting payload.
	msg.SetPayload("key1", "value1")
	msg.SetPayload("key2", 42)
	msg.SetPayload("key3", true)

	val1, exists := msg.GetPayload("key1")
	if !exists || val1 != "value1" {
		t.Errorf("Expected payload key1 'value1', got %v", val1)
	}

	val2, exists := msg.GetPayload("key2")
	if !exists || val2 != 42 {
		t.Errorf("Expected payload key2 42, got %v", val2)
	}

	val3, exists := msg.GetPayload("key3")
	if !exists || val3 != true {
		t.Errorf("Expected payload key3 true, got %v", val3)
	}

	// Test non-existent key.
	_, exists = msg.GetPayload("nonexistent")
	if exists {
		t.Error("Expected non-existent key to return false")
	}
}

func TestAgentMsg_SetGetMetadata(t *testing.T) {
	msg := NewAgentMsg(MsgTypeSTORY, "test", "test")

	// Test setting and getting metadata.
	msg.SetMetadata("env", "production")
	msg.SetMetadata("version", "1.0.0")

	env, exists := msg.GetMetadata("env")
	if !exists || env != "production" {
		t.Errorf("Expected metadata env 'production', got %s", env)
	}

	version, exists := msg.GetMetadata("version")
	if !exists || version != "1.0.0" {
		t.Errorf("Expected metadata version '1.0.0', got %s", version)
	}

	// Test non-existent key.
	_, exists = msg.GetMetadata("nonexistent")
	if exists {
		t.Error("Expected non-existent key to return false")
	}
}

func TestAgentMsg_Clone(t *testing.T) {
	original := NewAgentMsg(MsgTypeSTORY, "architect", "claude")
	original.SetPayload("key", "value")
	original.SetMetadata("meta", "data")
	original.RetryCount = 2
	original.ParentMsgID = "parent_456"

	clone := original.Clone()

	// Test that clone has same values.
	if clone.ID != original.ID {
		t.Errorf("Clone ID mismatch: expected %s, got %s", original.ID, clone.ID)
	}
	if clone.Type != original.Type {
		t.Errorf("Clone Type mismatch: expected %s, got %s", original.Type, clone.Type)
	}
	if clone.RetryCount != original.RetryCount {
		t.Errorf("Clone RetryCount mismatch: expected %d, got %d", original.RetryCount, clone.RetryCount)
	}
	if clone.ParentMsgID != original.ParentMsgID {
		t.Errorf("Clone ParentMsgID mismatch: expected %s, got %s", original.ParentMsgID, clone.ParentMsgID)
	}

	// Test payload clone.
	val, exists := clone.GetPayload("key")
	if !exists || val != "value" {
		t.Errorf("Clone payload mismatch: expected 'value', got %v", val)
	}

	// Test metadata clone.
	meta, exists := clone.GetMetadata("meta")
	if !exists || meta != "data" {
		t.Errorf("Clone metadata mismatch: expected 'data', got %s", meta)
	}

	// Test that modifying clone doesn't affect original.
	clone.SetPayload("key", "modified")
	originalVal, _ := original.GetPayload("key")
	if originalVal != "value" {
		t.Error("Modifying clone affected original payload")
	}
}

func TestAgentMsg_Validate(t *testing.T) {
	tests := []struct {
		name      string
		setupMsg  func() *AgentMsg
		wantError bool
	}{
		{
			name: "valid message",
			setupMsg: func() *AgentMsg {
				return NewAgentMsg(MsgTypeSTORY, "architect", "claude")
			},
			wantError: false,
		},
		{
			name: "missing ID",
			setupMsg: func() *AgentMsg {
				msg := NewAgentMsg(MsgTypeSTORY, "architect", "claude")
				msg.ID = ""
				return msg
			},
			wantError: true,
		},
		{
			name: "missing type",
			setupMsg: func() *AgentMsg {
				msg := NewAgentMsg(MsgTypeSTORY, "architect", "claude")
				msg.Type = ""
				return msg
			},
			wantError: true,
		},
		{
			name: "missing from_agent",
			setupMsg: func() *AgentMsg {
				msg := NewAgentMsg(MsgTypeSTORY, "architect", "claude")
				msg.FromAgent = ""
				return msg
			},
			wantError: true,
		},
		{
			name: "missing to_agent",
			setupMsg: func() *AgentMsg {
				msg := NewAgentMsg(MsgTypeSTORY, "architect", "claude")
				msg.ToAgent = ""
				return msg
			},
			wantError: true,
		},
		{
			name: "zero timestamp",
			setupMsg: func() *AgentMsg {
				msg := NewAgentMsg(MsgTypeSTORY, "architect", "claude")
				msg.Timestamp = time.Time{}
				return msg
			},
			wantError: true,
		},
		{
			name: "invalid message type",
			setupMsg: func() *AgentMsg {
				msg := NewAgentMsg(MsgTypeSTORY, "architect", "claude")
				msg.Type = "INVALID"
				return msg
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := tt.setupMsg()
			err := msg.Validate()

			if tt.wantError && err == nil {
				t.Error("Expected validation error but got none")
			}
			if !tt.wantError && err != nil {
				t.Errorf("Expected no validation error but got: %v", err)
			}
		})
	}
}

func TestMsgType_Constants(t *testing.T) {
	// Test that all message types are defined correctly.
	expectedTypes := []MsgType{
		MsgTypeSTORY,
		MsgTypeRESULT,
		MsgTypeERROR,
		MsgTypeQUESTION,
		MsgTypeSHUTDOWN,
	}

	expectedValues := []string{
		"STORY",
		"RESULT",
		"ERROR",
		"QUESTION",
		"SHUTDOWN",
	}

	for i, msgType := range expectedTypes {
		if string(msgType) != expectedValues[i] {
			t.Errorf("Expected message type %s, got %s", expectedValues[i], string(msgType))
		}
	}
}

func TestAgentMsg_JSONRoundTrip(t *testing.T) {
	// Test all message types.
	msgTypes := []MsgType{
		MsgTypeSTORY,
		MsgTypeRESULT,
		MsgTypeERROR,
		MsgTypeQUESTION,
		MsgTypeSHUTDOWN,
	}

	for _, msgType := range msgTypes {
		t.Run(string(msgType), func(t *testing.T) {
			original := NewAgentMsg(msgType, "test_from", "test_to")
			original.SetPayload("test_key", "test_value")
			original.SetMetadata("test_meta", "test_meta_value")

			// Convert to JSON.
			jsonData, err := original.ToJSON()
			if err != nil {
				t.Fatalf("Failed to convert to JSON: %v", err)
			}

			// Ensure JSON is valid.
			var jsonCheck map[string]any
			if err := json.Unmarshal(jsonData, &jsonCheck); err != nil {
				t.Fatalf("Generated invalid JSON: %v", err)
			}

			// Convert back from JSON.
			restored, err := FromJSON(jsonData)
			if err != nil {
				t.Fatalf("Failed to restore from JSON: %v", err)
			}

			// Validate restored message.
			if err := restored.Validate(); err != nil {
				t.Fatalf("Restored message is invalid: %v", err)
			}

			// Check type preserved.
			if restored.Type != msgType {
				t.Errorf("Message type not preserved: expected %s, got %s", msgType, restored.Type)
			}
		})
	}
}

func TestGenerateID(t *testing.T) {
	// Test that generateID creates non-empty, unique IDs.
	id1 := generateID()
	id2 := generateID()

	if id1 == "" {
		t.Error("generateID returned empty string")
	}
	if id2 == "" {
		t.Error("generateID returned empty string")
	}
	if id1 == id2 {
		t.Error("generateID returned duplicate IDs")
	}

	// Test that ID has expected prefix.
	if !strings.HasPrefix(id1, "msg_") {
		t.Errorf("Expected ID to start with 'msg_', got %s", id1)
	}
}

// TestAutoAction tests the BUDGET_REVIEW command types for inter-agent communication.
func TestAutoAction(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expected    AutoAction
		expectError bool
	}{
		{"valid CONTINUE", "CONTINUE", AutoContinue, false},
		{"valid PIVOT", "PIVOT", AutoPivot, false},
		{"valid ESCALATE", "ESCALATE", AutoEscalate, false},
		{"valid ABANDON", "ABANDON", AutoAbandon, false},
		{"invalid command", "INVALID", "", true},
		{"empty string", "", "", true},
		{"lowercase", "continue", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseAutoAction(tt.input)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error for input %q but got none", tt.input)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error for input %q: %v", tt.input, err)
				}
				if result != tt.expected {
					t.Errorf("Expected %q but got %q", tt.expected, result)
				}
				if result.String() != string(tt.expected) {
					t.Errorf("String() method failed: expected %q but got %q", tt.expected, result.String())
				}
			}
		})
	}
}

// TestQuestionReasonBudgetReview tests the BUDGET_REVIEW question reason constant.
func TestQuestionReasonBudgetReview(t *testing.T) {
	if QuestionReasonBudgetReview != "BUDGET_REVIEW" {
		t.Errorf("Expected QuestionReasonBudgetReview to be 'BUDGET_REVIEW', got %q", QuestionReasonBudgetReview)
	}
}

// TestEnumParsing tests the new enum parsing functions.
func TestEnumParsing(t *testing.T) {
	// Test ParseMsgType.
	t.Run("ParseMsgType", func(t *testing.T) {
		tests := []struct {
			input    string
			expected MsgType
			hasError bool
		}{
			{"story", MsgTypeSTORY, false},
			{"STORY", MsgTypeSTORY, false},
			{"request", MsgTypeREQUEST, false},
			{"requeue", MsgTypeREQUEUE, false},
			{"REQUEUE", MsgTypeREQUEUE, false},
			{"invalid", "", true},
		}

		for _, tt := range tests {
			result, err := ParseMsgType(tt.input)
			if tt.hasError {
				if err == nil {
					t.Errorf("ParseMsgType(%q) expected error but got none", tt.input)
				}
			} else {
				if err != nil {
					t.Errorf("ParseMsgType(%q) unexpected error: %v", tt.input, err)
				}
				if result != tt.expected {
					t.Errorf("ParseMsgType(%q) = %q, expected %q", tt.input, result, tt.expected)
				}
			}
		}
	})

	// Test ParseApprovalStatus.
	t.Run("ParseApprovalStatus", func(t *testing.T) {
		tests := []struct {
			input    string
			expected ApprovalStatus
			hasError bool
		}{
			{"approved", ApprovalStatusApproved, false},
			{"APPROVED", ApprovalStatusApproved, false},
			{"needs_changes", ApprovalStatusNeedsChanges, false},
			{"NEEDS_FIXES", ApprovalStatusNeedsChanges, false},
			{"invalid", "", true},
		}

		for _, tt := range tests {
			result, err := ParseApprovalStatus(tt.input)
			if tt.hasError {
				if err == nil {
					t.Errorf("ParseApprovalStatus(%q) expected error but got none", tt.input)
				}
			} else {
				if err != nil {
					t.Errorf("ParseApprovalStatus(%q) unexpected error: %v", tt.input, err)
				}
				if result != tt.expected {
					t.Errorf("ParseApprovalStatus(%q) = %q, expected %q", tt.input, result, tt.expected)
				}
			}
		}
	})

	// Test ParseApprovalType.
	t.Run("ParseApprovalType", func(t *testing.T) {
		tests := []struct {
			input    string
			expected ApprovalType
			hasError bool
		}{
			{"plan", ApprovalTypePlan, false},
			{"PLAN", ApprovalTypePlan, false},
			{"code", ApprovalTypeCode, false},
			{"budget_review", ApprovalTypeBudgetReview, false},
			{"completion", ApprovalTypeCompletion, false},
			{"invalid", "", true},
		}

		for _, tt := range tests {
			result, err := ParseApprovalType(tt.input)
			if tt.hasError {
				if err == nil {
					t.Errorf("ParseApprovalType(%q) expected error but got none", tt.input)
				}
			} else {
				if err != nil {
					t.Errorf("ParseApprovalType(%q) unexpected error: %v", tt.input, err)
				}
				if result != tt.expected {
					t.Errorf("ParseApprovalType(%q) = %q, expected %q", tt.input, result, tt.expected)
				}
			}
		}
	})
}

// TestSafeExtractFromPayload tests the generic enum extraction utility.
func TestSafeExtractFromPayload(t *testing.T) {
	msg := NewAgentMsg(MsgTypeREQUEST, "test", "test")

	// Test successful extraction.
	msg.SetPayload("approval_type", "plan")
	result, err := SafeExtractFromPayload(msg, "approval_type", ParseApprovalType)
	if err != nil {
		t.Errorf("SafeExtractFromPayload unexpected error: %v", err)
	}
	if result != ApprovalTypePlan {
		t.Errorf("SafeExtractFromPayload = %q, expected %q", result, ApprovalTypePlan)
	}

	// Test missing key.
	_, err = SafeExtractFromPayload(msg, "missing_key", ParseApprovalType)
	if err == nil {
		t.Error("SafeExtractFromPayload should fail for missing key")
	}

	// Test invalid value.
	msg.SetPayload("invalid_type", "invalid")
	_, err = SafeExtractFromPayload(msg, "invalid_type", ParseApprovalType)
	if err == nil {
		t.Error("SafeExtractFromPayload should fail for invalid enum value")
	}
}

// TestCorrelationIDGeneration tests the correlation ID generation functions.
func TestCorrelationIDGeneration(t *testing.T) {
	// Test that all generators produce unique, non-empty IDs.
	questionID1 := GenerateQuestionID()
	questionID2 := GenerateQuestionID()
	approvalID1 := GenerateApprovalID()
	approvalID2 := GenerateApprovalID()
	correlationID1 := GenerateCorrelationID()
	correlationID2 := GenerateCorrelationID()

	// Check all IDs are non-empty.
	ids := []string{questionID1, questionID2, approvalID1, approvalID2, correlationID1, correlationID2}
	for i, id := range ids {
		if id == "" {
			t.Errorf("ID %d is empty", i)
		}
	}

	// Check all IDs are unique.
	seen := make(map[string]bool)
	for i, id := range ids {
		if seen[id] {
			t.Errorf("Duplicate ID found at index %d: %s", i, id)
		}
		seen[id] = true
	}

	// Check ID prefixes.
	if !strings.HasPrefix(questionID1, "q_") {
		t.Errorf("Question ID should start with 'q_', got %s", questionID1)
	}
	if !strings.HasPrefix(approvalID1, "a_") {
		t.Errorf("Approval ID should start with 'a_', got %s", approvalID1)
	}
	if !strings.HasPrefix(correlationID1, "c_") {
		t.Errorf("Correlation ID should start with 'c_', got %s", correlationID1)
	}
}

// TestCorrelationHelpers tests the correlation helper methods on AgentMsg.
func TestCorrelationHelpers(t *testing.T) {
	msg := NewAgentMsg(MsgTypeQUESTION, "coder", "architect")

	// Test question correlation.
	questionID := GenerateQuestionID()
	msg.SetQuestionCorrelation(questionID)

	retrievedQuestionID, exists := msg.GetQuestionID()
	if !exists {
		t.Error("Question ID should exist after setting")
	}
	if retrievedQuestionID != questionID {
		t.Errorf("Expected question ID %s, got %s", questionID, retrievedQuestionID)
	}

	retrievedCorrelationID, exists := msg.GetCorrelationID()
	if !exists {
		t.Error("Correlation ID should exist after setting question correlation")
	}
	if retrievedCorrelationID != questionID {
		t.Errorf("Expected correlation ID to match question ID %s, got %s", questionID, retrievedCorrelationID)
	}

	// Test approval correlation.
	msg2 := NewAgentMsg(MsgTypeREQUEST, "coder", "architect")
	approvalID := GenerateApprovalID()
	msg2.SetApprovalCorrelation(approvalID)

	retrievedApprovalID, exists := msg2.GetApprovalID()
	if !exists {
		t.Error("Approval ID should exist after setting")
	}
	if retrievedApprovalID != approvalID {
		t.Errorf("Expected approval ID %s, got %s", approvalID, retrievedApprovalID)
	}

	retrievedCorrelationID2, exists := msg2.GetCorrelationID()
	if !exists {
		t.Error("Correlation ID should exist after setting approval correlation")
	}
	if retrievedCorrelationID2 != approvalID {
		t.Errorf("Expected correlation ID to match approval ID %s, got %s", approvalID, retrievedCorrelationID2)
	}

	// Test missing IDs.
	msg3 := NewAgentMsg(MsgTypeSTORY, "architect", "coder")
	if _, exists := msg3.GetQuestionID(); exists {
		t.Error("Should not have question ID when none set")
	}
	if _, exists := msg3.GetApprovalID(); exists {
		t.Error("Should not have approval ID when none set")
	}
	if _, exists := msg3.GetCorrelationID(); exists {
		t.Error("Should not have correlation ID when none set")
	}
}
