// Package forge provides abstractions for git hosting providers (GitHub, Gitea).
// This package defines the common interface that all forge implementations must satisfy.
package forge

import (
	"context"
	"time"
)

// Provider represents a git hosting provider type.
type Provider string

// Provider constants.
const (
	ProviderGitHub Provider = "github"
	ProviderGitea  Provider = "gitea"
)

// PullRequest represents a pull request from any forge provider.
// Field names are normalized across providers.
//
//nolint:govet // Logical field grouping preferred over memory optimization
type PullRequest struct {
	// Number is the PR number/index.
	Number int `json:"number"`

	// URL is the web URL for the PR.
	URL string `json:"url"`

	// Title is the PR title.
	Title string `json:"title"`

	// Body is the PR description.
	Body string `json:"body"`

	// State is the PR state (open, closed, merged).
	State string `json:"state"`

	// HeadBranch is the source branch name.
	HeadBranch string `json:"head_branch"`

	// HeadSHA is the source branch commit SHA.
	HeadSHA string `json:"head_sha"`

	// BaseBranch is the target branch name.
	BaseBranch string `json:"base_branch"`

	// BaseSHA is the target branch commit SHA.
	BaseSHA string `json:"base_sha"`

	// MergedAt is when the PR was merged (if merged).
	MergedAt *time.Time `json:"merged_at,omitempty"`

	// Merged indicates if the PR has been merged.
	Merged bool `json:"merged"`

	// Mergeable indicates if the PR can be merged.
	Mergeable bool `json:"mergeable"`

	// HasConflicts indicates if there are merge conflicts.
	HasConflicts bool `json:"has_conflicts"`
}

// IsMerged returns true if the PR has been merged.
func (pr *PullRequest) IsMerged() bool {
	return pr.Merged || pr.MergedAt != nil
}

// PRCreateOptions contains options for creating a pull request.
type PRCreateOptions struct {
	// Title is required.
	Title string

	// Body is the PR description.
	Body string

	// Head is the source branch (required).
	Head string

	// Base is the target branch (defaults to "main").
	Base string

	// Draft creates the PR as a draft.
	Draft bool
}

// PRMergeOptions contains options for merging a pull request.
//
//nolint:govet // Logical field grouping preferred over memory optimization
type PRMergeOptions struct {
	// Method is the merge method: "merge", "squash", or "rebase".
	// Default is "squash".
	Method string

	// CommitTitle is the merge commit title (optional).
	CommitTitle string

	// CommitMessage is the merge commit message (optional).
	CommitMessage string

	// DeleteBranch deletes the source branch after merge.
	DeleteBranch bool
}

// MergeResult contains the result of a merge operation.
//
//nolint:govet // Logical field grouping preferred over memory optimization
type MergeResult struct {
	// SHA is the merge commit SHA.
	SHA string

	// ConflictInfo provides details about conflicts.
	ConflictInfo string

	// Message provides additional information about the result.
	Message string

	// Merged indicates if the merge was successful.
	Merged bool

	// HasConflicts indicates merge conflicts prevented the merge.
	HasConflicts bool
}

// Client defines the interface for forge operations.
// Both GitHub and Gitea clients implement this interface.
type Client interface {
	// Provider returns the forge provider type.
	Provider() Provider

	// RepoPath returns the owner/repo path.
	RepoPath() string

	// PR operations

	// ListPRsForBranch lists pull requests for a specific head branch.
	ListPRsForBranch(ctx context.Context, branch string) ([]PullRequest, error)

	// GetPR retrieves a pull request by number or branch name.
	GetPR(ctx context.Context, ref string) (*PullRequest, error)

	// CreatePR creates a new pull request.
	CreatePR(ctx context.Context, opts PRCreateOptions) (*PullRequest, error)

	// GetOrCreatePR returns an existing PR for the branch or creates a new one.
	GetOrCreatePR(ctx context.Context, opts PRCreateOptions) (*PullRequest, error)

	// MergePR merges a pull request.
	MergePR(ctx context.Context, ref string, opts PRMergeOptions) error

	// MergePRWithResult merges a PR and returns detailed result.
	MergePRWithResult(ctx context.Context, ref string, opts PRMergeOptions) (*MergeResult, error)

	// ClosePR closes a pull request without merging.
	ClosePR(ctx context.Context, ref string) error

	// Branch operations

	// CleanupMergedBranches deletes branches that have been merged.
	CleanupMergedBranches(ctx context.Context, target string, protectedPatterns []string) ([]string, error)
}
