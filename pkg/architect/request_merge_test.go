package architect

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/github"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
)

// mockGitHubMergeClient is a test-local mock for GitHubMergeClient.
// This avoids import cycles with the shared mocks package.
type mockGitHubMergeClient struct {
	listPRsFunc  func(ctx context.Context, branch string) ([]github.PullRequest, error)
	createPRFunc func(ctx context.Context, opts github.PRCreateOptions) (*github.PullRequest, error)
	mergeFunc    func(ctx context.Context, ref string, opts github.PRMergeOptions) (*github.MergeResult, error)

	listPRsCalls  []string
	createPRCalls []github.PRCreateOptions
	mergeCalls    []mergePRCall
}

type mergePRCall struct {
	Ref  string
	Opts github.PRMergeOptions
}

func newMockGitHubClient() *mockGitHubMergeClient {
	m := &mockGitHubMergeClient{}

	// Default: no PRs found
	m.listPRsFunc = func(_ context.Context, _ string) ([]github.PullRequest, error) {
		return []github.PullRequest{}, nil
	}

	// Default: create PR succeeds
	m.createPRFunc = func(_ context.Context, opts github.PRCreateOptions) (*github.PullRequest, error) {
		return &github.PullRequest{
			Number:      1,
			Title:       opts.Title,
			State:       "OPEN",
			HeadRefName: opts.Head,
			BaseRefName: opts.Base,
			URL:         "https://github.com/test/repo/pull/1",
		}, nil
	}

	// Default: merge succeeds
	m.mergeFunc = func(_ context.Context, _ string, _ github.PRMergeOptions) (*github.MergeResult, error) {
		return &github.MergeResult{
			Merged: true,
			SHA:    "abc123def456",
		}, nil
	}

	return m
}

func (m *mockGitHubMergeClient) ListPRsForBranch(ctx context.Context, branch string) ([]github.PullRequest, error) {
	m.listPRsCalls = append(m.listPRsCalls, branch)
	return m.listPRsFunc(ctx, branch)
}

func (m *mockGitHubMergeClient) CreatePR(ctx context.Context, opts github.PRCreateOptions) (*github.PullRequest, error) {
	m.createPRCalls = append(m.createPRCalls, opts)
	return m.createPRFunc(ctx, opts)
}

func (m *mockGitHubMergeClient) MergePRWithResult(ctx context.Context, ref string, opts github.PRMergeOptions) (*github.MergeResult, error) {
	m.mergeCalls = append(m.mergeCalls, mergePRCall{Ref: ref, Opts: opts})
	return m.mergeFunc(ctx, ref, opts)
}

func (m *mockGitHubMergeClient) returnExistingPR(prNumber int, branch string) {
	m.listPRsFunc = func(_ context.Context, _ string) ([]github.PullRequest, error) {
		return []github.PullRequest{
			{
				Number:      prNumber,
				Title:       "Existing PR",
				State:       "OPEN",
				HeadRefName: branch,
				BaseRefName: "main",
				Mergeable:   "MERGEABLE",
			},
		}, nil
	}
}

func (m *mockGitHubMergeClient) failMergeWith(err error) {
	m.mergeFunc = func(_ context.Context, _ string, _ github.PRMergeOptions) (*github.MergeResult, error) {
		return nil, err
	}
}

func (m *mockGitHubMergeClient) returnMergeConflict(conflictInfo string) {
	m.mergeFunc = func(_ context.Context, _ string, _ github.PRMergeOptions) (*github.MergeResult, error) {
		return &github.MergeResult{
			Merged:       false,
			HasConflicts: true,
			ConflictInfo: conflictInfo,
		}, nil
	}
}

func (m *mockGitHubMergeClient) failCreatePRWith(err error) {
	m.createPRFunc = func(_ context.Context, _ github.PRCreateOptions) (*github.PullRequest, error) {
		return nil, err
	}
}

// TestHandleMergeRequest_MissingTypedPayload verifies error when typed payload is missing.
func TestHandleMergeRequest_MissingTypedPayload(t *testing.T) {
	baseSM := agent.NewBaseStateMachine("test-architect", StateRequest, nil, nil)

	driver := &Driver{
		BaseStateMachine: baseSM,
		logger:           logx.NewLogger("test-merge"),
	}

	// Create request without typed payload
	request := proto.NewAgentMsg(proto.MsgTypeREQUEST, "coder-001", "architect")
	request.Metadata = map[string]string{proto.KeyStoryID: "story-123"}
	// Don't set typed payload

	_, err := driver.handleMergeRequest(context.Background(), request)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "typed payload")
}

