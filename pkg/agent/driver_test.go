package agent

import (
	"context"
	"testing"
	"time"

	"orchestrator/pkg/proto"
)

func TestDriverNewConfig(t *testing.T) {
	ctx := Context{
		Context: context.Background(),
		Logger:  nil,
	}
	config := NewConfig("test-agent", "coder", ctx)

	if config.ID != "test-agent" {
		t.Errorf("Expected ID 'test-agent', got '%s'", config.ID)
	}
	if config.Type != "coder" {
		t.Errorf("Expected type 'coder', got '%s'", config.Type)
	}
}

func TestDriverConfigWithLLM(t *testing.T) {
	ctx := Context{
		Context: context.Background(),
		Logger:  nil,
	}
	config := NewConfig("test-agent", "coder", ctx)
	llmConfig := &LLMConfig{MaxTokens: 1000}

	newConfig := config.WithLLM(llmConfig)
	if newConfig.LLMConfig != llmConfig {
		t.Error("Expected LLM config to be set")
	}
	// This implementation modifies the original config
	if config.LLMConfig == nil {
		t.Error("Expected original config to be modified")
	}
}

func TestDriverInterface(t *testing.T) {
	// Test that we can create a mock driver that implements the interface
	driver := &mockDriver{}

	if driver.GetID() != "mock-driver" {
		t.Errorf("Expected ID 'mock-driver', got '%s'", driver.GetID())
	}
	if driver.GetAgentType() != TypeCoder {
		t.Errorf("Expected type %v, got %v", TypeCoder, driver.GetAgentType())
	}

	// Test basic operations
	states := driver.GetValidStates()
	if len(states) == 0 {
		t.Error("Expected non-empty valid states")
	}

	// Test ValidateState with valid state
	err := driver.ValidateState(proto.StateWaiting)
	if err != nil {
		t.Errorf("Expected no error for valid state, got: %v", err)
	}
}

func TestDriverTimeout(t *testing.T) {
	driver := &mockDriver{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	// Test that driver operations can handle context cancellation
	_, err := driver.Step(ctx)
	// The mock doesn't actually check context, so this will pass
	// This is just testing the interface works
	if err != nil {
		t.Errorf("Unexpected error from Step: %v", err)
	}
}

func TestDriverRun(t *testing.T) {
	driver := &mockDriver{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	// Test that driver Run can handle context cancellation
	err := driver.Run(ctx)
	// The mock doesn't actually check context, so this will pass
	// This is just testing the interface works
	if err != nil {
		t.Errorf("Unexpected error from Run: %v", err)
	}
}

// Mock implementations for testing.

type mockDriver struct{}

func (m *mockDriver) Initialize(_ context.Context) error { return nil }
func (m *mockDriver) GetID() string                      { return "mock-driver" }
func (m *mockDriver) GetAgentType() Type                 { return TypeCoder }
func (m *mockDriver) GetCurrentState() proto.State       { return proto.StateWaiting }
func (m *mockDriver) GetStateData() map[string]any       { return make(map[string]any) }
func (m *mockDriver) ValidateState(_ proto.State) error  { return nil }
func (m *mockDriver) GetValidStates() []proto.State      { return []proto.State{proto.StateWaiting} }
func (m *mockDriver) ProcessMessage(_ context.Context, _ *proto.AgentMsg) (*proto.AgentMsg, error) {
	return &proto.AgentMsg{}, nil
}

func (m *mockDriver) Run(_ context.Context) error {
	// Simulate long running operation
	time.Sleep(100 * time.Millisecond)
	return nil
}

func (m *mockDriver) Step(_ context.Context) (bool, error) {
	// Simulate long running operation
	time.Sleep(100 * time.Millisecond)
	return false, nil
}

func (m *mockDriver) Shutdown(_ context.Context) error {
	return nil
}

// The ValidateState method is already defined above

// Additional type tests that use the shared mock.
func TestTypeIsValid(t *testing.T) {
	tests := []struct {
		name     string
		typ      Type
		expected bool
	}{
		{"valid coder", TypeCoder, true},
		{"valid architect", TypeArchitect, true},
		{"invalid type", Type("invalid"), false},
		{"empty type", Type(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.typ.IsValid(); got != tt.expected {
				t.Errorf("Type.IsValid() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestTypeString(t *testing.T) {
	tests := []struct {
		name     string
		typ      Type
		expected string
	}{
		{"coder type", TypeCoder, "coder"},
		{"architect type", TypeArchitect, "architect"},
		{"unknown type", Type("unknown"), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.typ.String(); got != tt.expected {
				t.Errorf("Type.String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Type
		wantErr bool
	}{
		{"parse coder", "coder", TypeCoder, false},
		{"parse architect", "architect", TypeArchitect, false},
		{"parse invalid", "invalid", Type(""), true},
		{"parse empty", "", Type(""), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("Parse() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("Parse() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMockDriverMethods(t *testing.T) {
	driver := &mockDriver{}

	// Test basic methods work
	if driver.GetID() == "" {
		t.Error("Expected non-empty ID")
	}

	state := driver.GetCurrentState()
	if state == "" {
		t.Error("Expected non-empty state")
	}

	data := driver.GetStateData()
	if data == nil {
		t.Error("Expected non-nil state data")
	}

	states := driver.GetValidStates()
	if len(states) == 0 {
		t.Error("Expected non-empty valid states")
	}
}

func TestGetStateData(t *testing.T) {
	mockAgent := &mockDriver{}
	data := mockAgent.GetStateData()
	if data == nil {
		t.Error("Expected non-nil state data")
	}
}

func TestGetAgentType(t *testing.T) {
	mockAgent := &mockDriver{}
	agentType := mockAgent.GetAgentType()
	if agentType != TypeCoder {
		t.Errorf("Expected TypeCoder, got %v", agentType)
	}
}
