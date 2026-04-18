package preflight

import (
	"context"
	"os"
	"testing"

	"orchestrator/pkg/config"
)

// fakeValidator returns a ValidatorFunc that returns the given results.
func fakeValidator(results []KeyCheckResult) ValidatorFunc {
	return func(_ context.Context, _ *config.Config) []KeyCheckResult {
		return results
	}
}

func setupReadinessTestConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := &config.Config{
		OperatingMode: config.OperatingModeStandard,
		Agents: &config.AgentConfig{
			CoderModel:     "claude-sonnet-4-5",
			ArchitectModel: "o3-mini",
			PMModel:        "claude-opus-4-5",
		},
	}
	config.SetConfigForTesting(cfg)
	t.Cleanup(func() { config.SetConfigForTesting(nil) })
	return cfg
}

func setTestKeys(t *testing.T, keys map[string]string) {
	t.Helper()
	for k, v := range keys {
		orig := os.Getenv(k)
		if v != "" {
			os.Setenv(k, v)
		} else {
			os.Unsetenv(k)
		}
		k2, orig2 := k, orig
		t.Cleanup(func() {
			if orig2 != "" {
				os.Setenv(k2, orig2)
			} else {
				os.Unsetenv(k2)
			}
		})
	}
}

var allKeysPresent = map[string]string{
	"ANTHROPIC_API_KEY": "sk-ant-test",
	"OPENAI_API_KEY":    "sk-openai-test",
	"GITHUB_TOKEN":      "ghp_test",
}

var allKeysMissing = map[string]string{
	"ANTHROPIC_API_KEY": "",
	"OPENAI_API_KEY":    "",
	"GITHUB_TOKEN":      "",
}

