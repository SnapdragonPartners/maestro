package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	// Create temporary config file.
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	configData := `{
		"models": {
			"claude_sonnet4": {
				"max_tokens_per_minute": 1000,
				"max_budget_per_day_usd": 10.0,
				"cpm_tokens_in": 0.003,
				"cpm_tokens_out": 0.015,
				"api_key": "${CLAUDE_API_KEY}",
				"agents": [
					{
						"name": "test-claude",
						"id": "001", 
						"type": "coder",
						"workdir": "./work/test-claude"
					}
				]
			}
		},
		"graceful_shutdown_timeout_sec": 30,
		"event_log_rotation_hours": 24,
		"max_retry_attempts": 3,
		"retry_backoff_multiplier": 2.0
	}`

	err := os.WriteFile(configPath, []byte(configData), 0644)
	if err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	// Set environment variable.
	os.Setenv("CLAUDE_API_KEY", "test-key-123")
	defer os.Unsetenv("CLAUDE_API_KEY")

	config, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Test placeholder substitution.
	if config.Models["claude_sonnet4"].APIKey != "test-key-123" {
		t.Errorf("Expected API key 'test-key-123', got '%s'", config.Models["claude_sonnet4"].APIKey)
	}

	// Test other values.
	if config.Models["claude_sonnet4"].MaxTokensPerMinute != 1000 {
		t.Errorf("Expected MaxTokensPerMinute 1000, got %d", config.Models["claude_sonnet4"].MaxTokensPerMinute)
	}

	if config.Models["claude_sonnet4"].MaxBudgetPerDayUSD != 10.0 {
		t.Errorf("Expected MaxBudgetPerDayUSD 10.0, got %f", config.Models["claude_sonnet4"].MaxBudgetPerDayUSD)
	}

	if config.Models["claude_sonnet4"].CpmTokensIn != 0.003 {
		t.Errorf("Expected CpmTokensIn 0.003, got %f", config.Models["claude_sonnet4"].CpmTokensIn)
	}

	if len(config.Models["claude_sonnet4"].Agents) != 1 {
		t.Errorf("Expected 1 agent, got %d", len(config.Models["claude_sonnet4"].Agents))
	}

	agent := config.Models["claude_sonnet4"].Agents[0]
	if agent.Name != "test-claude" {
		t.Errorf("Expected agent name 'test-claude', got '%s'", agent.Name)
	}
	if agent.Type != "coder" {
		t.Errorf("Expected agent type 'coder', got '%s'", agent.Type)
	}
}

func TestEnvOverride(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	configData := `{
		"models": {
			"claude_sonnet4": {
				"max_tokens_per_minute": 1000,
				"max_budget_per_day_usd": 10.0,
				"cpm_tokens_in": 0.003,
				"cpm_tokens_out": 0.015,
				"api_key": "default-key",
				"agents": [
					{
						"name": "test-claude",
						"id": "001",
						"type": "coder",
						"workdir": "./work/test-claude"
					}
				]
			}
		},
		"graceful_shutdown_timeout_sec": 30
	}`

	err := os.WriteFile(configPath, []byte(configData), 0644)
	if err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	// Set environment override.
	os.Setenv("GRACEFUL_SHUTDOWN_TIMEOUT_SEC", "60")
	defer os.Unsetenv("GRACEFUL_SHUTDOWN_TIMEOUT_SEC")

	config, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Test environment override.
	if config.GracefulShutdownTimeoutSec != 60 {
		t.Errorf("Expected GracefulShutdownTimeoutSec 60, got %d", config.GracefulShutdownTimeoutSec)
	}
}