// TestHandleMergeRequest_NoPROrBranch verifies error when neither PR URL nor branch is provided.
func TestHandleMergeRequest_NoPROrBranch(t *testing.T) {
	baseSM := agent.NewBaseStateMachine("test-architect", StateRequest, nil, nil)
	mockGH := newMockGitHubClient()

	driver := &Driver{
		BaseStateMachine: baseSM,
		gitHubClient:     mockGH,
		logger:           logx.NewLogger("test-merge"),
	}

	// Create merge request with empty PR URL and branch
	request := proto.NewAgentMsg(proto.MsgTypeREQUEST, "coder-001", "architect")
	request.Metadata = map[string]string{proto.KeyStoryID: "story-123"}

	mergePayload := &proto.MergeRequestPayload{
		PRURL:      "",
		BranchName: "",
	}
	request.SetTypedPayload(proto.NewMergeRequestPayload(mergePayload))

	result, err := driver.handleMergeRequest(context.Background(), request)

	// Should return a response with error, not an error from the handler
	require.NoError(t, err)
	require.NotNil(t, result)

	// Extract response payload to check status
	responsePayload := result.GetTypedPayload()
	require.NotNil(t, responsePayload)
	mergeResp, extractErr := responsePayload.ExtractMergeResponse()
	require.NoError(t, extractErr)
	assert.Equal(t, string(proto.ApprovalStatusNeedsChanges), mergeResp.Status)
}

// TestHandleMergeRequest_MergeSuccess verifies successful merge with PR URL.
func TestHandleMergeRequest_MergeSuccess(t *testing.T) {
	baseSM := agent.NewBaseStateMachine("test-architect", StateRequest, nil, nil)
	mockGH := newMockGitHubClient()

	driver := &Driver{
		BaseStateMachine: baseSM,
		gitHubClient:     mockGH,
		logger:           logx.NewLogger("test-merge"),
		workDir:          t.TempDir(),
	}

	// Create merge request with PR URL
	request := proto.NewAgentMsg(proto.MsgTypeREQUEST, "coder-001", "architect")
	request.Metadata = map[string]string{proto.KeyStoryID: "story-123"}

	mergePayload := &proto.MergeRequestPayload{
		PRURL:      "https://github.com/test/repo/pull/42",
		BranchName: "",
	}
	request.SetTypedPayload(proto.NewMergeRequestPayload(mergePayload))

	result, err := driver.handleMergeRequest(context.Background(), request)

	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify merge was called with PR URL
	require.Len(t, mockGH.mergeCalls, 1)
	assert.Equal(t, "https://github.com/test/repo/pull/42", mockGH.mergeCalls[0].Ref)

	// Extract response payload to check status
	responsePayload := result.GetTypedPayload()
	require.NotNil(t, responsePayload)
	mergeResp, extractErr := responsePayload.ExtractMergeResponse()
	require.NoError(t, extractErr)
	assert.Equal(t, string(proto.ApprovalStatusApproved), mergeResp.Status)
	assert.Equal(t, "abc123def456", mergeResp.MergeCommit)
}

// TestHandleMergeRequest_MergeWithBranch_ExistingPR verifies merge with existing PR for branch.
func TestHandleMergeRequest_MergeWithBranch_ExistingPR(t *testing.T) {
	baseSM := agent.NewBaseStateMachine("test-architect", StateRequest, nil, nil)
	mockGH := newMockGitHubClient()
	mockGH.returnExistingPR(42, "feature-branch")

	driver := &Driver{
		BaseStateMachine: baseSM,
		gitHubClient:     mockGH,
		logger:           logx.NewLogger("test-merge"),
		workDir:          t.TempDir(),
	}

	// Create merge request with branch name only
	request := proto.NewAgentMsg(proto.MsgTypeREQUEST, "coder-001", "architect")
	request.Metadata = map[string]string{proto.KeyStoryID: "story-123"}

	mergePayload := &proto.MergeRequestPayload{
		PRURL:      "",
		BranchName: "feature-branch",
	}
	request.SetTypedPayload(proto.NewMergeRequestPayload(mergePayload))

	result, err := driver.handleMergeRequest(context.Background(), request)

	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify list was called to find existing PR
	require.Len(t, mockGH.listPRsCalls, 1)
	assert.Equal(t, "feature-branch", mockGH.listPRsCalls[0])

	// Verify no PR was created (used existing)
	assert.Len(t, mockGH.createPRCalls, 0)

	// Verify merge was called with PR number
	require.Len(t, mockGH.mergeCalls, 1)
	assert.Equal(t, "42", mockGH.mergeCalls[0].Ref)

	// Extract response payload to check status
	responsePayload := result.GetTypedPayload()
	require.NotNil(t, responsePayload)
	mergeResp, extractErr := responsePayload.ExtractMergeResponse()
	require.NoError(t, extractErr)
	assert.Equal(t, string(proto.ApprovalStatusApproved), mergeResp.Status)
}

