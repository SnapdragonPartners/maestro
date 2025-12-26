package preflight

import (
	"context"
	"os"
	"testing"

	"orchestrator/pkg/config"
)

// setupTestConfig creates a test configuration with specific models.
func setupTestConfig(t *testing.T, coderModel, architectModel, pmModel, mode string) func() {
	t.Helper()

	// Create a minimal config for testing
	cfg := &config.Config{
		OperatingMode: mode,
		Agents: &config.AgentConfig{
			CoderModel:     coderModel,
			ArchitectModel: architectModel,
			PMModel:        pmModel,
			Airplane: &config.AirplaneAgentConfig{
				CoderModel:     "mistral-nemo:latest",
				ArchitectModel: "mistral-nemo:latest",
				PMModel:        "mistral-nemo:latest",
			},
		},
	}

	// Set the global config (this is a bit of a hack for testing)
	config.SetConfigForTesting(cfg)

	return func() {
		config.SetConfigForTesting(nil)
	}
}

// TestDetectLLMProvider tests provider detection from model names.
func TestDetectLLMProvider(t *testing.T) {
	tests := []struct {
		model    string
		expected Provider
	}{
		// OpenAI models
		{"gpt-4", ProviderOpenAI},
		{"gpt-4-turbo", ProviderOpenAI},
		{"o3", ProviderOpenAI},
		{"o3-mini", ProviderOpenAI},
		{"o1-preview", ProviderOpenAI},

		// Anthropic models
		{"claude-3-opus", ProviderAnthropic},
		{"claude-sonnet-4-5", ProviderAnthropic},
		{"claude-3-haiku", ProviderAnthropic},

		// Google models
		{"gemini-pro", ProviderGoogle},
		{"gemini-1.5-flash", ProviderGoogle},
		{"gemini-3-pro-preview", ProviderGoogle},

		// Ollama models (contain colon)
		{"mistral-nemo:latest", ProviderOllama},
		{"llama3.1:70b", ProviderOllama},
		{"qwen2.5-coder:32b", ProviderOllama},
		{"deepseek-coder:33b", ProviderOllama},

		// Unknown/custom models
		{"custom-model", ""},
		{"local-llm", ""},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got := detectLLMProvider(tt.model)
			if got != tt.expected {
				t.Errorf("detectLLMProvider(%q) = %q, want %q", tt.model, got, tt.expected)
			}
		})
	}
}

// TestRequiredProviders_StandardMode tests provider detection in standard mode.
func TestRequiredProviders_StandardMode(t *testing.T) {
	cleanup := setupTestConfig(t,
		"claude-sonnet-4-5",    // Coder: Anthropic
		"gemini-3-pro-preview", // Architect: Google
		"claude-opus-4-5",      // PM: Anthropic
		config.OperatingModeStandard,
	)
	defer cleanup()

	cfg, _ := config.GetConfig()
	providers := RequiredProviders(&cfg)

	// Should require: Docker, GitHub, Anthropic, Google
	providerSet := make(map[Provider]bool)
	for _, p := range providers {
		providerSet[p] = true
	}

	if !providerSet[ProviderDocker] {
		t.Error("Standard mode should require Docker")
	}
	if !providerSet[ProviderGitHub] {
		t.Error("Standard mode should require GitHub")
	}
	if !providerSet[ProviderAnthropic] {
		t.Error("Standard mode with Claude models should require Anthropic")
	}
	if !providerSet[ProviderGoogle] {
		t.Error("Standard mode with Gemini model should require Google")
	}
	if providerSet[ProviderGitea] {
		t.Error("Standard mode should NOT require Gitea")
	}
	if providerSet[ProviderOllama] {
		t.Error("Standard mode with cloud models should NOT require Ollama")
	}
}

