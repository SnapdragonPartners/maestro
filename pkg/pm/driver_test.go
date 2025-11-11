package pm

import (
	"context"
	"testing"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/config"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/persistence"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
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

func createTestDriver(t *testing.T) *Driver {
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
		toolProvider:       nil, // No tool provider needed for basic tests
	}

	// Attach driver to dispatcher
	dispatcher.Attach(driver)

	return driver
}

// TestGetAgentType tests the GetAgentType method.
func TestGetAgentType(t *testing.T) {
	driver := createTestDriver(t)
	if driver.GetAgentType() != agent.TypePM {
		t.Errorf("Expected agent type %s, got %s", agent.TypePM, driver.GetAgentType())
	}
}

// TestGetID tests the GetID method.
func TestGetID(t *testing.T) {
	driver := createTestDriver(t)
	if driver.GetID() != "pm-test-001" {
		t.Errorf("Expected ID pm-test-001, got %s", driver.GetID())
	}
}

// TestGetState tests the GetState method.
func TestGetState(t *testing.T) {
	driver := createTestDriver(t)
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
	driver := createTestDriver(t)

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
	driver := createTestDriver(t)

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
