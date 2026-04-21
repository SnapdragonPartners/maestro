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

// notifySetupIfReady evaluates full readiness (presence + validation) and,
// if all required keys are accessible and valid, sends a non-blocking signal
// on the setupReady channel. Always persists the latest result so the setup
// UI reflects current state even when readiness is still false (e.g., keys
// decrypted after login but unauthorized).
func (s *Server) notifySetupIfReady(ctx context.Context) {
	if !s.IsSetupMode() {
		return
	}

	cfg, err := config.GetConfig()
	if err != nil {
		return
	}

	result := preflight.EvaluateSetupReadiness(ctx, &cfg)

	// Always persist so the setup UI can reflect the latest state.
	s.setValidationResults(result)

	if result.Ready {
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

	s.notifySetupIfReady(r.Context())

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

// handleValidationResults implements GET /api/setup/validation-results.
// Returns the latest readiness evaluation stored by WaitForSetup or notifySetupIfReady.
func (s *Server) handleValidationResults(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	result := s.getValidationResults()
	if result == nil {
		// No results yet — return empty object
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ready":false,"all_present":false,"key_info":[]}`))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		s.logger.Error("Failed to encode validation results: %v", err)
	}
}

// printSetupGuidance prints terminal guidance about missing or invalid keys.
func printSetupGuidance(result *preflight.ReadinessResult, cfg *config.Config) {
	fmt.Println()
	if !result.AllPresent {
		fmt.Println("Missing API keys:")
		for i := range result.KeyInfo {
			if !result.KeyInfo[i].Present {
				fmt.Printf("  - %s\n", result.KeyInfo[i].EnvVarName)
			}
		}
	}
	if len(result.ValidationErrors) > 0 {
		fmt.Println("Invalid API keys:")
		for i := range result.ValidationErrors {
			e := &result.ValidationErrors[i]
			fmt.Printf("  - %s: %s\n", e.EnvVar, e.Status)
		}
	}

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
}

// WaitForSetup blocks until all required API keys are configured and valid.
// If all keys are already present and valid, returns immediately without entering setup mode.
// If keys are missing or invalid (unauthorized/forbidden), enters setup mode and waits.
// Transient errors (unreachable) are logged as warnings but don't block startup.
func (s *Server) WaitForSetup(ctx context.Context, cfg *config.Config) error {
	result := preflight.EvaluateSetupReadiness(ctx, cfg)

	// Log warnings for transient provider issues (non-blocking)
	for i := range result.Warnings {
		w := &result.Warnings[i]
		s.logger.Warn("⚠️  %s: %s (non-blocking)", w.Provider, w.Message)
	}

	if result.Ready {
		return nil
	}

	// Store results for the setup page to display on load
	s.setValidationResults(result)

	// Enter setup mode
	s.SetSetupMode(true)
	defer s.SetSetupMode(false)

	printSetupGuidance(result, cfg)

	// Wait for keys to be configured and validated
	for {
		select {
		case <-s.setupReady:
			// Re-verify — channel signal may be a false positive
			fresh := preflight.EvaluateSetupReadiness(ctx, cfg)
			if fresh.Ready {
				s.logger.Info("All API keys configured and valid. Continuing startup...")
				fmt.Println("All API keys configured and valid. Continuing startup...")
				return nil
			}
			// Not ready yet — update stored results and keep waiting
			s.setValidationResults(fresh)
		case <-ctx.Done():
			return fmt.Errorf("waiting for API keys: %w", ctx.Err())
		}
	}
}
