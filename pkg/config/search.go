package config

import (
	"os"

	"orchestrator/pkg/logx"
)

// Search provider environment variable names.
// Add new providers here as they're supported.
const (
	// EnvGoogleSearchAPIKey is the environment variable for Google Custom Search API key.
	EnvGoogleSearchAPIKey = "GOOGLE_SEARCH_API_KEY"
	// EnvGoogleSearchCX is the environment variable for Google Custom Search Engine ID.
	EnvGoogleSearchCX = "GOOGLE_SEARCH_CX"
)

// SearchProviderType identifies which search provider is available.
type SearchProviderType string

// Search provider type constants.
const (
	SearchProviderNone   SearchProviderType = ""
	SearchProviderGoogle SearchProviderType = "google"
)

// SearchAPIStatus contains information about available search APIs.
type SearchAPIStatus struct {
	Available    bool               // Whether any search API is available
	Provider     SearchProviderType // Which provider is available (empty if none)
	GoogleAPIKey string             // Google API key (if available)
	GoogleCX     string             // Google Custom Search Engine ID (if available)
}

// DetectSearchAPIs checks environment variables and returns status of available search APIs.
// This function is idempotent and can be called multiple times.
func DetectSearchAPIs() SearchAPIStatus {
	status := SearchAPIStatus{}

	// Check Google Custom Search (highest priority)
	googleAPIKey := os.Getenv(EnvGoogleSearchAPIKey)
	googleCX := os.Getenv(EnvGoogleSearchCX)
	if googleAPIKey != "" && googleCX != "" {
		status.Available = true
		status.Provider = SearchProviderGoogle
		status.GoogleAPIKey = googleAPIKey
		status.GoogleCX = googleCX
		return status
	}

	// Future: Check other providers here in priority order
	// braveKey := os.Getenv(EnvBraveSearchAPIKey)
	// if braveKey != "" { ... }

	return status
}

// IsSearchEnabled determines if web search should be enabled based on config and API availability.
// Returns true if:
//   - Config explicitly enables search (search.enabled = true), OR
//   - Config doesn't specify (nil) AND search APIs are available
//
// Returns false if:
//   - Config explicitly disables search (search.enabled = false), OR
//   - Config doesn't specify (nil) AND no search APIs are available
//
// This function also logs warnings when search is disabled due to missing API keys.
func IsSearchEnabled(cfg *Config) bool {
	logger := logx.NewLogger("config")

	// Check if config exists
	if cfg == nil || cfg.Search == nil {
		// No config - check if APIs are available
		status := DetectSearchAPIs()
		if !status.Available {
			logger.Warn("Web search disabled: no search API keys found. Set %s and %s to enable.",
				EnvGoogleSearchAPIKey, EnvGoogleSearchCX)
		}
		return status.Available
	}

	// If explicitly configured, respect that setting
	if cfg.Search.Enabled != nil {
		enabled := *cfg.Search.Enabled
		if enabled {
			// User wants search enabled - verify we have APIs
			status := DetectSearchAPIs()
			if !status.Available {
				logger.Warn("Web search enabled in config but no API keys found. Set %s and %s.",
					EnvGoogleSearchAPIKey, EnvGoogleSearchCX)
				return false
			}
		}
		return enabled
	}

	// Auto-detect based on API availability
	status := DetectSearchAPIs()
	if !status.Available {
		logger.Warn("Web search disabled: no search API keys found. Set %s and %s to enable.",
			EnvGoogleSearchAPIKey, EnvGoogleSearchCX)
	}
	return status.Available
}

// GetSearchProvider returns the detected search provider type.
// Returns SearchProviderNone if no provider is available.
func GetSearchProvider() SearchProviderType {
	return DetectSearchAPIs().Provider
}
