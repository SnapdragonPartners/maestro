package gitea

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"orchestrator/pkg/logx"
)

// Setup constants.
const (
	// DefaultAdminUser is the admin username created in Gitea.
	DefaultAdminUser = "maestro-admin"

	// DefaultAdminEmail is the admin email address.
	DefaultAdminEmail = "admin@localhost"

	// DefaultOrganization is the organization name for repositories.
	DefaultOrganization = "maestro"

	// TokenName is the name for generated API tokens.
	TokenName = "maestro-api-token"

	// SetupTimeout is the timeout for setup operations.
	SetupTimeout = 30 * time.Second
)

// SetupConfig holds configuration for initial Gitea setup.
type SetupConfig struct {
	// ContainerInfo from EnsureContainer.
	Container *ContainerInfo

	// RepoName is the repository name to create.
	RepoName string

	// MirrorPath is the path to the git mirror to push.
	// If empty, an empty repository is created.
	MirrorPath string

	// AdminPassword is the password for the admin user.
	// If empty, a random password is generated.
	AdminPassword string
}

// SetupResult holds the result of Gitea setup.
type SetupResult struct {
	// Token is the generated API token.
	Token string

	// URL is the base URL for the Gitea instance.
	URL string

	// Owner is the organization that owns the repository.
	Owner string

	// RepoName is the repository name.
	RepoName string

	// CloneURL is the HTTP clone URL for the repository.
	CloneURL string
}

// SetupManager handles initial Gitea setup.
type SetupManager struct {
	logger    *logx.Logger
	dockerCmd string
}

// NewSetupManager creates a new setup manager.
func NewSetupManager() *SetupManager {
	logger := logx.NewLogger("gitea-setup")

	dockerCmd := "docker"
	if _, err := exec.LookPath("podman"); err == nil {
		if _, err := exec.LookPath("docker"); err != nil {
			dockerCmd = "podman"
		}
	}

	return &SetupManager{
		logger:    logger,
		dockerCmd: dockerCmd,
	}
}

// Setup performs initial Gitea setup: creates admin user, token, org, and repo.
// This is idempotent - if already set up, it returns existing credentials.
func (m *SetupManager) Setup(ctx context.Context, cfg SetupConfig) (*SetupResult, error) {
	ctx, cancel := context.WithTimeout(ctx, SetupTimeout)
	defer cancel()

	baseURL := cfg.Container.URL

	// Generate admin password if not provided.
	adminPassword := cfg.AdminPassword
	if adminPassword == "" {
		var err error
		adminPassword, err = generateRandomPassword(32)
		if err != nil {
			return nil, fmt.Errorf("failed to generate admin password: %w", err)
		}
	}

	// Step 1: Create admin user via CLI (docker exec).
	m.logger.Info("Creating admin user in Gitea container...")
	if err := m.createAdminUser(ctx, cfg.Container.Name, adminPassword); err != nil {
		// User might already exist - that's OK.
		if !strings.Contains(err.Error(), "already exists") {
			m.logger.Warn("Admin user creation returned error (may already exist): %v", err)
		}
	}

	// Step 2: Generate API token.
	m.logger.Info("Generating API token...")
	token, err := m.generateToken(ctx, baseURL, DefaultAdminUser, adminPassword)
	if err != nil {
		return nil, fmt.Errorf("failed to generate token: %w", err)
	}

	// Step 3: Create organization.
	m.logger.Info("Creating organization %s...", DefaultOrganization)
	if err := m.createOrganization(ctx, baseURL, token); err != nil {
		// Org might already exist.
		if !strings.Contains(err.Error(), "already exists") && !strings.Contains(err.Error(), "409") {
			m.logger.Warn("Organization creation returned error (may already exist): %v", err)
		}
	}

	// Step 4: Create repository.
	repoName := cfg.RepoName
	if repoName == "" {
		repoName = "project"
	}
	m.logger.Info("Creating repository %s/%s...", DefaultOrganization, repoName)
	if err := m.createRepository(ctx, baseURL, token, repoName); err != nil {
		if !strings.Contains(err.Error(), "already exists") && !strings.Contains(err.Error(), "409") {
			m.logger.Warn("Repository creation returned error (may already exist): %v", err)
		}
	}

	// Step 5: Push mirror content if provided.
	if cfg.MirrorPath != "" {
		cloneURL := fmt.Sprintf("%s/%s/%s.git", baseURL, DefaultOrganization, repoName)
		m.logger.Info("Pushing mirror content to %s...", cloneURL)
		if err := m.pushMirror(ctx, cfg.MirrorPath, cloneURL, token); err != nil {
			return nil, fmt.Errorf("failed to push mirror: %w", err)
		}
	}

	result := &SetupResult{
		Token:    token,
		URL:      baseURL,
		Owner:    DefaultOrganization,
		RepoName: repoName,
		CloneURL: fmt.Sprintf("%s/%s/%s.git", baseURL, DefaultOrganization, repoName),
	}

	m.logger.Info("Gitea setup complete: %s/%s", DefaultOrganization, repoName)
	return result, nil
}

// createAdminUser creates an admin user via docker exec gitea admin.
func (m *SetupManager) createAdminUser(ctx context.Context, containerName, password string) error {
	// Use gitea admin user create command.
	args := []string{
		"exec", containerName,
		"gitea", "admin", "user", "create",
		"--username", DefaultAdminUser,
		"--password", password,
		"--email", DefaultAdminEmail,
		"--admin",
	}

	cmd := exec.CommandContext(ctx, m.dockerCmd, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gitea admin user create failed: %w (output: %s)", err, string(output))
	}

	m.logger.Debug("Admin user created: %s", string(output))
	return nil
}

