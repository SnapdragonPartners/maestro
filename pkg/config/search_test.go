package config

import (
	"os"
	"testing"
)

func TestDetectSearchAPIs_NoKeys(t *testing.T) {
	// Clear any existing keys
	oldAPIKey := os.Getenv(EnvGoogleSearchAPIKey)
	oldCX := os.Getenv(EnvGoogleSearchCX)
	defer func() {
		os.Setenv(EnvGoogleSearchAPIKey, oldAPIKey)
		os.Setenv(EnvGoogleSearchCX, oldCX)
	}()
	os.Unsetenv(EnvGoogleSearchAPIKey)
	os.Unsetenv(EnvGoogleSearchCX)

	status := DetectSearchAPIs()
	if status.Available {
		t.Error("Expected Available=false when no keys set")
	}
	if status.Provider != SearchProviderNone {
		t.Errorf("Expected Provider=SearchProviderNone, got %q", status.Provider)
	}
}

func TestDetectSearchAPIs_GoogleKeys(t *testing.T) {
	// Set Google keys
	oldAPIKey := os.Getenv(EnvGoogleSearchAPIKey)
	oldCX := os.Getenv(EnvGoogleSearchCX)
	defer func() {
		os.Setenv(EnvGoogleSearchAPIKey, oldAPIKey)
		os.Setenv(EnvGoogleSearchCX, oldCX)
	}()
	os.Setenv(EnvGoogleSearchAPIKey, "test-api-key")
	os.Setenv(EnvGoogleSearchCX, "test-cx")

	status := DetectSearchAPIs()
	if !status.Available {
		t.Error("Expected Available=true when Google keys set")
	}
	if status.Provider != SearchProviderGoogle {
		t.Errorf("Expected Provider=SearchProviderGoogle, got %q", status.Provider)
	}
	if status.GoogleAPIKey != "test-api-key" {
		t.Errorf("Expected GoogleAPIKey='test-api-key', got %q", status.GoogleAPIKey)
	}
	if status.GoogleCX != "test-cx" {
		t.Errorf("Expected GoogleCX='test-cx', got %q", status.GoogleCX)
	}
}

func TestDetectSearchAPIs_PartialGoogleKeys(t *testing.T) {
	// Set only API key, not CX
	oldAPIKey := os.Getenv(EnvGoogleSearchAPIKey)
	oldCX := os.Getenv(EnvGoogleSearchCX)
	defer func() {
		os.Setenv(EnvGoogleSearchAPIKey, oldAPIKey)
		os.Setenv(EnvGoogleSearchCX, oldCX)
	}()
	os.Setenv(EnvGoogleSearchAPIKey, "test-api-key")
	os.Unsetenv(EnvGoogleSearchCX)

	status := DetectSearchAPIs()
	if status.Available {
		t.Error("Expected Available=false when only partial keys set")
	}
}

func TestIsSearchEnabled_ExplicitTrue(t *testing.T) {
	// Set up Google keys so search would be available
	oldAPIKey := os.Getenv(EnvGoogleSearchAPIKey)
	oldCX := os.Getenv(EnvGoogleSearchCX)
	defer func() {
		os.Setenv(EnvGoogleSearchAPIKey, oldAPIKey)
		os.Setenv(EnvGoogleSearchCX, oldCX)
	}()
	os.Setenv(EnvGoogleSearchAPIKey, "test-api-key")
	os.Setenv(EnvGoogleSearchCX, "test-cx")

	enabled := true
	cfg := &Config{
		Search: &SearchConfig{
			Enabled: &enabled,
		},
	}

	if !IsSearchEnabled(cfg) {
		t.Error("Expected IsSearchEnabled=true when explicitly enabled and keys available")
	}
}

func TestIsSearchEnabled_ExplicitFalse(t *testing.T) {
	// Set up Google keys so search would be available
	oldAPIKey := os.Getenv(EnvGoogleSearchAPIKey)
	oldCX := os.Getenv(EnvGoogleSearchCX)
	defer func() {
		os.Setenv(EnvGoogleSearchAPIKey, oldAPIKey)
		os.Setenv(EnvGoogleSearchCX, oldCX)
	}()
	os.Setenv(EnvGoogleSearchAPIKey, "test-api-key")
	os.Setenv(EnvGoogleSearchCX, "test-cx")

	enabled := false
	cfg := &Config{
		Search: &SearchConfig{
			Enabled: &enabled,
		},
	}

	if IsSearchEnabled(cfg) {
		t.Error("Expected IsSearchEnabled=false when explicitly disabled")
	}
}

func TestIsSearchEnabled_AutoDetect_WithKeys(t *testing.T) {
	oldAPIKey := os.Getenv(EnvGoogleSearchAPIKey)
	oldCX := os.Getenv(EnvGoogleSearchCX)
	defer func() {
		os.Setenv(EnvGoogleSearchAPIKey, oldAPIKey)
		os.Setenv(EnvGoogleSearchCX, oldCX)
	}()
	os.Setenv(EnvGoogleSearchAPIKey, "test-api-key")
	os.Setenv(EnvGoogleSearchCX, "test-cx")

	// nil Enabled = auto-detect
	cfg := &Config{
		Search: &SearchConfig{
			Enabled: nil,
		},
	}

	if !IsSearchEnabled(cfg) {
		t.Error("Expected IsSearchEnabled=true when auto-detecting with keys available")
	}
}

func TestIsSearchEnabled_AutoDetect_NoKeys(t *testing.T) {
	oldAPIKey := os.Getenv(EnvGoogleSearchAPIKey)
	oldCX := os.Getenv(EnvGoogleSearchCX)
	defer func() {
		os.Setenv(EnvGoogleSearchAPIKey, oldAPIKey)
		os.Setenv(EnvGoogleSearchCX, oldCX)
	}()
	os.Unsetenv(EnvGoogleSearchAPIKey)
	os.Unsetenv(EnvGoogleSearchCX)

	cfg := &Config{
		Search: &SearchConfig{
			Enabled: nil,
		},
	}

	if IsSearchEnabled(cfg) {
		t.Error("Expected IsSearchEnabled=false when auto-detecting with no keys")
	}
}

func TestIsSearchEnabled_NilConfig(t *testing.T) {
	oldAPIKey := os.Getenv(EnvGoogleSearchAPIKey)
	oldCX := os.Getenv(EnvGoogleSearchCX)
	defer func() {
		os.Setenv(EnvGoogleSearchAPIKey, oldAPIKey)
		os.Setenv(EnvGoogleSearchCX, oldCX)
	}()
	os.Setenv(EnvGoogleSearchAPIKey, "test-api-key")
	os.Setenv(EnvGoogleSearchCX, "test-cx")

	if !IsSearchEnabled(nil) {
		t.Error("Expected IsSearchEnabled=true when config is nil but keys available")
	}
}

func TestGetSearchProvider(t *testing.T) {
	oldAPIKey := os.Getenv(EnvGoogleSearchAPIKey)
	oldCX := os.Getenv(EnvGoogleSearchCX)
	defer func() {
		os.Setenv(EnvGoogleSearchAPIKey, oldAPIKey)
		os.Setenv(EnvGoogleSearchCX, oldCX)
	}()
	os.Setenv(EnvGoogleSearchAPIKey, "test-api-key")
	os.Setenv(EnvGoogleSearchCX, "test-cx")

	provider := GetSearchProvider()
	if provider != SearchProviderGoogle {
		t.Errorf("Expected provider=SearchProviderGoogle, got %q", provider)
	}
}
