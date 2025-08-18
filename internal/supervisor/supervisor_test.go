package supervisor

import (
	"context"
	"os"
	"testing"
	"time"

	"orchestrator/internal/kernel"
	"orchestrator/pkg/agent"
	"orchestrator/pkg/config"
	"orchestrator/pkg/proto"
)

// createTestConfig creates a minimal valid config for testing.
func createTestConfig() config.Config {
	return config.Config{
		Agents: &config.AgentConfig{
			MaxCoders:      2,
			CoderModel:     "test-model",
			ArchitectModel: "test-model",
		},
		Orchestrator: &config.OrchestratorConfig{
			Models: []config.Model{
				{Name: "test-model", MaxTPM: 1000, MaxConnections: 1, CPM: 1.0, DailyBudget: 10.0},
			},
		},
	}
}

// MockAgent implements dispatch.Agent for testing.
type MockAgent struct {
	state proto.State
	err   error
	id    string
}

func (m *MockAgent) GetID() string {
	return m.id
}

func (m *MockAgent) Shutdown(_ context.Context) error {
	return m.err
}

func (m *MockAgent) GetCurrentState() proto.State {
	return m.state
}

// TestNewSupervisor tests supervisor creation.
func TestNewSupervisor(t *testing.T) {
	// Create temporary directory for test
	tempDir, err := os.MkdirTemp("", "supervisor-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create minimal config and kernel
	cfg := createTestConfig()

	ctx := context.Background()
	k, err := kernel.NewKernel(ctx, &cfg, tempDir)
	if err != nil {
		t.Fatalf("Failed to create kernel: %v", err)
	}
	defer k.Stop()

	// Create supervisor
	supervisor := NewSupervisor(k)

	if supervisor == nil {
		t.Fatal("NewSupervisor returned nil")
	}

	// Verify supervisor components
	if supervisor.Kernel != k {
		t.Error("Supervisor kernel reference is incorrect")
	}
	if supervisor.Logger == nil {
		t.Error("Supervisor logger is nil")
	}
	if supervisor.Agents == nil {
		t.Error("Supervisor agents map is nil")
	}
	if supervisor.AgentTypes == nil {
		t.Error("Supervisor agent types map is nil")
	}
	if supervisor.running {
		t.Error("Supervisor should not be running initially")
	}

	// Verify default policy is set
	policy := supervisor.Policy
	if len(policy.OnDone) == 0 {
		t.Error("Default policy should have OnDone actions")
	}
	if len(policy.OnError) == 0 {
		t.Error("Default policy should have OnError actions")
	}
}

// TestDefaultRestartPolicy tests the default restart policy configuration.
func TestDefaultRestartPolicy(t *testing.T) {
	policy := DefaultRestartPolicy()

	// Test coder policies
	coderDoneAction := policy.OnDone[string(agent.TypeCoder)]
	if coderDoneAction != RestartAgent {
		t.Errorf("Expected RestartAgent for coder done, got %v", coderDoneAction)
	}

	coderErrorAction := policy.OnError[string(agent.TypeCoder)]
	if coderErrorAction != RestartAgent {
		t.Errorf("Expected RestartAgent for coder error, got %v", coderErrorAction)
	}

	// Test architect policies
	architectDoneAction := policy.OnDone[string(agent.TypeArchitect)]
	if architectDoneAction != RestartAgent {
		t.Errorf("Expected RestartAgent for architect done, got %v", architectDoneAction)
	}

	architectErrorAction := policy.OnError[string(agent.TypeArchitect)]
	if architectErrorAction != FatalShutdown {
		t.Errorf("Expected FatalShutdown for architect error, got %v", architectErrorAction)
	}
}

// TestSupervisorAgentRegistration tests agent registration functionality.
func TestSupervisorAgentRegistration(t *testing.T) {
	// Create temporary directory for test
	tempDir, err := os.MkdirTemp("", "supervisor-registration-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create minimal config and kernel
	cfg := createTestConfig()

	ctx := context.Background()
	k, err := kernel.NewKernel(ctx, &cfg, tempDir)
	if err != nil {
		t.Fatalf("Failed to create kernel: %v", err)
	}
	defer k.Stop()

	supervisor := NewSupervisor(k)

	// Create mock agent
	mockAgent := &MockAgent{
		id:    "test-agent-001",
		state: proto.StateWaiting,
	}

	// Register agent
	agentID := "test-agent-001"
	agentType := string(agent.TypeCoder)
	supervisor.RegisterAgent(ctx, agentID, agentType, mockAgent)

	// Verify registration
	if supervisor.getAgentType(agentID) != agentType {
		t.Errorf("Expected agent type %s, got %s", agentType, supervisor.getAgentType(agentID))
	}

	agents, agentTypes := supervisor.GetAgents()
	if len(agents) != 1 {
		t.Errorf("Expected 1 agent, got %d", len(agents))
	}
	if len(agentTypes) != 1 {
		t.Errorf("Expected 1 agent type, got %d", len(agentTypes))
	}

	if agents[agentID].GetID() != mockAgent.GetID() {
		t.Error("Agent reference is incorrect")
	}
	if agentTypes[agentID] != agentType {
		t.Errorf("Expected agent type %s, got %s", agentType, agentTypes[agentID])
	}
}

// TestSupervisorCleanup tests agent cleanup functionality.
func TestSupervisorCleanup(t *testing.T) {
	// Create temporary directory for test
	tempDir, err := os.MkdirTemp("", "supervisor-cleanup-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create minimal config and kernel
	cfg := createTestConfig()

	ctx := context.Background()
	k, err := kernel.NewKernel(ctx, &cfg, tempDir)
	if err != nil {
		t.Fatalf("Failed to create kernel: %v", err)
	}
	defer k.Stop()

	supervisor := NewSupervisor(k)

	// Register mock agent
	mockAgent := &MockAgent{
		id:    "test-agent-001",
		state: proto.StateWaiting,
	}

	agentID := "test-agent-001"
	agentType := "coder"
	supervisor.RegisterAgent(ctx, agentID, agentType, mockAgent)

	// Verify agent is registered
	if len(supervisor.Agents) != 1 {
		t.Error("Agent should be registered")
	}

	// Clean up agent
	supervisor.cleanupAgentResources(agentID)

	// Verify agent is cleaned up
	if len(supervisor.Agents) != 0 {
		t.Error("Agent should be cleaned up from Agents map")
	}
	if len(supervisor.AgentTypes) != 0 {
		t.Error("Agent should be cleaned up from AgentTypes map")
	}
	if supervisor.getAgentType(agentID) != "" {
		t.Error("getAgentType should return empty string for cleaned up agent")
	}
}

// TestSupervisorStartStop tests supervisor lifecycle.
func TestSupervisorStartStop(t *testing.T) {
	// Create temporary directory for test
	tempDir, err := os.MkdirTemp("", "supervisor-lifecycle-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create minimal config and kernel
	cfg := createTestConfig()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	k, err := kernel.NewKernel(ctx, &cfg, tempDir)
	if err != nil {
		t.Fatalf("Failed to create kernel: %v", err)
	}
	defer k.Stop()

	// Start kernel to initialize dispatcher
	if err := k.Start(); err != nil {
		t.Fatalf("Failed to start kernel: %v", err)
	}

	supervisor := NewSupervisor(k)

	// Verify initial state
	if supervisor.running {
		t.Error("Supervisor should not be running initially")
	}

	// Start supervisor
	supervisor.Start(ctx)

	// Give it a moment to start
	time.Sleep(100 * time.Millisecond)

	// Verify running state
	if !supervisor.running {
		t.Error("Supervisor should be running after Start()")
	}

	// Test double start (should not cause issues)
	supervisor.Start(ctx)

	// Cancel context to stop supervisor
	cancel()

	// Wait for supervisor to stop
	time.Sleep(200 * time.Millisecond)

	// Verify stopped state
	if supervisor.running {
		t.Error("Supervisor should not be running after context cancellation")
	}
}

// TestRestartActions tests restart action constants and behavior.
func TestRestartActions(t *testing.T) {
	// Test action values
	if RestartAgent != 0 {
		t.Errorf("Expected RestartAgent to be 0, got %d", RestartAgent)
	}
	if FatalShutdown != 1 {
		t.Errorf("Expected FatalShutdown to be 1, got %d", FatalShutdown)
	}

	// Test that actions can be used in maps (compile-time check)
	actionMap := map[RestartAction]string{
		RestartAgent:  "restart",
		FatalShutdown: "shutdown",
	}

	if actionMap[RestartAgent] != "restart" {
		t.Error("RestartAgent action mapping failed")
	}
	if actionMap[FatalShutdown] != "shutdown" {
		t.Error("FatalShutdown action mapping failed")
	}
}
