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

	err = UpdateContainer(newContainer)
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

// TestUpdateAgents was removed due to hanging issue with LLM client initialization.
// The UpdateAgents function will be tested through integration tests.

func TestMaintenanceConfigDefaults(t *testing.T) {
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

	// Load config (should create default)
	err = LoadConfig(tempDir)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Get the loaded config
	cfg, err := GetConfig()
	if err != nil {
		t.Fatalf("Failed to get config: %v", err)
	}

	// Test maintenance config exists
	if cfg.Maintenance == nil {
		t.Fatal("Expected maintenance config to be created")
	}

	// Test default values
	if !cfg.Maintenance.Enabled {
		t.Error("Expected maintenance.enabled to be true by default")
	}

	if cfg.Maintenance.AfterSpecs != 1 {
		t.Errorf("Expected maintenance.after_specs to be 1, got %d", cfg.Maintenance.AfterSpecs)
	}

	// Test tasks defaults
	if !cfg.Maintenance.Tasks.BranchCleanup {
		t.Error("Expected tasks.branch_cleanup to be true by default")
	}
	if !cfg.Maintenance.Tasks.KnowledgeSync {
		t.Error("Expected tasks.knowledge_sync to be true by default")
	}
	if !cfg.Maintenance.Tasks.DocsVerification {
		t.Error("Expected tasks.docs_verification to be true by default")
	}
	if !cfg.Maintenance.Tasks.TodoScan {
		t.Error("Expected tasks.todo_scan to be true by default")
	}
	if !cfg.Maintenance.Tasks.DeferredReview {
		t.Error("Expected tasks.deferred_review to be true by default")
	}
	if !cfg.Maintenance.Tasks.TestCoverage {
		t.Error("Expected tasks.test_coverage to be true by default")
	}

	// Test branch cleanup defaults
	if len(cfg.Maintenance.BranchCleanup.ProtectedPatterns) == 0 {
		t.Error("Expected protected_patterns to have default values")
	}
	expectedPatterns := []string{"main", "master", "develop", "release/*", "hotfix/*"}
	if len(cfg.Maintenance.BranchCleanup.ProtectedPatterns) != len(expectedPatterns) {
		t.Errorf("Expected %d protected patterns, got %d",
			len(expectedPatterns), len(cfg.Maintenance.BranchCleanup.ProtectedPatterns))
	}

	// Test TODO scan defaults
	if len(cfg.Maintenance.TodoScan.Markers) == 0 {
		t.Error("Expected markers to have default values")
	}
	expectedMarkers := []string{"TODO", "FIXME", "HACK", "XXX", "deprecated", "DEPRECATED", "@deprecated"}
	if len(cfg.Maintenance.TodoScan.Markers) != len(expectedMarkers) {
		t.Errorf("Expected %d markers, got %d",
			len(expectedMarkers), len(cfg.Maintenance.TodoScan.Markers))
	}
}
