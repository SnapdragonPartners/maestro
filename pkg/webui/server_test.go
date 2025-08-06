package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/architect"
	"orchestrator/pkg/config"
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/eventlog"
	"orchestrator/pkg/limiter"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/state"
	"orchestrator/pkg/utils"
)

func TestHandleAgents(t *testing.T) {
	// Create temporary store.
	tempDir := t.TempDir()
	store, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Create test agents.
	testAgents := []struct {
		id    string
		state string
	}{
		{"architect:001", "WAITING"},
		{"coder:001", "PLANNING"},
		{"coder:002", "CODING"},
	}

	for _, agent := range testAgents {
		if err := store.SaveState(agent.id, agent.state, nil); err != nil {
			t.Fatalf("Failed to save agent %s: %v", agent.id, err)
		}
	}

	// Create server.
	server := NewServer(nil, store, tempDir)

	// Create test request.
	req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	w := httptest.NewRecorder()

	// Call handler.
	server.handleAgents(w, req)

	// Check response.
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	// Parse response.
	var agents []AgentListItem
	if err := json.NewDecoder(w.Body).Decode(&agents); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Verify we got all agents.
	if len(agents) != 3 {
		t.Errorf("Expected 3 agents, got %d", len(agents))
	}

	// Check that agents are sorted by ID.
	expectedOrder := []string{"architect:001", "coder:001", "coder:002"}
	for i, agent := range agents {
		if agent.ID != expectedOrder[i] {
			t.Errorf("Expected agent %d to be %s, got %s", i, expectedOrder[i], agent.ID)
		}
	}

	// Check role extraction.
	if agents[0].Role != "architect" {
		t.Errorf("Expected architect role, got %s", agents[0].Role)
	}
	if agents[1].Role != "coder" {
		t.Errorf("Expected coder role, got %s", agents[1].Role)
	}

	// Check states.
	if agents[0].State != "WAITING" {
		t.Errorf("Expected WAITING state, got %s", agents[0].State)
	}
	if agents[1].State != "PLANNING" {
		t.Errorf("Expected PLANNING state, got %s", agents[1].State)
	}
	if agents[2].State != "CODING" {
		t.Errorf("Expected CODING state, got %s", agents[2].State)
	}

	// Check timestamps are recent.
	for i, agent := range agents {
		if time.Since(agent.LastTS) > time.Minute {
			t.Errorf("Expected recent timestamp for agent %d", i)
		}
	}
}

func TestHandleAgent(t *testing.T) {
	// Create temporary store.
	tempDir := t.TempDir()
	store, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Create test agent with full state.
	agentID := "test-agent"
	if saveErr := store.SaveState(agentID, "TESTING", map[string]any{"test": "data"}); saveErr != nil {
		t.Fatalf("Failed to save agent: %v", saveErr)
	}

	// Add some additional fields.
	agentState, err := store.GetStateInfo(agentID)
	if err != nil {
		t.Fatalf("Failed to get agent state: %v", err)
	}

	plan := "Test plan"
	taskContent := "Test task"
	agentState.Plan = &plan
	agentState.TaskContent = &taskContent
	agentState.AppendTransition("IDLE", "TESTING")

	if err := store.Save(agentID, agentState); err != nil {
		t.Fatalf("Failed to save updated agent: %v", err)
	}

	// Create server.
	server := NewServer(nil, store, tempDir)

	// Test valid agent.
	req := httptest.NewRequest(http.MethodGet, "/api/agent/test-agent", nil)
	w := httptest.NewRecorder()

	server.handleAgent(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	// Parse response.
	var responseState state.AgentState
	if err := json.NewDecoder(w.Body).Decode(&responseState); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Verify fields.
	if responseState.State != "TESTING" {
		t.Errorf("Expected state TESTING, got %s", responseState.State)
	}

	if responseState.Plan == nil || *responseState.Plan != "Test plan" {
		t.Errorf("Expected plan 'Test plan', got %v", responseState.Plan)
	}

	if responseState.TaskContent == nil || *responseState.TaskContent != "Test task" {
		t.Errorf("Expected task content 'Test task', got %v", responseState.TaskContent)
	}

	if len(responseState.Transitions) != 1 {
		t.Errorf("Expected 1 transition, got %d", len(responseState.Transitions))
	}

	// Test non-existent agent.
	req = httptest.NewRequest(http.MethodGet, "/api/agent/nonexistent", nil)
	w = httptest.NewRecorder()

	server.handleAgent(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w.Code)
	}

	// Test empty agent ID.
	req = httptest.NewRequest(http.MethodGet, "/api/agent/", nil)
	w = httptest.NewRecorder()

	server.handleAgent(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}
}

