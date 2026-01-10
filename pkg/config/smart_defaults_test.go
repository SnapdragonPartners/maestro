package config

import (
	"os"
	"testing"
)

func TestGetAvailableProviders(t *testing.T) {
	// Save and restore environment
	origAnthropic := os.Getenv(EnvAnthropicAPIKey)
	origOpenAI := os.Getenv(EnvOpenAIAPIKey)
	origGoogle := os.Getenv(EnvGoogleAPIKey)
	defer func() {
		os.Setenv(EnvAnthropicAPIKey, origAnthropic)
		os.Setenv(EnvOpenAIAPIKey, origOpenAI)
		os.Setenv(EnvGoogleAPIKey, origGoogle)
	}()

	tests := []struct {
		name      string
		anthropic string
		openai    string
		google    string
		wantCount int
	}{
		{
			name:      "all providers",
			anthropic: "sk-ant-test",
			openai:    "sk-openai-test",
			google:    "google-test",
			wantCount: 3,
		},
		{
			name:      "anthropic only",
			anthropic: "sk-ant-test",
			openai:    "",
			google:    "",
			wantCount: 1,
		},
		{
			name:      "openai only",
			anthropic: "",
			openai:    "sk-openai-test",
			google:    "",
			wantCount: 1,
		},
		{
			name:      "google only",
			anthropic: "",
			openai:    "",
			google:    "google-test",
			wantCount: 1,
		},
		{
			name:      "anthropic and google",
			anthropic: "sk-ant-test",
			openai:    "",
			google:    "google-test",
			wantCount: 2,
		},
		{
			name:      "no providers",
			anthropic: "",
			openai:    "",
			google:    "",
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv(EnvAnthropicAPIKey, tt.anthropic)
			os.Setenv(EnvOpenAIAPIKey, tt.openai)
			os.Setenv(EnvGoogleAPIKey, tt.google)

			providers := getAvailableProviders()
			gotCount := providers.countAvailableProviders()

			if gotCount != tt.wantCount {
				t.Errorf("countAvailableProviders() = %d, want %d", gotCount, tt.wantCount)
			}

			// Verify individual flags
			if providers.Anthropic != (tt.anthropic != "") {
				t.Errorf("Anthropic = %v, want %v", providers.Anthropic, tt.anthropic != "")
			}
			if providers.OpenAI != (tt.openai != "") {
				t.Errorf("OpenAI = %v, want %v", providers.OpenAI, tt.openai != "")
			}
			if providers.Google != (tt.google != "") {
				t.Errorf("Google = %v, want %v", providers.Google, tt.google != "")
			}
		})
	}
}

func TestGetSmartDefaultModel(t *testing.T) {
	tests := []struct {
		name      string
		agentType string
		providers AvailableProviders
		want      string
	}{
		// Architect: Google → OpenAI → Anthropic
		{
			name:      "architect prefers google",
			agentType: AgentTypeArchitect,
			providers: AvailableProviders{Anthropic: true, OpenAI: true, Google: true},
			want:      ModelGemini3Pro,
		},
		{
			name:      "architect falls back to openai",
			agentType: AgentTypeArchitect,
			providers: AvailableProviders{Anthropic: true, OpenAI: true, Google: false},
			want:      ModelGPT52,
		},
		{
			name:      "architect falls back to anthropic",
			agentType: AgentTypeArchitect,
			providers: AvailableProviders{Anthropic: true, OpenAI: false, Google: false},
			want:      ModelClaudeOpus45,
		},

		// PM: Anthropic → OpenAI → Google
		{
			name:      "pm prefers anthropic",
			agentType: AgentTypePM,
			providers: AvailableProviders{Anthropic: true, OpenAI: true, Google: true},
			want:      ModelClaudeOpus45,
		},
		{
			name:      "pm falls back to openai",
			agentType: AgentTypePM,
			providers: AvailableProviders{Anthropic: false, OpenAI: true, Google: true},
			want:      ModelGPT52,
		},
		{
			name:      "pm falls back to google",
			agentType: AgentTypePM,
			providers: AvailableProviders{Anthropic: false, OpenAI: false, Google: true},
			want:      ModelGemini3Pro,
		},

		// Coder: Anthropic → OpenAI → Google
		{
			name:      "coder prefers anthropic",
			agentType: AgentTypeCoder,
			providers: AvailableProviders{Anthropic: true, OpenAI: true, Google: true},
			want:      ModelClaudeSonnet4,
		},
		{
			name:      "coder falls back to openai",
			agentType: AgentTypeCoder,
			providers: AvailableProviders{Anthropic: false, OpenAI: true, Google: true},
			want:      ModelGPT52,
		},
		{
			name:      "coder falls back to google",
			agentType: AgentTypeCoder,
			providers: AvailableProviders{Anthropic: false, OpenAI: false, Google: true},
			want:      ModelGemini3Pro,
		},

		// No providers
		{
			name:      "no providers returns empty",
			agentType: AgentTypeCoder,
			providers: AvailableProviders{Anthropic: false, OpenAI: false, Google: false},
			want:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getSmartDefaultModel(tt.agentType, tt.providers)
			if got != tt.want {
				t.Errorf("getSmartDefaultModel(%s) = %q, want %q", tt.agentType, got, tt.want)
			}
		})
	}
}

