package dispatch

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/eventlog"
	"orchestrator/pkg/limiter"
	"orchestrator/pkg/proto"
)

// MockAgent implements the Agent interface for testing
type MockAgent struct {
	id              string
	responses       map[string]*proto.AgentMsg
	errors          map[string]error
	processDelay    time.Duration
	callCount       int
	customProcessor func(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error)
	mu              sync.Mutex
}

func NewMockAgent(id string) *MockAgent {
	return &MockAgent{
		id:        id,
		responses: make(map[string]*proto.AgentMsg),
		errors:    make(map[string]error),
	}
}

func (a *MockAgent) GetID() string {
	return a.id
}

func (a *MockAgent) SetResponse(msgType proto.MsgType, response *proto.AgentMsg) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.responses[string(msgType)] = response
}

func (a *MockAgent) SetError(msgType proto.MsgType, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.errors[string(msgType)] = err
}

func (a *MockAgent) SetProcessDelay(delay time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.processDelay = delay
}

func (a *MockAgent) ProcessMessage(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.callCount++

	// Use custom processor if set
	if a.customProcessor != nil {
		return a.customProcessor(ctx, msg)
	}

	// Simulate processing delay
	if a.processDelay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(a.processDelay):
		}
	}

	// Check for configured error
	if err, exists := a.errors[string(msg.Type)]; exists {
		return nil, err
	}

	// Return configured response
	if response, exists := a.responses[string(msg.Type)]; exists {
		return response.Clone(), nil
	}

	// Default response
	return proto.NewAgentMsg(proto.MsgTypeRESULT, a.id, msg.FromAgent), nil
}

func (a *MockAgent) Shutdown(ctx context.Context) error {
	return nil
}

func (a *MockAgent) SetCustomProcessor(processor func(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.customProcessor = processor
}

func (a *MockAgent) GetCallCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.callCount
}

func createTestConfig() *config.Config {
	return &config.Config{
		Models: map[string]config.ModelCfg{
			"claude": {
				MaxTokensPerMinute: 1000,
				MaxBudgetPerDayUSD: 25.0,
				MaxAgents:          3,
				APIKey:             "test-key",
			},
		},
		MaxRetryAttempts:       3,
		RetryBackoffMultiplier: 2.0,
	}
}

func createTestDispatcher(t *testing.T) (*Dispatcher, *eventlog.Writer, func()) {
	cfg := createTestConfig()

	// Create rate limiter
	rateLimiter := limiter.NewLimiter(cfg)

	// Create event log
	tmpDir := t.TempDir()
	eventLog, err := eventlog.NewWriter(tmpDir, 24)
	if err != nil {
		t.Fatalf("Failed to create event log: %v", err)
	}

	// Create dispatcher
	dispatcher, err := NewDispatcher(cfg, rateLimiter, eventLog)
	if err != nil {
		t.Fatalf("Failed to create dispatcher: %v", err)
	}

	cleanup := func() {
		rateLimiter.Close()
		eventLog.Close()
	}

	return dispatcher, eventLog, cleanup
}

func TestNewDispatcher(t *testing.T) {
	dispatcher, _, cleanup := createTestDispatcher(t)
	defer cleanup()

	if dispatcher == nil {
		t.Fatal("Expected dispatcher to be created")
	}

	stats := dispatcher.GetStats()
	if stats["running"].(bool) {
		t.Error("Expected dispatcher to not be running initially")
	}

	if len(stats["agents"].([]string)) != 0 {
		t.Error("Expected no agents registered initially")
	}
}

func TestRegisterAgent(t *testing.T) {
	dispatcher, _, cleanup := createTestDispatcher(t)
	defer cleanup()

	agent := NewMockAgent("claude")

	err := dispatcher.RegisterAgent(agent)
	if err != nil {
		t.Fatalf("Failed to register agent: %v", err)
	}

	stats := dispatcher.GetStats()
	agents := stats["agents"].([]string)
	if len(agents) != 1 || agents[0] != "claude" {
		t.Errorf("Expected agent 'claude' to be registered, got %v", agents)
	}

	// Test registering duplicate agent
	err = dispatcher.RegisterAgent(agent)
	if err == nil {
		t.Error("Expected error when registering duplicate agent")
	}
}

