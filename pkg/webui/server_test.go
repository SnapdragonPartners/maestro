package webui

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/eventlog"
	"orchestrator/pkg/limiter"
	"orchestrator/pkg/state"
)

func TestHandleAgents(t *testing.T) {
	// Create temporary store
	tempDir := t.TempDir()
	store, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Create test agents
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

	// Create server
	server := NewServer(nil, store, tempDir)

	// Create test request
	req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	w := httptest.NewRecorder()

	// Call handler
	server.handleAgents(w, req)

	// Check response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	// Parse response
	var agents []AgentListItem
	if err := json.NewDecoder(w.Body).Decode(&agents); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Verify we got all agents
	if len(agents) != 3 {
		t.Errorf("Expected 3 agents, got %d", len(agents))
	}

	// Check that agents are sorted by ID
	expectedOrder := []string{"architect:001", "coder:001", "coder:002"}
	for i, agent := range agents {
		if agent.ID != expectedOrder[i] {
			t.Errorf("Expected agent %d to be %s, got %s", i, expectedOrder[i], agent.ID)
		}
	}

	// Check role extraction
	if agents[0].Role != "architect" {
		t.Errorf("Expected architect role, got %s", agents[0].Role)
	}
	if agents[1].Role != "coder" {
		t.Errorf("Expected coder role, got %s", agents[1].Role)
	}

	// Check states
	if agents[0].State != "WAITING" {
		t.Errorf("Expected WAITING state, got %s", agents[0].State)
	}
	if agents[1].State != "PLANNING" {
		t.Errorf("Expected PLANNING state, got %s", agents[1].State)
	}
	if agents[2].State != "CODING" {
		t.Errorf("Expected CODING state, got %s", agents[2].State)
	}

	// Check timestamps are recent
	for i, agent := range agents {
		if time.Since(agent.LastTS) > time.Minute {
			t.Errorf("Expected recent timestamp for agent %d", i)
		}
	}
}

func TestHandleAgent(t *testing.T) {
	// Create temporary store
	tempDir := t.TempDir()
	store, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Create test agent with full state
	agentID := "test-agent"
	if err := store.SaveState(agentID, "TESTING", map[string]any{"test": "data"}); err != nil {
		t.Fatalf("Failed to save agent: %v", err)
	}

	// Add some additional fields
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

	// Create server
	server := NewServer(nil, store, tempDir)

	// Test valid agent
	req := httptest.NewRequest(http.MethodGet, "/api/agent/test-agent", nil)
	w := httptest.NewRecorder()

	server.handleAgent(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	// Parse response
	var responseState state.AgentState
	if err := json.NewDecoder(w.Body).Decode(&responseState); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Verify fields
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

	// Test non-existent agent
	req = httptest.NewRequest(http.MethodGet, "/api/agent/nonexistent", nil)
	w = httptest.NewRecorder()

	server.handleAgent(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w.Code)
	}

	// Test empty agent ID
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

	// Parse response
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

	// Test POST to agents endpoint
	req := httptest.NewRequest(http.MethodPost, "/api/agents", nil)
	w := httptest.NewRecorder()

	server.handleAgents(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status 405, got %d", w.Code)
	}

	// Test POST to health endpoint
	req = httptest.NewRequest(http.MethodPost, "/api/healthz", nil)
	w = httptest.NewRecorder()

	server.handleHealth(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status 405, got %d", w.Code)
	}
}

func TestHandleQueues(t *testing.T) {
	// We'll test with nil dispatcher first
	server := NewServer(nil, nil, "")

	req := httptest.NewRequest(http.MethodGet, "/api/queues", nil)
	w := httptest.NewRecorder()

	server.handleQueues(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected status 503 with nil dispatcher, got %d", w.Code)
	}

	// Test method not allowed
	req = httptest.NewRequest(http.MethodPost, "/api/queues", nil)
	w = httptest.NewRecorder()

	server.handleQueues(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status 405 for POST, got %d", w.Code)
	}
}

