package pm

import (
	"context"
	"fmt"
	"testing"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/config"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/persistence"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
	"orchestrator/pkg/tools"
)

// mockLLMClient is a mock LLM client for testing.
type mockLLMClient struct{}

func (m *mockLLMClient) Complete(_ context.Context, _ llm.CompletionRequest) (llm.CompletionResponse, error) {
	return llm.CompletionResponse{
		Content:    "Mock response",
		StopReason: "end_turn",
	}, nil
}

func (m *mockLLMClient) Stream(_ context.Context, _ llm.CompletionRequest) (<-chan llm.StreamChunk, error) {
	ch := make(chan llm.StreamChunk, 1)
	close(ch)
	return ch, nil
}

func (m *mockLLMClient) GetModelName() string {
	return "mock-model"
}

// mockToolProvider is a mock tool provider for testing.
type mockToolProvider struct {
	tools map[string]tools.Tool
}

func (m *mockToolProvider) Get(name string) (tools.Tool, error) {
	tool, ok := m.tools[name]
	if !ok {
		return nil, fmt.Errorf("tool %s not found", name)
	}
	return tool, nil
}

func (m *mockToolProvider) List() []tools.ToolMeta {
	return nil
}

// mockSpecSubmitTool is a mock spec_submit tool for testing.
type mockSpecSubmitTool struct {
	returnSuccess bool
	returnErrors  []string
}

func (m *mockSpecSubmitTool) Name() string {
	return "spec_submit"
}

func (m *mockSpecSubmitTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name: "spec_submit",
	}
}

func (m *mockSpecSubmitTool) Exec(_ context.Context, args map[string]any) (any, error) {
	if m.returnSuccess {
		return map[string]any{
			"success":       true,
			"message":       "Specification validated and ready for submission",
			"summary":       "Test spec summary",
			"spec_markdown": args["markdown"],
			"send_request":  true,
			"request_type":  "spec_review",
		}, nil
	}

	return map[string]any{
		"success":           false,
		"validation_errors": m.returnErrors,
		"send_request":      false,
	}, nil
}

func (m *mockSpecSubmitTool) PromptDocumentation() string {
	return "Mock spec_submit tool"
}

// createTestDriver creates a PM driver for testing.
func createTestDriver(t *testing.T, toolProvider ToolProvider) *Driver {
	t.Helper()

	renderer, err := templates.NewRenderer()
	if err != nil {
		t.Fatalf("Failed to create renderer: %v", err)
	}

	contextManager := contextmgr.NewContextManagerWithModel("mock-model")
	cfg := &config.Config{
		Agents: &config.AgentConfig{
			MaxCoders: 4,
		},
	}
	dispatcher, err := dispatch.NewDispatcher(cfg)
	if err != nil {
		t.Fatalf("Failed to create dispatcher: %v", err)
	}
	persistenceChannel := make(chan *persistence.Request, 100)
	interviewRequestCh := make(chan *proto.AgentMsg, 10)

	driver := &Driver{
		pmID:               "pm-test-001",
		llmClient:          &mockLLMClient{},
		renderer:           renderer,
		contextManager:     contextManager,
		logger:             logx.NewLogger("pm-test"),
		dispatcher:         dispatcher,
		persistenceChannel: persistenceChannel,
		currentState:       StateWaiting,
		stateData:          make(map[string]any),
		interviewRequestCh: interviewRequestCh,
		workDir:            "/tmp/test-pm",
		toolProvider:       toolProvider,
	}

	// Attach driver to dispatcher
	dispatcher.Attach(driver)

	return driver
}