func TestUnregisterAgent(t *testing.T) {
	dispatcher, _, cleanup := createTestDispatcher(t)
	defer cleanup()

	agent := NewMockAgent("claude")
	dispatcher.RegisterAgent(agent)

	err := dispatcher.UnregisterAgent("claude")
	if err != nil {
		t.Fatalf("Failed to unregister agent: %v", err)
	}

	stats := dispatcher.GetStats()
	agents := stats["agents"].([]string)
	if len(agents) != 0 {
		t.Errorf("Expected no agents after unregistering, got %v", agents)
	}

	// Test unregistering non-existent agent
	err = dispatcher.UnregisterAgent("nonexistent")
	if err == nil {
		t.Error("Expected error when unregistering non-existent agent")
	}
}

func TestStartStop(t *testing.T) {
	dispatcher, _, cleanup := createTestDispatcher(t)
	defer cleanup()

	ctx := context.Background()

	// Test start
	err := dispatcher.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start dispatcher: %v", err)
	}

	stats := dispatcher.GetStats()
	if !stats["running"].(bool) {
		t.Error("Expected dispatcher to be running after start")
	}

	// Test double start
	err = dispatcher.Start(ctx)
	if err == nil {
		t.Error("Expected error when starting already running dispatcher")
	}

	// Test stop
	stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	err = dispatcher.Stop(stopCtx)
	if err != nil {
		t.Fatalf("Failed to stop dispatcher: %v", err)
	}

	stats = dispatcher.GetStats()
	if stats["running"].(bool) {
		t.Error("Expected dispatcher to not be running after stop")
	}
}

func TestDispatchMessage(t *testing.T) {
	dispatcher, eventLog, cleanup := createTestDispatcher(t)
	defer cleanup()

	// Start dispatcher
	ctx := context.Background()
	dispatcher.Start(ctx)
	defer dispatcher.Stop(ctx)

	// Register mock agent
	agent := NewMockAgent("claude")
	response := proto.NewAgentMsg(proto.MsgTypeRESULT, "claude", "architect")
	response.SetPayload("status", "completed")
	agent.SetResponse(proto.MsgTypeTASK, response)

	dispatcher.RegisterAgent(agent)

	// Create and dispatch message
	msg := proto.NewAgentMsg(proto.MsgTypeTASK, "architect", "claude")
	msg.SetPayload("content", "test task")

	err := dispatcher.DispatchMessage(msg)
	if err != nil {
		t.Fatalf("Failed to dispatch message: %v", err)
	}

	// Wait for processing
	time.Sleep(100 * time.Millisecond)

	// Verify agent was called
	if agent.GetCallCount() != 1 {
		t.Errorf("Expected agent to be called once, got %d calls", agent.GetCallCount())
	}

	// Verify message was logged
	logFile := eventLog.GetCurrentLogFile()
	messages, err := eventlog.ReadMessages(logFile)
	if err != nil {
		t.Fatalf("Failed to read event log: %v", err)
	}

	if len(messages) < 1 {
		t.Error("Expected at least one message in event log")
	}
}

func TestRetryLogic(t *testing.T) {
	dispatcher, _, cleanup := createTestDispatcher(t)
	defer cleanup()

	ctx := context.Background()
	dispatcher.Start(ctx)
	defer dispatcher.Stop(ctx)

	// Register mock agent that fails first two times
	agent := NewMockAgent("claude")
	failCount := 0

	// Set custom processor to succeed on third attempt
	agent.SetCustomProcessor(func(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
		// Don't use agent.mu here to avoid deadlock - failCount is already captured
		failCount++
		count := failCount

		if count < 3 {
			return nil, fmt.Errorf("attempt %d failed", count)
		}

		// Succeed on third attempt
		return proto.NewAgentMsg(proto.MsgTypeRESULT, "claude", "architect"), nil
	})
	defer agent.SetCustomProcessor(nil)

	dispatcher.RegisterAgent(agent)

	// Dispatch message
	msg := proto.NewAgentMsg(proto.MsgTypeTASK, "architect", "claude")
	err := dispatcher.DispatchMessage(msg)
	if err != nil {
		t.Fatalf("Failed to dispatch message: %v", err)
	}

	// Wait for processing with retries
	time.Sleep(1500 * time.Millisecond) // Allow time for retries with backoff

	// Verify agent was called multiple times
	if agent.GetCallCount() < 2 {
		t.Errorf("Expected agent to be called at least 2 times, got %d calls", agent.GetCallCount())
	}
}

