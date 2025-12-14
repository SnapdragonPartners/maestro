package mocks

import (
	"context"
	"time"

	"orchestrator/pkg/github"
)

// MockGitHubClient implements github.GitHubClient for testing.
// It provides configurable behavior for all GitHub operations.
//
//nolint:govet // fieldalignment: mock struct layout optimized for readability
type MockGitHubClient struct {
	// Function handlers for each method
	ListPRsForBranchFunc      func(ctx context.Context, branch string) ([]github.PullRequest, error)
	CreatePRFunc              func(ctx context.Context, opts github.PRCreateOptions) (*github.PullRequest, error)
	MergePRWithResultFunc     func(ctx context.Context, ref string, opts github.PRMergeOptions) (*github.MergeResult, error)
	CleanupMergedBranchesFunc func(ctx context.Context, target string, protectedPatterns []string) ([]string, error)
	GetWorkflowRunsForRefFunc func(ctx context.Context, ref string) ([]github.WorkflowRun, error)
	GetWorkflowRunsForPRFunc  func(ctx context.Context, prNumber int) ([]github.WorkflowRun, error)
	GetCheckRunsForRefFunc    func(ctx context.Context, ref string) ([]github.CheckRun, error)
	GetWorkflowStatusFunc     func(ctx context.Context, commitSHA string) (*github.WorkflowStatus, error)
	GetPRWorkflowStatusFunc   func(ctx context.Context, prNumber int) (*github.WorkflowStatus, error)
	IsWorkflowPassingFunc     func(ctx context.Context, commitSHA string) (bool, error)
	IsPRWorkflowPassingFunc   func(ctx context.Context, prNumber int) (bool, error)

	// Call tracking
	ListPRsForBranchCalls      []ListPRsForBranchCall
	CreatePRCalls              []github.PRCreateOptions
	MergePRWithResultCalls     []MergePRWithResultCall
	CleanupMergedBranchesCalls []CleanupMergedBranchesCall
	GetWorkflowRunsForRefCalls []string
	GetWorkflowRunsForPRCalls  []int
	GetCheckRunsForRefCalls    []string
	GetWorkflowStatusCalls     []string
	GetPRWorkflowStatusCalls   []int
	IsWorkflowPassingCalls     []string
	IsPRWorkflowPassingCalls   []int

	// Configuration
	repoPath string
	timeout  time.Duration
}

// ListPRsForBranchCall records the parameters of a ListPRsForBranch call.
type ListPRsForBranchCall struct {
	Branch string
}

// MergePRWithResultCall records the parameters of a MergePRWithResult call.
type MergePRWithResultCall struct {
	Ref  string
	Opts github.PRMergeOptions
}

// CleanupMergedBranchesCall records the parameters of a CleanupMergedBranches call.
type CleanupMergedBranchesCall struct {
	Target            string
	ProtectedPatterns []string
}

