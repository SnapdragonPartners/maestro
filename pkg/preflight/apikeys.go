package preflight

import (
	"orchestrator/pkg/config"
)

// ProviderKeyInfo describes a required API key and its current status.
type ProviderKeyInfo struct {
	EnvVarName  string   `json:"env_var"`
	Provider    Provider `json:"provider"`
	GuidanceURL string   `json:"guidance_url"`
	Present     bool     `json:"present"`
}

// providerEnvVar maps LLM/forge providers to their environment variable names.
var providerEnvVar = map[Provider]string{ //nolint:gochecknoglobals // static lookup table
	ProviderAnthropic: config.EnvAnthropicAPIKey,
	ProviderOpenAI:    config.EnvOpenAIAPIKey,
	ProviderGoogle:    config.EnvGoogleAPIKey,
	ProviderGitHub:    "GITHUB_TOKEN",
}

// providerGuidanceURL maps providers to where users can obtain API keys.
var providerGuidanceURL = map[Provider]string{ //nolint:gochecknoglobals // static lookup table
	ProviderAnthropic: "https://console.anthropic.com/",
	ProviderOpenAI:    "https://platform.openai.com/api-keys",
	ProviderGoogle:    "https://aistudio.google.com/app/apikey",
	ProviderGitHub:    "https://github.com/settings/tokens",
}

// CheckRequiredAPIKeys determines which API keys are needed for the current
// configuration and whether each is present. It skips providers that don't
// use API keys (Docker, Gitea, Ollama).
func CheckRequiredAPIKeys(cfg *config.Config) ([]ProviderKeyInfo, bool) {
	required := RequiredProviders(cfg)

	keys := make([]ProviderKeyInfo, 0, len(required))
	allPresent := true

	for _, provider := range required {
		envVar, ok := providerEnvVar[provider]
		if !ok {
			// Skip providers without API keys (Docker, Gitea, Ollama)
			continue
		}

		value, _ := config.GetSystemSecret(envVar)
		present := value != ""

		keys = append(keys, ProviderKeyInfo{
			EnvVarName:  envVar,
			Provider:    provider,
			GuidanceURL: providerGuidanceURL[provider],
			Present:     present,
		})

		if !present {
			allPresent = false
		}
	}

	return keys, allPresent
}