func TestEvaluateSetupReadiness(t *testing.T) {
	tests := []struct {
		name             string
		keys             map[string]string
		validatorResults []KeyCheckResult
		wantReady        bool
		wantAllPresent   bool
		wantErrors       int
		wantWarnings     int
	}{
		{
			name: "all_valid",
			keys: allKeysPresent,
			validatorResults: []KeyCheckResult{
				{Provider: ProviderAnthropic, EnvVar: "ANTHROPIC_API_KEY", Status: KeyStatusValid},
				{Provider: ProviderOpenAI, EnvVar: "OPENAI_API_KEY", Status: KeyStatusValid},
				{Provider: ProviderGitHub, EnvVar: "GITHUB_TOKEN", Status: KeyStatusValid},
			},
			wantReady:      true,
			wantAllPresent: true,
			wantErrors:     0,
			wantWarnings:   0,
		},
		{
			name:           "missing_keys_skips_validation",
			keys:           allKeysMissing,
			wantReady:      false,
			wantAllPresent: false,
			wantErrors:     0,
			wantWarnings:   0,
		},
		{
			name: "unauthorized_blocks",
			keys: allKeysPresent,
			validatorResults: []KeyCheckResult{
				{Provider: ProviderAnthropic, EnvVar: "ANTHROPIC_API_KEY", Status: KeyStatusUnauthorized, Message: "401"},
				{Provider: ProviderOpenAI, EnvVar: "OPENAI_API_KEY", Status: KeyStatusValid},
				{Provider: ProviderGitHub, EnvVar: "GITHUB_TOKEN", Status: KeyStatusValid},
			},
			wantReady:      false,
			wantAllPresent: true,
			wantErrors:     1,
			wantWarnings:   0,
		},
		{
			name: "forbidden_blocks",
			keys: allKeysPresent,
			validatorResults: []KeyCheckResult{
				{Provider: ProviderAnthropic, EnvVar: "ANTHROPIC_API_KEY", Status: KeyStatusValid},
				{Provider: ProviderOpenAI, EnvVar: "OPENAI_API_KEY", Status: KeyStatusForbidden, Message: "403"},
				{Provider: ProviderGitHub, EnvVar: "GITHUB_TOKEN", Status: KeyStatusValid},
			},
			wantReady:      false,
			wantAllPresent: true,
			wantErrors:     1,
			wantWarnings:   0,
		},
		{
			name: "unreachable_warns_but_allows",
			keys: allKeysPresent,
			validatorResults: []KeyCheckResult{
				{Provider: ProviderAnthropic, EnvVar: "ANTHROPIC_API_KEY", Status: KeyStatusValid},
				{Provider: ProviderOpenAI, EnvVar: "OPENAI_API_KEY", Status: KeyStatusUnreachable, Message: "timeout"},
				{Provider: ProviderGitHub, EnvVar: "GITHUB_TOKEN", Status: KeyStatusValid},
			},
			wantReady:      true,
			wantAllPresent: true,
			wantErrors:     0,
			wantWarnings:   1,
		},
		{
			name: "generic_error_warns_but_allows",
			keys: allKeysPresent,
			validatorResults: []KeyCheckResult{
				{Provider: ProviderAnthropic, EnvVar: "ANTHROPIC_API_KEY", Status: KeyStatusError, Message: "unexpected"},
				{Provider: ProviderOpenAI, EnvVar: "OPENAI_API_KEY", Status: KeyStatusValid},
				{Provider: ProviderGitHub, EnvVar: "GITHUB_TOKEN", Status: KeyStatusValid},
			},
			wantReady:      true,
			wantAllPresent: true,
			wantErrors:     0,
			wantWarnings:   1,
		},
		{
			name: "mixed_unauthorized_and_unreachable",
			keys: allKeysPresent,
			validatorResults: []KeyCheckResult{
				{Provider: ProviderAnthropic, EnvVar: "ANTHROPIC_API_KEY", Status: KeyStatusUnauthorized, Message: "bad key"},
				{Provider: ProviderOpenAI, EnvVar: "OPENAI_API_KEY", Status: KeyStatusUnreachable, Message: "timeout"},
				{Provider: ProviderGitHub, EnvVar: "GITHUB_TOKEN", Status: KeyStatusValid},
			},
			wantReady:      false,
			wantAllPresent: true,
			wantErrors:     1,
			wantWarnings:   1,
		},
		{
			name: "partial_keys_missing",
			keys: map[string]string{
				"ANTHROPIC_API_KEY": "sk-ant-test",
				"OPENAI_API_KEY":    "",
				"GITHUB_TOKEN":      "ghp_test",
			},
			wantReady:      false,
			wantAllPresent: false,
			wantErrors:     0,
			wantWarnings:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := setupReadinessTestConfig(t)
			setTestKeys(t, tt.keys)
			config.SetDecryptedSecrets(nil)

			if tt.validatorResults != nil {
				SetValidatorFunc(fakeValidator(tt.validatorResults))
			} else {
				// No validator needed — missing keys path skips validation entirely
				SetValidatorFunc(nil)
			}
			t.Cleanup(func() { SetValidatorFunc(nil) })

			result := EvaluateSetupReadiness(context.Background(), cfg)

			if result.Ready != tt.wantReady {
				t.Errorf("Ready = %v, want %v", result.Ready, tt.wantReady)
			}
			if result.AllPresent != tt.wantAllPresent {
				t.Errorf("AllPresent = %v, want %v", result.AllPresent, tt.wantAllPresent)
			}
			if len(result.ValidationErrors) != tt.wantErrors {
				t.Errorf("ValidationErrors = %d, want %d", len(result.ValidationErrors), tt.wantErrors)
			}
			if len(result.Warnings) != tt.wantWarnings {
				t.Errorf("Warnings = %d, want %d", len(result.Warnings), tt.wantWarnings)
			}
		})
	}
}

func TestSetValidatorFunc_NilRestoresDefault(t *testing.T) {
	// Setting nil should make EvaluateSetupReadiness use ValidateRequiredKeys.
	// We can't call it with real keys, but we verify the variable is nil.
	SetValidatorFunc(func(_ context.Context, _ *config.Config) []KeyCheckResult {
		return nil
	})
	if validatorFunc == nil {
		t.Error("expected non-nil after set")
	}
	SetValidatorFunc(nil)
	if validatorFunc != nil {
		t.Error("expected nil after reset")
	}
}
