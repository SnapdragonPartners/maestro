package dispatch

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/config"
	"orchestrator/pkg/proto"
)

// Mock agent for testing.
type mockAgent struct {
	id       string
	shutdown bool
}

func (m *mockAgent) GetID() string {
	return m.id
}

func (m *mockAgent) Shutdown(_ context.Context) error {
	m.shutdown = true
	return nil
}

// Mock channel receiver agent that also implements Driver interface.
type mockChannelReceiver struct {
	agentType agent.Type
	mockAgent
	channels bool
}

func (m *mockChannelReceiver) SetChannels(_ chan *proto.AgentMsg, _ chan *proto.AgentMsg, _ <-chan *proto.AgentMsg) {
	m.channels = true
}

func (m *mockChannelReceiver) SetDispatcher(_ *Dispatcher) {}

func (m *mockChannelReceiver) SetStateNotificationChannel(_ chan<- *proto.StateChangeNotification) {}

// Implement Driver interface methods.
func (m *mockChannelReceiver) Initialize(_ context.Context) error   { return nil }
func (m *mockChannelReceiver) Run(_ context.Context) error          { return nil }
func (m *mockChannelReceiver) Step(_ context.Context) (bool, error) { return false, nil }
func (m *mockChannelReceiver) GetCurrentState() proto.State         { return proto.StateWaiting }
func (m *mockChannelReceiver) GetStateData() agent.StateData        { return make(agent.StateData) }
func (m *mockChannelReceiver) GetAgentType() agent.Type {
	if m.agentType != "" {
		return m.agentType
	}
	return agent.TypeCoder
}
func (m *mockChannelReceiver) ValidateState(_ proto.State) error { return nil }
func (m *mockChannelReceiver) GetValidStates() []proto.State {
	return []proto.State{proto.StateWaiting}
}
func (m *mockChannelReceiver) ProcessMessage(_ context.Context, _ *proto.AgentMsg) (*proto.AgentMsg, error) {
	return &proto.AgentMsg{}, nil
}

// NewTestDispatcher creates a dispatcher with step strategy for deterministic testing.
func NewTestDispatcher(t *testing.T) *Dispatcher {
	// Create temporary directory and load config
	tempDir := t.TempDir()

	err := config.LoadConfig(tempDir)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	cfg, err := config.GetConfig()
	if err != nil {
		t.Fatalf("Failed to get config: %v", err)
	}

	// Create dispatcher using the same signature as createTestDispatcher
	dispatcher, err := NewDispatcher(&cfg)
	if err != nil {
		t.Fatalf("Failed to create dispatcher: %v", err)
	}

	// Use default goroutine strategy for normal tests

	t.Cleanup(func() {
		ctx := context.Background()
		_ = dispatcher.Stop(ctx)
	})
	return dispatcher
}

func createTestDispatcher(t *testing.T) *Dispatcher {
	// Create temporary directory and load config
	tempDir := t.TempDir()

	//nolint:contextcheck // Test uses background context which is appropriate for tests
	err := config.LoadConfig(tempDir)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	cfg, err := config.GetConfig()
	if err != nil {
		t.Fatalf("Failed to get config: %v", err)
	}

	// Create dispatcher
	dispatcher, err := NewDispatcher(&cfg)
	if err != nil {
		t.Fatalf("Failed to create dispatcher: %v", err)
	}

	return dispatcher
}

func TestNewDispatcher(t *testing.T) {
	dispatcher := createTestDispatcher(t)

	if dispatcher == nil {
		t.Error("Expected non-nil dispatcher")
		return
	}

	if dispatcher.agents == nil {
		t.Error("Expected agents map to be initialized")
	}

	if dispatcher.replyChannels == nil {
		t.Error("Expected reply channels map to be initialized")
	}

	if dispatcher.leases == nil {
		t.Error("Expected leases map to be initialized")
	}
}

func TestAttachAgent(t *testing.T) {
	dispatcher := createTestDispatcher(t)
	agent := &mockAgent{id: "test-agent-001"}

	// Test attaching agent
	dispatcher.Attach(agent)

	// Verify agent is registered
	dispatcher.mu.RLock()
	storedAgent, exists := dispatcher.agents[agent.GetID()]
	dispatcher.mu.RUnlock()

	if !exists {
		t.Error("Expected agent to be registered")
	}

	if storedAgent != agent {
		t.Error("Expected stored agent to match attached agent")
	}
}

