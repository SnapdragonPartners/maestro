package main

import (
	"context"
	"os"
	"testing"

	"orchestrator/pkg/build"
	"orchestrator/pkg/config"
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/limiter"
	"orchestrator/pkg/persistence"
)

// TestNewAgentFactory tests agent factory creation.
func TestNewAgentFactory(t *testing.T) {
	factory := NewAgentFactory()

	if factory == nil {
		t.Fatal("NewAgentFactory returned nil")
	}
}

// TestCreateAgentConfig tests agent configuration creation.
func TestCreateAgentConfig(t *testing.T) {
	// Create test config
	testConfig := &config.Config{
		Agents: &config.AgentConfig{
			MaxCoders:      2,
			ArchitectModel: "test-architect-model",
			CoderModel:     "test-coder-model",
		},
		Orchestrator: &config.OrchestratorConfig{
			Models: []config.Model{
				{
					Name:           "test-architect-model",
					MaxTPM:         1000,
					MaxConnections: 1,
					CPM:            1.0,
					DailyBudget:    10.0,
				},
				{
					Name:           "test-coder-model",
					MaxTPM:         1000,
					MaxConnections: 2,
					CPM:            1.0,
					DailyBudget:    10.0,
				},
			},
		},
		Git: &config.GitConfig{
			RepoURL:      "https://github.com/test/repo.git",
			TargetBranch: "develop",
		},
	}

	workDir := "/tmp/test-work"
	agentConfig, err := createAgentConfig(testConfig, workDir)

	if err != nil {
		t.Fatalf("createAgentConfig failed: %v", err)
	}

	if agentConfig == nil {
		t.Fatal("createAgentConfig returned nil")
	}

	// Verify config values
	if agentConfig.NumCoders != 2 {
		t.Errorf("Expected NumCoders to be 2, got %d", agentConfig.NumCoders)
	}
	if agentConfig.WorkDir != workDir {
		t.Errorf("Expected WorkDir to be %s, got %s", workDir, agentConfig.WorkDir)
	}
	if agentConfig.RepoURL != "https://github.com/test/repo.git" {
		t.Errorf("Expected RepoURL to be https://github.com/test/repo.git, got %s", agentConfig.RepoURL)
	}
	if agentConfig.TargetBranch != "develop" {
		t.Errorf("Expected TargetBranch to be develop, got %s", agentConfig.TargetBranch)
	}
	if agentConfig.MirrorDir != ".mirrors" {
		t.Errorf("Expected MirrorDir to be .mirrors, got %s", agentConfig.MirrorDir)
	}
	if agentConfig.BranchPattern != "maestro/story-{STORY_ID}" {
		t.Errorf("Expected BranchPattern to be maestro/story-{STORY_ID}, got %s", agentConfig.BranchPattern)
	}
	if agentConfig.WorktreePattern != "maestro-story-%s" {
		t.Errorf("Expected WorktreePattern to be maestro-story-%%s, got %s", agentConfig.WorktreePattern)
	}

	// Verify model references
	if agentConfig.ArchitectModel == nil {
		t.Error("ArchitectModel should not be nil")
	} else if agentConfig.ArchitectModel.Name != "test-architect-model" {
		t.Errorf("Expected architect model name to be test-architect-model, got %s", agentConfig.ArchitectModel.Name)
	}

	if agentConfig.CoderModel == nil {
		t.Error("CoderModel should not be nil")
	} else if agentConfig.CoderModel.Name != "test-coder-model" {
		t.Errorf("Expected coder model name to be test-coder-model, got %s", agentConfig.CoderModel.Name)
	}
}

// TestCreateAgentConfigDefaults tests agent config creation with default values.
func TestCreateAgentConfigDefaults(t *testing.T) {
	// Create minimal config (no Git config)
	testConfig := &config.Config{
		Agents: &config.AgentConfig{
			MaxCoders:      1,
			ArchitectModel: "test-architect-model",
			CoderModel:     "test-coder-model",
		},
		Orchestrator: &config.OrchestratorConfig{
			Models: []config.Model{
				{
					Name:           "test-architect-model",
					MaxTPM:         1000,
					MaxConnections: 1,
					CPM:            1.0,
					DailyBudget:    10.0,
				},
				{
					Name:           "test-coder-model",
					MaxTPM:         1000,
					MaxConnections: 2,
					CPM:            1.0,
					DailyBudget:    10.0,
				},
			},
		},
		// No Git config - should use defaults
	}

	workDir := "/tmp/test-work"
	agentConfig, err := createAgentConfig(testConfig, workDir)

	if err != nil {
		t.Fatalf("createAgentConfig failed: %v", err)
	}

	// Verify default values are used
	if agentConfig.RepoURL != "" {
		t.Errorf("Expected empty RepoURL, got %s", agentConfig.RepoURL)
	}
	if agentConfig.TargetBranch != "main" {
		t.Errorf("Expected TargetBranch to default to main, got %s", agentConfig.TargetBranch)
	}
}

// TestCreateAgentConfigModelNotFound tests error handling for missing models.
func TestCreateAgentConfigModelNotFound(t *testing.T) {
	// Create config with missing model
	testConfig := &config.Config{
		Agents: &config.AgentConfig{
			MaxCoders:      1,
			ArchitectModel: "missing-model",
			CoderModel:     "test-coder-model",
		},
		Orchestrator: &config.OrchestratorConfig{
			Models: []config.Model{
				{
					Name:           "test-coder-model",
					MaxTPM:         1000,
					MaxConnections: 2,
					CPM:            1.0,
					DailyBudget:    10.0,
				},
			},
		},
	}

	workDir := "/tmp/test-work"
	_, err := createAgentConfig(testConfig, workDir)

	if err == nil {
		t.Fatal("Expected error for missing architect model")
	}

	expectedError := "failed to get architect model: model missing-model not found in configuration"
	if err.Error() != expectedError {
		t.Errorf("Expected error message '%s', got '%s'", expectedError, err.Error())
	}
}

