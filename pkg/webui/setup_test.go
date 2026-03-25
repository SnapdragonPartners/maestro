package webui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
)

func TestSetupMode_Toggle(t *testing.T) {
	s := &Server{
		setupReady: make(chan struct{}, 1),
	}

	if s.IsSetupMode() {
		t.Error("Setup mode should be false by default")
	}

	s.SetSetupMode(true)
	if !s.IsSetupMode() {
		t.Error("Setup mode should be true after enabling")
	}

	s.SetSetupMode(false)
	if s.IsSetupMode() {
		t.Error("Setup mode should be false after disabling")
	}
}

func TestSetupModeRedirect_Active(t *testing.T) {
	s := &Server{
		setupReady: make(chan struct{}, 1),
	}
	s.SetSetupMode(true)

	handler := s.setupModeRedirect(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Request to / should redirect
	req := httptest.NewRequest("GET", "/", http.NoBody)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusTemporaryRedirect {
		t.Errorf("Expected redirect (307), got %d", w.Code)
	}
	if w.Header().Get("Location") != "/setup" {
		t.Errorf("Expected redirect to /setup, got %s", w.Header().Get("Location"))
	}
}

func TestSetupModeRedirect_Inactive(t *testing.T) {
	s := &Server{
		setupReady: make(chan struct{}, 1),
	}
	// Setup mode is off by default

	called := false
	handler := s.setupModeRedirect(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/", http.NoBody)
	w := httptest.NewRecorder()
	handler(w, req)

	if !called {
		t.Error("Handler should be called when setup mode is inactive")
	}
	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", w.Code)
	}
}

func TestSetupModeRedirect_AllowedPaths(t *testing.T) {
	s := &Server{
		setupReady: make(chan struct{}, 1),
	}
	s.SetSetupMode(true)

	handler := s.setupModeRedirect(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	allowedPaths := []string{
		"/setup",
		"/api/setup/status",
		"/api/setup/recheck",
		"/api/secrets",
		"/api/secrets/ANTHROPIC_API_KEY",
		"/api/healthz",
		"/static/css/tailwind.css",
		"/static/js/maestro.js",
	}

	for _, path := range allowedPaths {
		req := httptest.NewRequest("GET", path, http.NoBody)
		w := httptest.NewRecorder()
		handler(w, req)

		if w.Code == http.StatusTemporaryRedirect {
			t.Errorf("Path %s should not be redirected during setup mode", path)
		}
	}

	// Non-allowed paths should redirect
	blockedPaths := []string{"/", "/dashboard", "/api/agents"}
	for _, path := range blockedPaths {
		req := httptest.NewRequest("GET", path, http.NoBody)
		w := httptest.NewRecorder()
		handler(w, req)

		if w.Code != http.StatusTemporaryRedirect {
			t.Errorf("Path %s should be redirected during setup mode, got %d", path, w.Code)
		}
	}
}

func TestSetupStatus_Response(t *testing.T) {
	// Set up test config
	cfg := &config.Config{
		OperatingMode: config.OperatingModeStandard,
		Agents: &config.AgentConfig{
			CoderModel:     "claude-sonnet-4-5",
			ArchitectModel: "o3-mini",
			PMModel:        "claude-opus-4-5",
		},
	}
	config.SetConfigForTesting(cfg)
	defer config.SetConfigForTesting(nil)

	s := &Server{
		setupReady: make(chan struct{}, 1),
	}
	s.SetSetupMode(true)

	req := httptest.NewRequest("GET", "/api/setup/status", http.NoBody)
	w := httptest.NewRecorder()
	s.handleSetupStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", w.Code)
	}

	var resp setupStatusResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if !resp.SetupMode {
		t.Error("Expected setup_mode=true")
	}

	if len(resp.Keys) == 0 {
		t.Error("Expected at least one key in response")
	}
}

func TestNotifySetupIfReady_SignalsWhenReady(t *testing.T) {
	cfg := &config.Config{
		OperatingMode: config.OperatingModeStandard,
		Agents: &config.AgentConfig{
			CoderModel:     "claude-sonnet-4-5",
			ArchitectModel: "o3-mini",
			PMModel:        "claude-opus-4-5",
		},
	}
	config.SetConfigForTesting(cfg)
	defer config.SetConfigForTesting(nil)

	// Set all required keys
	origAnthropic := os.Getenv("ANTHROPIC_API_KEY")
	origOpenAI := os.Getenv("OPENAI_API_KEY")
	origGitHub := os.Getenv("GITHUB_TOKEN")
	defer func() {
		restoreEnv("ANTHROPIC_API_KEY", origAnthropic)
		restoreEnv("OPENAI_API_KEY", origOpenAI)
		restoreEnv("GITHUB_TOKEN", origGitHub)
	}()
	os.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	os.Setenv("OPENAI_API_KEY", "sk-openai-test")
	os.Setenv("GITHUB_TOKEN", "ghp_test")

	s := &Server{
		setupReady: make(chan struct{}, 1),
	}
	s.SetSetupMode(true)

	s.notifySetupIfReady()

	select {
	case <-s.setupReady:
		// Got the signal - correct
	case <-time.After(100 * time.Millisecond):
		t.Error("Expected setupReady signal when all keys are present")
	}
}

func TestNotifySetupIfReady_NoSignalWhenMissing(t *testing.T) {
	cfg := &config.Config{
		OperatingMode: config.OperatingModeStandard,
		Agents: &config.AgentConfig{
			CoderModel:     "claude-sonnet-4-5",
			ArchitectModel: "o3-mini",
			PMModel:        "claude-opus-4-5",
		},
	}
	config.SetConfigForTesting(cfg)
	defer config.SetConfigForTesting(nil)

	// Clear all keys
	origAnthropic := os.Getenv("ANTHROPIC_API_KEY")
	origOpenAI := os.Getenv("OPENAI_API_KEY")
	origGitHub := os.Getenv("GITHUB_TOKEN")
	defer func() {
		restoreEnv("ANTHROPIC_API_KEY", origAnthropic)
		restoreEnv("OPENAI_API_KEY", origOpenAI)
		restoreEnv("GITHUB_TOKEN", origGitHub)
	}()
	os.Unsetenv("ANTHROPIC_API_KEY")
	os.Unsetenv("OPENAI_API_KEY")
	os.Unsetenv("GITHUB_TOKEN")
	config.SetDecryptedSecrets(nil)

	s := &Server{
		setupReady: make(chan struct{}, 1),
	}
	s.SetSetupMode(true)

	s.notifySetupIfReady()

	select {
	case <-s.setupReady:
		t.Error("Should not signal when keys are missing")
	case <-time.After(50 * time.Millisecond):
		// Correct - no signal
	}
}

func TestNotifySetupIfReady_MultipleSignalsNoPanic(_ *testing.T) {
	cfg := &config.Config{
		OperatingMode: config.OperatingModeStandard,
		Agents: &config.AgentConfig{
			CoderModel:     "claude-sonnet-4-5",
			ArchitectModel: "o3-mini",
			PMModel:        "claude-opus-4-5",
		},
	}
	config.SetConfigForTesting(cfg)
	defer config.SetConfigForTesting(nil)

	origAnthropic := os.Getenv("ANTHROPIC_API_KEY")
	origOpenAI := os.Getenv("OPENAI_API_KEY")
	origGitHub := os.Getenv("GITHUB_TOKEN")
	defer func() {
		restoreEnv("ANTHROPIC_API_KEY", origAnthropic)
		restoreEnv("OPENAI_API_KEY", origOpenAI)
		restoreEnv("GITHUB_TOKEN", origGitHub)
	}()
	os.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	os.Setenv("OPENAI_API_KEY", "sk-openai-test")
	os.Setenv("GITHUB_TOKEN", "ghp_test")

	s := &Server{
		setupReady: make(chan struct{}, 1),
	}
	s.SetSetupMode(true)

	// Multiple signals should not panic
	for i := 0; i < 10; i++ {
		s.notifySetupIfReady()
	}
}

func TestWaitForSetup_ReturnsImmediatelyWhenReady(t *testing.T) {
	cfg := &config.Config{
		OperatingMode: config.OperatingModeStandard,
		Agents: &config.AgentConfig{
			CoderModel:     "claude-sonnet-4-5",
			ArchitectModel: "o3-mini",
			PMModel:        "claude-opus-4-5",
		},
	}
	config.SetConfigForTesting(cfg)
	defer config.SetConfigForTesting(nil)

	origAnthropic := os.Getenv("ANTHROPIC_API_KEY")
	origOpenAI := os.Getenv("OPENAI_API_KEY")
	origGitHub := os.Getenv("GITHUB_TOKEN")
	defer func() {
		restoreEnv("ANTHROPIC_API_KEY", origAnthropic)
		restoreEnv("OPENAI_API_KEY", origOpenAI)
		restoreEnv("GITHUB_TOKEN", origGitHub)
	}()
	os.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	os.Setenv("OPENAI_API_KEY", "sk-openai-test")
	os.Setenv("GITHUB_TOKEN", "ghp_test")

	s := &Server{
		setupReady: make(chan struct{}, 1),
		logger:     logx.NewLogger("webui-test"),
	}

	ctx := context.Background()
	err := s.WaitForSetup(ctx, cfg)
	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
	if s.IsSetupMode() {
		t.Error("Should not be in setup mode after WaitForSetup returns")
	}
}

func TestWaitForSetup_BlocksAndUnblocks(t *testing.T) {
	cfg := &config.Config{
		OperatingMode: config.OperatingModeStandard,
		Agents: &config.AgentConfig{
			CoderModel:     "claude-sonnet-4-5",
			ArchitectModel: "o3-mini",
			PMModel:        "claude-opus-4-5",
		},
		WebUI: &config.WebUIConfig{
			Host: "localhost",
			Port: 9999,
		},
	}
	config.SetConfigForTesting(cfg)
	defer config.SetConfigForTesting(nil)

	// Start with missing keys
	origAnthropic := os.Getenv("ANTHROPIC_API_KEY")
	origOpenAI := os.Getenv("OPENAI_API_KEY")
	origGitHub := os.Getenv("GITHUB_TOKEN")
	defer func() {
		restoreEnv("ANTHROPIC_API_KEY", origAnthropic)
		restoreEnv("OPENAI_API_KEY", origOpenAI)
		restoreEnv("GITHUB_TOKEN", origGitHub)
		config.SetDecryptedSecrets(nil)
	}()
	os.Unsetenv("ANTHROPIC_API_KEY")
	os.Setenv("OPENAI_API_KEY", "sk-openai-test")
	os.Setenv("GITHUB_TOKEN", "ghp_test")
	config.SetDecryptedSecrets(nil)

	s := &Server{
		setupReady: make(chan struct{}, 1),
		logger:     logx.NewLogger("webui-test"),
	}

	done := make(chan error, 1)
	go func() {
		done <- s.WaitForSetup(context.Background(), cfg)
	}()

	// Give WaitForSetup time to enter the loop
	time.Sleep(50 * time.Millisecond)

	if !s.IsSetupMode() {
		t.Fatal("Should be in setup mode while waiting")
	}

	// Simulate adding the missing key
	os.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	s.notifySetupIfReady()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForSetup did not return after keys were added")
	}

	if s.IsSetupMode() {
		t.Error("Should not be in setup mode after WaitForSetup returns")
	}
}

