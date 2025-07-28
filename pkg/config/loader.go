// Package config provides configuration loading, validation, and management for the orchestrator.
// It handles JSON config files, environment variable substitution, and agent configuration.
package config

import (
	"crypto/md5" //nolint:gosec // MD5 is acceptable for non-cryptographic file change detection
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Agent type constants.
const (
	AgentTypeArchitect = "architect"
	AgentTypeCoder     = "coder"
)

// Default iteration budgets for coder agents.
const (
	DefaultCodingBudget = 8 // Default from existing hardcoded value
	DefaultFixingBudget = 3 // Default from story requirements
)

// Default Docker images for different project types.
const (
	DefaultGoDockerImage     = "golang:1.24-alpine"
	DefaultUbuntuDockerImage = "ubuntu:22.04"
)

// Project config constants.
const (
	ProjectConfigFilename = "config.json"
	UserConfigDir         = ".maestro"
	ProjectConfigDir      = ".maestro"
	SchemaVersion         = "1.0"
)

// IterationBudgets defines the retry limits for coding operations.
type IterationBudgets struct {
	CodingBudget int `json:"coding_budget"`
	FixingBudget int `json:"fixing_budget"`
}

// Agent represents a single agent configuration with its type and execution parameters.
type Agent struct {
	Name             string           `json:"name"`
	ID               string           `json:"id"`
	Type             string           `json:"type"` // "architect" or "coder"
	WorkDir          string           `json:"workdir"`
	IterationBudgets IterationBudgets `json:"iteration_budgets"`
	DockerImage      string           `json:"docker_image,omitempty"` // Optional Docker image override
}

// ModelCfg defines the configuration for an LLM model including rate limits and API settings.
type ModelCfg struct {
	MaxTokensPerMinute int     `json:"max_tokens_per_minute"`
	MaxBudgetPerDayUSD float64 `json:"max_budget_per_day_usd"`
	CpmTokensIn        float64 `json:"cpm_tokens_in"`
	CpmTokensOut       float64 `json:"cpm_tokens_out"`
	APIKey             string  `json:"api_key"`
	Agents             []Agent `json:"agents"`
	// Context management settings.
	MaxContextTokens int `json:"max_context_tokens"` // Maximum total context size
	MaxReplyTokens   int `json:"max_reply_tokens"`   // Maximum tokens for model reply
	CompactionBuffer int `json:"compaction_buffer"`  // Buffer tokens before compaction
}

// ExecutorConfig contains executor-specific configuration.
type ExecutorConfig struct {
	Type     string       `json:"type"`     // "auto", "docker", "local"
	Docker   DockerConfig `json:"docker"`   // Docker-specific settings
	Fallback string       `json:"fallback"` // Fallback executor when preferred unavailable
}

// DockerConfig contains Docker executor configuration.
type DockerConfig struct {
	Image       string            `json:"image"`        // Docker image to use
	Network     string            `json:"network"`      // Network mode: "none", "bridge", "host"
	ReadOnly    bool              `json:"read_only"`    // Mount filesystem read-only
	TmpfsSize   string            `json:"tmpfs_size"`   // Size for tmpfs mounts (e.g., "100m")
	Volumes     []string          `json:"volumes"`      // Additional volumes to mount
	Env         map[string]string `json:"env"`          // Environment variables
	CPUs        string            `json:"cpus"`         // CPU limit (e.g., "2", "1.5")
	Memory      string            `json:"memory"`       // Memory limit (e.g., "2g", "512m")
	PIDs        int64             `json:"pids"`         // Process limit
	AutoPull    bool              `json:"auto_pull"`    // Auto-pull image if not available
	PullTimeout int               `json:"pull_timeout"` // Timeout for image pull in seconds
}

// Config represents the main configuration for the orchestrator system.
type Config struct {
	Models                     map[string]ModelCfg `json:"models"`
	GracefulShutdownTimeoutSec int                 `json:"graceful_shutdown_timeout_sec"`
	EventLogRotationHours      int                 `json:"event_log_rotation_hours"`
	MaxRetryAttempts           int                 `json:"max_retry_attempts"`
	RetryBackoffMultiplier     float64             `json:"retry_backoff_multiplier"`
	StoryChannelFactor         int                 `json:"story_channel_factor"`   // Buffer factor for storyCh: factor × numCoders
	QuestionsChannelSize       int                 `json:"questions_channel_size"` // Buffer size for questionsCh
	// Git worktree settings.
	RepoURL         string `json:"repo_url"`         // Git repository URL for SSH clone/push
	BaseBranch      string `json:"base_branch"`      // Base branch name (default: main)
	MirrorDir       string `json:"mirror_dir"`       // Mirror directory path (default: $WORKDIR/.mirrors)
	WorktreePattern string `json:"worktree_pattern"` // Worktree path pattern (default: {$WORKDIR}/{AGENT_ID}/{STORY_ID})
	BranchPattern   string `json:"branch_pattern"`   // Branch name pattern (default: story-{STORY_ID})
	// Executor configuration.
	Executor ExecutorConfig `json:"executor"` // Executor settings
}

// ProjectInfo contains basic project metadata.
type ProjectInfo struct {
	Name               string    `json:"name"`
	GitRepo            string    `json:"git_repo"`
	TargetBranch       string    `json:"target_branch"`
	Platform           string    `json:"platform"`
	PlatformConfidence float64   `json:"platform_confidence"`
	CreatedAt          time.Time `json:"created_at"`
}

// ContainerConfig defines container settings for the project.
type ContainerConfig struct {
	Image          string            `json:"image,omitempty"`           // Pre-built image name
	Dockerfile     string            `json:"dockerfile,omitempty"`      // Custom dockerfile path
	DockerfileHash string            `json:"dockerfile_hash,omitempty"` // MD5 hash of dockerfile
	ImageTag       string            `json:"image_tag,omitempty"`       // Built image tag
	NeedsRebuild   bool              `json:"needs_rebuild"`             // Whether container needs rebuilding
	LastBuilt      time.Time         `json:"last_built,omitempty"`      // When container was last built
	Environment    map[string]string `json:"environment,omitempty"`     // Environment variables
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

// BootstrapInfo tracks bootstrap completion status.
type BootstrapInfo struct {
	Completed        bool            `json:"completed"`
	LastRun          time.Time       `json:"last_run,omitempty"`
	RequirementsMet  map[string]bool `json:"requirements_met"`
	ValidationErrors []string        `json:"validation_errors,omitempty"`
}

// ProjectConfig represents the project-specific configuration stored in .maestro/config.json.
type ProjectConfig struct {
	SchemaVersion string          `json:"schema_version"`
	Project       ProjectInfo     `json:"project"`
	Container     ContainerConfig `json:"container"`
	Build         BuildConfig     `json:"build"`
	Bootstrap     BootstrapInfo   `json:"bootstrap"`
	mu            sync.RWMutex    `json:"-"` // Mutex for thread-safe access
}

// UserConfig represents user-level defaults stored in ~/.maestro/config.json.
type UserConfig struct {
	SchemaVersion    string                 `json:"schema_version"`
	DefaultPlatform  string                 `json:"default_platform,omitempty"`
	DefaultContainer ContainerConfig        `json:"default_container,omitempty"`
	DefaultBuild     BuildConfig            `json:"default_build,omitempty"`
	UserPreferences  map[string]interface{} `json:"user_preferences,omitempty"`
}

var envVarRegex = regexp.MustCompile(`\$\{([^}]+)\}`)

// LoadConfig loads and validates configuration from a JSON file with environment variable substitution.
func LoadConfig(configPath string) (*Config, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Replace environment variable placeholders.
	dataStr := string(data)
	dataStr = envVarRegex.ReplaceAllStringFunc(dataStr, func(match string) string {
		envVar := match[2 : len(match)-1] // Remove ${ and }
		if value := os.Getenv(envVar); value != "" {
			return value
		}
		return match // Return original if env var not found
	})

	var config Config
	if err := json.Unmarshal([]byte(dataStr), &config); err != nil {
		return nil, fmt.Errorf("failed to parse config JSON: %w", err)
	}

	// Apply environment variable overrides.
	applyEnvOverrides(&config)

	// Apply defaults.
	applyDefaults(&config)

	// Validate config.
	if err := validateConfig(&config); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return &config, nil
}

func applyEnvOverrides(config *Config) {
	v := reflect.ValueOf(config).Elem()
	t := reflect.TypeOf(config).Elem()

	applyEnvOverridesRecursive(v, t, "")
}

func applyEnvOverridesRecursive(v reflect.Value, t reflect.Type, prefix string) {
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		fieldType := t.Field(i)

		jsonTag := fieldType.Tag.Get("json")
		if jsonTag == "" || jsonTag == "-" {
			continue
		}

		fieldName := strings.Split(jsonTag, ",")[0]
		envKey := strings.ToUpper(prefix + fieldName)

		if envValue := os.Getenv(envKey); envValue != "" {
			setFieldFromEnv(field, envValue)
		}

		if field.Kind() == reflect.Map && field.Type().Key().Kind() == reflect.String {
			// Handle map fields like Models.
			if field.IsNil() {
				field.Set(reflect.MakeMap(field.Type()))
			}

			for _, key := range field.MapKeys() {
				mapValue := field.MapIndex(key)
				if mapValue.Kind() == reflect.Struct {
					structValue := reflect.New(mapValue.Type()).Elem()
					structValue.Set(mapValue)
					applyEnvOverridesRecursive(structValue, mapValue.Type(), envKey+"_"+strings.ToUpper(key.String())+"_")
					field.SetMapIndex(key, structValue)
				}
			}
		}
	}
}

func setFieldFromEnv(field reflect.Value, envValue string) {
	if !field.CanSet() {
		return
	}

	switch field.Kind() {
	case reflect.String:
		field.SetString(envValue)
	case reflect.Int:
		if val, err := parseInt(envValue); err == nil {
			field.SetInt(int64(val))
		}
	case reflect.Float64:
		if val, err := parseFloat(envValue); err == nil {
			field.SetFloat(val)
		}
	}
}

func parseInt(s string) (int, error) {
	var result int
	_, err := fmt.Sscanf(s, "%d", &result)
	if err != nil {
		return 0, fmt.Errorf("failed to parse int from '%s': %w", s, err)
	}
	return result, nil
}

func parseFloat(s string) (float64, error) {
	var result float64
	_, err := fmt.Sscanf(s, "%f", &result)
	if err != nil {
		return 0, fmt.Errorf("failed to parse float from '%s': %w", s, err)
	}
	return result, nil
}

// applyDefaults sets default values for missing configuration.
func applyDefaults(config *Config) {
	// Set executor defaults if not specified.
	if config.Executor.Type == "" {
		config.Executor.Type = "docker"
	}
	if config.Executor.Fallback == "" {
		config.Executor.Fallback = "local"
	}

	// Set Docker defaults.
	if config.Executor.Docker.Image == "" {
		config.Executor.Docker.Image = DefaultGoDockerImage
	}
	if config.Executor.Docker.Network == "" {
		config.Executor.Docker.Network = "none"
	}
	if config.Executor.Docker.TmpfsSize == "" {
		config.Executor.Docker.TmpfsSize = "100m"
	}
	if config.Executor.Docker.CPUs == "" {
		config.Executor.Docker.CPUs = "2"
	}
	if config.Executor.Docker.Memory == "" {
		config.Executor.Docker.Memory = "2g"
	}
	if config.Executor.Docker.PIDs == 0 {
		config.Executor.Docker.PIDs = 1024
	}
	if config.Executor.Docker.PullTimeout == 0 {
		config.Executor.Docker.PullTimeout = 300 // 5 minutes
	}

	// Initialize empty maps if nil.
	if config.Executor.Docker.Env == nil {
		config.Executor.Docker.Env = make(map[string]string)
	}
	if config.Executor.Docker.Volumes == nil {
		config.Executor.Docker.Volumes = []string{}
	}
}

func validateConfig(config *Config) error {
	if len(config.Models) == 0 {
		return fmt.Errorf("no models configured")
	}

	// Validate executor configuration.
	if err := validateExecutorConfig(&config.Executor); err != nil {
		return fmt.Errorf("executor config validation failed: %w", err)
	}

	agentIDs := make(map[string]bool)

	for name := range config.Models {
		model := config.Models[name]
		if model.MaxTokensPerMinute <= 0 {
			return fmt.Errorf("model %s: max_tokens_per_minute must be positive", name)
		}
		if model.MaxBudgetPerDayUSD < 0 {
			return fmt.Errorf("model %s: max_budget_per_day_usd cannot be negative", name)
		}
		if model.CpmTokensIn < 0 {
			return fmt.Errorf("model %s: cpm_tokens_in cannot be negative", name)
		}
		if model.CpmTokensOut < 0 {
			return fmt.Errorf("model %s: cpm_tokens_out cannot be negative", name)
		}
		if model.APIKey == "" {
			return fmt.Errorf("model %s: api_key is required", name)
		}
		if len(model.Agents) == 0 {
			return fmt.Errorf("model %s: at least one agent must be configured", name)
		}

		// Set defaults for context management if not specified.
		if model.MaxContextTokens <= 0 {
			// Default context sizes based on common model limits.
			if strings.Contains(strings.ToLower(name), "claude") {
				model.MaxContextTokens = 200000 // Claude 3.5 Sonnet context limit
			} else if strings.Contains(strings.ToLower(name), "gpt") || strings.Contains(strings.ToLower(name), "o3") {
				model.MaxContextTokens = 128000 // GPT-4 Turbo / o3 context limit
			} else {
				model.MaxContextTokens = 32000 // Conservative default
			}
		}

		if model.MaxReplyTokens <= 0 {
			// Default reply limits.
			if strings.Contains(strings.ToLower(name), "claude") {
				model.MaxReplyTokens = 8192 // Claude max output tokens
			} else {
				model.MaxReplyTokens = 4096 // Conservative default
			}
		}

		if model.CompactionBuffer <= 0 {
			model.CompactionBuffer = 2000 // Default buffer before compaction
		}

		// Validate context management settings.
		if model.MaxReplyTokens >= model.MaxContextTokens {
			return fmt.Errorf("model %s: max_reply_tokens (%d) must be less than max_context_tokens (%d)",
				name, model.MaxReplyTokens, model.MaxContextTokens)
		}

		if model.CompactionBuffer >= model.MaxContextTokens/2 {
			return fmt.Errorf("model %s: compaction_buffer (%d) should be much smaller than max_context_tokens (%d)",
				name, model.CompactionBuffer, model.MaxContextTokens)
		}

		// Update the model in the config with defaults.
		config.Models[name] = model

		// Validate each agent.
		for i := range model.Agents {
			agent := &model.Agents[i]
			if agent.Name == "" {
				return fmt.Errorf("model %s, agent %d: name is required", name, i)
			}
			if agent.ID == "" {
				return fmt.Errorf("model %s, agent %s: id is required", name, agent.Name)
			}
			if agent.Type != AgentTypeArchitect && agent.Type != AgentTypeCoder {
				return fmt.Errorf("model %s, agent %s: type must be '%s' or '%s', got '%s'", name, agent.Name, AgentTypeArchitect, AgentTypeCoder, agent.Type)
			}
			if agent.WorkDir == "" {
				return fmt.Errorf("model %s, agent %s: workdir is required", name, agent.Name)
			}

			// Set default iteration budgets for coder agents.
			if agent.Type == AgentTypeCoder {
				if agent.IterationBudgets.CodingBudget <= 0 {
					agent.IterationBudgets.CodingBudget = DefaultCodingBudget
				}
				if agent.IterationBudgets.FixingBudget <= 0 {
					agent.IterationBudgets.FixingBudget = DefaultFixingBudget
				}
				// Update the agent in the slice with defaults.
				model.Agents[i] = *agent
			}

			// Check for duplicate agent IDs across all models using new format.
			logID := agent.GetLogID(name)
			if agentIDs[logID] {
				return fmt.Errorf("duplicate agent ID: %s (model %s, agent %s)", logID, name, agent.Name)
			}
			agentIDs[logID] = true
		}
	}

	if config.GracefulShutdownTimeoutSec <= 0 {
		config.GracefulShutdownTimeoutSec = 30 // default
	}
	if config.EventLogRotationHours <= 0 {
		config.EventLogRotationHours = 24 // default
	}
	if config.MaxRetryAttempts <= 0 {
		config.MaxRetryAttempts = 3 // default
	}
	if config.RetryBackoffMultiplier <= 0 {
		config.RetryBackoffMultiplier = 2.0 // default
	}
	if config.StoryChannelFactor <= 0 {
		config.StoryChannelFactor = 16 // increased from 8 to reduce backlog warnings
	}
	if config.QuestionsChannelSize <= 0 {
		config.QuestionsChannelSize = config.CountCoders() // default to number of coders
	}

	// Set Git worktree defaults.
	if config.BaseBranch == "" {
		config.BaseBranch = "main"
	}
	if config.MirrorDir == "" {
		config.MirrorDir = ".mirrors" // relative to WORKDIR
	}
	if config.WorktreePattern == "" {
		config.WorktreePattern = "{AGENT_ID}/{STORY_ID}" // relative to WORKDIR
	}
	if config.BranchPattern == "" {
		config.BranchPattern = "story-{STORY_ID}"
	}

	// Validate Git settings (RepoURL is optional - may not be using Git worktrees yet).
	if config.RepoURL != "" {
		// Basic URL validation.
		if !strings.HasPrefix(config.RepoURL, "git@") && !strings.HasPrefix(config.RepoURL, "https://") {
			return fmt.Errorf("repo_url must start with 'git@' or 'https://'")
		}
	}

	return nil
}

// validateExecutorConfig validates the executor configuration.
func validateExecutorConfig(executor *ExecutorConfig) error {
	// Validate executor type.
	validTypes := []string{"auto", "docker", "local"}
	found := false
	for _, t := range validTypes {
		if executor.Type == t {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("invalid executor type '%s', must be one of: %s", executor.Type, strings.Join(validTypes, ", "))
	}

	// Validate fallback executor.
	if executor.Fallback != "" {
		validFallbacks := []string{"docker", "local"}
		found = false
		for _, t := range validFallbacks {
			if executor.Fallback == t {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("invalid fallback executor '%s', must be one of: %s", executor.Fallback, strings.Join(validFallbacks, ", "))
		}
	}

	// Validate Docker configuration.
	if executor.Docker.Image == "" {
		return fmt.Errorf("docker.image is required")
	}

	if executor.Docker.Network != "" {
		validNetworks := []string{"none", "bridge", "host"}
		found = false
		for _, n := range validNetworks {
			if executor.Docker.Network == n {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("invalid docker network '%s', must be one of: %s", executor.Docker.Network, strings.Join(validNetworks, ", "))
		}
	}

	// Validate resource limits.
	if executor.Docker.PIDs < 0 {
		return fmt.Errorf("docker.pids cannot be negative")
	}
	if executor.Docker.PullTimeout < 0 {
		return fmt.Errorf("docker.pull_timeout cannot be negative")
	}

	return nil
}

// GetLogID returns the log ID for an agent (agentType-id format).
func (a *Agent) GetLogID(_ string) string {
	return fmt.Sprintf("%s-%s", a.Type, a.ID)
}

// GetAllAgents returns all agents from all models.
func (c *Config) GetAllAgents() []AgentWithModel {
	var agents []AgentWithModel
	for modelName := range c.Models {
		model := c.Models[modelName]
		for i := range model.Agents {
			agent := &model.Agents[i]
			agents = append(agents, AgentWithModel{
				Agent:     *agent,
				ModelName: modelName,
				Model:     model,
			})
		}
	}
	return agents
}

// GetAgentByLogID finds an agent by its log ID (type-id format).
func (c *Config) GetAgentByLogID(logID string) (*AgentWithModel, error) {
	parts := strings.Split(logID, "-")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid log ID format: %s (expected type-id)", logID)
	}

	agentType, agentID := parts[0], parts[1]

	// Search through all models to find the agent with matching type and ID.
	for modelName := range c.Models {
		model := c.Models[modelName]
		for i := range model.Agents {
			agent := &model.Agents[i]
			if agent.Type == agentType && agent.ID == agentID {
				return &AgentWithModel{
					Agent:     *agent,
					ModelName: modelName,
					Model:     model,
				}, nil
			}
		}
	}

	return nil, fmt.Errorf("agent not found: %s", logID)
}

// AgentWithModel combines an agent with its model information.
type AgentWithModel struct {
	Agent     Agent
	ModelName string
	Model     ModelCfg
}

// CountCoders returns the total number of coder agents across all models.
func (c *Config) CountCoders() int {
	count := 0
	for modelName := range c.Models {
		model := c.Models[modelName]
		for i := range model.Agents {
			agent := &model.Agents[i]
			if agent.Type == AgentTypeCoder {
				count++
			}
		}
	}
	return count
}

// CountArchitects returns the total number of architect agents across all models.
func (c *Config) CountArchitects() int {
	count := 0
	for modelName := range c.Models {
		model := c.Models[modelName]
		for i := range model.Agents {
			agent := &model.Agents[i]
			if agent.Type == AgentTypeArchitect {
				count++
			}
		}
	}
	return count
}

// GetBuildCommand returns the command for the specified build target.
func (p *ProjectConfig) GetBuildCommand(target string) string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	switch target {
	case "build":
		return p.Build.Build
	case "test":
		return p.Build.Test
	case "lint":
		return p.Build.Lint
	case "run":
		return p.Build.Run
	case "clean":
		return p.Build.Clean
	case "install":
		return p.Build.Install
	default:
		return ""
	}
}

// SetContainerNeedsRebuild marks the container as needing rebuild.
func (p *ProjectConfig) SetContainerNeedsRebuild() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Container.NeedsRebuild = true
}

// ContainerNeedsRebuild checks if the container needs rebuilding based on Dockerfile changes.
func (p *ProjectConfig) ContainerNeedsRebuild() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.Container.Dockerfile == "" {
		return false // Using pre-built image
	}

	// Check if Dockerfile exists and get its hash
	if _, err := os.Stat(p.Container.Dockerfile); err != nil {
		return true // Dockerfile missing, needs rebuild
	}

	currentHash, err := calculateDockerfileHash(p.Container.Dockerfile)
	if err != nil {
		return true // Error calculating hash, assume rebuild needed
	}

	return currentHash != p.Container.DockerfileHash
}

