package architect

import (
	"context"
	"testing"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/state"
)

func TestResourceRequestCreation(t *testing.T) {
	// Create test setup
	stateStore, _ := state.NewStore("test_data")
	mockConfig := &config.ModelCfg{}
	mockLLM := &mockLLMClient{}
	mockOrchestratorConfig := &config.Config{}
	driver := NewDriver("test-architect", stateStore, mockConfig, mockLLM, nil, "test_work", "test_stories", mockOrchestratorConfig)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := driver.Initialize(ctx)
	if err != nil {
		t.Fatalf("Failed to initialize driver: %v", err)
	}
	defer driver.Shutdown(ctx)

	t.Run("Valid resource request message creation", func(t *testing.T) {
		// Create a resource request message
		resourceMsg := proto.NewAgentMsg(proto.MsgTypeREQUEST, "test-coder", "test-architect")
		resourceMsg.SetPayload(proto.KeyRequestType, string(proto.RequestResource))
		resourceMsg.SetPayload(proto.KeyRequestedTokens, 5000)
		resourceMsg.SetPayload(proto.KeyRequestedIterations, 10)
		resourceMsg.SetPayload(proto.KeyJustification, "Need additional tokens for complex feature implementation")
		resourceMsg.SetPayload(proto.KeyStoryID, "test-001")

		// Test message creation
		if resourceMsg.Type != proto.MsgTypeREQUEST {
			t.Errorf("Expected message type REQUEST, got %s", resourceMsg.Type)
		}

		// Test payload extraction
		requestType, exists := resourceMsg.GetPayload(proto.KeyRequestType)
		if !exists || requestType != string(proto.RequestResource) {
			t.Error("Expected resource request type")
		}

		requestedTokens, exists := resourceMsg.GetPayload(proto.KeyRequestedTokens)
		if !exists || requestedTokens != 5000 {
			t.Errorf("Expected 5000 tokens, got %v", requestedTokens)
		}

		t.Logf("Resource request %s created successfully", resourceMsg.ID)
	})
}
