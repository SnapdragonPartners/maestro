// Package config provides configuration loading, validation, and management for the orchestrator.
//
// ARCHITECTURE OVERVIEW:
//
// This package implements a clean, atomic configuration management system that strictly separates
// configuration from state, with clear boundaries between project and orchestrator settings.
//
// KEY PRINCIPLES:
//
//  1. SEPARATION OF CONCERNS:
//     - Project Config: Per-project settings (container, build, agents, git) saved to .maestro/config.json
//     - Orchestrator Config: System-wide settings (models, rate limits) for the entire orchestrator
//     - Constants: Hardcoded algorithm parameters that users should not modify
//     - State/Metadata: Build status, timestamps, etc. belong in DATABASE, never in config
//
//  2. SCHEMA VERSIONING: All config changes MUST increment SchemaVersion to prevent breaking changes.
//     This prevents "willy-nilly" config updates that break existing installations.
//
//  3. GLOBAL SINGLETON: A single global Config instance is maintained in memory, protected by
//     mutex for thread safety.
//
//  4. ATOMIC UPDATES: Configuration changes happen atomically by subsystem (e.g., UpdateContainer,
//     UpdateBuild) with validation and automatic persistence. This prevents partial updates and
//     ensures consistency.
//
//  5. VALUE-BASED ACCESS: GetConfig() returns the config BY VALUE (copy, not reference) to
//     prevent external mutation. All updates MUST go through the Update* functions.
//
//  6. VALIDATION FIRST: All config updates are validated before persistence. Invalid configs
//     are rejected to maintain system integrity.
//
// USAGE PATTERNS:
//
//	// Load config from file (usually done once at startup)
//	err := config.LoadConfig(projectDir)
//
//	// Access config (always by value)
//	cfg, err := config.GetConfig()
//
//	// Update container config atomically with validation
//	err := config.UpdateContainer(projectDir, &newContainerConfig)
//
// ANTI-PATTERNS TO AVOID:
//
// - Adding build state (last built time, needs rebuild flags) to config - use database
// - Mixing project-specific and orchestrator-wide settings in same section
// - Direct field access without going through Update* functions
// - Adding user-configurable settings for algorithm constants
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Global config instance with mutex protection.
//
//nolint:gochecknoglobals // Intentional singleton pattern for config management
var (
	config *Config
	mu     sync.RWMutex
)

// Model represents an LLM model with its capabilities and limits.
type Model struct {
	Name           string  `json:"name"`            // e.g. "claude-3-5-sonnet"
	MaxTPM         int     `json:"max_tpm"`         // tokens per minute
	MaxConnections int     `json:"max_connections"` // max concurrent connections
	CPM            float64 `json:"cpm"`             // cost per million tokens (USD)
	DailyBudget    float64 `json:"daily_budget"`    // max spend per day (USD)
}

// ModelDefaults defines default parameters for all supported models.
//
//nolint:gochecknoglobals // Intentional global for model definitions
var ModelDefaults = map[string]Model{
	ModelClaudeSonnet: {
		Name:           ModelClaudeSonnet,
		MaxTPM:         40000,
		MaxConnections: 5,
		CPM:            3.0,
		DailyBudget:    10.0,
	},
	ModelO3Mini: {
		Name:           ModelO3Mini,
		MaxTPM:         10000,
		MaxConnections: 3,
		CPM:            0.6,
		DailyBudget:    5.0,
	},
}

// IsModelSupported checks if we have defaults for this model.
func IsModelSupported(modelName string) bool {
	_, exists := ModelDefaults[modelName]
	return exists
}

// AgentConfig defines which models to use and concurrency limits.
type AgentConfig struct {
	MaxCoders      int    `json:"max_coders"`      // must be <= CoderModel.MaxConnections
	CoderModel     string `json:"coder_model"`     // must match a Model.Name
	ArchitectModel string `json:"architect_model"` // must match a Model.Name
}

