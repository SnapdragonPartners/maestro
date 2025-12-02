package github

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// PullRequest represents a GitHub pull request.
// Field names match gh CLI --json output (GraphQL field names).
//
//nolint:govet // Logical grouping preferred over memory optimization
type PullRequest struct {
	Number      int    `json:"number"`
	URL         string `json:"url"`
	Title       string `json:"title"`
	State       string `json:"state"`       // OPEN, CLOSED, MERGED
	HeadRefName string `json:"headRefName"` // Branch name (gh CLI)
	HeadRefOid  string `json:"headRefOid"`  // Commit SHA (gh CLI)
	BaseRefName string `json:"baseRefName"` // Target branch name (gh CLI)
	BaseRefOid  string `json:"baseRefOid"`  // Target commit SHA (gh CLI)
	Closed      bool   `json:"closed"`      // Whether PR is closed
	MergedAt    string `json:"mergedAt"`    // Non-empty if merged
	Mergeable   string `json:"mergeable"`   // MERGEABLE, CONFLICTING, or UNKNOWN
}

// IsMerged returns true if the PR has been merged.
func (pr *PullRequest) IsMerged() bool {
	return pr.MergedAt != ""
}

// PRCreateOptions contains options for creating a pull request.
type PRCreateOptions struct {
	Title string
	Body  string
	Head  string // Source branch
	Base  string // Target branch (default: main)
	Draft bool
}

// PRMergeOptions contains options for merging a pull request.
type PRMergeOptions struct {
	Method       string // merge, squash, or rebase (default: squash)
	DeleteBranch bool   // Delete branch after merge
}

// ListPRs lists pull requests for the repository.
func (c *Client) ListPRs(ctx context.Context, state string) ([]PullRequest, error) {
	args := []string{
		"pr", "list",
		"--repo", c.RepoPath(),
		"--json", "number,url,title,state,headRefName,headRefOid,baseRefName,baseRefOid,closed,mergedAt",
	}

	if state != "" {
		args = append(args, "--state", state)
	}

	var prs []PullRequest
	if err := c.runJSON(ctx, &prs, args...); err != nil {
		return nil, fmt.Errorf("failed to list PRs: %w", err)
	}

	return prs, nil
}

// ListPRsForBranch lists pull requests for a specific head branch.
func (c *Client) ListPRsForBranch(ctx context.Context, branch string) ([]PullRequest, error) {
	args := []string{
		"pr", "list",
		"--repo", c.RepoPath(),
		"--head", branch,
		"--json", "number,url,title,state,headRefName,headRefOid,baseRefName,baseRefOid,closed,mergedAt",
	}

	var prs []PullRequest
	if err := c.runJSON(ctx, &prs, args...); err != nil {
		return nil, fmt.Errorf("failed to list PRs for branch %s: %w", branch, err)
	}

	return prs, nil
}

// GetPR retrieves a pull request by number or branch name.
func (c *Client) GetPR(ctx context.Context, ref string) (*PullRequest, error) {
	args := []string{
		"pr", "view", ref,
		"--repo", c.RepoPath(),
		"--json", "number,url,title,state,headRefName,headRefOid,baseRefName,baseRefOid,closed,mergedAt,mergeable",
	}

	var pr PullRequest
	if err := c.runJSON(ctx, &pr, args...); err != nil {
		return nil, fmt.Errorf("failed to get PR %s: %w", ref, err)
	}

	return &pr, nil
}

// CreatePR creates a new pull request.
func (c *Client) CreatePR(ctx context.Context, opts PRCreateOptions) (*PullRequest, error) {
	if opts.Head == "" {
		return nil, fmt.Errorf("head branch is required")
	}
	if opts.Title == "" {
		return nil, fmt.Errorf("title is required")
	}

	base := opts.Base
	if base == "" {
		base = "main"
	}

	args := []string{
		"pr", "create",
		"--repo", c.RepoPath(),
		"--title", opts.Title,
		"--head", opts.Head,
		"--base", base,
	}

	if opts.Body != "" {
		args = append(args, "--body", opts.Body)
	}

	if opts.Draft {
		args = append(args, "--draft")
	}

	// Use longer timeout for PR creation
	client := c.WithTimeout(2 * time.Minute)
	output, err := client.run(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to create PR: %w", err)
	}

	// gh pr create returns the PR URL
	prURL := strings.TrimSpace(string(output))
	if prURL == "" {
		return nil, fmt.Errorf("PR created but no URL returned")
	}

	// Fetch the full PR details
	return c.GetPR(ctx, prURL)
}

// MergePR merges a pull request.
func (c *Client) MergePR(ctx context.Context, ref string, opts PRMergeOptions) error {
	method := opts.Method
	if method == "" {
		method = "squash"
	}

	args := []string{
		"pr", "merge", ref,
		"--repo", c.RepoPath(),
		"--" + method,
	}

	if opts.DeleteBranch {
		args = append(args, "--delete-branch")
	}

	// Use longer timeout for merge
	client := c.WithTimeout(2 * time.Minute)
	_, err := client.run(ctx, args...)
	if err != nil {
		return fmt.Errorf("failed to merge PR %s: %w", ref, err)
	}

	return nil
}