// TestHandleWaitingReceivesArchitectFeedback tests WAITING state receives RESULT from architect.
func TestHandleWaitingReceivesArchitectFeedback(t *testing.T) {
	driver := createTestDriver(t, nil)

	// Test: Architect sends APPROVED result
	t.Run("Architect approves spec", func(t *testing.T) {
		driver.currentState = StateWaiting
		driver.stateData["pending_request_id"] = "request-123"

		// Create RESPONSE message with APPROVED status
		resultMsg := proto.NewAgentMsg(proto.MsgTypeRESPONSE, "architect-001", "pm-test-001")
		approvalResult := &proto.ApprovalResult{
			ID:         proto.GenerateApprovalID(),
			Type:       proto.ApprovalTypeSpec,
			Status:     proto.ApprovalStatusApproved,
			Feedback:   "Spec looks good",
			ReviewedBy: "architect-001",
			ReviewedAt: time.Now(),
		}
		resultMsg.SetTypedPayload(proto.NewApprovalResponsePayload(approvalResult))

		// Process result
		nextState, err := driver.handleArchitectResult(resultMsg)
		if err != nil {
			t.Fatalf("handleArchitectResult failed: %v", err)
		}

		// Verify transition to WAITING and state cleared
		if nextState != StateWaiting {
			t.Errorf("Expected transition to WAITING, got %s", nextState)
		}
		if driver.stateData["pending_request_id"] != nil {
			t.Error("Expected pending_request_id to be cleared")
		}
	})

	// Test: Architect requests changes
	t.Run("Architect requests changes", func(t *testing.T) {
		driver.currentState = StateWaiting
		driver.stateData["pending_request_id"] = "request-456"

		// Create RESPONSE message with NEEDS_CHANGES status
		resultMsg := proto.NewAgentMsg(proto.MsgTypeRESPONSE, "architect-001", "pm-test-001")
		approvalResult := &proto.ApprovalResult{
			ID:         proto.GenerateApprovalID(),
			Type:       proto.ApprovalTypeSpec,
			Status:     proto.ApprovalStatusNeedsChanges,
			Feedback:   "Requirements need more detail",
			ReviewedBy: "architect-001",
			ReviewedAt: time.Now(),
		}
		resultMsg.SetTypedPayload(proto.NewApprovalResponsePayload(approvalResult))

		// Process result
		nextState, err := driver.handleArchitectResult(resultMsg)
		if err != nil {
			t.Fatalf("handleArchitectResult failed: %v", err)
		}

		// Verify transition to INTERVIEWING with feedback stored
		if nextState != StateWorking {
			t.Errorf("Expected transition to INTERVIEWING, got %s", nextState)
		}
		if feedback, ok := driver.stateData["architect_feedback"].(string); !ok || feedback == "" {
			t.Error("Expected architect_feedback to be stored in stateData")
		}
		if driver.stateData["pending_request_id"] != nil {
			t.Error("Expected pending_request_id to be cleared")
		}
	})
}

// TestHandleSubmittingValidSpec tests SUBMITTING state with valid spec.
func TestHandleSubmittingValidSpec(t *testing.T) {
	// Create mock tool provider with successful spec_submit
	mockTool := &mockSpecSubmitTool{returnSuccess: true}
	toolProvider := &mockToolProvider{
		tools: map[string]tools.Tool{
			tools.ToolSpecSubmit: mockTool,
		},
	}

	driver := createTestDriver(t, toolProvider)
	driver.currentState = StateWorking

	// Start dispatcher for message sending
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := driver.dispatcher.Start(ctx); err != nil {
		t.Fatalf("Failed to start dispatcher: %v", err)
	}

	validSpec := `---
version: "1.0"
priority: must
---

# Feature: Test Feature

## Vision
Test vision.

## Scope
### In Scope
- Item 1

## Requirements

### R-001: Test Requirement
**Type:** functional
**Priority:** must
**Dependencies:** []

**Description:** Test description.

**Acceptance Criteria:**
- [ ] Criterion 1
`

	driver.stateData["draft_spec"] = validSpec

	nextState, err := driver.handleWorking(ctx)

	if err != nil {
		t.Fatalf("handleWorking failed: %v", err)
	}

	// Verify transition to WAITING (spec submitted to architect)
	if nextState != StateWaiting {
		t.Errorf("Expected transition to WAITING, got %s", nextState)
	}

	// Verify REQUEST was created and pending_request_id stored
	if driver.stateData["pending_request_id"] == nil {
		t.Error("Expected pending_request_id to be set")
	}
}