func TestHandleHealth(t *testing.T) {
	server := NewServer(nil, nil, "")

	req := httptest.NewRequest(http.MethodGet, "/api/healthz", nil)
	w := httptest.NewRecorder()

	server.handleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	// Parse response.
	var response map[string]string
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response["status"] != "ok" {
		t.Errorf("Expected status 'ok', got %s", response["status"])
	}

	if response["version"] != "v1.0" {
		t.Errorf("Expected version 'v1.0', got %s", response["version"])
	}
}

func TestHandleMethodNotAllowed(t *testing.T) {
	server := NewServer(nil, nil, "")

	// Test POST to agents endpoint.
	req := httptest.NewRequest(http.MethodPost, "/api/agents", nil)
	w := httptest.NewRecorder()

	server.handleAgents(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status 405, got %d", w.Code)
	}

	// Test POST to health endpoint.
	req = httptest.NewRequest(http.MethodPost, "/api/healthz", nil)
	w = httptest.NewRecorder()

	server.handleHealth(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status 405, got %d", w.Code)
	}
}

func TestHandleQueues(t *testing.T) {
	// We'll test with nil dispatcher first.
	server := NewServer(nil, nil, "")

	req := httptest.NewRequest(http.MethodGet, "/api/queues", nil)
	w := httptest.NewRecorder()

	server.handleQueues(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected status 503 with nil dispatcher, got %d", w.Code)
	}

	// Test method not allowed.
	req = httptest.NewRequest(http.MethodPost, "/api/queues", nil)
	w = httptest.NewRecorder()

	server.handleQueues(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status 405 for POST, got %d", w.Code)
	}
}

func TestHandleUpload(t *testing.T) {
	// Create temporary store and work directory.
	tempDir := t.TempDir()
	store, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	workDir := t.TempDir()

	// Create server with nil dispatcher first.
	server := NewServer(nil, store, workDir)

	// Test with nil dispatcher.
	req := createUploadRequest(t, "test.md", "# Test content")
	w := httptest.NewRecorder()

	server.handleUpload(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected status 503 with nil dispatcher, got %d", w.Code)
	}

	// Test method not allowed.
	req = httptest.NewRequest(http.MethodGet, "/api/upload", nil)
	w = httptest.NewRecorder()

	server.handleUpload(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status 405 for GET, got %d", w.Code)
	}

	// Test file too large.
	largeContent := strings.Repeat("x", 101*1024) // 101 KB
	req = createUploadRequest(t, "large.md", largeContent)
	w = httptest.NewRecorder()

	server.handleUpload(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 for large file, got %d", w.Code)
	}

	// Test invalid file extension.
	req = createUploadRequest(t, "test.txt", "content")
	w = httptest.NewRecorder()

	server.handleUpload(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 for .txt file, got %d", w.Code)
	}

	// Now test with a real dispatcher for architect state checks.
	dispatcher := createTestDispatcher(t)
	server = NewServer(dispatcher, store, workDir)

	// Test architect not in WAITING state (no architect).
	req = createUploadRequest(t, "test.md", "# Content")
	w = httptest.NewRecorder()

	server.handleUpload(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("Expected status 409 when no architect, got %d", w.Code)
	}

	// Create architect in non-WAITING state.
	if err := store.SaveState("architect:001", "BUSY", nil); err != nil {
		t.Fatalf("Failed to save architect state: %v", err)
	}

	req = createUploadRequest(t, "test.md", "# Content")
	w = httptest.NewRecorder()

	server.handleUpload(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("Expected status 409 when architect busy, got %d", w.Code)
	}
}

func TestFindArchitectState(t *testing.T) {
	tempDir := t.TempDir()
	store, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	server := NewServer(nil, store, "")

	// Test with no agents.
	state, err := server.findArchitectState()
	if err == nil {
		t.Error("Expected error with empty store")
	}
	if state != nil {
		t.Error("Expected nil state with no agents")
	}

	// Create non-architect agents.
	if coderErr := store.SaveState("coder:001", "CODING", nil); coderErr != nil {
		t.Fatalf("Failed to save coder state: %v", coderErr)
	}

	state, err = server.findArchitectState()
	if err == nil {
		t.Error("Expected error with no architect")
	}
	if state != nil {
		t.Error("Expected nil state with no architect")
	}

	// Create architect.
	if archErr := store.SaveState("architect:001", "WAITING", nil); archErr != nil {
		t.Fatalf("Failed to save architect state: %v", archErr)
	}

	state, err = server.findArchitectState()
	if err != nil {
		t.Errorf("Expected no error with architect, got %v", err)
	}
	if state == nil {
		t.Error("Expected non-nil state with architect")
	} else if state.State != "WAITING" {
		t.Errorf("Expected WAITING state, got %s", state.State)
	}
}

// Helper function to create multipart upload request.
func createUploadRequest(t *testing.T, filename, content string) *http.Request {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("Failed to create form file: %v", err)
	}

	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatalf("Failed to write content: %v", err)
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("Failed to close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/upload", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	return req
}