// ClosePR closes a pull request without merging.
func (c *Client) ClosePR(ctx context.Context, ref string) error {
	args := []string{
		"pr", "close", ref,
		"--repo", c.RepoPath(),
	}

	_, err := c.run(ctx, args...)
	if err != nil {
		return fmt.Errorf("failed to close PR %s: %w", ref, err)
	}

	return nil
}

// PRExists checks if a PR exists for the given branch.
func (c *Client) PRExists(ctx context.Context, branch string) (bool, error) {
	prs, err := c.ListPRsForBranch(ctx, branch)
	if err != nil {
		return false, err
	}
	return len(prs) > 0, nil
}

// GetOrCreatePR returns an existing PR for the branch or creates a new one.
func (c *Client) GetOrCreatePR(ctx context.Context, opts PRCreateOptions) (*PullRequest, error) {
	// Check for existing PR
	prs, err := c.ListPRsForBranch(ctx, opts.Head)
	if err != nil {
		c.logger.Debug("Failed to check for existing PR, will try to create: %v", err)
	} else if len(prs) > 0 {
		c.logger.Debug("Found existing PR #%d for branch %s", prs[0].Number, opts.Head)
		return &prs[0], nil
	}

	// Create new PR
	return c.CreatePR(ctx, opts)
}

// MergeResult contains the result of a merge operation.
//
//nolint:govet // Logical grouping preferred over memory optimization
type MergeResult struct {
	Merged       bool
	SHA          string
	HasConflicts bool
	ConflictInfo string
}

// MergePRWithResult merges a PR and returns detailed result.
func (c *Client) MergePRWithResult(ctx context.Context, ref string, opts PRMergeOptions) (*MergeResult, error) {
	method := opts.Method
	if method == "" {
		method = "squash"
	}

	args := []string{
		"pr", "merge", ref,
		"--repo", c.RepoPath(),
		"--" + method,
	}

	if opts.DeleteBranch {
		args = append(args, "--delete-branch")
	}

	client := c.WithTimeout(2 * time.Minute)
	output, err := client.run(ctx, args...)

	result := &MergeResult{}

	if err != nil {
		outputStr := strings.ToLower(string(output))
		if strings.Contains(outputStr, "conflict") || strings.Contains(outputStr, "merge conflict") {
			result.HasConflicts = true
			result.ConflictInfo = string(output)
			return result, nil // Conflicts are not an error, just a state
		}
		return nil, fmt.Errorf("merge failed: %w", err)
	}

	result.Merged = true
	// TODO: Parse SHA from output if needed
	result.SHA = "merged"

	return result, nil
}

// EnablePRAutoMerge enables auto-merge for a specific pull request.
func (c *Client) EnablePRAutoMerge(ctx context.Context, prNumber int, method string) error {
	if method == "" {
		method = "SQUASH"
	}

	// First, get the PR's GraphQL node ID (required for the mutation)
	nodeID, err := c.getPRNodeID(ctx, prNumber)
	if err != nil {
		return fmt.Errorf("failed to get PR node ID: %w", err)
	}

	// Use GraphQL mutation for auto-merge
	query := fmt.Sprintf(`mutation {
		enablePullRequestAutoMerge(input: {pullRequestId: "%s", mergeMethod: %s}) {
			clientMutationId
		}
	}`, nodeID, method)

	args := []string{"api", "graphql", "-f", fmt.Sprintf("query=%s", query)}
	_, err = c.run(ctx, args...)
	if err != nil {
		return fmt.Errorf("failed to enable auto-merge: %w", err)
	}

	return nil
}

// getPRNodeID retrieves the GraphQL node ID for a pull request.
func (c *Client) getPRNodeID(ctx context.Context, prNumber int) (string, error) {
	args := []string{
		"pr", "view", fmt.Sprintf("%d", prNumber),
		"--repo", c.RepoPath(),
		"--json", "id",
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := c.runJSON(ctx, &result, args...); err != nil {
		return "", fmt.Errorf("failed to get PR #%d: %w", prNumber, err)
	}

	if result.ID == "" {
		return "", fmt.Errorf("PR #%d has no node ID", prNumber)
	}

	return result.ID, nil
}

// PRComment represents a comment on a pull request.
//
//nolint:govet // Logical grouping preferred over memory optimization
type PRComment struct {
	ID        int       `json:"id"`
	Body      string    `json:"body"`
	Author    string    `json:"author"`
	CreatedAt time.Time `json:"createdAt"`
}

// CommentOnPR adds a comment to a pull request.
func (c *Client) CommentOnPR(ctx context.Context, ref, body string) error {
	args := []string{
		"pr", "comment", ref,
		"--repo", c.RepoPath(),
		"--body", body,
	}

	_, err := c.run(ctx, args...)
	if err != nil {
		return fmt.Errorf("failed to comment on PR %s: %w", ref, err)
	}

	return nil
}

// GetPRComments retrieves comments on a pull request.
func (c *Client) GetPRComments(ctx context.Context, prNumber int) ([]PRComment, error) {
	endpoint := fmt.Sprintf("/repos/%s/issues/%d/comments", c.RepoPath(), prNumber)
	output, err := c.APIGet(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to get PR comments: %w", err)
	}

	var comments []PRComment
	if err := json.Unmarshal(output, &comments); err != nil {
		return nil, fmt.Errorf("failed to parse comments: %w", err)
	}

	return comments, nil
}