// TestHandleSubmittingInvalidSpec tests SUBMITTING state with invalid spec.
func TestHandleSubmittingInvalidSpec(t *testing.T) {
	// Create mock tool provider with failing spec_submit
	mockTool := &mockSpecSubmitTool{
		returnSuccess: false,
		returnErrors:  []string{"Missing requirements section", "Invalid YAML frontmatter"},
	}
	toolProvider := &mockToolProvider{
		tools: map[string]tools.Tool{
			tools.ToolSpecSubmit: mockTool,
		},
	}

	driver := createTestDriver(t, toolProvider)
	driver.currentState = StateWorking

	invalidSpec := `# Invalid Spec
No frontmatter or requirements.
`

	driver.stateData["draft_spec"] = invalidSpec

	ctx := context.Background()
	nextState, err := driver.handleWorking(ctx)

	if err != nil {
		t.Fatalf("handleWorking failed: %v", err)
	}

	// Verify transition back to INTERVIEWING (validation failed)
	if nextState != StateWorking {
		t.Errorf("Expected transition to INTERVIEWING, got %s", nextState)
	}

	// Verify validation errors stored in stateData
	if driver.stateData["validation_feedback"] == nil {
		t.Error("Expected validation_feedback to be stored")
	}
}

// TestHandleSubmittingNoDraftSpec tests SUBMITTING state without draft spec.
func TestHandleSubmittingNoDraftSpec(t *testing.T) {
	driver := createTestDriver(t, nil)
	driver.currentState = StateWorking

	ctx := context.Background()
	nextState, err := driver.handleWorking(ctx)

	// Should return ERROR state and error
	if err == nil {
		t.Error("Expected error when no draft spec present")
	}
	if nextState != proto.StateError {
		t.Errorf("Expected transition to ERROR, got %s", nextState)
	}
}

// TestGetAgentType tests the GetAgentType method.
func TestGetAgentType(t *testing.T) {
	driver := createTestDriver(t, nil)
	if driver.GetAgentType() != agent.TypePM {
		t.Errorf("Expected agent type %s, got %s", agent.TypePM, driver.GetAgentType())
	}
}

// TestGetID tests the GetID method.
func TestGetID(t *testing.T) {
	driver := createTestDriver(t, nil)
	if driver.GetID() != "pm-test-001" {
		t.Errorf("Expected ID pm-test-001, got %s", driver.GetID())
	}
}

// TestGetState tests the GetState method.
func TestGetState(t *testing.T) {
	driver := createTestDriver(t, nil)
	if driver.GetState() != StateWaiting {
		t.Errorf("Expected state WAITING, got %s", driver.GetState())
	}

	driver.currentState = StateWorking
	if driver.GetState() != StateWorking {
		t.Errorf("Expected state INTERVIEWING, got %s", driver.GetState())
	}
}

// TestSetChannels tests the SetChannels method.
func TestSetChannels(t *testing.T) {
	driver := createTestDriver(t, nil)

	specCh := make(chan *proto.AgentMsg)
	replyCh := make(chan *proto.AgentMsg)

	driver.SetChannels(specCh, nil, replyCh)

	if driver.interviewRequestCh == nil {
		t.Error("Expected interviewRequestCh to be set")
	}
	if driver.replyCh == nil {
		t.Error("Expected replyCh to be set")
	}
}

// TestSetDispatcher tests the SetDispatcher method.
func TestSetDispatcher(t *testing.T) {
	driver := createTestDriver(t, nil)

	cfg := &config.Config{
		Agents: &config.AgentConfig{
			MaxCoders: 4,
		},
	}
	newDispatcher, err := dispatch.NewDispatcher(cfg)
	if err != nil {
		t.Fatalf("Failed to create dispatcher: %v", err)
	}
	driver.SetDispatcher(newDispatcher)

	if driver.dispatcher != newDispatcher {
		t.Error("Expected dispatcher to be updated")
	}
}
