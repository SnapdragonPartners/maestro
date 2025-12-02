package bootstrap

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"orchestrator/pkg/github"
	"orchestrator/pkg/logx"
)

// GitHubConfig holds configuration for GitHub API operations.
type GitHubConfig struct {
	Owner string
	Repo  string
}

// GitHubManager handles GitHub API operations for bootstrap.
// It wraps the centralized github.Client with bootstrap-specific logging.
type GitHubManager struct {
	config *GitHubConfig
	client *github.Client
	logger *logx.Logger
}

// NewGitHubManager creates a new GitHub manager.
func NewGitHubManager(owner, repo string) *GitHubManager {
	return &GitHubManager{
		config: &GitHubConfig{
			Owner: owner,
			Repo:  repo,
		},
		client: github.NewClient(owner, repo),
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

	owner, repo, err := github.ParseGitHubURL(strings.TrimSpace(string(output)))
	if err != nil {
		return nil, fmt.Errorf("failed to parse GitHub URL: %w", err)
	}

	return NewGitHubManager(owner, repo), nil
}

// EnableSecurityFeatures enables GitHub security features for the repository.
func (g *GitHubManager) EnableSecurityFeatures(ctx context.Context) error {
	g.logger.Info("Enabling GitHub security features for %s/%s", g.config.Owner, g.config.Repo)
	if err := g.client.EnableSecurityFeatures(ctx); err != nil {
		return fmt.Errorf("failed to enable security features: %w", err)
	}
	return nil
}

// GetRepoInfo retrieves repository information.
func (g *GitHubManager) GetRepoInfo(ctx context.Context) (map[string]interface{}, error) {
	repo, err := g.client.GetRepository(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get repository info: %w", err)
	}

	// Convert to map for backward compatibility with existing callers
	return map[string]interface{}{
		"name":               repo.Name,
		"full_name":          repo.FullName,
		"default_branch":     repo.DefaultBranch,
		"allow_auto_merge":   repo.AllowAutoMerge,
		"private":            repo.Private,
		"archived":           repo.Archived,
		"has_issues":         repo.HasIssues,
		"has_wiki":           repo.HasWiki,
		"has_projects":       repo.HasProjects,
		"allow_squash_merge": repo.AllowSquashMerge,
		"allow_merge_commit": repo.AllowMergeCommit,
		"allow_rebase_merge": repo.AllowRebaseMerge,
	}, nil
}

// SecurityFeatureStatus represents the status of GitHub security features.
// Alias to github.SecurityFeatureStatus for backward compatibility.
type SecurityFeatureStatus = github.SecurityFeatureStatus

// CheckSecurityFeatures checks which security features are enabled.
func (g *GitHubManager) CheckSecurityFeatures(ctx context.Context) (*SecurityFeatureStatus, error) {
	status, err := g.client.GetSecurityFeatures(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to check security features: %w", err)
	}
	return status, nil
}

// Client returns the underlying github.Client for direct access to additional methods.
func (g *GitHubManager) Client() *github.Client {
	return g.client
}

// Owner returns the repository owner.
func (g *GitHubManager) Owner() string {
	return g.config.Owner
}

// Repo returns the repository name.
func (g *GitHubManager) Repo() string {
	return g.config.Repo
}