// Helper function to create test dispatcher.
func createTestDispatcher(t *testing.T) *dispatch.Dispatcher {
	// Create minimal config.
	cfg := &config.Config{}

	// Create rate limiter.
	rateLimiter := limiter.NewLimiter(cfg)

	// Create event log.
	tmpDir := t.TempDir()
	eventLog, err := eventlog.NewWriter(tmpDir, 24)
	if err != nil {
		t.Fatalf("Failed to create event log: %v", err)
	}

	// Create dispatcher.
	dispatcher, err := dispatch.NewDispatcher(cfg, rateLimiter, eventLog)
	if err != nil {
		t.Fatalf("Failed to create dispatcher: %v", err)
	}

	return dispatcher
}

func TestHandleLogs(t *testing.T) {
	tempDir := t.TempDir()
	workDir := t.TempDir()

	// Create test log files.
	logsDir := filepath.Join(workDir, "logs")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		t.Fatalf("Failed to create logs dir: %v", err)
	}

	// Create test log content.
	logContent := `[2025-01-01T10:00:00.000Z] [architect:001] INFO: Starting architect
[2025-01-01T10:00:01.000Z] [coder:001] DEBUG: [coder] Starting coding task
[2025-01-01T10:00:02.000Z] [coder:002] WARN: Task failed
[2025-01-01T10:00:03.000Z] [architect:001] ERROR: System error
`

	logFile := filepath.Join(logsDir, "run.log")
	if err := os.WriteFile(logFile, []byte(logContent), 0644); err != nil {
		t.Fatalf("Failed to write log file: %v", err)
	}

	store, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	server := NewServer(nil, store, workDir)

	// Test basic logs endpoint.
	req := httptest.NewRequest(http.MethodGet, "/api/logs", nil)
	w := httptest.NewRecorder()

	server.handleLogs(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	// Parse response.
	var logs []logx.LogEntry
	if err := json.NewDecoder(w.Body).Decode(&logs); err != nil {
		t.Fatalf("Failed to decode logs response: %v", err)
	}

	// Check that we have at least the 4 test log entries we created
	// (there might be additional entries from the in-memory buffer from other tests)
	if len(logs) < 4 {
		t.Errorf("Expected at least 4 log entries, got %d", len(logs))
	}

	// Verify that our test entries are present by checking for specific messages
	foundTestEntries := 0
	testMessages := []string{"Starting architect", "Starting coding task", "Task failed", "System error"}
	for _, log := range logs {
		for _, testMsg := range testMessages {
			if strings.Contains(log.Message, testMsg) {
				foundTestEntries++
				break
			}
		}
	}
	if foundTestEntries != 4 {
		t.Errorf("Expected to find 4 test log entries, found %d", foundTestEntries)
	}

	// Test domain filtering.
	req = httptest.NewRequest(http.MethodGet, "/api/logs?domain=coder", nil)
	w = httptest.NewRecorder()

	server.handleLogs(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 for domain filter, got %d", w.Code)
	}

	logs = []logx.LogEntry{}
	if err := json.NewDecoder(w.Body).Decode(&logs); err != nil {
		t.Fatalf("Failed to decode filtered logs: %v", err)
	}

	// Should have at least 2 coder entries from our test data
	if len(logs) < 2 {
		t.Errorf("Expected at least 2 coder log entries, got %d", len(logs))
	}

	// Verify coder entries are present
	foundCoderEntries := 0
	coderMessages := []string{"Starting coding task", "Task failed"}
	for _, log := range logs {
		for _, coderMsg := range coderMessages {
			if strings.Contains(log.Message, coderMsg) {
				foundCoderEntries++
				break
			}
		}
	}
	if foundCoderEntries != 2 {
		t.Errorf("Expected to find 2 coder log entries, found %d", foundCoderEntries)
	}

	// Test since filtering.
	since := "2025-01-01T10:00:01.500Z"
	req = httptest.NewRequest(http.MethodGet, "/api/logs?since="+since, nil)
	w = httptest.NewRecorder()

	server.handleLogs(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 for since filter, got %d", w.Code)
	}

	logs = []logx.LogEntry{}
	if err := json.NewDecoder(w.Body).Decode(&logs); err != nil {
		t.Fatalf("Failed to decode since-filtered logs: %v", err)
	}

	// Should have at least 2 entries after 10:00:01.500Z (so 10:00:02 and 10:00:03)
	if len(logs) < 2 {
		t.Errorf("Expected at least 2 log entries after since time, got %d", len(logs))
	}

	// Verify that the specific entries after the since time are present
	foundSinceEntries := 0
	sinceMessages := []string{"Task failed", "System error"}
	for _, log := range logs {
		for _, sinceMsg := range sinceMessages {
			if strings.Contains(log.Message, sinceMsg) {
				foundSinceEntries++
				break
			}
		}
	}
	if foundSinceEntries != 2 {
		t.Errorf("Expected to find 2 log entries after since time, found %d", foundSinceEntries)
	}

	// Test invalid since parameter.
	req = httptest.NewRequest(http.MethodGet, "/api/logs?since=invalid", nil)
	w = httptest.NewRecorder()

	server.handleLogs(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 for invalid since, got %d", w.Code)
	}

	// Test method not allowed.
	req = httptest.NewRequest(http.MethodPost, "/api/logs", nil)
	w = httptest.NewRecorder()

	server.handleLogs(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status 405 for POST, got %d", w.Code)
	}
}