func TestWaitForSetup_RespectsContextCancellation(t *testing.T) {
	cfg := &config.Config{
		OperatingMode: config.OperatingModeStandard,
		Agents: &config.AgentConfig{
			CoderModel:     "claude-sonnet-4-5",
			ArchitectModel: "o3-mini",
			PMModel:        "claude-opus-4-5",
		},
	}
	config.SetConfigForTesting(cfg)
	defer config.SetConfigForTesting(nil)

	// Clear all keys
	origAnthropic := os.Getenv("ANTHROPIC_API_KEY")
	origOpenAI := os.Getenv("OPENAI_API_KEY")
	origGitHub := os.Getenv("GITHUB_TOKEN")
	defer func() {
		restoreEnv("ANTHROPIC_API_KEY", origAnthropic)
		restoreEnv("OPENAI_API_KEY", origOpenAI)
		restoreEnv("GITHUB_TOKEN", origGitHub)
	}()
	os.Unsetenv("ANTHROPIC_API_KEY")
	os.Unsetenv("OPENAI_API_KEY")
	os.Unsetenv("GITHUB_TOKEN")
	config.SetDecryptedSecrets(nil)

	s := &Server{
		setupReady: make(chan struct{}, 1),
		logger:     logx.NewLogger("webui-test"),
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- s.WaitForSetup(ctx, cfg)
	}()

	// Give time to enter loop, then cancel
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Expected context.Canceled, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForSetup did not return after context cancellation")
	}
}

func restoreEnv(key, value string) {
	if value != "" {
		os.Setenv(key, value)
	} else {
		os.Unsetenv(key)
	}
}