func TestApplySmartModelDefaults(t *testing.T) {
	// Save and restore environment
	origAnthropic := os.Getenv(EnvAnthropicAPIKey)
	origOpenAI := os.Getenv(EnvOpenAIAPIKey)
	origGoogle := os.Getenv(EnvGoogleAPIKey)
	defer func() {
		os.Setenv(EnvAnthropicAPIKey, origAnthropic)
		os.Setenv(EnvOpenAIAPIKey, origOpenAI)
		os.Setenv(EnvGoogleAPIKey, origGoogle)
	}()

	tests := []struct {
		name               string
		anthropic          string
		openai             string
		google             string
		wantSingleProvider bool
		wantCoderModel     string
		wantArchitectModel string
		wantPMModel        string
	}{
		{
			name:               "all providers - heterogeneous setup",
			anthropic:          "sk-ant-test",
			openai:             "sk-openai-test",
			google:             "google-test",
			wantSingleProvider: false,
			wantCoderModel:     ModelClaudeSonnet4, // Anthropic preferred
			wantArchitectModel: ModelGemini3Pro,    // Google preferred
			wantPMModel:        ModelClaudeOpus45,  // Anthropic preferred
		},
		{
			name:               "anthropic only - single provider warning",
			anthropic:          "sk-ant-test",
			openai:             "",
			google:             "",
			wantSingleProvider: true,
			wantCoderModel:     ModelClaudeSonnet4,
			wantArchitectModel: ModelClaudeOpus45,
			wantPMModel:        ModelClaudeOpus45,
		},
		{
			name:               "google only - single provider warning",
			anthropic:          "",
			openai:             "",
			google:             "google-test",
			wantSingleProvider: true,
			wantCoderModel:     ModelGemini3Pro,
			wantArchitectModel: ModelGemini3Pro,
			wantPMModel:        ModelGemini3Pro,
		},
		{
			name:               "anthropic and google - good heterogeneity",
			anthropic:          "sk-ant-test",
			openai:             "",
			google:             "google-test",
			wantSingleProvider: false,
			wantCoderModel:     ModelClaudeSonnet4,
			wantArchitectModel: ModelGemini3Pro,
			wantPMModel:        ModelClaudeOpus45,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv(EnvAnthropicAPIKey, tt.anthropic)
			os.Setenv(EnvOpenAIAPIKey, tt.openai)
			os.Setenv(EnvGoogleAPIKey, tt.google)

			cfg := &Config{
				Agents: &AgentConfig{},
			}

			gotSingleProvider := applySmartModelDefaults(cfg)

			if gotSingleProvider != tt.wantSingleProvider {
				t.Errorf("applySmartModelDefaults() singleProvider = %v, want %v", gotSingleProvider, tt.wantSingleProvider)
			}
			if cfg.Agents.CoderModel != tt.wantCoderModel {
				t.Errorf("CoderModel = %q, want %q", cfg.Agents.CoderModel, tt.wantCoderModel)
			}
			if cfg.Agents.ArchitectModel != tt.wantArchitectModel {
				t.Errorf("ArchitectModel = %q, want %q", cfg.Agents.ArchitectModel, tt.wantArchitectModel)
			}
			if cfg.Agents.PMModel != tt.wantPMModel {
				t.Errorf("PMModel = %q, want %q", cfg.Agents.PMModel, tt.wantPMModel)
			}
		})
	}
}

func TestApplySmartModelDefaults_RespectsExistingConfig(t *testing.T) {
	// Save and restore environment
	origAnthropic := os.Getenv(EnvAnthropicAPIKey)
	origOpenAI := os.Getenv(EnvOpenAIAPIKey)
	origGoogle := os.Getenv(EnvGoogleAPIKey)
	defer func() {
		os.Setenv(EnvAnthropicAPIKey, origAnthropic)
		os.Setenv(EnvOpenAIAPIKey, origOpenAI)
		os.Setenv(EnvGoogleAPIKey, origGoogle)
	}()

	// Set all providers available
	os.Setenv(EnvAnthropicAPIKey, "sk-ant-test")
	os.Setenv(EnvOpenAIAPIKey, "sk-openai-test")
	os.Setenv(EnvGoogleAPIKey, "google-test")

	// Config with pre-set models should not be overwritten
	cfg := &Config{
		Agents: &AgentConfig{
			CoderModel:     "custom-coder-model",
			ArchitectModel: "custom-architect-model",
			PMModel:        "", // Only this should be filled in
		},
	}

	applySmartModelDefaults(cfg)

	// Pre-set values should be preserved
	if cfg.Agents.CoderModel != "custom-coder-model" {
		t.Errorf("CoderModel was overwritten: got %q, want %q", cfg.Agents.CoderModel, "custom-coder-model")
	}
	if cfg.Agents.ArchitectModel != "custom-architect-model" {
		t.Errorf("ArchitectModel was overwritten: got %q, want %q", cfg.Agents.ArchitectModel, "custom-architect-model")
	}

	// Empty value should be filled with smart default
	if cfg.Agents.PMModel != ModelClaudeOpus45 {
		t.Errorf("PMModel should be set to default: got %q, want %q", cfg.Agents.PMModel, ModelClaudeOpus45)
	}
}