// All constants bundled together for easy maintenance.
const (
	// System behavior constants - these control orchestrator behavior and should not be user-configurable.

	// Shutdown and retry behavior.
	GracefulShutdownTimeoutSec = 30  // How long to wait for graceful shutdown before force-kill
	MaxRetryAttempts           = 3   // Maximum number of retry attempts for failed operations
	RetryBackoffMultiplier     = 2.0 // Exponential backoff multiplier for retries

	// Channel sizing for performance tuning.
	StoryChannelFactor   = 16 // Buffer factor for story channels: factor × numCoders
	QuestionsChannelSize = 2  // Buffer size for questions channel between agents

	// Docker container runtime defaults (applied when not specified in config).
	DefaultDockerNetwork = "none"      // Network isolation for security
	DefaultTmpfsSize     = "100m"      // Temporary filesystem size for /tmp
	DefaultDockerCPUs    = "2"         // CPU limit for container execution
	DefaultDockerMemory  = "2g"        // Memory limit for container execution
	DefaultDockerPIDs    = int64(1024) // Process limit for container execution

	// Git repository defaults.
	DefaultTargetBranch  = "main"             // Default target branch for pull requests
	DefaultMirrorDir     = ".mirrors"         // Default directory for git mirrors
	DefaultBranchPattern = "story-{STORY_ID}" // Default pattern for story branch names

	// Default Docker images for different project types (used only for dockerfile mode fallbacks).
	DefaultGoDockerImage     = "golang:alpine" // Use latest stable Go with alpine
	DefaultUbuntuDockerImage = "ubuntu:22.04"

	// Build target constants - used for GetBuildCommand() and elsewhere.
	BuildTargetBuild   = "build"
	BuildTargetTest    = "test"
	BuildTargetLint    = "lint"
	BuildTargetRun     = "run"
	BuildTargetClean   = "clean"
	BuildTargetInstall = "install"

	// Model name constants.
	ModelClaudeSonnet = "claude-3-5-sonnet-20241022"
	ModelO3Mini       = "o3-mini"

	// Project config constants.
	ProjectConfigFilename = "config.json"
	ProjectConfigDir      = ".maestro"
	DatabaseFilename      = "maestro.db"
	SchemaVersion         = "1.0"
)

// GitConfig contains git repository settings for the project.
// All git-related configuration is bundled here to eliminate redundancy.
type GitConfig struct {
	RepoURL       string `json:"repo_url"`       // Git repository URL for SSH clone/push
	TargetBranch  string `json:"target_branch"`  // Target branch for pull requests (default: main)
	MirrorDir     string `json:"mirror_dir"`     // Mirror directory path (default: .mirrors)
	BranchPattern string `json:"branch_pattern"` // Branch name pattern (default: story-{STORY_ID})
}

// OrchestratorConfig contains system-wide orchestrator settings.
// These settings apply to the entire orchestrator system, not individual projects.
// Keep this minimal - most settings should be per-project or constants.
type OrchestratorConfig struct {
	Models []Model `json:"models"` // Available LLM models with rate limits and budgets
}

// Config represents the main configuration for the orchestrator system.
//
// IMPORTANT: This structure enforces strict separation between:
// - Project settings: Project-specific configuration (container, build, agents, git)
// - Orchestrator settings: System-wide settings (models, rate limits)
// - State/metadata: Build status, timestamps, etc. belong in DATABASE, not here
//
// NOTE: Both project and orchestrator settings are saved together in .maestro/config.json
//
// Schema versioning prevents breaking changes - increment SchemaVersion for any structural changes.
type Config struct {
	SchemaVersion string `json:"schema_version"` // MUST increment for breaking changes

	// === PROJECT-SPECIFIC SETTINGS (per .maestro/config.json) ===
	Project   *ProjectInfo     `json:"project"`   // Basic project metadata (name, platform)
	Container *ContainerConfig `json:"container"` // Container settings (NO build state/metadata)
	Build     *BuildConfig     `json:"build"`     // Build commands and targets
	Agents    *AgentConfig     `json:"agents"`    // Which models to use for this project
	Git       *GitConfig       `json:"git"`       // Git repository and branching settings

	// === SYSTEM-WIDE ORCHESTRATOR SETTINGS ===
	Orchestrator *OrchestratorConfig `json:"orchestrator"` // LLM models, rate limits, budgets
}