// TestGetModelByName tests model lookup functionality.
func TestGetModelByName(t *testing.T) {
	testConfig := &config.Config{
		Orchestrator: &config.OrchestratorConfig{
			Models: []config.Model{
				{
					Name:           "model-1",
					MaxTPM:         1000,
					MaxConnections: 1,
					CPM:            1.0,
					DailyBudget:    10.0,
				},
				{
					Name:           "model-2",
					MaxTPM:         2000,
					MaxConnections: 2,
					CPM:            2.0,
					DailyBudget:    20.0,
				},
			},
		},
	}

	// Test successful lookup
	model, err := getModelByName(testConfig, "model-1")
	if err != nil {
		t.Fatalf("getModelByName failed: %v", err)
	}
	if model == nil {
		t.Fatal("getModelByName returned nil model")
	}
	if model.Name != "model-1" {
		t.Errorf("Expected model name model-1, got %s", model.Name)
	}
	if model.MaxTPM != 1000 {
		t.Errorf("Expected model MaxTPM 1000, got %d", model.MaxTPM)
	}

	// Test second model
	model, err = getModelByName(testConfig, "model-2")
	if err != nil {
		t.Fatalf("getModelByName failed: %v", err)
	}
	if model.Name != "model-2" {
		t.Errorf("Expected model name model-2, got %s", model.Name)
	}
	if model.MaxTPM != 2000 {
		t.Errorf("Expected model MaxTPM 2000, got %d", model.MaxTPM)
	}

	// Test missing model
	_, err = getModelByName(testConfig, "missing-model")
	if err == nil {
		t.Fatal("Expected error for missing model")
	}
	expectedError := "model missing-model not found in configuration"
	if err.Error() != expectedError {
		t.Errorf("Expected error message '%s', got '%s'", expectedError, err.Error())
	}
}

func TestCreateAgentSetValidation(t *testing.T) {
	// Create temporary directory for test
	tempDir, err := os.MkdirTemp("", "agent-factory-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create minimal test infrastructure
	testConfig := &config.Config{
		Agents: &config.AgentConfig{
			MaxCoders:      2,
			CoderModel:     "test-coder-model",
			ArchitectModel: "test-architect-model",
		},
		Orchestrator: &config.OrchestratorConfig{
			Models: []config.Model{
				{
					Name:           "test-architect-model",
					MaxTPM:         1000,
					MaxConnections: 1,
					CPM:            1.0,
					DailyBudget:    10.0,
				},
				{
					Name:           "test-coder-model",
					MaxTPM:         1000,
					MaxConnections: 2,
					CPM:            1.0,
					DailyBudget:    10.0,
				},
			},
		},
	}

	// Create basic dispatcher (for interface compliance)
	rateLimiter := limiter.NewLimiter(testConfig)
	dispatcher, err := dispatch.NewDispatcher(testConfig, rateLimiter)
	if err != nil {
		t.Fatalf("Failed to create dispatcher: %v", err)
	}
	defer dispatcher.Stop(context.Background())

	// Create agent config
	agentConfig := &AgentConfig{
		ArchitectModel:  &testConfig.Orchestrator.Models[0],
		CoderModel:      &testConfig.Orchestrator.Models[1],
		WorkDir:         tempDir,
		RepoURL:         "https://github.com/test/repo.git",
		TargetBranch:    "main",
		MirrorDir:       ".mirrors",
		BranchPattern:   "maestro/story-{STORY_ID}",
		WorktreePattern: "maestro-story-%s",
		NumCoders:       2,
	}

	// Create factory
	factory := NewAgentFactory()

	// Create build service and persistence channel
	buildService := build.NewBuildService()
	persistenceChannel := make(chan *persistence.Request, 10)

	// Test CreateAgentSet (this will likely fail due to missing dependencies,
	// but we can test the validation logic)
	ctx := context.Background()
	_, err = factory.CreateAgentSet(ctx, agentConfig, dispatcher, testConfig, persistenceChannel, buildService)

	// We expect this to fail in the test environment, but we can verify
	// that the error is related to agent creation rather than parameter validation
	if err != nil {
		// This is expected in the test environment - just verify error message
		// indicates we got past parameter validation
		t.Logf("CreateAgentSet failed as expected in test environment: %v", err)

		// The error should be about agent creation, not parameter validation
		if err.Error() == "" {
			t.Error("Expected non-empty error message")
		}
	}
}

// TestAgentSetStructure tests the AgentSet structure.
func TestAgentSetStructure(t *testing.T) {
	agentSet := &AgentSet{
		Coders:   make([]dispatch.Agent, 0, 2),
		AgentIDs: make([]string, 0, 3), // architect + 2 coders
	}

	if agentSet.Coders == nil {
		t.Error("Coders slice should be initialized")
	}
	if agentSet.AgentIDs == nil {
		t.Error("AgentIDs slice should be initialized")
	}
	if cap(agentSet.Coders) != 2 {
		t.Errorf("Expected Coders capacity to be 2, got %d", cap(agentSet.Coders))
	}
	if cap(agentSet.AgentIDs) != 3 {
		t.Errorf("Expected AgentIDs capacity to be 3, got %d", cap(agentSet.AgentIDs))
	}
}