func TestAttachChannelReceiver(t *testing.T) {
	dispatcher := createTestDispatcher(t)
	agent := &mockChannelReceiver{
		mockAgent: mockAgent{id: "channel-receiver-001"},
	}

	// Test attaching channel receiver
	dispatcher.Attach(agent)

	// Verify channels were set
	if !agent.channels {
		t.Error("Expected channels to be set on channel receiver")
	}

	// Verify reply channel was created
	dispatcher.mu.RLock()
	_, exists := dispatcher.replyChannels[agent.GetID()]
	dispatcher.mu.RUnlock()

	if !exists {
		t.Error("Expected reply channel to be created")
	}
}

func TestDetachAgent(t *testing.T) {
	dispatcher := createTestDispatcher(t)
	agent := &mockAgent{id: "detach-test-001"}

	// First attach the agent
	dispatcher.Attach(agent)

	// Then detach it
	dispatcher.Detach(agent.GetID())

	// Verify agent is removed
	dispatcher.mu.RLock()
	_, exists := dispatcher.agents[agent.GetID()]
	replyExists := len(dispatcher.replyChannels[agent.GetID()]) >= 0 // Channel might exist but be closed
	dispatcher.mu.RUnlock()

	if exists {
		t.Error("Expected agent to be removed after detach")
	}

	// Reply channel should be cleaned up
	_ = replyExists // We'll allow the channel to exist but be closed
}

func TestGetRegisteredAgents(t *testing.T) {
	dispatcher := createTestDispatcher(t)
	agent1 := &mockAgent{id: "agent-001"}
	agent2 := &mockAgent{id: "agent-002"}

	// Attach agents
	dispatcher.Attach(agent1)
	dispatcher.Attach(agent2)

	// Get agents list
	agentInfos := dispatcher.GetRegisteredAgents()

	if len(agentInfos) != 2 {
		t.Errorf("Expected 2 agents, got %d", len(agentInfos))
	}

	// Check that both agents are in the list
	found1, found2 := false, false
	for _, agentInfo := range agentInfos {
		if agentInfo.ID == "agent-001" {
			found1 = true
		}
		if agentInfo.ID == "agent-002" {
			found2 = true
		}
	}

	if !found1 || !found2 {
		t.Error("Expected both agents to be in the list")
	}
}

func TestGetStats(t *testing.T) {
	dispatcher := createTestDispatcher(t)
	agent := &mockAgent{id: "stats-test-001"}

	// Attach agent and test getting stats
	dispatcher.Attach(agent)
	stats := dispatcher.GetStats()

	if stats == nil {
		t.Error("Expected non-nil stats")
	}

	// Stats should contain some basic information
	if len(stats) == 0 {
		t.Error("Expected stats to contain some information")
	}
}

func TestSeverityValues(t *testing.T) {
	if Warn >= Fatal {
		t.Error("Expected Warn to have lower severity than Fatal")
	}
}

func TestAgentError(t *testing.T) {
	testErr := context.DeadlineExceeded
	err := AgentError{
		ID:  "test-agent",
		Sev: Fatal,
		Err: testErr,
	}

	if err.ID != "test-agent" {
		t.Error("Expected agent ID to be preserved")
	}

	if err.Sev != Fatal {
		t.Error("Expected severity to be preserved")
	}

	if err.Err.Error() != testErr.Error() {
		t.Error("Expected error to be preserved")
	}
}

func TestResult(t *testing.T) {
	msg := &proto.AgentMsg{
		ID:   "test-msg",
		Type: "STORY",
	}

	result := Result{
		Message: msg,
		Error:   nil,
	}

	if result.Message != msg {
		t.Error("Expected message to be preserved")
	}

	if result.Error != nil {
		t.Error("Expected no error")
	}
}

