package config

import (
	"encoding/json"
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

func TestApplyConfigFile_DeepMergesAndPersists(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "config-apply-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)
	defer SetConfigForTesting(nil)

	maestroDir := filepath.Join(tempDir, ProjectConfigDir)
	if mkdirErr := os.MkdirAll(maestroDir, 0755); mkdirErr != nil {
		t.Fatalf("Failed to create .maestro dir: %v", mkdirErr)
	}

	if loadErr := LoadConfig(tempDir); loadErr != nil {
		t.Fatalf("Failed to load config: %v", loadErr)
	}

	overridePath := filepath.Join(tempDir, "benchmark-config.json")
	override := map[string]any{
		"project": map[string]any{
			"primary_platform": "python",
			"pack_name":        "python",
		},
		"webui": map[string]any{
			"enabled": false,
		},
		"maintenance": map[string]any{
			"enabled": false,
		},
		"container": map[string]any{
			"name": "python:3.11-slim",
		},
		"build": map[string]any{
			"build": "true",
			"test":  "pytest",
			"lint":  "true",
			"run":   "true",
		},
	}

	overrideJSON, marshalErr := json.Marshal(override)
	if marshalErr != nil {
		t.Fatalf("Failed to marshal override JSON: %v", marshalErr)
	}
	if writeErr := os.WriteFile(overridePath, overrideJSON, 0644); writeErr != nil {
		t.Fatalf("Failed to write override file: %v", writeErr)
	}

	if applyErr := ApplyConfigFile(overridePath); applyErr != nil {
		t.Fatalf("ApplyConfigFile() failed: %v", applyErr)
	}

	cfg, err := GetConfig()
	if err != nil {
		t.Fatalf("Failed to get config after apply: %v", err)
	}

	if cfg.Project.PrimaryPlatform != "python" {
		t.Errorf("Expected project.primary_platform 'python', got %q", cfg.Project.PrimaryPlatform)
	}
	if cfg.Project.PackName != "python" {
		t.Errorf("Expected project.pack_name 'python', got %q", cfg.Project.PackName)
	}
	if cfg.WebUI == nil || cfg.WebUI.Enabled {
		t.Fatalf("Expected webui.enabled to be false after override")
	}
	if cfg.WebUI.Port != 8080 {
		t.Errorf("Expected webui.port default to survive merge, got %d", cfg.WebUI.Port)
	}
	if cfg.Maintenance == nil || cfg.Maintenance.Enabled {
		t.Fatalf("Expected maintenance.enabled to be false after override")
	}
	if !cfg.Maintenance.Tasks.BranchCleanup {
		t.Error("Expected maintenance task defaults to survive nested merge")
	}
	if cfg.Container == nil || cfg.Container.Name != "python:3.11-slim" {
		t.Fatalf("Expected container.name override to persist, got %+v", cfg.Container)
	}
	if cfg.Container.Dockerfile != DefaultDockerfilePath {
		t.Errorf("Expected default dockerfile to survive merge, got %q", cfg.Container.Dockerfile)
	}
	if cfg.Build == nil || cfg.Build.Test != "pytest" {
		t.Fatalf("Expected build.test override 'pytest', got %+v", cfg.Build)
	}
	if cfg.Build.Build != "true" || cfg.Build.Lint != "true" || cfg.Build.Run != "true" {
		t.Errorf("Expected build overrides to persist, got build=%q test=%q lint=%q run=%q",
			cfg.Build.Build, cfg.Build.Test, cfg.Build.Lint, cfg.Build.Run)
	}

	SetConfigForTesting(nil)
	if reloadErr := LoadConfig(tempDir); reloadErr != nil {
		t.Fatalf("Failed to reload persisted config: %v", reloadErr)
	}

	reloaded, reloadGetErr := GetConfig()
	if reloadGetErr != nil {
		t.Fatalf("Failed to get reloaded config: %v", reloadGetErr)
	}
	if reloaded.WebUI == nil || reloaded.WebUI.Enabled {
		t.Fatalf("Expected persisted webui.enabled false after reload")
	}
	if reloaded.Maintenance == nil || reloaded.Maintenance.Enabled {
		t.Fatalf("Expected persisted maintenance.enabled false after reload")
	}
	if reloaded.Build == nil || reloaded.Build.Test != "pytest" {
		t.Fatalf("Expected persisted build.test 'pytest' after reload, got %+v", reloaded.Build)
	}
}

// TestLoadConfigWithGiteaForge verifies that a config with forge.provider = "gitea"
// and an HTTP git URL loads without deadlock (regression test for validateConfig
// calling GetForgeProvider under mu.Lock).
func TestLoadConfigWithGiteaForge(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "config-gitea-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)
	defer SetConfigForTesting(nil)

	maestroDir := filepath.Join(tempDir, ProjectConfigDir)
	if mkdirErr := os.MkdirAll(maestroDir, 0755); mkdirErr != nil {
		t.Fatalf("Failed to create .maestro dir: %v", mkdirErr)
	}

	// Write a config that uses Gitea forge with an HTTP URL.
	cfgJSON := `{
		"forge": {"provider": "gitea"},
		"git": {"repo_url": "http://localhost:3000/maestro/test-repo.git"}
	}`
	configPath := filepath.Join(maestroDir, "config.json")
	if writeErr := os.WriteFile(configPath, []byte(cfgJSON), 0644); writeErr != nil {
		t.Fatalf("Failed to write config: %v", writeErr)
	}

	// This would deadlock before the fix (validateConfig called GetForgeProvider
	// which tries mu.RLock while LoadConfig holds mu.Lock).
	if loadErr := LoadConfig(tempDir); loadErr != nil {
		t.Fatalf("LoadConfig failed: %v", loadErr)
	}

	cfg, getErr := GetConfig()
	if getErr != nil {
		t.Fatalf("GetConfig failed: %v", getErr)
	}

	if cfg.Forge == nil || cfg.Forge.Provider != ForgeProviderGitea {
		t.Errorf("Expected forge.provider %q, got %+v", ForgeProviderGitea, cfg.Forge)
	}
	if cfg.Git == nil || cfg.Git.RepoURL != "http://localhost:3000/maestro/test-repo.git" {
		t.Errorf("Expected git.repo_url to be preserved, got %+v", cfg.Git)
	}

	// Verify GetForgeProvider resolves correctly post-load.
	if provider := GetForgeProvider(); provider != ForgeProviderGitea {
		t.Errorf("Expected GetForgeProvider() = %q, got %q", ForgeProviderGitea, provider)
	}
}