func TestAgentNotFound(t *testing.T) {
	dispatcher, eventLog, cleanup := createTestDispatcher(t)
	defer cleanup()

	ctx := context.Background()
	dispatcher.Start(ctx)
	defer dispatcher.Stop(ctx)

	// Dispatch message to non-existent agent
	msg := proto.NewAgentMsg(proto.MsgTypeTASK, "architect", "nonexistent")
	err := dispatcher.DispatchMessage(msg)
	if err != nil {
		t.Fatalf("Failed to dispatch message: %v", err)
	}

	// Wait for processing
	time.Sleep(100 * time.Millisecond)

	// Verify error message was logged
	logFile := eventLog.GetCurrentLogFile()
	messages, err := eventlog.ReadMessages(logFile)
	if err != nil {
		t.Fatalf("Failed to read event log: %v", err)
	}

	// Should have original message and error response
	if len(messages) < 2 {
		t.Errorf("Expected at least 2 messages in event log, got %d", len(messages))
	}

	// Check for error message
	hasError := false
	for _, loggedMsg := range messages {
		if loggedMsg.Type == proto.MsgTypeERROR {
			hasError = true
			break
		}
	}

	if !hasError {
		t.Error("Expected error message to be logged")
	}
}

func TestConcurrentMessages(t *testing.T) {
	dispatcher, _, cleanup := createTestDispatcher(t)
	defer cleanup()

	ctx := context.Background()
	dispatcher.Start(ctx)
	defer dispatcher.Stop(ctx)

	// Register mock agent with processing delay
	agent := NewMockAgent("claude")
	agent.SetProcessDelay(50 * time.Millisecond)
	dispatcher.RegisterAgent(agent)

	// Dispatch multiple messages concurrently
	numMessages := 10
	var wg sync.WaitGroup

	for i := 0; i < numMessages; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			msg := proto.NewAgentMsg(proto.MsgTypeTASK, "architect", "claude")
			msg.SetPayload("id", id)

			err := dispatcher.DispatchMessage(msg)
			if err != nil {
				t.Errorf("Failed to dispatch message %d: %v", id, err)
			}
		}(i)
	}

	wg.Wait()

	// Wait for all messages to be processed
	time.Sleep(2 * time.Second)

	// Verify all messages were processed
	if agent.GetCallCount() != numMessages {
		t.Errorf("Expected %d messages to be processed, got %d", numMessages, agent.GetCallCount())
	}
}

func TestQueueFull(t *testing.T) {
	// Create dispatcher with small queue for testing
	cfg := createTestConfig()
	rateLimiter := limiter.NewLimiter(cfg)
	defer rateLimiter.Close()

	tmpDir := t.TempDir()
	eventLog, _ := eventlog.NewWriter(tmpDir, 24)
	defer eventLog.Close()

	dispatcher, _ := NewDispatcher(cfg, rateLimiter, eventLog)

	// Don't start the dispatcher so messages can be queued but not processed

	// Fill the queue
	queueCap := cap(dispatcher.inputChan)

	// We need to manually test the DispatchMessage function's queue behavior
	// Since DispatchMessage checks if running, we'll test the channel directly
	for i := 0; i < queueCap; i++ {
		msg := proto.NewAgentMsg(proto.MsgTypeTASK, "architect", "claude")
		select {
		case dispatcher.inputChan <- msg:
			// Successfully queued
		default:
			t.Fatalf("Queue should not be full at message %d", i)
		}
	}

	// Next message should fail to queue
	msg := proto.NewAgentMsg(proto.MsgTypeTASK, "architect", "claude")
	select {
	case dispatcher.inputChan <- msg:
		t.Error("Expected queue to be full")
	default:
		// Expected - queue is full
	}
}

func TestDispatcherStats(t *testing.T) {
	dispatcher, _, cleanup := createTestDispatcher(t)
	defer cleanup()

	ctx := context.Background()

	// Initial stats
	stats := dispatcher.GetStats()
	if stats["running"].(bool) {
		t.Error("Expected dispatcher to not be running initially")
	}
	if stats["queue_length"].(int) != 0 {
		t.Error("Expected empty queue initially")
	}

	// Start and register agent
	dispatcher.Start(ctx)
	defer dispatcher.Stop(ctx)

	agent := NewMockAgent("claude")
	dispatcher.RegisterAgent(agent)

	// Check updated stats
	stats = dispatcher.GetStats()
	if !stats["running"].(bool) {
		t.Error("Expected dispatcher to be running")
	}

	agents := stats["agents"].([]string)
	if len(agents) != 1 || agents[0] != "claude" {
		t.Errorf("Expected one agent 'claude', got %v", agents)
	}
}
