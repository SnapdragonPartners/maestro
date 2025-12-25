// Package github provides a forge.Client adapter for the existing github package.
// This adapter wraps pkg/github.Client to implement the forge.Client interface.
package github

import (
	"context"
	"fmt"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/forge"
	"orchestrator/pkg/github"
)

// Client adapts github.Client to implement forge.Client.
type Client struct {
	ghClient *github.Client
}

// NewClient creates a new GitHub forge client from a github.Client.
func NewClient(ghClient *github.Client) *Client {
	return &Client{ghClient: ghClient}
}

// NewClientFromConfig creates a GitHub forge client from config.
func NewClientFromConfig() (forge.Client, error) {
	cfg, err := config.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get config: %w", err)
	}

	if cfg.Git == nil || cfg.Git.RepoURL == "" {
		return nil, fmt.Errorf("git repo_url not configured")
	}

	ghClient, err := github.NewClientFromRemote(cfg.Git.RepoURL)
	if err != nil {
		return nil, fmt.Errorf("failed to create GitHub client: %w", err)
	}

	// Use longer timeout for PR operations
	ghClient = ghClient.WithTimeout(2 * time.Minute)

	return NewClient(ghClient), nil
}

// Provider returns the forge provider type.
func (c *Client) Provider() forge.Provider {
	return forge.ProviderGitHub
}

// RepoPath returns the owner/repo path.
func (c *Client) RepoPath() string {
	return c.ghClient.RepoPath()
}

// ListPRsForBranch lists pull requests for a specific head branch.
func (c *Client) ListPRsForBranch(ctx context.Context, branch string) ([]forge.PullRequest, error) {
	prs, err := c.ghClient.ListPRsForBranch(ctx, branch)
	if err != nil {
		return nil, err
	}

	result := make([]forge.PullRequest, len(prs))
	for i, pr := range prs {
		result[i] = convertPR(&pr)
	}
	return result, nil
}

// GetPR retrieves a pull request by number or branch name.
func (c *Client) GetPR(ctx context.Context, ref string) (*forge.PullRequest, error) {
	pr, err := c.ghClient.GetPR(ctx, ref)
	if err != nil {
		return nil, err
	}
	result := convertPR(pr)
	return &result, nil
}

// CreatePR creates a new pull request.
func (c *Client) CreatePR(ctx context.Context, opts forge.PRCreateOptions) (*forge.PullRequest, error) {
	pr, err := c.ghClient.CreatePR(ctx, github.PRCreateOptions{
		Title: opts.Title,
		Body:  opts.Body,
		Head:  opts.Head,
		Base:  opts.Base,
		Draft: opts.Draft,
	})
	if err != nil {
		return nil, err
	}
	result := convertPR(pr)
	return &result, nil
}

// GetOrCreatePR returns an existing PR for the branch or creates a new one.
func (c *Client) GetOrCreatePR(ctx context.Context, opts forge.PRCreateOptions) (*forge.PullRequest, error) {
	pr, err := c.ghClient.GetOrCreatePR(ctx, github.PRCreateOptions{
		Title: opts.Title,
		Body:  opts.Body,
		Head:  opts.Head,
		Base:  opts.Base,
		Draft: opts.Draft,
	})
	if err != nil {
		return nil, err
	}
	result := convertPR(pr)
	return &result, nil
}

// MergePR merges a pull request.
func (c *Client) MergePR(ctx context.Context, ref string, opts forge.PRMergeOptions) error {
	return c.ghClient.MergePR(ctx, ref, github.PRMergeOptions{
		Method:       opts.Method,
		DeleteBranch: opts.DeleteBranch,
	})
}

// MergePRWithResult merges a PR and returns detailed result.
func (c *Client) MergePRWithResult(ctx context.Context, ref string, opts forge.PRMergeOptions) (*forge.MergeResult, error) {
	result, err := c.ghClient.MergePRWithResult(ctx, ref, github.PRMergeOptions{
		Method:       opts.Method,
		DeleteBranch: opts.DeleteBranch,
	})
	if err != nil {
		return nil, err
	}

	return &forge.MergeResult{
		Merged:       result.Merged,
		SHA:          result.SHA,
		HasConflicts: result.HasConflicts,
		ConflictInfo: result.ConflictInfo,
	}, nil
}

// ClosePR closes a pull request without merging.
func (c *Client) ClosePR(ctx context.Context, ref string) error {
	return c.ghClient.ClosePR(ctx, ref)
}

// CleanupMergedBranches deletes branches that have been merged.
func (c *Client) CleanupMergedBranches(ctx context.Context, target string, protectedPatterns []string) ([]string, error) {
	return c.ghClient.CleanupMergedBranches(ctx, target, protectedPatterns)
}

// convertPR converts a github.PullRequest to forge.PullRequest.
func convertPR(pr *github.PullRequest) forge.PullRequest {
	result := forge.PullRequest{
		Number:     pr.Number,
		URL:        pr.URL,
		Title:      pr.Title,
		State:      pr.State,
		HeadBranch: pr.HeadRefName,
		HeadSHA:    pr.HeadRefOid,
		BaseBranch: pr.BaseRefName,
		BaseSHA:    pr.BaseRefOid,
		Merged:     pr.IsMerged(),
		Mergeable:  pr.Mergeable == "MERGEABLE",
	}

	// Parse MergedAt if present
	if pr.MergedAt != "" {
		if t, err := time.Parse(time.RFC3339, pr.MergedAt); err == nil {
			result.MergedAt = &t
		}
	}

	// Check for conflicts
	result.HasConflicts = pr.Mergeable == "CONFLICTING"

	return result
}