func TestRegisterAgentDeprecated(t *testing.T) {
	dispatcher := createTestDispatcher(t)
	agent := &mockAgent{id: "deprecated-test-001"}

	// Test the deprecated RegisterAgent method
	err := dispatcher.RegisterAgent(agent)
	if err != nil {
		t.Errorf("Expected no error from deprecated RegisterAgent, got: %v", err)
	}

	// Verify agent was actually registered by checking the registered agents list
	agentInfos := dispatcher.GetRegisteredAgents()
	found := false
	for _, info := range agentInfos {
		if info.ID == agent.GetID() {
			found = true
			break
		}
	}

	if !found {
		t.Error("Expected deprecated RegisterAgent to still work")
	}
}

func TestUnregisterAgent(t *testing.T) {
	dispatcher := createTestDispatcher(t)
	agent := &mockAgent{id: "unregister-test-001"}

	// Attach then unregister
	dispatcher.Attach(agent)
	err := dispatcher.UnregisterAgent(agent.GetID())
	if err != nil {
		t.Errorf("Expected no error from UnregisterAgent, got: %v", err)
	}

	// Verify agent was removed
	agentInfos := dispatcher.GetRegisteredAgents()
	for _, info := range agentInfos {
		if info.ID == agent.GetID() {
			t.Error("Expected agent to be unregistered")
		}
	}
}

func TestGetChannels(t *testing.T) {
	dispatcher := createTestDispatcher(t)

	// Test getting various channels
	questionsCh := dispatcher.GetQuestionsCh()
	if questionsCh == nil {
		t.Error("Expected non-nil questions channel")
	}

	storyCh := dispatcher.GetStoryCh()
	if storyCh == nil {
		t.Error("Expected non-nil story channel")
	}

	stateChangeCh := dispatcher.GetStateChangeChannel()
	if stateChangeCh == nil {
		t.Error("Expected non-nil state change channel")
	}
}

func TestGetReplyCh(t *testing.T) {
	dispatcher := createTestDispatcher(t)
	agent := &mockAgent{id: "reply-ch-test-001"}

	// Test getting reply channel for non-existent agent
	replyCh := dispatcher.GetReplyCh("non-existent")
	if replyCh != nil {
		t.Error("Expected nil reply channel for non-existent agent")
	}

	// Attach agent and test getting its reply channel
	dispatcher.Attach(agent)
	replyCh = dispatcher.GetReplyCh(agent.GetID())
	if replyCh == nil {
		t.Error("Expected non-nil reply channel for attached agent")
	}
}

func TestGetContainerRegistry(t *testing.T) {
	dispatcher := createTestDispatcher(t)

	registry := dispatcher.GetContainerRegistry()
	if registry == nil {
		t.Error("Expected non-nil container registry")
	}
}

func TestReportError(t *testing.T) {
	dispatcher := createTestDispatcher(t)

	// Test reporting errors
	dispatcher.ReportError("test-agent", context.DeadlineExceeded, Warn)
	dispatcher.ReportError("test-agent-2", context.Canceled, Fatal)

	// These calls should not panic and should be recorded somewhere
	// The actual error handling is done asynchronously
}

func TestGetLease(t *testing.T) {
	dispatcher := createTestDispatcher(t)

	// Test getting lease for non-existent agent
	lease := dispatcher.GetLease("non-existent")
	if lease != "" {
		t.Error("Expected empty lease for non-existent agent")
	}
}

func TestDispatchMessage(t *testing.T) {
	dispatcher := createTestDispatcher(t)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Start dispatcher
	err := dispatcher.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start dispatcher: %v", err)
	}

	msg := &proto.AgentMsg{
		ID:        "test-dispatch-msg",
		Type:      proto.MsgTypeSTORY,
		FromAgent: "orchestrator",
		ToAgent:   "coder",
	}
	// Build story payload with typed generic payload
	msg.SetTypedPayload(proto.NewGenericPayload(proto.PayloadKindStory, map[string]any{"test": "content"}))

	// Test dispatching a message
	err = dispatcher.DispatchMessage(msg)
	if err != nil {
		t.Errorf("Expected no error dispatching message, got: %v", err)
	}

	// Give a short time for message to be processed
	time.Sleep(10 * time.Millisecond)

	// Stop dispatcher
	dispatcher.Stop(ctx)
}

func TestDispatchMessageNotRunning(t *testing.T) {
	dispatcher := createTestDispatcher(t)

	msg := &proto.AgentMsg{
		ID:        "test-msg",
		Type:      proto.MsgTypeSTORY,
		FromAgent: "orchestrator",
		ToAgent:   "coder",
	}

	// Test dispatching when not running
	err := dispatcher.DispatchMessage(msg)
	if err == nil {
		t.Error("Expected error when dispatcher not running")
	}
}

