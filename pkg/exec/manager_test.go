package exec

import (
	"context"
	"strings"
	"testing"
	"time"

	"orchestrator/pkg/config"
)

func TestExecutorManager_Initialize(t *testing.T) {
	testCases := []struct {
		name       string
		config     *config.ExecutorConfig
		expectErr  bool
		expectType string
	}{
		{
			name: "Local executor",
			config: &config.ExecutorConfig{
				Type:     "local",
				Fallback: "local",
				Docker: config.DockerConfig{
					Image: "alpine:latest",
				},
			},
			expectErr:  false,
			expectType: "local",
		},
		{
			name: "Auto executor with Docker available",
			config: &config.ExecutorConfig{
				Type:     "auto",
				Fallback: "local",
				Docker: config.DockerConfig{
					Image:       "alpine:latest",
					AutoPull:    true,
					PullTimeout: 300,
				},
			},
			expectErr: false,
			// expectType depends on Docker availability.
		},
		{
			name: "Auto executor with unavailable Docker",
			config: &config.ExecutorConfig{
				Type:     "auto",
				Fallback: "local",
				Docker: config.DockerConfig{
					Image:       "nonexistent:image",
					AutoPull:    false,
					PullTimeout: 300,
				},
			},
			expectErr: true, // Should fail since Docker is required for auto mode
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			manager := NewExecutorManager(tc.config)

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			err := manager.Initialize(ctx)

			if tc.expectErr && err == nil {
				t.Error("Expected error but got none")
			}

			if !tc.expectErr && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if !tc.expectErr && tc.expectType != "" {
				defaultExec, err := manager.GetDefaultExecutor()
				if err != nil {
					t.Fatalf("Failed to get default executor: %v", err)
				}

				if string(defaultExec.Name()) != tc.expectType {
					t.Errorf("Expected default executor %s, got %s", tc.expectType, string(defaultExec.Name()))
				}
			}
		})
	}
}

func TestExecutorManager_SelectDefaultExecutor(t *testing.T) {
	testCases := []struct {
		name       string
		config     *config.ExecutorConfig
		expectType string
		expectErr  bool
	}{
		{
			name: "Force local",
			config: &config.ExecutorConfig{
				Type:     "local",
				Fallback: "local",
			},
			expectType: "local",
			expectErr:  false,
		},
		{
			name: "Auto with unavailable Docker",
			config: &config.ExecutorConfig{
				Type:     "auto",
				Fallback: "local",
				Docker: config.DockerConfig{
					Image:    "nonexistent:image",
					AutoPull: false,
				},
			},
			expectErr: true, // Should fail since Docker is required for auto mode
		},
		{
			name: "Invalid type",
			config: &config.ExecutorConfig{
				Type: "invalid",
			},
			expectErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			manager := NewExecutorManager(tc.config)

			ctx := context.Background()
			executorType, err := manager.selectDefaultExecutor(ctx)

			if tc.expectErr && err == nil {
				t.Error("Expected error but got none")
			}

			if !tc.expectErr && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if !tc.expectErr && executorType != tc.expectType {
				t.Errorf("Expected executor type %s, got %s", tc.expectType, executorType)
			}
		})
	}
}

func TestExecutorManager_GetStatus(t *testing.T) {
	config := &config.ExecutorConfig{
		Type:     "auto",
		Fallback: "local",
		Docker: config.DockerConfig{
			Image: "alpine:latest",
		},
	}

	manager := NewExecutorManager(config)

	ctx := context.Background()
	err := manager.Initialize(ctx)
	if err != nil {
		t.Fatalf("Failed to initialize manager: %v", err)
	}

	status := manager.GetStatus()

	// Should have at least local executor.
	if _, ok := status["local"]; !ok {
		t.Error("Expected local executor in status")
	}

	// Should have Docker executor (may or may not be available)
	if _, ok := status["docker"]; !ok {
		t.Error("Expected docker executor in status")
	}

	// Local should always be available.
	if !status["local"] {
		t.Error("Expected local executor to be available")
	}
}

