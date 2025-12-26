package gitea

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"orchestrator/pkg/forge"
	"orchestrator/pkg/logx"
)

// Client implements forge.Client for Gitea API operations.
type Client struct {
	baseURL string
	token   string
	owner   string
	repo    string
	logger  *logx.Logger
	client  *http.Client
}

// NewClient creates a new Gitea API client.
func NewClient(baseURL, token, owner, repo string) *Client {
	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		token:   token,
		owner:   owner,
		repo:    repo,
		logger:  logx.NewLogger("gitea-client"),
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// Provider returns the forge provider type.
func (c *Client) Provider() forge.Provider {
	return forge.ProviderGitea
}

// RepoPath returns the owner/repo path.
func (c *Client) RepoPath() string {
	return fmt.Sprintf("%s/%s", c.owner, c.repo)
}

// apiURL constructs a full API URL.
func (c *Client) apiURL(path string) string {
	return fmt.Sprintf("%s/api/v1%s", c.baseURL, path)
}

// doRequest performs an HTTP request with authentication.
func (c *Client) doRequest(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
	url := c.apiURL(path)

	var bodyReader io.Reader
	if body != nil {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "token "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	c.logger.Debug("%s %s", method, url)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}

// Gitea API response structures.
type giteaPR struct {
	Number    int       `json:"number"`
	HTMLURL   string    `json:"html_url"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	State     string    `json:"state"` // open, closed
	Merged    bool      `json:"merged"`
	MergedAt  *string   `json:"merged_at"`
	Mergeable bool      `json:"mergeable"`
	Head      giteaRef  `json:"head"`
	Base      giteaRef  `json:"base"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type giteaRef struct {
	Label string `json:"label"`
	Ref   string `json:"ref"`
	SHA   string `json:"sha"`
}

// convertPR converts a Gitea PR to forge.PullRequest.
func convertPR(gpr *giteaPR) *forge.PullRequest {
	pr := &forge.PullRequest{
		Number:     gpr.Number,
		URL:        gpr.HTMLURL,
		Title:      gpr.Title,
		Body:       gpr.Body,
		State:      gpr.State,
		HeadBranch: gpr.Head.Ref,
		HeadSHA:    gpr.Head.SHA,
		BaseBranch: gpr.Base.Ref,
		BaseSHA:    gpr.Base.SHA,
		Merged:     gpr.Merged,
		Mergeable:  gpr.Mergeable,
	}

	if gpr.MergedAt != nil && *gpr.MergedAt != "" {
		if t, err := time.Parse(time.RFC3339, *gpr.MergedAt); err == nil {
			pr.MergedAt = &t
		}
	}

	return pr
}

// ListPRsForBranch lists pull requests for a specific head branch.
func (c *Client) ListPRsForBranch(ctx context.Context, branch string) ([]forge.PullRequest, error) {
	// Gitea API requires owner:branch format for head filter
	head := fmt.Sprintf("%s:%s", c.owner, branch)
	path := fmt.Sprintf("/repos/%s/%s/pulls?state=open&head=%s", c.owner, c.repo, head)

	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list PRs failed with status %d: %s", resp.StatusCode, string(body))
	}

	var giteaPRs []giteaPR
	if err := json.NewDecoder(resp.Body).Decode(&giteaPRs); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Filter by branch name (Gitea's head filter might not be exact match)
	result := make([]forge.PullRequest, 0, len(giteaPRs))
	for i := range giteaPRs {
		if giteaPRs[i].Head.Ref == branch {
			result = append(result, *convertPR(&giteaPRs[i]))
		}
	}

	return result, nil
}

// GetPR retrieves a pull request by number or branch name.
func (c *Client) GetPR(ctx context.Context, ref string) (*forge.PullRequest, error) {
	// Check if ref is a number
	if prNum, err := strconv.Atoi(ref); err == nil {
		return c.getPRByNumber(ctx, prNum)
	}

	// Otherwise, search by branch name
	prs, err := c.ListPRsForBranch(ctx, ref)
	if err != nil {
		return nil, err
	}

	if len(prs) == 0 {
		return nil, fmt.Errorf("no PR found for branch %s", ref)
	}

	return &prs[0], nil
}

// getPRByNumber retrieves a PR by its number.
func (c *Client) getPRByNumber(ctx context.Context, number int) (*forge.PullRequest, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", c.owner, c.repo, number)

	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("PR #%d not found", number)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get PR failed with status %d: %s", resp.StatusCode, string(body))
	}

	var gpr giteaPR
	if err := json.NewDecoder(resp.Body).Decode(&gpr); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return convertPR(&gpr), nil
}

// CreatePR creates a new pull request.
func (c *Client) CreatePR(ctx context.Context, opts forge.PRCreateOptions) (*forge.PullRequest, error) {
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

	payload := map[string]interface{}{
		"title": opts.Title,
		"head":  opts.Head,
		"base":  base,
	}

	if opts.Body != "" {
		payload["body"] = opts.Body
	}

	path := fmt.Sprintf("/repos/%s/%s/pulls", c.owner, c.repo)

	resp, err := c.doRequest(ctx, http.MethodPost, path, payload)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusUnprocessableEntity {
		// Check if PR already exists
		if strings.Contains(string(body), "pull request already exists") {
			prs, listErr := c.ListPRsForBranch(ctx, opts.Head)
			if listErr == nil && len(prs) > 0 {
				return &prs[0], nil
			}
		}
		return nil, fmt.Errorf("create PR failed: %s", string(body))
	}

	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("create PR failed with status %d: %s", resp.StatusCode, string(body))
	}

	var gpr giteaPR
	if err := json.Unmarshal(body, &gpr); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	c.logger.Info("Created PR #%d: %s", gpr.Number, gpr.Title)
	return convertPR(&gpr), nil
}

// GetOrCreatePR returns an existing PR for the branch or creates a new one.
func (c *Client) GetOrCreatePR(ctx context.Context, opts forge.PRCreateOptions) (*forge.PullRequest, error) {
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

// MergePR merges a pull request.
func (c *Client) MergePR(ctx context.Context, ref string, opts forge.PRMergeOptions) error {
	_, err := c.MergePRWithResult(ctx, ref, opts)
	return err
}

// MergePRWithResult merges a PR and returns detailed result.
func (c *Client) MergePRWithResult(ctx context.Context, ref string, opts forge.PRMergeOptions) (*forge.MergeResult, error) {
	// Get PR number
	pr, err := c.GetPR(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("failed to get PR: %w", err)
	}

	// Determine merge method
	method := opts.Method
	if method == "" {
		method = "squash"
	}

	// Map method names to Gitea's expected values
	var giteaMethod string
	switch method {
	case "merge":
		giteaMethod = "merge"
	case "rebase":
		giteaMethod = "rebase"
	case "squash":
		giteaMethod = "squash"
	default:
		giteaMethod = "squash"
	}

	payload := map[string]interface{}{
		"do":                        giteaMethod,
		"delete_branch_after_merge": opts.DeleteBranch,
	}

	if opts.CommitTitle != "" {
		payload["merge_commit_message"] = opts.CommitTitle
	}

	if opts.CommitMessage != "" {
		payload["merge_commit_message"] = opts.CommitMessage
	}

	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/merge", c.owner, c.repo, pr.Number)

	resp, err := c.doRequest(ctx, http.MethodPost, path, payload)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	result := &forge.MergeResult{}

	// Handle various response codes
	switch resp.StatusCode {
	case http.StatusOK:
		result.Merged = true
		result.Message = "PR merged successfully"
		c.logger.Info("Merged PR #%d", pr.Number)
		return result, nil

	case http.StatusMethodNotAllowed:
		// PR is not mergeable (e.g., conflicts, not approved)
		if strings.Contains(string(body), "conflict") {
			result.HasConflicts = true
			result.ConflictInfo = string(body)
			return result, nil
		}
		return nil, fmt.Errorf("merge not allowed: %s", string(body))

	case http.StatusNotFound:
		return nil, fmt.Errorf("PR #%d not found", pr.Number)

	default:
		return nil, fmt.Errorf("merge failed with status %d: %s", resp.StatusCode, string(body))
	}
}

// ClosePR closes a pull request without merging.
func (c *Client) ClosePR(ctx context.Context, ref string) error {
	// Get PR number
	pr, err := c.GetPR(ctx, ref)
	if err != nil {
		return fmt.Errorf("failed to get PR: %w", err)
	}

	payload := map[string]interface{}{
		"state": "closed",
	}

	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", c.owner, c.repo, pr.Number)

	resp, err := c.doRequest(ctx, http.MethodPatch, path, payload)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("close PR failed with status %d: %s", resp.StatusCode, string(body))
	}

	c.logger.Info("Closed PR #%d", pr.Number)
	return nil
}

// CleanupMergedBranches deletes branches that have been merged.
func (c *Client) CleanupMergedBranches(ctx context.Context, target string, protectedPatterns []string) ([]string, error) {
	// List all branches
	path := fmt.Sprintf("/repos/%s/%s/branches", c.owner, c.repo)

	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list branches failed with status %d: %s", resp.StatusCode, string(body))
	}

	var branches []struct {
		Name      string `json:"name"`
		Protected bool   `json:"protected"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&branches); err != nil {
		return nil, fmt.Errorf("failed to decode branches: %w", err)
	}

	var deleted []string
	for _, branch := range branches {
		// Skip protected branches
		if branch.Protected {
			continue
		}

		// Skip target branch
		if branch.Name == target {
			continue
		}

		// Skip branches matching protected patterns
		protected := false
		for _, pattern := range protectedPatterns {
			if strings.Contains(branch.Name, pattern) || branch.Name == pattern {
				protected = true
				break
			}
		}
		if protected {
			continue
		}

		// Check if branch has merged PR
		prs, err := c.ListPRsForBranch(ctx, branch.Name)
		if err != nil {
			c.logger.Debug("Failed to check PRs for branch %s: %v", branch.Name, err)
			continue
		}

		// Check for merged PRs
		hasMergedPR := false
		for i := range prs {
			if prs[i].IsMerged() {
				hasMergedPR = true
				break
			}
		}

		if !hasMergedPR {
			continue
		}

		// Delete the branch
		if err := c.deleteBranch(ctx, branch.Name); err != nil {
			c.logger.Warn("Failed to delete branch %s: %v", branch.Name, err)
			continue
		}

		deleted = append(deleted, branch.Name)
	}

	return deleted, nil
}

// deleteBranch deletes a branch by name.
func (c *Client) deleteBranch(ctx context.Context, branch string) error {
	path := fmt.Sprintf("/repos/%s/%s/branches/%s", c.owner, c.repo, branch)

	resp, err := c.doRequest(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete branch failed with status %d: %s", resp.StatusCode, string(body))
	}

	c.logger.Info("Deleted branch %s", branch)
	return nil
}

// WaitForBranches waits for Gitea to index branches after a push.
// Gitea can take a moment to index pushed content - this function
// polls until at least one branch is available.
func (c *Client) WaitForBranches(ctx context.Context, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for branches to be indexed: %w", ctx.Err())
		case <-ticker.C:
			path := fmt.Sprintf("/repos/%s/%s/branches", c.owner, c.repo)
			resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
			if err != nil {
				continue // Retry on error
			}

			var branches []struct {
				Name string `json:"name"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&branches); err != nil {
				_ = resp.Body.Close()
				continue
			}
			_ = resp.Body.Close()

			if len(branches) > 0 {
				c.logger.Debug("Found %d branches after indexing", len(branches))
				return nil
			}
		}
	}
}

