// Package preflight provides pre-startup validation for Maestro.
// It validates that all required services and credentials are available
// based on which providers the configured models actually require.
package preflight

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"orchestrator/pkg/config"
)

// Provider represents a service provider that may need validation.
type Provider string

// Provider constants for supported service providers.
const (
	ProviderGitHub    Provider = "github"
	ProviderGitea     Provider = "gitea"
	ProviderOpenAI    Provider = "openai"
	ProviderAnthropic Provider = "anthropic"
	ProviderGoogle    Provider = "google"
	ProviderOllama    Provider = "ollama"
	ProviderDocker    Provider = "docker"
)

// CheckResult represents the outcome of a single preflight check.
type CheckResult struct {
	Error    error
	Message  string
	Provider Provider
	Passed   bool
}

// Results contains all preflight check results.
type Results struct {
	Summary string
	Checks  []CheckResult
	Passed  bool
}

// RequiredProviders determines which providers are needed based on configured models.
// This is provider-aware, not just mode-aware - allowing flexibility like using
// Ollama models in standard mode.
func RequiredProviders(_ *config.Config) []Provider {
	providers := make(map[Provider]bool)

	// Docker is always required
	providers[ProviderDocker] = true

	// Determine git forge provider
	if config.IsAirplaneMode() {
		providers[ProviderGitea] = true
	} else {
		providers[ProviderGitHub] = true
	}

	// Check each agent model to determine LLM providers
	models := []string{
		config.GetEffectiveCoderModel(),
		config.GetEffectiveArchitectModel(),
		config.GetEffectivePMModel(),
	}

	for _, model := range models {
		provider := detectLLMProvider(model)
		if provider != "" {
			providers[provider] = true
		}
	}

	// Convert map to slice
	result := make([]Provider, 0, len(providers))
	for p := range providers {
		result = append(result, p)
	}

	return result
}

// detectLLMProvider determines which provider a model name belongs to.
func detectLLMProvider(model string) Provider {
	model = strings.ToLower(model)

	// Ollama models contain a colon (e.g., "mistral-nemo:latest", "llama3.1:70b")
	// or are in Ollama's model format
	if strings.Contains(model, ":") && !strings.HasPrefix(model, "claude-") &&
		!strings.HasPrefix(model, "gpt-") && !strings.HasPrefix(model, "o3") &&
		!strings.HasPrefix(model, "gemini-") {
		return ProviderOllama
	}

	// OpenAI models
	if strings.HasPrefix(model, "gpt-") || strings.HasPrefix(model, "o3") ||
		strings.HasPrefix(model, "o1") {
		return ProviderOpenAI
	}

	// Anthropic models
	if strings.HasPrefix(model, "claude-") {
		return ProviderAnthropic
	}

	// Google models
	if strings.HasPrefix(model, "gemini-") {
		return ProviderGoogle
	}

	// Unknown provider - could be custom/local
	return ""
}

// Run executes all preflight checks for the required providers.
func Run(ctx context.Context, cfg *config.Config) (*Results, error) {
	required := RequiredProviders(cfg)

	results := &Results{
		Checks: make([]CheckResult, 0, len(required)),
		Passed: true,
	}

	var checkErrors []string

	for _, provider := range required {
		result := runCheck(ctx, provider, cfg)
		results.Checks = append(results.Checks, result)

		if !result.Passed {
			results.Passed = false
			checkErrors = append(checkErrors, fmt.Sprintf("%s: %s", provider, result.Message))
		}
	}

	if results.Passed {
		results.Summary = fmt.Sprintf("All %d preflight checks passed", len(results.Checks))
	} else {
		results.Summary = fmt.Sprintf("%d of %d preflight checks failed",
			len(checkErrors), len(results.Checks))
	}

	return results, nil
}

// runCheck executes a single provider check.
func runCheck(ctx context.Context, provider Provider, cfg *config.Config) CheckResult {
	switch provider {
	case ProviderDocker:
		return checkDocker(ctx)
	case ProviderGitHub:
		return checkGitHub(ctx)
	case ProviderGitea:
		return checkGitea(ctx, cfg)
	case ProviderOpenAI:
		return checkOpenAI(ctx)
	case ProviderAnthropic:
		return checkAnthropic(ctx)
	case ProviderGoogle:
		return checkGoogle(ctx)
	case ProviderOllama:
		return checkOllama(ctx, cfg)
	default:
		return CheckResult{
			Provider: provider,
			Passed:   false,
			Message:  "Unknown provider",
			Error:    fmt.Errorf("unknown provider: %s", provider),
		}
	}
}

// Validate is a convenience function that runs preflight checks and returns
// an error if any checks fail. Use this for simple pass/fail validation.
func Validate(ctx context.Context, cfg *config.Config) error {
	results, err := Run(ctx, cfg)
	if err != nil {
		return fmt.Errorf("preflight check error: %w", err)
	}

	if !results.Passed {
		var failedChecks []string
		for i := range results.Checks {
			if !results.Checks[i].Passed {
				failedChecks = append(failedChecks, FormatCheckError(results.Checks[i]))
			}
		}
		return errors.New(strings.Join(failedChecks, "\n"))
	}

	return nil
}
