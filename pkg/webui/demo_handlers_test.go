package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"orchestrator/pkg/demo"
)

// mockDemoService implements DemoService for testing.
type mockDemoService struct {
	running       bool
	startErr      error
	stopErr       error
	restartErr    error
	rebuildErr    error
	logsErr       error
	logs          string
	status        *demo.Status
	workspacePath string
}

func (m *mockDemoService) Start(_ interface{ Done() <-chan struct{} }) error {
	if m.startErr != nil {
		return m.startErr
	}
	m.running = true
	return nil
}

func (m *mockDemoService) Stop(_ interface{ Done() <-chan struct{} }) error {
	if m.stopErr != nil {
		return m.stopErr
	}
	m.running = false
	return nil
}

func (m *mockDemoService) Restart(_ interface{ Done() <-chan struct{} }) error {
	return m.restartErr
}

func (m *mockDemoService) Rebuild(_ interface{ Done() <-chan struct{} }) error {
	return m.rebuildErr
}

func (m *mockDemoService) Status(_ interface{ Done() <-chan struct{} }) *demo.Status {
	if m.status != nil {
		return m.status
	}
	return &demo.Status{
		Running: m.running,
		Port:    8081,
	}
}

func (m *mockDemoService) GetLogs(_ interface{ Done() <-chan struct{} }) (string, error) {
	if m.logsErr != nil {
		return "", m.logsErr
	}
	return m.logs, nil
}

func (m *mockDemoService) IsRunning() bool {
	return m.running
}

func (m *mockDemoService) SetWorkspacePath(path string) {
	m.workspacePath = path
}

