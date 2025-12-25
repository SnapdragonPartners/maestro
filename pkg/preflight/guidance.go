package preflight

import (
	"fmt"
	"strings"
)

// FormatCheckError formats a failed check result with actionable guidance.
func FormatCheckError(check CheckResult) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("  %s: %s\n", check.Provider, check.Message))
	sb.WriteString(fmt.Sprintf("    %s\n", getGuidance(check.Provider)))

	return sb.String()
}

// FormatResults formats all preflight results for display.
func FormatResults(results *Results) string {
	var sb strings.Builder

	if results.Passed {
		sb.WriteString("Preflight checks passed\n")
		for i := range results.Checks {
			sb.WriteString(fmt.Sprintf("  [PASS] %s: %s\n", results.Checks[i].Provider, results.Checks[i].Message))
		}
	} else {
		sb.WriteString("Preflight checks failed\n\n")
		sb.WriteString("Failed checks:\n")
		for i := range results.Checks {
			if !results.Checks[i].Passed {
				sb.WriteString(FormatCheckError(results.Checks[i]))
				sb.WriteString("\n")
			}
		}

		sb.WriteString("Passed checks:\n")
		for i := range results.Checks {
			if results.Checks[i].Passed {
				sb.WriteString(fmt.Sprintf("  [PASS] %s: %s\n", results.Checks[i].Provider, results.Checks[i].Message))
			}
		}
	}

	return sb.String()
}

// getGuidance returns actionable guidance for fixing a failed check.
func getGuidance(provider Provider) string {
	switch provider {
	case ProviderDocker:
		return "Install Docker Desktop or ensure Docker daemon is running: https://docs.docker.com/get-docker/"

	case ProviderGitHub:
		return "Set GITHUB_TOKEN environment variable and install gh CLI: https://cli.github.com/"

	case ProviderGitea:
		return "Gitea will be started automatically in airplane mode. If this error persists, check Docker is running."

	case ProviderOpenAI:
		return "Set OPENAI_API_KEY environment variable: https://platform.openai.com/api-keys"

	case ProviderAnthropic:
		return "Set ANTHROPIC_API_KEY environment variable: https://console.anthropic.com/"

	case ProviderGoogle:
		return "Set GOOGLE_GENAI_API_KEY environment variable: https://aistudio.google.com/app/apikey"

	case ProviderOllama:
		return "Install and start Ollama, then pull required models:\n" +
			"    brew install ollama && ollama serve\n" +
			"    ollama pull mistral-nemo:latest"

	default:
		return "Check the provider documentation for setup instructions."
	}
}

// FormatModeInfo returns a summary of what the current mode requires.
func FormatModeInfo(mode string) string {
	if mode == "airplane" {
		return `Airplane Mode Requirements:
  - Docker (for containers)
  - Ollama (for local LLM inference)
  - Required models pulled in Ollama

Note: Gitea will be started automatically for git hosting.
Cloud API keys (OpenAI, Anthropic, Google) are NOT required.`
	}

	return `Standard Mode Requirements:
  - Docker (for containers)
  - GITHUB_TOKEN environment variable
  - gh CLI installed
  - API keys for configured models:
    - OPENAI_API_KEY (for o3/gpt models)
    - ANTHROPIC_API_KEY (for Claude models)
    - GOOGLE_GENAI_API_KEY (for Gemini models)

Note: Only keys for models you're using are required.`
}

// FormatProviderList returns a formatted list of required providers.
func FormatProviderList(providers []Provider) string {
	var sb strings.Builder
	sb.WriteString("Required providers:\n")
	for _, p := range providers {
		sb.WriteString(fmt.Sprintf("  - %s\n", p))
	}
	return sb.String()
}
