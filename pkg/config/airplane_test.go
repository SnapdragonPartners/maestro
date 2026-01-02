package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestAirplaneAgentConfigDefaults tests that airplane agent config defaults are applied.
func TestAirplaneAgentConfigDefaults(t *testing.T) {
	// Test createDefaultConfig includes airplane defaults
	cfg := createDefaultConfig()

	if cfg.Agents == nil {
		t.Fatal("Expected agents config to be created")
	}

	if cfg.Agents.Airplane == nil {
		t.Fatal("Expected airplane agent config to be created by default")
	}

	// Verify default airplane models
	if cfg.Agents.Airplane.CoderModel != "mistral-nemo:latest" {
		t.Errorf("Expected airplane coder model 'mistral-nemo:latest', got '%s'", cfg.Agents.Airplane.CoderModel)
	}
	if cfg.Agents.Airplane.ArchitectModel != "mistral-nemo:latest" {
		t.Errorf("Expected airplane architect model 'mistral-nemo:latest', got '%s'", cfg.Agents.Airplane.ArchitectModel)
	}
	if cfg.Agents.Airplane.PMModel != "mistral-nemo:latest" {
		t.Errorf("Expected airplane PM model 'mistral-nemo:latest', got '%s'", cfg.Agents.Airplane.PMModel)
	}
}

// createMinimalConfig creates a minimal config for testing applyDefaults.
// This provides required sections so applyDefaults doesn't panic.
func createMinimalConfig(airplane *AirplaneAgentConfig) *Config {
	return &Config{
		Project:   &ProjectInfo{},
		Container: &ContainerConfig{},
		Build:     &BuildConfig{},
		Agents: &AgentConfig{
			Airplane: airplane,
		},
		Git:   &GitConfig{},
		WebUI: &WebUIConfig{},
		Chat: &ChatConfig{
			Limits:  ChatLimitsConfig{},
			Scanner: ChatScannerConfig{},
		},
		Search:      &SearchConfig{},
		PM:          &PMConfig{},
		Logs:        &LogsConfig{},
		Debug:       &DebugConfig{},
		Demo:        &DemoConfig{},
		Maintenance: &MaintenanceConfig{},
	}
}

// TestApplyDefaultsAirplaneConfig tests that applyDefaults handles airplane config correctly.
func TestApplyDefaultsAirplaneConfig(t *testing.T) {
	tests := []struct {
		name     string
		airplane *AirplaneAgentConfig // Input airplane config (nil, empty, partial, or full)
		expected *AirplaneAgentConfig
	}{
		{
			name:     "nil airplane config gets defaults",
			airplane: nil,
			expected: &AirplaneAgentConfig{
				CoderModel:     "mistral-nemo:latest",
				ArchitectModel: "mistral-nemo:latest",
				PMModel:        "mistral-nemo:latest",
			},
		},
		{
			name:     "empty airplane config gets defaults",
			airplane: &AirplaneAgentConfig{},
			expected: &AirplaneAgentConfig{
				CoderModel:     "mistral-nemo:latest",
				ArchitectModel: "mistral-nemo:latest",
				PMModel:        "mistral-nemo:latest",
			},
		},
		{
			name: "partial airplane config gets remaining defaults",
			airplane: &AirplaneAgentConfig{
				CoderModel: "qwen2.5-coder:32b",
				// ArchitectModel and PMModel missing
			},
			expected: &AirplaneAgentConfig{
				CoderModel:     "qwen2.5-coder:32b",   // Kept as-is
				ArchitectModel: "mistral-nemo:latest", // Default applied
				PMModel:        "mistral-nemo:latest", // Default applied
			},
		},
		{
			name: "full airplane config kept as-is",
			airplane: &AirplaneAgentConfig{
				CoderModel:     "deepseek-coder:33b",
				ArchitectModel: "llama3.1:70b",
				PMModel:        "llama3.1:8b",
			},
			expected: &AirplaneAgentConfig{
				CoderModel:     "deepseek-coder:33b",
				ArchitectModel: "llama3.1:70b",
				PMModel:        "llama3.1:8b",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := createMinimalConfig(tt.airplane)
			applyDefaults(cfg)

			if cfg.Agents.Airplane == nil {
				t.Fatal("Expected airplane config to exist after applyDefaults")
			}

			if cfg.Agents.Airplane.CoderModel != tt.expected.CoderModel {
				t.Errorf("CoderModel: expected '%s', got '%s'",
					tt.expected.CoderModel, cfg.Agents.Airplane.CoderModel)
			}
			if cfg.Agents.Airplane.ArchitectModel != tt.expected.ArchitectModel {
				t.Errorf("ArchitectModel: expected '%s', got '%s'",
					tt.expected.ArchitectModel, cfg.Agents.Airplane.ArchitectModel)
			}
			if cfg.Agents.Airplane.PMModel != tt.expected.PMModel {
				t.Errorf("PMModel: expected '%s', got '%s'",
					tt.expected.PMModel, cfg.Agents.Airplane.PMModel)
			}
		})
	}
}

