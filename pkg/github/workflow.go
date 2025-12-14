package github

import (
	"context"
	"encoding/json"
	"fmt"
)

const (
	// WorkflowStateSuccess represents a successful workflow state.
	WorkflowStateSuccess = "success"
	// WorkflowStateFailure represents a failed workflow state.
	WorkflowStateFailure = "failure"
	// WorkflowStatePending represents a pending workflow state.
	WorkflowStatePending = "pending"
)

// WorkflowRun represents a GitHub Actions workflow run.
//
//nolint:govet // Logical grouping preferred over memory optimization
type WorkflowRun struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	HeadBranch string `json:"head_branch"`
	HeadSHA    string `json:"head_sha"`
	Status     string `json:"status"`     // queued, in_progress, completed
	Conclusion string `json:"conclusion"` // success, failure, cancelled, skipped, etc. (only for completed runs)
	WorkflowID int64  `json:"workflow_id"`
	URL        string `json:"html_url"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
	RunNumber  int    `json:"run_number"`
	Event      string `json:"event"`
	RunAttempt int    `json:"run_attempt"`
}

// WorkflowRunsResponse represents the API response for listing workflow runs.
//
//nolint:govet // fieldalignment: API response struct, field order matches API
type WorkflowRunsResponse struct {
	TotalCount   int           `json:"total_count"`
	WorkflowRuns []WorkflowRun `json:"workflow_runs"`
}

// CheckRun represents a check run (individual job/check within a workflow).
//
//nolint:govet // Logical grouping preferred over memory optimization
type CheckRun struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Status      string `json:"status"`     // queued, in_progress, completed
	Conclusion  string `json:"conclusion"` // success, failure, neutral, cancelled, skipped, timed_out, action_required
	StartedAt   string `json:"started_at"`
	CompletedAt string `json:"completed_at"`
	URL         string `json:"html_url"`
	HeadSHA     string `json:"head_sha"`
}

// CheckRunsResponse represents the API response for listing check runs.
//
//nolint:govet // fieldalignment: API response struct, field order matches API
type CheckRunsResponse struct {
	TotalCount int        `json:"total_count"`
	CheckRuns  []CheckRun `json:"check_runs"`
}

// WorkflowStatus represents the overall status of workflows for a commit.
//
//nolint:govet // Logical grouping preferred over memory optimization
type WorkflowStatus struct {
	State      string   // pending, success, failure
	TotalRuns  int      // Total number of workflow runs
	Successful int      // Number of successful runs
	Failed     int      // Number of failed runs
	Pending    int      // Number of pending/in-progress runs
	FailedRuns []string // Names of failed workflow runs
}

// GetWorkflowRunsForRef retrieves workflow runs for a specific git ref (branch or commit SHA).
func (c *Client) GetWorkflowRunsForRef(ctx context.Context, ref string) ([]WorkflowRun, error) {
	endpoint := fmt.Sprintf("/repos/%s/actions/runs?head_sha=%s", c.RepoPath(), ref)
	output, err := c.APIGet(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to get workflow runs for ref %s: %w", ref, err)
	}

	var response WorkflowRunsResponse
	if err := json.Unmarshal(output, &response); err != nil {
		return nil, fmt.Errorf("failed to parse workflow runs: %w", err)
	}

	return response.WorkflowRuns, nil
}

// GetWorkflowRunsForPR retrieves workflow runs for a pull request.
func (c *Client) GetWorkflowRunsForPR(ctx context.Context, prNumber int) ([]WorkflowRun, error) {
	// First, get the PR to find its head SHA
	pr, err := c.GetPR(ctx, fmt.Sprintf("%d", prNumber))
	if err != nil {
		return nil, fmt.Errorf("failed to get PR #%d: %w", prNumber, err)
	}

	// Get workflow runs for the head SHA
	return c.GetWorkflowRunsForRef(ctx, pr.HeadRefOid)
}

// GetCheckRunsForRef retrieves check runs for a specific git ref (commit SHA).
func (c *Client) GetCheckRunsForRef(ctx context.Context, ref string) ([]CheckRun, error) {
	endpoint := fmt.Sprintf("/repos/%s/commits/%s/check-runs", c.RepoPath(), ref)
	output, err := c.APIGet(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to get check runs for ref %s: %w", ref, err)
	}

	var response CheckRunsResponse
	if err := json.Unmarshal(output, &response); err != nil {
		return nil, fmt.Errorf("failed to parse check runs: %w", err)
	}

	return response.CheckRuns, nil
}

// GetWorkflowStatus returns the overall status of workflows for a commit.
func (c *Client) GetWorkflowStatus(ctx context.Context, commitSHA string) (*WorkflowStatus, error) {
	runs, err := c.GetWorkflowRunsForRef(ctx, commitSHA)
	if err != nil {
		return nil, err
	}

	status := &WorkflowStatus{
		TotalRuns:  len(runs),
		FailedRuns: []string{},
	}

	// If no workflow runs, consider it success (no checks required)
	if len(runs) == 0 {
		status.State = WorkflowStateSuccess
		return status, nil
	}

	//nolint:gocritic // rangeValCopy: WorkflowRun is small, copy is acceptable
	for _, run := range runs {
		switch run.Status {
		case "completed":
			switch run.Conclusion {
			case "success":
				status.Successful++
			case "failure", "timed_out", "startup_failure":
				status.Failed++
				status.FailedRuns = append(status.FailedRuns, run.Name)
			case "cancelled", "skipped":
				// Don't count cancelled/skipped as success or failure
			}
		case "queued", "in_progress":
			status.Pending++
		}
	}

	// Determine overall state
	if status.Pending > 0 {
		status.State = WorkflowStatePending
	} else if status.Failed > 0 {
		status.State = WorkflowStateFailure
	} else {
		status.State = WorkflowStateSuccess
	}

	return status, nil
}

// GetPRWorkflowStatus returns the overall workflow status for a pull request.
func (c *Client) GetPRWorkflowStatus(ctx context.Context, prNumber int) (*WorkflowStatus, error) {
	// Get the PR to find its head SHA
	pr, err := c.GetPR(ctx, fmt.Sprintf("%d", prNumber))
	if err != nil {
		return nil, fmt.Errorf("failed to get PR #%d: %w", prNumber, err)
	}

	return c.GetWorkflowStatus(ctx, pr.HeadRefOid)
}

// IsWorkflowPassing checks if all workflows for a commit are passing.
func (c *Client) IsWorkflowPassing(ctx context.Context, commitSHA string) (bool, error) {
	status, err := c.GetWorkflowStatus(ctx, commitSHA)
	if err != nil {
		return false, err
	}

	return status.State == WorkflowStateSuccess, nil
}

// IsPRWorkflowPassing checks if all workflows for a PR are passing.
func (c *Client) IsPRWorkflowPassing(ctx context.Context, prNumber int) (bool, error) {
	status, err := c.GetPRWorkflowStatus(ctx, prNumber)
	if err != nil {
		return false, err
	}

	return status.State == WorkflowStateSuccess, nil
}