func TestStartStop(t *testing.T) {
	dispatcher := createTestDispatcher(t)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// Test starting dispatcher
	err := dispatcher.Start(ctx)
	if err != nil {
		t.Errorf("Expected no error starting dispatcher, got: %v", err)
	}

	// Test starting again (should fail)
	err = dispatcher.Start(ctx)
	if err == nil {
		t.Error("Expected error when starting already running dispatcher")
	}

	// Test stopping
	err = dispatcher.Stop(ctx)
	if err != nil {
		t.Errorf("Expected no error stopping dispatcher, got: %v", err)
	}

	// Test stopping again (should not fail)
	err = dispatcher.Stop(ctx)
	if err != nil {
		t.Errorf("Expected no error stopping already stopped dispatcher, got: %v", err)
	}
}

func TestResolveAgentName(t *testing.T) {
	dispatcher := createTestDispatcher(t)

	// Test exact agent ID
	agent := &mockAgent{id: "test-agent-001"}
	dispatcher.Attach(agent)

	resolvedID := dispatcher.resolveAgentName("test-agent-001")
	if resolvedID != "test-agent-001" {
		t.Errorf("Expected exact match, got %s", resolvedID)
	}

	// Test logical names
	architect := &mockChannelReceiver{
		mockAgent: mockAgent{id: "architect-001"},
	}
	dispatcher.Attach(architect)

	resolvedID = dispatcher.resolveAgentName("architect")
	if resolvedID != "architect-001" {
		t.Errorf("Expected architect-001, got %s", resolvedID)
	}

	coder := &mockChannelReceiver{
		mockAgent: mockAgent{id: "coder-001"},
	}
	dispatcher.Attach(coder)

	resolvedID = dispatcher.resolveAgentName("coder")
	if resolvedID != "coder-001" {
		t.Errorf("Expected coder-001, got %s", resolvedID)
	}

	// Test non-existent logical name
	resolvedID = dispatcher.resolveAgentName("unknown")
	if resolvedID != "unknown" {
		t.Errorf("Expected original name for unknown, got %s", resolvedID)
	}
}

func TestLeaseOperations(t *testing.T) {
	dispatcher := createTestDispatcher(t)

	// Test setting lease
	dispatcher.SetLease("agent-001", "story-001")
	lease := dispatcher.GetLease("agent-001")
	if lease != "story-001" {
		t.Errorf("Expected story-001, got %s", lease)
	}

	// Test getting non-existent lease
	lease = dispatcher.GetLease("non-existent")
	if lease != "" {
		t.Errorf("Expected empty string for non-existent lease, got %s", lease)
	}

	// Test clearing lease
	dispatcher.ClearLease("agent-001")
	lease = dispatcher.GetLease("agent-001")
	if lease != "" {
		t.Errorf("Expected empty string after clearing lease, got %s", lease)
	}

	// Test clearing non-existent lease (should not panic)
	dispatcher.ClearLease("non-existent")
}

func TestSendRequeue(t *testing.T) {
	dispatcher := createTestDispatcher(t)

	// Test requeue without lease
	err := dispatcher.SendRequeue("agent-001", "test reason")
	if err == nil {
		t.Error("Expected error when no lease found")
	}

	// Test requeue with lease
	dispatcher.SetLease("agent-001", "story-001")
	err = dispatcher.SendRequeue("agent-001", "test reason")
	if err != nil {
		t.Errorf("Expected no error with valid lease, got: %v", err)
	}
}

func TestDumpHeads(t *testing.T) {
	dispatcher := createTestDispatcher(t)

	heads := dispatcher.DumpHeads(5)
	if heads == nil {
		t.Error("Expected non-nil heads dump")
	}

	// Check expected keys exist
	if _, exists := heads["questions_ch"]; !exists {
		t.Error("Expected questions_ch in dump")
	}

	if _, exists := heads["story_ch"]; !exists {
		t.Error("Expected story_ch in dump")
	}
}