// NewMockGitHubClient creates a new mock GitHub client with default behavior.
func NewMockGitHubClient() *MockGitHubClient {
	m := &MockGitHubClient{
		repoPath: "mock-owner/mock-repo",
		timeout:  30 * time.Second,
	}

	// Default ListPRsForBranch: return empty list
	m.ListPRsForBranchFunc = func(_ context.Context, _ string) ([]github.PullRequest, error) {
		return []github.PullRequest{}, nil
	}

	// Default CreatePR: return a mock PR
	m.CreatePRFunc = func(_ context.Context, opts github.PRCreateOptions) (*github.PullRequest, error) {
		return &github.PullRequest{
			Number:      1,
			URL:         "https://github.com/mock-owner/mock-repo/pull/1",
			Title:       opts.Title,
			State:       "OPEN",
			HeadRefName: opts.Head,
			BaseRefName: opts.Base,
			Mergeable:   "MERGEABLE",
		}, nil
	}

	// Default MergePRWithResult: return success
	m.MergePRWithResultFunc = func(_ context.Context, _ string, _ github.PRMergeOptions) (*github.MergeResult, error) {
		return &github.MergeResult{
			Merged: true,
			SHA:    "abc123def456",
		}, nil
	}

	// Default CleanupMergedBranches: return empty list (no branches deleted)
	m.CleanupMergedBranchesFunc = func(_ context.Context, _ string, _ []string) ([]string, error) {
		return []string{}, nil
	}

	// Default GetWorkflowRunsForRef: return successful workflow runs
	m.GetWorkflowRunsForRefFunc = func(_ context.Context, _ string) ([]github.WorkflowRun, error) {
		return []github.WorkflowRun{
			{
				ID:         123,
				Name:       "CI",
				Status:     "completed",
				Conclusion: "success",
			},
		}, nil
	}

	// Default GetWorkflowRunsForPR: return successful workflow runs
	m.GetWorkflowRunsForPRFunc = func(_ context.Context, _ int) ([]github.WorkflowRun, error) {
		return []github.WorkflowRun{
			{
				ID:         123,
				Name:       "CI",
				Status:     "completed",
				Conclusion: "success",
			},
		}, nil
	}

	// Default GetCheckRunsForRef: return successful check runs
	m.GetCheckRunsForRefFunc = func(_ context.Context, _ string) ([]github.CheckRun, error) {
		return []github.CheckRun{
			{
				ID:         456,
				Name:       "build",
				Status:     "completed",
				Conclusion: "success",
			},
		}, nil
	}

	// Default GetWorkflowStatus: return success status
	m.GetWorkflowStatusFunc = func(_ context.Context, _ string) (*github.WorkflowStatus, error) {
		return &github.WorkflowStatus{
			State:      github.WorkflowStateSuccess,
			TotalRuns:  1,
			Successful: 1,
			Failed:     0,
			Pending:    0,
			FailedRuns: []string{},
		}, nil
	}

	// Default GetPRWorkflowStatus: return success status
	m.GetPRWorkflowStatusFunc = func(_ context.Context, _ int) (*github.WorkflowStatus, error) {
		return &github.WorkflowStatus{
			State:      github.WorkflowStateSuccess,
			TotalRuns:  1,
			Successful: 1,
			Failed:     0,
			Pending:    0,
			FailedRuns: []string{},
		}, nil
	}

	// Default IsWorkflowPassing: return true
	m.IsWorkflowPassingFunc = func(_ context.Context, _ string) (bool, error) {
		return true, nil
	}

	// Default IsPRWorkflowPassing: return true
	m.IsPRWorkflowPassingFunc = func(_ context.Context, _ int) (bool, error) {
		return true, nil
	}

	return m
}

// ListPRsForBranch implements github.GitHubClient.
func (m *MockGitHubClient) ListPRsForBranch(ctx context.Context, branch string) ([]github.PullRequest, error) {
	m.ListPRsForBranchCalls = append(m.ListPRsForBranchCalls, ListPRsForBranchCall{Branch: branch})
	return m.ListPRsForBranchFunc(ctx, branch)
}

// CreatePR implements github.GitHubClient.
func (m *MockGitHubClient) CreatePR(ctx context.Context, opts github.PRCreateOptions) (*github.PullRequest, error) {
	m.CreatePRCalls = append(m.CreatePRCalls, opts)
	return m.CreatePRFunc(ctx, opts)
}

// MergePRWithResult implements github.GitHubClient.
func (m *MockGitHubClient) MergePRWithResult(ctx context.Context, ref string, opts github.PRMergeOptions) (*github.MergeResult, error) {
	m.MergePRWithResultCalls = append(m.MergePRWithResultCalls, MergePRWithResultCall{Ref: ref, Opts: opts})
	return m.MergePRWithResultFunc(ctx, ref, opts)
}

// CleanupMergedBranches implements github.GitHubClient.
func (m *MockGitHubClient) CleanupMergedBranches(ctx context.Context, target string, protectedPatterns []string) ([]string, error) {
	m.CleanupMergedBranchesCalls = append(m.CleanupMergedBranchesCalls, CleanupMergedBranchesCall{
		Target:            target,
		ProtectedPatterns: protectedPatterns,
	})
	return m.CleanupMergedBranchesFunc(ctx, target, protectedPatterns)
}

// WithTimeout implements github.GitHubClient.
// Note: Returns *github.Client which requires type assertion in tests using the interface.
// For mock scenarios, we return nil since mocks don't need timeout configuration.
func (m *MockGitHubClient) WithTimeout(_ time.Duration) *github.Client {
	// MockGitHubClient doesn't use timeout, return nil
	// Callers using the mock should be aware of this
	return nil
}

