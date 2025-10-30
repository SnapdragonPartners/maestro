package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/config"
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/proto"
)

type mockAgent struct {
	id    string
	typ   agent.Type
	state string
}

func (m *mockAgent) GetID() string {
	return m.id
}

func (m *mockAgent) GetAgentType() agent.Type {
	return m.typ
}

func (m *mockAgent) GetCurrentState() proto.State {
	return proto.State(m.state)
}

func (m *mockAgent) GetStateData() map[string]any {
	return make(map[string]any)
}

func (m *mockAgent) ValidateState(_ proto.State) error {
	return nil
}

func (m *mockAgent) GetValidStates() []proto.State {
	return []proto.State{proto.State(m.state)}
}

func (m *mockAgent) Initialize(_ context.Context) error {
	return nil
}

func (m *mockAgent) Run(_ context.Context) error {
	return nil
}

func (m *mockAgent) Step(_ context.Context) (bool, error) {
	return true, nil
}

func (m *mockAgent) ProcessMessage(_ context.Context, _ *proto.AgentMsg) (*proto.AgentMsg, error) {
	return &proto.AgentMsg{}, nil
}

func (m *mockAgent) Shutdown(_ context.Context) error {
	return nil
}

func createTestConfig() *config.Config {
	return &config.Config{
		Agents: &config.AgentConfig{
			MaxCoders:      3,
			CoderModel:     config.ModelClaudeSonnetLatest,
			ArchitectModel: config.ModelOpenAIO3Mini,
		},
	}
}