// generateToken creates an API token for the admin user.
func (m *SetupManager) generateToken(ctx context.Context, baseURL, username, password string) (string, error) {
	// First, try to use existing token (check by listing).
	// If that fails, create a new one.

	// Create a new token via API.
	tokenURL := fmt.Sprintf("%s/api/v1/users/%s/tokens", baseURL, username)

	payload := map[string]interface{}{
		"name": TokenName,
		"scopes": []string{
			"read:organization",
			"write:organization",
			"read:repository",
			"write:repository",
			"read:user",
			"write:user",
		},
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal token request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(username, password)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)

	// 201 Created = new token created.
	// 422 = token with this name already exists.
	if resp.StatusCode == http.StatusUnprocessableEntity {
		// Token already exists - we need to delete and recreate.
		m.logger.Debug("Token already exists, deleting and recreating...")
		if deleteErr := m.deleteToken(ctx, baseURL, username, password); deleteErr != nil {
			m.logger.Warn("Failed to delete existing token: %v", deleteErr)
		}
		// Retry creation.
		return m.generateToken(ctx, baseURL, username, password)
	}

	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("token creation failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response to get token.
	var tokenResp struct {
		SHA1 string `json:"sha1"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("failed to parse token response: %w", err)
	}

	if tokenResp.SHA1 == "" {
		return "", fmt.Errorf("token response missing sha1 field")
	}

	return tokenResp.SHA1, nil
}

// deleteToken deletes an existing token by name.
func (m *SetupManager) deleteToken(ctx context.Context, baseURL, username, password string) error {
	deleteURL := fmt.Sprintf("%s/api/v1/users/%s/tokens/%s", baseURL, username, TokenName)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, deleteURL, http.NoBody)
	if err != nil {
		return err
	}

	req.SetBasicAuth(username, password)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("delete token failed with status %d", resp.StatusCode)
	}

	return nil
}

// createOrganization creates the maestro organization.
func (m *SetupManager) createOrganization(ctx context.Context, baseURL, token string) error {
	orgURL := fmt.Sprintf("%s/api/v1/orgs", baseURL)

	payload := map[string]interface{}{
		"username": DefaultOrganization,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal org request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, orgURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "token "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("org request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)

	// 201 = created, 409/422 = already exists.
	if resp.StatusCode != http.StatusCreated &&
		resp.StatusCode != http.StatusConflict &&
		resp.StatusCode != http.StatusUnprocessableEntity {
		return fmt.Errorf("org creation failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// createRepository creates a repository in the organization.
func (m *SetupManager) createRepository(ctx context.Context, baseURL, token, repoName string) error {
	repoURL := fmt.Sprintf("%s/api/v1/orgs/%s/repos", baseURL, DefaultOrganization)

	payload := map[string]interface{}{
		"name":           repoName,
		"private":        false,
		"default_branch": "main",
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal repo request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, repoURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "token "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("repo request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)

	// 201 = created, 409/422 = already exists.
	if resp.StatusCode != http.StatusCreated &&
		resp.StatusCode != http.StatusConflict &&
		resp.StatusCode != http.StatusUnprocessableEntity {
		return fmt.Errorf("repo creation failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// pushMirror pushes content from a git mirror to the Gitea repository.
func (m *SetupManager) pushMirror(ctx context.Context, mirrorPath, cloneURL, token string) error {
	// We need to push all refs from the mirror to Gitea.
	// Format the URL with token for authentication.
	// Example: http://token:TOKEN@localhost:3000/maestro/project.git

	// Parse the URL and add authentication.
	authURL := strings.Replace(cloneURL, "://", fmt.Sprintf("://%s:%s@", DefaultAdminUser, token), 1)

	// Add the remote and push.
	// First, check if remote already exists.
	checkCmd := exec.CommandContext(ctx, "git", "-C", mirrorPath, "remote", "get-url", "gitea")
	if checkCmd.Run() != nil {
		// Remote doesn't exist, add it.
		addCmd := exec.CommandContext(ctx, "git", "-C", mirrorPath, "remote", "add", "gitea", authURL)
		if output, err := addCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to add gitea remote: %w (output: %s)", err, string(output))
		}
	} else {
		// Update existing remote.
		updateCmd := exec.CommandContext(ctx, "git", "-C", mirrorPath, "remote", "set-url", "gitea", authURL)
		if output, err := updateCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to update gitea remote: %w (output: %s)", err, string(output))
		}
	}

	// Push all refs.
	pushCmd := exec.CommandContext(ctx, "git", "-C", mirrorPath, "push", "--mirror", "gitea")
	if output, err := pushCmd.CombinedOutput(); err != nil {
		// Check if it's just "everything up-to-date".
		if strings.Contains(string(output), "Everything up-to-date") ||
			strings.Contains(string(output), "up to date") {
			m.logger.Info("Mirror already up to date with Gitea")
			return nil
		}
		return fmt.Errorf("failed to push to gitea: %w (output: %s)", err, string(output))
	}

	m.logger.Info("Mirror pushed to Gitea successfully")
	return nil
}

// generateRandomPassword generates a cryptographically secure random password.
func generateRandomPassword(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes)[:length], nil
}

// IsSetupComplete checks if Gitea has already been set up for a project.
func (m *SetupManager) IsSetupComplete(ctx context.Context, baseURL, token, repoName string) bool {
	// Check if the repository exists.
	repoURL := fmt.Sprintf("%s/api/v1/repos/%s/%s", baseURL, DefaultOrganization, repoName)

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, repoURL, http.NoBody)
	if err != nil {
		return false
	}

	req.Header.Set("Authorization", "token "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()

	return resp.StatusCode == http.StatusOK
}