// RepoPath implements github.GitHubClient.
func (m *MockGitHubClient) RepoPath() string {
	return m.repoPath
}

// GetWorkflowRunsForRef implements github.GitHubClient.
func (m *MockGitHubClient) GetWorkflowRunsForRef(ctx context.Context, ref string) ([]github.WorkflowRun, error) {
	m.GetWorkflowRunsForRefCalls = append(m.GetWorkflowRunsForRefCalls, ref)
	return m.GetWorkflowRunsForRefFunc(ctx, ref)
}

// GetWorkflowRunsForPR implements github.GitHubClient.
func (m *MockGitHubClient) GetWorkflowRunsForPR(ctx context.Context, prNumber int) ([]github.WorkflowRun, error) {
	m.GetWorkflowRunsForPRCalls = append(m.GetWorkflowRunsForPRCalls, prNumber)
	return m.GetWorkflowRunsForPRFunc(ctx, prNumber)
}

// GetWorkflowStatus implements github.GitHubClient.
func (m *MockGitHubClient) GetWorkflowStatus(ctx context.Context, commitSHA string) (*github.WorkflowStatus, error) {
	m.GetWorkflowStatusCalls = append(m.GetWorkflowStatusCalls, commitSHA)
	return m.GetWorkflowStatusFunc(ctx, commitSHA)
}

// GetCheckRunsForRef implements github.GitHubClient.
func (m *MockGitHubClient) GetCheckRunsForRef(ctx context.Context, ref string) ([]github.CheckRun, error) {
	m.GetCheckRunsForRefCalls = append(m.GetCheckRunsForRefCalls, ref)
	return m.GetCheckRunsForRefFunc(ctx, ref)
}

// GetPRWorkflowStatus implements github.GitHubClient.
func (m *MockGitHubClient) GetPRWorkflowStatus(ctx context.Context, prNumber int) (*github.WorkflowStatus, error) {
	m.GetPRWorkflowStatusCalls = append(m.GetPRWorkflowStatusCalls, prNumber)
	return m.GetPRWorkflowStatusFunc(ctx, prNumber)
}

// IsWorkflowPassing implements github.GitHubClient.
func (m *MockGitHubClient) IsWorkflowPassing(ctx context.Context, commitSHA string) (bool, error) {
	m.IsWorkflowPassingCalls = append(m.IsWorkflowPassingCalls, commitSHA)
	return m.IsWorkflowPassingFunc(ctx, commitSHA)
}

// IsPRWorkflowPassing implements github.GitHubClient.
func (m *MockGitHubClient) IsPRWorkflowPassing(ctx context.Context, prNumber int) (bool, error) {
	m.IsPRWorkflowPassingCalls = append(m.IsPRWorkflowPassingCalls, prNumber)
	return m.IsPRWorkflowPassingFunc(ctx, prNumber)
}

// --- Helper methods for test configuration ---

// OnListPRsForBranch sets a custom handler for ListPRsForBranch calls.
func (m *MockGitHubClient) OnListPRsForBranch(fn func(ctx context.Context, branch string) ([]github.PullRequest, error)) {
	m.ListPRsForBranchFunc = fn
}

// OnCreatePR sets a custom handler for CreatePR calls.
func (m *MockGitHubClient) OnCreatePR(fn func(ctx context.Context, opts github.PRCreateOptions) (*github.PullRequest, error)) {
	m.CreatePRFunc = fn
}

// OnMergePRWithResult sets a custom handler for MergePRWithResult calls.
func (m *MockGitHubClient) OnMergePRWithResult(fn func(ctx context.Context, ref string, opts github.PRMergeOptions) (*github.MergeResult, error)) {
	m.MergePRWithResultFunc = fn
}

// OnCleanupMergedBranches sets a custom handler for CleanupMergedBranches calls.
func (m *MockGitHubClient) OnCleanupMergedBranches(fn func(ctx context.Context, target string, protectedPatterns []string) ([]string, error)) {
	m.CleanupMergedBranchesFunc = fn
}

// --- Error simulation helpers ---

// FailListPRsForBranchWith configures ListPRsForBranch to return the specified error.
func (m *MockGitHubClient) FailListPRsForBranchWith(err error) {
	m.ListPRsForBranchFunc = func(_ context.Context, _ string) ([]github.PullRequest, error) {
		return nil, err
	}
}

