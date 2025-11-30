package webui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"orchestrator/pkg/config"
)

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

	var entries []SecretEntry
	if err := json.NewDecoder(w.Body).Decode(&entries); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestHandleSecretsList_WithSecrets(t *testing.T) {
	// Set up test secrets
	config.SetDecryptedSecrets(map[string]string{
		"DB_PASSWORD": "secret1",
		"API_KEY":     "secret2",
	})
	defer config.SetDecryptedSecrets(nil)

	server := NewServer(nil, "/tmp", nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/secrets", nil)
	w := httptest.NewRecorder()

	server.handleSecretsList(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var entries []SecretEntry
	if err := json.NewDecoder(w.Body).Decode(&entries); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(entries))
	}

	// Should be sorted alphabetically
	if entries[0].Name != "API_KEY" {
		t.Errorf("expected first entry to be API_KEY, got %s", entries[0].Name)
	}
	if entries[1].Name != "DB_PASSWORD" {
		t.Errorf("expected second entry to be DB_PASSWORD, got %s", entries[1].Name)
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
	defer config.SetDecryptedSecrets(nil)

	server := NewServer(nil, "/tmp", nil, nil)

	body := bytes.NewBufferString(`{"name": "TEST_SECRET", "value": "test_value"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/secrets", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleSecretsSet(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	// Verify secret was set
	value, err := config.GetSecret("TEST_SECRET")
	if err != nil {
		t.Errorf("secret not set: %v", err)
	}
	if value != "test_value" {
		t.Errorf("expected value 'test_value', got %q", value)
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
	config.SetDecryptedSecrets(map[string]string{
		"DELETE_ME": "value",
	})
	defer config.SetDecryptedSecrets(nil)

	server := NewServer(nil, "/tmp", nil, nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/secrets/DELETE_ME", nil)
	w := httptest.NewRecorder()

	server.handleSecretsDelete(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	// Verify secret was deleted
	_, err := config.GetSecret("DELETE_ME")
	if err == nil {
		t.Error("expected secret to be deleted")
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

func TestSanitizeSecretName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"TEST_SECRET", "TEST_SECRET"},
		{"test_secret", "test_secret"},
		{"TestSecret123", "TestSecret123"},
		{"TEST-SECRET", "TESTSECRET"},
		{"TEST.SECRET", "TESTSECRET"},
		{"TEST SECRET", "TESTSECRET"},
		{"", ""},
	}

	for _, tt := range tests {
		result := sanitizeSecretName(tt.input)
		if result != tt.expected {
			t.Errorf("sanitizeSecretName(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestHandleSecretsRouter(t *testing.T) {
	config.SetDecryptedSecrets(nil)
	defer config.SetDecryptedSecrets(nil)

	server := NewServer(nil, "/tmp", nil, nil)

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
			t.Errorf("expected status 200 for POST, got %d", w.Code)
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
