package dispatch

import (
	"context"
	"testing"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/eventlog"
	"orchestrator/pkg/limiter"
	"orchestrator/pkg/proto"
)

// MockArchitectAgent simulates an architect agent for testing
type MockArchitectAgent struct {
	id               string
	idleAgentCh      <-chan string
	receivedMessages []*proto.AgentMsg
	sentMessages     []*proto.AgentMsg
	dispatcher       *Dispatcher
}

// GetID returns the agent ID
func (a *MockArchitectAgent) GetID() string {
	return a.id
}

// ProcessMessage handles incoming messages
func (a *MockArchitectAgent) ProcessMessage(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	a.receivedMessages = append(a.receivedMessages, msg)

	// Simulate processing and respond
	response := proto.NewAgentMsg(proto.MsgTypeRESULT, a.id, msg.FromAgent)
	response.ParentMsgID = msg.ID
	response.SetPayload("status", "processed")
	response.SetPayload("message", "Mock architect processed the message")

	a.sentMessages = append(a.sentMessages, response)
	return response, nil
}

// Shutdown shuts down the agent
func (a *MockArchitectAgent) Shutdown(ctx context.Context) error {
	return nil
}

// MockCoderAgent simulates a coding agent for testing
type MockCoderAgent struct {
	id               string
	receivedMessages []*proto.AgentMsg
	sentMessages     []*proto.AgentMsg
	dispatcher       *Dispatcher
}

// GetID returns the agent ID
func (a *MockCoderAgent) GetID() string {
	return a.id
}

// ProcessMessage handles incoming messages
func (a *MockCoderAgent) ProcessMessage(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	a.receivedMessages = append(a.receivedMessages, msg)

	// Simulate work completion after a brief delay
	go func() {
		time.Sleep(100 * time.Millisecond)

		// Send a RESULT message indicating completion
		completionMsg := proto.NewAgentMsg(proto.MsgTypeRESULT, a.id, "architect")
		completionMsg.ParentMsgID = msg.ID
		completionMsg.SetPayload("status", "completed")
		storyIDValue, _ := msg.GetPayload("story_id")
		completionMsg.SetPayload("story_id", storyIDValue)
		completionMsg.SetPayload("message", "Story implementation completed")

		// Send through dispatcher
		a.dispatcher.DispatchMessage(completionMsg)
		a.sentMessages = append(a.sentMessages, completionMsg)
	}()

	// Immediate acknowledgment
	response := proto.NewAgentMsg(proto.MsgTypeRESULT, a.id, msg.FromAgent)
	response.ParentMsgID = msg.ID
	response.SetPayload("status", "acknowledged")
	response.SetPayload("message", "Task received and processing started")

	return response, nil
}

// Shutdown shuts down the agent
func (a *MockCoderAgent) Shutdown(ctx context.Context) error {
	return nil
}

