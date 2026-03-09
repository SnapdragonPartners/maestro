package preflight

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/config"
)

// KeyStatus represents the result of validating a single API key.
type KeyStatus string

// Key validation status constants.
const (
	KeyStatusMissing      KeyStatus = "missing"
	KeyStatusValid        KeyStatus = "valid"
	KeyStatusUnauthorized KeyStatus = "unauthorized"
	KeyStatusForbidden    KeyStatus = "forbidden"
	KeyStatusUnreachable  KeyStatus = "unreachable"
	KeyStatusError        KeyStatus = "error"
)

// KeyCheckResult represents the outcome of validating a single provider's key.
type KeyCheckResult struct {
	Provider  Provider  `json:"provider"`
	EnvVar    string    `json:"env_var"`
	Status    KeyStatus `json:"status"`
	Message   string    `json:"message"`
	LatencyMs int64     `json:"latency_ms"`
}

// perProviderTimeout is the maximum time to wait for a single provider check.
const perProviderTimeout = 15 * time.Second

// validationModels maps providers to cheap models for key validation.
var validationModels = map[Provider]string{ //nolint:gochecknoglobals // static lookup table
	ProviderAnthropic: "claude-sonnet-4-5",
	ProviderOpenAI:    "gpt-4o-mini",
	ProviderGoogle:    "gemini-3-pro-preview",
}

// providerToConfig maps preflight Provider to config provider string.
var providerToConfig = map[Provider]string{ //nolint:gochecknoglobals // static lookup table
	ProviderAnthropic: config.ProviderAnthropic,
	ProviderOpenAI:    config.ProviderOpenAI,
	ProviderGoogle:    config.ProviderGoogle,
}

// validateMu prevents concurrent ValidateKeys calls (e.g., button spam).
var validateMu sync.Mutex //nolint:gochecknoglobals // protects against concurrent validation runs

// ValidateKeys checks all configured API keys by making real API calls.
// Keys that are present get validated; missing keys are reported as "missing".
// All providers are checked concurrently with per-provider timeouts.
func ValidateKeys(ctx context.Context) []KeyCheckResult {
	validateMu.Lock()
	defer validateMu.Unlock()

	// Check all providers that have env var mappings (LLM + GitHub)
	providers := []Provider{ProviderAnthropic, ProviderOpenAI, ProviderGoogle, ProviderGitHub}

	var wg sync.WaitGroup
	results := make([]KeyCheckResult, len(providers))

	for i, provider := range providers {
		envVar := providerEnvVar[provider]
		value, _ := config.GetSystemSecret(envVar)

		if value == "" {
			results[i] = KeyCheckResult{
				Provider: provider,
				EnvVar:   envVar,
				Status:   KeyStatusMissing,
				Message:  "Not configured",
			}
			continue
		}

		wg.Add(1)
		go func(idx int, p Provider, key string) {
			defer wg.Done()
			providerCtx, cancel := context.WithTimeout(ctx, perProviderTimeout)
			defer cancel()
			results[idx] = validateProvider(providerCtx, p, key)
		}(i, provider, value)
	}

	wg.Wait()
	return results
}

// validateProvider validates a single provider's API key.
func validateProvider(ctx context.Context, provider Provider, apiKey string) KeyCheckResult {
	envVar := providerEnvVar[provider]
	start := time.Now()

	var err error
	switch provider {
	case ProviderAnthropic, ProviderOpenAI, ProviderGoogle:
		err = validateLLMKey(ctx, provider, apiKey)
	case ProviderGitHub:
		err = validateGitHubToken(ctx, apiKey)
	default:
		return KeyCheckResult{
			Provider: provider,
			EnvVar:   envVar,
			Status:   KeyStatusError,
			Message:  fmt.Sprintf("Unsupported provider: %s", provider),
		}
	}

	latency := time.Since(start).Milliseconds()

	if err == nil {
		return KeyCheckResult{
			Provider:  provider,
			EnvVar:    envVar,
			Status:    KeyStatusValid,
			Message:   "Key accepted",
			LatencyMs: latency,
		}
	}

	status := classifyError(err)
	return KeyCheckResult{
		Provider:  provider,
		EnvVar:    envVar,
		Status:    status,
		Message:   err.Error(),
		LatencyMs: latency,
	}
}

// validateLLMKey creates a throwaway client and sends a minimal completion request.
func validateLLMKey(ctx context.Context, provider Provider, apiKey string) error {
	model := validationModels[provider]
	configProvider := providerToConfig[provider]

	client, err := agent.CreateRawClient(configProvider, apiKey, model)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	req := llm.CompletionRequest{
		Messages:    []llm.CompletionMessage{llm.NewUserMessage("Say OK")},
		MaxTokens:   16,
		Temperature: 0,
	}

	_, err = client.Complete(ctx, req)
	if err != nil {
		return fmt.Errorf("completion failed: %w", err)
	}
	return nil
}

// validateGitHubToken validates a GitHub token via the REST API.
func validateGitHubToken(ctx context.Context, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user", http.NoBody)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "maestro-key-validator")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("network error: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body) // drain body for connection reuse

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		return fmt.Errorf("401 Unauthorized: invalid token")
	case http.StatusForbidden:
		return fmt.Errorf("403 Forbidden: token lacks required permissions")
	default:
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}
}

// errorPatterns maps error message substrings to KeyStatus values.
var errorPatterns = []struct { //nolint:gochecknoglobals // static classification table
	status   KeyStatus
	patterns []string
}{
	{KeyStatusUnauthorized, []string{"401", "unauthorized", "invalid_api_key", "authentication"}},
	{KeyStatusForbidden, []string{"403", "forbidden", "permission"}},
	{KeyStatusUnreachable, []string{"timeout", "connection refused", "no such host", "network", "unreachable", "dial", "deadline exceeded"}},
}

// classifyError maps an error to a KeyStatus based on error content.
func classifyError(err error) KeyStatus {
	if err == nil {
		return KeyStatusValid
	}

	// Check for context errors
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return KeyStatusUnreachable
	}

	lower := strings.ToLower(err.Error())

	for i := range errorPatterns {
		for _, pattern := range errorPatterns[i].patterns {
			if strings.Contains(lower, pattern) {
				return errorPatterns[i].status
			}
		}
	}

	return KeyStatusError
}
