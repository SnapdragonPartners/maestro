package supervisor

import (
	"context"
	"os"
	"testing"
	"time"

	"orchestrator/internal/kernel"
	"orchestrator/pkg/agent"
	"orchestrator/pkg/config"
	"orchestrator/pkg/persistence"
	"orchestrator/pkg/proto"
)

// createTestConfig creates a minimal valid config for testing.
func createTestConfig() config.Config {
	return config.Config{
		Agents: &config.AgentConfig{
			MaxCoders:      2,
			CoderModel:     config.ModelClaudeSonnetLatest,
			ArchitectModel: config.ModelOpenAIO3Mini,
		},
	}
}

// resetPersistence resets the database singleton for testing.
// Must be called before creating a kernel in tests.
func resetPersistence(t *testing.T) {
	t.Helper()
	if err := persistence.Reset(); err != nil {
		t.Fatalf("Failed to reset persistence: %v", err)
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
	resetPersistence(t)

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

	// Create supervisor with nil factory for testing
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
	resetPersistence(t)

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
	resetPersistence(t)

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
	resetPersistence(t)

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

// TestWaitForAgentsShutdownNoAgents tests shutdown wait with no registered agents.
func TestWaitForAgentsShutdownNoAgents(t *testing.T) {
	resetPersistence(t)

	// Create temporary directory for test
	tempDir, err := os.MkdirTemp("", "supervisor-shutdown-test-*")
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

	// With no agents registered, WaitForAgentsShutdown should return immediately
	err = supervisor.WaitForAgentsShutdown(1 * time.Second)
	if err != nil {
		t.Errorf("WaitForAgentsShutdown should succeed with no agents, got: %v", err)
	}
}

// RunnableMockAgent implements dispatch.Agent with Run method for testing.
type RunnableMockAgent struct {
	MockAgent
	runCalled chan struct{}
	runDelay  time.Duration
}

func (m *RunnableMockAgent) Run(ctx context.Context) error {
	if m.runCalled != nil {
		close(m.runCalled)
	}
	// Wait for context cancellation or delay
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(m.runDelay):
		return nil
	}
}

// TestWaitForAgentsShutdownWithAgents tests shutdown wait with running agents.
func TestWaitForAgentsShutdownWithAgents(t *testing.T) {
	resetPersistence(t)

	// Create temporary directory for test
	tempDir, err := os.MkdirTemp("", "supervisor-shutdown-agents-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create minimal config and kernel
	cfg := createTestConfig()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	k, err := kernel.NewKernel(ctx, &cfg, tempDir)
	if err != nil {
		t.Fatalf("Failed to create kernel: %v", err)
	}
	defer k.Stop()

	supervisor := NewSupervisor(k)

	// Create runnable mock agent that waits for context cancellation
	runCalled := make(chan struct{})
	mockAgent := &RunnableMockAgent{
		MockAgent: MockAgent{
			id:    "test-agent-001",
			state: proto.StateWaiting,
		},
		runCalled: runCalled,
		runDelay:  10 * time.Second, // Long delay so it waits for context
	}

	// Register agent (this starts the Run goroutine)
	supervisor.RegisterAgent(ctx, "test-agent-001", string(agent.TypeCoder), mockAgent)

	// Wait for Run to be called
	select {
	case <-runCalled:
		// Good, agent started
	case <-time.After(1 * time.Second):
		t.Fatal("Agent Run was not called within timeout")
	}

	// Cancel the context to trigger shutdown
	cancel()

	// Wait for agents - should complete quickly since context is cancelled
	err = supervisor.WaitForAgentsShutdown(5 * time.Second)
	if err != nil {
		t.Errorf("WaitForAgentsShutdown should succeed after context cancel, got: %v", err)
	}
}

// TestWaitForAgentsShutdownTimeout tests shutdown wait timeout.
func TestWaitForAgentsShutdownTimeout(t *testing.T) {
	resetPersistence(t)

	// Create temporary directory for test
	tempDir, err := os.MkdirTemp("", "supervisor-shutdown-timeout-test-*")
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

	// Manually increment waitgroup to simulate a stuck agent
	supervisor.agentWg.Add(1)

	// Wait for agents with short timeout - should timeout
	err = supervisor.WaitForAgentsShutdown(100 * time.Millisecond)
	if err == nil {
		t.Error("WaitForAgentsShutdown should return error on timeout")
	}

	// Clean up: decrement the waitgroup
	supervisor.agentWg.Done()
}

func TestWatchdogSkipsWaitingAgents(t *testing.T) {
	resetPersistence(t)

	tempDir, err := os.MkdirTemp("", "supervisor-watchdog-waiting-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cfg := createTestConfig()
	ctx := context.Background()
	k, err := kernel.NewKernel(ctx, &cfg, tempDir)
	if err != nil {
		t.Fatalf("Failed to create kernel: %v", err)
	}
	defer k.Stop()

	supervisor := NewSupervisor(k)

	agentID := "coder-001"
	mockAgent := &MockAgent{id: agentID, state: proto.StateWaiting}
	supervisor.Agents[agentID] = mockAgent
	supervisor.AgentTypes[agentID] = string(agent.TypeCoder)

	// Record activity far in the past — would normally trigger watchdog kill
	supervisor.activityMu.Lock()
	supervisor.lastActivity[agentID] = time.Now().Add(-2 * time.Hour)
	supervisor.agentStates[agentID] = proto.StateWaiting
	supervisor.activityMu.Unlock()

	// Set up a cancel func to detect if watchdog tries to kill the agent
	agentCtx, cancel := context.WithCancel(ctx)
	supervisor.AgentContexts[agentID] = cancel

	supervisor.checkCodingActivity()

	// Context should NOT have been cancelled — agent is WAITING
	select {
	case <-agentCtx.Done():
		t.Error("Watchdog should NOT cancel WAITING agents")
	default:
		// Expected: context still alive
	}
}

func TestWatchdogKillsStuckCodingAgent(t *testing.T) {
	resetPersistence(t)

	tempDir, err := os.MkdirTemp("", "supervisor-watchdog-stuck-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cfg := createTestConfig()
	ctx := context.Background()
	k, err := kernel.NewKernel(ctx, &cfg, tempDir)
	if err != nil {
		t.Fatalf("Failed to create kernel: %v", err)
	}
	defer k.Stop()

	supervisor := NewSupervisor(k)

	agentID := "coder-002"
	mockAgent := &MockAgent{id: agentID, state: proto.State("CODING")}
	supervisor.Agents[agentID] = mockAgent
	supervisor.AgentTypes[agentID] = string(agent.TypeCoder)

	// Record activity far in the past
	supervisor.activityMu.Lock()
	supervisor.lastActivity[agentID] = time.Now().Add(-2 * time.Hour)
	supervisor.agentStates[agentID] = proto.State("CODING")
	supervisor.activityMu.Unlock()

	agentCtx, cancel := context.WithCancel(ctx)
	supervisor.AgentContexts[agentID] = cancel

	supervisor.checkCodingActivity()

	// Context SHOULD have been cancelled — agent is stuck in CODING
	select {
	case <-agentCtx.Done():
		// Expected: watchdog killed it
	default:
		t.Error("Watchdog should cancel stuck CODING agents")
	}
}

func TestUnexpectedExitRestartsCoderAgent(t *testing.T) {
	resetPersistence(t)

	tempDir, err := os.MkdirTemp("", "supervisor-unexpected-exit-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cfg := createTestConfig()
	ctx := context.Background()
	k, err := kernel.NewKernel(ctx, &cfg, tempDir)
	if err != nil {
		t.Fatalf("Failed to create kernel: %v", err)
	}
	defer k.Stop()

	supervisor := NewSupervisor(k)

	agentID := "coder-010"
	mockAgent := &MockAgent{id: agentID, state: proto.StateWaiting}
	supervisor.Agents[agentID] = mockAgent
	supervisor.AgentTypes[agentID] = string(agent.TypeCoder)

	supervisor.activityMu.Lock()
	supervisor.agentGeneration[agentID] = 1
	supervisor.agentStates[agentID] = proto.State("CODING")
	supervisor.activityMu.Unlock()

	// Call with current generation — should attempt restart
	supervisor.handleUnexpectedExit(ctx, agentID, 1)

	// Verify restart was attempted: cleanupAgentResources deletes the agent type entry,
	// and since the factory won't find a real agent config in tests, the entry stays deleted.
	supervisor.activityMu.Lock()
	_, typeExists := supervisor.AgentTypes[agentID]
	genAfter := supervisor.agentGeneration[agentID]
	supervisor.activityMu.Unlock()

	if typeExists {
		t.Error("Expected AgentTypes entry to be cleaned up by restart attempt")
	}
	// Generation should still be 1 (cleanup doesn't delete it, and factory failed so
	// RegisterAgent was never called to increment it)
	if genAfter != 1 {
		t.Errorf("Expected generation to remain 1 (factory fails in test), got %d", genAfter)
	}
}

func TestNoRestartDuringShutdown(t *testing.T) {
	resetPersistence(t)

	tempDir, err := os.MkdirTemp("", "supervisor-no-restart-shutdown-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cfg := createTestConfig()
	parentCtx := context.Background()
	k, err := kernel.NewKernel(parentCtx, &cfg, tempDir)
	if err != nil {
		t.Fatalf("Failed to create kernel: %v", err)
	}
	defer k.Stop()

	supervisor := NewSupervisor(k)

	agentID := "coder-011"
	mockAgent := &MockAgent{id: agentID, state: proto.State("CODING")}
	supervisor.Agents[agentID] = mockAgent
	supervisor.AgentTypes[agentID] = string(agent.TypeCoder)

	supervisor.activityMu.Lock()
	supervisor.agentGeneration[agentID] = 1
	supervisor.activityMu.Unlock()

	cancelledCtx, cancel := context.WithCancel(parentCtx)
	cancel()

	supervisor.handleUnexpectedExit(cancelledCtx, agentID, 1)

	// Agent should NOT have been cleaned up (no restart attempted)
	if _, exists := supervisor.AgentTypes[agentID]; !exists {
		t.Error("Agent should not be restarted during system shutdown")
	}
}

func TestNoDoubleRestartViaGeneration(t *testing.T) {
	resetPersistence(t)

	tempDir, err := os.MkdirTemp("", "supervisor-no-double-restart-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cfg := createTestConfig()
	ctx := context.Background()
	k, err := kernel.NewKernel(ctx, &cfg, tempDir)
	if err != nil {
		t.Fatalf("Failed to create kernel: %v", err)
	}
	defer k.Stop()

	supervisor := NewSupervisor(k)

	agentID := "coder-012"
	mockAgent := &MockAgent{id: agentID, state: proto.State("DONE")}
	supervisor.Agents[agentID] = mockAgent
	supervisor.AgentTypes[agentID] = string(agent.TypeCoder)

	// Simulate: DONE notification triggered restartAgent which incremented generation to 2
	supervisor.activityMu.Lock()
	supervisor.agentGeneration[agentID] = 2
	supervisor.activityMu.Unlock()

	// Old goroutine calls handleUnexpectedExit with stale generation 1
	supervisor.handleUnexpectedExit(ctx, agentID, 1)

	// Agent should still be present — stale generation was detected
	if _, exists := supervisor.AgentTypes[agentID]; !exists {
		t.Error("Stale goroutine should not restart when generation has advanced")
	}
}

func TestWatchdogSkipsBlockingStates(t *testing.T) {
	resetPersistence(t)

	tempDir, err := os.MkdirTemp("", "supervisor-watchdog-blocking-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cfg := createTestConfig()
	ctx := context.Background()
	k, err := kernel.NewKernel(ctx, &cfg, tempDir)
	if err != nil {
		t.Fatalf("Failed to create kernel: %v", err)
	}
	defer k.Stop()

	// Test that watchdog skips all states where coder blocks on external input
	blockingStates := []proto.State{
		proto.StateWaiting,
		proto.State("SETUP"),
		proto.State("PLAN_REVIEW"),
		proto.State("CODE_REVIEW"),
		proto.State("QUESTION"),
		proto.State("BUDGET_REVIEW"),
		proto.State("PREPARE_MERGE"),
		proto.State("AWAIT_MERGE"),
	}

	for _, state := range blockingStates {
		t.Run(string(state), func(t *testing.T) {
			supervisor := NewSupervisor(k)

			agentID := "coder-blocking"
			mockAgent := &MockAgent{id: agentID, state: state}
			supervisor.Agents[agentID] = mockAgent
			supervisor.AgentTypes[agentID] = string(agent.TypeCoder)

			supervisor.activityMu.Lock()
			supervisor.lastActivity[agentID] = time.Now().Add(-2 * time.Hour)
			supervisor.agentStates[agentID] = state
			supervisor.activityMu.Unlock()

			agentCtx, cancel := context.WithCancel(ctx)
			supervisor.AgentContexts[agentID] = cancel

			supervisor.checkCodingActivity()

			select {
			case <-agentCtx.Done():
				t.Errorf("Watchdog should NOT cancel agents in %s state", state)
			default:
			}
		})
	}
}

func TestRegisteredAgentGetsStateFromAgent(t *testing.T) {
	resetPersistence(t)

	tempDir, err := os.MkdirTemp("", "supervisor-initial-state-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cfg := createTestConfig()
	ctx := context.Background()
	k, err := kernel.NewKernel(ctx, &cfg, tempDir)
	if err != nil {
		t.Fatalf("Failed to create kernel: %v", err)
	}
	defer k.Stop()

	supervisor := NewSupervisor(k)

	// Agent that reports CODING as its current state (simulates restored agent)
	agentID := "coder-restored"
	mockAgent := &MockAgent{id: agentID, state: proto.State("CODING")}

	supervisor.RegisterAgent(ctx, agentID, string(agent.TypeCoder), mockAgent)

	supervisor.activityMu.Lock()
	state := supervisor.agentStates[agentID]
	supervisor.activityMu.Unlock()

	if state != proto.State("CODING") {
		t.Errorf("Expected initial state CODING from agent, got: %s", state)
	}
}

func TestRegisteredAgentDefaultsToWaiting(t *testing.T) {
	resetPersistence(t)

	tempDir, err := os.MkdirTemp("", "supervisor-default-state-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cfg := createTestConfig()
	ctx := context.Background()
	k, err := kernel.NewKernel(ctx, &cfg, tempDir)
	if err != nil {
		t.Fatalf("Failed to create kernel: %v", err)
	}
	defer k.Stop()

	supervisor := NewSupervisor(k)

	// Agent that reports empty state (fresh agent)
	agentID := "coder-fresh"
	mockAgent := &MockAgent{id: agentID, state: ""}

	supervisor.RegisterAgent(ctx, agentID, string(agent.TypeCoder), mockAgent)

	supervisor.activityMu.Lock()
	state := supervisor.agentStates[agentID]
	supervisor.activityMu.Unlock()

	if state != proto.StateWaiting {
		t.Errorf("Expected default state WAITING for fresh agent, got: %s", state)
	}
}

func TestAgentStateTrackedThroughLifecycle(t *testing.T) {
	resetPersistence(t)

	tempDir, err := os.MkdirTemp("", "supervisor-state-track-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cfg := createTestConfig()
	ctx := context.Background()
	k, err := kernel.NewKernel(ctx, &cfg, tempDir)
	if err != nil {
		t.Fatalf("Failed to create kernel: %v", err)
	}
	defer k.Stop()

	supervisor := NewSupervisor(k)

	agentID := "coder-003"
	mockAgent := &MockAgent{id: agentID}
	supervisor.Agents[agentID] = mockAgent
	supervisor.AgentTypes[agentID] = string(agent.TypeCoder)

	// Simulate registration sets WAITING
	supervisor.activityMu.Lock()
	supervisor.agentStates[agentID] = proto.StateWaiting
	supervisor.activityMu.Unlock()

	// Simulate state change to CODING
	supervisor.handleStateChange(ctx, &proto.StateChangeNotification{
		AgentID:   agentID,
		FromState: proto.StateWaiting,
		ToState:   proto.State("CODING"),
	})

	supervisor.activityMu.Lock()
	state := supervisor.agentStates[agentID]
	supervisor.activityMu.Unlock()

	if state != proto.State("CODING") {
		t.Errorf("Expected state CODING after state change, got: %s", state)
	}

	// Simulate cleanup removes state
	supervisor.cleanupAgentResources(agentID)

	supervisor.activityMu.Lock()
	_, exists := supervisor.agentStates[agentID]
	supervisor.activityMu.Unlock()

	if exists {
		t.Error("Expected agent state to be cleaned up after resource cleanup")
	}
}
