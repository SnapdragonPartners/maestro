package config

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"
)

// Default iteration budgets for coder agents
const (
	DefaultCodingBudget = 8 // Default from existing hardcoded value
	DefaultFixingBudget = 3 // Default from story requirements
)

type IterationBudgets struct {
	CodingBudget int `json:"coding_budget"`
	FixingBudget int `json:"fixing_budget"`
}

type Agent struct {
	Name             string           `json:"name"`
	ID               string           `json:"id"`
	Type             string           `json:"type"` // "architect" or "coder"
	WorkDir          string           `json:"workdir"`
	IterationBudgets IterationBudgets `json:"iteration_budgets"`
}

type ModelCfg struct {
	MaxTokensPerMinute int     `json:"max_tokens_per_minute"`
	MaxBudgetPerDayUSD float64 `json:"max_budget_per_day_usd"`
	CpmTokensIn        float64 `json:"cpm_tokens_in"`
	CpmTokensOut       float64 `json:"cpm_tokens_out"`
	APIKey             string  `json:"api_key"`
	Agents             []Agent `json:"agents"`
	// Context management settings
	MaxContextTokens int `json:"max_context_tokens"` // Maximum total context size
	MaxReplyTokens   int `json:"max_reply_tokens"`   // Maximum tokens for model reply
	CompactionBuffer int `json:"compaction_buffer"`  // Buffer tokens before compaction
}

type Config struct {
	Models                     map[string]ModelCfg `json:"models"`
	GracefulShutdownTimeoutSec int                 `json:"graceful_shutdown_timeout_sec"`
	EventLogRotationHours      int                 `json:"event_log_rotation_hours"`
	MaxRetryAttempts           int                 `json:"max_retry_attempts"`
	RetryBackoffMultiplier     float64             `json:"retry_backoff_multiplier"`
	StoryChannelFactor         int                 `json:"story_channel_factor"`   // Buffer factor for storyCh: factor × numCoders
	QuestionsChannelSize       int                 `json:"questions_channel_size"` // Buffer size for questionsCh
	// Git worktree settings
	RepoURL          string `json:"repo_url"`           // Git repository URL for SSH clone/push
	BaseBranch       string `json:"base_branch"`        // Base branch name (default: main)
	MirrorDir        string `json:"mirror_dir"`         // Mirror directory path (default: $WORKDIR/.mirrors)
	WorktreePattern  string `json:"worktree_pattern"`   // Worktree path pattern (default: {$WORKDIR}/{AGENT_ID}/{STORY_ID})
	BranchPattern    string `json:"branch_pattern"`     // Branch name pattern (default: story-{STORY_ID})
}

var envVarRegex = regexp.MustCompile(`\$\{([^}]+)\}`)

func LoadConfig(configPath string) (*Config, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Replace environment variable placeholders
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

	// Apply environment variable overrides
	applyEnvOverrides(&config)

	// Validate config
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
			// Handle map fields like Models
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
	return result, err
}

func parseFloat(s string) (float64, error) {
	var result float64
	_, err := fmt.Sscanf(s, "%f", &result)
	return result, err
}

