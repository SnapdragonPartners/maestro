package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/config"
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/proto"
)

type mockAgent struct {
	id    string
	typ   agent.Type
	state string
}

func (m *mockAgent) GetID() string {
	return m.id
}

func (m *mockAgent) GetAgentType() agent.Type {
	return m.typ
}

func (m *mockAgent) GetCurrentState() proto.State {
	return proto.State(m.state)
}

func (m *mockAgent) GetStateData() agent.StateData {
	return make(agent.StateData)
}

func (m *mockAgent) ValidateState(_ proto.State) error {
	return nil
}

func (m *mockAgent) GetValidStates() []proto.State {
	return []proto.State{proto.State(m.state)}
}

func (m *mockAgent) Initialize(_ context.Context) error {
	return nil
}

func (m *mockAgent) Run(_ context.Context) error {
	return nil
}

func (m *mockAgent) Step(_ context.Context) (bool, error) {
	return true, nil
}

func (m *mockAgent) ProcessMessage(_ context.Context, _ *proto.AgentMsg) (*proto.AgentMsg, error) {
	return &proto.AgentMsg{}, nil
}

func (m *mockAgent) Shutdown(_ context.Context) error {
	return nil
}

func createTestConfig() *config.Config {
	return &config.Config{
		Agents: &config.AgentConfig{
			MaxCoders:      3,
			CoderModel:     config.ModelClaudeSonnetLatest,
			ArchitectModel: config.ModelOpenAIO3Mini,
			Resilience: config.ResilienceConfig{
				RateLimit: config.RateLimitConfig{
					Anthropic: config.ProviderLimits{
						TokensPerMinute: 300000,
						MaxConcurrency:  5,
					},
					OpenAI: config.ProviderLimits{
						TokensPerMinute: 150000,
						MaxConcurrency:  5,
					},
				},
				CircuitBreaker: config.CircuitBreakerConfig{
					FailureThreshold: 5,
					SuccessThreshold: 3,
					Timeout:          30_000_000_000,
				},
				Retry: config.RetryConfig{
					MaxAttempts:   3,
					InitialDelay:  100_000_000,
					MaxDelay:      10_000_000_000,
					BackoffFactor: 2,
					Jitter:        true,
				},
				Timeout: 180_000_000_000,
			},
			Metrics: config.MetricsConfig{
				Enabled: false,
			},
		},
	}
}

// createTestLLMFactory creates a minimal LLM factory for testing.
func createTestLLMFactory(t *testing.T) *agent.LLMClientFactory {
	t.Helper()
	cfg := createTestConfig()
	factory, err := agent.NewLLMClientFactory(cfg)
	if err != nil {
		t.Fatalf("Failed to create test LLM factory: %v", err)
	}
	return factory
}