func TestHandleDemoStatus(t *testing.T) {
	server := NewServer(nil, "/tmp", nil, nil)
	mockDemo := &mockDemoService{
		running: true,
		status: &demo.Status{
			Running: true,
			Port:    8081,
			URL:     "http://localhost:8081",
		},
	}
	server.SetDemoService(mockDemo)

	req := httptest.NewRequest(http.MethodGet, "/api/demo/status", nil)
	w := httptest.NewRecorder()

	server.handleDemoStatus(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var status demo.Status
	if err := json.NewDecoder(w.Body).Decode(&status); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !status.Running {
		t.Error("expected Running = true")
	}
	if status.Port != 8081 {
		t.Errorf("expected Port = 8081, got %d", status.Port)
	}
}

func TestHandleDemoStatus_NoService(t *testing.T) {
	server := NewServer(nil, "/tmp", nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/demo/status", nil)
	w := httptest.NewRecorder()

	server.handleDemoStatus(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", w.Code)
	}
}

func TestHandleDemoStatus_WrongMethod(t *testing.T) {
	server := NewServer(nil, "/tmp", nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/demo/status", nil)
	w := httptest.NewRecorder()

	server.handleDemoStatus(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", w.Code)
	}
}

func TestHandleDemoStart_Success(t *testing.T) {
	server := NewServer(nil, "/tmp", nil, nil)
	mockDemo := &mockDemoService{running: false}
	server.SetDemoService(mockDemo)

	req := httptest.NewRequest(http.MethodPost, "/api/demo/start", nil)
	w := httptest.NewRecorder()

	server.handleDemoStart(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	if !mockDemo.running {
		t.Error("expected demo to be running after start")
	}
}

func TestHandleDemoStart_AlreadyRunning(t *testing.T) {
	server := NewServer(nil, "/tmp", nil, nil)
	mockDemo := &mockDemoService{running: true}
	server.SetDemoService(mockDemo)

	req := httptest.NewRequest(http.MethodPost, "/api/demo/start", nil)
	w := httptest.NewRecorder()

	server.handleDemoStart(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("expected status 409, got %d", w.Code)
	}
}

func TestHandleDemoStart_NoService(t *testing.T) {
	server := NewServer(nil, "/tmp", nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/demo/start", nil)
	w := httptest.NewRecorder()

	server.handleDemoStart(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", w.Code)
	}
}

func TestHandleDemoStart_Error(t *testing.T) {
	server := NewServer(nil, "/tmp", nil, nil)
	mockDemo := &mockDemoService{
		running:  false,
		startErr: context.DeadlineExceeded,
	}
	server.SetDemoService(mockDemo)

	req := httptest.NewRequest(http.MethodPost, "/api/demo/start", nil)
	w := httptest.NewRecorder()

	server.handleDemoStart(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestHandleDemoStop_Success(t *testing.T) {
	server := NewServer(nil, "/tmp", nil, nil)
	mockDemo := &mockDemoService{running: true}
	server.SetDemoService(mockDemo)

	req := httptest.NewRequest(http.MethodPost, "/api/demo/stop", nil)
	w := httptest.NewRecorder()

	server.handleDemoStop(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	if mockDemo.running {
		t.Error("expected demo to not be running after stop")
	}
}

func TestHandleDemoStop_NotRunning(t *testing.T) {
	server := NewServer(nil, "/tmp", nil, nil)
	mockDemo := &mockDemoService{running: false}
	server.SetDemoService(mockDemo)

	req := httptest.NewRequest(http.MethodPost, "/api/demo/stop", nil)
	w := httptest.NewRecorder()

	server.handleDemoStop(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("expected status 409, got %d", w.Code)
	}
}

func TestHandleDemoRestart_Success(t *testing.T) {
	server := NewServer(nil, "/tmp", nil, nil)
	mockDemo := &mockDemoService{running: true}
	server.SetDemoService(mockDemo)

	req := httptest.NewRequest(http.MethodPost, "/api/demo/restart", nil)
	w := httptest.NewRecorder()

	server.handleDemoRestart(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

func TestHandleDemoRestart_NotRunning(t *testing.T) {
	server := NewServer(nil, "/tmp", nil, nil)
	mockDemo := &mockDemoService{running: false}
	server.SetDemoService(mockDemo)

	req := httptest.NewRequest(http.MethodPost, "/api/demo/restart", nil)
	w := httptest.NewRecorder()

	server.handleDemoRestart(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("expected status 409, got %d", w.Code)
	}
}

func TestHandleDemoRebuild_Success(t *testing.T) {
	server := NewServer(nil, "/tmp", nil, nil)
	mockDemo := &mockDemoService{running: true}
	server.SetDemoService(mockDemo)

	req := httptest.NewRequest(http.MethodPost, "/api/demo/rebuild", nil)
	w := httptest.NewRecorder()

	server.handleDemoRebuild(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

func TestHandleDemoLogs_Success(t *testing.T) {
	server := NewServer(nil, "/tmp", nil, nil)
	mockDemo := &mockDemoService{
		running: true,
		logs:    "line 1\nline 2\nline 3",
	}
	server.SetDemoService(mockDemo)

	req := httptest.NewRequest(http.MethodGet, "/api/demo/logs", nil)
	w := httptest.NewRecorder()

	server.handleDemoLogs(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	logs, ok := response["logs"].(string)
	if !ok {
		t.Fatal("expected logs to be a string")
	}
	if logs != "line 1\nline 2\nline 3" {
		t.Errorf("unexpected logs: %q", logs)
	}
}

func TestHandleDemoLogs_Error(t *testing.T) {
	server := NewServer(nil, "/tmp", nil, nil)
	mockDemo := &mockDemoService{
		running: true,
		logsErr: context.DeadlineExceeded,
	}
	server.SetDemoService(mockDemo)

	req := httptest.NewRequest(http.MethodGet, "/api/demo/logs", nil)
	w := httptest.NewRecorder()

	server.handleDemoLogs(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestHandleDemoLogs_WrongMethod(t *testing.T) {
	server := NewServer(nil, "/tmp", nil, nil)
	mockDemo := &mockDemoService{running: true}
	server.SetDemoService(mockDemo)

	req := httptest.NewRequest(http.MethodPost, "/api/demo/logs", nil)
	w := httptest.NewRecorder()

	server.handleDemoLogs(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", w.Code)
	}
}