// TestResolveOperatingMode tests the mode resolution logic.
func TestResolveOperatingMode(t *testing.T) {
	// Store original config and restore after test
	mu.Lock()
	originalConfig := config
	originalProjectDir := projectDir
	mu.Unlock()

	defer func() {
		mu.Lock()
		config = originalConfig
		projectDir = originalProjectDir
		mu.Unlock()
	}()

	tests := []struct {
		name            string
		cliAirplaneFlag bool
		defaultMode     string
		expectedMode    string
	}{
		{
			name:            "CLI airplane flag takes precedence",
			cliAirplaneFlag: true,
			defaultMode:     OperatingModeStandard,
			expectedMode:    OperatingModeAirplane,
		},
		{
			name:            "config default_mode used when no CLI flag",
			cliAirplaneFlag: false,
			defaultMode:     OperatingModeAirplane,
			expectedMode:    OperatingModeAirplane,
		},
		{
			name:            "standard is default when nothing set",
			cliAirplaneFlag: false,
			defaultMode:     "",
			expectedMode:    OperatingModeStandard,
		},
		{
			name:            "CLI false with standard config",
			cliAirplaneFlag: false,
			defaultMode:     OperatingModeStandard,
			expectedMode:    OperatingModeStandard,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup test config
			mu.Lock()
			config = &Config{
				DefaultMode: tt.defaultMode,
			}
			mu.Unlock()

			// Resolve operating mode
			err := ResolveOperatingMode(tt.cliAirplaneFlag)
			if err != nil {
				t.Fatalf("ResolveOperatingMode failed: %v", err)
			}

			// Verify result
			mode := GetOperatingMode()
			if mode != tt.expectedMode {
				t.Errorf("Expected mode '%s', got '%s'", tt.expectedMode, mode)
			}
		})
	}
}

// TestIsAirplaneMode tests the IsAirplaneMode helper.
func TestIsAirplaneMode(t *testing.T) {
	// Store original config and restore after test
	mu.Lock()
	originalConfig := config
	mu.Unlock()

	defer func() {
		mu.Lock()
		config = originalConfig
		mu.Unlock()
	}()

	tests := []struct {
		name          string
		operatingMode string
		expected      bool
	}{
		{
			name:          "airplane mode returns true",
			operatingMode: OperatingModeAirplane,
			expected:      true,
		},
		{
			name:          "standard mode returns false",
			operatingMode: OperatingModeStandard,
			expected:      false,
		},
		{
			name:          "empty mode returns false (default to standard)",
			operatingMode: "",
			expected:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mu.Lock()
			config = &Config{
				OperatingMode: tt.operatingMode,
			}
			mu.Unlock()

			result := IsAirplaneMode()
			if result != tt.expected {
				t.Errorf("Expected IsAirplaneMode() = %v, got %v", tt.expected, result)
			}
		})
	}
}