func TestHandleAgents(t *testing.T) {
	cfg := createTestConfig()
	dispatcher, err := dispatch.NewDispatcher(cfg)
	if err != nil {
		t.Fatalf("Failed to create dispatcher: %v", err)
	}

	ctx := context.Background()
	if err := dispatcher.Start(ctx); err != nil {
		t.Fatalf("Failed to start dispatcher: %v", err)
	}
	defer dispatcher.Stop(ctx)

	architectID := "architect-001"
	architectAgent := &mockAgent{
		id:    architectID,
		typ:   agent.TypeArchitect,
		state: "SPEC_PARSING",
	}
	if err := dispatcher.RegisterAgent(architectAgent); err != nil {
		t.Fatalf("Failed to register architect: %v", err)
	}

	coderID := "coder-001"
	coderAgent := &mockAgent{
		id:    coderID,
		typ:   agent.TypeCoder,
		state: "PLANNING",
	}
	if err := dispatcher.RegisterAgent(coderAgent); err != nil {
		t.Fatalf("Failed to register coder: %v", err)
	}

	llmFactory := createTestLLMFactory(t)
	defer llmFactory.Stop()

	server := NewServer(dispatcher, "/tmp/test", nil, llmFactory)

	req := httptest.NewRequest("GET", "/api/agents", nil)
	w := httptest.NewRecorder()

	server.handleAgents(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var agents []AgentListItem
	if err := json.NewDecoder(w.Body).Decode(&agents); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if len(agents) != 2 {
		t.Errorf("Expected 2 agents, got %d", len(agents))
	}

	foundArchitect := false
	foundCoder := false
	for _, a := range agents {
		if a.ID == architectID && a.Role == agent.TypeArchitect.String() && a.State == "SPEC_PARSING" {
			foundArchitect = true
		}
		if a.ID == coderID && a.Role == agent.TypeCoder.String() && a.State == "PLANNING" {
			foundCoder = true
		}
	}

	if !foundArchitect {
		t.Error("Architect not found in response")
	}
	if !foundCoder {
		t.Error("Coder not found in response")
	}
}

func TestHandleAgent(t *testing.T) {
	cfg := createTestConfig()
	dispatcher, err := dispatch.NewDispatcher(cfg)
	if err != nil {
		t.Fatalf("Failed to create dispatcher: %v", err)
	}

	ctx := context.Background()
	if err := dispatcher.Start(ctx); err != nil {
		t.Fatalf("Failed to start dispatcher: %v", err)
	}
	defer dispatcher.Stop(ctx)

	architectID := "architect-001"
	architectAgent := &mockAgent{
		id:    architectID,
		typ:   agent.TypeArchitect,
		state: "SPEC_PARSING",
	}
	if err := dispatcher.RegisterAgent(architectAgent); err != nil {
		t.Fatalf("Failed to register architect: %v", err)
	}

	llmFactory := createTestLLMFactory(t)
	defer llmFactory.Stop()

	server := NewServer(dispatcher, "/tmp/test", nil, llmFactory)

	req := httptest.NewRequest("GET", "/api/agent/"+architectID, nil)
	w := httptest.NewRecorder()

	server.handleAgent(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response["id"] != architectID {
		t.Errorf("Expected agent ID %s, got %v", architectID, response["id"])
	}
	if response["type"] != agent.TypeArchitect.String() {
		t.Errorf("Expected agent type %s, got %v", agent.TypeArchitect.String(), response["type"])
	}
	if response["state"] != "SPEC_PARSING" {
		t.Errorf("Expected agent state SPEC_PARSING, got %v", response["state"])
	}
}

func TestHandleAgentNotFound(t *testing.T) {
	cfg := createTestConfig()
	dispatcher, err := dispatch.NewDispatcher(cfg)
	if err != nil {
		t.Fatalf("Failed to create dispatcher: %v", err)
	}

	ctx := context.Background()
	if err := dispatcher.Start(ctx); err != nil {
		t.Fatalf("Failed to start dispatcher: %v", err)
	}
	defer dispatcher.Stop(ctx)

	llmFactory := createTestLLMFactory(t)
	defer llmFactory.Stop()

	server := NewServer(dispatcher, "/tmp/test", nil, llmFactory)

	req := httptest.NewRequest("GET", "/api/agent/nonexistent", nil)
	w := httptest.NewRecorder()

	server.handleAgent(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w.Code)
	}
}

func TestFindArchitectState(t *testing.T) {
	cfg := createTestConfig()
	dispatcher, err := dispatch.NewDispatcher(cfg)
	if err != nil {
		t.Fatalf("Failed to create dispatcher: %v", err)
	}

	ctx := context.Background()
	if startErr := dispatcher.Start(ctx); startErr != nil {
		t.Fatalf("Failed to start dispatcher: %v", startErr)
	}
	defer dispatcher.Stop(ctx)

	architectID := "architect-001"
	architectAgent := &mockAgent{
		id:    architectID,
		typ:   agent.TypeArchitect,
		state: "DISPATCHING",
	}
	if regErr := dispatcher.RegisterAgent(architectAgent); regErr != nil {
		t.Fatalf("Failed to register architect: %v", regErr)
	}

	llmFactory := createTestLLMFactory(t)
	defer llmFactory.Stop()

	server := NewServer(dispatcher, "/tmp/test", nil, llmFactory)

	state, err := server.findArchitectState()
	if err != nil {
		t.Fatalf("Expected to find architect, got error: %v", err)
	}

	if state != "DISPATCHING" {
		t.Errorf("Expected architect state DISPATCHING, got %s", state)
	}
}

func TestFindArchitectStateNoArchitect(t *testing.T) {
	cfg := createTestConfig()
	dispatcher, err := dispatch.NewDispatcher(cfg)
	if err != nil {
		t.Fatalf("Failed to create dispatcher: %v", err)
	}

	ctx := context.Background()
	if startErr := dispatcher.Start(ctx); startErr != nil {
		t.Fatalf("Failed to start dispatcher: %v", startErr)
	}
	defer dispatcher.Stop(ctx)

	llmFactory := createTestLLMFactory(t)
	defer llmFactory.Stop()

	server := NewServer(dispatcher, "/tmp/test", nil, llmFactory)

	_, err = server.findArchitectState()
	if err == nil {
		t.Error("Expected error when no architect registered, got nil")
	}
}

func TestRequireAuthFastPath(t *testing.T) {
	cfg := createTestConfig()
	dispatcher, _ := dispatch.NewDispatcher(cfg)
	llmFactory := createTestLLMFactory(t)
	defer llmFactory.Stop()

	server := NewServer(dispatcher, t.TempDir(), nil, llmFactory)

	// Set password in memory
	config.SetProjectPassword("test-password")
	defer config.SetProjectPassword("")

	handler := server.requireAuth(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Correct password
	req := httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("maestro", "test-password")
	w := httptest.NewRecorder()
	handler(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 for correct password, got %d", w.Code)
	}

	// Wrong password
	req = httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("maestro", "wrong")
	w = httptest.NewRecorder()
	handler(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 for wrong password, got %d", w.Code)
	}

	// No credentials
	req = httptest.NewRequest("GET", "/", nil)
	w = httptest.NewRecorder()
	handler(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 for no credentials, got %d", w.Code)
	}

	// Wrong username
	req = httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("admin", "test-password")
	w = httptest.NewRecorder()
	handler(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 for wrong username, got %d", w.Code)
	}
}

func TestRequireAuthVerifierRecovery(t *testing.T) {
	cfg := createTestConfig()
	dispatcher, _ := dispatch.NewDispatcher(cfg)
	llmFactory := createTestLLMFactory(t)
	defer llmFactory.Stop()

	workDir := t.TempDir()
	server := NewServer(dispatcher, workDir, nil, llmFactory)

	// Create a verifier but do NOT set password in memory
	password := "recovery-password"
	if err := config.SavePasswordVerifier(workDir, password); err != nil {
		t.Fatalf("Failed to save verifier: %v", err)
	}

	// Clear any in-memory password and temporarily unset env var
	config.SetProjectPassword("")
	if orig := os.Getenv("MAESTRO_PASSWORD"); orig != "" {
		t.Setenv("MAESTRO_PASSWORD", "")
	}

	handler := server.requireAuth(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Correct password should recover
	req := httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("maestro", password)
	w := httptest.NewRecorder()
	handler(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 for correct password via verifier recovery, got %d", w.Code)
	}

	// After recovery, password should be in memory
	if config.GetWebUIPassword() == "" {
		t.Error("Password should be cached in memory after recovery")
	}

	// Clean up
	config.SetProjectPassword("")
}

func TestRequireAuthVerifierWrongPassword(t *testing.T) {
	cfg := createTestConfig()
	dispatcher, _ := dispatch.NewDispatcher(cfg)
	llmFactory := createTestLLMFactory(t)
	defer llmFactory.Stop()

	workDir := t.TempDir()
	server := NewServer(dispatcher, workDir, nil, llmFactory)

	// Create a verifier but do NOT set password in memory
	if err := config.SavePasswordVerifier(workDir, "correct-password"); err != nil {
		t.Fatalf("Failed to save verifier: %v", err)
	}
	config.SetProjectPassword("")
	if orig := os.Getenv("MAESTRO_PASSWORD"); orig != "" {
		t.Setenv("MAESTRO_PASSWORD", "")
	}

	handler := server.requireAuth(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Wrong password should fail
	req := httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("maestro", "wrong-password")
	w := httptest.NewRecorder()
	handler(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 for wrong password via verifier, got %d", w.Code)
	}

	// Password should NOT be cached
	if config.GetWebUIPassword() != "" {
		t.Error("Password should not be cached after failed recovery")
	}
}

func TestSessionTokenExchange(t *testing.T) {
	cfg := createTestConfig()
	dispatcher, _ := dispatch.NewDispatcher(cfg)
	llmFactory := createTestLLMFactory(t)
	defer llmFactory.Stop()

	server := NewServer(dispatcher, t.TempDir(), nil, llmFactory)
	server.sessionToken = "test-token-abc123"

	// Set a password so the HMAC cookie signing works
	config.SetProjectPassword("test-password")
	defer config.SetProjectPassword("")

	// Valid token should set cookie and redirect
	req := httptest.NewRequest("GET", "/auth/session?token=test-token-abc123", nil)
	w := httptest.NewRecorder()
	server.handleSessionAuth(w, req)

	if w.Code != http.StatusFound {
		t.Errorf("Expected 302 redirect, got %d", w.Code)
	}
	if w.Header().Get("Location") != "/" {
		t.Errorf("Expected redirect to /, got %s", w.Header().Get("Location"))
	}

	// Check cookie was set
	cookies := w.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "maestro_session" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("Expected maestro_session cookie to be set")
	}
	if !sessionCookie.HttpOnly {
		t.Error("Session cookie should be HttpOnly")
	}
	if sessionCookie.SameSite != http.SameSiteStrictMode {
		t.Error("Session cookie should have SameSite=Strict")
	}
}

func TestSessionTokenOneTimeUse(t *testing.T) {
	cfg := createTestConfig()
	dispatcher, _ := dispatch.NewDispatcher(cfg)
	llmFactory := createTestLLMFactory(t)
	defer llmFactory.Stop()

	server := NewServer(dispatcher, t.TempDir(), nil, llmFactory)
	server.sessionToken = "one-time-token"

	config.SetProjectPassword("test-password")
	defer config.SetProjectPassword("")

	// First use: should succeed
	req := httptest.NewRequest("GET", "/auth/session?token=one-time-token", nil)
	w := httptest.NewRecorder()
	server.handleSessionAuth(w, req)
	if w.Code != http.StatusFound {
		t.Errorf("First use: expected 302, got %d", w.Code)
	}

	// Second use: should fail (token invalidated)
	req = httptest.NewRequest("GET", "/auth/session?token=one-time-token", nil)
	w = httptest.NewRecorder()
	server.handleSessionAuth(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("Second use: expected 403, got %d", w.Code)
	}
}

func TestSessionTokenInvalid(t *testing.T) {
	cfg := createTestConfig()
	dispatcher, _ := dispatch.NewDispatcher(cfg)
	llmFactory := createTestLLMFactory(t)
	defer llmFactory.Stop()

	server := NewServer(dispatcher, t.TempDir(), nil, llmFactory)
	server.sessionToken = "real-token"

	// Wrong token
	req := httptest.NewRequest("GET", "/auth/session?token=wrong-token", nil)
	w := httptest.NewRecorder()
	server.handleSessionAuth(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("Expected 403 for wrong token, got %d", w.Code)
	}

	// Missing token param
	req = httptest.NewRequest("GET", "/auth/session", nil)
	w = httptest.NewRecorder()
	server.handleSessionAuth(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 for missing token, got %d", w.Code)
	}

	// No session token configured
	server.sessionToken = ""
	req = httptest.NewRequest("GET", "/auth/session?token=anything", nil)
	w = httptest.NewRecorder()
	server.handleSessionAuth(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("Expected 403 when no token configured, got %d", w.Code)
	}
}

func TestSessionCookieBypassesBasicAuth(t *testing.T) {
	cfg := createTestConfig()
	dispatcher, _ := dispatch.NewDispatcher(cfg)
	llmFactory := createTestLLMFactory(t)
	defer llmFactory.Stop()

	server := NewServer(dispatcher, t.TempDir(), nil, llmFactory)
	server.sessionToken = "exchange-token"

	config.SetProjectPassword("test-password")
	defer config.SetProjectPassword("")

	// Exchange token for cookie
	req := httptest.NewRequest("GET", "/auth/session?token=exchange-token", nil)
	w := httptest.NewRecorder()
	server.handleSessionAuth(w, req)

	// Extract the cookie
	cookies := w.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "maestro_session" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("Expected session cookie from token exchange")
	}

	// Use cookie to access a protected endpoint (no Basic Auth)
	handler := server.requireAuth(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req = httptest.NewRequest("GET", "/", nil)
	req.AddCookie(sessionCookie)
	w = httptest.NewRecorder()
	handler(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 with valid session cookie, got %d", w.Code)
	}

	// Invalid cookie should fall back to Basic Auth (and fail without credentials)
	req = httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "maestro_session", Value: "invalid-value"})
	w = httptest.NewRecorder()
	handler(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 with invalid session cookie and no Basic Auth, got %d", w.Code)
	}
}

func TestEmbeddedTemplates(t *testing.T) {
	cfg := createTestConfig()
	dispatcher, err := dispatch.NewDispatcher(cfg)
	if err != nil {
		t.Fatalf("Failed to create dispatcher: %v", err)
	}

	llmFactory := createTestLLMFactory(t)
	defer llmFactory.Stop()

	server := NewServer(dispatcher, "/tmp/test", nil, llmFactory)

	if server.templates == nil {
		t.Error("Templates should be loaded from embedded filesystem")
	}
}