// FailCreatePRWith configures CreatePR to return the specified error.
func (m *MockGitHubClient) FailCreatePRWith(err error) {
	m.CreatePRFunc = func(_ context.Context, _ github.PRCreateOptions) (*github.PullRequest, error) {
		return nil, err
	}
}

// FailMergePRWithResultWith configures MergePRWithResult to return the specified error.
func (m *MockGitHubClient) FailMergePRWithResultWith(err error) {
	m.MergePRWithResultFunc = func(_ context.Context, _ string, _ github.PRMergeOptions) (*github.MergeResult, error) {
		return nil, err
	}
}

// --- Scenario helpers ---

// ReturnExistingPR configures ListPRsForBranch to return an existing PR.
func (m *MockGitHubClient) ReturnExistingPR(prNumber int, branch string) {
	m.ListPRsForBranchFunc = func(_ context.Context, _ string) ([]github.PullRequest, error) {
		return []github.PullRequest{
			{
				Number:      prNumber,
				URL:         "https://github.com/mock-owner/mock-repo/pull/" + string(rune('0'+prNumber)),
				Title:       "Existing PR",
				State:       "OPEN",
				HeadRefName: branch,
				BaseRefName: "main",
				Mergeable:   "MERGEABLE",
			},
		}, nil
	}
}

// ReturnMergeConflict configures MergePRWithResult to return a conflict result.
func (m *MockGitHubClient) ReturnMergeConflict(conflictInfo string) {
	m.MergePRWithResultFunc = func(_ context.Context, _ string, _ github.PRMergeOptions) (*github.MergeResult, error) {
		return &github.MergeResult{
			Merged:       false,
			HasConflicts: true,
			ConflictInfo: conflictInfo,
		}, nil
	}
}

// ReturnMergeSuccess configures MergePRWithResult to return a successful merge.
func (m *MockGitHubClient) ReturnMergeSuccess(commitSHA string) {
	m.MergePRWithResultFunc = func(_ context.Context, _ string, _ github.PRMergeOptions) (*github.MergeResult, error) {
		return &github.MergeResult{
			Merged: true,
			SHA:    commitSHA,
		}, nil
	}
}

// --- Verification helpers ---

// Reset clears all recorded calls.
func (m *MockGitHubClient) Reset() {
	m.ListPRsForBranchCalls = nil
	m.CreatePRCalls = nil
	m.MergePRWithResultCalls = nil
	m.CleanupMergedBranchesCalls = nil
}

// GetMergeCallCount returns the number of times MergePRWithResult was called.
func (m *MockGitHubClient) GetMergeCallCount() int {
	return len(m.MergePRWithResultCalls)
}

// GetCreatePRCallCount returns the number of times CreatePR was called.
func (m *MockGitHubClient) GetCreatePRCallCount() int {
	return len(m.CreatePRCalls)
}

// WasMergeCalled returns true if MergePRWithResult was called at least once.
func (m *MockGitHubClient) WasMergeCalled() bool {
	return len(m.MergePRWithResultCalls) > 0
}

// WasCreatePRCalled returns true if CreatePR was called at least once.
func (m *MockGitHubClient) WasCreatePRCalled() bool {
	return len(m.CreatePRCalls) > 0
}

// LastMergeCall returns the most recent MergePRWithResult call, or empty if none.
func (m *MockGitHubClient) LastMergeCall() *MergePRWithResultCall {
	if len(m.MergePRWithResultCalls) == 0 {
		return nil
	}
	return &m.MergePRWithResultCalls[len(m.MergePRWithResultCalls)-1]
}

// --- Workflow-related helpers ---

// OnGetWorkflowRunsForRef sets a custom handler for GetWorkflowRunsForRef calls.
func (m *MockGitHubClient) OnGetWorkflowRunsForRef(fn func(ctx context.Context, ref string) ([]github.WorkflowRun, error)) {
	m.GetWorkflowRunsForRefFunc = fn
}

// OnGetWorkflowRunsForPR sets a custom handler for GetWorkflowRunsForPR calls.
func (m *MockGitHubClient) OnGetWorkflowRunsForPR(fn func(ctx context.Context, prNumber int) ([]github.WorkflowRun, error)) {
	m.GetWorkflowRunsForPRFunc = fn
}

