package webui

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"orchestrator/pkg/config"
)

// SecretEntry represents a secret for the API response (name only, no value).
type SecretEntry struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// handleSecretsList implements GET /api/secrets.
// Returns list of secret names (not values for security).
// Optional query param: ?type=user|system to filter by type.
func (s *Server) handleSecretsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get all secret names from in-memory secrets
	secrets := config.GetDecryptedSecretNames()

	// Optional type filter (validate if provided)
	typeFilter := r.URL.Query().Get("type")
	if typeFilter != "" && typeFilter != string(config.SecretTypeUser) && typeFilter != string(config.SecretTypeSystem) {
		http.Error(w, `Invalid secret type; must be "user" or "system"`, http.StatusBadRequest)
		return
	}

	// Build response
	entries := make([]SecretEntry, 0, len(secrets))
	for i := range secrets {
		if typeFilter != "" && string(secrets[i].Type) != typeFilter {
			continue
		}
		entries = append(entries, SecretEntry{Name: secrets[i].Name, Type: string(secrets[i].Type)})
	}

	// Sort alphabetically
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(entries); err != nil {
		s.logger.Error("Failed to encode secrets list response: %v", err)
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}

	s.logger.Debug("Served secrets list: %d secrets", len(entries))
}

// handleSecretsSet implements POST /api/secrets.
// Sets a secret value (encrypted at rest).
// Request body includes optional "type" field (defaults to "user").
func (s *Server) handleSecretsSet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse request body
	var reqBody struct {
		Name  string `json:"name"`
		Value string `json:"value"`
		Type  string `json:"type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Validate
	if reqBody.Name == "" {
		http.Error(w, "Secret name is required", http.StatusBadRequest)
		return
	}
	if reqBody.Value == "" {
		http.Error(w, "Secret value is required", http.StatusBadRequest)
		return
	}

	// Validate type (default to "user")
	secretType := config.SecretTypeUser
	switch reqBody.Type {
	case "", string(config.SecretTypeUser):
		// keep default user type
	case string(config.SecretTypeSystem):
		secretType = config.SecretTypeSystem
	default:
		http.Error(w, `Invalid secret type; must be "user" or "system"`, http.StatusBadRequest)
		return
	}

	// Validate name format
	if err := config.ValidateSecretName(reqBody.Name); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Set the secret in memory (SetSecret validates system names against allowlist)
	if err := config.SetSecret(reqBody.Name, reqBody.Value, secretType); err != nil {
		s.logger.Error("Failed to set secret: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Persist to encrypted file
	password := config.GetProjectPassword()
	if password == "" {
		// If no password set, we can only store in memory
		s.logger.Warn("No project password set - secret stored in memory only")
	} else {
		if err := config.SaveSecretsToFile(s.workDir, password); err != nil {
			s.logger.Error("Failed to persist secret to file: %v", err)
			// Don't fail - the secret is still in memory
		}
	}

	response := map[string]interface{}{
		"success": true,
		"name":    reqBody.Name,
		"type":    string(secretType),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.logger.Error("Failed to encode response: %v", err)
	}

	s.logger.Info("Secret %q (%s) set successfully", reqBody.Name, secretType)
}

// handleSecretsDelete implements DELETE /api/secrets/:name.
// Removes a secret. Optional query param: ?type=user|system (defaults to "user").
func (s *Server) handleSecretsDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract secret name from URL path
	path := strings.TrimPrefix(r.URL.Path, "/api/secrets/")
	if path == "" {
		http.Error(w, "Secret name required", http.StatusBadRequest)
		return
	}

	secretName := path

	// Get type from query param (default: user)
	secretType := config.SecretTypeUser
	switch r.URL.Query().Get("type") {
	case "", string(config.SecretTypeUser):
		// keep default user type
	case string(config.SecretTypeSystem):
		secretType = config.SecretTypeSystem
	default:
		http.Error(w, `Invalid secret type; must be "user" or "system"`, http.StatusBadRequest)
		return
	}

	// Delete the secret from memory
	if err := config.DeleteSecret(secretName, secretType); err != nil {
		s.logger.Error("Failed to delete secret: %v", err)
		http.Error(w, "Failed to delete secret", http.StatusInternalServerError)
		return
	}

	// Persist to encrypted file
	password := config.GetProjectPassword()
	if password != "" {
		if err := config.SaveSecretsToFile(s.workDir, password); err != nil {
			s.logger.Error("Failed to persist secret deletion to file: %v", err)
			// Don't fail - the secret is deleted from memory
		}
	}

	response := map[string]interface{}{
		"success": true,
		"name":    secretName,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.logger.Error("Failed to encode response: %v", err)
	}

	s.logger.Info("Secret %q (%s) deleted successfully", secretName, secretType)
}
