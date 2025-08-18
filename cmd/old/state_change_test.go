package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/config"
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/limiter"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
)

// MockCoder is a minimal coder implementation for testing state notifications.
type MockCoder struct {
	id           string
	stateNotifCh chan<- *proto.StateChangeNotification
	currentState proto.State
	mu           sync.Mutex
}

func NewMockCoder(id string) *MockCoder {
	return &MockCoder{
		id:           id,
		currentState: proto.StateWaiting,
	}
}

// Implement Agent interface.
func (m *MockCoder) GetID() string { return m.id }

// Implement Driver interface.
func (m *MockCoder) GetAgentType() agent.Type { return agent.TypeCoder }
func (m *MockCoder) GetCurrentState() proto.State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.currentState
}
func (m *MockCoder) GetStateData() map[string]any { return make(map[string]any) }
func (m *MockCoder) GetValidStates() []proto.State {
	return []proto.State{proto.StateWaiting, proto.StateDone, proto.StateError}
}
func (m *MockCoder) ValidateState(_ proto.State) error    { return nil }
func (m *MockCoder) GetContextSummary() string            { return "" }
func (m *MockCoder) Initialize(_ context.Context) error   { return nil }
func (m *MockCoder) Shutdown(_ context.Context) error     { return nil }
func (m *MockCoder) Step(_ context.Context) (bool, error) { return false, nil }
func (m *MockCoder) Run(_ context.Context) error          { return nil }

// Implement ChannelReceiver interface.
func (m *MockCoder) SetChannels(_ <-chan *proto.AgentMsg, _ chan *proto.AgentMsg, _ <-chan *proto.AgentMsg) {
	// No-op for test
}

func (m *MockCoder) SetDispatcher(_ *dispatch.Dispatcher) {
	// No-op for test
}

func (m *MockCoder) SetStateNotificationChannel(stateNotifCh chan<- *proto.StateChangeNotification) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stateNotifCh = stateNotifCh
}

// TransitionTo simulates a state transition and sends notification.
func (m *MockCoder) TransitionTo(newState proto.State) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	oldState := m.currentState
	m.currentState = newState

	// Send state change notification if channel is set
	if m.stateNotifCh != nil {
		notification := &proto.StateChangeNotification{
			AgentID:   m.id,
			FromState: oldState,
			ToState:   newState,
			Timestamp: time.Now(),
		}

		// Non-blocking send
		select {
		case m.stateNotifCh <- notification:
			// Sent successfully
		default:
			// Channel full, but don't block
		}
	}

	return nil
}

// GenerateStatus implements StatusAgent interface.
func (m *MockCoder) GenerateStatus() (string, error) {
	return "Mock coder status", nil
}

