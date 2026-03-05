package preflight

import (
	"os"
	"testing"

	"orchestrator/pkg/config"
)

func TestCheckRequiredAPIKeys_AllPresent(t *testing.T) {
	cleanup := setupTestConfig(t,
		"claude-sonnet-4-5",
		"o3-mini",
		"claude-opus-4-5",
		config.OperatingModeStandard,
	)
	defer cleanup()

	// Set required keys via env vars (GetSecret falls back to os.Getenv)
	origAnthropic := os.Getenv("ANTHROPIC_API_KEY")
	origOpenAI := os.Getenv("OPENAI_API_KEY")
	origGitHub := os.Getenv("GITHUB_TOKEN")
	defer func() {
		restoreEnv("ANTHROPIC_API_KEY", origAnthropic)
		restoreEnv("OPENAI_API_KEY", origOpenAI)
		restoreEnv("GITHUB_TOKEN", origGitHub)
	}()

	os.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	os.Setenv("OPENAI_API_KEY", "sk-openai-test")
	os.Setenv("GITHUB_TOKEN", "ghp_test")

	cfg, _ := config.GetConfig()
	keys, allPresent := CheckRequiredAPIKeys(&cfg)

	if !allPresent {
		t.Error("Expected allPresent=true when all keys are set")
	}
	for _, k := range keys {
		if !k.Present {
			t.Errorf("Expected key %s to be present", k.EnvVarName)
		}
	}
}

func TestCheckRequiredAPIKeys_SomeMissing(t *testing.T) {
	cleanup := setupTestConfig(t,
		"claude-sonnet-4-5",
		"o3-mini",
		"claude-opus-4-5",
		config.OperatingModeStandard,
	)
	defer cleanup()

	// Clear all keys
	origAnthropic := os.Getenv("ANTHROPIC_API_KEY")
	origOpenAI := os.Getenv("OPENAI_API_KEY")
	origGitHub := os.Getenv("GITHUB_TOKEN")
	defer func() {
		restoreEnv("ANTHROPIC_API_KEY", origAnthropic)
		restoreEnv("OPENAI_API_KEY", origOpenAI)
		restoreEnv("GITHUB_TOKEN", origGitHub)
	}()

	os.Unsetenv("ANTHROPIC_API_KEY")
	os.Setenv("OPENAI_API_KEY", "sk-openai-test")
	os.Unsetenv("GITHUB_TOKEN")

	// Clear secrets store too
	config.SetDecryptedSecrets(nil)

	cfg, _ := config.GetConfig()
	keys, allPresent := CheckRequiredAPIKeys(&cfg)

	if allPresent {
		t.Error("Expected allPresent=false when some keys are missing")
	}

	// Verify specific keys
	for _, k := range keys {
		switch k.EnvVarName {
		case "OPENAI_API_KEY":
			if !k.Present {
				t.Error("OPENAI_API_KEY should be present")
			}
		case "ANTHROPIC_API_KEY":
			if k.Present {
				t.Error("ANTHROPIC_API_KEY should be missing")
			}
		case "GITHUB_TOKEN":
			if k.Present {
				t.Error("GITHUB_TOKEN should be missing")
			}
		}
	}
}

func TestCheckRequiredAPIKeys_OllamaExcluded(t *testing.T) {
	cleanup := setupTestConfig(t,
		"mistral-nemo:latest",
		"mistral-nemo:latest",
		"mistral-nemo:latest",
		config.OperatingModeAirplane,
	)
	defer cleanup()

	cfg, _ := config.GetConfig()
	keys, _ := CheckRequiredAPIKeys(&cfg)

	// Ollama and Docker should not appear in the API key check list
	for _, k := range keys {
		if k.Provider == ProviderOllama {
			t.Error("Ollama should not appear in API key checks")
		}
		if k.Provider == ProviderDocker {
			t.Error("Docker should not appear in API key checks")
		}
	}
}

func TestCheckRequiredAPIKeys_GitHubConditional(t *testing.T) {
	// In airplane mode, GitHub should not be required
	cleanup := setupTestConfig(t,
		"mistral-nemo:latest",
		"mistral-nemo:latest",
		"mistral-nemo:latest",
		config.OperatingModeAirplane,
	)
	defer cleanup()

	cfg, _ := config.GetConfig()
	keys, _ := CheckRequiredAPIKeys(&cfg)

	for _, k := range keys {
		if k.Provider == ProviderGitHub {
			t.Error("GitHub should not be required in airplane mode")
		}
	}
}

func TestCheckRequiredAPIKeys_GuidanceURLs(t *testing.T) {
	cleanup := setupTestConfig(t,
		"claude-sonnet-4-5",
		"o3-mini",
		"gemini-3-pro-preview",
		config.OperatingModeStandard,
	)
	defer cleanup()

	cfg, _ := config.GetConfig()
	keys, _ := CheckRequiredAPIKeys(&cfg)

	for _, k := range keys {
		if k.GuidanceURL == "" {
			t.Errorf("Key %s should have a guidance URL", k.EnvVarName)
		}
	}
}

func TestCheckRequiredAPIKeys_SecretsStore(t *testing.T) {
	cleanup := setupTestConfig(t,
		"claude-sonnet-4-5",
		"o3-mini",
		"claude-opus-4-5",
		config.OperatingModeStandard,
	)
	defer cleanup()

	// Clear env vars
	origAnthropic := os.Getenv("ANTHROPIC_API_KEY")
	origOpenAI := os.Getenv("OPENAI_API_KEY")
	origGitHub := os.Getenv("GITHUB_TOKEN")
	defer func() {
		restoreEnv("ANTHROPIC_API_KEY", origAnthropic)
		restoreEnv("OPENAI_API_KEY", origOpenAI)
		restoreEnv("GITHUB_TOKEN", origGitHub)
		config.SetDecryptedSecrets(nil)
	}()

	os.Unsetenv("ANTHROPIC_API_KEY")
	os.Unsetenv("OPENAI_API_KEY")
	os.Unsetenv("GITHUB_TOKEN")

	// Set keys via secrets store
	config.SetDecryptedSecrets(&config.StructuredSecrets{
		System: map[string]string{
			"ANTHROPIC_API_KEY": "sk-ant-test",
			"OPENAI_API_KEY":    "sk-openai-test",
			"GITHUB_TOKEN":      "ghp_test",
		},
		User: map[string]string{},
	})

	cfg, _ := config.GetConfig()
	keys, allPresent := CheckRequiredAPIKeys(&cfg)

	if !allPresent {
		t.Error("Expected allPresent=true when all keys are in secrets store")
	}
	for _, k := range keys {
		if !k.Present {
			t.Errorf("Expected key %s to be present from secrets store", k.EnvVarName)
		}
	}
}

func restoreEnv(key, value string) {
	if value != "" {
		os.Setenv(key, value)
	} else {
		os.Unsetenv(key)
	}
}
