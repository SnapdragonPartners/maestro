package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"orchestrator/pkg/logx"
)

// GitHubConfig holds configuration for GitHub API operations.
type GitHubConfig struct {
	Owner string
	Repo  string
}

// GitHubManager handles GitHub API operations for bootstrap.
type GitHubManager struct {
	config *GitHubConfig
	logger *logx.Logger
}

// NewGitHubManager creates a new GitHub manager.
func NewGitHubManager(owner, repo string) *GitHubManager {
	return &GitHubManager{
		config: &GitHubConfig{
			Owner: owner,
			Repo:  repo,
		},
		logger: logx.NewLogger("bootstrap-github"),
	}
}

// NewGitHubManagerFromRemote creates a GitHubManager by parsing the remote URL.
func NewGitHubManagerFromRemote(ctx context.Context, workDir string) (*GitHubManager, error) {
	// Get the remote URL
	cmd := exec.CommandContext(ctx, "git", "remote", "get-url", "origin")
	cmd.Dir = workDir
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get git remote URL: %w", err)
	}

	owner, repo, err := parseGitHubURL(strings.TrimSpace(string(output)))
	if err != nil {
		return nil, err
	}

	return NewGitHubManager(owner, repo), nil
}

// parseGitHubURL extracts owner and repo from various GitHub URL formats.
func parseGitHubURL(url string) (owner, repo string, err error) {
	// Handle SSH format: git@github.com:owner/repo.git
	if strings.HasPrefix(url, "git@github.com:") {
		path := strings.TrimPrefix(url, "git@github.com:")
		path = strings.TrimSuffix(path, ".git")
		parts := strings.Split(path, "/")
		if len(parts) != 2 {
			return "", "", fmt.Errorf("invalid GitHub SSH URL format: %s", url)
		}
		return parts[0], parts[1], nil
	}

	// Handle HTTPS format: https://github.com/owner/repo.git
	if strings.HasPrefix(url, "https://github.com/") {
		path := strings.TrimPrefix(url, "https://github.com/")
		path = strings.TrimSuffix(path, ".git")
		parts := strings.Split(path, "/")
		if len(parts) != 2 {
			return "", "", fmt.Errorf("invalid GitHub HTTPS URL format: %s", url)
		}
		return parts[0], parts[1], nil
	}

	return "", "", fmt.Errorf("unsupported Git URL format: %s", url)
}

// EnableSecurityFeatures enables GitHub security features for the repository.
func (g *GitHubManager) EnableSecurityFeatures(ctx context.Context) error {
	g.logger.Info("Enabling GitHub security features for %s/%s", g.config.Owner, g.config.Repo)

	// Enable vulnerability alerts
	if err := g.enableVulnerabilityAlerts(ctx); err != nil {
		g.logger.Warn("Failed to enable vulnerability alerts: %v", err)
		// Continue - this might fail due to permissions
	}

	// Enable automated security fixes (dependabot security updates)
	if err := g.enableAutomatedSecurityFixes(ctx); err != nil {
		g.logger.Warn("Failed to enable automated security fixes: %v", err)
		// Continue - this might fail due to permissions
	}

	// Enable auto-merge repository setting
	if err := g.enableAutoMerge(ctx); err != nil {
		g.logger.Warn("Failed to enable auto-merge: %v", err)
		// Continue - this might fail due to permissions
	}

	return nil
}

// enableVulnerabilityAlerts enables Dependabot vulnerability alerts.
func (g *GitHubManager) enableVulnerabilityAlerts(ctx context.Context) error {
	endpoint := fmt.Sprintf("/repos/%s/%s/vulnerability-alerts", g.config.Owner, g.config.Repo)
	_, err := g.ghAPI(ctx, "PUT", endpoint, nil)
	if err != nil {
		return fmt.Errorf("failed to enable vulnerability alerts: %w", err)
	}
	g.logger.Info("Enabled vulnerability alerts")
	return nil
}

// enableAutomatedSecurityFixes enables Dependabot automated security fixes.
func (g *GitHubManager) enableAutomatedSecurityFixes(ctx context.Context) error {
	endpoint := fmt.Sprintf("/repos/%s/%s/automated-security-fixes", g.config.Owner, g.config.Repo)
	_, err := g.ghAPI(ctx, "PUT", endpoint, nil)
	if err != nil {
		return fmt.Errorf("failed to enable automated security fixes: %w", err)
	}
	g.logger.Info("Enabled automated security fixes")
	return nil
}

// enableAutoMerge enables the auto-merge repository setting.
func (g *GitHubManager) enableAutoMerge(ctx context.Context) error {
	endpoint := fmt.Sprintf("/repos/%s/%s", g.config.Owner, g.config.Repo)
	_, err := g.ghAPI(ctx, "PATCH", endpoint, map[string]interface{}{
		"allow_auto_merge": true,
	})
	if err != nil {
		return fmt.Errorf("failed to enable auto-merge: %w", err)
	}
	g.logger.Info("Enabled auto-merge")
	return nil
}

// GetRepoInfo retrieves repository information.
func (g *GitHubManager) GetRepoInfo(ctx context.Context) (map[string]interface{}, error) {
	endpoint := fmt.Sprintf("/repos/%s/%s", g.config.Owner, g.config.Repo)
	output, err := g.ghAPI(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get repo info: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return nil, fmt.Errorf("failed to parse repo info: %w", err)
	}

	return result, nil
}

// CheckSecurityFeatures checks which security features are enabled.
func (g *GitHubManager) CheckSecurityFeatures(ctx context.Context) (*SecurityFeatureStatus, error) {
	status := &SecurityFeatureStatus{}

	// Check vulnerability alerts
	endpoint := fmt.Sprintf("/repos/%s/%s/vulnerability-alerts", g.config.Owner, g.config.Repo)
	_, err := g.ghAPI(ctx, "GET", endpoint, nil)
	status.VulnerabilityAlerts = err == nil

	// Check automated security fixes
	endpoint = fmt.Sprintf("/repos/%s/%s/automated-security-fixes", g.config.Owner, g.config.Repo)
	_, err = g.ghAPI(ctx, "GET", endpoint, nil)
	status.AutomatedSecurityFixes = err == nil

	// Check auto-merge setting
	repoInfo, err := g.GetRepoInfo(ctx)
	if err == nil {
		if allowAutoMerge, ok := repoInfo["allow_auto_merge"].(bool); ok {
			status.AutoMerge = allowAutoMerge
		}
	}

	return status, nil
}

// SecurityFeatureStatus represents the status of GitHub security features.
type SecurityFeatureStatus struct {
	VulnerabilityAlerts    bool `json:"vulnerability_alerts"`
	AutomatedSecurityFixes bool `json:"automated_security_fixes"`
	AutoMerge              bool `json:"auto_merge"`
}

// ghAPI executes a GitHub API call using the gh CLI.
func (g *GitHubManager) ghAPI(ctx context.Context, method, endpoint string, body map[string]interface{}) (string, error) {
	args := []string{"api", "-X", method, endpoint}

	// Add body fields if present
	for key, value := range body {
		switch v := value.(type) {
		case bool:
			args = append(args, "-f", fmt.Sprintf("%s=%t", key, v))
		case string:
			args = append(args, "-f", fmt.Sprintf("%s=%s", key, v))
		default:
			args = append(args, "-f", fmt.Sprintf("%s=%v", key, v))
		}
	}

	cmd := exec.CommandContext(ctx, "gh", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gh api call failed: %w\nOutput: %s", err, string(output))
	}

	return string(output), nil
}
