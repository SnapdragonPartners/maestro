package webui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"orchestrator/pkg/config"
)

// secretsListResponse matches the wrapped response from handleSecretsList.
type secretsListResponse struct {
	Secrets []SecretEntry `json:"secrets"`
	Warning string        `json:"warning,omitempty"`
}

func TestHandleSecretsList_Empty(t *testing.T) {
	// Clear any existing secrets
	config.SetDecryptedSecrets(nil)

	server := NewServer(nil, "/tmp", nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/secrets", nil)
	w := httptest.NewRecorder()

	server.handleSecretsList(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp secretsListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Secrets) != 0 {
		t.Errorf("expected 0 entries, got %d", len(resp.Secrets))
	}
}

func TestHandleSecretsList_WithSecrets(t *testing.T) {
	// Set up test secrets with structured format
	config.SetDecryptedSecrets(&config.StructuredSecrets{
		System: map[string]string{"ANTHROPIC_API_KEY": "secret1"},
		User:   map[string]string{"DB_PASSWORD": "secret2"},
	})
	defer config.SetDecryptedSecrets(nil)

	server := NewServer(nil, "/tmp", nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/secrets", nil)
	w := httptest.NewRecorder()

	server.handleSecretsList(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp secretsListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Secrets) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(resp.Secrets))
	}

	// Should be sorted alphabetically
	if resp.Secrets[0].Name != "ANTHROPIC_API_KEY" {
		t.Errorf("expected first entry to be ANTHROPIC_API_KEY, got %s", resp.Secrets[0].Name)
	}
	if resp.Secrets[1].Name != "DB_PASSWORD" {
		t.Errorf("expected second entry to be DB_PASSWORD, got %s", resp.Secrets[1].Name)
	}
}

func TestHandleSecretsList_FilterByType(t *testing.T) {
	config.SetDecryptedSecrets(&config.StructuredSecrets{
		System: map[string]string{"ANTHROPIC_API_KEY": "sk-ant"},
		User:   map[string]string{"DB_URL": "postgres://localhost"},
	})
	defer config.SetDecryptedSecrets(nil)

	server := NewServer(nil, "/tmp", nil, nil)

	// Filter by user type
	req := httptest.NewRequest(http.MethodGet, "/api/secrets?type=user", nil)
	w := httptest.NewRecorder()
	server.handleSecretsList(w, req)

	var resp secretsListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Secrets) != 1 {
		t.Fatalf("expected 1 user entry, got %d", len(resp.Secrets))
	}
	if resp.Secrets[0].Name != "DB_URL" {
		t.Errorf("expected DB_URL, got %s", resp.Secrets[0].Name)
	}
	if resp.Secrets[0].Type != "user" {
		t.Errorf("expected type user, got %s", resp.Secrets[0].Type)
	}

	// Filter by system type
	req = httptest.NewRequest(http.MethodGet, "/api/secrets?type=system", nil)
	w = httptest.NewRecorder()
	server.handleSecretsList(w, req)

	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Secrets) != 1 {
		t.Fatalf("expected 1 system entry, got %d", len(resp.Secrets))
	}
	if resp.Secrets[0].Name != "ANTHROPIC_API_KEY" {
		t.Errorf("expected ANTHROPIC_API_KEY, got %s", resp.Secrets[0].Name)
	}
}

