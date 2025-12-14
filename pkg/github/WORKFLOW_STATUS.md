# GitHub Workflow Status API

This package provides methods to check the status of GitHub Actions workflows for commits and pull requests.

## Overview

The workflow status functionality allows you to:
- Get workflow runs for a specific commit or PR
- Check the overall status of all workflows
- Determine if all workflows are passing before merging a PR

## Key Types

### WorkflowRun
Represents a GitHub Actions workflow run with status and conclusion information.

### WorkflowStatus
Aggregated status of all workflow runs for a commit:
- `State`: One of "success", "failure", or "pending"
- `TotalRuns`: Total number of workflow runs
- `Successful`: Number of successful runs
- `Failed`: Number of failed runs
- `Pending`: Number of pending/in-progress runs
- `FailedRuns`: Names of failed workflow runs

## Usage Examples

### Check if PR workflows are passing

```go
ctx := context.Background()
client := github.NewClient("owner", "repo")

// Simple boolean check
passing, err := client.IsPRWorkflowPassing(ctx, prNumber)
if err != nil {
    return err
}

if passing {
    // All workflows passed, safe to merge
    return client.MergePR(ctx, prNumber, mergeOpts)
}
```

### Get detailed workflow status

```go
status, err := client.GetPRWorkflowStatus(ctx, prNumber)
if err != nil {
    return err
}

switch status.State {
case github.WorkflowStateSuccess:
    log.Println("All workflows passed!")
case github.WorkflowStateFailure:
    log.Printf("Failed workflows: %v", status.FailedRuns)
case github.WorkflowStatePending:
    log.Printf("Waiting for %d workflows to complete", status.Pending)
}
```

### Get workflow runs for a PR

```go
runs, err := client.GetWorkflowRunsForPR(ctx, prNumber)
if err != nil {
    return err
}

for _, run := range runs {
    fmt.Printf("%s: %s\n", run.Name, run.Status)
    if run.Status == "completed" {
        fmt.Printf("  Result: %s\n", run.Conclusion)
    }
}
```

## API Methods

### GetWorkflowRunsForRef
```go
func (c *Client) GetWorkflowRunsForRef(ctx context.Context, ref string) ([]WorkflowRun, error)
```
Get all workflow runs for a specific git ref (branch name or commit SHA).

### GetWorkflowRunsForPR
```go
func (c *Client) GetWorkflowRunsForPR(ctx context.Context, prNumber int) ([]WorkflowRun, error)
```
Get all workflow runs for a pull request.

### GetWorkflowStatus
```go
func (c *Client) GetWorkflowStatus(ctx context.Context, commitSHA string) (*WorkflowStatus, error)
```
Get the aggregated status of all workflows for a commit.

### GetPRWorkflowStatus
```go
func (c *Client) GetPRWorkflowStatus(ctx context.Context, prNumber int) (*WorkflowStatus, error)
```
Get the aggregated status of all workflows for a pull request.

### IsPRWorkflowPassing
```go
func (c *Client) IsPRWorkflowPassing(ctx context.Context, prNumber int) (bool, error)
```
Convenience method to check if all workflows are passing for a PR.

## Workflow States

- `WorkflowStateSuccess`: All workflows completed successfully
- `WorkflowStateFailure`: One or more workflows failed
- `WorkflowStatePending`: Workflows are queued or in progress

## Implementation Notes

- Uses the GitHub API v3 actions endpoints
- No workflow runs is considered a success (no checks required)
- Cancelled and skipped runs are not counted as failures
- Timed out and startup failures are counted as failures

## Testing

Unit tests are in `workflow_test.go`.
Integration tests are in `integration_test.go` (requires GitHub token).
Example usage is in `workflow_example_test.go`.

## Future Enhancements

Potential future additions:
- Wait for workflows to complete with polling/timeout
- Get detailed check run information
- Trigger workflow reruns
- Filter workflows by name or path
