package coder

import (
	"context"
	"testing"

	"orchestrator/pkg/config"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/state"
)

func TestRobustApprovalMessageHandling(t *testing.T) {
	// Test Case 1: Invalid approval type should return error
	t.Run("InvalidApprovalType", func(t *testing.T) {
		// Create minimal coder just for testing message parsing logic
		tempDir := t.TempDir()
		stateStore, _ := state.NewStore(tempDir)
		coder, _ := NewCoder("test-coder", "test-coder", tempDir, stateStore, &config.ModelCfg{})
		
		resultMsg := proto.NewAgentMsg(proto.MsgTypeRESULT, "architect", "test-coder")
		resultMsg.SetPayload(proto.KeyStatus, "APPROVED")
		resultMsg.SetPayload(proto.KeyRequestType, proto.RequestApproval.String())
		resultMsg.SetPayload(proto.KeyApprovalType, "unknown")

		_, err := coder.handleResultMessage(context.Background(), resultMsg)
		if err == nil {
			t.Error("Should return error for invalid approval type")
		}
		if err != nil && err.Error() != "invalid approval type 'unknown': unknown approval type: unknown" {
			t.Errorf("Expected specific error message, got: %v", err)
		}
	})

	// Test Case 2: Missing approval type should return error
	t.Run("MissingApprovalType", func(t *testing.T) {
		tempDir := t.TempDir()
		stateStore, _ := state.NewStore(tempDir)
		coder, _ := NewCoder("test-coder", "test-coder", tempDir, stateStore, &config.ModelCfg{})
		
		resultMsg := proto.NewAgentMsg(proto.MsgTypeRESULT, "architect", "test-coder")
		resultMsg.SetPayload(proto.KeyStatus, "APPROVED")
		resultMsg.SetPayload(proto.KeyRequestType, proto.RequestApproval.String())
		// No approval_type set

		_, err := coder.handleResultMessage(context.Background(), resultMsg)
		if err == nil {
			t.Error("Should return error for missing approval type")
		}
		if err != nil && err.Error() != "missing approval_type in approval result message" {
			t.Errorf("Expected specific error message, got: %v", err)
		}
	})
}

func TestNormaliseApprovalType(t *testing.T) {
	testCases := []struct {
		input    string
		expected proto.ApprovalType
		hasError bool
	}{
		{"plan", proto.ApprovalTypePlan, false},
		{"Plan", proto.ApprovalTypePlan, false},
		{"PLAN", proto.ApprovalTypePlan, false},
		{"code", proto.ApprovalTypeCode, false},
		{"Code", proto.ApprovalTypeCode, false},
		{"CODE", proto.ApprovalTypeCode, false},
		{"unknown", "", true},
		{"", "", true},
		{"review", "", true}, // Any other value should fail
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			result, err := proto.NormaliseApprovalType(tc.input)
			
			if tc.hasError {
				if err == nil {
					t.Errorf("Expected error for input '%s', got none", tc.input)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error for input '%s': %v", tc.input, err)
				}
				if result != tc.expected {
					t.Errorf("Expected '%s', got '%s'", tc.expected, result)
				}
			}
		})
	}
}

func TestGetStringFromPayloadOrMetadata(t *testing.T) {
	// Create a temporary coder for testing
	tempDir := t.TempDir()
	stateStore, _ := state.NewStore(tempDir)
	coder, _ := NewCoder("test", "test", tempDir, stateStore, &config.ModelCfg{})

	msg := proto.NewAgentMsg(proto.MsgTypeTASK, "sender", "receiver")
	
	// Test payload preference over metadata
	msg.SetPayload("test_key", "payload_value")
	msg.SetMetadata("test_key", "metadata_value")
	
	result := coder.getStringFromPayloadOrMetadata(msg, "test_key")
	if result != "payload_value" {
		t.Errorf("Expected payload value, got %s", result)
	}
	
	// Test metadata fallback
	msg2 := proto.NewAgentMsg(proto.MsgTypeTASK, "sender", "receiver")
	msg2.SetMetadata("test_key", "metadata_value")
	
	result2 := coder.getStringFromPayloadOrMetadata(msg2, "test_key")
	if result2 != "metadata_value" {
		t.Errorf("Expected metadata value, got %s", result2)
	}
	
	// Test missing key
	result3 := coder.getStringFromPayloadOrMetadata(msg2, "missing_key")
	if result3 != "" {
		t.Errorf("Expected empty string for missing key, got %s", result3)
	}
}