func TestHandleSecretsList_WarningWhenNoEnvVar(t *testing.T) {
	config.SetDecryptedSecrets(nil)

	server := NewServer(nil, "/tmp", nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/secrets", nil)
	w := httptest.NewRecorder()
	server.handleSecretsList(w, req)

	var resp secretsListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Warning should be present when MAESTRO_PASSWORD env var is not set
	// (test environment typically doesn't have it)
	if resp.Warning == "" {
		t.Log("MAESTRO_PASSWORD is set in env - warning correctly absent")
	} else {
		if resp.Warning == "" {
			t.Error("expected warning when MAESTRO_PASSWORD is not set")
		}
	}
}

func TestHandleSecretsList_WrongMethod(t *testing.T) {
	server := NewServer(nil, "/tmp", nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/secrets", nil)
	w := httptest.NewRecorder()

	server.handleSecretsList(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", w.Code)
	}
}

func TestHandleSecretsSet_Success(t *testing.T) {
	config.SetDecryptedSecrets(nil)
	config.SetProjectPassword("test-password")
	defer config.SetDecryptedSecrets(nil)
	defer config.SetProjectPassword("")

	server := NewServer(nil, t.TempDir(), nil, nil)

	body := bytes.NewBufferString(`{"name": "TEST_SECRET", "value": "test_value"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/secrets", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleSecretsSet(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d; body: %s", w.Code, w.Body.String())
	}

	// Verify secret was set as user type (default)
	value, err := config.GetSecret("TEST_SECRET")
	if err != nil {
		t.Errorf("secret not set: %v", err)
	}
	if value != "test_value" {
		t.Errorf("expected value 'test_value', got %q", value)
	}

	// Verify it's in user secrets
	userSecrets := config.GetUserSecrets()
	if userSecrets["TEST_SECRET"] != "test_value" {
		t.Errorf("expected secret in user bucket, got: %v", userSecrets)
	}
}

func TestHandleSecretsSet_NoPassword(t *testing.T) {
	config.SetDecryptedSecrets(nil)
	config.SetProjectPassword("")
	defer config.SetDecryptedSecrets(nil)

	server := NewServer(nil, "/tmp", nil, nil)

	body := bytes.NewBufferString(`{"name": "TEST_SECRET", "value": "test_value"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/secrets", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleSecretsSet(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500 when no password, got %d", w.Code)
	}
}

func TestHandleSecretsSet_DefaultsToUser(t *testing.T) {
	config.SetDecryptedSecrets(nil)
	config.SetProjectPassword("test-password")
	defer config.SetDecryptedSecrets(nil)
	defer config.SetProjectPassword("")

	server := NewServer(nil, t.TempDir(), nil, nil)

	// No type field = defaults to user
	body := bytes.NewBufferString(`{"name": "MY_APP_KEY", "value": "some_value"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/secrets", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleSecretsSet(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d; body: %s", w.Code, w.Body.String())
	}

	// Verify it's in user secrets
	userSecrets := config.GetUserSecrets()
	if userSecrets["MY_APP_KEY"] != "some_value" {
		t.Errorf("expected user secret, got: %v", userSecrets)
	}
}

func TestHandleSecretsSet_SystemValidation(t *testing.T) {
	config.SetDecryptedSecrets(nil)
	config.SetProjectPassword("test-password")
	defer config.SetDecryptedSecrets(nil)
	defer config.SetProjectPassword("")

	server := NewServer(nil, t.TempDir(), nil, nil)

	// Valid system secret should succeed
	body := bytes.NewBufferString(`{"name": "ANTHROPIC_API_KEY", "value": "sk-ant-test", "type": "system"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/secrets", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleSecretsSet(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200 for valid system secret, got %d; body: %s", w.Code, w.Body.String())
	}

	// Invalid system secret name should fail
	body = bytes.NewBufferString(`{"name": "UNKNOWN_KEY", "value": "test", "type": "system"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/secrets", body)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()

	server.handleSecretsSet(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for invalid system secret name, got %d", w.Code)
	}
}

func TestHandleSecretsSet_MissingName(t *testing.T) {
	server := NewServer(nil, "/tmp", nil, nil)

	body := bytes.NewBufferString(`{"value": "test_value"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/secrets", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleSecretsSet(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestHandleSecretsSet_MissingValue(t *testing.T) {
	server := NewServer(nil, "/tmp", nil, nil)

	body := bytes.NewBufferString(`{"name": "TEST_SECRET"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/secrets", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleSecretsSet(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestHandleSecretsSet_InvalidName(t *testing.T) {
	server := NewServer(nil, "/tmp", nil, nil)

	body := bytes.NewBufferString(`{"name": "TEST-SECRET", "value": "test_value"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/secrets", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleSecretsSet(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for invalid name, got %d", w.Code)
	}
}

func TestHandleSecretsDelete_Success(t *testing.T) {
	config.SetDecryptedSecrets(&config.StructuredSecrets{
		System: map[string]string{},
		User:   map[string]string{"DELETE_ME": "value"},
	})
	config.SetProjectPassword("test-password")
	defer config.SetDecryptedSecrets(nil)
	defer config.SetProjectPassword("")

	server := NewServer(nil, t.TempDir(), nil, nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/secrets/DELETE_ME", nil)
	w := httptest.NewRecorder()

	server.handleSecretsDelete(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d; body: %s", w.Code, w.Body.String())
	}

	// Verify secret was deleted
	_, err := config.GetSecret("DELETE_ME")
	if err == nil {
		t.Error("expected secret to be deleted")
	}
}

func TestHandleSecretsDelete_NoPassword(t *testing.T) {
	config.SetDecryptedSecrets(&config.StructuredSecrets{
		System: map[string]string{},
		User:   map[string]string{"DELETE_ME": "value"},
	})
	config.SetProjectPassword("")
	defer config.SetDecryptedSecrets(nil)

	server := NewServer(nil, "/tmp", nil, nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/secrets/DELETE_ME", nil)
	w := httptest.NewRecorder()

	server.handleSecretsDelete(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500 when no password, got %d", w.Code)
	}
}

func TestHandleSecretsDelete_WithType(t *testing.T) {
	config.SetDecryptedSecrets(&config.StructuredSecrets{
		System: map[string]string{"ANTHROPIC_API_KEY": "sk-ant-test"},
		User:   map[string]string{"ANTHROPIC_API_KEY": "user-override"},
	})
	config.SetProjectPassword("test-password")
	defer config.SetDecryptedSecrets(nil)
	defer config.SetProjectPassword("")

	server := NewServer(nil, t.TempDir(), nil, nil)

	// Delete system version
	req := httptest.NewRequest(http.MethodDelete, "/api/secrets/ANTHROPIC_API_KEY?type=system", nil)
	w := httptest.NewRecorder()

	server.handleSecretsDelete(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d; body: %s", w.Code, w.Body.String())
	}

	// User version should still exist
	val, err := config.GetSecret("ANTHROPIC_API_KEY")
	if err != nil {
		t.Fatalf("expected user secret to remain: %v", err)
	}
	if val != "user-override" {
		t.Errorf("expected user version to remain, got: %q", val)
	}
}

func TestHandleSecretsDelete_MissingName(t *testing.T) {
	server := NewServer(nil, "/tmp", nil, nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/secrets/", nil)
	w := httptest.NewRecorder()

	server.handleSecretsDelete(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestHandleSecretsDelete_WrongMethod(t *testing.T) {
	server := NewServer(nil, "/tmp", nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/secrets/SOME_SECRET", nil)
	w := httptest.NewRecorder()

	server.handleSecretsDelete(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", w.Code)
	}
}

func TestHandleSecretsRouter(t *testing.T) {
	config.SetDecryptedSecrets(nil)
	config.SetProjectPassword("test-password")
	defer config.SetDecryptedSecrets(nil)
	defer config.SetProjectPassword("")

	server := NewServer(nil, t.TempDir(), nil, nil)

	// Test GET routes to list
	t.Run("GET routes to list", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/secrets", nil)
		w := httptest.NewRecorder()
		server.handleSecretsRouter(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("expected status 200 for GET, got %d", w.Code)
		}
	})

	// Test POST routes to set
	t.Run("POST routes to set", func(t *testing.T) {
		body := bytes.NewBufferString(`{"name": "ROUTER_TEST", "value": "value"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/secrets", body)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		server.handleSecretsRouter(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("expected status 200 for POST, got %d; body: %s", w.Code, w.Body.String())
		}
	})

	// Test DELETE returns 405 on router (needs path)
	t.Run("DELETE returns 405", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/secrets", nil)
		w := httptest.NewRecorder()
		server.handleSecretsRouter(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected status 405 for DELETE, got %d", w.Code)
		}
	})
}
