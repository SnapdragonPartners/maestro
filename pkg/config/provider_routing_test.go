package config

import "testing"

// TestGetModelProviderRouting pins provider inference from model-name
// prefixes — in particular that gpt-oss (OpenAI's open-weight model, run
// locally via Ollama) routes to Ollama and is not misrouted to the hosted
// OpenAI API by the "gpt" prefix rule (first prefix match wins, so the
// gpt-oss entry must precede it).
func TestGetModelProviderRouting(t *testing.T) {
	cases := map[string]string{
		"gpt-oss:20b":       ProviderOllama,
		"gpt-oss":           ProviderOllama,
		"gpt-4o":            ProviderOpenAI,
		"gpt-4.1":           ProviderOpenAI,
		"claude-sonnet-4-6": ProviderAnthropic,
		"qwen3-coder:30b":   ProviderOllama,
		"mistral-small3.2:24b-instruct-2506-q8_0": ProviderOllama,
		"gemini-2.5-pro": ProviderGoogle,
	}
	for model, want := range cases {
		got, err := GetModelProvider(model)
		if err != nil {
			t.Errorf("GetModelProvider(%q) unexpected error: %v", model, err)
			continue
		}
		if got != want {
			t.Errorf("GetModelProvider(%q) = %q, want %q", model, got, want)
		}
	}
}