// LoadProjectConfig loads project configuration from .maestro/config.json.
func LoadProjectConfig(projectRoot string) (*ProjectConfig, error) {
	configPath := filepath.Join(projectRoot, ProjectConfigDir, ProjectConfigFilename)

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read project config file: %w", err)
	}

	var config ProjectConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse project config JSON: %w", err)
	}

	return &config, nil
}

// Save writes the project configuration to .maestro/config.json.
func (p *ProjectConfig) Save(projectRoot string) error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	configDir := filepath.Join(projectRoot, ProjectConfigDir)
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	configPath := filepath.Join(configDir, ProjectConfigFilename)
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal project config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write project config file: %w", err)
	}

	return nil
}

// LoadUserConfig loads user-level defaults from ~/.maestro/config.json.
func LoadUserConfig() (*UserConfig, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get user home directory: %w", err)
	}

	configPath := filepath.Join(homeDir, UserConfigDir, ProjectConfigFilename)

	// User config is optional
	if _, statErr := os.Stat(configPath); os.IsNotExist(statErr) {
		return &UserConfig{
			SchemaVersion: SchemaVersion,
		}, nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read user config file: %w", err)
	}

	var config UserConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse user config JSON: %w", err)
	}

	return &config, nil
}

// calculateDockerfileHash computes MD5 hash of a Dockerfile.
func calculateDockerfileHash(dockerfilePath string) (string, error) {
	file, err := os.Open(dockerfilePath)
	if err != nil {
		return "", fmt.Errorf("failed to open dockerfile: %w", err)
	}
	defer func() { _ = file.Close() }()

	hash := md5.New() //nolint:gosec // MD5 is acceptable for non-cryptographic file change detection
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("failed to read dockerfile: %w", err)
	}

	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

// LoadProjectConfigFromPath loads project configuration from a specific file path.
func LoadProjectConfigFromPath(configPath string) (*ProjectConfig, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read project config file: %w", err)
	}

	var config ProjectConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse project config JSON: %w", err)
	}

	return &config, nil
}
