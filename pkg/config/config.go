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
	"time"
)

// Global config instance with mutex protection.
// projectDir is set once during LoadConfig and never changes - it defines where all
// maestro files are stored relative to the project root.
//
//nolint:gochecknoglobals // Intentional singleton pattern for config management
var (
	config     *Config
	projectDir string // Immutable after LoadConfig - set once at startup
	mu         sync.RWMutex
)

// Model represents an LLM model with its capabilities and limits.
type Model struct {
	Name           string  `json:"name"`            // e.g. "claude-sonnet-4-20250514"
	MaxTPM         int     `json:"max_tpm"`         // tokens per minute
	MaxConnections int     `json:"max_connections"` // max concurrent connections
	CPM            float64 `json:"cpm"`             // cost per million tokens (USD)
	DailyBudget    float64 `json:"daily_budget"`    // max spend per day (USD)
}

// ModelDefaults defines default parameters for all supported models.
//
//nolint:gochecknoglobals // Intentional global for model definitions
var ModelDefaults = map[string]Model{
	ModelClaudeSonnet3: {
		Name:           ModelClaudeSonnet3,
		MaxTPM:         300000,
		MaxConnections: 5,
		CPM:            3.0,
		DailyBudget:    10.0,
	},
	ModelClaudeSonnet4: {
		Name:           ModelClaudeSonnet4,
		MaxTPM:         3000000,
		MaxConnections: 5,
		CPM:            3.0,
		DailyBudget:    10.0,
	},
	ModelOpenAIO3Mini: {
		Name:           ModelOpenAIO3Mini,
		MaxTPM:         100000,
		MaxConnections: 3,
		CPM:            0.6,
		DailyBudget:    5.0,
	},
	ModelOpenAIO3: {
		Name:           ModelOpenAIO3,
		MaxTPM:         100000,
		MaxConnections: 3,
		CPM:            0.6,
		DailyBudget:    5.0,
	},
	ModelGPT5: {
		Name:           ModelGPT5,
		MaxTPM:         150000, // Higher limits for GPT-5
		MaxConnections: 5,      // More connections
		CPM:            30.0,   // Premium pricing for GPT-5
		DailyBudget:    100.0,  // Higher budget
	},
}

// ModelProviders maps each model to its API provider for middleware configuration.
// This mapping is immutable and not user-configurable.
//
//nolint:gochecknoglobals // Intentional global for model-to-provider mapping
var ModelProviders = map[string]string{
	ModelClaudeSonnet3: ProviderAnthropic,
	ModelClaudeSonnet4: ProviderAnthropic,
	ModelOpenAIO3:      ProviderOpenAI,
	ModelOpenAIO3Mini:  ProviderOpenAIOfficial,
	ModelGPT5:          ProviderOpenAIOfficial,
}

// IsModelSupported checks if we have defaults for this model.
func IsModelSupported(modelName string) bool {
	_, exists := ModelDefaults[modelName]
	return exists
}

// GetModelProvider returns the API provider for a given model.
func GetModelProvider(modelName string) (string, error) {
	provider, exists := ModelProviders[modelName]
	if !exists {
		return "", fmt.Errorf("unknown model: %s", modelName)
	}
	return provider, nil
}

// CircuitBreakerConfig defines configuration for circuit breaker behavior.
type CircuitBreakerConfig struct {
	FailureThreshold int           `json:"failure_threshold"` // Number of failures before opening circuit
	SuccessThreshold int           `json:"success_threshold"` // Number of successes to close circuit from half-open
	Timeout          time.Duration `json:"timeout"`           // Time to wait before trying half-open
}

// RetryConfig defines configuration for retry behavior.
type RetryConfig struct {
	MaxAttempts   int           `json:"max_attempts"`   // Maximum number of attempts (including initial)
	InitialDelay  time.Duration `json:"initial_delay"`  // Initial delay before first retry
	MaxDelay      time.Duration `json:"max_delay"`      // Maximum delay between retries
	BackoffFactor float64       `json:"backoff_factor"` // Multiplier for exponential backoff
	Jitter        bool          `json:"jitter"`         // Add random jitter to prevent thundering herd
}

// ProviderLimits defines rate limiting configuration for a specific API provider.
type ProviderLimits struct {
	TokensPerMinute int `json:"tokens_per_minute"` // Rate limit in tokens per minute
	Burst           int `json:"burst"`             // Burst capacity
	MaxConcurrency  int `json:"max_concurrency"`   // Maximum concurrent requests
}