// TestHandleMergeRequest_MergeWithBranch_CreatesPR verifies PR creation when none exists.
func TestHandleMergeRequest_MergeWithBranch_CreatesPR(t *testing.T) {
	baseSM := agent.NewBaseStateMachine("test-architect", StateRequest, nil, nil)
	mockGH := newMockGitHubClient()
	// Default behavior: no existing PRs

	driver := &Driver{
		BaseStateMachine: baseSM,
		gitHubClient:     mockGH,
		logger:           logx.NewLogger("test-merge"),
		workDir:          t.TempDir(),
	}

	// Create merge request with branch name only
	request := proto.NewAgentMsg(proto.MsgTypeREQUEST, "coder-001", "architect")
	request.Metadata = map[string]string{proto.KeyStoryID: "story-123"}

	mergePayload := &proto.MergeRequestPayload{
		PRURL:      "",
		BranchName: "new-feature-branch",
	}
	request.SetTypedPayload(proto.NewMergeRequestPayload(mergePayload))

	result, err := driver.handleMergeRequest(context.Background(), request)

	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify list was called
	require.Len(t, mockGH.listPRsCalls, 1)

	// Verify PR was created
	require.Len(t, mockGH.createPRCalls, 1)
	assert.Equal(t, "new-feature-branch", mockGH.createPRCalls[0].Head)
	assert.Contains(t, mockGH.createPRCalls[0].Title, "story-123")

	// Verify merge was called with created PR number
	require.Len(t, mockGH.mergeCalls, 1)
	assert.Equal(t, "1", mockGH.mergeCalls[0].Ref) // Mock returns PR #1

	// Extract response payload to check status
	responsePayload := result.GetTypedPayload()
	require.NotNil(t, responsePayload)
	mergeResp, extractErr := responsePayload.ExtractMergeResponse()
	require.NoError(t, extractErr)
	assert.Equal(t, string(proto.ApprovalStatusApproved), mergeResp.Status)
}

// TestHandleMergeRequest_MergeConflict verifies handling of merge conflicts.
func TestHandleMergeRequest_MergeConflict(t *testing.T) {
	baseSM := agent.NewBaseStateMachine("test-architect", StateRequest, nil, nil)
	mockGH := newMockGitHubClient()
	mockGH.returnMergeConflict("CONFLICT in src/main.go: content differs\nCONFLICT in README.md: content differs")

	driver := &Driver{
		BaseStateMachine: baseSM,
		gitHubClient:     mockGH,
		logger:           logx.NewLogger("test-merge"),
	}

	// Create merge request
	request := proto.NewAgentMsg(proto.MsgTypeREQUEST, "coder-001", "architect")
	request.Metadata = map[string]string{proto.KeyStoryID: "story-123"}

	mergePayload := &proto.MergeRequestPayload{
		PRURL:      "https://github.com/test/repo/pull/42",
		BranchName: "",
	}
	request.SetTypedPayload(proto.NewMergeRequestPayload(mergePayload))

	result, err := driver.handleMergeRequest(context.Background(), request)

	require.NoError(t, err)
	require.NotNil(t, result)

	// Extract response payload
	responsePayload := result.GetTypedPayload()
	require.NotNil(t, responsePayload)
	mergeResp, extractErr := responsePayload.ExtractMergeResponse()
	require.NoError(t, extractErr)

	// Should return NEEDS_CHANGES status
	assert.Equal(t, string(proto.ApprovalStatusNeedsChanges), mergeResp.Status)
	assert.Contains(t, mergeResp.Feedback, "conflict")
	assert.Contains(t, mergeResp.ConflictDetails, "src/main.go")
}