// TestGetEffectiveModels tests that effective model getters work correctly.
func TestGetEffectiveModels(t *testing.T) {
	// Store original config and restore after test
	mu.Lock()
	originalConfig := config
	mu.Unlock()

	defer func() {
		mu.Lock()
		config = originalConfig
		mu.Unlock()
	}()

	standardConfig := &Config{
		OperatingMode: OperatingModeStandard,
		Agents: &AgentConfig{
			CoderModel:     "claude-sonnet-4-5",
			ArchitectModel: "gemini-3-pro-preview",
			PMModel:        "claude-opus-4-5",
			Airplane: &AirplaneAgentConfig{
				CoderModel:     "mistral-nemo:latest",
				ArchitectModel: "llama3.1:70b",
				PMModel:        "llama3.1:8b",
			},
		},
	}

	airplaneConfig := &Config{
		OperatingMode: OperatingModeAirplane,
		Agents: &AgentConfig{
			CoderModel:     "claude-sonnet-4-5",
			ArchitectModel: "gemini-3-pro-preview",
			PMModel:        "claude-opus-4-5",
			Airplane: &AirplaneAgentConfig{
				CoderModel:     "mistral-nemo:latest",
				ArchitectModel: "llama3.1:70b",
				PMModel:        "llama3.1:8b",
			},
		},
	}

	t.Run("standard mode returns cloud models", func(t *testing.T) {
		mu.Lock()
		config = standardConfig
		mu.Unlock()

		if got := GetEffectiveCoderModel(); got != "claude-sonnet-4-5" {
			t.Errorf("GetEffectiveCoderModel() = %s, want claude-sonnet-4-5", got)
		}
		if got := GetEffectiveArchitectModel(); got != "gemini-3-pro-preview" {
			t.Errorf("GetEffectiveArchitectModel() = %s, want gemini-3-pro-preview", got)
		}
		if got := GetEffectivePMModel(); got != "claude-opus-4-5" {
			t.Errorf("GetEffectivePMModel() = %s, want claude-opus-4-5", got)
		}
	})

	t.Run("airplane mode returns local models", func(t *testing.T) {
		mu.Lock()
		config = airplaneConfig
		mu.Unlock()

		if got := GetEffectiveCoderModel(); got != "mistral-nemo:latest" {
			t.Errorf("GetEffectiveCoderModel() = %s, want mistral-nemo:latest", got)
		}
		if got := GetEffectiveArchitectModel(); got != "llama3.1:70b" {
			t.Errorf("GetEffectiveArchitectModel() = %s, want llama3.1:70b", got)
		}
		if got := GetEffectivePMModel(); got != "llama3.1:8b" {
			t.Errorf("GetEffectivePMModel() = %s, want llama3.1:8b", got)
		}
	})

	t.Run("airplane mode without override falls back to standard", func(t *testing.T) {
		mu.Lock()
		config = &Config{
			OperatingMode: OperatingModeAirplane,
			Agents: &AgentConfig{
				CoderModel:     "claude-sonnet-4-5",
				ArchitectModel: "gemini-3-pro-preview",
				PMModel:        "claude-opus-4-5",
				Airplane:       &AirplaneAgentConfig{}, // Empty - no overrides
			},
		}
		mu.Unlock()

		// Should fall back to standard models when airplane overrides are empty
		if got := GetEffectiveCoderModel(); got != "claude-sonnet-4-5" {
			t.Errorf("GetEffectiveCoderModel() = %s, want claude-sonnet-4-5 (fallback)", got)
		}
	})
}

