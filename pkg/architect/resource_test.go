package architect

import (
	"context"
	"testing"
	"time"

	"orchestrator/pkg/proto"
	"orchestrator/pkg/state"
	"orchestrator/pkg/templates"
)

func TestResourceApprovalHandling(t *testing.T) {
	// Create test setup
	stateStore, _ := state.NewStore("test_data")
	driver := NewDriver("test-architect", stateStore, "test_work", "test_stories")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := driver.Initialize(ctx)
	if err != nil {
		t.Fatalf("Failed to initialize driver: %v", err)
	}
	defer driver.Shutdown()

	t.Run("Valid resource request approval", func(t *testing.T) {
		// Create a resource request message
		resourceMsg := proto.NewAgentMsg(proto.MsgTypeREQUEST, "test-coder", "test-architect")
		resourceMsg.SetPayload(proto.KeyRequestType, string(proto.RequestResource))
		resourceMsg.SetPayload(proto.KeyRequestedTokens, 5000)
		resourceMsg.SetPayload(proto.KeyRequestedIterations, 10)
		resourceMsg.SetPayload(proto.KeyJustification, "Need additional tokens for complex feature implementation")
		resourceMsg.SetPayload(proto.KeyStoryID, "test-001")

		// Route the message
		err := driver.RouteMessage(resourceMsg)
		if err != nil {
			t.Errorf("Failed to route resource request message: %v", err)
		}

		// Wait for completion
		select {
		case msgID := <-driver.requestDoneCh:
			if msgID != resourceMsg.ID {
				t.Errorf("Expected message ID %s, got %s", resourceMsg.ID, msgID)
			}
			t.Logf("Resource request %s completed successfully", msgID)
		case <-time.After(2 * time.Second):
			t.Error("Timeout waiting for resource request completion")
		}
	})

	t.Run("Resource request rejection - excessive tokens", func(t *testing.T) {
		// Create a resource request with excessive tokens
		resourceMsg := proto.NewAgentMsg(proto.MsgTypeREQUEST, "test-coder", "test-architect")
		resourceMsg.SetPayload(proto.KeyRequestType, string(proto.RequestResource))
		resourceMsg.SetPayload(proto.KeyRequestedTokens, 15000) // Over limit
		resourceMsg.SetPayload(proto.KeyRequestedIterations, 5)
		resourceMsg.SetPayload(proto.KeyJustification, "Need many tokens")
		resourceMsg.SetPayload(proto.KeyStoryID, "test-002")

		// Route the message
		err := driver.RouteMessage(resourceMsg)
		if err != nil {
			t.Errorf("Failed to route resource request message: %v", err)
		}

		// Wait for completion
		select {
		case msgID := <-driver.requestDoneCh:
			if msgID != resourceMsg.ID {
				t.Errorf("Expected message ID %s, got %s", resourceMsg.ID, msgID)
			}
			t.Logf("Resource request %s completed (expected rejection)", msgID)
		case <-time.After(2 * time.Second):
			t.Error("Timeout waiting for resource request completion")
		}
	})

	t.Run("Resource request rejection - no justification", func(t *testing.T) {
		// Create a resource request without justification
		resourceMsg := proto.NewAgentMsg(proto.MsgTypeREQUEST, "test-coder", "test-architect")
		resourceMsg.SetPayload(proto.KeyRequestType, string(proto.RequestResource))
		resourceMsg.SetPayload(proto.KeyRequestedTokens, 2000)
		resourceMsg.SetPayload(proto.KeyRequestedIterations, 3)
		// No justification provided
		resourceMsg.SetPayload(proto.KeyStoryID, "test-003")

		// Route the message
		err := driver.RouteMessage(resourceMsg)
		if err != nil {
			t.Errorf("Failed to route resource request message: %v", err)
		}

		// Wait for completion
		select {
		case msgID := <-driver.requestDoneCh:
			if msgID != resourceMsg.ID {
				t.Errorf("Expected message ID %s, got %s", resourceMsg.ID, msgID)
			}
			t.Logf("Resource request %s completed (expected rejection)", msgID)
		case <-time.After(2 * time.Second):
			t.Error("Timeout waiting for resource request completion")
		}
	})
}