func TestParseLogLine(t *testing.T) {
	server := &Server{}

	// Test valid log line.
	line := "[2025-01-01T10:00:00.000Z] [architect:001] INFO: Starting system"
	entry := server.parseLogLine(line)

	if entry == nil {
		t.Fatal("Expected non-nil log entry")
	}

	if entry.Timestamp != "2025-01-01T10:00:00.000Z" {
		t.Errorf("Expected timestamp '2025-01-01T10:00:00.000Z', got '%s'", entry.Timestamp)
	}

	if entry.AgentID != "architect:001" {
		t.Errorf("Expected agentID 'architect:001', got '%s'", entry.AgentID)
	}

	if entry.Level != "INFO" {
		t.Errorf("Expected level 'INFO', got '%s'", entry.Level)
	}

	if entry.Message != "Starting system" {
		t.Errorf("Expected message 'Starting system', got '%s'", entry.Message)
	}

	// Test log line with domain.
	line = "[2025-01-01T10:00:00.000Z] [coder:001] DEBUG: [coder] Task started"
	entry = server.parseLogLine(line)

	if entry == nil {
		t.Fatal("Expected non-nil log entry with domain")
	}

	if entry.Domain != "coder" {
		t.Errorf("Expected domain 'coder', got '%s'", entry.Domain)
	}

	if entry.Message != "Task started" {
		t.Errorf("Expected message 'Task started', got '%s'", entry.Message)
	}

	// Test invalid log line.
	invalidLines := []string{
		"invalid line",
		"[timestamp] missing agentID",
		"[timestamp] [agentID] missing colon",
	}

	for _, line := range invalidLines {
		entry := server.parseLogLine(line)
		if entry != nil {
			t.Errorf("Expected nil for invalid line '%s', got %+v", line, entry)
		}
	}
}

// MockDriver implements the agent.Driver interface for testing.
type MockDriver struct {
	id        string
	agentType agent.Type
	state     proto.State
}

func NewMockDriver(id string, agentType agent.Type, state proto.State) *MockDriver {
	return &MockDriver{
		id:        id,
		agentType: agentType,
		state:     state,
	}
}

func (m *MockDriver) GetID() string                { return m.id }
func (m *MockDriver) GetAgentType() agent.Type     { return m.agentType }
func (m *MockDriver) GetCurrentState() proto.State { return m.state }
func (m *MockDriver) SetState(state proto.State)   { m.state = state }

// Minimal implementations for interface compliance (using context.Context).
func (m *MockDriver) Initialize(_ context.Context) error            { return nil }
func (m *MockDriver) Run(_ context.Context) error                   { return nil }
func (m *MockDriver) Step(_ context.Context) (bool, error)          { return false, nil }
func (m *MockDriver) GetStateData() map[string]any                  { return make(map[string]any) }
func (m *MockDriver) ValidateState(_ /* state */ proto.State) error { return nil }
func (m *MockDriver) GetValidStates() []proto.State                 { return []proto.State{} }
func (m *MockDriver) Shutdown(_ context.Context) error              { return nil }
func (m *MockDriver) ProcessMessage(_ context.Context, _ /* msg */ *proto.AgentMsg) (*proto.AgentMsg, error) {
	return nil, fmt.Errorf("mock driver - no message processing implemented")
}

