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

	"orchestrator/pkg/logx"
)

// Global config instance with mutex protection.
// projectDir is set once during LoadConfig and never changes - it defines where all
// maestro files are stored relative to the project root.
//
//nolint:gochecknoglobals // Intentional singleton pattern for config management
var (
	config     *Config
	projectDir string       // Immutable after LoadConfig - set once at startup
	logger     *logx.Logger // Package logger for config operations
	mu         sync.RWMutex
)

// getLogger returns the config logger, initializing it if needed.
func getLogger() *logx.Logger {
	if logger == nil {
		logger = logx.NewLogger("config")
	}
	return logger
}

// LogInfo logs an info message using the config logger.
// This is exposed for other packages (like main) to use consistent logging.
func LogInfo(format string, args ...interface{}) {
	getLogger().Info(format, args...)
}

// ModelInfo contains static information about a known LLM model.
// This data is hardcoded in the application, not user-configurable.
type ModelInfo struct {
	Provider         string  // API provider (anthropic, openai)
	InputCPM         float64 // Cost per million input tokens (USD)
	OutputCPM        float64 // Cost per million output tokens (USD)
	MaxContextTokens int     // Maximum context window size in tokens
	MaxOutputTokens  int     // Maximum output tokens per request
}

// KnownModels registry contains pricing and provider information for common models.
// This is optional - unknown models will be inferred via ProviderPatterns.
//
//nolint:gochecknoglobals // Intentional global for static model registry
var KnownModels = map[string]ModelInfo{
	// Claude models (Anthropic)
	"claude-3-7-sonnet-20250219": {
		Provider:         ProviderAnthropic,
		InputCPM:         3.0,
		OutputCPM:        15.0,
		MaxContextTokens: 200000,
		MaxOutputTokens:  8192,
	},
	"claude-sonnet-4-5": {
		Provider:         ProviderAnthropic,
		InputCPM:         3.0,
		OutputCPM:        15.0,
		MaxContextTokens: 200000,
		MaxOutputTokens:  8192,
	},
	"claude-sonnet-4-20250514": {
		Provider:         ProviderAnthropic,
		InputCPM:         3.0,
		OutputCPM:        15.0,
		MaxContextTokens: 200000,
		MaxOutputTokens:  8192,
	},
	"claude-opus-4-1": {
		Provider:         ProviderAnthropic,
		InputCPM:         15.0,
		OutputCPM:        75.0,
		MaxContextTokens: 200000,
		MaxOutputTokens:  16384,
	},
	"claude-opus-4-1-20250805": {
		Provider:         ProviderAnthropic,
		InputCPM:         15.0,
		OutputCPM:        75.0,
		MaxContextTokens: 200000,
		MaxOutputTokens:  16384,
	},
	"claude-opus-4-5": {
		Provider:         ProviderAnthropic,
		InputCPM:         15.0,
		OutputCPM:        75.0,
		MaxContextTokens: 200000,
		MaxOutputTokens:  16384,
	},
	"claude-opus-4-5-20250514": {
		Provider:         ProviderAnthropic,
		InputCPM:         15.0,
		OutputCPM:        75.0,
		MaxContextTokens: 200000,
		MaxOutputTokens:  16384,
	},

	// OpenAI GPT models
	"gpt-4o": {
		Provider:         ProviderOpenAI,
		InputCPM:         2.5,
		OutputCPM:        10.0,
		MaxContextTokens: 128000,
		MaxOutputTokens:  4096,
	},

	// OpenAI o3 models
	"o3-mini": {
		Provider:         ProviderOpenAI,
		InputCPM:         1.1,
		OutputCPM:        4.4,
		MaxContextTokens: 128000,
		MaxOutputTokens:  16384,
	},
	"o3": {
		Provider:         ProviderOpenAI,
		InputCPM:         1.1,
		OutputCPM:        4.4,
		MaxContextTokens: 128000,
		MaxOutputTokens:  16384,
	},

	// OpenAI o4 models
	"o4-mini": {
		Provider:         ProviderOpenAI,
		InputCPM:         1.1,
		OutputCPM:        4.4,
		MaxContextTokens: 128000,
		MaxOutputTokens:  16384,
	},

	// GPT-5 (premium pricing)
	"gpt-5": {
		Provider:         ProviderOpenAI,
		InputCPM:         20.0,
		OutputCPM:        60.0,
		MaxContextTokens: 128000,
		MaxOutputTokens:  4096,
	},

	// Google Gemini models
	"gemini-2.0-flash": {
		Provider:         ProviderGoogle,
		InputCPM:         0.10,
		OutputCPM:        0.40,
		MaxContextTokens: 1048576,
		MaxOutputTokens:  8192,
	},
	"gemini-2.5-flash": {
		Provider:         ProviderGoogle,
		InputCPM:         0.30,
		OutputCPM:        2.50,
		MaxContextTokens: 1048576,
		MaxOutputTokens:  65536,
	},
	"gemini-3-pro-preview": {
		Provider:         ProviderGoogle,
		InputCPM:         2.0,
		OutputCPM:        12.0,
		MaxContextTokens: 1048576,
		MaxOutputTokens:  65536,
	},
}

// ProviderPattern represents a pattern for inferring provider from model name.
type ProviderPattern struct {
	Prefix   string
	Provider string
}

// ProviderPatterns defines rules for inferring providers from unknown model names.
// Allows using new models without code changes.
//
//nolint:gochecknoglobals // Intentional global for inference rules
var ProviderPatterns = []ProviderPattern{
	{"claude", ProviderAnthropic},
	{"gpt", ProviderOpenAI},
	{"o1", ProviderOpenAI},
	{"o3", ProviderOpenAI},
	{"o4", ProviderOpenAI},
	{"gemini", ProviderGoogle},
	// Ollama models - common open-source model prefixes
	{"phi", ProviderOllama},
	{"llama", ProviderOllama},
	{"qwen", ProviderOllama},
	{"mistral", ProviderOllama},
	{"codellama", ProviderOllama},
	{"deepseek", ProviderOllama},
	{"ollama:", ProviderOllama}, // Explicit prefix like "ollama:phi4"
}

// GetModelProvider returns the API provider for a given model.
// First checks KnownModels, then tries pattern matching.
// Returns error if model cannot be mapped to a provider (FATAL).
func GetModelProvider(modelName string) (string, error) {
	// Check known models first
	if info, exists := KnownModels[modelName]; exists {
		return info.Provider, nil
	}

	// Try pattern matching for unknown models
	for i := range ProviderPatterns {
		if strings.HasPrefix(modelName, ProviderPatterns[i].Prefix) {
			return ProviderPatterns[i].Provider, nil
		}
	}

	// FATAL: Cannot proceed without valid provider
	return "", fmt.Errorf("unknown model '%s': no known provider mapping or pattern match - cannot determine API provider", modelName)
}