// ProjectInfo contains basic project metadata.
// Only contains actual project configuration, not transient state or redundant data.
type ProjectInfo struct {
	Name            string `json:"name"`             // Project name
	PrimaryPlatform string `json:"primary_platform"` // Primary platform (go, node, python, etc.)
}

// ContainerConfig defines container settings for the project.
// This contains only declarative configuration - no build state or metadata.
type ContainerConfig struct {
	Name       string `json:"name"`                 // Container name/tag (standard image or custom built image)
	Dockerfile string `json:"dockerfile,omitempty"` // Path to dockerfile if building custom image

	// Docker runtime settings (command-line only, cannot be set in Dockerfile)
	Network   string `json:"network,omitempty"`    // Docker --network setting
	TmpfsSize string `json:"tmpfs_size,omitempty"` // Docker --tmpfs size setting
	CPUs      string `json:"cpus,omitempty"`       // Docker --cpus limit
	Memory    string `json:"memory,omitempty"`     // Docker --memory limit
	PIDs      int64  `json:"pids,omitempty"`       // Docker --pids-limit setting
}

// BuildConfig defines build targets and commands.
type BuildConfig struct {
	// Required targets (must exist)
	Build string `json:"build"` // Build command (default: "make build")
	Test  string `json:"test"`  // Test command (default: "make test")
	Lint  string `json:"lint"`  // Lint command (default: "make lint")
	Run   string `json:"run"`   // Run command (default: "make run")

	// Optional targets
	Clean   string `json:"clean,omitempty"`   // Clean command
	Install string `json:"install,omitempty"` // Install command
}

// GetConfig returns the current global config BY VALUE (copy, not reference).
// This prevents external mutation - all updates must go through Update* functions.
// Must call LoadConfig first to initialize the global config.
func GetConfig() (Config, error) {
	mu.RLock()
	defer mu.RUnlock()
	if config == nil {
		return Config{}, fmt.Errorf("config not initialized - call LoadConfig first")
	}
	// Return by value (copy) to prevent external mutation
	return *config, nil
}

// LoadConfig loads the entire configuration from <projectDir>/.maestro/config.json into
// the global singleton. This is a simple unmarshal operation of the complete Config struct.
//
// Behavior:
// - Missing file: Creates new config with defaults and saves it
// - Existing file: Loads and validates, applying defaults for missing fields
// - Unparseable file: Returns error to avoid overwriting user changes
//
// This should typically be called once at application startup.
func LoadConfig(projectDir string) error {
	mu.Lock()
	defer mu.Unlock()

	configPath := filepath.Join(projectDir, ProjectConfigDir, "config.json")

	// Check if config file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// Missing file - create new config with defaults
		config = createDefaultConfig()
		if err := saveConfigLocked(configPath); err != nil {
			return fmt.Errorf("failed to save initial config: %w", err)
		}
		return nil
	}

	// File exists - try to load it
	loadedConfig, err := loadConfigFromFile(configPath)
	if err != nil {
		return fmt.Errorf("fatal: config file exists but cannot be parsed (to avoid overwriting your changes): %w", err)
	}

	// Apply defaults and migrate old model names
	applyDefaults(loadedConfig)
	migrateModelNames(loadedConfig)
	if err := validateConfig(loadedConfig); err != nil {
		return fmt.Errorf("config validation failed: %w", err)
	}

	config = loadedConfig
	return nil
}