// TestAgentRestartMonitoring tests that the web UI properly handles agent restart scenarios.
func TestAgentRestartMonitoring(t *testing.T) {
	// Create temporary directory and stores.
	tempDir := t.TempDir()

	// Create config for dispatcher.
	cfg := &config.Config{
		Orchestrator: &config.OrchestratorConfig{
			Models: []config.Model{
				{
					Name:           "test_model",
					MaxTPM:         1000,
					DailyBudget:    10.0,
					MaxConnections: 2,
					CPM:            3.0,
				},
			},
		},
	}

	// Create rate limiter and event log.
	rateLimiter := limiter.NewLimiter(cfg)
	eventLog, err := eventlog.NewWriter(filepath.Join(tempDir, "logs"), 24)
	if err != nil {
		t.Fatalf("Failed to create event log: %v", err)
	}
	defer eventLog.Close()

	// Create dispatcher.
	dispatcher, err := dispatch.NewDispatcher(cfg, rateLimiter, eventLog)
	if err != nil {
		t.Fatalf("Failed to create dispatcher: %v", err)
	}

	// Create state store for web UI.
	store, err := state.NewStore(filepath.Join(tempDir, ".maestro", "states"))
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	// Create web UI server.
	server := NewServer(dispatcher, store, tempDir)

	// Test 1: Initial agent registration.
	t.Run("InitialAgentRegistration", func(t *testing.T) {
		// Create and register a mock coder agent.
		mockCoder := NewMockDriver("claude_sonnet4:001", agent.TypeCoder, proto.StateWaiting)
		dispatcher.Attach(mockCoder)

		// Get agents from web UI.
		req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
		w := httptest.NewRecorder()
		server.handleAgents(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Expected status 200, got %d", w.Code)
		}

		var agents []AgentListItem
		if err := json.NewDecoder(w.Body).Decode(&agents); err != nil {
			t.Fatalf("Failed to decode agents response: %v", err)
		}

		// Verify agent is present.
		if len(agents) != 1 {
			t.Fatalf("Expected 1 agent, got %d", len(agents))
		}

		if agents[0].ID != "claude_sonnet4:001" {
			t.Errorf("Expected agent ID 'claude_sonnet4:001', got '%s'", agents[0].ID)
		}

		if agents[0].State != "WAITING" {
			t.Errorf("Expected agent state 'WAITING', got '%s'", agents[0].State)
		}

		t.Logf("✅ Initial agent registration verified: %s in state %s", agents[0].ID, agents[0].State)
	})

	// Test 2: Agent state progression.
	t.Run("AgentStateProgression", func(t *testing.T) {
		// Get the mock agent and change its state to DONE (triggering restart).
		registeredAgents := dispatcher.GetRegisteredAgents()
		if len(registeredAgents) != 1 {
			t.Fatalf("Expected 1 registered agent, got %d", len(registeredAgents))
		}

		mockDriver := utils.MustAssert[*MockDriver](registeredAgents[0].Driver, "mock driver")
		mockDriver.SetState(proto.StateDone)

		// Get agents from web UI to verify state change.
		req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
		w := httptest.NewRecorder()
		server.handleAgents(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Expected status 200, got %d", w.Code)
		}

		var agents []AgentListItem
		if err := json.NewDecoder(w.Body).Decode(&agents); err != nil {
			t.Fatalf("Failed to decode agents response: %v", err)
		}

		// Verify agent state changed to DONE.
		if len(agents) != 1 {
			t.Fatalf("Expected 1 agent, got %d", len(agents))
		}

		if agents[0].State != "DONE" {
			t.Errorf("Expected agent state 'DONE', got '%s'", agents[0].State)
		}

		t.Logf("✅ Agent state progression verified: %s now in state %s", agents[0].ID, agents[0].State)
	})

	// Test 3: Agent restart simulation (detach and reattach).
	t.Run("AgentRestartSimulation", func(t *testing.T) {
		agentID := "claude_sonnet4:001"

		// Step 1: Detach the agent (simulating shutdown).
		dispatcher.Detach(agentID)

		// Verify agent is no longer listed.
		req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
		w := httptest.NewRecorder()
		server.handleAgents(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Expected status 200, got %d", w.Code)
		}

		var agents []AgentListItem
		if err := json.NewDecoder(w.Body).Decode(&agents); err != nil {
			t.Fatalf("Failed to decode agents response: %v", err)
		}

		// Agent should be gone during restart.
		if len(agents) != 0 {
			t.Errorf("Expected 0 agents during restart, got %d", len(agents))
		}

		t.Logf("✅ Agent detachment verified: agent list is empty during restart")

		// Step 2: Reattach a new agent instance (simulating restart completion).
		newMockCoder := NewMockDriver(agentID, agent.TypeCoder, proto.StateWaiting)
		dispatcher.Attach(newMockCoder)

		// Verify agent reappears.
		req = httptest.NewRequest(http.MethodGet, "/api/agents", nil)
		w = httptest.NewRecorder()
		server.handleAgents(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Expected status 200, got %d", w.Code)
		}

		if err := json.NewDecoder(w.Body).Decode(&agents); err != nil {
			t.Fatalf("Failed to decode agents response: %v", err)
		}

		// Agent should be back with fresh state.
		if len(agents) != 1 {
			t.Fatalf("Expected 1 agent after restart, got %d", len(agents))
		}

		if agents[0].ID != agentID {
			t.Errorf("Expected agent ID '%s', got '%s'", agentID, agents[0].ID)
		}

		if agents[0].State != "WAITING" {
			t.Errorf("Expected restarted agent state 'WAITING', got '%s'", agents[0].State)
		}

		t.Logf("✅ Agent restart verified: %s reappeared in state %s", agents[0].ID, agents[0].State)
	})

	// Test 4: Multiple agent restart scenario.
	t.Run("MultipleAgentHandling", func(t *testing.T) {
		// Add a second agent (architect).
		architect := NewMockDriver("openai_o3:001", agent.TypeArchitect, proto.StateWaiting)
		dispatcher.Attach(architect)

		// Verify both agents are present.
		req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
		w := httptest.NewRecorder()
		server.handleAgents(w, req)

		var agents []AgentListItem
		json.NewDecoder(w.Body).Decode(&agents)

		if len(agents) != 2 {
			t.Fatalf("Expected 2 agents, got %d", len(agents))
		}

		// Restart just the coder agent.
		dispatcher.Detach("claude_sonnet4:001")

		req = httptest.NewRequest(http.MethodGet, "/api/agents", nil)
		w = httptest.NewRecorder()
		server.handleAgents(w, req)
		json.NewDecoder(w.Body).Decode(&agents)

		// Should have only architect now.
		if len(agents) != 1 {
			t.Fatalf("Expected 1 agent during coder restart, got %d", len(agents))
		}

		if agents[0].ID != "openai_o3:001" {
			t.Errorf("Expected architect to remain, got agent %s", agents[0].ID)
		}

		// Reattach coder.
		newCoder := NewMockDriver("claude_sonnet4:001", agent.TypeCoder, proto.StateWaiting)
		dispatcher.Attach(newCoder)

		req = httptest.NewRequest(http.MethodGet, "/api/agents", nil)
		w = httptest.NewRecorder()
		server.handleAgents(w, req)
		json.NewDecoder(w.Body).Decode(&agents)

		// Both should be present again.
		if len(agents) != 2 {
			t.Fatalf("Expected 2 agents after coder restart, got %d", len(agents))
		}

		t.Logf("✅ Multiple agent restart verified: architect unaffected, coder restarted")
	})

	t.Log("🎉 Agent restart monitoring continuity tests passed")
}