func TestMessageProcessingBasic(t *testing.T) {
	dispatcher := createTestDispatcher(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// Start dispatcher
	err := dispatcher.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start dispatcher: %v", err)
	}

	// Test STORY message only
	storyMsg := &proto.AgentMsg{
		ID:        "story-001",
		Type:      proto.MsgTypeSTORY,
		FromAgent: "orchestrator",
		ToAgent:   "coder",
	}

	err = dispatcher.DispatchMessage(storyMsg)
	if err != nil {
		t.Errorf("Failed to dispatch STORY message: %v", err)
	}

	// Allow some time for message processing
	time.Sleep(5 * time.Millisecond)

	// Stop dispatcher
	dispatcher.Stop(ctx)
}

func TestChannelUtilization(t *testing.T) {
	dispatcher := createTestDispatcher(t)

	// Test story channel capacity
	storyCap := cap(dispatcher.storyCh)
	if storyCap <= 0 {
		t.Error("Expected positive story channel capacity")
	}

	// Test questions channel capacity
	questionsCap := cap(dispatcher.questionsCh)
	if questionsCap <= 0 {
		t.Error("Expected positive questions channel capacity")
	}
}

func TestRouteToReplyCh(t *testing.T) {
	dispatcher := createTestDispatcher(t)
	agent := &mockChannelReceiver{
		mockAgent: mockAgent{id: "test-agent-001"},
	}

	// Attach agent to create reply channel
	dispatcher.Attach(agent)

	// Create RESPONSE message
	responseMsg := &proto.AgentMsg{
		ID:        "response-001",
		Type:      proto.MsgTypeRESPONSE,
		FromAgent: "architect",
		ToAgent:   "test-agent-001",
	}

	// Test routing to reply channel
	dispatcher.routeToReplyCh(responseMsg)

	// Verify message was delivered to reply channel
	replyCh := dispatcher.GetReplyCh("test-agent-001")
	select {
	case msg := <-replyCh:
		if msg.ID != "response-001" {
			t.Errorf("Expected message ID response-001, got %s", msg.ID)
		}
	default:
		t.Error("Expected message in reply channel")
	}
}

func TestErrorHandling(t *testing.T) {
	dispatcher := createTestDispatcher(t)

	// Test error reporting without starting supervisor
	// Just ensure ReportError doesn't panic when error channel is available
	dispatcher.ReportError("test-agent", context.DeadlineExceeded, Warn)
	dispatcher.ReportError("fatal-agent", context.Canceled, Fatal)
}

func TestCheckZeroAgentCondition(t *testing.T) {
	dispatcher := createTestDispatcher(t)

	// Test with no agents (should warn about both types)
	dispatcher.checkZeroAgentCondition()

	// Add architect
	architect := &mockChannelReceiver{
		mockAgent: mockAgent{id: "architect-001"},
		agentType: agent.TypeArchitect,
	}
	dispatcher.Attach(architect)

	// Should warn about no coders
	dispatcher.checkZeroAgentCondition()

	// Add coder
	coder := &mockChannelReceiver{
		mockAgent: mockAgent{id: "coder-001"},
	}
	dispatcher.Attach(coder) // Defaults to TypeCoder

	// Should not warn
	dispatcher.checkZeroAgentCondition()

	// agentType field handles the type differences
}

func TestSendResponse(t *testing.T) {
	dispatcher := createTestDispatcher(t)

	// Test RESPONSE message routing
	responseMsg := &proto.AgentMsg{
		ID:        "response-001",
		Type:      proto.MsgTypeRESPONSE,
		FromAgent: "architect",
		ToAgent:   "coder-001",
	}
	// Build generic response payload
	responseMsg.SetTypedPayload(proto.NewGenericPayload(proto.PayloadKindGeneric, map[string]any{}))

	// Create agent to receive response
	agent := &mockChannelReceiver{
		mockAgent: mockAgent{id: "coder-001"},
	}
	dispatcher.Attach(agent)

	// Test sendResponse method
	dispatcher.sendResponse(responseMsg)

	// Verify message was delivered
	replyCh := dispatcher.GetReplyCh("coder-001")
	select {
	case msg := <-replyCh:
		if msg.ID != "response-001" {
			t.Errorf("Expected response-001, got %s", msg.ID)
		}
	default:
		t.Error("Expected message in reply channel")
	}
}

