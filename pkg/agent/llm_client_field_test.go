package agent_test

import (
	"context"
	"testing"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/proto"
)

// TestLLMClientFieldAccess verifies that LLMClient field is properly accessible
// through embedded BaseStateMachine without shadowing issues.
func TestLLMClientFieldAccess(t *testing.T) {
	// Create a base state machine
	sm := agent.NewBaseStateMachine("test-agent", proto.StateWaiting, nil, nil)

	// Create a mock LLM client
	mockClient := &mockLLMClient{}

	// Set via SetLLMClient method
	sm.SetLLMClient(mockClient)

	// Verify it's accessible via GetLLMClient
	retrieved := sm.GetLLMClient()
	if retrieved != mockClient {
		t.Errorf("GetLLMClient returned different client: got %v, want %v", retrieved, mockClient)
	}

	// Verify it's accessible via direct field access (now exported)
	if sm.LLMClient != mockClient {
		t.Errorf("Direct field access returned different client: got %v, want %v", sm.LLMClient, mockClient)
	}
}

// mockLLMClient is a minimal mock for testing.
type mockLLMClient struct{}

func (m *mockLLMClient) Complete(_ context.Context, _ agent.CompletionRequest) (agent.CompletionResponse, error) {
	return agent.CompletionResponse{}, nil
}

func (m *mockLLMClient) Stream(_ context.Context, _ agent.CompletionRequest) (<-chan agent.StreamChunk, error) {
	ch := make(chan agent.StreamChunk)
	close(ch)
	return ch, nil
}

func (m *mockLLMClient) GetModelName() string {
	return "mock-model"
}
