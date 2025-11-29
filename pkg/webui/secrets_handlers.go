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
}

// handleSecretsList implements GET /api/secrets.
// Returns list of secret names (not values for security).
func (s *Server) handleSecretsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get all secret names from in-memory secrets
	secrets := config.GetDecryptedSecretNames()

	// Build response
	entries := make([]SecretEntry, 0, len(secrets))
	for _, name := range secrets {
		entries = append(entries, SecretEntry{Name: name})
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
func (s *Server) handleSecretsSet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse request body
	var reqBody struct {
		Name  string `json:"name"`
		Value string `json:"value"`
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

	// Sanitize name (alphanumeric and underscores only)
	sanitizedName := sanitizeSecretName(reqBody.Name)
	if sanitizedName != reqBody.Name {
		http.Error(w, "Secret name must contain only alphanumeric characters and underscores", http.StatusBadRequest)
		return
	}

	// Set the secret in memory
	if err := config.SetSecret(sanitizedName, reqBody.Value); err != nil {
		s.logger.Error("Failed to set secret: %v", err)
		http.Error(w, "Failed to set secret", http.StatusInternalServerError)
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
		"name":    sanitizedName,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.logger.Error("Failed to encode response: %v", err)
	}

	s.logger.Info("Secret %q set successfully", sanitizedName)
}

// handleSecretsDelete implements DELETE /api/secrets/:name.
// Removes a secret.
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

	// Delete the secret from memory
	if err := config.DeleteSecret(secretName); err != nil {
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

	s.logger.Info("Secret %q deleted successfully", secretName)
}

// sanitizeSecretName ensures secret name contains only valid characters.
func sanitizeSecretName(name string) string {
	var result strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			result.WriteRune(r)
		}
	}
	return result.String()
}