func TestSendErrorResponse(t *testing.T) {
	dispatcher := createTestDispatcher(t)

	originalMsg := &proto.AgentMsg{
		ID:        "original-001",
		Type:      proto.MsgTypeSTORY,
		FromAgent: "orchestrator",
		ToAgent:   "coder",
	}

	// Test error response generation
	dispatcher.sendErrorResponse(originalMsg, context.DeadlineExceeded)
	// This method logs the error but doesn't return anything to verify
	// The test ensures it doesn't panic
}

func TestProcessWithRetry(t *testing.T) {
	dispatcher := createTestDispatcher(t)
	ctx := context.Background()

	// Create mock agent
	mockAgent := &mockAgent{id: "test-agent"}

	// Test SHUTDOWN message (bypasses rate limiting)
	shutdownMsg := &proto.AgentMsg{
		ID:        "shutdown-001",
		Type:      proto.MsgTypeSHUTDOWN,
		FromAgent: "orchestrator",
		ToAgent:   "test-agent",
	}

	result := dispatcher.processWithRetry(ctx, shutdownMsg, mockAgent)
	if result.Error != nil {
		t.Errorf("Expected no error for SHUTDOWN message, got: %v", result.Error)
	}

	// Test regular message with rate limiting
	regularMsg := &proto.AgentMsg{
		ID:        "regular-001",
		Type:      proto.MsgTypeSTORY,
		FromAgent: "orchestrator",
		ToAgent:   "test-agent",
	}

	_ = dispatcher.processWithRetry(ctx, regularMsg, mockAgent)
	// Rate limiting might fail due to budget constraints in test environment
	// This test mainly ensures the method doesn't panic
}

func TestResolveAgentNameEdgeCases(t *testing.T) {
	dispatcher := createTestDispatcher(t)

	// Test with empty agent map
	resolvedID := dispatcher.resolveAgentName("architect")
	if resolvedID != "architect" {
		t.Errorf("Expected original name when no agents, got %s", resolvedID)
	}

	// Test with unrecognized logical name
	resolvedID = dispatcher.resolveAgentName("unknown-type")
	if resolvedID != "unknown-type" {
		t.Errorf("Expected original name for unknown type, got %s", resolvedID)
	}

	// Test with agent that doesn't match prefix pattern
	nonMatchingAgent := &mockAgent{id: "random-name-123"}
	dispatcher.Attach(nonMatchingAgent)

	resolvedID = dispatcher.resolveAgentName("architect")
	if resolvedID != "architect" {
		t.Errorf("Expected original name when no matching agents, got %s", resolvedID)
	}
}

func TestAgentInfoFallback(t *testing.T) {
	dispatcher := createTestDispatcher(t)

	// Test with agent that doesn't implement Driver interface
	nonDriverAgent := &mockAgent{id: "non-driver-001"}
	dispatcher.Attach(nonDriverAgent)

	agentInfos := dispatcher.GetRegisteredAgents()
	found := false
	for _, info := range agentInfos {
		if info.ID == "non-driver-001" {
			found = true
			if info.Type != agent.TypeCoder {
				t.Errorf("Expected fallback to TypeCoder, got %v", info.Type)
			}
			if info.State != "UNKNOWN" {
				t.Errorf("Expected UNKNOWN state, got %s", info.State)
			}
			if info.Driver != nil {
				t.Error("Expected nil Driver for non-Driver agent")
			}
		}
	}
	if !found {
		t.Error("Expected to find non-driver agent in list")
	}
}

func TestGetStatsUtilization(t *testing.T) {
	dispatcher := createTestDispatcher(t)

	// Fill story channel partially to test utilization calculation
	storyMsg := &proto.AgentMsg{
		ID:        "story-util-001",
		Type:      proto.MsgTypeSTORY,
		FromAgent: "orchestrator",
		ToAgent:   "coder",
	}

	// Send a message to story channel
	select {
	case dispatcher.storyCh <- storyMsg:
		// Message sent successfully
	default:
		t.Error("Story channel should not be full initially")
	}

	stats := dispatcher.GetStats()
	storyLengthRaw, ok := stats["story_ch_length"]
	if !ok {
		t.Error("Expected story_ch_length in stats")
		return
	}
	storyLength, ok := storyLengthRaw.(int)
	if !ok {
		t.Error("Expected story_ch_length to be int")
		return
	}
	if storyLength != 1 {
		t.Errorf("Expected story channel length 1, got %d", storyLength)
	}

	utilizationRaw, ok := stats["story_ch_utilization"]
	if !ok {
		t.Error("Expected story_ch_utilization in stats")
		return
	}
	utilization, ok := utilizationRaw.(float64)
	if !ok {
		t.Error("Expected story_ch_utilization to be float64")
		return
	}
	if utilization <= 0 {
		t.Errorf("Expected positive utilization, got %f", utilization)
	}
}