func TestHandleAgents(t *testing.T) {
	cfg := createTestConfig()
	dispatcher, err := dispatch.NewDispatcher(cfg)
	if err != nil {
		t.Fatalf("Failed to create dispatcher: %v", err)
	}

	ctx := context.Background()
	if err := dispatcher.Start(ctx); err != nil {
		t.Fatalf("Failed to start dispatcher: %v", err)
	}
	defer dispatcher.Stop(ctx)

	architectID := "architect-001"
	architectAgent := &mockAgent{
		id:    architectID,
		typ:   agent.TypeArchitect,
		state: "SPEC_PARSING",
	}
	if err := dispatcher.RegisterAgent(architectAgent); err != nil {
		t.Fatalf("Failed to register architect: %v", err)
	}

	coderID := "coder-001"
	coderAgent := &mockAgent{
		id:    coderID,
		typ:   agent.TypeCoder,
		state: "PLANNING",
	}
	if err := dispatcher.RegisterAgent(coderAgent); err != nil {
		t.Fatalf("Failed to register coder: %v", err)
	}

	server := NewServer(dispatcher, "/tmp/test", nil)

	req := httptest.NewRequest("GET", "/api/agents", nil)
	w := httptest.NewRecorder()

	server.handleAgents(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var agents []AgentListItem
	if err := json.NewDecoder(w.Body).Decode(&agents); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if len(agents) != 2 {
		t.Errorf("Expected 2 agents, got %d", len(agents))
	}

	foundArchitect := false
	foundCoder := false
	for _, a := range agents {
		if a.ID == architectID && a.Role == agent.TypeArchitect.String() && a.State == "SPEC_PARSING" {
			foundArchitect = true
		}
		if a.ID == coderID && a.Role == agent.TypeCoder.String() && a.State == "PLANNING" {
			foundCoder = true
		}
	}

	if !foundArchitect {
		t.Error("Architect not found in response")
	}
	if !foundCoder {
		t.Error("Coder not found in response")
	}
}

func TestHandleAgent(t *testing.T) {
	cfg := createTestConfig()
	dispatcher, err := dispatch.NewDispatcher(cfg)
	if err != nil {
		t.Fatalf("Failed to create dispatcher: %v", err)
	}

	ctx := context.Background()
	if err := dispatcher.Start(ctx); err != nil {
		t.Fatalf("Failed to start dispatcher: %v", err)
	}
	defer dispatcher.Stop(ctx)

	architectID := "architect-001"
	architectAgent := &mockAgent{
		id:    architectID,
		typ:   agent.TypeArchitect,
		state: "SPEC_PARSING",
	}
	if err := dispatcher.RegisterAgent(architectAgent); err != nil {
		t.Fatalf("Failed to register architect: %v", err)
	}

	server := NewServer(dispatcher, "/tmp/test", nil)

	req := httptest.NewRequest("GET", "/api/agent/"+architectID, nil)
	w := httptest.NewRecorder()

	server.handleAgent(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response["id"] != architectID {
		t.Errorf("Expected agent ID %s, got %v", architectID, response["id"])
	}
	if response["type"] != agent.TypeArchitect.String() {
		t.Errorf("Expected agent type %s, got %v", agent.TypeArchitect.String(), response["type"])
	}
	if response["state"] != "SPEC_PARSING" {
		t.Errorf("Expected agent state SPEC_PARSING, got %v", response["state"])
	}
}

func TestHandleAgentNotFound(t *testing.T) {
	cfg := createTestConfig()
	dispatcher, err := dispatch.NewDispatcher(cfg)
	if err != nil {
		t.Fatalf("Failed to create dispatcher: %v", err)
	}

	ctx := context.Background()
	if err := dispatcher.Start(ctx); err != nil {
		t.Fatalf("Failed to start dispatcher: %v", err)
	}
	defer dispatcher.Stop(ctx)

	server := NewServer(dispatcher, "/tmp/test", nil)

	req := httptest.NewRequest("GET", "/api/agent/nonexistent", nil)
	w := httptest.NewRecorder()

	server.handleAgent(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w.Code)
	}
}

func TestFindArchitectState(t *testing.T) {
	cfg := createTestConfig()
	dispatcher, err := dispatch.NewDispatcher(cfg)
	if err != nil {
		t.Fatalf("Failed to create dispatcher: %v", err)
	}

	ctx := context.Background()
	if startErr := dispatcher.Start(ctx); startErr != nil {
		t.Fatalf("Failed to start dispatcher: %v", startErr)
	}
	defer dispatcher.Stop(ctx)

	architectID := "architect-001"
	architectAgent := &mockAgent{
		id:    architectID,
		typ:   agent.TypeArchitect,
		state: "DISPATCHING",
	}
	if regErr := dispatcher.RegisterAgent(architectAgent); regErr != nil {
		t.Fatalf("Failed to register architect: %v", regErr)
	}

	server := NewServer(dispatcher, "/tmp/test", nil)

	state, err := server.findArchitectState()
	if err != nil {
		t.Fatalf("Expected to find architect, got error: %v", err)
	}

	if state != "DISPATCHING" {
		t.Errorf("Expected architect state DISPATCHING, got %s", state)
	}
}

func TestFindArchitectStateNoArchitect(t *testing.T) {
	cfg := createTestConfig()
	dispatcher, err := dispatch.NewDispatcher(cfg)
	if err != nil {
		t.Fatalf("Failed to create dispatcher: %v", err)
	}

	ctx := context.Background()
	if startErr := dispatcher.Start(ctx); startErr != nil {
		t.Fatalf("Failed to start dispatcher: %v", startErr)
	}
	defer dispatcher.Stop(ctx)

	server := NewServer(dispatcher, "/tmp/test", nil)

	_, err = server.findArchitectState()
	if err == nil {
		t.Error("Expected error when no architect registered, got nil")
	}
}

func TestEmbeddedTemplates(t *testing.T) {
	cfg := createTestConfig()
	dispatcher, err := dispatch.NewDispatcher(cfg)
	if err != nil {
		t.Fatalf("Failed to create dispatcher: %v", err)
	}

	server := NewServer(dispatcher, "/tmp/test", nil)

	if server.templates == nil {
		t.Error("Templates should be loaded from embedded filesystem")
	}
}