// OnGetWorkflowStatus sets a custom handler for GetWorkflowStatus calls.
func (m *MockGitHubClient) OnGetWorkflowStatus(fn func(ctx context.Context, commitSHA string) (*github.WorkflowStatus, error)) {
	m.GetWorkflowStatusFunc = fn
}

// OnGetPRWorkflowStatus sets a custom handler for GetPRWorkflowStatus calls.
func (m *MockGitHubClient) OnGetPRWorkflowStatus(fn func(ctx context.Context, prNumber int) (*github.WorkflowStatus, error)) {
	m.GetPRWorkflowStatusFunc = fn
}

// OnGetCheckRunsForRef sets a custom handler for GetCheckRunsForRef calls.
func (m *MockGitHubClient) OnGetCheckRunsForRef(fn func(ctx context.Context, ref string) ([]github.CheckRun, error)) {
	m.GetCheckRunsForRefFunc = fn
}

// OnIsWorkflowPassing sets a custom handler for IsWorkflowPassing calls.
func (m *MockGitHubClient) OnIsWorkflowPassing(fn func(ctx context.Context, commitSHA string) (bool, error)) {
	m.IsWorkflowPassingFunc = fn
}

// OnIsPRWorkflowPassing sets a custom handler for IsPRWorkflowPassing calls.
func (m *MockGitHubClient) OnIsPRWorkflowPassing(fn func(ctx context.Context, prNumber int) (bool, error)) {
	m.IsPRWorkflowPassingFunc = fn
}

// ReturnWorkflowsPassing configures mock to return passing workflows.
func (m *MockGitHubClient) ReturnWorkflowsPassing() {
	m.GetWorkflowStatusFunc = func(_ context.Context, _ string) (*github.WorkflowStatus, error) {
		return &github.WorkflowStatus{
			State:      github.WorkflowStateSuccess,
			TotalRuns:  1,
			Successful: 1,
			Failed:     0,
			Pending:    0,
			FailedRuns: []string{},
		}, nil
	}
	m.IsWorkflowPassingFunc = func(_ context.Context, _ string) (bool, error) {
		return true, nil
	}
	m.IsPRWorkflowPassingFunc = func(_ context.Context, _ int) (bool, error) {
		return true, nil
	}
}

// ReturnWorkflowsFailing configures mock to return failing workflows.
func (m *MockGitHubClient) ReturnWorkflowsFailing(failedNames ...string) {
	m.GetWorkflowStatusFunc = func(_ context.Context, _ string) (*github.WorkflowStatus, error) {
		return &github.WorkflowStatus{
			State:      github.WorkflowStateFailure,
			TotalRuns:  len(failedNames),
			Successful: 0,
			Failed:     len(failedNames),
			Pending:    0,
			FailedRuns: failedNames,
		}, nil
	}
	m.IsWorkflowPassingFunc = func(_ context.Context, _ string) (bool, error) {
		return false, nil
	}
	m.IsPRWorkflowPassingFunc = func(_ context.Context, _ int) (bool, error) {
		return false, nil
	}
}

// ReturnWorkflowsPending configures mock to return pending workflows.
func (m *MockGitHubClient) ReturnWorkflowsPending() {
	m.GetWorkflowStatusFunc = func(_ context.Context, _ string) (*github.WorkflowStatus, error) {
		return &github.WorkflowStatus{
			State:      github.WorkflowStatePending,
			TotalRuns:  1,
			Successful: 0,
			Failed:     0,
			Pending:    1,
			FailedRuns: []string{},
		}, nil
	}
	m.IsWorkflowPassingFunc = func(_ context.Context, _ string) (bool, error) {
		return false, nil
	}
	m.IsPRWorkflowPassingFunc = func(_ context.Context, _ int) (bool, error) {
		return false, nil
	}
}

// FailGetWorkflowStatusWith configures GetWorkflowStatus to return the specified error.
func (m *MockGitHubClient) FailGetWorkflowStatusWith(err error) {
	m.GetWorkflowStatusFunc = func(_ context.Context, _ string) (*github.WorkflowStatus, error) {
		return nil, err
	}
}