// UpdateAgents updates the agent configuration and persists to disk.
func UpdateAgents(projectDir string, agents *AgentConfig) error {
	mu.Lock()
	defer mu.Unlock()

	// Validate agent config by temporarily setting it and testing factory functions
	oldAgents := config.Agents
	config.Agents = agents

	// Test that both models can be retrieved with the new config
	if _, err := GetCoderModel(); err != nil {
		config.Agents = oldAgents // Restore old config
		return fmt.Errorf("invalid coder config: %w", err)
	}
	if _, err := GetArchitectModel(); err != nil {
		config.Agents = oldAgents // Restore old config
		return fmt.Errorf("invalid architect config: %w", err)
	}

	// Validation passed, keep the new config (already set above)
	return saveConfigLocked(filepath.Join(projectDir, ProjectConfigDir, "config.json"))
}

// UpdateContainer updates the container configuration and persists to disk.
func UpdateContainer(projectDir string, container *ContainerConfig) error {
	mu.Lock()
	defer mu.Unlock()

	config.Container = container
	return saveConfigLocked(filepath.Join(projectDir, ProjectConfigDir, "config.json"))
}

// UpdateBuild updates the build configuration and persists to disk.
func UpdateBuild(projectDir string, build *BuildConfig) error {
	mu.Lock()
	defer mu.Unlock()

	config.Build = build
	return saveConfigLocked(filepath.Join(projectDir, ProjectConfigDir, "config.json"))
}

// UpdateProject updates the project information and persists to disk.
func UpdateProject(projectDir string, project *ProjectInfo) error {
	mu.Lock()
	defer mu.Unlock()

	config.Project = project
	return saveConfigLocked(filepath.Join(projectDir, ProjectConfigDir, "config.json"))
}

// UpdateBootstrap is deprecated - bootstrap status is now tracked in database/logs.
// This function is kept for backward compatibility but does nothing.
func UpdateBootstrap(_ string, _ interface{}) error {
	// Bootstrap status moved to database - this is a no-op for compatibility
	return nil
}

// UpdateGit atomically updates the git configuration and persists to disk.
// This ensures that git repository settings are correctly saved.
func UpdateGit(projectDir string, git *GitConfig) error {
	mu.Lock()
	defer mu.Unlock()

	config.Git = git
	return saveConfigLocked(filepath.Join(projectDir, ProjectConfigDir, "config.json"))
}

// loadConfigFromFile loads a config file and parses JSON.
func loadConfigFromFile(configPath string) (*Config, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", configPath, err)
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config JSON %s: %w", configPath, err)
	}

	return &config, nil
}