// RateLimitConfig defines rate limiting configuration grouped by API provider.
type RateLimitConfig struct {
	Anthropic      ProviderLimits `json:"anthropic"`       // Rate limits for Anthropic models
	OpenAI         ProviderLimits `json:"openai"`          // Rate limits for OpenAI models
	OpenAIOfficial ProviderLimits `json:"openai_official"` // Rate limits for OpenAI Official models
}

// ResilienceConfig bundles all resilience-related middleware configuration.
type ResilienceConfig struct {
	CircuitBreaker CircuitBreakerConfig `json:"circuit_breaker"` // Circuit breaker settings
	Retry          RetryConfig          `json:"retry"`           // Retry policy settings
	RateLimit      RateLimitConfig      `json:"rate_limit"`      // Rate limiting settings
	Timeout        time.Duration        `json:"timeout"`         // Per-request timeout
}

// MetricsConfig defines configuration for metrics collection.
type MetricsConfig struct {
	Enabled   bool   `json:"enabled"`   // Whether metrics collection is enabled
	Exporter  string `json:"exporter"`  // Metrics exporter type ("prometheus", "noop")
	Namespace string `json:"namespace"` // Metrics namespace for grouping
}

// AgentConfig defines which models to use and concurrency limits.
type AgentConfig struct {
	MaxCoders      int              `json:"max_coders"`      // must be <= CoderModel.MaxConnections
	CoderModel     string           `json:"coder_model"`     // must match a Model.Name
	ArchitectModel string           `json:"architect_model"` // must match a Model.Name
	Metrics        MetricsConfig    `json:"metrics"`         // Metrics collection configuration
	Resilience     ResilienceConfig `json:"resilience"`      // Resilience middleware configuration
	StateTimeout   time.Duration    `json:"state_timeout"`   // Global timeout for any state processing
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
	ModelClaudeSonnet4      = "claude-sonnet-4-20250514"
	ModelClaudeSonnet3      = "claude-3-7-sonnet-20250219"
	ModelClaudeSonnetLatest = ModelClaudeSonnet4
	ModelOpenAIO3           = "o3"
	ModelOpenAIO3Mini       = "o3-mini"
	ModelOpenAIO3Latest     = ModelOpenAIO3
	ModelGPT5               = "gpt-5"
	DefaultCoderModel       = ModelClaudeSonnet4
	DefaultArchitectModel   = ModelOpenAIO3Mini

	// Project config constants.
	ProjectConfigFilename = "config.json"
	ProjectConfigDir      = ".maestro"
	DatabaseFilename      = "maestro.db"
	SchemaVersion         = "1.0"

	// Provider constants for middleware rate limiting.
	ProviderAnthropic      = "anthropic"
	ProviderOpenAI         = "openai"
	ProviderOpenAIOfficial = "openai_official"
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
	Name          string `json:"name"`                     // Container name/tag (standard image or custom built image)
	Dockerfile    string `json:"dockerfile,omitempty"`     // Path to dockerfile if building custom image
	WorkspacePath string `json:"workspace_path,omitempty"` // Path where project is mounted inside container (default: "/workspace")

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

// GetProjectMaestroDir returns the path to the .maestro directory containing all maestro files.
// Must call LoadConfig first to initialize projectDir.
func GetProjectMaestroDir() (string, error) {
	mu.RLock()
	defer mu.RUnlock()
	if projectDir == "" {
		return "", fmt.Errorf("config not initialized - call LoadConfig first")
	}
	return filepath.Join(projectDir, ProjectConfigDir), nil
}

// GetProjectDir returns the current project directory.
// Must call LoadConfig first to initialize projectDir.
func GetProjectDir() (string, error) {
	mu.RLock()
	defer mu.RUnlock()
	if projectDir == "" {
		return "", fmt.Errorf("config not initialized - call LoadConfig first")
	}
	return projectDir, nil
}

// GetContainerWorkspacePath returns the workspace path used inside containers.
// This is where the project directory gets mounted inside containers.
// Must call LoadConfig first to initialize config.
func GetContainerWorkspacePath() (string, error) {
	cfg, err := GetConfig()
	if err != nil {
		return "", err
	}

	// Use configured workspace path or default to "/workspace"
	if cfg.Container != nil && cfg.Container.WorkspacePath != "" {
		return cfg.Container.WorkspacePath, nil
	}

	return "/workspace", nil
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
func LoadConfig(inputProjectDir string) error {
	mu.Lock()
	defer mu.Unlock()

	// Store project directory - immutable after this point
	projectDir = inputProjectDir
	configPath := filepath.Join(projectDir, ProjectConfigDir, "config.json")

	// Check if config file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// Missing file - create new config with defaults
		config = createDefaultConfig()
		if err := saveConfigLocked(); err != nil {
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
	if err := validateConfig(loadedConfig); err != nil {
		return fmt.Errorf("config validation failed: %w", err)
	}

	config = loadedConfig
	return nil
}

// UpdateAgents updates the agent configuration and persists to disk.
func UpdateAgents(_ string, agents *AgentConfig) error {
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
	return saveConfigLocked()
}

// UpdateContainer updates the container configuration and persists to disk.
func UpdateContainer(container *ContainerConfig) error {
	mu.Lock()
	defer mu.Unlock()

	config.Container = container
	return saveConfigLocked()
}

// UpdateBuild updates the build configuration and persists to disk.
func UpdateBuild(build *BuildConfig) error {
	mu.Lock()
	defer mu.Unlock()

	config.Build = build
	return saveConfigLocked()
}

// UpdateProject updates the project information and persists to disk.
func UpdateProject(project *ProjectInfo) error {
	mu.Lock()
	defer mu.Unlock()

	config.Project = project
	return saveConfigLocked()
}

// UpdateBootstrap is deprecated - bootstrap status is now tracked in database/logs.
// This function is kept for backward compatibility but does nothing.
func UpdateBootstrap(_ string, _ interface{}) error {
	// Bootstrap status moved to database - this is a no-op for compatibility
	return nil
}

// UpdateGit atomically updates the git configuration and persists to disk.
// This ensures that git repository settings are correctly saved.
func UpdateGit(git *GitConfig) error {
	mu.Lock()
	defer mu.Unlock()

	config.Git = git
	return saveConfigLocked()
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
			CoderModel:     DefaultCoderModel,
			ArchitectModel: DefaultArchitectModel,
			Metrics: MetricsConfig{
				Enabled:   false,
				Exporter:  "noop",
				Namespace: "maestro",
			},
			Resilience: ResilienceConfig{
				CircuitBreaker: CircuitBreakerConfig{
					FailureThreshold: 5,
					SuccessThreshold: 3,
					Timeout:          30 * time.Second,
				},
				Retry: RetryConfig{
					MaxAttempts:   3,
					InitialDelay:  100 * time.Millisecond,
					MaxDelay:      10 * time.Second,
					BackoffFactor: 2.0,
					Jitter:        true,
				},
				RateLimit: RateLimitConfig{
					Anthropic: ProviderLimits{
						TokensPerMinute: 300000,
						Burst:           10000,
						MaxConcurrency:  5,
					},
					OpenAI: ProviderLimits{
						TokensPerMinute: 100000,
						Burst:           5000,
						MaxConcurrency:  3,
					},
				},
				Timeout: 3 * time.Minute, // Increased for GPT-5 reasoning time
			},
			StateTimeout: 10 * time.Minute, // Global timeout for state processing
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

// saveConfigLocked saves config to disk using the stored project directory.
// Must be called with mutex locked.
func saveConfigLocked() error {
	if projectDir == "" {
		return fmt.Errorf("config not initialized - call LoadConfig first")
	}

	configPath := filepath.Join(projectDir, ProjectConfigDir, ProjectConfigFilename)

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

	// Apply agent defaults
	if config.Agents.MaxCoders == 0 {
		config.Agents.MaxCoders = 2
	}
	if config.Agents.CoderModel == "" {
		config.Agents.CoderModel = DefaultCoderModel
	}
	if config.Agents.ArchitectModel == "" {
		config.Agents.ArchitectModel = DefaultArchitectModel
	}

	// Apply metrics defaults
	if config.Agents.Metrics.Exporter == "" {
		config.Agents.Metrics.Exporter = "noop"
	}
	if config.Agents.Metrics.Namespace == "" {
		config.Agents.Metrics.Namespace = "maestro"
	}

	// Apply resilience defaults
	if config.Agents.Resilience.CircuitBreaker.FailureThreshold == 0 {
		config.Agents.Resilience.CircuitBreaker.FailureThreshold = 5
	}
	if config.Agents.Resilience.CircuitBreaker.SuccessThreshold == 0 {
		config.Agents.Resilience.CircuitBreaker.SuccessThreshold = 3
	}
	if config.Agents.Resilience.CircuitBreaker.Timeout == 0 {
		config.Agents.Resilience.CircuitBreaker.Timeout = 30 * time.Second
	}

	if config.Agents.Resilience.Retry.MaxAttempts == 0 {
		config.Agents.Resilience.Retry.MaxAttempts = 3
	}
	if config.Agents.Resilience.Retry.InitialDelay == 0 {
		config.Agents.Resilience.Retry.InitialDelay = 100 * time.Millisecond
	}
	if config.Agents.Resilience.Retry.MaxDelay == 0 {
		config.Agents.Resilience.Retry.MaxDelay = 10 * time.Second
	}
	if config.Agents.Resilience.Retry.BackoffFactor == 0 {
		config.Agents.Resilience.Retry.BackoffFactor = 2.0
	}

	if config.Agents.Resilience.RateLimit.Anthropic.TokensPerMinute == 0 {
		config.Agents.Resilience.RateLimit.Anthropic.TokensPerMinute = 300000
	}
	if config.Agents.Resilience.RateLimit.Anthropic.Burst == 0 {
		config.Agents.Resilience.RateLimit.Anthropic.Burst = 10000
	}
	if config.Agents.Resilience.RateLimit.Anthropic.MaxConcurrency == 0 {
		config.Agents.Resilience.RateLimit.Anthropic.MaxConcurrency = 5
	}

	if config.Agents.Resilience.RateLimit.OpenAI.TokensPerMinute == 0 {
		config.Agents.Resilience.RateLimit.OpenAI.TokensPerMinute = 100000
	}
	if config.Agents.Resilience.RateLimit.OpenAI.Burst == 0 {
		config.Agents.Resilience.RateLimit.OpenAI.Burst = 5000
	}
	if config.Agents.Resilience.RateLimit.OpenAI.MaxConcurrency == 0 {
		config.Agents.Resilience.RateLimit.OpenAI.MaxConcurrency = 3
	}

	// Set defaults for OpenAI Official provider (higher limits for premium GPT-5)
	if config.Agents.Resilience.RateLimit.OpenAIOfficial.TokensPerMinute == 0 {
		config.Agents.Resilience.RateLimit.OpenAIOfficial.TokensPerMinute = 150000
	}
	if config.Agents.Resilience.RateLimit.OpenAIOfficial.Burst == 0 {
		config.Agents.Resilience.RateLimit.OpenAIOfficial.Burst = 10000
	}
	if config.Agents.Resilience.RateLimit.OpenAIOfficial.MaxConcurrency == 0 {
		config.Agents.Resilience.RateLimit.OpenAIOfficial.MaxConcurrency = 5
	}

	if config.Agents.Resilience.Timeout == 0 {
		config.Agents.Resilience.Timeout = 3 * time.Minute // Increased for GPT-5 reasoning time (was 60s)
	}

	// Apply state timeout default
	if config.Agents.StateTimeout == 0 {
		config.Agents.StateTimeout = 10 * time.Minute
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

// GetAPIKey returns the API key for a given provider from environment variables.
func GetAPIKey(provider string) (string, error) {
	var envVar string
	switch provider {
	case ProviderAnthropic:
		envVar = "ANTHROPIC_API_KEY"
	case ProviderOpenAI, ProviderOpenAIOfficial:
		envVar = "OPENAI_API_KEY" // Both use the same API key
	default:
		return "", fmt.Errorf("unknown provider: %s", provider)
	}

	key := os.Getenv(envVar)
	if key == "" {
		return "", fmt.Errorf("API key not found: %s environment variable is not set", envVar)
	}
	return key, nil
}

// ValidateAPIKeysForConfig validates that all required API keys are available for the configured models.
func ValidateAPIKeysForConfig() error {
	cfg, err := GetConfig()
	if err != nil {
		return fmt.Errorf("configuration not loaded: %w", err)
	}

	// Collect all providers used by configured models
	requiredProviders := make(map[string]bool)

	// Check coder model provider
	coderProvider, err := GetModelProvider(cfg.Agents.CoderModel)
	if err != nil {
		return fmt.Errorf("failed to get provider for coder model: %w", err)
	}
	requiredProviders[coderProvider] = true

	// Check architect model provider
	architectProvider, err := GetModelProvider(cfg.Agents.ArchitectModel)
	if err != nil {
		return fmt.Errorf("failed to get provider for architect model: %w", err)
	}
	requiredProviders[architectProvider] = true

	// Validate API keys for all required providers
	for provider := range requiredProviders {
		if _, err := GetAPIKey(provider); err != nil {
			return fmt.Errorf("missing API key for provider %s: %w", provider, err)
		}
	}

	return nil
}