// TestAirplaneConfigSerialization tests that airplane config serializes correctly to JSON.
func TestAirplaneConfigSerialization(t *testing.T) {
	cfg := &Config{
		SchemaVersion: SchemaVersion,
		DefaultMode:   OperatingModeAirplane,
		Agents: &AgentConfig{
			MaxCoders:      3,
			CoderModel:     "claude-sonnet-4-5",
			ArchitectModel: "gemini-3-pro-preview",
			PMModel:        "claude-opus-4-5",
			Airplane: &AirplaneAgentConfig{
				CoderModel:     "qwen2.5-coder:32b",
				ArchitectModel: "llama3.1:70b",
				PMModel:        "llama3.1:8b",
			},
		},
	}

	// Serialize to JSON
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal config: %v", err)
	}

	// Deserialize back
	var loaded Config
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("Failed to unmarshal config: %v", err)
	}

	// Verify values preserved
	if loaded.DefaultMode != OperatingModeAirplane {
		t.Errorf("DefaultMode not preserved: got '%s'", loaded.DefaultMode)
	}
	if loaded.Agents.Airplane == nil {
		t.Fatal("Airplane config not preserved")
	}
	if loaded.Agents.Airplane.CoderModel != "qwen2.5-coder:32b" {
		t.Errorf("Airplane CoderModel not preserved: got '%s'", loaded.Agents.Airplane.CoderModel)
	}
}

// TestLoadConfigWithAirplaneSection tests loading config that includes airplane settings.
func TestLoadConfigWithAirplaneSection(t *testing.T) {
	// Create temporary directory for test
	tempDir, err := os.MkdirTemp("", "airplane-config-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create .maestro directory
	maestroDir := filepath.Join(tempDir, ProjectConfigDir)
	if mkdirErr := os.MkdirAll(maestroDir, 0755); mkdirErr != nil {
		t.Fatalf("Failed to create .maestro dir: %v", mkdirErr)
	}

	// Write config with airplane settings
	configContent := `{
		"schema_version": "1.0",
		"default_mode": "airplane",
		"agents": {
			"max_coders": 2,
			"coder_model": "claude-sonnet-4-5",
			"architect_model": "gemini-3-pro-preview",
			"pm_model": "claude-opus-4-5",
			"airplane": {
				"coder_model": "deepseek-coder:33b",
				"architect_model": "qwen2.5:72b",
				"pm_model": "llama3.1:8b"
			}
		}
	}`

	configPath := filepath.Join(maestroDir, "config.json")
	if writeErr := os.WriteFile(configPath, []byte(configContent), 0644); writeErr != nil {
		t.Fatalf("Failed to write config: %v", writeErr)
	}

	// Load the config file directly (without full validation that requires GITHUB_TOKEN)
	loaded, err := loadConfigFromFile(configPath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Verify airplane settings loaded correctly
	if loaded.DefaultMode != "airplane" {
		t.Errorf("Expected default_mode 'airplane', got '%s'", loaded.DefaultMode)
	}

	if loaded.Agents == nil {
		t.Fatal("Expected agents config")
	}

	if loaded.Agents.Airplane == nil {
		t.Fatal("Expected airplane config")
	}

	if loaded.Agents.Airplane.CoderModel != "deepseek-coder:33b" {
		t.Errorf("Expected airplane coder_model 'deepseek-coder:33b', got '%s'",
			loaded.Agents.Airplane.CoderModel)
	}

	if loaded.Agents.Airplane.ArchitectModel != "qwen2.5:72b" {
		t.Errorf("Expected airplane architect_model 'qwen2.5:72b', got '%s'",
			loaded.Agents.Airplane.ArchitectModel)
	}

	if loaded.Agents.Airplane.PMModel != "llama3.1:8b" {
		t.Errorf("Expected airplane pm_model 'llama3.1:8b', got '%s'",
			loaded.Agents.Airplane.PMModel)
	}
}

// TestOperatingModeConstants tests that the constants are defined correctly.
func TestOperatingModeConstants(t *testing.T) {
	if OperatingModeStandard != "standard" {
		t.Errorf("OperatingModeStandard should be 'standard', got '%s'", OperatingModeStandard)
	}
	if OperatingModeAirplane != "airplane" {
		t.Errorf("OperatingModeAirplane should be 'airplane', got '%s'", OperatingModeAirplane)
	}
}
