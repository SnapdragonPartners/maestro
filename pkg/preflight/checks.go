package preflight

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/forge"
)

// checkDocker verifies Docker is available and running.
func checkDocker(ctx context.Context) CheckResult {
	result := CheckResult{Provider: ProviderDocker}

	// Check if docker command exists
	cmd := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}")
	output, err := cmd.Output()
	if err != nil {
		result.Passed = false
		result.Message = "Docker is not running or not installed"
		result.Error = err
		return result
	}

	version := strings.TrimSpace(string(output))
	result.Passed = true
	result.Message = fmt.Sprintf("Docker %s is running", version)
	return result
}

// checkGitHub verifies GitHub token and gh CLI are available.
func checkGitHub(ctx context.Context) CheckResult {
	result := CheckResult{Provider: ProviderGitHub}

	// Check for GITHUB_TOKEN environment variable
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		result.Passed = false
		result.Message = "GITHUB_TOKEN environment variable is not set"
		result.Error = fmt.Errorf("missing GITHUB_TOKEN")
		return result
	}

	// Check if gh CLI is available
	cmd := exec.CommandContext(ctx, "gh", "--version")
	if err := cmd.Run(); err != nil {
		result.Passed = false
		result.Message = "GitHub CLI (gh) is not installed"
		result.Error = err
		return result
	}

	result.Passed = true
	result.Message = "GitHub token and CLI available"
	return result
}

// checkGitea verifies local Gitea instance is available.
func checkGitea(ctx context.Context, _ *config.Config) CheckResult {
	result := CheckResult{Provider: ProviderGitea}

	// Try to load forge state to get Gitea URL
	projectDir := config.GetProjectDir()
	state, err := forge.LoadState(projectDir)
	if err != nil {
		// Gitea not yet configured - this is OK during initial setup
		// The Gitea lifecycle manager will set this up
		result.Passed = true
		result.Message = "Gitea not yet configured (will be started)"
		return result
	}

	// Verify Gitea is reachable
	client := &http.Client{Timeout: 5 * time.Second}
	healthURL := fmt.Sprintf("%s/api/v1/version", state.URL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, http.NoBody)
	if err != nil {
		result.Passed = false
		result.Message = "Failed to create health check request"
		result.Error = err
		return result
	}

	resp, err := client.Do(req)
	if err != nil {
		result.Passed = false
		result.Message = fmt.Sprintf("Cannot reach Gitea at %s", state.URL)
		result.Error = err
		return result
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		result.Passed = false
		result.Message = fmt.Sprintf("Gitea health check failed (status %d)", resp.StatusCode)
		result.Error = fmt.Errorf("gitea returned status %d", resp.StatusCode)
		return result
	}

	result.Passed = true
	result.Message = fmt.Sprintf("Gitea is healthy at %s", state.URL)
	return result
}

// checkOpenAI verifies OpenAI API key is available.
func checkOpenAI(_ context.Context) CheckResult {
	result := CheckResult{Provider: ProviderOpenAI}

	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		result.Passed = false
		result.Message = "OPENAI_API_KEY environment variable is not set"
		result.Error = fmt.Errorf("missing OPENAI_API_KEY")
		return result
	}

	// Optionally verify the key works (skip for now to avoid API calls)
	result.Passed = true
	result.Message = "OpenAI API key is configured"
	return result
}

// checkAnthropic verifies Anthropic API key is available.
func checkAnthropic(_ context.Context) CheckResult {
	result := CheckResult{Provider: ProviderAnthropic}

	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		result.Passed = false
		result.Message = "ANTHROPIC_API_KEY environment variable is not set"
		result.Error = fmt.Errorf("missing ANTHROPIC_API_KEY")
		return result
	}

	result.Passed = true
	result.Message = "Anthropic API key is configured"
	return result
}

// checkGoogle verifies Google AI API key is available.
func checkGoogle(_ context.Context) CheckResult {
	result := CheckResult{Provider: ProviderGoogle}

	key := os.Getenv("GOOGLE_GENAI_API_KEY")
	if key == "" {
		result.Passed = false
		result.Message = "GOOGLE_GENAI_API_KEY environment variable is not set"
		result.Error = fmt.Errorf("missing GOOGLE_GENAI_API_KEY")
		return result
	}

	result.Passed = true
	result.Message = "Google AI API key is configured"
	return result
}

// OllamaModel represents a model from Ollama's API.
type OllamaModel struct {
	Name string `json:"name"`
}

// OllamaModelsResponse represents the response from Ollama's /api/tags endpoint.
type OllamaModelsResponse struct {
	Models []OllamaModel `json:"models"`
}

// checkOllama verifies Ollama is reachable and required models are available.
func checkOllama(ctx context.Context, cfg *config.Config) CheckResult {
	result := CheckResult{Provider: ProviderOllama}

	// Default Ollama URL
	ollamaURL := os.Getenv("OLLAMA_HOST")
	if ollamaURL == "" {
		ollamaURL = "http://localhost:11434"
	}

	// Check Ollama is reachable
	client := &http.Client{Timeout: 5 * time.Second}
	tagsURL := fmt.Sprintf("%s/api/tags", ollamaURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tagsURL, http.NoBody)
	if err != nil {
		result.Passed = false
		result.Message = "Failed to create Ollama request"
		result.Error = err
		return result
	}

	resp, err := client.Do(req)
	if err != nil {
		result.Passed = false
		result.Message = fmt.Sprintf("Cannot reach Ollama at %s", ollamaURL)
		result.Error = err
		return result
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		result.Passed = false
		result.Message = fmt.Sprintf("Ollama returned status %d", resp.StatusCode)
		result.Error = fmt.Errorf("ollama returned status %d", resp.StatusCode)
		return result
	}

	// Parse available models
	var modelsResp OllamaModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&modelsResp); err != nil {
		result.Passed = false
		result.Message = "Failed to parse Ollama models response"
		result.Error = err
		return result
	}

	// Build set of available models
	availableModels := make(map[string]bool)
	for _, m := range modelsResp.Models {
		availableModels[m.Name] = true
	}

	// Check required models are available
	requiredModels := getOllamaModels(cfg)
	var missingModels []string

	for _, model := range requiredModels {
		if !availableModels[model] {
			missingModels = append(missingModels, model)
		}
	}

	if len(missingModels) > 0 {
		result.Passed = false
		result.Message = fmt.Sprintf("Missing Ollama models: %s", strings.Join(missingModels, ", "))
		result.Error = fmt.Errorf("missing models: %v", missingModels)
		return result
	}

	result.Passed = true
	result.Message = fmt.Sprintf("Ollama is running with %d models available", len(modelsResp.Models))
	return result
}

// getOllamaModels returns the list of Ollama models that need to be available.
func getOllamaModels(_ *config.Config) []string {
	models := make(map[string]bool)

	// Check each effective model
	effectiveModels := []string{
		config.GetEffectiveCoderModel(),
		config.GetEffectiveArchitectModel(),
		config.GetEffectivePMModel(),
	}

	for _, model := range effectiveModels {
		// Only include Ollama models (contain colon, not cloud provider prefixes)
		if detectLLMProvider(model) == ProviderOllama {
			models[model] = true
		}
	}

	result := make([]string, 0, len(models))
	for m := range models {
		result = append(result, m)
	}
	return result
}