// TestArchitectMonitoringDuringRestart tests that architect monitoring remains stable during coder restarts.
func TestArchitectMonitoringDuringRestart(t *testing.T) {
	// Create temporary directory and stores.
	tempDir := t.TempDir()

	// Create config for dispatcher.
	cfg := &config.Config{
		Orchestrator: &config.OrchestratorConfig{
			Models: []config.Model{
				{
					Name:           "test_model",
					MaxTPM:         1000,
					DailyBudget:    10.0,
					MaxConnections: 2,
					CPM:            3.0,
				},
			},
		},
	}

	// Create rate limiter and event log.
	rateLimiter := limiter.NewLimiter(cfg)
	eventLog, err := eventlog.NewWriter(filepath.Join(tempDir, "logs"), 24)
	if err != nil {
		t.Fatalf("Failed to create event log: %v", err)
	}
	defer eventLog.Close()

	// Create dispatcher.
	dispatcher, err := dispatch.NewDispatcher(cfg, rateLimiter, eventLog)
	if err != nil {
		t.Fatalf("Failed to create dispatcher: %v", err)
	}

	// Create state store for web UI.
	store, err := state.NewStore(filepath.Join(tempDir, ".maestro", "states"))
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	// Create web UI server.
	server := NewServer(dispatcher, store, tempDir)

	// Test scenario: Architect monitoring stability during coder restart cycles.
	t.Run("ArchitectStabilityDuringCoderRestart", func(t *testing.T) {
		// Step 1: Register architect and coder agents.
		architect := NewMockDriver("openai_o3:001", agent.TypeArchitect, proto.StateWaiting)
		coder := NewMockDriver("claude_sonnet4:001", agent.TypeCoder, proto.StateWaiting)

		dispatcher.Attach(architect)
		dispatcher.Attach(coder)

		// Verify both agents are registered.
		req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
		w := httptest.NewRecorder()
		server.handleAgents(w, req)

		var agents []AgentListItem
		json.NewDecoder(w.Body).Decode(&agents)

		if len(agents) != 2 {
			t.Fatalf("Expected 2 agents initially, got %d", len(agents))
		}

		// Find architect and coder in response.
		var architectAgent, coderAgent *AgentListItem
		for i := range agents {
			if agents[i].ID == "openai_o3:001" {
				architectAgent = &agents[i]
			} else if agents[i].ID == "claude_sonnet4:001" {
				coderAgent = &agents[i]
			}
		}

		if architectAgent == nil || coderAgent == nil {
			t.Fatal("Both architect and coder should be present")
		}

		t.Logf("✅ Initial setup: architect=%s (%s), coder=%s (%s)",
			architectAgent.ID, architectAgent.State, coderAgent.ID, coderAgent.State)

		// Step 2: Change architect to PROCESSING state (simulating work).
		registeredAgents := dispatcher.GetRegisteredAgents()
		var mockArchitect *MockDriver
		for _, regAgent := range registeredAgents {
			if regAgent.ID == "openai_o3:001" {
				mockArchitect = utils.MustAssert[*MockDriver](regAgent.Driver, "architect mock driver")
				break
			}
		}
		mockArchitect.SetState(proto.State("MONITORING"))

		// Step 3: Simulate coder restart cycle while architect is working.
		originalCoderID := "claude_sonnet4:001"

		// Detach coder (simulating restart).
		dispatcher.Detach(originalCoderID)

		// Check architect is still visible and state unchanged.
		req = httptest.NewRequest(http.MethodGet, "/api/agents", nil)
		w = httptest.NewRecorder()
		server.handleAgents(w, req)
		json.NewDecoder(w.Body).Decode(&agents)

		// Should have only architect now.
		if len(agents) != 1 {
			t.Fatalf("Expected 1 agent during coder restart, got %d", len(agents))
		}

		if agents[0].ID != "openai_o3:001" {
			t.Errorf("Expected architect to remain, got %s", agents[0].ID)
		}

		if agents[0].State != "MONITORING" {
			t.Errorf("Expected architect state to remain MONITORING, got %s", agents[0].State)
		}

		t.Logf("✅ Coder restart: architect remains stable in %s state", agents[0].State)

		// Step 4: Reattach new coder instance.
		newCoder := NewMockDriver(originalCoderID, agent.TypeCoder, proto.StateWaiting)
		dispatcher.Attach(newCoder)

		// Check both agents are visible again.
		req = httptest.NewRequest(http.MethodGet, "/api/agents", nil)
		w = httptest.NewRecorder()
		server.handleAgents(w, req)
		json.NewDecoder(w.Body).Decode(&agents)

		if len(agents) != 2 {
			t.Fatalf("Expected 2 agents after coder restart, got %d", len(agents))
		}

		// Verify architect state persisted and coder was recreated.
		var newArchitectAgent, newCoderAgent *AgentListItem
		for i := range agents {
			if agents[i].ID == "openai_o3:001" {
				newArchitectAgent = &agents[i]
			} else if agents[i].ID == "claude_sonnet4:001" {
				newCoderAgent = &agents[i]
			}
		}

		if newArchitectAgent.State != "MONITORING" {
			t.Errorf("Expected architect to maintain MONITORING state, got %s", newArchitectAgent.State)
		}

		if newCoderAgent.State != "WAITING" {
			t.Errorf("Expected new coder to start in WAITING state, got %s", newCoderAgent.State)
		}

		t.Logf("✅ Post-restart verification: architect=%s (%s), coder=%s (%s)",
			newArchitectAgent.ID, newArchitectAgent.State, newCoderAgent.ID, newCoderAgent.State)

		// Step 5: Test multiple restart cycles.
		for cycle := 1; cycle <= 3; cycle++ {
			t.Logf("Testing restart cycle %d", cycle)

			// Change architect state to show it's actively working.
			mockArchitect.SetState(proto.State("DISPATCHING"))

			// Restart coder again.
			dispatcher.Detach(originalCoderID)

			// Quick check that architect is unaffected.
			req = httptest.NewRequest(http.MethodGet, "/api/agents", nil)
			w = httptest.NewRecorder()
			server.handleAgents(w, req)
			json.NewDecoder(w.Body).Decode(&agents)

			if len(agents) != 1 || agents[0].ID != "openai_o3:001" {
				t.Errorf("Cycle %d: architect should remain stable during coder restart", cycle)
			}

			// Reattach coder.
			anotherCoder := NewMockDriver(originalCoderID, agent.TypeCoder, proto.StateWaiting)
			dispatcher.Attach(anotherCoder)

			// Verify both agents present.
			req = httptest.NewRequest(http.MethodGet, "/api/agents", nil)
			w = httptest.NewRecorder()
			server.handleAgents(w, req)
			json.NewDecoder(w.Body).Decode(&agents)

			if len(agents) != 2 {
				t.Errorf("Cycle %d: expected 2 agents after restart, got %d", cycle, len(agents))
			}
		}

		t.Log("✅ Multiple restart cycles completed successfully")
	})

	t.Log("🎉 Architect monitoring stability tests passed")
}