// TestHandleMergeRequest_KnowledgeGraphConflict verifies special handling for knowledge.dot conflicts.
func TestHandleMergeRequest_KnowledgeGraphConflict(t *testing.T) {
	baseSM := agent.NewBaseStateMachine("test-architect", StateRequest, nil, nil)
	mockGH := newMockGitHubClient()
	mockGH.returnMergeConflict("CONFLICT in .maestro/knowledge.dot: content differs")

	driver := &Driver{
		BaseStateMachine: baseSM,
		gitHubClient:     mockGH,
		logger:           logx.NewLogger("test-merge"),
	}

	// Create merge request
	request := proto.NewAgentMsg(proto.MsgTypeREQUEST, "coder-001", "architect")
	request.Metadata = map[string]string{proto.KeyStoryID: "story-123"}

	mergePayload := &proto.MergeRequestPayload{
		PRURL:      "https://github.com/test/repo/pull/42",
		BranchName: "",
	}
	request.SetTypedPayload(proto.NewMergeRequestPayload(mergePayload))

	result, err := driver.handleMergeRequest(context.Background(), request)

	require.NoError(t, err)
	require.NotNil(t, result)

	// Extract response payload
	responsePayload := result.GetTypedPayload()
	require.NotNil(t, responsePayload)
	mergeResp, extractErr := responsePayload.ExtractMergeResponse()
	require.NoError(t, extractErr)

	// Should return NEEDS_CHANGES with special knowledge graph guidance
	assert.Equal(t, string(proto.ApprovalStatusNeedsChanges), mergeResp.Status)
	assert.Contains(t, mergeResp.Feedback, "KNOWLEDGE GRAPH CONFLICT")
	assert.Contains(t, mergeResp.Feedback, "Keep all unique nodes")
}

// TestHandleMergeRequest_MergeError verifies handling of merge errors.
func TestHandleMergeRequest_MergeError(t *testing.T) {
	baseSM := agent.NewBaseStateMachine("test-architect", StateRequest, nil, nil)
	mockGH := newMockGitHubClient()
	mockGH.failMergeWith(assert.AnError)

	driver := &Driver{
		BaseStateMachine: baseSM,
		gitHubClient:     mockGH,
		logger:           logx.NewLogger("test-merge"),
	}

	// Create merge request
	request := proto.NewAgentMsg(proto.MsgTypeREQUEST, "coder-001", "architect")
	request.Metadata = map[string]string{proto.KeyStoryID: "story-123"}

	mergePayload := &proto.MergeRequestPayload{
		PRURL:      "https://github.com/test/repo/pull/42",
		BranchName: "",
	}
	request.SetTypedPayload(proto.NewMergeRequestPayload(mergePayload))

	result, err := driver.handleMergeRequest(context.Background(), request)

	require.NoError(t, err)
	require.NotNil(t, result)

	// Extract response payload
	responsePayload := result.GetTypedPayload()
	require.NotNil(t, responsePayload)
	mergeResp, extractErr := responsePayload.ExtractMergeResponse()
	require.NoError(t, extractErr)

	// Should return error status
	assert.Equal(t, string(proto.ApprovalStatusNeedsChanges), mergeResp.Status)
}

// TestHandleMergeRequest_CreatePRError verifies handling of PR creation errors.
func TestHandleMergeRequest_CreatePRError(t *testing.T) {
	baseSM := agent.NewBaseStateMachine("test-architect", StateRequest, nil, nil)
	mockGH := newMockGitHubClient()
	mockGH.failCreatePRWith(assert.AnError)

	driver := &Driver{
		BaseStateMachine: baseSM,
		gitHubClient:     mockGH,
		logger:           logx.NewLogger("test-merge"),
	}

	// Create merge request with branch (no PR URL)
	request := proto.NewAgentMsg(proto.MsgTypeREQUEST, "coder-001", "architect")
	request.Metadata = map[string]string{proto.KeyStoryID: "story-123"}

	mergePayload := &proto.MergeRequestPayload{
		PRURL:      "",
		BranchName: "feature-branch",
	}
	request.SetTypedPayload(proto.NewMergeRequestPayload(mergePayload))

	result, err := driver.handleMergeRequest(context.Background(), request)

	require.NoError(t, err)
	require.NotNil(t, result)

	// Extract response payload
	responsePayload := result.GetTypedPayload()
	require.NotNil(t, responsePayload)
	mergeResp, extractErr := responsePayload.ExtractMergeResponse()
	require.NoError(t, extractErr)

	// Should return error status
	assert.Equal(t, string(proto.ApprovalStatusNeedsChanges), mergeResp.Status)
	assert.Contains(t, mergeResp.Feedback, "failed")
}