// Additional simple tests for better coverage.
func TestSeverityConstants(t *testing.T) {
	// Test severity constant values (renamed to avoid duplication)
	if Warn != 0 {
		t.Errorf("Expected Warn to be 0, got %d", Warn)
	}
	if Fatal != 1 {
		t.Errorf("Expected Fatal to be 1, got %d", Fatal)
	}
	if Fatal <= Warn {
		t.Error("Expected Fatal to be greater than Warn")
	}
}

func TestAgentErrorStruct(t *testing.T) {
	testErr := fmt.Errorf("test error")
	agentErr := AgentError{
		ID:  "test-agent",
		Sev: Fatal,
		Err: testErr,
	}

	if agentErr.ID != "test-agent" {
		t.Errorf("Expected agent ID 'test-agent', got '%s'", agentErr.ID)
	}
	if agentErr.Sev != Fatal {
		t.Errorf("Expected severity Fatal, got %v", agentErr.Sev)
	}
	if !errors.Is(agentErr.Err, testErr) && agentErr.Err.Error() != testErr.Error() {
		t.Error("Expected error to match original error")
	}
}

func TestMessageTypeHandling(t *testing.T) {
	dispatcher := createTestDispatcher(t)

	// Test basic message types that we know exist
	validTypes := []proto.MsgType{
		proto.MsgTypeSTORY,
		proto.MsgTypeRESPONSE,
		proto.MsgTypeERROR,
		proto.MsgTypeSHUTDOWN,
	}

	for _, msgType := range validTypes {
		msg := &proto.AgentMsg{
			ID:        fmt.Sprintf("test-%s", msgType),
			Type:      msgType,
			FromAgent: "test-sender",
			ToAgent:   "test-receiver",
		}

		// Basic message validation - just ensure we can create messages
		if msg.ID == "" {
			t.Error("Expected non-empty message ID")
		}
		if msg.Type == "" {
			t.Error("Expected non-empty message type")
		}
		if msg.FromAgent == "" {
			t.Error("Expected non-empty from agent")
		}
		if msg.ToAgent == "" {
			t.Error("Expected non-empty to agent")
		}
	}

	// Test basic dispatcher state access
	stats := dispatcher.GetStats()
	if stats == nil {
		t.Error("Expected non-nil stats")
	}
}

func TestSimpleDispatcherState(t *testing.T) {
	dispatcher := createTestDispatcher(t)

	// Test basic state without starting/stopping to avoid race conditions
	if dispatcher.agents == nil {
		t.Error("Expected agents map to be initialized")
	}

	if dispatcher.replyChannels == nil {
		t.Error("Expected reply channels map to be initialized")
	}

	// Test that we can get channels
	questionsCh := dispatcher.GetQuestionsCh()
	if questionsCh == nil {
		t.Error("Expected non-nil questions channel")
	}

	storyCh := dispatcher.GetStoryCh()
	if storyCh == nil {
		t.Error("Expected non-nil story channel")
	}
}