func TestEndToEndChannelWiring(t *testing.T) {
	// Create test configuration
	cfg := &config.Config{
		MaxRetryAttempts:       3,
		RetryBackoffMultiplier: 2.0,
	}

	// Create rate limiter and event log
	rateLimiter := limiter.NewLimiter(cfg)
	eventLog, err := eventlog.NewWriter("test_logs", 24)
	if err != nil {
		t.Fatalf("Failed to create event log: %v", err)
	}
	defer eventLog.Close()

	// Create dispatcher
	dispatcher, err := NewDispatcher(cfg, rateLimiter, eventLog)
	if err != nil {
		t.Fatalf("Failed to create dispatcher: %v", err)
	}

	// Create mock agents
	architectAgent := &MockArchitectAgent{
		id:               "architect-001",
		receivedMessages: make([]*proto.AgentMsg, 0),
		sentMessages:     make([]*proto.AgentMsg, 0),
		dispatcher:       dispatcher,
	}

	coderAgent := &MockCoderAgent{
		id:               "coder-001",
		receivedMessages: make([]*proto.AgentMsg, 0),
		sentMessages:     make([]*proto.AgentMsg, 0),
		dispatcher:       dispatcher,
	}

	// Register agents
	if err := dispatcher.RegisterAgent(architectAgent); err != nil {
		t.Fatalf("Failed to register architect agent: %v", err)
	}

	if err := dispatcher.RegisterAgent(coderAgent); err != nil {
		t.Fatalf("Failed to register coder agent: %v", err)
	}

	// Start dispatcher
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := dispatcher.Start(ctx); err != nil {
		t.Fatalf("Failed to start dispatcher: %v", err)
	}
	defer dispatcher.Stop(ctx)

	t.Run("Architect subscribes to idle agent notifications", func(t *testing.T) {
		// Subscribe architect to idle agent notifications
		idleAgentCh := dispatcher.SubscribeIdleAgents(architectAgent.GetID())
		architectAgent.idleAgentCh = idleAgentCh

		if idleAgentCh == nil {
			t.Fatal("Expected non-nil idle agent channel")
		}

		t.Log("✅ Architect successfully subscribed to idle agent notifications")
	})

	t.Run("Dispatch task to coder", func(t *testing.T) {
		// Create a TASK message from architect to coder
		taskMsg := proto.NewAgentMsg(proto.MsgTypeTASK, architectAgent.GetID(), coderAgent.GetID())
		taskMsg.SetPayload("story_id", "test-story-001")
		taskMsg.SetPayload("task_type", "implement_story")
		taskMsg.SetPayload("title", "Test Story Implementation")

		// Dispatch the task
		if err := dispatcher.DispatchMessage(taskMsg); err != nil {
			t.Fatalf("Failed to dispatch task: %v", err)
		}

		// Wait for task to be queued
		time.Sleep(100 * time.Millisecond)

		// Simulate coder pulling work from shared queue
		pulledTask := dispatcher.PullSharedWork()
		if pulledTask == nil {
			t.Fatal("Expected to find task in shared work queue")
		}

		// Simulate agent processing the pulled task
		response, err := coderAgent.ProcessMessage(ctx, pulledTask)
		if err != nil {
			t.Fatalf("Failed to process task: %v", err)
		}
		if response == nil {
			t.Fatal("Expected response from task processing")
		}

		// Verify coder received the task
		if len(coderAgent.receivedMessages) == 0 {
			t.Fatal("Coder should have received the task message")
		}

		receivedTask := coderAgent.receivedMessages[0]
		if receivedTask.Type != proto.MsgTypeTASK {
			t.Errorf("Expected TASK message, got %s", receivedTask.Type)
		}

		storyIDValue, _ := receivedTask.GetPayload("story_id")
		storyID := storyIDValue.(string)
		if storyID != "test-story-001" {
			t.Errorf("Expected story_id 'test-story-001', got '%s'", storyID)
		}

		t.Log("✅ Task successfully dispatched to coder")
	})

	t.Run("Coder completion triggers idle notification", func(t *testing.T) {
		// Wait for coder to complete the work and send completion message
		time.Sleep(300 * time.Millisecond)

		// Check if architect received idle agent notification
		select {
		case idleAgentID := <-architectAgent.idleAgentCh:
			if idleAgentID != coderAgent.GetID() {
				t.Errorf("Expected idle agent ID '%s', got '%s'", coderAgent.GetID(), idleAgentID)
			}
			t.Logf("✅ Architect received idle notification for agent: %s", idleAgentID)
		case <-time.After(1 * time.Second):
			t.Error("Timeout waiting for idle agent notification")
		}
	})

	t.Run("Message routing through queues", func(t *testing.T) {
		// Test architect work queue
		architectWork := dispatcher.PullArchitectWork()
		if architectWork == nil {
			t.Log("No architect work in queue (expected for this test)")
		}

		// Test coder feedback queue
		coderFeedback := dispatcher.PullCoderFeedback(coderAgent.GetID())
		if coderFeedback != nil {
			t.Logf("✅ Coder received feedback message: %s", coderFeedback.Type)
		}

		// Test shared work queue
		sharedWork := dispatcher.PullSharedWork()
		if sharedWork == nil {
			t.Log("No shared work in queue (expected after processing)")
		}
	})

	t.Run("Stats and monitoring", func(t *testing.T) {
		stats := dispatcher.GetStats()

		// Verify stats structure
		if !stats["running"].(bool) {
			t.Error("Expected dispatcher to be running")
		}

		agents := stats["agents"].([]string)
		if len(agents) != 2 {
			t.Errorf("Expected 2 registered agents, got %d", len(agents))
		}

		t.Logf("✅ Dispatcher stats: %+v", stats)
	})
}

func TestIdleAgentChannelCleanup(t *testing.T) {
	// Create minimal test setup
	cfg := &config.Config{
		MaxRetryAttempts:       3,
		RetryBackoffMultiplier: 2.0,
	}

	rateLimiter := limiter.NewLimiter(cfg)
	eventLog, err := eventlog.NewWriter("test_logs", 24)
	if err != nil {
		t.Fatalf("Failed to create event log: %v", err)
	}
	defer eventLog.Close()

	dispatcher, err := NewDispatcher(cfg, rateLimiter, eventLog)
	if err != nil {
		t.Fatalf("Failed to create dispatcher: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := dispatcher.Start(ctx); err != nil {
		t.Fatalf("Failed to start dispatcher: %v", err)
	}

	// Subscribe to idle notifications
	idleAgentCh := dispatcher.SubscribeIdleAgents("test-architect")

	// Test graceful shutdown closes channel
	if err := dispatcher.Stop(ctx); err != nil {
		t.Fatalf("Failed to stop dispatcher: %v", err)
	}

	// Verify channel is closed
	select {
	case _, ok := <-idleAgentCh:
		if ok {
			t.Error("Expected channel to be closed")
		} else {
			t.Log("✅ Idle agent channel properly closed on shutdown")
		}
	case <-time.After(1 * time.Second):
		t.Error("Timeout waiting for channel close")
	}
}
