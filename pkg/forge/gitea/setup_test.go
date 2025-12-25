package gitea

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGenerateRandomPassword tests password generation.
func TestGenerateRandomPassword(t *testing.T) {
	// Generate multiple passwords and verify they're unique.
	passwords := make(map[string]bool)
	for i := 0; i < 10; i++ {
		pw, err := generateRandomPassword(32)
		if err != nil {
			t.Fatalf("generateRandomPassword failed: %v", err)
		}
		if len(pw) != 32 {
			t.Errorf("Password length should be 32, got %d", len(pw))
		}
		if passwords[pw] {
			t.Error("Generated duplicate password")
		}
		passwords[pw] = true
	}
}

// TestNewSetupManager tests setup manager creation.
func TestNewSetupManager(t *testing.T) {
	manager := NewSetupManager()
	if manager == nil {
		t.Fatal("NewSetupManager should not return nil")
	}
	if manager.logger == nil {
		t.Error("SetupManager should have a logger")
	}
	if manager.dockerCmd == "" {
		t.Error("SetupManager should have a docker command")
	}
}

// TestIsSetupComplete tests setup completion check.
func TestIsSetupComplete(t *testing.T) {
	manager := NewSetupManager()

	// Test with server that returns 200 (repo exists).
	t.Run("setup complete", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/repos/maestro/myrepo" {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"name": "myrepo"}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		ctx := context.Background()
		if !manager.IsSetupComplete(ctx, server.URL, "test-token", "myrepo") {
			t.Error("IsSetupComplete should return true when repo exists")
		}
	})

	// Test with server that returns 404 (repo doesn't exist).
	t.Run("setup incomplete", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		ctx := context.Background()
		if manager.IsSetupComplete(ctx, server.URL, "test-token", "myrepo") {
			t.Error("IsSetupComplete should return false when repo doesn't exist")
		}
	})

	// Test with unreachable server.
	t.Run("server unreachable", func(t *testing.T) {
		ctx := context.Background()
		if manager.IsSetupComplete(ctx, "http://localhost:99999", "test-token", "myrepo") {
			t.Error("IsSetupComplete should return false for unreachable server")
		}
	})
}

// TestSetupConstants tests that constants are set correctly.
func TestSetupConstants(t *testing.T) {
	if DefaultAdminUser == "" {
		t.Error("DefaultAdminUser should not be empty")
	}
	if DefaultAdminEmail == "" {
		t.Error("DefaultAdminEmail should not be empty")
	}
	if DefaultOrganization == "" {
		t.Error("DefaultOrganization should not be empty")
	}
	if TokenName == "" {
		t.Error("TokenName should not be empty")
	}
	if SetupTimeout <= 0 {
		t.Error("SetupTimeout should be positive")
	}
}

// TestCreateOrganization tests organization creation via API.
func TestCreateOrganization(t *testing.T) {
	manager := NewSetupManager()

	// Test successful creation.
	t.Run("success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/orgs" && r.Method == http.MethodPost {
				// Verify authorization header.
				if r.Header.Get("Authorization") != "token test-token" {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"username": "maestro"}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		ctx := context.Background()
		err := manager.createOrganization(ctx, server.URL, "test-token")
		if err != nil {
			t.Errorf("createOrganization should succeed: %v", err)
		}
	})

	// Test already exists (409).
	t.Run("already exists", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/orgs" {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"message": "organization already exists"}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		ctx := context.Background()
		err := manager.createOrganization(ctx, server.URL, "test-token")
		// Should not error when org already exists.
		if err != nil {
			t.Errorf("createOrganization should not error when org exists: %v", err)
		}
	})
}

// TestCreateRepository tests repository creation via API.
func TestCreateRepository(t *testing.T) {
	manager := NewSetupManager()

	// Test successful creation.
	t.Run("success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/orgs/maestro/repos" && r.Method == http.MethodPost {
				// Verify request body.
				var body map[string]interface{}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if body["name"] != "myrepo" {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"name": "myrepo"}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		ctx := context.Background()
		err := manager.createRepository(ctx, server.URL, "test-token", "myrepo")
		if err != nil {
			t.Errorf("createRepository should succeed: %v", err)
		}
	})

	// Test already exists (422).
	t.Run("already exists", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/orgs/maestro/repos" {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = w.Write([]byte(`{"message": "repository already exists"}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		ctx := context.Background()
		err := manager.createRepository(ctx, server.URL, "test-token", "myrepo")
		// Should not error when repo already exists.
		if err != nil {
			t.Errorf("createRepository should not error when repo exists: %v", err)
		}
	})
}

// TestDeleteToken tests token deletion via API.
func TestDeleteToken(t *testing.T) {
	manager := NewSetupManager()

	// Test successful deletion.
	t.Run("success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			expectedPath := "/api/v1/users/maestro-admin/tokens/" + TokenName
			if r.URL.Path == expectedPath && r.Method == http.MethodDelete {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		ctx := context.Background()
		err := manager.deleteToken(ctx, server.URL, DefaultAdminUser, "password")
		if err != nil {
			t.Errorf("deleteToken should succeed: %v", err)
		}
	})

	// Test token not found (404) - should not error.
	t.Run("not found", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		ctx := context.Background()
		err := manager.deleteToken(ctx, server.URL, DefaultAdminUser, "password")
		if err != nil {
			t.Errorf("deleteToken should not error for 404: %v", err)
		}
	})
}

// TestGenerateToken tests token generation via API.
func TestGenerateToken(t *testing.T) {
	manager := NewSetupManager()

	// Test successful token generation.
	t.Run("success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/users/maestro-admin/tokens" && r.Method == http.MethodPost {
				// Verify basic auth.
				user, pass, ok := r.BasicAuth()
				if !ok || user != DefaultAdminUser || pass != "testpass" {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"sha1": "abc123token456"}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		ctx := context.Background()
		token, err := manager.generateToken(ctx, server.URL, DefaultAdminUser, "testpass")
		if err != nil {
			t.Fatalf("generateToken should succeed: %v", err)
		}
		if token != "abc123token456" {
			t.Errorf("Expected token 'abc123token456', got %q", token)
		}
	})
}

// TestSetupResult tests the setup result struct.
func TestSetupResult(t *testing.T) {
	result := &SetupResult{
		Token:    "test-token",
		URL:      "http://localhost:3000",
		Owner:    "maestro",
		RepoName: "myrepo",
		CloneURL: "http://localhost:3000/maestro/myrepo.git",
	}

	if result.Token == "" {
		t.Error("Token should not be empty")
	}
	if result.URL == "" {
		t.Error("URL should not be empty")
	}
	if result.Owner == "" {
		t.Error("Owner should not be empty")
	}
	if result.RepoName == "" {
		t.Error("RepoName should not be empty")
	}
	if result.CloneURL == "" {
		t.Error("CloneURL should not be empty")
	}
}