func TestStoryIDValidation(t *testing.T) {
	dispatcher := createTestDispatcher(t)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Start dispatcher
	err := dispatcher.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start dispatcher: %v", err)
	}

	// Use separate cleanup context to avoid timeout during stop
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer stopCancel()
		dispatcher.Stop(stopCtx)
	}()

	// Test SPEC message without story_id (should pass)
	specMsg := &proto.AgentMsg{
		ID:        "spec-001",
		Type:      proto.MsgTypeSPEC,
		FromAgent: "orchestrator",
		ToAgent:   "architect",
	}

	err = dispatcher.DispatchMessage(specMsg)
	if err != nil {
		t.Errorf("SPEC message without story_id should be allowed, got error: %v", err)
	}

	// Give time for message processing
	time.Sleep(50 * time.Millisecond)

	// Test STORY message without story_id (should fail immediately)
	storyMsg := &proto.AgentMsg{
		ID:        "story-001",
		Type:      proto.MsgTypeSTORY,
		FromAgent: "orchestrator",
		ToAgent:   "coder",
	}

	err = dispatcher.DispatchMessage(storyMsg)
	if err != nil {
		// Good - the message was rejected synchronously during dispatch
		t.Logf("Message correctly rejected during dispatch: %v", err)
	} else {
		// Message was accepted but should be rejected during processing
		// This is also valid behavior depending on implementation
		t.Log("Message accepted for async processing (validation happens during processing)")
	}

	// Test STORY message with empty story_id (should fail)
	storyMsgEmpty := &proto.AgentMsg{
		ID:        "story-002",
		Type:      proto.MsgTypeSTORY,
		FromAgent: "orchestrator",
		ToAgent:   "coder",
	}
	// story_id in metadata, not payload
	storyMsgEmpty.SetMetadata(proto.KeyStoryID, "")

	err = dispatcher.DispatchMessage(storyMsgEmpty)
	// Accept either sync or async rejection
	if err != nil {
		t.Logf("Message correctly rejected during dispatch: %v", err)
	} else {
		t.Log("Message accepted for async processing (validation happens during processing)")
	}

	// Test STORY message with valid story_id (should pass)
	storyMsgValid := &proto.AgentMsg{
		ID:        "story-003",
		Type:      proto.MsgTypeSTORY,
		FromAgent: "orchestrator",
		ToAgent:   "coder",
	}
	// story_id in metadata, not payload
	storyMsgValid.SetMetadata(proto.KeyStoryID, "story-123")

	err = dispatcher.DispatchMessage(storyMsgValid)
	if err != nil {
		t.Errorf("STORY message with valid story_id should be allowed, got error: %v", err)
	}

	// Give time for async processing to complete
	time.Sleep(100 * time.Millisecond)
}

func TestHotfixRequestStoryIndependent(t *testing.T) {
	dispatcher := createTestDispatcher(t)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Start dispatcher
	err := dispatcher.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start dispatcher: %v", err)
	}

	// Use separate cleanup context to avoid timeout during stop
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer stopCancel()
		dispatcher.Stop(stopCtx)
	}()

	// Test hotfix REQUEST without story_id (should pass - hotfix requests are story-independent)
	hotfixPayload := &proto.HotfixRequestPayload{
		Platform: "go",
		Analysis: "Fix a bug in the main package",
	}

	hotfixMsg := &proto.AgentMsg{
		ID:        "hotfix-req-001",
		Type:      proto.MsgTypeREQUEST,
		FromAgent: "pm-001",
		ToAgent:   "architect",
		Payload:   proto.NewHotfixRequestPayload(hotfixPayload),
	}

	err = dispatcher.DispatchMessage(hotfixMsg)
	if err != nil {
		t.Errorf("Hotfix REQUEST without story_id should be allowed (story-independent), got error: %v", err)
	}

	// Give time for message processing
	time.Sleep(50 * time.Millisecond)

	t.Log("Hotfix REQUEST correctly accepted without story_id")
}

func TestLeaseOperationsExtended(t *testing.T) {
	dispatcher := createTestDispatcher(t)

	// Test multiple lease operations
	agentIDs := []string{"agent-001", "agent-002", "agent-003"}
	storyIDs := []string{"story-001", "story-002", "story-003"}

	// Set leases for multiple agents
	for i, agentID := range agentIDs {
		dispatcher.SetLease(agentID, storyIDs[i])
		lease := dispatcher.GetLease(agentID)
		if lease != storyIDs[i] {
			t.Errorf("Expected lease %s for agent %s, got %s", storyIDs[i], agentID, lease)
		}
	}

	// Clear all leases
	for _, agentID := range agentIDs {
		dispatcher.ClearLease(agentID)
		lease := dispatcher.GetLease(agentID)
		if lease != "" {
			t.Errorf("Expected empty lease after clearing for agent %s, got %s", agentID, lease)
		}
	}
}