// TestRequiredProviders_AirplaneMode tests provider detection in airplane mode.
func TestRequiredProviders_AirplaneMode(t *testing.T) {
	cleanup := setupTestConfig(t,
		"mistral-nemo:latest", // Coder: Ollama
		"mistral-nemo:latest", // Architect: Ollama
		"mistral-nemo:latest", // PM: Ollama
		config.OperatingModeAirplane,
	)
	defer cleanup()

	cfg, _ := config.GetConfig()
	providers := RequiredProviders(&cfg)

	providerSet := make(map[Provider]bool)
	for _, p := range providers {
		providerSet[p] = true
	}

	if !providerSet[ProviderDocker] {
		t.Error("Airplane mode should require Docker")
	}
	if !providerSet[ProviderGitea] {
		t.Error("Airplane mode should require Gitea")
	}
	if !providerSet[ProviderOllama] {
		t.Error("Airplane mode with Ollama models should require Ollama")
	}
	if providerSet[ProviderGitHub] {
		t.Error("Airplane mode should NOT require GitHub")
	}
	if providerSet[ProviderAnthropic] {
		t.Error("Airplane mode with Ollama models should NOT require Anthropic")
	}
}

// TestRequiredProviders_MixedProviders tests using Ollama in standard mode.
func TestRequiredProviders_MixedProviders(t *testing.T) {
	// Use Ollama for coder but cloud for others - this should require both
	cleanup := setupTestConfig(t,
		"mistral-nemo:latest",  // Coder: Ollama (local)
		"gemini-3-pro-preview", // Architect: Google (cloud)
		"claude-opus-4-5",      // PM: Anthropic (cloud)
		config.OperatingModeStandard,
	)
	defer cleanup()

	cfg, _ := config.GetConfig()
	providers := RequiredProviders(&cfg)

	providerSet := make(map[Provider]bool)
	for _, p := range providers {
		providerSet[p] = true
	}

	// Should require both Ollama and cloud providers
	if !providerSet[ProviderOllama] {
		t.Error("Mixed mode with Ollama coder should require Ollama")
	}
	if !providerSet[ProviderGoogle] {
		t.Error("Mixed mode with Gemini architect should require Google")
	}
	if !providerSet[ProviderAnthropic] {
		t.Error("Mixed mode with Claude PM should require Anthropic")
	}
	if !providerSet[ProviderGitHub] {
		t.Error("Standard mode should require GitHub even with mixed models")
	}
}

// TestCheckDocker tests Docker availability check.
func TestCheckDocker(t *testing.T) {
	ctx := context.Background()
	result := checkDocker(ctx)

	// Docker should be available in development environment
	// This test may fail in CI without Docker
	if result.Passed {
		if result.Message == "" {
			t.Error("Passed check should have a message")
		}
	} else {
		t.Logf("Docker check failed (may be expected in some environments): %s", result.Message)
	}
}

// TestCheckGitHub tests GitHub token check.
func TestCheckGitHub(t *testing.T) {
	ctx := context.Background()

	// Save and clear GITHUB_TOKEN
	originalToken := os.Getenv("GITHUB_TOKEN")
	os.Unsetenv("GITHUB_TOKEN")
	defer func() {
		if originalToken != "" {
			os.Setenv("GITHUB_TOKEN", originalToken)
		}
	}()

	result := checkGitHub(ctx)
	if result.Passed {
		t.Error("GitHub check should fail without GITHUB_TOKEN")
	}
	if result.Error == nil {
		t.Error("Failed check should have an error")
	}

	// Set a token and verify it passes the token check
	os.Setenv("GITHUB_TOKEN", "test-token")
	result = checkGitHub(ctx)
	// Note: This may still fail if gh CLI is not installed, which is OK
	// We just want to verify the token check doesn't fail with "not set" message
	if result.Message == "GITHUB_TOKEN environment variable is not set" {
		t.Error("GitHub check should not report token missing when it is set")
	}
}

// TestCheckOpenAI tests OpenAI API key check.
func TestCheckOpenAI(t *testing.T) {
	ctx := context.Background()

	// Save and clear OPENAI_API_KEY
	originalKey := os.Getenv("OPENAI_API_KEY")
	os.Unsetenv("OPENAI_API_KEY")
	defer func() {
		if originalKey != "" {
			os.Setenv("OPENAI_API_KEY", originalKey)
		}
	}()

	result := checkOpenAI(ctx)
	if result.Passed {
		t.Error("OpenAI check should fail without OPENAI_API_KEY")
	}

	os.Setenv("OPENAI_API_KEY", "test-key")
	result = checkOpenAI(ctx)
	if !result.Passed {
		t.Error("OpenAI check should pass with OPENAI_API_KEY set")
	}
}

