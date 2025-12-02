package github

import (
	"context"
	"encoding/json"
	"fmt"
)

// Repository represents a GitHub repository.
type Repository struct {
	Name             string `json:"name"`
	FullName         string `json:"full_name"`
	DefaultBranch    string `json:"default_branch"`
	AllowAutoMerge   bool   `json:"allow_auto_merge"`
	Private          bool   `json:"private"`
	Archived         bool   `json:"archived"`
	HasIssues        bool   `json:"has_issues"`
	HasWiki          bool   `json:"has_wiki"`
	HasProjects      bool   `json:"has_projects"`
	AllowSquashMerge bool   `json:"allow_squash_merge"`
	AllowMergeCommit bool   `json:"allow_merge_commit"`
	AllowRebaseMerge bool   `json:"allow_rebase_merge"`
}

// GetRepository retrieves repository information.
func (c *Client) GetRepository(ctx context.Context) (*Repository, error) {
	endpoint := fmt.Sprintf("/repos/%s", c.RepoPath())
	output, err := c.APIGet(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to get repository: %w", err)
	}

	var repo Repository
	if err := json.Unmarshal(output, &repo); err != nil {
		return nil, fmt.Errorf("failed to parse repository: %w", err)
	}

	return &repo, nil
}

// RepoExists checks if the repository exists and is accessible.
func (c *Client) RepoExists(ctx context.Context) bool {
	args := []string{"repo", "view", c.RepoPath(), "--json", "name"}
	_, err := c.run(ctx, args...)
	return err == nil
}

// SecurityFeatureStatus represents the status of GitHub security features.
type SecurityFeatureStatus struct {
	VulnerabilityAlerts    bool `json:"vulnerability_alerts"`
	AutomatedSecurityFixes bool `json:"automated_security_fixes"`
	AutoMerge              bool `json:"auto_merge"`
}

// GetSecurityFeatures checks which security features are enabled.
func (c *Client) GetSecurityFeatures(ctx context.Context) (*SecurityFeatureStatus, error) {
	status := &SecurityFeatureStatus{}

	// Check vulnerability alerts
	endpoint := fmt.Sprintf("/repos/%s/vulnerability-alerts", c.RepoPath())
	_, err := c.APIGet(ctx, endpoint)
	status.VulnerabilityAlerts = err == nil

	// Check automated security fixes
	endpoint = fmt.Sprintf("/repos/%s/automated-security-fixes", c.RepoPath())
	_, err = c.APIGet(ctx, endpoint)
	status.AutomatedSecurityFixes = err == nil

	// Check auto-merge setting from repo info
	repo, err := c.GetRepository(ctx)
	if err == nil {
		status.AutoMerge = repo.AllowAutoMerge
	}

	return status, nil
}

// EnableVulnerabilityAlerts enables Dependabot vulnerability alerts.
func (c *Client) EnableVulnerabilityAlerts(ctx context.Context) error {
	endpoint := fmt.Sprintf("/repos/%s/vulnerability-alerts", c.RepoPath())
	_, err := c.APIPut(ctx, endpoint, nil)
	if err != nil {
		return fmt.Errorf("failed to enable vulnerability alerts: %w", err)
	}
	c.logger.Info("Enabled vulnerability alerts for %s", c.RepoPath())
	return nil
}

// DisableVulnerabilityAlerts disables Dependabot vulnerability alerts.
func (c *Client) DisableVulnerabilityAlerts(ctx context.Context) error {
	endpoint := fmt.Sprintf("/repos/%s/vulnerability-alerts", c.RepoPath())
	_, err := c.APIDelete(ctx, endpoint)
	if err != nil {
		return fmt.Errorf("failed to disable vulnerability alerts: %w", err)
	}
	return nil
}

// EnableAutomatedSecurityFixes enables Dependabot automated security fixes.
func (c *Client) EnableAutomatedSecurityFixes(ctx context.Context) error {
	endpoint := fmt.Sprintf("/repos/%s/automated-security-fixes", c.RepoPath())
	_, err := c.APIPut(ctx, endpoint, nil)
	if err != nil {
		return fmt.Errorf("failed to enable automated security fixes: %w", err)
	}
	c.logger.Info("Enabled automated security fixes for %s", c.RepoPath())
	return nil
}

// DisableAutomatedSecurityFixes disables Dependabot automated security fixes.
func (c *Client) DisableAutomatedSecurityFixes(ctx context.Context) error {
	endpoint := fmt.Sprintf("/repos/%s/automated-security-fixes", c.RepoPath())
	_, err := c.APIDelete(ctx, endpoint)
	if err != nil {
		return fmt.Errorf("failed to disable automated security fixes: %w", err)
	}
	return nil
}

// EnableAutoMerge enables the auto-merge repository setting.
func (c *Client) EnableAutoMerge(ctx context.Context) error {
	endpoint := fmt.Sprintf("/repos/%s", c.RepoPath())
	_, err := c.APIPatch(ctx, endpoint, map[string]interface{}{
		"allow_auto_merge": true,
	})
	if err != nil {
		return fmt.Errorf("failed to enable auto-merge: %w", err)
	}
	c.logger.Info("Enabled auto-merge for %s", c.RepoPath())
	return nil
}

// DisableAutoMerge disables the auto-merge repository setting.
func (c *Client) DisableAutoMerge(ctx context.Context) error {
	endpoint := fmt.Sprintf("/repos/%s", c.RepoPath())
	_, err := c.APIPatch(ctx, endpoint, map[string]interface{}{
		"allow_auto_merge": false,
	})
	if err != nil {
		return fmt.Errorf("failed to disable auto-merge: %w", err)
	}
	return nil
}

// EnableSecurityFeatures enables all security features (convenience method).
func (c *Client) EnableSecurityFeatures(ctx context.Context) error {
	c.logger.Info("Enabling GitHub security features for %s", c.RepoPath())

	// Enable vulnerability alerts
	if err := c.EnableVulnerabilityAlerts(ctx); err != nil {
		c.logger.Warn("Failed to enable vulnerability alerts: %v", err)
		// Continue - might fail due to permissions
	}

	// Enable automated security fixes
	if err := c.EnableAutomatedSecurityFixes(ctx); err != nil {
		c.logger.Warn("Failed to enable automated security fixes: %v", err)
	}

	// Enable auto-merge
	if err := c.EnableAutoMerge(ctx); err != nil {
		c.logger.Warn("Failed to enable auto-merge: %v", err)
	}

	return nil
}

// UpdateRepository updates repository settings.
func (c *Client) UpdateRepository(ctx context.Context, settings map[string]interface{}) error {
	endpoint := fmt.Sprintf("/repos/%s", c.RepoPath())
	_, err := c.APIPatch(ctx, endpoint, settings)
	if err != nil {
		return fmt.Errorf("failed to update repository: %w", err)
	}
	return nil
}