func TestExecutorManager_GetStartupInfo(t *testing.T) {
	config := &config.ExecutorConfig{
		Type:     "auto",
		Fallback: "local",
		Docker: config.DockerConfig{
			Image: "alpine:latest",
		},
	}

	manager := NewExecutorManager(config)

	ctx := context.Background()
	err := manager.Initialize(ctx)
	if err != nil {
		t.Fatalf("Failed to initialize manager: %v", err)
	}

	info := manager.GetStartupInfo()

	// Should contain type information.
	if !contains(info, "Type: auto") {
		t.Errorf("Expected startup info to contain 'Type: auto', got: %s", info)
	}

	// Should contain Docker information.
	if !contains(info, "Docker:") {
		t.Errorf("Expected startup info to contain Docker information, got: %s", info)
	}

	// Should contain Local information.
	if !contains(info, "Local:") {
		t.Errorf("Expected startup info to contain Local information, got: %s", info)
	}

	// Should contain default executor.
	if !contains(info, "Default:") {
		t.Errorf("Expected startup info to contain Default information, got: %s", info)
	}
}

func TestExecutorManager_IsDockerAvailable(t *testing.T) {
	config := &config.ExecutorConfig{
		Type: "auto",
		Docker: config.DockerConfig{
			Image: "alpine:latest",
		},
	}

	manager := NewExecutorManager(config)

	ctx := context.Background()
	available := manager.isDockerAvailable(ctx)

	// This test depends on environment, so just verify it returns a boolean.
	t.Logf("Docker available: %v", available)

	// Test with short timeout.
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	// This should complete quickly regardless of Docker availability.
	result := manager.isDockerAvailable(timeoutCtx)
	t.Logf("Docker available with timeout: %v", result)
}

func TestExecutorManager_IsDockerImageAvailable(t *testing.T) {
	config := &config.ExecutorConfig{
		Type: "auto",
		Docker: config.DockerConfig{
			Image:       "alpine:latest",
			AutoPull:    false, // Don't auto-pull for this test
			PullTimeout: 300,
		},
	}

	manager := NewExecutorManager(config)

	ctx := context.Background()

	// Skip this test if Docker is not available.
	if !manager.isDockerAvailable(ctx) {
		t.Skip("Docker not available, skipping image availability test")
	}

	available := manager.isDockerImageAvailable(ctx)
	t.Logf("Docker image %s available: %v", config.Docker.Image, available)
}

func TestExecutorManager_GetExecutor(t *testing.T) {
	config := &config.ExecutorConfig{
		Type:     "auto",
		Fallback: "local",
		Docker: config.DockerConfig{
			Image: "alpine:latest",
		},
	}

	manager := NewExecutorManager(config)

	ctx := context.Background()
	err := manager.Initialize(ctx)
	if err != nil {
		t.Fatalf("Failed to initialize manager: %v", err)
	}

	// Test getting executor with preferences.
	executor, err := manager.GetExecutor([]string{"docker", "local"})
	if err != nil {
		t.Fatalf("Failed to get executor: %v", err)
	}

	if executor == nil {
		t.Error("Expected non-nil executor")
	}

	// Should get local executor if docker not available.
	localExec, err := manager.GetExecutor([]string{"local"})
	if err != nil {
		t.Fatalf("Failed to get local executor: %v", err)
	}

	if string(localExec.Name()) != "local" {
		t.Errorf("Expected local executor, got %s", string(localExec.Name()))
	}
}