// TestCheckAnthropic tests Anthropic API key check.
func TestCheckAnthropic(t *testing.T) {
	ctx := context.Background()

	// Save and clear ANTHROPIC_API_KEY
	originalKey := os.Getenv("ANTHROPIC_API_KEY")
	os.Unsetenv("ANTHROPIC_API_KEY")
	defer func() {
		if originalKey != "" {
			os.Setenv("ANTHROPIC_API_KEY", originalKey)
		}
	}()

	result := checkAnthropic(ctx)
	if result.Passed {
		t.Error("Anthropic check should fail without ANTHROPIC_API_KEY")
	}

	os.Setenv("ANTHROPIC_API_KEY", "test-key")
	result = checkAnthropic(ctx)
	if !result.Passed {
		t.Error("Anthropic check should pass with ANTHROPIC_API_KEY set")
	}
}

// TestCheckGoogle tests Google API key check.
func TestCheckGoogle(t *testing.T) {
	ctx := context.Background()

	// Save and clear GOOGLE_GENAI_API_KEY
	originalKey := os.Getenv("GOOGLE_GENAI_API_KEY")
	os.Unsetenv("GOOGLE_GENAI_API_KEY")
	defer func() {
		if originalKey != "" {
			os.Setenv("GOOGLE_GENAI_API_KEY", originalKey)
		}
	}()

	result := checkGoogle(ctx)
	if result.Passed {
		t.Error("Google check should fail without GOOGLE_GENAI_API_KEY")
	}

	os.Setenv("GOOGLE_GENAI_API_KEY", "test-key")
	result = checkGoogle(ctx)
	if !result.Passed {
		t.Error("Google check should pass with GOOGLE_GENAI_API_KEY set")
	}
}

// TestFormatCheckError tests error formatting.
func TestFormatCheckError(t *testing.T) {
	check := CheckResult{
		Provider: ProviderDocker,
		Passed:   false,
		Message:  "Docker is not running",
	}

	formatted := FormatCheckError(check)
	if formatted == "" {
		t.Error("FormatCheckError should return non-empty string")
	}
	if !contains(formatted, "docker") {
		t.Error("Formatted error should mention the provider")
	}
	if !contains(formatted, "Docker is not running") {
		t.Error("Formatted error should include the message")
	}
}

// TestFormatResults tests results formatting.
func TestFormatResults(t *testing.T) {
	results := &Results{
		Passed: true,
		Checks: []CheckResult{
			{Provider: ProviderDocker, Passed: true, Message: "Docker OK"},
			{Provider: ProviderGitHub, Passed: true, Message: "GitHub OK"},
		},
	}

	formatted := FormatResults(results)
	if !contains(formatted, "passed") {
		t.Error("Passed results should indicate success")
	}

	// Test failed results
	results.Passed = false
	results.Checks[1].Passed = false
	results.Checks[1].Message = "GitHub token missing"

	formatted = FormatResults(results)
	if !contains(formatted, "failed") {
		t.Error("Failed results should indicate failure")
	}
}

// TestFormatModeInfo tests mode information formatting.
func TestFormatModeInfo(t *testing.T) {
	airplaneInfo := FormatModeInfo("airplane")
	if !contains(airplaneInfo, "Ollama") {
		t.Error("Airplane mode info should mention Ollama")
	}
	if !contains(airplaneInfo, "Gitea") {
		t.Error("Airplane mode info should mention Gitea")
	}

	standardInfo := FormatModeInfo("standard")
	if !contains(standardInfo, "GITHUB_TOKEN") {
		t.Error("Standard mode info should mention GITHUB_TOKEN")
	}
}

// contains is a helper to check string containment.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsHelper(s, substr)
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
