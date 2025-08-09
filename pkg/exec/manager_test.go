package exec

import (
	"context"
	"testing"
	"time"
)

func TestExecutorManager_Initialize(t *testing.T) {
	// Simplified test since manager is deprecated and only supports Docker now
	manager := NewExecutorManager(nil) // Config is ignored

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err := manager.Initialize(ctx)
	if err != nil {
		t.Errorf("Unexpected error during initialization: %v", err)
	}

	// Check that both local and docker executors are registered
	if manager.registry == nil {
		t.Error("Expected registry to be initialized")
	}

	names := manager.registry.List()
	if len(names) == 0 {
		t.Error("Expected at least one executor to be registered")
	}

	// Should have local and docker executors
	hasLocal, hasDocker := false, false
	for _, name := range names {
		if name == "local" {
			hasLocal = true
		}
		if name == "docker" {
			hasDocker = true
		}
	}

	if !hasLocal {
		t.Error("Expected local executor to be registered")
	}
	if !hasDocker {
		t.Error("Expected docker executor to be registered")
	}
}

// TestExecutorManager_SelectDefaultExecutor removed since selectDefaultExecutor method doesn't exist
// in the simplified manager implementation

func TestExecutorManager_GetStatus(t *testing.T) {
	manager := NewExecutorManager(nil) // Config is ignored

	ctx := context.Background()
	err := manager.Initialize(ctx)
	if err != nil {
		t.Fatalf("Failed to initialize manager: %v", err)
	}

	// Test that we can get executors from the registry
	names := manager.registry.List()
	if len(names) == 0 {
		t.Error("Expected at least one executor to be registered")
	}

	// Should be able to get available executors
	available := manager.registry.GetAvailable()
	if len(available) == 0 {
		t.Error("Expected at least one available executor")
	}

	// Should be able to get default executor
	defaultExec, err := manager.registry.GetDefault()
	if err != nil {
		t.Errorf("Failed to get default executor: %v", err)
	}
	if defaultExec == nil {
		t.Error("Expected non-nil default executor")
	}
}

// TestExecutorManager_GetStartupInfo removed since GetStartupInfo method doesn't exist
// in the simplified manager implementation

func TestExecutorManager_IsDockerAvailable(t *testing.T) {
	manager := NewExecutorManager(nil) // Config is ignored

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
	manager := NewExecutorManager(nil) // Config is ignored

	ctx := context.Background()

	// Skip this test if Docker is not available.
	if !manager.isDockerAvailable(ctx) {
		t.Skip("Docker not available, skipping image availability test")
	}

	available := manager.isDockerImageAvailable(ctx)
	t.Logf("Docker image available: %v", available)
}

func TestExecutorManager_GetExecutor(t *testing.T) {
	manager := NewExecutorManager(nil) // Config is ignored

	ctx := context.Background()
	err := manager.Initialize(ctx)
	if err != nil {
		t.Fatalf("Failed to initialize manager: %v", err)
	}

	// Test getting executor with preferences using registry.
	executor, err := manager.registry.GetBest([]string{"docker", "local"})
	if err != nil {
		t.Fatalf("Failed to get executor: %v", err)
	}

	if executor == nil {
		t.Error("Expected non-nil executor")
	}

	// Should get local executor.
	localExec, err := manager.registry.Get("local")
	if err != nil {
		t.Fatalf("Failed to get local executor: %v", err)
	}

	if string(localExec.Name()) != "local" {
		t.Errorf("Expected local executor, got %s", string(localExec.Name()))
	}
}

// Test Story 073 acceptance criteria - simplified for the deprecated manager.
func TestStory073AcceptanceCriteria(t *testing.T) {
	t.Run("executor manager initialization", func(t *testing.T) {
		manager := NewExecutorManager(nil) // Config is ignored in simplified version
		ctx := context.Background()
		err := manager.Initialize(ctx)

		if err != nil {
			t.Errorf("Expected initialization to succeed, got error: %v", err)
		}

		// Should have both local and docker executors registered
		names := manager.registry.List()
		hasLocal, hasDocker := false, false
		for _, name := range names {
			if name == "local" {
				hasLocal = true
			}
			if name == "docker" {
				hasDocker = true
			}
		}

		if !hasLocal {
			t.Error("Expected local executor to be registered")
		}
		if !hasDocker {
			t.Error("Expected docker executor to be registered")
		}
	})

	t.Run("default executor selection", func(t *testing.T) {
		manager := NewExecutorManager(nil)
		ctx := context.Background()
		err := manager.Initialize(ctx)

		if err != nil {
			t.Fatalf("Failed to initialize manager: %v", err)
		}

		// Should default to docker executor
		defaultExec, err := manager.registry.GetDefault()
		if err != nil {
			t.Fatalf("Failed to get default executor: %v", err)
		}

		// Should be docker by default in simplified manager
		if string(defaultExec.Name()) != "docker" {
			t.Errorf("Expected default to be 'docker', got '%s'", string(defaultExec.Name()))
		}
	})

	t.Run("executor availability", func(t *testing.T) {
		manager := NewExecutorManager(nil)
		ctx := context.Background()
		err := manager.Initialize(ctx)

		if err != nil {
			t.Fatalf("Failed to initialize manager: %v", err)
		}

		// Should have at least one available executor
		available := manager.registry.GetAvailable()
		if len(available) == 0 {
			t.Error("Expected at least one available executor")
		}
	})
}

// Helper function to check if a string contains a substring.
// func contains(s, substr string) bool {
//	return strings.Contains(s, substr)
// }