// SaveConfig saves config to <projectDir>/.maestro/config.json.
func SaveConfig(config *Config, projectDir string) error {
	configPath := filepath.Join(projectDir, ProjectConfigDir, "config.json")

	// Create directory if needed
	configDir := filepath.Dir(configPath)
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// createDefaultConfig creates a new config with sensible defaults.
func createDefaultConfig() *Config {
	// Use ModelDefaults to populate default models
	defaultModels := make([]Model, 0, len(ModelDefaults))
	for name := range ModelDefaults {
		defaultModels = append(defaultModels, ModelDefaults[name])
	}

	return &Config{
		SchemaVersion: SchemaVersion,

		// Project-specific settings with defaults
		Project: &ProjectInfo{},
		Container: &ContainerConfig{
			// Apply Docker runtime defaults
			Network:   DefaultDockerNetwork,
			TmpfsSize: DefaultTmpfsSize,
			CPUs:      DefaultDockerCPUs,
			Memory:    DefaultDockerMemory,
			PIDs:      DefaultDockerPIDs,
		},
		Build: &BuildConfig{
			// Set default build targets
			Build: "make build",
			Test:  "make test",
			Lint:  "make lint",
			Run:   "make run",
		},
		Agents: &AgentConfig{
			MaxCoders:      2,
			CoderModel:     ModelClaudeSonnet,
			ArchitectModel: ModelO3Mini,
		},
		Git: &GitConfig{
			TargetBranch:  DefaultTargetBranch,
			MirrorDir:     DefaultMirrorDir,
			BranchPattern: DefaultBranchPattern,
		},

		// Orchestrator settings
		Orchestrator: &OrchestratorConfig{
			Models: defaultModels,
		},
	}
}

// saveConfigLocked saves config to disk. Must be called with mutex locked.
func saveConfigLocked(configPath string) error {
	// Create directory if needed
	configDir := filepath.Dir(configPath)
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// validateAgentConfigInternal validates agent configuration during config loading.
// This is separate from the factory functions to avoid circular dependencies.
func validateAgentConfigInternal(agents *AgentConfig, cfg *Config) error {
	if agents.MaxCoders <= 0 {
		return fmt.Errorf("max_coders must be positive")
	}

	// Validate coder model is supported
	if !IsModelSupported(agents.CoderModel) {
		return fmt.Errorf("coder_model '%s' is not supported", agents.CoderModel)
	}

	// Validate architect model is supported
	if !IsModelSupported(agents.ArchitectModel) {
		return fmt.Errorf("architect_model '%s' is not supported", agents.ArchitectModel)
	}

	// Find coder model in orchestrator config
	var coderModel *Model
	for i := range cfg.Orchestrator.Models {
		if cfg.Orchestrator.Models[i].Name == agents.CoderModel {
			coderModel = &cfg.Orchestrator.Models[i]
			break
		}
	}
	if coderModel == nil {
		return fmt.Errorf("coder_model '%s' not found in models list", agents.CoderModel)
	}

	// Find architect model in orchestrator config
	var architectModel *Model
	for i := range cfg.Orchestrator.Models {
		if cfg.Orchestrator.Models[i].Name == agents.ArchitectModel {
			architectModel = &cfg.Orchestrator.Models[i]
			break
		}
	}
	if architectModel == nil {
		return fmt.Errorf("architect_model '%s' not found in models list", agents.ArchitectModel)
	}

	// Validate concurrency limits
	if agents.MaxCoders > coderModel.MaxConnections {
		return fmt.Errorf("max_coders (%d) exceeds coder model max_connections (%d)",
			agents.MaxCoders, coderModel.MaxConnections)
	}

	return nil
}

// migrateModelNames updates old model names to new supported names.
func migrateModelNames(config *Config) {
	if config.Agents == nil || config.Orchestrator == nil {
		return
	}

	// Map of old name -> new name
	modelMigrations := map[string]string{
		"claude-3-5-sonnet": ModelClaudeSonnet,
		"o3-mini":           ModelO3Mini, // This one is the same, but for completeness
	}

	// Migrate coder model name
	if newName, exists := modelMigrations[config.Agents.CoderModel]; exists {
		config.Agents.CoderModel = newName
	}

	// Migrate architect model name
	if newName, exists := modelMigrations[config.Agents.ArchitectModel]; exists {
		config.Agents.ArchitectModel = newName
	}

	// Migrate model names in the Orchestrator Models slice
	for i := range config.Orchestrator.Models {
		if newName, exists := modelMigrations[config.Orchestrator.Models[i].Name]; exists {
			config.Orchestrator.Models[i].Name = newName
		}
	}
}

// applyDefaults sets default values for missing configuration.
func applyDefaults(config *Config) {
	// Initialize sections if nil
	if config.Project == nil {
		config.Project = &ProjectInfo{}
	}
	if config.Container == nil {
		config.Container = &ContainerConfig{}
	}
	if config.Build == nil {
		config.Build = &BuildConfig{}
	}
	if config.Agents == nil {
		config.Agents = &AgentConfig{}
	}
	if config.Git == nil {
		config.Git = &GitConfig{}
	}
	if config.Orchestrator == nil {
		config.Orchestrator = &OrchestratorConfig{}
	}

	// Apply container defaults
	if config.Container.Network == "" {
		config.Container.Network = DefaultDockerNetwork
	}
	if config.Container.TmpfsSize == "" {
		config.Container.TmpfsSize = DefaultTmpfsSize
	}
	if config.Container.CPUs == "" {
		config.Container.CPUs = DefaultDockerCPUs
	}
	if config.Container.Memory == "" {
		config.Container.Memory = DefaultDockerMemory
	}
	if config.Container.PIDs == 0 {
		config.Container.PIDs = DefaultDockerPIDs
	}

	// Apply build defaults
	if config.Build.Build == "" {
		config.Build.Build = "make build"
	}
	if config.Build.Test == "" {
		config.Build.Test = "make test"
	}
	if config.Build.Lint == "" {
		config.Build.Lint = "make lint"
	}
	if config.Build.Run == "" {
		config.Build.Run = "make run"
	}

	// Apply git defaults
	if config.Git.TargetBranch == "" {
		config.Git.TargetBranch = DefaultTargetBranch
	}
	if config.Git.MirrorDir == "" {
		config.Git.MirrorDir = DefaultMirrorDir
	}
	if config.Git.BranchPattern == "" {
		config.Git.BranchPattern = DefaultBranchPattern
	}

	// Apply orchestrator defaults
	if len(config.Orchestrator.Models) == 0 {
		// Use ModelDefaults to populate default models
		defaultModels := make([]Model, 0, len(ModelDefaults))
		for name := range ModelDefaults {
			defaultModels = append(defaultModels, ModelDefaults[name])
		}
		config.Orchestrator.Models = defaultModels
	}
}

func validateConfig(config *Config) error {
	// Validate orchestrator models
	if config.Orchestrator == nil || len(config.Orchestrator.Models) == 0 {
		return fmt.Errorf("no models configured in orchestrator section")
	}

	for i := range config.Orchestrator.Models {
		model := &config.Orchestrator.Models[i]
		if model.Name == "" {
			return fmt.Errorf("model[%d]: name is required", i)
		}
		if model.MaxTPM <= 0 {
			return fmt.Errorf("model %s: max_tpm must be positive", model.Name)
		}
		if model.MaxConnections <= 0 {
			return fmt.Errorf("model %s: max_connections must be positive", model.Name)
		}
		if model.CPM < 0 {
			return fmt.Errorf("model %s: cpm cannot be negative", model.Name)
		}
		if model.DailyBudget < 0 {
			return fmt.Errorf("model %s: daily_budget cannot be negative", model.Name)
		}
	}

	// Validate agent config
	if config.Agents != nil {
		if err := validateAgentConfigInternal(config.Agents, config); err != nil {
			return fmt.Errorf("agent config validation failed: %w", err)
		}
	}

	// Validate Git settings (RepoURL is optional - may not be using Git worktrees yet)
	if config.Git != nil && config.Git.RepoURL != "" {
		if !strings.HasPrefix(config.Git.RepoURL, "git@") && !strings.HasPrefix(config.Git.RepoURL, "https://") {
			return fmt.Errorf("git repo_url must start with 'git@' or 'https://'")
		}
	}

	return nil
}

// GetCoderModel returns the model configuration for coders.
func (c *Config) GetCoderModel() *Model {
	if c.Agents == nil || c.Orchestrator == nil {
		return nil
	}
	for i := range c.Orchestrator.Models {
		if c.Orchestrator.Models[i].Name == c.Agents.CoderModel {
			return &c.Orchestrator.Models[i]
		}
	}
	return nil
}

// GetArchitectModel returns the model configuration for the architect.
func (c *Config) GetArchitectModel() *Model {
	if c.Agents == nil || c.Orchestrator == nil {
		return nil
	}
	for i := range c.Orchestrator.Models {
		if c.Orchestrator.Models[i].Name == c.Agents.ArchitectModel {
			return &c.Orchestrator.Models[i]
		}
	}
	return nil
}

// validateAgentLimits performs basic validation on agent configuration.
func validateAgentLimits() error {
	cfg, err := GetConfig()
	if err != nil {
		return err
	}

	if cfg.Agents.MaxCoders <= 0 {
		return fmt.Errorf("max_coders must be positive")
	}

	return nil
}

// GetCoderModel returns the validated coder model configuration.
func GetCoderModel() (*Model, error) {
	// Validate basic agent limits first
	if err := validateAgentLimits(); err != nil {
		return nil, err
	}

	cfg, err := GetConfig()
	if err != nil {
		return nil, err
	}

	// Validate model is supported
	if !IsModelSupported(cfg.Agents.CoderModel) {
		return nil, fmt.Errorf("coder_model '%s' is not supported", cfg.Agents.CoderModel)
	}

	// Find model in orchestrator config
	for i := range cfg.Orchestrator.Models {
		if cfg.Orchestrator.Models[i].Name == cfg.Agents.CoderModel { //nolint:gocritic // Clear logic flow
			model := &cfg.Orchestrator.Models[i]

			// Validate all model parameters
			if model.MaxTPM <= 0 {
				return nil, fmt.Errorf("model '%s' has invalid MaxTPM: %d", model.Name, model.MaxTPM)
			}
			if model.MaxConnections <= 0 {
				return nil, fmt.Errorf("model '%s' has invalid MaxConnections: %d", model.Name, model.MaxConnections)
			}
			if model.CPM < 0 {
				return nil, fmt.Errorf("model '%s' has invalid CPM: %f", model.Name, model.CPM)
			}
			if model.DailyBudget < 0 {
				return nil, fmt.Errorf("model '%s' has invalid DailyBudget: %f", model.Name, model.DailyBudget)
			}

			// Validate concurrency limits
			if cfg.Agents.MaxCoders > model.MaxConnections {
				return nil, fmt.Errorf("max_coders (%d) exceeds coder model max_connections (%d)",
					cfg.Agents.MaxCoders, model.MaxConnections)
			}

			return model, nil
		}
	}
	return nil, fmt.Errorf("coder_model '%s' not found in config", cfg.Agents.CoderModel)
}

// GetArchitectModel returns the validated architect model configuration.
func GetArchitectModel() (*Model, error) {
	// Validate basic agent limits first
	if err := validateAgentLimits(); err != nil {
		return nil, err
	}

	cfg, err := GetConfig()
	if err != nil {
		return nil, err
	}

	// Validate model is supported
	if !IsModelSupported(cfg.Agents.ArchitectModel) {
		return nil, fmt.Errorf("architect_model '%s' is not supported", cfg.Agents.ArchitectModel)
	}

	// Find model in orchestrator config
	for i := range cfg.Orchestrator.Models {
		if cfg.Orchestrator.Models[i].Name == cfg.Agents.ArchitectModel { //nolint:gocritic // Clear logic flow
			model := &cfg.Orchestrator.Models[i]

			// Validate all model parameters
			if model.MaxTPM <= 0 {
				return nil, fmt.Errorf("model '%s' has invalid MaxTPM: %d", model.Name, model.MaxTPM)
			}
			if model.MaxConnections <= 0 {
				return nil, fmt.Errorf("model '%s' has invalid MaxConnections: %d", model.Name, model.MaxConnections)
			}
			if model.CPM < 0 {
				return nil, fmt.Errorf("model '%s' has invalid CPM: %f", model.Name, model.CPM)
			}
			if model.DailyBudget < 0 {
				return nil, fmt.Errorf("model '%s' has invalid DailyBudget: %f", model.Name, model.DailyBudget)
			}

			// No concurrency limit check for architect (only one architect)
			return model, nil
		}
	}
	return nil, fmt.Errorf("architect_model '%s' not found in config", cfg.Agents.ArchitectModel)
}