func TestResourceRequestWorkerDirect(t *testing.T) {
	// Test the RequestWorker resource handling directly
	renderer, _ := templates.NewRenderer()
	requestCh := make(chan *proto.AgentMsg, 1)
	requestDoneCh := make(chan string, 1)
	mockDispatcher := NewMockDispatcher()

	worker := NewRequestWorker(
		nil, // nil LLM client for mock mode
		renderer,
		requestCh,
		requestDoneCh,
		mockDispatcher,
		"test-architect",
		nil, // nil queue for this test
		nil, // nil merge channel
	)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Start worker in background
	go worker.Run(ctx)

	t.Run("Process valid resource request", func(t *testing.T) {
		// Create resource request message
		resourceMsg := proto.NewAgentMsg(proto.MsgTypeREQUEST, "test-coder", "test-architect")
		resourceMsg.SetPayload(proto.KeyRequestType, string(proto.RequestResource))
		resourceMsg.SetPayload(proto.KeyRequestedTokens, 3000)
		resourceMsg.SetPayload(proto.KeyRequestedIterations, 5)
		resourceMsg.SetPayload(proto.KeyJustification, "Complex algorithm needs more tokens")
		resourceMsg.SetPayload(proto.KeyStoryID, "story-001")

		// Send to worker
		requestCh <- resourceMsg

		// Wait for completion
		select {
		case msgID := <-requestDoneCh:
			if msgID != resourceMsg.ID {
				t.Errorf("Expected message ID %s, got %s", resourceMsg.ID, msgID)
			}
		case <-time.After(1 * time.Second):
			t.Error("Timeout waiting for resource request processing")
		}

		// Check that approval message was sent
		messages := mockDispatcher.GetSentMessages()
		if len(messages) != 1 {
			t.Fatalf("Expected 1 sent message, got %d", len(messages))
		}

		resultMsg := messages[0]
		if resultMsg.Type != proto.MsgTypeRESULT {
			t.Errorf("Expected RESULT message, got %s", resultMsg.Type)
		}

		if resultMsg.ToAgent != "test-coder" {
			t.Errorf("Expected message to test-coder, got %s", resultMsg.ToAgent)
		}

		// Check approval result
		approved, exists := resultMsg.GetPayload("approved")
		if !exists {
			t.Error("No approval payload in result message")
		}
		if !approved.(bool) {
			t.Error("Expected resource request to be approved")
		}

		// Check approved tokens
		approvedTokens, exists := resultMsg.GetPayload("approved_tokens")
		if !exists {
			t.Error("No approved_tokens payload in result message")
		}
		if approvedTokens.(int) != 3000 {
			t.Errorf("Expected 3000 approved tokens, got %d", approvedTokens.(int))
		}

		// Check metadata
		approvalType, exists := resultMsg.GetMetadata("approval_type")
		if !exists || approvalType != "resource" {
			t.Errorf("Expected approval_type metadata 'resource', got %s", approvalType)
		}
	})

	t.Run("Process rejected resource request", func(t *testing.T) {
		// Clear previous messages
		mockDispatcher.sentMessages = nil

		// Create resource request with excessive iterations
		resourceMsg := proto.NewAgentMsg(proto.MsgTypeREQUEST, "test-coder-2", "test-architect")
		resourceMsg.SetPayload(proto.KeyRequestType, string(proto.RequestResource))
		resourceMsg.SetPayload(proto.KeyRequestedTokens, 1000)
		resourceMsg.SetPayload(proto.KeyRequestedIterations, 100) // Over limit
		resourceMsg.SetPayload(proto.KeyJustification, "Need many iterations")
		resourceMsg.SetPayload(proto.KeyStoryID, "story-002")

		// Send to worker
		requestCh <- resourceMsg

		// Wait for completion
		select {
		case msgID := <-requestDoneCh:
			if msgID != resourceMsg.ID {
				t.Errorf("Expected message ID %s, got %s", resourceMsg.ID, msgID)
			}
		case <-time.After(1 * time.Second):
			t.Error("Timeout waiting for resource request processing")
		}

		// Check that rejection message was sent
		messages := mockDispatcher.GetSentMessages()
		if len(messages) != 1 {
			t.Fatalf("Expected 1 sent message, got %d", len(messages))
		}

		resultMsg := messages[0]

		// Check rejection result
		approved, exists := resultMsg.GetPayload("approved")
		if !exists {
			t.Error("No approval payload in result message")
		}
		if approved.(bool) {
			t.Error("Expected resource request to be rejected")
		}

		// Check status
		status, exists := resultMsg.GetPayload(proto.KeyStatus)
		if !exists {
			t.Error("No status payload in result message")
		}
		if status.(string) != string(proto.ApprovalStatusRejected) {
			t.Errorf("Expected status %s, got %s", proto.ApprovalStatusRejected, status)
		}

		// Check that approved amounts are 0
		approvedTokens, exists := resultMsg.GetPayload("approved_tokens")
		if !exists {
			t.Error("No approved_tokens payload in result message")
		}
		if approvedTokens.(int) != 0 {
			t.Errorf("Expected 0 approved tokens for rejection, got %d", approvedTokens.(int))
		}
	})
}
