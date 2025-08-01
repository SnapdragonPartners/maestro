package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	// Create temporary directory for test
	tempDir, err := os.MkdirTemp("", "config-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create .maestro directory
	maestroDir := filepath.Join(tempDir, ProjectConfigDir)
	if mkdirErr := os.MkdirAll(maestroDir, 0755); mkdirErr != nil {
		t.Fatalf("Failed to create .maestro dir: %v", mkdirErr)
	}

	// Test loading config (should create default)
	err = LoadConfig(tempDir)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Get the loaded config
	config, err := GetConfig()
	if err != nil {
		t.Fatalf("Failed to get config: %v", err)
	}

	// Test default values
	if len(config.Orchestrator.Models) == 0 {
		t.Error("Expected default models to be created")
	}

	if config.Agents == nil {
		t.Error("Expected agents config to be created")
	}

	if config.Container == nil {
		t.Error("Expected container config to be created")
	}
}

func TestUpdateContainer(t *testing.T) {
	// Create temporary directory for test
	tempDir, err := os.MkdirTemp("", "config-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create .maestro directory
	maestroDir := filepath.Join(tempDir, ProjectConfigDir)
	if mkdirErr := os.MkdirAll(maestroDir, 0755); mkdirErr != nil {
		t.Fatalf("Failed to create .maestro dir: %v", mkdirErr)
	}

	// Load initial config
	err = LoadConfig(tempDir)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Update container config
	newContainer := &ContainerConfig{
		Name:       "test-container",
		Dockerfile: "", // Using standard image, no custom dockerfile
		// Environment variables now go in dockerfile, not config
		// Docker runtime settings
		Network:   DefaultDockerNetwork,
		TmpfsSize: DefaultTmpfsSize,
		CPUs:      DefaultDockerCPUs,
		Memory:    DefaultDockerMemory,
		PIDs:      DefaultDockerPIDs,
	}

	err = UpdateContainer(tempDir, newContainer)
	if err != nil {
		t.Fatalf("Failed to update container config: %v", err)
	}

	// Verify update
	config, err := GetConfig()
	if err != nil {
		t.Fatalf("Failed to get config: %v", err)
	}
	if config.Container.Name != "test-container" {
		t.Errorf("Expected name 'test-container', got '%s'", config.Container.Name)
	}
}

func TestUpdateAgents(t *testing.T) {
	// Create temporary directory for test
	tempDir, err := os.MkdirTemp("", "config-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create .maestro directory
	maestroDir := filepath.Join(tempDir, ProjectConfigDir)
	if mkdirErr := os.MkdirAll(maestroDir, 0755); mkdirErr != nil {
		t.Fatalf("Failed to create .maestro dir: %v", mkdirErr)
	}

	// Load initial config
	err = LoadConfig(tempDir)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Update agents config
	newAgents := &AgentConfig{
		MaxCoders:      4,
		CoderModel:     "claude-3-5-sonnet-20241022",
		ArchitectModel: "o3-mini",
	}

	err = UpdateAgents(tempDir, newAgents)
	if err != nil {
		t.Fatalf("Failed to update agents config: %v", err)
	}

	// Verify update
	config, err := GetConfig()
	if err != nil {
		t.Fatalf("Failed to get config: %v", err)
	}
	if config.Agents.MaxCoders != 4 {
		t.Errorf("Expected MaxCoders 4, got %d", config.Agents.MaxCoders)
	}
	if config.Agents.CoderModel != "claude-3-5-sonnet-20241022" {
		t.Errorf("Expected CoderModel 'claude-3-5-sonnet-20241022', got '%s'", config.Agents.CoderModel)
	}
}
