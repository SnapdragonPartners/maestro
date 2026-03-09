package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"orchestrator/pkg/config"
	"orchestrator/pkg/preflight"
)

// SetSetupMode enables or disables setup mode.
func (s *Server) SetSetupMode(active bool) {
	if active {
		s.setupMode.Store(1)
	} else {
		s.setupMode.Store(0)
	}
}

// IsSetupMode returns whether setup mode is currently active.
func (s *Server) IsSetupMode() bool {
	return s.setupMode.Load() == 1
}

// notifySetupIfReady checks API key readiness and, if all keys are present,
// sends a non-blocking signal on the setupReady channel.
func (s *Server) notifySetupIfReady() {
	if !s.IsSetupMode() {
		return
	}

	cfg, err := config.GetConfig()
	if err != nil {
		return
	}

	_, allPresent := preflight.CheckRequiredAPIKeys(&cfg)
	if allPresent {
		select {
		case s.setupReady <- struct{}{}:
		default:
		}
	}
}

// setupModeRedirect is middleware that redirects page navigations to /setup
// when setup mode is active. It allows through setup, secrets, healthz, and
// static paths so the setup page can function.
func (s *Server) setupModeRedirect(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.IsSetupMode() {
			path := r.URL.Path
			// Allow through paths needed for setup to function
			if path != "/setup" &&
				!strings.HasPrefix(path, "/api/setup/") &&
				!strings.HasPrefix(path, "/api/secrets") &&
				!strings.HasPrefix(path, "/api/keys/") &&
				!strings.HasPrefix(path, "/api/healthz") &&
				!strings.HasPrefix(path, "/static/") {
				http.Redirect(w, r, "/setup", http.StatusTemporaryRedirect)
				return
			}
		}
		next(w, r)
	}
}

// handleSetupPage renders the setup.html template.
func (s *Server) handleSetupPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := s.templates.ExecuteTemplate(w, "setup.html", nil); err != nil {
		s.logger.Error("Failed to render setup template: %v", err)
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
		return
	}
}

// setupStatusResponse is the JSON structure returned by setup status endpoints.
type setupStatusResponse struct {
	Keys       []preflight.ProviderKeyInfo `json:"keys"`
	SetupMode  bool                        `json:"setup_mode"`
	AllPresent bool                        `json:"all_present"`
}

// handleSetupStatus implements GET /api/setup/status.
func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cfg, err := config.GetConfig()
	if err != nil {
		http.Error(w, "Failed to get config", http.StatusInternalServerError)
		return
	}

	keys, allPresent := preflight.CheckRequiredAPIKeys(&cfg)
	resp := setupStatusResponse{
		SetupMode:  s.IsSetupMode(),
		AllPresent: allPresent,
		Keys:       keys,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		s.logger.Error("Failed to encode setup status: %v", err)
	}
}

// handleSetupRecheck implements POST /api/setup/recheck.
func (s *Server) handleSetupRecheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.notifySetupIfReady()

	cfg, err := config.GetConfig()
	if err != nil {
		http.Error(w, "Failed to get config", http.StatusInternalServerError)
		return
	}

	keys, allPresent := preflight.CheckRequiredAPIKeys(&cfg)
	resp := setupStatusResponse{
		SetupMode:  s.IsSetupMode(),
		AllPresent: allPresent,
		Keys:       keys,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		s.logger.Error("Failed to encode setup recheck: %v", err)
	}
}

// handleKeysCheck implements POST /api/keys/check.
// Validates all configured API keys by making real provider API calls.
func (s *Server) handleKeysCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.logger.Info("Key validation requested from %s", r.RemoteAddr)
	results := preflight.ValidateKeys(r.Context())

	for i := range results {
		r := &results[i]
		s.logger.Info("Key check: %s (%s) = %s: %s [%dms]", r.Provider, r.EnvVar, r.Status, r.Message, r.LatencyMs)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(results); err != nil {
		s.logger.Error("Failed to encode key check results: %v", err)
	}
}

// WaitForSetup blocks until all required API keys are configured.
// If all keys are already present, returns immediately without entering setup mode.
// If WebUI is not available, this is a no-op (agent creation will fail with its own error).
func (s *Server) WaitForSetup(ctx context.Context, cfg *config.Config) error {
	keys, allPresent := preflight.CheckRequiredAPIKeys(cfg)
	if allPresent {
		return nil
	}

	// Enter setup mode
	s.SetSetupMode(true)
	defer s.SetSetupMode(false)

	// Print terminal guidance
	fmt.Println()
	fmt.Println("Missing API keys:")
	for i := range keys {
		if !keys[i].Present {
			fmt.Printf("  - %s\n", keys[i].EnvVarName)
		}
	}

	// Determine WebUI URL
	protocol := "http"
	if cfg.WebUI != nil && cfg.WebUI.SSL {
		protocol = "https"
	}
	host := "localhost"
	port := 8080
	if cfg.WebUI != nil {
		if cfg.WebUI.Host != "" {
			host = cfg.WebUI.Host
		}
		if cfg.WebUI.Port > 0 {
			port = cfg.WebUI.Port
		}
	}

	fmt.Printf("Configure them in the WebUI at %s://%s:%d/setup\n", protocol, host, port)
	fmt.Println("Press Ctrl+C to cancel.")
	fmt.Println()

	// Wait for keys to be configured
	for {
		select {
		case <-s.setupReady:
			// Re-verify — channel signal may be a false positive
			_, allPresent := preflight.CheckRequiredAPIKeys(cfg)
			if allPresent {
				s.logger.Info("All API keys configured. Continuing startup...")
				fmt.Println("All API keys configured. Continuing startup...")
				return nil
			}
			// Not all present yet, keep waiting
		case <-ctx.Done():
			return fmt.Errorf("waiting for API keys: %w", ctx.Err())
		}
	}
}
