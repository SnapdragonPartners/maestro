package github

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// BranchInfo represents a GitHub branch.
//
//nolint:govet // Logical grouping preferred over memory optimization
type BranchInfo struct {
	Name      string `json:"name"`
	Protected bool   `json:"protected"`
	Commit    struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

// ListBranches lists all branches in the repository.
func (c *Client) ListBranches(ctx context.Context) ([]BranchInfo, error) {
	endpoint := fmt.Sprintf("/repos/%s/branches", c.RepoPath())

	// Use pagination to get all branches
	args := []string{"api", endpoint, "--paginate"}
	output, err := c.run(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list branches: %w", err)
	}

	var branches []BranchInfo
	if err := json.Unmarshal(output, &branches); err != nil {
		return nil, fmt.Errorf("failed to parse branches: %w", err)
	}

	return branches, nil
}

// GetBranch retrieves information about a specific branch.
func (c *Client) GetBranch(ctx context.Context, branch string) (*BranchInfo, error) {
	endpoint := fmt.Sprintf("/repos/%s/branches/%s", c.RepoPath(), branch)
	output, err := c.APIGet(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to get branch %s: %w", branch, err)
	}

	var info BranchInfo
	if err := json.Unmarshal(output, &info); err != nil {
		return nil, fmt.Errorf("failed to parse branch: %w", err)
	}

	return &info, nil
}

// BranchExists checks if a branch exists.
func (c *Client) BranchExists(ctx context.Context, branch string) (bool, error) {
	_, err := c.GetBranch(ctx, branch)
	if err != nil {
		// Check if it's a "not found" error
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "Not Found") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// DeleteBranch deletes a remote branch.
func (c *Client) DeleteBranch(ctx context.Context, branch string) error {
	endpoint := fmt.Sprintf("/repos/%s/git/refs/heads/%s", c.RepoPath(), branch)
	_, err := c.APIDelete(ctx, endpoint)
	if err != nil {
		return fmt.Errorf("failed to delete branch %s: %w", branch, err)
	}
	c.logger.Info("Deleted branch %s from %s", branch, c.RepoPath())
	return nil
}

// IsBranchMerged checks if a branch has been merged to the target branch.
func (c *Client) IsBranchMerged(ctx context.Context, branch, target string) (bool, error) {
	if target == "" {
		target = DefaultBranch
	}

	// Compare the branches - if the branch is behind or equal, it's merged
	endpoint := fmt.Sprintf("/repos/%s/compare/%s...%s", c.RepoPath(), target, branch)
	output, err := c.APIGet(ctx, endpoint)
	if err != nil {
		return false, fmt.Errorf("failed to compare branches: %w", err)
	}

	var comparison struct {
		Status       string `json:"status"`
		AheadBy      int    `json:"ahead_by"`
		BehindBy     int    `json:"behind_by"`
		TotalCommits int    `json:"total_commits"`
	}

	if err := json.Unmarshal(output, &comparison); err != nil {
		return false, fmt.Errorf("failed to parse comparison: %w", err)
	}

	// Branch is merged if it has no commits ahead of target
	// (identical or behind means all changes are in target)
	return comparison.AheadBy == 0, nil
}

// CleanupMergedBranches deletes branches that have been merged to the target.
// It skips branches matching any of the protected patterns.
func (c *Client) CleanupMergedBranches(ctx context.Context, target string, protectedPatterns []string) ([]string, error) {
	if target == "" {
		target = DefaultBranch
	}

	branches, err := c.ListBranches(ctx)
	if err != nil {
		return nil, err
	}

	var deleted []string
	for i := range branches {
		branch := &branches[i]
		// Skip protected branches
		if c.isProtected(branch.Name, protectedPatterns) {
			c.logger.Debug("Skipping protected branch: %s", branch.Name)
			continue
		}

		// Skip if branch is marked as protected in GitHub
		if branch.Protected {
			c.logger.Debug("Skipping GitHub-protected branch: %s", branch.Name)
			continue
		}

		// Check if merged
		merged, mergeErr := c.IsBranchMerged(ctx, branch.Name, target)
		if mergeErr != nil {
			c.logger.Warn("Failed to check if %s is merged: %v", branch.Name, mergeErr)
			continue
		}

		if merged {
			if delErr := c.DeleteBranch(ctx, branch.Name); delErr != nil {
				c.logger.Warn("Failed to delete merged branch %s: %v", branch.Name, delErr)
				continue
			}
			deleted = append(deleted, branch.Name)
		}
	}

	return deleted, nil
}

// isProtected checks if a branch name matches any protected pattern.
func (c *Client) isProtected(branch string, patterns []string) bool {
	for _, pattern := range patterns {
		// Use filepath.Match for glob-style matching
		matched, err := filepath.Match(pattern, branch)
		if err != nil {
			// If pattern is invalid, do exact match
			if branch == pattern {
				return true
			}
			continue
		}
		if matched {
			return true
		}

		// Also check for prefix match for patterns like "release/*"
		if strings.HasSuffix(pattern, "/*") {
			prefix := strings.TrimSuffix(pattern, "/*")
			if strings.HasPrefix(branch, prefix+"/") {
				return true
			}
		}
	}
	return false
}

// GetDefaultBranch returns the repository's default branch.
func (c *Client) GetDefaultBranch(ctx context.Context) (string, error) {
	repo, err := c.GetRepository(ctx)
	if err != nil {
		return "", err
	}
	return repo.DefaultBranch, nil
}

// ListStaleBranches lists branches that haven't been updated recently.
// daysOld specifies the minimum age in days for a branch to be considered stale.
// Note: daysOld filtering is not yet implemented - currently returns all non-protected branches.
func (c *Client) ListStaleBranches(ctx context.Context, _ /* daysOld */ int, protectedPatterns []string) ([]BranchInfo, error) {
	branches, err := c.ListBranches(ctx)
	if err != nil {
		return nil, err
	}

	// For now, we just return non-protected branches
	// Full implementation would check commit dates
	stale := make([]BranchInfo, 0, len(branches))
	for i := range branches {
		branch := &branches[i]
		if c.isProtected(branch.Name, protectedPatterns) {
			continue
		}
		if branch.Protected {
			continue
		}
		// TODO: Check commit date against daysOld threshold
		stale = append(stale, *branch)
	}

	return stale, nil
}