func TestHandleUpload(t *testing.T) {
	// Create temporary store and work directory
	tempDir := t.TempDir()
	store, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	workDir := t.TempDir()

	// Create server with nil dispatcher first
	server := NewServer(nil, store, workDir)

	// Test with nil dispatcher
	req := createUploadRequest(t, "test.md", "# Test content")
	w := httptest.NewRecorder()

	server.handleUpload(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected status 503 with nil dispatcher, got %d", w.Code)
	}

	// Test method not allowed
	req = httptest.NewRequest(http.MethodGet, "/api/upload", nil)
	w = httptest.NewRecorder()

	server.handleUpload(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status 405 for GET, got %d", w.Code)
	}

	// Test file too large
	largeContent := strings.Repeat("x", 101*1024) // 101 KB
	req = createUploadRequest(t, "large.md", largeContent)
	w = httptest.NewRecorder()

	server.handleUpload(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 for large file, got %d", w.Code)
	}

	// Test invalid file extension
	req = createUploadRequest(t, "test.txt", "content")
	w = httptest.NewRecorder()

	server.handleUpload(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 for .txt file, got %d", w.Code)
	}

	// Now test with a real dispatcher for architect state checks
	dispatcher := createTestDispatcher(t)
	server = NewServer(dispatcher, store, workDir)

	// Test architect not in WAITING state (no architect)
	req = createUploadRequest(t, "test.md", "# Content")
	w = httptest.NewRecorder()

	server.handleUpload(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("Expected status 409 when no architect, got %d", w.Code)
	}

	// Create architect in non-WAITING state
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

	// Test with no agents
	state, err := server.findArchitectState()
	if err != nil {
		t.Errorf("Expected no error with empty store, got %v", err)
	}
	if state != nil {
		t.Error("Expected nil state with no agents")
	}

	// Create non-architect agents
	if err := store.SaveState("coder:001", "CODING", nil); err != nil {
		t.Fatalf("Failed to save coder state: %v", err)
	}

	state, err = server.findArchitectState()
	if err != nil {
		t.Errorf("Expected no error with no architect, got %v", err)
	}
	if state != nil {
		t.Error("Expected nil state with no architect")
	}

	// Create architect
	if err := store.SaveState("architect:001", "WAITING", nil); err != nil {
		t.Fatalf("Failed to save architect state: %v", err)
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

// Helper function to create multipart upload request
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

// Helper function to create test dispatcher
func createTestDispatcher(t *testing.T) *dispatch.Dispatcher {
	// Create minimal config
	cfg := &config.Config{
		MaxRetryAttempts:       3,
		RetryBackoffMultiplier: 2.0,
	}

	// Create rate limiter
	rateLimiter := limiter.NewLimiter(cfg)

	// Create event log
	tmpDir := t.TempDir()
	eventLog, err := eventlog.NewWriter(tmpDir, 24)
	if err != nil {
		t.Fatalf("Failed to create event log: %v", err)
	}

	// Create dispatcher
	dispatcher, err := dispatch.NewDispatcher(cfg, rateLimiter, eventLog)
	if err != nil {
		t.Fatalf("Failed to create dispatcher: %v", err)
	}

	return dispatcher
}

func TestHandleLogs(t *testing.T) {
	tempDir := t.TempDir()
	workDir := t.TempDir()

	// Create test log files
	logsDir := filepath.Join(workDir, "logs")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		t.Fatalf("Failed to create logs dir: %v", err)
	}

	// Create test log content
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

	// Test basic logs endpoint
	req := httptest.NewRequest(http.MethodGet, "/api/logs", nil)
	w := httptest.NewRecorder()

	server.handleLogs(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	// Parse response
	var logs []LogEntry
	if err := json.NewDecoder(w.Body).Decode(&logs); err != nil {
		t.Fatalf("Failed to decode logs response: %v", err)
	}

	if len(logs) != 4 {
		t.Errorf("Expected 4 log entries, got %d", len(logs))
	}

	// Test domain filtering
	req = httptest.NewRequest(http.MethodGet, "/api/logs?domain=coder", nil)
	w = httptest.NewRecorder()

	server.handleLogs(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 for domain filter, got %d", w.Code)
	}

	logs = []LogEntry{}
	if err := json.NewDecoder(w.Body).Decode(&logs); err != nil {
		t.Fatalf("Failed to decode filtered logs: %v", err)
	}

	// Should have 2 coder entries
	if len(logs) != 2 {
		t.Errorf("Expected 2 coder log entries, got %d", len(logs))
	}

	// Test since filtering
	since := "2025-01-01T10:00:01.500Z"
	req = httptest.NewRequest(http.MethodGet, "/api/logs?since="+since, nil)
	w = httptest.NewRecorder()

	server.handleLogs(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 for since filter, got %d", w.Code)
	}

	logs = []LogEntry{}
	if err := json.NewDecoder(w.Body).Decode(&logs); err != nil {
		t.Fatalf("Failed to decode since-filtered logs: %v", err)
	}

	// Should have entries after 10:00:01.500Z (so 10:00:02 and 10:00:03)
	if len(logs) != 2 {
		t.Errorf("Expected 2 log entries after since time, got %d", len(logs))
	}

	// Test invalid since parameter
	req = httptest.NewRequest(http.MethodGet, "/api/logs?since=invalid", nil)
	w = httptest.NewRecorder()

	server.handleLogs(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 for invalid since, got %d", w.Code)
	}

	// Test method not allowed
	req = httptest.NewRequest(http.MethodPost, "/api/logs", nil)
	w = httptest.NewRecorder()

	server.handleLogs(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status 405 for POST, got %d", w.Code)
	}
}

func TestParseLogLine(t *testing.T) {
	server := &Server{}

	// Test valid log line
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

	// Test log line with domain
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

	// Test invalid log line
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