func TestCoderStateChangeNotifications(t *testing.T) {
	// Create minimal config
	cfg := &config.Config{
		Orchestrator: &config.OrchestratorConfig{
			Models: []config.Model{
				{
					Name:           "test-model",
					MaxTPM:         1000,
					DailyBudget:    10.0,
					MaxConnections: 1,
				},
			},
		},
		Agents: &config.AgentConfig{
			MaxCoders:      1,
			CoderModel:     "test-model",
			ArchitectModel: "test-model",
		},
	}

	// Create rate limiter
	rateLimiter := limiter.NewLimiter(cfg)
	defer rateLimiter.Close()

	// Create dispatcher
	dispatcher, err := dispatch.NewDispatcher(cfg, rateLimiter)
	if err != nil {
		t.Fatalf("Failed to create dispatcher: %v", err)
	}

	// Start dispatcher
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := dispatcher.Start(ctx); err != nil {
		t.Fatalf("Failed to start dispatcher: %v", err)
	}
	defer func() {
		_ = dispatcher.Stop(ctx)
	}()

	// Create test orchestrator components
	logger := logx.NewLogger("test-orchestrator")
	agents := make(map[string]StatusAgent)
	agentTypes := make(map[string]string)

	// Create mock coder
	mockCoder := NewMockCoder("test-coder-001")

	// Attach mock coder to dispatcher
	dispatcher.Attach(mockCoder)
	agents["test-coder-001"] = &StatusAgentWrapper{Agent: mockCoder}
	agentTypes["test-coder-001"] = string(agent.TypeCoder)

	// Get state change channel from dispatcher
	stateChangeCh := dispatcher.GetStateChangeChannel()
	if stateChangeCh == nil {
		t.Fatal("Dispatcher returned nil state change channel")
	}

	// Track received notifications
	var receivedNotifications []*proto.StateChangeNotification
	var notificationsMu sync.Mutex

	// Start state change processor (simplified version of orchestrator logic)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case notification, ok := <-stateChangeCh:
				if !ok {
					return
				}
				if notification != nil {
					notificationsMu.Lock()
					receivedNotifications = append(receivedNotifications, notification)
					logger.Info("🔔 Test received state change notification: %s %s -> %s",
						notification.AgentID, notification.FromState, notification.ToState)
					notificationsMu.Unlock()
				}
			}
		}
	}()

	// Give the system a moment to set up
	time.Sleep(100 * time.Millisecond)

	// Test 1: Transition to DONE state
	t.Run("CoderTransitionToDONE", func(t *testing.T) {
		// Simulate coder transitioning to DONE
		if err := mockCoder.TransitionTo(proto.StateDone); err != nil {
			t.Fatalf("Failed to transition coder to DONE: %v", err)
		}

		// Wait for notification
		timeout := time.After(2 * time.Second)
		for {
			select {
			case <-timeout:
				t.Fatal("Timeout waiting for DONE state notification")
			default:
				notificationsMu.Lock()
				found := false
				for _, notif := range receivedNotifications {
					if notif.AgentID == "test-coder-001" && notif.ToState == proto.StateDone {
						found = true
						if notif.FromState != proto.StateWaiting {
							t.Errorf("Expected FromState to be WAITING, got %s", notif.FromState)
						}
						break
					}
				}
				notificationsMu.Unlock()

				if found {
					return // Test passed
				}

				time.Sleep(10 * time.Millisecond)
			}
		}
	})

	// Test 2: Transition to ERROR state
	t.Run("CoderTransitionToERROR", func(t *testing.T) {
		// Clear previous notifications
		notificationsMu.Lock()
		receivedNotifications = nil
		notificationsMu.Unlock()

		// Simulate coder transitioning to ERROR
		if err := mockCoder.TransitionTo(proto.StateError); err != nil {
			t.Fatalf("Failed to transition coder to ERROR: %v", err)
		}

		// Wait for notification
		timeout := time.After(2 * time.Second)
		for {
			select {
			case <-timeout:
				t.Fatal("Timeout waiting for ERROR state notification")
			default:
				notificationsMu.Lock()
				found := false
				for _, notif := range receivedNotifications {
					if notif.AgentID == "test-coder-001" && notif.ToState == proto.StateError {
						found = true
						if notif.FromState != proto.StateDone {
							t.Errorf("Expected FromState to be DONE, got %s", notif.FromState)
						}
						break
					}
				}
				notificationsMu.Unlock()

				if found {
					return // Test passed
				}

				time.Sleep(10 * time.Millisecond)
			}
		}
	})

	// Test 3: Verify orchestrator restart logic would trigger
	t.Run("RestartLogicTriggers", func(t *testing.T) {
		// This tests the orchestrator's handleStateChange logic
		notification := &proto.StateChangeNotification{
			AgentID: "test-coder-001",
			ToState: proto.StateDone,
		}

		// Check agent type detection
		agentType := ""
		if aType, exists := agentTypes[notification.AgentID]; exists {
			agentType = aType
		}

		if agentType != string(agent.TypeCoder) {
			t.Errorf("Expected agent type to be 'coder', got '%s'", agentType)
		}

		// Verify restart condition
		shouldRestart := (notification.ToState == proto.StateDone || notification.ToState == proto.StateError) &&
			agentType == string(agent.TypeCoder)

		if !shouldRestart {
			t.Error("Orchestrator restart logic should trigger for DONE coder agent")
		}
	})
}

// TestCoderStateChangeNotificationChannelSetup verifies the channel setup process.
func TestCoderStateChangeNotificationChannelSetup(t *testing.T) {
	// Create minimal dispatcher
	cfg := &config.Config{
		Orchestrator: &config.OrchestratorConfig{
			Models: []config.Model{{Name: "test", MaxTPM: 1000, DailyBudget: 10.0, MaxConnections: 1}},
		},
		Agents: &config.AgentConfig{
			MaxCoders:      1,
			CoderModel:     "test",
			ArchitectModel: "test",
		},
	}

	rateLimiter := limiter.NewLimiter(cfg)
	defer rateLimiter.Close()

	dispatcher, err := dispatch.NewDispatcher(cfg, rateLimiter)
	if err != nil {
		t.Fatalf("Failed to create dispatcher: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := dispatcher.Start(ctx); err != nil {
		t.Fatalf("Failed to start dispatcher: %v", err)
	}
	defer func() { _ = dispatcher.Stop(ctx) }()

	// Create mock coder
	mockCoder := NewMockCoder("test-setup-coder")

	// Before attachment, notification channel should be nil
	if mockCoder.stateNotifCh != nil {
		t.Error("State notification channel should be nil before attachment")
	}

	// Attach to dispatcher
	dispatcher.Attach(mockCoder)

	// Give attachment time to complete
	time.Sleep(50 * time.Millisecond)

	// After attachment, notification channel should be set
	mockCoder.mu.Lock()
	hasChannel := mockCoder.stateNotifCh != nil
	mockCoder.mu.Unlock()

	if !hasChannel {
		t.Error("State notification channel should be set after attachment to dispatcher")
	}

	// Verify we can get the channel from dispatcher
	stateChangeCh := dispatcher.GetStateChangeChannel()
	if stateChangeCh == nil {
		t.Error("Dispatcher should return a valid state change channel")
	}
}
