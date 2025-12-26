package gitea

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestSanitizeName tests the name sanitization function.
func TestSanitizeName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"myproject", "myproject"},
		{"my-project", "my-project"},
		{"my_project", "my_project"},
		{"my.project", "my.project"},
		{"MyProject", "myproject"},
		{"my project", "my-project"},
		{"my/project", "my-project"},
		{"123project", "123project"},
		{"-project", "p-project"}, // Must start with alphanumeric
		{"_project", "p_project"}, // Must start with alphanumeric
		{".project", "p.project"}, // Must start with alphanumeric
		{"", "project"},           // Empty becomes "project"
		{"a!b@c#d", "a-b-c-d"},    // Special chars become hyphens
		{"PROJECT_NAME", "project_name"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeName(tt.input)
			if got != tt.expected {
				t.Errorf("sanitizeName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

// TestContainerName tests container name generation.
func TestContainerName(t *testing.T) {
	tests := []struct {
		projectName string
		expected    string
	}{
		{"myproject", "maestro-gitea-myproject"},
		{"my-app", "maestro-gitea-my-app"},
		{"My Project", "maestro-gitea-my-project"},
	}

	for _, tt := range tests {
		t.Run(tt.projectName, func(t *testing.T) {
			got := ContainerName(tt.projectName)
			if got != tt.expected {
				t.Errorf("ContainerName(%q) = %q, want %q", tt.projectName, got, tt.expected)
			}
		})
	}
}

// TestVolumeName tests volume name generation.
func TestVolumeName(t *testing.T) {
	tests := []struct {
		projectName string
		expected    string
	}{
		{"myproject", "maestro-gitea-myproject-data"},
		{"my-app", "maestro-gitea-my-app-data"},
		{"My Project", "maestro-gitea-my-project-data"},
	}

	for _, tt := range tests {
		t.Run(tt.projectName, func(t *testing.T) {
			got := VolumeName(tt.projectName)
			if got != tt.expected {
				t.Errorf("VolumeName(%q) = %q, want %q", tt.projectName, got, tt.expected)
			}
		})
	}
}

// TestIsHealthy tests the health check function.
func TestIsHealthy(t *testing.T) {
	// Test with healthy server.
	t.Run("healthy server", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/version" {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"version": "1.25"}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		ctx := context.Background()
		if !IsHealthy(ctx, server.URL) {
			t.Error("IsHealthy should return true for healthy server")
		}
	})

	// Test with unhealthy server (500 error).
	t.Run("unhealthy server", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		ctx := context.Background()
		if IsHealthy(ctx, server.URL) {
			t.Error("IsHealthy should return false for unhealthy server")
		}
	})

	// Test with unreachable server.
	t.Run("unreachable server", func(t *testing.T) {
		ctx := context.Background()
		if IsHealthy(ctx, "http://localhost:99999") {
			t.Error("IsHealthy should return false for unreachable server")
		}
	})
}

// TestWaitForReady tests the wait for ready function.
func TestWaitForReady(t *testing.T) {
	// Test with immediately healthy server.
	t.Run("immediately healthy", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/version" {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		ctx := context.Background()
		err := WaitForReady(ctx, server.URL, 5*time.Second)
		if err != nil {
			t.Errorf("WaitForReady should succeed for healthy server: %v", err)
		}
	})

	// Test with delayed healthy server.
	t.Run("delayed healthy", func(t *testing.T) {
		startTime := time.Now()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Return unhealthy for first 500ms.
			if time.Since(startTime) < 500*time.Millisecond {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			if r.URL.Path == "/api/v1/version" {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		ctx := context.Background()
		err := WaitForReady(ctx, server.URL, 5*time.Second)
		if err != nil {
			t.Errorf("WaitForReady should succeed after delay: %v", err)
		}
	})

	// Test timeout.
	t.Run("timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer server.Close()

		ctx := context.Background()
		err := WaitForReady(ctx, server.URL, 500*time.Millisecond)
		if err == nil {
			t.Error("WaitForReady should timeout for unhealthy server")
		}
	})

	// Test context cancellation.
	t.Run("context cancelled", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer server.Close()

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately.

		err := WaitForReady(ctx, server.URL, 5*time.Second)
		if err == nil {
			t.Error("WaitForReady should fail when context is cancelled")
		}
	})
}

// TestGetContainerURL tests URL generation.
func TestGetContainerURL(t *testing.T) {
	tests := []struct {
		port     int
		expected string
	}{
		{3000, "http://localhost:3000"},
		{8080, "http://localhost:8080"},
		{3001, "http://localhost:3001"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			got := GetContainerURL(tt.port)
			if got != tt.expected {
				t.Errorf("GetContainerURL(%d) = %q, want %q", tt.port, got, tt.expected)
			}
		})
	}
}

// TestNewContainerManager tests manager creation.
func TestNewContainerManager(t *testing.T) {
	manager := NewContainerManager()
	if manager == nil {
		t.Fatal("NewContainerManager should not return nil")
	}
	if manager.logger == nil {
		t.Error("ContainerManager should have a logger")
	}
	if manager.dockerCmd == "" {
		t.Error("ContainerManager should have a docker command")
	}
}

// TestContainerConfig_Defaults tests default port values.
func TestContainerConfig_Defaults(t *testing.T) {
	// Verify that when ports are 0, EnsureContainer uses defaults.
	// This is a unit test - we don't actually start containers.
	cfg := ContainerConfig{
		ProjectName: "test-project",
		HTTPPort:    0,
		SSHPort:     0,
	}

	if cfg.HTTPPort != 0 {
		t.Errorf("Default HTTPPort should be 0 in config, got %d", cfg.HTTPPort)
	}

	// The actual default application happens in EnsureContainer.
	// We just verify the constants are as expected.
	if DefaultHTTPPort != 3000 {
		t.Errorf("DefaultHTTPPort should be 3000, got %d", DefaultHTTPPort)
	}
	if DefaultSSHPort != 2222 {
		t.Errorf("DefaultSSHPort should be 2222, got %d", DefaultSSHPort)
	}
}

// TestGiteaImage tests the pinned image version.
func TestGiteaImage(t *testing.T) {
	// Verify the image is pinned to a specific version (not latest).
	expected := "gitea/gitea:1.25"
	if GiteaImage != expected {
		t.Errorf("GiteaImage should be %q, got %q", expected, GiteaImage)
	}

	// Verify it's not using 'latest' tag.
	if GiteaImage == "gitea/gitea:latest" || GiteaImage == "gitea/gitea" {
		t.Error("GiteaImage should not use 'latest' tag for reproducibility")
	}
}