// MockArchitectDriver extends MockDriver to implement architect-specific methods.
type MockArchitectDriver struct {
	MockDriver
	stories []*architect.QueuedStory
}

// NewMockArchitectDriver creates a new mock architect driver with test stories.
func NewMockArchitectDriver(id string, state proto.State, stories []*architect.QueuedStory) *MockArchitectDriver {
	return &MockArchitectDriver{
		MockDriver: MockDriver{
			id:        id,
			agentType: agent.TypeArchitect,
			state:     state,
		},
		stories: stories,
	}
}

// GetStoryList implements the architect-specific method for returning stories.
func (m *MockArchitectDriver) GetStoryList() []*architect.QueuedStory {
	return m.stories
}

func TestHandleStories(t *testing.T) {
	// Create temporary directory and stores.
	tempDir := t.TempDir()

	// Create config for dispatcher.
	cfg := &config.Config{
		Orchestrator: &config.OrchestratorConfig{
			Models: []config.Model{
				{
					Name:           "test_model",
					MaxTPM:         1000,
					DailyBudget:    10.0,
					MaxConnections: 2,
					CPM:            3.0,
				},
			},
		},
	}

	// Create rate limiter and event log.
	rateLimiter := limiter.NewLimiter(cfg)
	eventLog, err := eventlog.NewWriter(filepath.Join(tempDir, "logs"), 24)
	if err != nil {
		t.Fatalf("Failed to create event log: %v", err)
	}
	defer eventLog.Close()

	// Create dispatcher.
	dispatcher, err := dispatch.NewDispatcher(cfg, rateLimiter, eventLog)
	if err != nil {
		t.Fatalf("Failed to create dispatcher: %v", err)
	}

	// Create state store for web UI.
	store, err := state.NewStore(filepath.Join(tempDir, ".maestro", "states"))
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	// Create test stories.
	testStories := []*architect.QueuedStory{
		{
			ID:              "story-001",
			Title:           "Implement user authentication",
			Status:          architect.StatusPending,
			EstimatedPoints: 5,
			DependsOn:       []string{},
			LastUpdated:     time.Now(),
			StoryType:       "app",
		},
		{
			ID:              "story-002",
			Title:           "Add database layer",
			Status:          architect.StatusInProgress,
			EstimatedPoints: 8,
			DependsOn:       []string{"story-001"},
			AssignedAgent:   "coder-001",
			StartedAt:       &[]time.Time{time.Now().Add(-1 * time.Hour)}[0],
			LastUpdated:     time.Now(),
			StoryType:       "app",
		},
		{
			ID:              "story-003",
			Title:           "Create REST API endpoints",
			Status:          architect.StatusCompleted,
			EstimatedPoints: 3,
			DependsOn:       []string{"story-002"},
			AssignedAgent:   "coder-002",
			StartedAt:       &[]time.Time{time.Now().Add(-2 * time.Hour)}[0],
			CompletedAt:     &[]time.Time{time.Now().Add(-30 * time.Minute)}[0],
			LastUpdated:     time.Now(),
			StoryType:       "app",
		},
	}

	// Create web UI server.
	server := NewServer(dispatcher, store, tempDir)

	t.Run("NoArchitectAgent", func(t *testing.T) {
		// Test when no architect agent is registered.
		req := httptest.NewRequest(http.MethodGet, "/api/stories", nil)
		w := httptest.NewRecorder()

		server.handleStories(w, req)

		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("Expected status 503, got %d", w.Code)
		}
	})

	t.Run("ArchitectWithStories", func(t *testing.T) {
		// Create and register mock architect with stories.
		mockArchitect := NewMockArchitectDriver("architect-001", proto.StateWaiting, testStories)
		dispatcher.Attach(mockArchitect)

		// Save architect state so findArchitectState can find it.
		if err := store.SaveState("architect-001", "WAITING", nil); err != nil {
			t.Fatalf("Failed to save architect state: %v", err)
		}

		// Test getting stories.
		req := httptest.NewRequest(http.MethodGet, "/api/stories", nil)
		w := httptest.NewRecorder()

		server.handleStories(w, req)

		// Check response.
		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}

		// Parse response.
		var stories []*architect.QueuedStory
		if err := json.NewDecoder(w.Body).Decode(&stories); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		// Verify we got all stories.
		if len(stories) != 3 {
			t.Errorf("Expected 3 stories, got %d", len(stories))
		}

		// Check story details.
		if stories[0].ID != "story-001" {
			t.Errorf("Expected first story ID to be story-001, got %s", stories[0].ID)
		}
		if stories[0].Status != architect.StatusPending {
			t.Errorf("Expected first story status to be pending, got %s", stories[0].Status)
		}
		if stories[1].Status != architect.StatusInProgress {
			t.Errorf("Expected second story status to be in_progress, got %s", stories[1].Status)
		}
		if stories[2].Status != architect.StatusCompleted {
			t.Errorf("Expected third story status to be completed, got %s", stories[2].Status)
		}
	})

	t.Run("ArchitectWithNoStories", func(t *testing.T) {
		// Unregister the previous architect first.
		dispatcher.UnregisterAgent("architect-001")

		// Create and register mock architect with no stories.
		mockArchitect := NewMockArchitectDriver("architect-002", proto.StateWaiting, []*architect.QueuedStory{})
		dispatcher.Attach(mockArchitect)

		// Test getting empty stories.
		req := httptest.NewRequest(http.MethodGet, "/api/stories", nil)
		w := httptest.NewRecorder()

		server.handleStories(w, req)

		// Check response.
		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}

		// Parse response.
		var stories []*architect.QueuedStory
		if err := json.NewDecoder(w.Body).Decode(&stories); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		// Verify empty response.
		if len(stories) != 0 {
			t.Errorf("Expected 0 stories, got %d", len(stories))
		}
	})

	t.Run("MethodNotAllowed", func(t *testing.T) {
		// Test POST method (should be method not allowed).
		req := httptest.NewRequest(http.MethodPost, "/api/stories", nil)
		w := httptest.NewRecorder()

		server.handleStories(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("Expected status 405, got %d", w.Code)
		}
	})
}
