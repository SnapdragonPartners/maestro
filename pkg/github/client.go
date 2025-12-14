// Package github provides centralized GitHub API operations using the gh CLI.
// All operations run on the host (not in containers) since they're pure API calls.
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"orchestrator/pkg/logx"
)

// DefaultBranch is the default target branch for operations.
const DefaultBranch = "main"

// GitHubClient defines the interface for GitHub operations.
// This interface enables testing with mock implementations.
type GitHubClient interface {
	// PR operations
	ListPRsForBranch(ctx context.Context, branch string) ([]PullRequest, error)
	CreatePR(ctx context.Context, opts PRCreateOptions) (*PullRequest, error)
	MergePRWithResult(ctx context.Context, ref string, opts PRMergeOptions) (*MergeResult, error)

	// Branch operations
	CleanupMergedBranches(ctx context.Context, target string, protectedPatterns []string) ([]string, error)

	// Workflow/Actions operations
	GetWorkflowRunsForRef(ctx context.Context, ref string) ([]WorkflowRun, error)
	GetWorkflowRunsForPR(ctx context.Context, prNumber int) ([]WorkflowRun, error)
	GetWorkflowStatus(ctx context.Context, commitSHA string) (*WorkflowStatus, error)
	GetPRWorkflowStatus(ctx context.Context, prNumber int) (*WorkflowStatus, error)
	IsPRWorkflowPassing(ctx context.Context, prNumber int) (bool, error)

	// Configuration
	WithTimeout(timeout time.Duration) *Client
	RepoPath() string
}

// Client provides GitHub API operations via the gh CLI.
// Client implements GitHubClient interface.
//
//nolint:govet // Logical grouping preferred over memory optimization
type Client struct {
	owner   string
	repo    string
	logger  *logx.Logger
	timeout time.Duration
}

// NewClient creates a new GitHub client for the specified repository.
func NewClient(owner, repo string) *Client {
	return &Client{
		owner:   owner,
		repo:    repo,
		logger:  logx.NewLogger("github"),
		timeout: 30 * time.Second,
	}
}

// NewClientFromRemote creates a GitHub client by parsing a git remote URL.
func NewClientFromRemote(remoteURL string) (*Client, error) {
	owner, repo, err := ParseGitHubURL(remoteURL)
	if err != nil {
		return nil, err
	}
	return NewClient(owner, repo), nil
}

// WithTimeout returns a new client with the specified timeout.
func (c *Client) WithTimeout(timeout time.Duration) *Client {
	return &Client{
		owner:   c.owner,
		repo:    c.repo,
		logger:  c.logger,
		timeout: timeout,
	}
}

// Owner returns the repository owner.
func (c *Client) Owner() string {
	return c.owner
}

// Repo returns the repository name.
func (c *Client) Repo() string {
	return c.repo
}

// RepoPath returns the owner/repo path.
func (c *Client) RepoPath() string {
	return fmt.Sprintf("%s/%s", c.owner, c.repo)
}

// API executes a GitHub API call and returns the raw response.
func (c *Client) API(ctx context.Context, method, endpoint string, fields map[string]interface{}) ([]byte, error) {
	args := []string{"api", "-X", method, endpoint}

	// Add fields
	for key, value := range fields {
		switch v := value.(type) {
		case bool:
			args = append(args, "-f", fmt.Sprintf("%s=%t", key, v))
		case string:
			args = append(args, "-f", fmt.Sprintf("%s=%s", key, v))
		case int, int64:
			args = append(args, "-f", fmt.Sprintf("%s=%d", key, v))
		default:
			args = append(args, "-f", fmt.Sprintf("%s=%v", key, v))
		}
	}

	return c.run(ctx, args...)
}

// APIGet executes a GET request to the GitHub API.
func (c *Client) APIGet(ctx context.Context, endpoint string) ([]byte, error) {
	return c.API(ctx, "GET", endpoint, nil)
}

// APIPut executes a PUT request to the GitHub API.
func (c *Client) APIPut(ctx context.Context, endpoint string, fields map[string]interface{}) ([]byte, error) {
	return c.API(ctx, "PUT", endpoint, fields)
}

// APIPatch executes a PATCH request to the GitHub API.
func (c *Client) APIPatch(ctx context.Context, endpoint string, fields map[string]interface{}) ([]byte, error) {
	return c.API(ctx, "PATCH", endpoint, fields)
}

// APIDelete executes a DELETE request to the GitHub API.
func (c *Client) APIDelete(ctx context.Context, endpoint string) ([]byte, error) {
	return c.API(ctx, "DELETE", endpoint, nil)
}

// run executes a gh command and returns the output.
func (c *Client) run(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	c.logger.Debug("Executing: gh %s", strings.Join(args, " "))

	cmd := exec.CommandContext(ctx, "gh", args...)
	output, err := cmd.CombinedOutput()

	if err != nil {
		c.logger.Debug("Command failed: %v, output: %s", err, string(output))
		return nil, fmt.Errorf("gh command failed: %w\nOutput: %s", err, string(output))
	}

	return output, nil
}

// runJSON executes a gh command and unmarshals the JSON response.
func (c *Client) runJSON(ctx context.Context, result interface{}, args ...string) error {
	output, err := c.run(ctx, args...)
	if err != nil {
		return err
	}

	if len(output) == 0 {
		return nil // Empty response is valid for some operations
	}

	if err := json.Unmarshal(output, result); err != nil {
		return fmt.Errorf("failed to parse JSON response: %w\nOutput: %s", err, string(output))
	}

	return nil
}

// ParseGitHubURL extracts owner and repo from various GitHub URL formats.
func ParseGitHubURL(url string) (owner, repo string, err error) {
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

// CheckAuth verifies that gh CLI is authenticated.
func CheckAuth(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "gh", "auth", "status")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh auth check failed: %w\nOutput: %s", err, string(output))
	}
	return nil
}