func validateConfig(config *Config) error {
	if len(config.Models) == 0 {
		return fmt.Errorf("no models configured")
	}

	agentIDs := make(map[string]bool)

	for name, model := range config.Models {
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

		// Set defaults for context management if not specified
		if model.MaxContextTokens <= 0 {
			// Default context sizes based on common model limits
			if strings.Contains(strings.ToLower(name), "claude") {
				model.MaxContextTokens = 200000 // Claude 3.5 Sonnet context limit
			} else if strings.Contains(strings.ToLower(name), "gpt") || strings.Contains(strings.ToLower(name), "o3") {
				model.MaxContextTokens = 128000 // GPT-4 Turbo / o3 context limit
			} else {
				model.MaxContextTokens = 32000 // Conservative default
			}
		}

		if model.MaxReplyTokens <= 0 {
			// Default reply limits
			if strings.Contains(strings.ToLower(name), "claude") {
				model.MaxReplyTokens = 8192 // Claude max output tokens
			} else {
				model.MaxReplyTokens = 4096 // Conservative default
			}
		}

		if model.CompactionBuffer <= 0 {
			model.CompactionBuffer = 2000 // Default buffer before compaction
		}

		// Validate context management settings
		if model.MaxReplyTokens >= model.MaxContextTokens {
			return fmt.Errorf("model %s: max_reply_tokens (%d) must be less than max_context_tokens (%d)",
				name, model.MaxReplyTokens, model.MaxContextTokens)
		}

		if model.CompactionBuffer >= model.MaxContextTokens/2 {
			return fmt.Errorf("model %s: compaction_buffer (%d) should be much smaller than max_context_tokens (%d)",
				name, model.CompactionBuffer, model.MaxContextTokens)
		}

		// Update the model in the config with defaults
		config.Models[name] = model

		// Validate each agent
		for i, agent := range model.Agents {
			if agent.Name == "" {
				return fmt.Errorf("model %s, agent %d: name is required", name, i)
			}
			if agent.ID == "" {
				return fmt.Errorf("model %s, agent %s: id is required", name, agent.Name)
			}
			if agent.Type != "architect" && agent.Type != "coder" {
				return fmt.Errorf("model %s, agent %s: type must be 'architect' or 'coder', got '%s'", name, agent.Name, agent.Type)
			}
			if agent.WorkDir == "" {
				return fmt.Errorf("model %s, agent %s: workdir is required", name, agent.Name)
			}

			// Set default iteration budgets for coder agents
			if agent.Type == "coder" {
				if agent.IterationBudgets.CodingBudget <= 0 {
					agent.IterationBudgets.CodingBudget = DefaultCodingBudget
				}
				if agent.IterationBudgets.FixingBudget <= 0 {
					agent.IterationBudgets.FixingBudget = DefaultFixingBudget
				}
				// Update the agent in the slice with defaults
				model.Agents[i] = agent
			}

			// Check for duplicate agent IDs across all models
			logID := fmt.Sprintf("%s:%s", name, agent.ID)
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
		config.StoryChannelFactor = 8 // default factor as per S-5
	}
	if config.QuestionsChannelSize <= 0 {
		config.QuestionsChannelSize = config.CountCoders() // default to number of coders
	}

	// Set Git worktree defaults
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

	// Validate Git settings (RepoURL is optional - may not be using Git worktrees yet)
	if config.RepoURL != "" {
		// Basic URL validation
		if !strings.HasPrefix(config.RepoURL, "git@") && !strings.HasPrefix(config.RepoURL, "https://") {
			return fmt.Errorf("repo_url must start with 'git@' or 'https://'")
		}
	}

	return nil
}

// GetLogID returns the log ID for an agent (model:id format)
func (a *Agent) GetLogID(modelName string) string {
	return fmt.Sprintf("%s:%s", modelName, a.ID)
}

// GetAllAgents returns all agents from all models
func (c *Config) GetAllAgents() []AgentWithModel {
	var agents []AgentWithModel
	for modelName, model := range c.Models {
		for _, agent := range model.Agents {
			agents = append(agents, AgentWithModel{
				Agent:     agent,
				ModelName: modelName,
				Model:     model,
			})
		}
	}
	return agents
}

// GetAgentByLogID finds an agent by its log ID (model:id)
func (c *Config) GetAgentByLogID(logID string) (*AgentWithModel, error) {
	parts := strings.Split(logID, ":")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid log ID format: %s (expected model:id)", logID)
	}

	modelName, agentID := parts[0], parts[1]
	model, exists := c.Models[modelName]
	if !exists {
		return nil, fmt.Errorf("model not found: %s", modelName)
	}

	for _, agent := range model.Agents {
		if agent.ID == agentID {
			return &AgentWithModel{
				Agent:     agent,
				ModelName: modelName,
				Model:     model,
			}, nil
		}
	}

	return nil, fmt.Errorf("agent not found: %s", logID)
}

// AgentWithModel combines an agent with its model information
type AgentWithModel struct {
	Agent     Agent
	ModelName string
	Model     ModelCfg
}

// CountCoders returns the total number of coder agents across all models
func (c *Config) CountCoders() int {
	count := 0
	for _, model := range c.Models {
		for _, agent := range model.Agents {
			if agent.Type == "coder" {
				count++
			}
		}
	}
	return count
}

// CountArchitects returns the total number of architect agents across all models
func (c *Config) CountArchitects() int {
	count := 0
	for _, model := range c.Models {
		for _, agent := range model.Agents {
			if agent.Type == "architect" {
				count++
			}
		}
	}
	return count
}
