package architect

import (
	"context"
	"testing"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/state"
)

// Mock implementations are defined in escalation_timeout_test.go

func TestArchitectDriverBasics(t *testing.T) {
	// Create a driver with all required parameters
	stateStore, _ := state.NewStore("test_data")
	mockConfig := &config.ModelCfg{}
	mockLLM := &mockLLMClient{}
	mockOrchestratorConfig := &config.Config{}
	driver := NewDriver("test-architect", stateStore, mockConfig, mockLLM, nil, "test_work", "test_stories", mockOrchestratorConfig)

	// Initialize the driver
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := driver.Initialize(ctx)
	if err != nil {
		t.Fatalf("Failed to initialize driver: %v", err)
	}
	defer driver.Shutdown(ctx)

	t.Run("Driver initialized successfully", func(t *testing.T) {
		// Test that driver initializes without error
		if driver == nil {
			t.Error("Expected driver to be non-nil")
		}
	})

	t.Run("Driver has valid ID", func(t *testing.T) {
		// Test that driver has the expected ID
		expectedID := "test-architect"
		if driver.architectID != expectedID {
			t.Errorf("Expected architect ID %s, got %s", expectedID, driver.architectID)
		}
	})
}

func TestArchitectDriverCreation(t *testing.T) {
	// Test creating multiple drivers
	stateStore, _ := state.NewStore("test_data")
	mockConfig := &config.ModelCfg{}
	mockLLM := &mockLLMClient{}
	mockOrchestratorConfig := &config.Config{}

	driver1 := NewDriver("test-architect-1", stateStore, mockConfig, mockLLM, nil, "test_work_1", "test_stories_1", mockOrchestratorConfig)
	driver2 := NewDriver("test-architect-2", stateStore, mockConfig, mockLLM, nil, "test_work_2", "test_stories_2", mockOrchestratorConfig)

	if driver1.architectID == driver2.architectID {
		t.Error("Expected different architect IDs for different drivers")
	}
}