// WaitForBranch waits for a specific branch to be indexed in Gitea.
// Use this after pushing a new branch to ensure it's available via API.
func (c *Client) WaitForBranch(ctx context.Context, branchName string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for branch %q to be indexed: %w", branchName, ctx.Err())
		case <-ticker.C:
			path := fmt.Sprintf("/repos/%s/%s/branches", c.owner, c.repo)
			resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
			if err != nil {
				continue // Retry on error
			}

			var branches []struct {
				Name string `json:"name"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&branches); err != nil {
				_ = resp.Body.Close()
				continue
			}
			_ = resp.Body.Close()

			for _, b := range branches {
				if b.Name == branchName {
					c.logger.Debug("Branch %q is now indexed", branchName)
					return nil
				}
			}
		}
	}
}

// BaseURL returns the base URL of the Gitea instance.
func (c *Client) BaseURL() string {
	return c.baseURL
}

// Owner returns the repository owner.
func (c *Client) Owner() string {
	return c.owner
}

// Repo returns the repository name.
func (c *Client) Repo() string {
	return c.repo
}

// CloneURL returns the HTTP clone URL for the repository.
func (c *Client) CloneURL() string {
	return fmt.Sprintf("%s/%s/%s.git", c.baseURL, c.owner, c.repo)
}