func TestValidationErrors(t *testing.T) {
	tests := []struct {
		name       string
		configData string
		wantError  bool
	}{
		{
			name: "no models",
			configData: `{
				"graceful_shutdown_timeout_sec": 30
			}`,
			wantError: true,
		},
		{
			name: "invalid max_tokens_per_minute",
			configData: `{
				"models": {
					"claude_sonnet4": {
						"max_tokens_per_minute": 0,
						"max_budget_per_day_usd": 10.0,
						"cpm_tokens_in": 0.003,
						"cpm_tokens_out": 0.015,
						"api_key": "test-key",
						"agents": [{"name": "test", "id": "001", "type": "coder", "workdir": "./work/test"}]
					}
				}
			}`,
			wantError: true,
		},
		{
			name: "no agents configured",
			configData: `{
				"models": {
					"claude_sonnet4": {
						"max_tokens_per_minute": 1000,
						"max_budget_per_day_usd": 10.0,
						"cpm_tokens_in": 0.003,
						"cpm_tokens_out": 0.015,
						"api_key": "test-key",
						"agents": []
					}
				}
			}`,
			wantError: true,
		},
		{
			name: "missing api_key",
			configData: `{
				"models": {
					"claude_sonnet4": {
						"max_tokens_per_minute": 1000,
						"max_budget_per_day_usd": 10.0,
						"cpm_tokens_in": 0.003,
						"cpm_tokens_out": 0.015,
						"agents": [{"name": "test", "id": "001", "type": "coder", "workdir": "./work/test"}]
					}
				}
			}`,
			wantError: true,
		},
		{
			name: "invalid agent type",
			configData: `{
				"models": {
					"claude_sonnet4": {
						"max_tokens_per_minute": 1000,
						"max_budget_per_day_usd": 10.0,
						"cpm_tokens_in": 0.003,
						"cpm_tokens_out": 0.015,
						"api_key": "test-key",
						"agents": [{"name": "test", "id": "001", "type": "invalid", "workdir": "./work/test"}]
					}
				}
			}`,
			wantError: true,
		},
		{
			name: "missing agent name",
			configData: `{
				"models": {
					"claude_sonnet4": {
						"max_tokens_per_minute": 1000,
						"max_budget_per_day_usd": 10.0,
						"cpm_tokens_in": 0.003,
						"cpm_tokens_out": 0.015,
						"api_key": "test-key",
						"agents": [{"id": "001", "type": "coder", "workdir": "./work/test"}]
					}
				}
			}`,
			wantError: true,
		},
		{
			name: "valid config",
			configData: `{
				"models": {
					"claude_sonnet4": {
						"max_tokens_per_minute": 1000,
						"max_budget_per_day_usd": 10.0,
						"cpm_tokens_in": 0.003,
						"cpm_tokens_out": 0.015,
						"api_key": "test-key",
						"agents": [{"name": "test", "id": "001", "type": "coder", "workdir": "./work/test"}]
					}
				}
			}`,
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			configPath := filepath.Join(tmpDir, "config.json")

			err := os.WriteFile(configPath, []byte(tt.configData), 0644)
			if err != nil {
				t.Fatalf("Failed to write config file: %v", err)
			}

			_, err = LoadConfig(configPath)

			if tt.wantError && err == nil {
				t.Errorf("Expected error but got none")
			}
			if !tt.wantError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
		})
	}
}

func TestGetAllAgents(t *testing.T) {
	config := &Config{
		Models: map[string]ModelCfg{
			"claude_sonnet4": {
				Agents: []Agent{
					{Name: "claude1", ID: "001", Type: "coder", WorkDir: "./work/claude1"},
					{Name: "claude2", ID: "002", Type: "coder", WorkDir: "./work/claude2"},
				},
			},
			"openai_o3": {
				Agents: []Agent{
					{Name: "architect1", ID: "001", Type: "architect", WorkDir: "./work/arch1"},
				},
			},
		},
	}

	agents := config.GetAllAgents()
	if len(agents) != 3 {
		t.Errorf("Expected 3 agents, got %d", len(agents))
	}

	// Test agent lookup by log ID.
	agentWithModel, err := config.GetAgentByLogID("claude_sonnet4:001")
	if err != nil {
		t.Fatalf("Failed to get agent by log ID: %v", err)
	}
	if agentWithModel.Agent.Name != "claude1" {
		t.Errorf("Expected agent name 'claude1', got '%s'", agentWithModel.Agent.Name)
	}
	if agentWithModel.ModelName != "claude_sonnet4" {
		t.Errorf("Expected model name 'claude_sonnet4', got '%s'", agentWithModel.ModelName)
	}
}

func TestFileNotFound(t *testing.T) {
	_, err := LoadConfig("nonexistent.json")
	if err == nil {
		t.Error("Expected error for nonexistent file")
	}
}

func TestInvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	err := os.WriteFile(configPath, []byte("invalid json"), 0644)
	if err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	_, err = LoadConfig(configPath)
	if err == nil {
		t.Error("Expected error for invalid JSON")
	}
}