// TestCategorizeMergeError verifies error categorization logic.
func TestCategorizeMergeError(t *testing.T) {
	driver := &Driver{
		logger: logx.NewLogger("test-merge"),
	}

	tests := []struct {
		name           string
		errMsg         string
		expectedStatus proto.ApprovalStatus
		expectedInFB   string
	}{
		{
			name:           "conflict error",
			errMsg:         "merge conflict detected",
			expectedStatus: proto.ApprovalStatusNeedsChanges,
			expectedInFB:   "conflict",
		},
		{
			name:           "PR not found",
			errMsg:         "no pull request found",
			expectedStatus: proto.ApprovalStatusNeedsChanges,
			expectedInFB:   "not found",
		},
		{
			name:           "permission denied",
			errMsg:         "permission denied for merge",
			expectedStatus: proto.ApprovalStatusNeedsChanges,
			expectedInFB:   "Permission denied",
		},
		{
			name:           "branch not found",
			errMsg:         "branch feature-x not found",
			expectedStatus: proto.ApprovalStatusNeedsChanges,
			expectedInFB:   "Branch not found",
		},
		{
			name:           "network error",
			errMsg:         "network timeout",
			expectedStatus: proto.ApprovalStatusNeedsChanges,
			expectedInFB:   "Network error",
		},
		{
			name:           "gh not found",
			errMsg:         "gh command not found",
			expectedStatus: proto.ApprovalStatusRejected,
			expectedInFB:   "GitHub CLI",
		},
		{
			name:           "not a git repo",
			errMsg:         "not a git repository",
			expectedStatus: proto.ApprovalStatusRejected,
			expectedInFB:   "repository",
		},
		{
			name:           "unknown error",
			errMsg:         "some unexpected error occurred",
			expectedStatus: proto.ApprovalStatusNeedsChanges,
			expectedInFB:   "unexpected error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, feedback := driver.categorizeMergeError(newError(tc.errMsg))
			assert.Equal(t, tc.expectedStatus, status)
			assert.Contains(t, feedback, tc.expectedInFB)
		})
	}
}

// newError creates a simple error with the given message for testing.
func newError(msg string) error {
	return &simpleError{msg: msg}
}

type simpleError struct {
	msg string
}

func (e *simpleError) Error() string {
	return e.msg
}

// TestExtractPRIDFromURL verifies PR ID extraction from various URL formats.
func TestExtractPRIDFromURL(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{
			name:     "standard PR URL",
			url:      "https://github.com/owner/repo/pull/123",
			expected: "123",
		},
		{
			name:     "API URL",
			url:      "https://api.github.com/repos/owner/repo/pulls/456",
			expected: "456",
		},
		{
			name:     "URL with trailing slash",
			url:      "https://github.com/owner/repo/pull/789/",
			expected: "", // trailing slash causes parse failure
		},
		{
			name:     "empty URL",
			url:      "",
			expected: "",
		},
		{
			name:     "invalid URL",
			url:      "not-a-url",
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := extractPRIDFromURL(tc.url)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// TestGenerateConflictGuidance verifies conflict guidance generation.
func TestGenerateConflictGuidance(t *testing.T) {
	driver := &Driver{
		logger: logx.NewLogger("test-merge"),
	}

	t.Run("standard conflict", func(t *testing.T) {
		guidance := driver.generateConflictGuidance("CONFLICT in src/main.go")
		assert.Contains(t, guidance, "Merge conflicts detected")
		assert.Contains(t, guidance, "src/main.go")
		assert.NotContains(t, guidance, "KNOWLEDGE GRAPH")
	})

	t.Run("knowledge graph conflict", func(t *testing.T) {
		guidance := driver.generateConflictGuidance("CONFLICT in .maestro/knowledge.dot")
		assert.Contains(t, guidance, "KNOWLEDGE GRAPH CONFLICT")
		assert.Contains(t, guidance, "Keep all unique nodes")
		assert.Contains(t, guidance, "DOT syntax")
	})
}