// GetModelInfo returns the ModelInfo for a given model name.
// Returns the info and true if found in KnownModels, or a default info with inferred provider and false if not found.
func GetModelInfo(modelName string) (ModelInfo, bool) {
	// Check known models first
	if info, exists := KnownModels[modelName]; exists {
		return info, true
	}

	// Try to infer provider for unknown models
	provider := ""
	for i := range ProviderPatterns {
		if strings.HasPrefix(modelName, ProviderPatterns[i].Prefix) {
			provider = ProviderPatterns[i].Provider
			break
		}
	}

	// Return default info with inferred provider (or empty if no pattern matched)
	// Use conservative defaults for unknown models
	return ModelInfo{
		Provider:         provider,
		InputCPM:         0.0,   // No cost tracking for unknown models
		OutputCPM:        0.0,   // No cost tracking for unknown models
		MaxContextTokens: 32000, // Conservative default
		MaxOutputTokens:  4096,  // Conservative default
	}, false
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
// These are user-configurable values that can be overridden in config.json.
type ProviderLimits struct {
	TokensPerMinute int `json:"tokens_per_minute"` // Rate limit in tokens per minute
	MaxConcurrency  int `json:"max_concurrency"`   // Maximum concurrent requests
}

// RateLimitConfig defines rate limiting configuration grouped by API provider.
type RateLimitConfig struct {
	Anthropic ProviderLimits `json:"anthropic"` // Rate limits for Anthropic models
	OpenAI    ProviderLimits `json:"openai"`    // Rate limits for OpenAI models
	Google    ProviderLimits `json:"google"`    // Rate limits for Google models
	Ollama    ProviderLimits `json:"ollama"`    // Rate limits for Ollama models (local inference)
}

// ProviderDefaults defines default rate limits for each provider.
// These are used when rate limits are not specified in config.json.
//
//nolint:gochecknoglobals // Intentional global for provider defaults
var ProviderDefaults = map[string]ProviderLimits{
	ProviderAnthropic: {
		TokensPerMinute: 300000,
		MaxConcurrency:  5,
	},
	ProviderOpenAI: {
		TokensPerMinute: 150000,
		MaxConcurrency:  5,
	},
	ProviderGoogle: {
		TokensPerMinute: 1200000, // Must be > MaxContextTokens/0.9 for Gemini models (1M context)
		MaxConcurrency:  5,
	},
	ProviderOllama: {
		TokensPerMinute: 1000000, // Effectively unlimited for local inference
		MaxConcurrency:  2,       // Limited by GPU memory - users can increase if they have more VRAM
	},
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
	Enabled       bool   `json:"enabled"`        // Whether metrics collection is enabled
	Exporter      string `json:"exporter"`       // Metrics exporter type ("prometheus", "noop")
	Namespace     string `json:"namespace"`      // Metrics namespace for grouping
	PrometheusURL string `json:"prometheus_url"` // Prometheus server URL for querying metrics
}

// DebugConfig defines configuration for debug logging.
type DebugConfig struct {
	LLMMessages bool `json:"llm_messages"` // Enable debug logging for LLM message formatting (default: false)
}

// AirplaneAgentConfig defines model overrides for airplane (offline) mode.
// When operating in airplane mode, these models are used instead of the standard cloud models.
// All models should be Ollama-compatible (e.g., "qwen2.5-coder:32b" or "mistral-nemo:latest").
type AirplaneAgentConfig struct {
	CoderModel     string `json:"coder_model,omitempty"`     // Ollama model for coder agents
	ArchitectModel string `json:"architect_model,omitempty"` // Ollama model for architect agent
	PMModel        string `json:"pm_model,omitempty"`        // Ollama model for PM agent
}

// AgentConfig defines which models to use and concurrency limits.
type AgentConfig struct {
	MaxCoders      int              `json:"max_coders"`      // Maximum concurrent coder agents
	CoderModel     string           `json:"coder_model"`     // Model name for coder agents (mapped to provider via KnownModels)
	CoderMode      string           `json:"coder_mode"`      // Coder execution mode: "standard" (default) or "claude-code"
	ArchitectModel string           `json:"architect_model"` // Model name for architect agent (mapped to provider via KnownModels)
	PMModel        string           `json:"pm_model"`        // Model name for PM agent (mapped to provider via KnownModels)
	Metrics        MetricsConfig    `json:"metrics"`         // Metrics collection configuration
	Resilience     ResilienceConfig `json:"resilience"`      // Resilience middleware configuration
	StateTimeout   time.Duration    `json:"state_timeout"`   // Global timeout for any state processing

	// Airplane mode model overrides
	Airplane *AirplaneAgentConfig `json:"airplane,omitempty"` // Model overrides for airplane (offline) mode
}

// All constants bundled together for easy maintenance.
const (
	// System behavior constants - these control orchestrator behavior and should not be user-configurable.

	// Default model for airplane mode - a capable local model available via Ollama.
	DefaultAirplaneModel = "mistral-nemo:latest"

	// Shutdown and retry behavior.
	GracefulShutdownTimeoutSec = 30  // How long to wait for graceful shutdown before force-kill
	MaxRetryAttempts           = 3   // Maximum number of retry attempts for failed operations
	RetryBackoffMultiplier     = 2.0 // Exponential backoff multiplier for retries

	// Channel sizing for performance tuning.
	StoryChannelFactor   = 16 // Buffer factor for story channels: factor × numCoders
	QuestionsChannelSize = 2  // Buffer size for questions channel between agents

	// Docker container runtime defaults (applied when not specified in config).
	DefaultDockerNetwork = "bridge"    // Enable networking (required for git operations)
	DefaultTmpfsSize     = "1g"        // Temporary filesystem size for /tmp
	DefaultDockerCPUs    = "2"         // CPU limit for container execution
	DefaultDockerMemory  = "2g"        // Memory limit for container execution
	DefaultDockerPIDs    = int64(1024) // Process limit for container execution

	// Git repository defaults.
	DefaultTargetBranch  = "main"                         // Default target branch for pull requests
	DefaultMirrorDir     = ".mirrors"                     // Default directory for git mirrors
	DefaultBranchPattern = "story-{STORY_ID}"             // Default pattern for story branch names
	DefaultGitUserName   = "Maestro {AGENT_ID}"           // Default git commit author name
	DefaultGitUserEmail  = "maestro-{AGENT_ID}@localhost" // Default git commit author email

	// Default Docker images for different project types (used only for dockerfile mode fallbacks).
	DefaultGoDockerImage     = "golang:alpine" // Use latest stable Go with alpine
	DefaultUbuntuDockerImage = "ubuntu:22.04"

	// Platform constants.
	PlatformGo      = "go"
	PlatformPython  = "python"
	PlatformNode    = "node"
	PlatformDocker  = "docker"
	PlatformGeneric = "generic"

	// Build target constants - used for GetBuildCommand() and elsewhere.
	BuildTargetBuild   = "build"
	BuildTargetTest    = "test"
	BuildTargetLint    = "lint"
	BuildTargetRun     = "run"
	BuildTargetClean   = "clean"
	BuildTargetInstall = "install"

	// Model name constants.
	ModelClaudeSonnet4      = "claude-sonnet-4-5"
	ModelClaudeSonnet4Old   = "claude-sonnet-4-20250514"
	ModelClaudeSonnet3      = "claude-3-7-sonnet-20250219"
	ModelClaudeSonnetLatest = ModelClaudeSonnet4
	ModelClaudeOpus41       = "claude-opus-4-1"
	ModelClaudeOpus45       = "claude-opus-4-5"
	ModelClaudeOpusLatest   = ModelClaudeOpus45
	ModelOpenAIO3           = "o3"
	ModelGemini3Pro         = "gemini-3-pro-preview"

	// Container image constants.
	BootstrapContainerTag = "maestro-bootstrap:latest"
	ModelOpenAIO3Mini     = "o3-mini"
	ModelOpenAIO4Mini     = "o4-mini"
	ModelOpenAIO3Latest   = ModelOpenAIO3
	ModelGPT4o            = "gpt-4o"
	ModelGPT5             = "gpt-5"
	DefaultCoderModel     = ModelClaudeSonnet4
	DefaultArchitectModel = ModelGemini3Pro
	DefaultPMModel        = ModelClaudeOpus45

	// Coder execution mode constants.
	CoderModeStandard   = "standard"    // Default: use standard LLM-based coder agent
	CoderModeClaudeCode = "claude-code" // Use Claude Code subprocess for planning/coding

	// Operating mode constants (connectivity/deployment mode).
	// Note: This is distinct from "Operating Modes" (Bootstrap, Development, etc.) and "Coder Mode" (standard, claude-code).
	// This controls whether Maestro uses cloud APIs or local-only resources.
	OperatingModeStandard = "standard" // Default: use cloud APIs (GitHub, Anthropic, OpenAI, etc.)
	OperatingModeAirplane = "airplane" // Offline mode: use local Gitea + Ollama only

	// Project config constants.
	ProjectConfigFilename = "config.json"
	ProjectConfigDir      = ".maestro"
	DatabaseFilename      = "maestro.db"
	SchemaVersion         = "1.0"

	// Provider constants for middleware rate limiting.
	ProviderAnthropic = "anthropic"
	ProviderOpenAI    = "openai"
	ProviderGoogle    = "google"
	ProviderOllama    = "ollama"

	// API key environment variable names.
	EnvAnthropicAPIKey = "ANTHROPIC_API_KEY"
	EnvOpenAIAPIKey    = "OPENAI_API_KEY"
	EnvGoogleAPIKey    = "GOOGLE_GENAI_API_KEY"
	EnvOllamaHost      = "OLLAMA_HOST"
)

// GitConfig contains git repository settings for the project.
// All git-related configuration is bundled here to eliminate redundancy.
type GitConfig struct {
	RepoURL       string `json:"repo_url"`       // Git repository URL for SSH clone/push
	TargetBranch  string `json:"target_branch"`  // Target branch for pull requests (default: main)
	MirrorDir     string `json:"mirror_dir"`     // Mirror directory path (default: .mirrors)
	BranchPattern string `json:"branch_pattern"` // Branch name pattern (default: story-{STORY_ID})
	GitUserName   string `json:"git_user_name"`  // Git commit author name (default: Maestro {AGENT_ID})
	GitUserEmail  string `json:"git_user_email"` // Git commit author email (default: maestro-{AGENT_ID}@localhost)
}

// WebUIConfig contains web UI server settings.
type WebUIConfig struct {
	Enabled bool   `json:"enabled"` // Whether web UI is enabled (default: true)
	Host    string `json:"host"`    // Host to bind to (default: "localhost")
	Port    int    `json:"port"`    // Port to listen on (default: 8080, must be > 0 if enabled)
	SSL     bool   `json:"ssl"`     // Whether to use SSL/TLS (default: false)
	Cert    string `json:"cert"`    // Path to SSL certificate file (required if ssl=true)
	Key     string `json:"key"`     // Path to SSL private key file (required if ssl=true)
}

// ChatLimitsConfig contains size and compaction limits for chat messages.
type ChatLimitsConfig struct {
	MaxMessageChars int `json:"max_message_chars"` // Maximum message size (default: 4096)
}

// ChatScannerConfig contains secret scanning configuration.
type ChatScannerConfig struct {
	Enabled   bool `json:"enabled"`    // Whether secret scanning is enabled (default: true)
	TimeoutMs int  `json:"timeout_ms"` // Scanner timeout in milliseconds (default: 800)
}

// ChatConfig contains agent chat system configuration.
type ChatConfig struct {
	Enabled        bool              `json:"enabled"`          // Whether chat system is enabled (default: true)
	MaxNewMessages int               `json:"max_new_messages"` // Maximum new messages to inject per LLM call (default: 100)
	Limits         ChatLimitsConfig  `json:"limits"`           // Size and compaction limits
	Scanner        ChatScannerConfig `json:"scanner"`          // Secret scanning configuration
}

// SearchConfig defines web search tool configuration.
// Search is auto-enabled when API keys are detected, but can be explicitly disabled.
type SearchConfig struct {
	Enabled *bool `json:"enabled,omitempty"` // Whether web search is enabled (nil = auto-detect from API keys)
}

// LogsConfig contains log file management configuration.
type LogsConfig struct {
	RotationCount int `json:"rotation_count"` // Number of old log files to keep (default: 4)
}

// PMConfig defines PM agent configuration.
type PMConfig struct {
	Enabled           bool   `json:"enabled"`             // Whether PM agent is enabled (default: true)
	MaxInterviewTurns int    `json:"max_interview_turns"` // Maximum conversation turns before forcing submission (default: 20)
	DefaultExpertise  string `json:"default_expertise"`   // Default user expertise level: NON_TECHNICAL, BASIC, EXPERT (default: BASIC)
}

// DemoConfig defines demo mode configuration for running applications.
// Demo availability is controlled by PM (based on bootstrap status), not config.
type DemoConfig struct {
	// Container port settings (see docs/DEMO_CONTAINER_PORTS.md)
	ContainerPortOverride int        `json:"container_port_override,omitempty"` // Manual override for container port
	SelectedContainerPort int        `json:"selected_container_port,omitempty"` // User-selected or auto-detected main port
	DetectedPorts         []PortInfo `json:"detected_ports,omitempty"`          // All detected listeners from discovery
	LastAssignedHostPort  int        `json:"last_assigned_host_port,omitempty"` // Last Docker-assigned host port (informational)

	// Existing fields
	RunCmdOverride            string `json:"run_cmd_override"`            // Override Build.RunCmd for demo (optional)
	HealthcheckPath           string `json:"healthcheck_path"`            // HTTP path to check for readiness (default: /health)
	HealthcheckTimeoutSeconds int    `json:"healthcheck_timeout_seconds"` // Max wait time for app to become healthy (default: 60)
}

// PortInfo describes a detected listening port in a container.
type PortInfo struct {
	Port        int    `json:"port"`         // Container port number
	BindAddress string `json:"bind_address"` // "0.0.0.0", "127.0.0.1", etc.
	Protocol    string `json:"protocol"`     // "tcp", "udp"
	Exposed     bool   `json:"exposed"`      // Was in Dockerfile EXPOSE
	Reachable   bool   `json:"reachable"`    // Can be published (not loopback-bound)
}

// MaintenanceConfig defines automated maintenance mode settings.
// Maintenance mode runs periodic housekeeping tasks between specs to manage technical debt.
type MaintenanceConfig struct {
	Enabled       bool                   `json:"enabled"`        // Whether maintenance mode is enabled (default: true)
	AfterSpecs    int                    `json:"after_specs"`    // Number of specs before triggering maintenance (default: 1)
	Tasks         MaintenanceTasksConfig `json:"tasks"`          // Which maintenance tasks to run
	BranchCleanup BranchCleanupConfig    `json:"branch_cleanup"` // Branch cleanup configuration
	TodoScan      TodoScanConfig         `json:"todo_scan"`      // TODO/deprecated scan configuration
}

// MaintenanceTasksConfig defines which maintenance tasks are enabled.
type MaintenanceTasksConfig struct {
	BranchCleanup    bool `json:"branch_cleanup"`    // Clean up stale merged branches (default: true)
	KnowledgeSync    bool `json:"knowledge_sync"`    // Sync knowledge graph with codebase (default: true)
	DocsVerification bool `json:"docs_verification"` // Verify documentation accuracy (default: true)
	TodoScan         bool `json:"todo_scan"`         // Scan for TODOs and deprecated code (default: true)
	DeferredReview   bool `json:"deferred_review"`   // Review deferred knowledge nodes (default: true)
	TestCoverage     bool `json:"test_coverage"`     // Improve test coverage (default: true)
}

// BranchCleanupConfig defines branch cleanup settings.
type BranchCleanupConfig struct {
	ProtectedPatterns []string `json:"protected_patterns"` // Branch patterns to never delete (default: main, master, develop, release/*, hotfix/*)
}

// TodoScanConfig defines TODO/deprecated scanning settings.
type TodoScanConfig struct {
	Markers []string `json:"markers"` // Comment markers to scan for (default: TODO, FIXME, HACK, XXX, deprecated, DEPRECATED, @deprecated)
}

// Config represents the main configuration for the orchestrator system.
//
// IMPORTANT: This structure contains only user-configurable project settings.
// Model pricing, provider mappings, and other static data are hardcoded in KnownModels and ProviderDefaults.
//
// Schema versioning prevents breaking changes - increment SchemaVersion for any structural changes.
type Config struct {
	SchemaVersion string `json:"schema_version"` // MUST increment for breaking changes

	// === OPERATING MODE ===
	// DefaultMode controls connectivity/deployment: "standard" (cloud APIs) or "airplane" (local only).
	// Can be overridden at runtime with --airplane CLI flag.
	// Note: This is distinct from "Operating Modes" (Bootstrap, Development) and "Coder Mode" (standard, claude-code).
	DefaultMode string `json:"default_mode,omitempty"` // Default operating mode: "standard" or "airplane"

	// === PROJECT-SPECIFIC SETTINGS (per .maestro/config.json) ===
	Project     *ProjectInfo       `json:"project"`     // Basic project metadata (name, platform)
	Container   *ContainerConfig   `json:"container"`   // Container settings (NO build state/metadata)
	Build       *BuildConfig       `json:"build"`       // Build commands and targets
	Agents      *AgentConfig       `json:"agents"`      // Which models to use and rate limits for this project
	Git         *GitConfig         `json:"git"`         // Git repository and branching settings
	WebUI       *WebUIConfig       `json:"webui"`       // Web UI server settings
	Chat        *ChatConfig        `json:"chat"`        // Agent chat system settings
	Search      *SearchConfig      `json:"search"`      // Web search settings
	PM          *PMConfig          `json:"pm"`          // PM agent settings
	Logs        *LogsConfig        `json:"logs"`        // Log file management settings
	Debug       *DebugConfig       `json:"debug"`       // Debug settings
	Demo        *DemoConfig        `json:"demo"`        // Demo mode settings
	Maintenance *MaintenanceConfig `json:"maintenance"` // Automated maintenance mode settings

	// === RUNTIME-ONLY STATE (NOT PERSISTED) ===
	SessionID        string `json:"-"` // Current orchestrator session UUID (generated at startup or loaded for restarts)
	OperatingMode    string `json:"-"` // Resolved operating mode for this session (from CLI or DefaultMode)
	validTargetImage bool   `json:"-"` // Whether the configured target container is valid and runnable
}

// ProjectInfo contains basic project metadata.
// Only contains actual project configuration, not transient state or redundant data.
// Note: Project description is handled via MAESTRO.md file (not config).
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

	// Container orchestration (atomic switching)
	PinnedImageID string `json:"pinned_image_id,omitempty"` // Currently pinned Docker image ID (sha256:...)
	SafeImageID   string `json:"safe_image_id,omitempty"`   // Safe fallback Docker image ID (sha256:...)

	// Docker capabilities (detected at startup)
	BuildxAvailable bool `json:"-"` // Whether buildx is available on the host (transient, not persisted)

	// Docker runtime settings (command-line only, cannot be set in Dockerfile)
	Network   string `json:"network"`    // Docker --network setting
	TmpfsSize string `json:"tmpfs_size"` // Docker --tmpfs size setting
	CPUs      string `json:"cpus"`       // Docker --cpus limit
	Memory    string `json:"memory"`     // Docker --memory limit
	PIDs      int64  `json:"pids"`       // Docker --pids-limit setting
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
func GetProjectDir() string {
	mu.RLock()
	defer mu.RUnlock()
	return projectDir
}

// MustGetProjectDir returns the current project directory or panics if not initialized.
// Use this only in code paths where LoadConfig is guaranteed to have been called.
func MustGetProjectDir() string {
	mu.RLock()
	defer mu.RUnlock()
	if projectDir == "" {
		panic("config not initialized - call LoadConfig first")
	}
	return projectDir
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

// GetContainerTmpfsSize returns the tmpfs size for containers.
// Returns the configured size or the default if not set.
// Must call LoadConfig first to initialize config.
func GetContainerTmpfsSize() string {
	cfg, err := GetConfig()
	if err != nil {
		return DefaultTmpfsSize // Fallback to default if config not loaded
	}

	// Use configured tmpfs size or default
	if cfg.Container != nil && cfg.Container.TmpfsSize != "" {
		return cfg.Container.TmpfsSize
	}

	return DefaultTmpfsSize
}

// GetDebugLLMMessages returns whether debug logging for LLM message formatting is enabled.
// Returns false by default if config is not loaded or debug is not configured.
// Must call LoadConfig first to initialize config.
func GetDebugLLMMessages() bool {
	cfg, err := GetConfig()
	if err != nil {
		return false // Fallback to disabled if config not loaded
	}

	// Use configured debug setting or default to false
	if cfg.Debug != nil {
		return cfg.Debug.LLMMessages
	}

	return false
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

// SetConfigForTesting sets the global config for testing purposes.
// Pass nil to reset. This bypasses normal initialization and should only be used in tests.
func SetConfigForTesting(cfg *Config) {
	mu.Lock()
	defer mu.Unlock()
	config = cfg
	if cfg == nil {
		projectDir = ""
	}
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
		getLogger().Info("📝 Config file not found, creating new config at %s", configPath)
		config = createDefaultConfig()

		// Validate default config immediately (including API keys and tools)
		if err := validateConfig(config); err != nil {
			return fmt.Errorf("default config validation failed: %w", err)
		}

		if err := saveConfigLocked(); err != nil {
			return fmt.Errorf("failed to save initial config: %w", err)
		}
		getLogger().Info("✅ New config file created and validated")
		return nil
	}

	// File exists - try to load it
	getLogger().Info("📝 Loading config from %s", configPath)
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

	// Save config back to disk with applied defaults (ensures old configs get updated)
	if err := saveConfigLocked(); err != nil {
		return fmt.Errorf("failed to save config with applied defaults: %w", err)
	}

	// Validate target container and set runtime flag (testing with docker inspect only)
	config.validTargetImage = validateTargetContainer(config)

	getLogger().Info("✅ Config loaded and validated successfully")
	return nil
}

// UpdateAgents updates the agent configuration and persists to disk.
func UpdateAgents(_ string, agents *AgentConfig) error {
	mu.Lock()
	defer mu.Unlock()

	// Validate agent config by temporarily setting it and testing provider mappings
	oldAgents := config.Agents
	config.Agents = agents

	// Validate that both models can be mapped to providers
	if _, err := GetModelProvider(agents.CoderModel); err != nil {
		config.Agents = oldAgents // Restore old config
		return fmt.Errorf("invalid coder model: %w", err)
	}
	if _, err := GetModelProvider(agents.ArchitectModel); err != nil {
		config.Agents = oldAgents // Restore old config
		return fmt.Errorf("invalid architect model: %w", err)
	}

	// Validation passed, keep the new config (already set above)
	return saveConfigLocked()
}

// UpdateContainer updates the container configuration and persists to disk.
func UpdateContainer(container *ContainerConfig) error {
	mu.Lock()
	defer mu.Unlock()

	config.Container = container

	// Apply defaults to ensure all Docker runtime settings are populated
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

	// Validate target container and update runtime flag (testing with docker inspect only)
	config.validTargetImage = validateTargetContainer(config)

	return saveConfigLocked()
}

// UpdateBuild updates the build configuration and persists to disk.
func UpdateBuild(build *BuildConfig) error {
	mu.Lock()
	defer mu.Unlock()

	config.Build = build
	return saveConfigLocked()
}

// SetPinnedImageID atomically updates the pinned image ID and persists to disk.
func SetPinnedImageID(imageID string) error {
	mu.Lock()
	defer mu.Unlock()

	if config.Container == nil {
		return fmt.Errorf("container config not initialized")
	}

	config.Container.PinnedImageID = imageID
	return saveConfigLocked()
}

// GetPinnedImageID returns the currently pinned image ID.
func GetPinnedImageID() string {
	mu.RLock()
	defer mu.RUnlock()

	if config.Container == nil {
		return ""
	}

	return config.Container.PinnedImageID
}

// SetSafeImageID atomically updates the safe fallback image ID and persists to disk.
func SetSafeImageID(imageID string) error {
	mu.Lock()
	defer mu.Unlock()

	if config.Container == nil {
		return fmt.Errorf("container config not initialized")
	}

	config.Container.SafeImageID = imageID
	return saveConfigLocked()
}

// GetSafeImageID returns the safe fallback image ID.
func GetSafeImageID() string {
	mu.RLock()
	defer mu.RUnlock()

	if config.Container == nil {
		return ""
	}

	return config.Container.SafeImageID
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

	// Convert SSH URLs to HTTPS format for token-based authentication
	if git.RepoURL != "" {
		if convertedURL := convertSSHToHTTPS(git.RepoURL); convertedURL != git.RepoURL {
			logx.NewLogger("config").Info("Converting SSH URL to HTTPS: %s -> %s", git.RepoURL, convertedURL)
			git.RepoURL = convertedURL
		}
	}

	// Apply git user defaults if not provided
	if git.GitUserName == "" {
		git.GitUserName = DefaultGitUserName
	}
	if git.GitUserEmail == "" {
		git.GitUserEmail = DefaultGitUserEmail
	}

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
	return &Config{
		SchemaVersion: SchemaVersion,

		// Project-specific settings with defaults
		Project: &ProjectInfo{},
		Container: &ContainerConfig{
			// Apply Docker runtime defaults
			Name:      BootstrapContainerTag,
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
			MaxCoders:      3,
			CoderModel:     DefaultCoderModel,
			ArchitectModel: DefaultArchitectModel,
			PMModel:        DefaultPMModel,
			// Airplane mode defaults - use local Ollama models
			// Users should customize these based on their available models
			Airplane: &AirplaneAgentConfig{
				CoderModel:     DefaultAirplaneModel, // Good for coding tasks
				ArchitectModel: DefaultAirplaneModel, // Good for planning/architecture
				PMModel:        DefaultAirplaneModel, // Good for requirements gathering
			},
			Metrics: MetricsConfig{
				Enabled:       true,       // Enable metrics by default for development visibility
				Exporter:      "internal", // Use internal aggregation by default
				Namespace:     "maestro",
				PrometheusURL: "", // Not needed for internal metrics
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
						MaxConcurrency:  5,
					},
					OpenAI: ProviderLimits{
						TokensPerMinute: 150000,
						MaxConcurrency:  5,
					},
					Google: ProviderLimits{
						TokensPerMinute: 1200000, // Must be > MaxContextTokens/0.9 for Gemini models
						MaxConcurrency:  5,
					},
					Ollama: ProviderLimits{
						TokensPerMinute: 1000000,
						MaxConcurrency:  2,
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
			GitUserName:   DefaultGitUserName,
			GitUserEmail:  DefaultGitUserEmail,
		},
		WebUI: &WebUIConfig{
			Enabled: true,        // Enabled by default
			Host:    "localhost", // Secure default: bind to localhost only
			Port:    8080,        // Standard development port
			SSL:     false,       // SSL disabled by default (requires cert/key setup)
			Cert:    "",          // No default certificate
			Key:     "",          // No default key
		},
		Chat: &ChatConfig{
			Enabled:        true, // Enabled by default
			MaxNewMessages: 100,  // Inject up to 100 new messages per LLM call
			Limits: ChatLimitsConfig{
				MaxMessageChars: 4096, // 4KB message limit
			},
			Scanner: ChatScannerConfig{
				Enabled:   true, // Enable secret scanning by default
				TimeoutMs: 800,  // 800ms timeout for scanning
			},
		},
		PM: &PMConfig{
			Enabled:           true,    // Enabled by default
			MaxInterviewTurns: 20,      // Maximum 20 turns per interview
			DefaultExpertise:  "BASIC", // Default to BASIC expertise level
		},
		Logs: &LogsConfig{
			RotationCount: 4, // Keep last 4 log files
		},
		Debug: &DebugConfig{
			LLMMessages: false, // Disabled by default
		},
		Maintenance: &MaintenanceConfig{
			Enabled:    true, // Enabled by default
			AfterSpecs: 1,    // Trigger after every spec
			Tasks: MaintenanceTasksConfig{
				BranchCleanup:    true,
				KnowledgeSync:    true,
				DocsVerification: true,
				TodoScan:         true,
				DeferredReview:   true,
				TestCoverage:     true,
			},
			BranchCleanup: BranchCleanupConfig{
				ProtectedPatterns: []string{"main", "master", "develop", "release/*", "hotfix/*"},
			},
			TodoScan: TodoScanConfig{
				Markers: []string{"TODO", "FIXME", "HACK", "XXX", "deprecated", "DEPRECATED", "@deprecated"},
			},
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
func validateAgentConfigInternal(agents *AgentConfig, _ *Config) error {
	if agents.MaxCoders <= 0 {
		return fmt.Errorf("max_coders must be positive")
	}

	// Validate coder model can be mapped to a provider
	if _, err := GetModelProvider(agents.CoderModel); err != nil {
		return fmt.Errorf("coder_model '%s': %w", agents.CoderModel, err)
	}

	// Validate architect model can be mapped to a provider
	if _, err := GetModelProvider(agents.ArchitectModel); err != nil {
		return fmt.Errorf("architect_model '%s': %w", agents.ArchitectModel, err)
	}

	// Validate coder_mode is a valid value
	if agents.CoderMode != "" && agents.CoderMode != CoderModeStandard && agents.CoderMode != CoderModeClaudeCode {
		return fmt.Errorf("coder_mode must be '%s' or '%s', got '%s'", CoderModeStandard, CoderModeClaudeCode, agents.CoderMode)
	}

	// If using claude-code mode, validate the coder_model is an Anthropic model
	if agents.CoderMode == CoderModeClaudeCode {
		provider, _ := GetModelProvider(agents.CoderModel)
		if provider != ProviderAnthropic {
			return fmt.Errorf("coder_mode '%s' requires an Anthropic model for coder_model, got '%s' (provider: %s)",
				CoderModeClaudeCode, agents.CoderModel, provider)
		}
	}

	// No need to validate MaxConnections or TPM - those are removed from config
	// Rate limits are now per-provider, not per-model
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
	if config.WebUI == nil {
		config.WebUI = &WebUIConfig{}
	}
	if config.Chat == nil {
		config.Chat = &ChatConfig{}
	}
	if config.Search == nil {
		config.Search = &SearchConfig{}
	}
	if config.Logs == nil {
		config.Logs = &LogsConfig{}
	}
	if config.Demo == nil {
		config.Demo = &DemoConfig{}
	}
	if config.PM == nil {
		config.PM = &PMConfig{}
	}
	if config.Debug == nil {
		config.Debug = &DebugConfig{}
	}

	// Apply container defaults
	if config.Container.Name == "" {
		config.Container.Name = BootstrapContainerTag
	}
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
	if config.Git.GitUserName == "" {
		config.Git.GitUserName = DefaultGitUserName
	}
	if config.Git.GitUserEmail == "" {
		config.Git.GitUserEmail = DefaultGitUserEmail
	}

	// Convert SSH URLs to HTTPS format for token-based authentication
	// This handles both new configs and user manual edits
	if config.Git.RepoURL != "" {
		if convertedURL := convertSSHToHTTPS(config.Git.RepoURL); convertedURL != config.Git.RepoURL {
			logx.NewLogger("config").Info("Converting SSH URL to HTTPS: %s -> %s", config.Git.RepoURL, convertedURL)
			config.Git.RepoURL = convertedURL
		}
	}

	// Apply agent defaults
	if config.Agents.MaxCoders == 0 {
		config.Agents.MaxCoders = 3
	}
	if config.Agents.CoderModel == "" {
		config.Agents.CoderModel = DefaultCoderModel
	}
	if config.Agents.ArchitectModel == "" {
		config.Agents.ArchitectModel = DefaultArchitectModel
	}
	if config.Agents.PMModel == "" {
		config.Agents.PMModel = DefaultPMModel
	}
	if config.Agents.CoderMode == "" {
		config.Agents.CoderMode = CoderModeStandard
	}
	// Apply airplane agent defaults if section exists but models not set
	if config.Agents.Airplane == nil {
		config.Agents.Airplane = &AirplaneAgentConfig{
			CoderModel:     DefaultAirplaneModel,
			ArchitectModel: DefaultAirplaneModel,
			PMModel:        DefaultAirplaneModel,
		}
	} else {
		// Fill in missing airplane models with defaults
		if config.Agents.Airplane.CoderModel == "" {
			config.Agents.Airplane.CoderModel = DefaultAirplaneModel
		}
		if config.Agents.Airplane.ArchitectModel == "" {
			config.Agents.Airplane.ArchitectModel = DefaultAirplaneModel
		}
		if config.Agents.Airplane.PMModel == "" {
			config.Agents.Airplane.PMModel = DefaultAirplaneModel
		}
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

	// Apply rate limit defaults from ProviderDefaults
	if config.Agents.Resilience.RateLimit.Anthropic.TokensPerMinute == 0 {
		config.Agents.Resilience.RateLimit.Anthropic = ProviderDefaults[ProviderAnthropic]
	}
	if config.Agents.Resilience.RateLimit.OpenAI.TokensPerMinute == 0 {
		config.Agents.Resilience.RateLimit.OpenAI = ProviderDefaults[ProviderOpenAI]
	}
	if config.Agents.Resilience.RateLimit.Google.TokensPerMinute == 0 {
		config.Agents.Resilience.RateLimit.Google = ProviderDefaults[ProviderGoogle]
	}
	if config.Agents.Resilience.RateLimit.Ollama.TokensPerMinute == 0 {
		config.Agents.Resilience.RateLimit.Ollama = ProviderDefaults[ProviderOllama]
	}

	// Validate rate limits are sufficient for model context sizes
	validateRateLimitCapacity(config)

	if config.Agents.Resilience.Timeout == 0 {
		config.Agents.Resilience.Timeout = 3 * time.Minute // Increased for GPT-5 reasoning time (was 60s)
	}

	// Apply state timeout default
	if config.Agents.StateTimeout == 0 {
		config.Agents.StateTimeout = 10 * time.Minute
	}

	// Apply WebUI defaults
	if config.WebUI.Host == "" {
		config.WebUI.Host = "localhost"
	}
	if config.WebUI.Port == 0 {
		config.WebUI.Port = 8080
	}
	// Note: Enabled defaults to false (zero value), but we want true by default
	// This is handled in createDefaultConfig for new configs
	// For existing configs without webui section, we set enabled=true to maintain backward compatibility

	// Apply Chat defaults
	if config.Chat.MaxNewMessages == 0 {
		config.Chat.MaxNewMessages = 100
	}
	if config.Chat.Limits.MaxMessageChars == 0 {
		config.Chat.Limits.MaxMessageChars = 4096
	}
	if config.Chat.Scanner.TimeoutMs == 0 {
		config.Chat.Scanner.TimeoutMs = 800
	}
	// Note: chat.enabled defaults to false, scanner.enabled defaults to false
	// If user wants chat, they must explicitly enable it

	// Apply PM defaults
	if config.PM.MaxInterviewTurns == 0 {
		config.PM.MaxInterviewTurns = 20
	}
	if config.PM.DefaultExpertise == "" {
		config.PM.DefaultExpertise = "BASIC"
	}
	// Note: PM.Enabled defaults to false (zero value), but we want true by default
	// This is handled in createDefaultConfig for new configs

	// Apply Logs defaults
	if config.Logs.RotationCount == 0 {
		config.Logs.RotationCount = 4
	}

	// Apply Demo defaults (host port is dynamically assigned by Docker)
	if config.Demo.HealthcheckPath == "" {
		config.Demo.HealthcheckPath = "/health"
	}
	if config.Demo.HealthcheckTimeoutSeconds == 0 {
		config.Demo.HealthcheckTimeoutSeconds = 60
	}

	// Apply Maintenance defaults
	if config.Maintenance == nil {
		config.Maintenance = &MaintenanceConfig{}
	}
	// Note: Maintenance.Enabled defaults to false (zero value), but we want true by default
	// This is handled in createDefaultConfig for new configs
	if config.Maintenance.AfterSpecs == 0 {
		config.Maintenance.AfterSpecs = 1
	}
	// Apply task defaults - all enabled by default for new sections without explicit false
	// Note: For existing configs with tasks section, we don't override explicit false values
	// Apply branch cleanup defaults
	if len(config.Maintenance.BranchCleanup.ProtectedPatterns) == 0 {
		config.Maintenance.BranchCleanup.ProtectedPatterns = []string{"main", "master", "develop", "release/*", "hotfix/*"}
	}
	// Apply TODO scan defaults
	if len(config.Maintenance.TodoScan.Markers) == 0 {
		config.Maintenance.TodoScan.Markers = []string{"TODO", "FIXME", "HACK", "XXX", "deprecated", "DEPRECATED", "@deprecated"}
	}
}

func validateConfig(config *Config) error {
	// Structural validation only - provider/credential checks are handled by pkg/preflight
	// after operating mode is resolved. This allows airplane mode to skip cloud provider checks.
	getLogger().Info("📋 Validating config structure")

	// Validate agent config structure (model name format, coder_mode values)
	if config.Agents != nil {
		if err := validateAgentConfigInternal(config.Agents, config); err != nil {
			return fmt.Errorf("agent config validation failed: %w", err)
		}
	}

	// Validate Git settings format (RepoURL is optional - may not be using Git worktrees yet)
	if config.Git != nil && config.Git.RepoURL != "" {
		if !strings.HasPrefix(config.Git.RepoURL, "git@") && !strings.HasPrefix(config.Git.RepoURL, "https://") {
			return fmt.Errorf("git repo_url must start with 'git@' or 'https://'")
		}
	}

	// Validate WebUI settings
	if config.WebUI != nil && config.WebUI.Enabled {
		// Validate port range
		if config.WebUI.Port <= 0 || config.WebUI.Port > 65535 {
			return fmt.Errorf("webui port must be between 1 and 65535 (got %d)", config.WebUI.Port)
		}

		// Validate SSL configuration
		if config.WebUI.SSL {
			if config.WebUI.Cert == "" {
				return fmt.Errorf("webui ssl enabled but cert path is empty")
			}
			if config.WebUI.Key == "" {
				return fmt.Errorf("webui ssl enabled but key path is empty")
			}

			// Resolve and validate certificate paths
			certPath, err := resolveWebUIFilePath(config.WebUI.Cert)
			if err != nil {
				return fmt.Errorf("webui cert path error: %w", err)
			}
			if _, statErr := os.Stat(certPath); os.IsNotExist(statErr) {
				return fmt.Errorf("webui cert file does not exist: %s", certPath)
			}

			keyPath, err := resolveWebUIFilePath(config.WebUI.Key)
			if err != nil {
				return fmt.Errorf("webui key path error: %w", err)
			}
			if _, statErr := os.Stat(keyPath); os.IsNotExist(statErr) {
				return fmt.Errorf("webui key file does not exist: %s", keyPath)
			}
		}
	}

	getLogger().Info("✅ Config structure validated")
	return nil
}

// resolveWebUIFilePath resolves a file path for WebUI cert/key files.
// - Absolute paths: returned as-is.
// - Relative paths with directories: resolved relative to current directory.
// - Filename only: resolved to {projectDir}/.maestro/{filename}.
func resolveWebUIFilePath(filePath string) (string, error) {
	if filePath == "" {
		return "", fmt.Errorf("file path is empty")
	}

	// Check if it's an absolute path
	if filepath.IsAbs(filePath) {
		return filePath, nil
	}

	// Check if it contains directory separators
	if strings.Contains(filePath, string(filepath.Separator)) || strings.Contains(filePath, "/") {
		// Relative path with directory - resolve relative to current directory
		absPath, err := filepath.Abs(filePath)
		if err != nil {
			return "", fmt.Errorf("failed to resolve relative path: %w", err)
		}
		return absPath, nil
	}

	// Filename only - resolve to .maestro directory
	if projectDir == "" {
		return "", fmt.Errorf("config not initialized - call LoadConfig first")
	}
	return filepath.Join(projectDir, ProjectConfigDir, filePath), nil
}

// validateTargetContainer checks if the configured target container is valid and runnable.
// Returns true if the container exists, is runnable, and is not the bootstrap container.
func validateTargetContainer(cfg *Config) bool {
	if cfg.Container == nil || cfg.Container.Name == "" {
		return false
	}

	// Don't validate bootstrap container as a target
	if cfg.Container.Name == BootstrapContainerTag {
		return false
	}

	// TODO: Commenting out docker commands to debug location-dependent crash
	// Check if the container exists and is runnable using docker inspect
	// cmd := exec.Command("docker", "inspect", cfg.Container.Name)
	// if err := cmd.Run(); err != nil {
	//	return false
	// }

	// Try to run a simple command to verify the container is actually runnable
	// cmd = exec.Command("docker", "run", "--rm", cfg.Container.Name, "echo", "test")
	// if err := cmd.Run(); err != nil {
	//	return false
	// }

	return true
}

// IsValidTargetImage returns whether the configured target container is valid and runnable.
func IsValidTargetImage() bool {
	mu.RLock()
	defer mu.RUnlock()
	if config == nil {
		return false
	}
	return config.validTargetImage
}

// CalculateCost calculates the cost in USD for a given model and token usage.
// Uses separate input and output token pricing from KnownModels registry.
// Returns 0 cost for unknown models (allows using new models without pricing data).
func CalculateCost(modelName string, promptTokens, completionTokens int) (float64, error) {
	// Try to get pricing from KnownModels
	if info, exists := KnownModels[modelName]; exists {
		inputCost := (float64(promptTokens) / 1_000_000.0) * info.InputCPM
		outputCost := (float64(completionTokens) / 1_000_000.0) * info.OutputCPM
		return inputCost + outputCost, nil
	}

	// For unknown models, return 0 cost (allows usage but no cost tracking)
	// This is intentional - we want to support new models without requiring pricing data
	return 0.0, nil
}

// GetAPIKey returns the API key for a given provider.
// Checks secrets file first, then falls back to environment variables.
// For Ollama, returns the host URL instead of an API key.
func GetAPIKey(provider string) (string, error) {
	var envVar string
	switch provider {
	case ProviderAnthropic:
		envVar = EnvAnthropicAPIKey
	case ProviderOpenAI:
		envVar = EnvOpenAIAPIKey
	case ProviderGoogle:
		envVar = EnvGoogleAPIKey
	case ProviderOllama:
		// Ollama doesn't use API keys - return host URL instead
		// Check environment variable first, then default to localhost
		host := os.Getenv(EnvOllamaHost)
		if host == "" {
			host = "http://localhost:11434"
		}
		return host, nil
	default:
		return "", fmt.Errorf("unknown provider: %s", provider)
	}

	// Try to get from secrets file first, then environment variable
	key, err := GetSecret(envVar)
	if err == nil && key != "" {
		return key, nil
	}

	return "", fmt.Errorf("API key not found: %s not found in secrets file or environment variables", envVar)
}

// GetGitHubToken returns the GitHub token.
// Checks secrets file first, then falls back to environment variable.
func GetGitHubToken() string {
	token, err := GetSecret("GITHUB_TOKEN")
	if err == nil && token != "" {
		return token
	}
	return ""
}

// HasGitHubToken returns true if a GitHub token is available.
func HasGitHubToken() bool {
	return GetGitHubToken() != ""
}

// GetWebUIPassword returns the WebUI password using unified password logic:
// 1. Project password from secrets decryption (in memory)
// 2. MAESTRO_PASSWORD environment variable
// 3. Empty string (caller should auto-generate ephemeral password).
func GetWebUIPassword() string {
	// Check for project password in memory (from secrets decryption)
	if password := GetProjectPassword(); password != "" {
		return password
	}

	// Check for MAESTRO_PASSWORD environment variable
	if password := os.Getenv("MAESTRO_PASSWORD"); password != "" {
		return password
	}

	// Return empty string to trigger auto-generation
	return ""
}

// convertSSHToHTTPS converts SSH git URLs to HTTPS format for token-based authentication.
// This enables all git operations to use GITHUB_TOKEN instead of SSH keys.
func convertSSHToHTTPS(originalURL string) string {
	// Check if it's already an HTTPS URL
	if strings.HasPrefix(originalURL, "https://") {
		return originalURL
	}

	// Convert SSH URL format: git@github.com:owner/repo.git -> https://github.com/owner/repo.git
	if strings.HasPrefix(originalURL, "git@github.com:") {
		// Extract owner/repo.git part
		repoPath := strings.TrimPrefix(originalURL, "git@github.com:")
		return "https://github.com/" + repoPath
	}

	// Handle other common SSH formats: ssh://git@github.com/owner/repo.git
	if strings.HasPrefix(originalURL, "ssh://git@github.com/") {
		repoPath := strings.TrimPrefix(originalURL, "ssh://git@github.com/")
		return "https://github.com/" + repoPath
	}

	// If it's not a recognized GitHub URL format, return as-is
	return originalURL
}

// GenerateSessionID generates a new UUID session ID for the current orchestrator run.
// This session ID is used for database session isolation (filtering all reads/writes by session).
// Must be called after LoadConfig and before any database operations.
func GenerateSessionID() error {
	mu.Lock()
	defer mu.Unlock()

	if config == nil {
		return fmt.Errorf("config not initialized - call LoadConfig first")
	}

	// Generate new UUID for this session
	sessionID := fmt.Sprintf("%d", time.Now().UnixNano())
	// For better readability, use a simple timestamp-based ID instead of full UUID
	// This makes logs and debugging easier while still being unique
	config.SessionID = sessionID

	getLogger().Info("Generated session ID: %s", sessionID)
	return nil
}

// SetSessionID sets a specific session ID (used for resume mode).
// This allows resuming a previous session by reusing its session ID.
func SetSessionID(sessionID string) error {
	mu.Lock()
	defer mu.Unlock()

	if config == nil {
		return fmt.Errorf("config not initialized - call LoadConfig first")
	}

	config.SessionID = sessionID
	getLogger().Info("Restored session ID: %s", sessionID)
	return nil
}

// ResolveOperatingMode determines the operating mode based on CLI flag and config default.
// Precedence: CLI flag > config default_mode > "standard"
// This sets the runtime OperatingMode field (not persisted).
func ResolveOperatingMode(cliAirplaneFlag bool) error {
	mu.Lock()
	defer mu.Unlock()

	if config == nil {
		return fmt.Errorf("config not initialized - call LoadConfig first")
	}

	var mode string
	if cliAirplaneFlag {
		mode = OperatingModeAirplane
		getLogger().Info("Operating mode: airplane (from --airplane flag)")
	} else if config.DefaultMode != "" {
		mode = config.DefaultMode
		getLogger().Info("Operating mode: %s (from config default_mode)", mode)
	} else {
		mode = OperatingModeStandard
		getLogger().Info("Operating mode: standard (default)")
	}

	// Validate mode value
	if mode != OperatingModeStandard && mode != OperatingModeAirplane {
		return fmt.Errorf("invalid operating mode '%s': must be '%s' or '%s'",
			mode, OperatingModeStandard, OperatingModeAirplane)
	}

	config.OperatingMode = mode
	return nil
}

// GetOperatingMode returns the current operating mode.
// Returns "standard" if not explicitly set.
func GetOperatingMode() string {
	mu.RLock()
	defer mu.RUnlock()

	if config == nil || config.OperatingMode == "" {
		return OperatingModeStandard
	}
	return config.OperatingMode
}

// IsAirplaneMode returns true if currently operating in airplane (offline) mode.
func IsAirplaneMode() bool {
	return GetOperatingMode() == OperatingModeAirplane
}

// GetEffectiveCoderModel returns the coder model to use based on current operating mode.
// In airplane mode, returns the airplane override if configured, otherwise the standard model.
func GetEffectiveCoderModel() string {
	mu.RLock()
	defer mu.RUnlock()

	if config == nil || config.Agents == nil {
		return DefaultCoderModel
	}

	if config.OperatingMode == OperatingModeAirplane && config.Agents.Airplane != nil && config.Agents.Airplane.CoderModel != "" {
		return config.Agents.Airplane.CoderModel
	}
	return config.Agents.CoderModel
}

// GetEffectiveArchitectModel returns the architect model to use based on current operating mode.
// In airplane mode, returns the airplane override if configured, otherwise the standard model.
func GetEffectiveArchitectModel() string {
	mu.RLock()
	defer mu.RUnlock()

	if config == nil || config.Agents == nil {
		return DefaultArchitectModel
	}

	if config.OperatingMode == OperatingModeAirplane && config.Agents.Airplane != nil && config.Agents.Airplane.ArchitectModel != "" {
		return config.Agents.Airplane.ArchitectModel
	}
	return config.Agents.ArchitectModel
}

// GetEffectivePMModel returns the PM model to use based on current operating mode.
// In airplane mode, returns the airplane override if configured, otherwise the standard model.
func GetEffectivePMModel() string {
	mu.RLock()
	defer mu.RUnlock()

	if config == nil || config.Agents == nil {
		return DefaultPMModel
	}

	if config.OperatingMode == OperatingModeAirplane && config.Agents.Airplane != nil && config.Agents.Airplane.PMModel != "" {
		return config.Agents.Airplane.PMModel
	}
	return config.Agents.PMModel
}

// GetTotalAgentCount returns the total number of agents in the system.
// Used for rate limiter timeout calculation: max wait = agent_count × 1 minute.
// Total = 1 architect + 1 PM + MaxCoders + 1 hotfix = MaxCoders + 3.
func GetTotalAgentCount() int {
	mu.RLock()
	defer mu.RUnlock()

	if config == nil || config.Agents == nil {
		return 6 // Default: 3 coders + 3 (architect + PM + hotfix)
	}
	return config.Agents.MaxCoders + 3
}

// GetGitRepoURL returns the current repository URL from config.
// Returns empty string if not configured.
func GetGitRepoURL() string {
	mu.RLock()
	defer mu.RUnlock()

	if config == nil || config.Git == nil {
		return ""
	}
	return config.Git.RepoURL
}

// GetGitBaseBranch returns the current base/target branch from config.
// Returns DefaultTargetBranch if not configured.
func GetGitBaseBranch() string {
	mu.RLock()
	defer mu.RUnlock()

	if config == nil || config.Git == nil || config.Git.TargetBranch == "" {
		return DefaultTargetBranch
	}
	return config.Git.TargetBranch
}

// GetGitMirrorDir returns the mirror directory path.
// Always returns DefaultMirrorDir as this is not user-configurable.
func GetGitMirrorDir() string {
	return DefaultMirrorDir
}

// GetGitBranchPattern returns the branch naming pattern from config.
// Returns DefaultBranchPattern if not configured.
func GetGitBranchPattern() string {
	mu.RLock()
	defer mu.RUnlock()

	if config == nil || config.Git == nil || config.Git.BranchPattern == "" {
		return DefaultBranchPattern
	}
	return config.Git.BranchPattern
}

// RateLimitBufferFactor is the safety margin applied to rate limit buckets.
// The effective bucket capacity is TokensPerMinute * RateLimitBufferFactor.
// This accounts for token estimation inaccuracies (tiktoken vs actual).
const RateLimitBufferFactor = 0.9

// validateRateLimitCapacity checks that rate limits are sufficient for model context sizes.
// If a model's MaxContextTokens exceeds the effective bucket capacity (TPM * 0.9),
// requests could block forever. This function warns about such configurations.
func validateRateLimitCapacity(cfg *Config) {
	if cfg == nil || cfg.Agents == nil {
		return
	}

	// Build provider -> TPM map from current config
	providerTPM := map[string]int{
		ProviderAnthropic: cfg.Agents.Resilience.RateLimit.Anthropic.TokensPerMinute,
		ProviderOpenAI:    cfg.Agents.Resilience.RateLimit.OpenAI.TokensPerMinute,
		ProviderGoogle:    cfg.Agents.Resilience.RateLimit.Google.TokensPerMinute,
		ProviderOllama:    cfg.Agents.Resilience.RateLimit.Ollama.TokensPerMinute,
	}

	// Check each known model
	for modelName := range KnownModels {
		modelInfo := KnownModels[modelName]
		tpm := providerTPM[modelInfo.Provider]
		if tpm == 0 {
			continue
		}

		// Effective capacity is TPM * 0.9 (the buffer factor used in limiter.go)
		effectiveCapacity := int(float64(tpm) * RateLimitBufferFactor)

		if modelInfo.MaxContextTokens > effectiveCapacity {
			logx.Warnf("CONFIG: Model %s has MaxContextTokens (%d) > effective rate limit capacity (%d = %d * %.1f). "+
				"Large contexts may block forever. Consider increasing %s tokens_per_minute to at least %d.",
				modelName,
				modelInfo.MaxContextTokens,
				effectiveCapacity,
				tpm,
				RateLimitBufferFactor,
				modelInfo.Provider,
				int(float64(modelInfo.MaxContextTokens)/RateLimitBufferFactor)+1,
			)
		}
	}
}
