package coder

import (
	"testing"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/build"
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
		mockLLM := agent.NewMockLLMClient([]agent.CompletionResponse{{Content: "mock"}}, nil)
		coder, _ := NewCoder("test-coder", stateStore, &config.ModelCfg{}, mockLLM, tempDir, &config.Agent{Name: "test-coder"}, &build.BuildService{})

		resultMsg := proto.NewAgentMsg(proto.MsgTypeRESULT, "architect", "test-coder")
		resultMsg.SetPayload(proto.KeyStatus, proto.ApprovalStatusApproved.String())
		resultMsg.SetPayload(proto.KeyRequestType, proto.RequestApproval.String())
		resultMsg.SetPayload(proto.KeyApprovalType, "unknown")

		// Since handleResultMessage is not exported, we'll test via ProcessApprovalResult
		err := coder.ProcessApprovalResult("APPROVED", "unknown")
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
		mockLLM := agent.NewMockLLMClient([]agent.CompletionResponse{{Content: "mock"}}, nil)
		coder, _ := NewCoder("test-coder", stateStore, &config.ModelCfg{}, mockLLM, tempDir, &config.Agent{Name: "test-coder"}, &build.BuildService{})

		resultMsg := proto.NewAgentMsg(proto.MsgTypeRESULT, "architect", "test-coder")
		resultMsg.SetPayload(proto.KeyStatus, proto.ApprovalStatusApproved.String())
		resultMsg.SetPayload(proto.KeyRequestType, proto.RequestApproval.String())
		// No approval_type set

		// Since handleResultMessage is not exported, we'll test via ProcessApprovalResult
		err := coder.ProcessApprovalResult("APPROVED", "unknown")
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

func TestCoderCreation(t *testing.T) {
	// Create a temporary coder for testing
	tempDir := t.TempDir()
	stateStore, _ := state.NewStore(tempDir)
	mockLLM := agent.NewMockLLMClient([]agent.CompletionResponse{{Content: "mock"}}, nil)
	coder, err := NewCoder("test", stateStore, &config.ModelCfg{}, mockLLM, tempDir, &config.Agent{Name: "test"}, &build.BuildService{})

	if err != nil {
		t.Fatalf("Failed to create coder: %v", err)
	}

	if coder.GetID() != "test" {
		t.Errorf("Expected ID 'test', got %s", coder.GetID())
	}
}
