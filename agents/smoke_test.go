package agents

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/eventlog"
	"orchestrator/pkg/limiter"
	"orchestrator/pkg/proto"
)

// SmokeTest demonstrates architect agent workflow:
// 1. Architect reads story and creates TASK
// 2. Sends TASK to dispatcher
// 3. Receives RESULT from coding agent
// 4. Logs success
func TestArchitectSmokeTest(t *testing.T) {
	// Setup test environment
	tmpDir := t.TempDir()
	storiesDir := filepath.Join(tmpDir, "stories")
	logsDir := filepath.Join(tmpDir, "logs")

	err := os.MkdirAll(storiesDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create stories directory: %v", err)
	}

	err = os.MkdirAll(logsDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create logs directory: %v", err)
	}

	// Create test story
	storyContent := `# Health Endpoint
	
Implement a health check endpoint.

- GET /health endpoint
- Return JSON response
- Include status and timestamp
`
	storyFile := filepath.Join(storiesDir, "001.md")
	err = os.WriteFile(storyFile, []byte(storyContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test story: %v", err)
	}

	// Setup infrastructure
	cfg := createSmokeTestConfig()
	rateLimiter := limiter.NewLimiter(cfg)
	defer rateLimiter.Close()

	eventLog, err := eventlog.NewWriter(logsDir, 24)
	if err != nil {
		t.Fatalf("Failed to create event log: %v", err)
	}
	defer eventLog.Close()

	dispatcher, err := dispatch.NewDispatcher(cfg, rateLimiter, eventLog)
	if err != nil {
		t.Fatalf("Failed to create dispatcher: %v", err)
	}

	// Start dispatcher
	ctx := context.Background()
	err = dispatcher.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start dispatcher: %v", err)
	}
	defer dispatcher.Stop(ctx)

	// Create architect agent using proper model:id format
	workDir := filepath.Join(tmpDir, "work")
	architect := NewArchitectAgent("architect:001", "smoke-test", storiesDir, workDir, "claude:001")
	architect.SetDispatcher(dispatcher)

	// Register architect with dispatcher
	err = dispatcher.RegisterAgent(architect)
	if err != nil {
		t.Fatalf("Failed to register architect agent: %v", err)
	}

	// Create mock coding agent for testing
	mockClaude := NewMockCodingAgent("claude:001")
	err = dispatcher.RegisterAgent(mockClaude)
	if err != nil {
		t.Fatalf("Failed to register mock coding agent: %v", err)
	}

	// Step 1: Send story processing request to architect
	orchestratorMsg := proto.NewAgentMsg(proto.MsgTypeTASK, "orchestrator", "architect:001")
	orchestratorMsg.SetPayload("story_id", "001")
	orchestratorMsg.SetMetadata("test_case", "smoke_test")

	t.Logf("Step 1: Sending story processing request to architect")
	err = dispatcher.DispatchMessage(orchestratorMsg)
	if err != nil {
		t.Fatalf("Failed to dispatch message to architect: %v", err)
	}

	// Wait for processing to complete
	time.Sleep(200 * time.Millisecond)

	// Step 2: Verify architect processed story and sent task to claude
	if mockClaude.GetMessageCount() == 0 {
		t.Error("Expected architect to send task to claude")
	}

	receivedMessage := mockClaude.GetLastMessage()
	if receivedMessage == nil {
		t.Fatal("Expected claude to receive a message from architect")
	}

	if receivedMessage.Type != proto.MsgTypeTASK {
		t.Errorf("Expected TASK message, got %s", receivedMessage.Type)
	}

	if receivedMessage.FromAgent != "architect:001" {
		t.Errorf("Expected message from architect:001, got from %s", receivedMessage.FromAgent)
	}

	t.Logf("Step 2: ✓ Architect sent TASK to claude")

	// Step 3: Verify event log contains expected messages
	logFile := eventLog.GetCurrentLogFile()
	messages, err := eventlog.ReadMessages(logFile)
	if err != nil {
		t.Fatalf("Failed to read event log: %v", err)
	}

	if len(messages) < 2 {
		t.Errorf("Expected at least 2 messages in event log, got %d", len(messages))
	}

	// Look for task message in logs
	var foundTask bool
	for _, msg := range messages {
		if msg.Type == proto.MsgTypeTASK && msg.FromAgent == "architect:001" && msg.ToAgent == "claude:001" {
			foundTask = true
			break
		}
	}

	if !foundTask {
		t.Error("Expected to find TASK message from architect:001 to claude:001 in event log")
	}

	t.Logf("Step 3: ✓ Task message logged successfully")

	// Step 4: Verify dispatcher statistics
	stats := dispatcher.GetStats()
	agents := stats["agents"].([]string)

	expectedAgents := []string{"architect:001", "claude:001"}
	if len(agents) != len(expectedAgents) {
		t.Errorf("Expected %d agents, got %d", len(expectedAgents), len(agents))
	}

	for _, expectedAgent := range expectedAgents {
		found := false
		for _, agent := range agents {
			if agent == expectedAgent {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected agent %s to be registered", expectedAgent)
		}
	}

	t.Logf("Step 4: ✓ All agents registered and dispatcher operational")

	t.Log("🎉 Smoke test completed successfully!")
	t.Log("   - Architect agent reads story files")
	t.Log("   - Converts stories to tasks")
	t.Log("   - Routes tasks to coding agents via dispatcher")
	t.Log("   - All messages properly logged")
}

// MockCodingAgent simulates a coding agent for testing
type MockCodingAgent struct {
	id           string
	messages     []*proto.AgentMsg
	messageCount int
}

func NewMockCodingAgent(id string) *MockCodingAgent {
	return &MockCodingAgent{
		id:       id,
		messages: make([]*proto.AgentMsg, 0),
	}
}

func (m *MockCodingAgent) GetID() string {
	return m.id
}

func (m *MockCodingAgent) ProcessMessage(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	// Store received message
	m.messages = append(m.messages, msg.Clone())
	m.messageCount++

	// Return simple success response
	response := proto.NewAgentMsg(proto.MsgTypeRESULT, m.id, msg.FromAgent)
	response.ParentMsgID = msg.ID
	response.SetPayload("status", "completed")
	response.SetPayload("message", "Task processed successfully")

	return response, nil
}

func (m *MockCodingAgent) Shutdown(ctx context.Context) error {
	return nil
}

func (m *MockCodingAgent) GetMessageCount() int {
	return m.messageCount
}

func (m *MockCodingAgent) GetLastMessage() *proto.AgentMsg {
	if len(m.messages) == 0 {
		return nil
	}
	return m.messages[len(m.messages)-1]
}

func createSmokeTestConfig() *config.Config {
	return &config.Config{
		Models: map[string]config.ModelCfg{
			"architect": {
				MaxTokensPerMinute: 1000,
				MaxBudgetPerDayUSD: 10.0,
				APIKey:             "test-key",
				Agents: []config.Agent{
					{Name: "smoke-architect", ID: "001", Type: "architect", WorkDir: "./work/architect"},
				},
			},
			"claude": {
				MaxTokensPerMinute: 1000,
				MaxBudgetPerDayUSD: 25.0,
				APIKey:             "test-key",
				Agents: []config.Agent{
					{Name: "smoke-claude", ID: "001", Type: "coder", WorkDir: "./work/claude"},
				},
			},
		},
		MaxRetryAttempts:       3,
		RetryBackoffMultiplier: 2.0,
	}
}