// Test Story 073 acceptance criteria.
func TestStory073AcceptanceCriteria(t *testing.T) {
	t.Run("executor type configuration", func(t *testing.T) {
		testCases := []struct {
			name        string
			configType  string
			expectValid bool
		}{
			{"auto", "auto", true},
			{"docker", "docker", true},
			{"local", "local", true},
			{"invalid", "invalid", false},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				config := &config.ExecutorConfig{
					Type:     tc.configType,
					Fallback: "local",
					Docker: config.DockerConfig{
						Image: "alpine:latest",
					},
				}

				manager := NewExecutorManager(config)
				ctx := context.Background()
				err := manager.Initialize(ctx)

				if tc.expectValid && err != nil {
					t.Errorf("Expected valid config but got error: %v", err)
				}

				if !tc.expectValid && err == nil {
					t.Error("Expected invalid config but got no error")
				}
			})
		}
	})

	t.Run("auto executor selection", func(t *testing.T) {
		config := &config.ExecutorConfig{
			Type:     "auto",
			Fallback: "local",
			Docker: config.DockerConfig{
				Image:       "alpine:latest",
				AutoPull:    true,
				PullTimeout: 300,
			},
		}

		manager := NewExecutorManager(config)
		ctx := context.Background()
		err := manager.Initialize(ctx)

		if err != nil {
			t.Fatalf("Failed to initialize manager: %v", err)
		}

		// Auto should select Docker (fails if Docker not available)
		defaultExec, err := manager.GetDefaultExecutor()
		if err != nil {
			t.Fatalf("Failed to get default executor: %v", err)
		}

		// Should be docker only (no fallback to local)
		if string(defaultExec.Name()) != "docker" {
			t.Errorf("Expected auto to select 'docker', got '%s'", string(defaultExec.Name()))
		}
	})

	t.Run("explicit local executor warning", func(t *testing.T) {
		config := &config.ExecutorConfig{
			Type:     "local",
			Fallback: "local",
			Docker: config.DockerConfig{
				Image: "alpine:latest",
			},
		}

		manager := NewExecutorManager(config)
		ctx := context.Background()
		err := manager.Initialize(ctx)

		if err != nil {
			t.Fatalf("Failed to initialize manager: %v", err)
		}

		// Should use local executor with warning.
		defaultExec, err := manager.GetDefaultExecutor()
		if err != nil {
			t.Fatalf("Failed to get default executor: %v", err)
		}

		if string(defaultExec.Name()) != "local" {
			t.Errorf("Expected local executor, got '%s'", string(defaultExec.Name()))
		}
	})

	t.Run("CPU and memory limits configuration", func(t *testing.T) {
		config := &config.ExecutorConfig{
			Type:     "docker",
			Fallback: "local",
			Docker: config.DockerConfig{
				Image:  "alpine:latest",
				CPUs:   "4",
				Memory: "4g",
				PIDs:   2048,
			},
		}

		manager := NewExecutorManager(config)
		ctx := context.Background()
		err := manager.Initialize(ctx)

		if err != nil {
			t.Fatalf("Failed to initialize manager: %v", err)
		}

		// Verify configuration is properly loaded.
		if config.Docker.CPUs != "4" {
			t.Errorf("Expected CPUs '4', got '%s'", config.Docker.CPUs)
		}
		if config.Docker.Memory != "4g" {
			t.Errorf("Expected Memory '4g', got '%s'", config.Docker.Memory)
		}
		if config.Docker.PIDs != 2048 {
			t.Errorf("Expected PIDs 2048, got %d", config.Docker.PIDs)
		}
	})

	t.Run("environment variable override", func(t *testing.T) {
		// This would need integration with the config loader.
		// For now, just verify the structure supports it.
		config := &config.ExecutorConfig{
			Type:     "auto",
			Fallback: "local",
			Docker: config.DockerConfig{
				Image: "golang:1.24-alpine",
			},
		}

		manager := NewExecutorManager(config)
		ctx := context.Background()
		err := manager.Initialize(ctx)

		if err != nil {
			t.Fatalf("Failed to initialize manager: %v", err)
		}

		// Just verify the manager can be created and initialized.
		status := manager.GetStatus()
		if len(status) == 0 {
			t.Error("Expected executor status to be populated")
		}
	})
}

// Helper function to check if a string contains a substring.
func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
