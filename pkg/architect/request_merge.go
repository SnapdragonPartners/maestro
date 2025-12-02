package architect

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"orchestrator/pkg/agent/middleware/metrics"
	"orchestrator/pkg/config"
	"orchestrator/pkg/git"
	"orchestrator/pkg/github"
	"orchestrator/pkg/proto"
)

// handleMergeRequest processes merge requests for completed PRs.
func (d *Driver) handleMergeRequest(ctx context.Context, request *proto.AgentMsg) (*proto.AgentMsg, error) {
	// Extract merge request from typed payload
	typedPayload := request.GetTypedPayload()
	if typedPayload == nil {
		return nil, fmt.Errorf("merge request message missing typed payload")
	}

	mergePayload, err := typedPayload.ExtractGeneric()
	if err != nil {
		return nil, fmt.Errorf("failed to extract merge request: %w", err)
	}

	// Extract fields from payload
	prURLStr, _ := mergePayload["pr_url"].(string)
	branchNameStr, _ := mergePayload["branch_name"].(string)

	// Extract story_id from metadata
	storyIDStr := request.Metadata["story_id"]

	d.logger.Info("🔀 Processing merge request for story %s: PR=%s, branch=%s", storyIDStr, prURLStr, branchNameStr)

	// Attempt merge using GitHub CLI.
	mergeResult, err := d.attemptPRMerge(ctx, prURLStr, branchNameStr, storyIDStr)

	// Create RESPONSE using unified protocol.
	resultMsg := proto.NewAgentMsg(proto.MsgTypeRESPONSE, d.GetAgentID(), request.FromAgent)
	resultMsg.ParentMsgID = request.ID

	// Copy story_id from request metadata for dispatcher validation
	if storyID, exists := request.Metadata[proto.KeyStoryID]; exists {
		resultMsg.SetMetadata(proto.KeyStoryID, storyID)
	}

	// Build merge response payload (typed)
	mergeResponsePayload := &proto.MergeResponsePayload{
		Metadata: make(map[string]string),
	}

	if err != nil {
		// Categorize error for appropriate response
		status, feedback := d.categorizeMergeError(err)
		d.logger.Error("🔀 Merge failed for story %s: %s (status: %s)", storyIDStr, err.Error(), status)

		mergeResponsePayload.Status = string(status)
		mergeResponsePayload.Feedback = feedback
		if status == proto.ApprovalStatusNeedsChanges {
			mergeResponsePayload.ErrorDetails = err.Error() // Preserve detailed error for debugging
		}
	} else if mergeResult != nil && mergeResult.HasConflicts {
		// Merge conflicts are always recoverable
		// Check if knowledge.dot is among the conflicting files and provide specific guidance
		conflictFeedback := d.generateConflictGuidance(mergeResult.ConflictInfo)
		d.logger.Warn("🔀 Merge conflicts for story %s: %s", storyIDStr, mergeResult.ConflictInfo)

		mergeResponsePayload.Status = string(proto.ApprovalStatusNeedsChanges)
		mergeResponsePayload.Feedback = conflictFeedback
		mergeResponsePayload.ConflictDetails = mergeResult.ConflictInfo
	} else {
		// Success
		d.logger.Info("🔀 Merge successful for story %s: commit %s", storyIDStr, mergeResult.CommitSHA)

		mergeResponsePayload.Status = string(proto.ApprovalStatusApproved)
		mergeResponsePayload.Feedback = "Pull request merged successfully"
		mergeResponsePayload.MergeCommit = mergeResult.CommitSHA

		// Update all dependent clones (architect, PM) to reflect the merge
		cfg, cfgErr := config.GetConfig()
		if cfgErr == nil {
			registry := git.NewRegistry(d.workDir)
			if updateErr := registry.UpdateDependentClones(ctx, cfg.Git.RepoURL, cfg.Git.TargetBranch, mergeResult.CommitSHA); updateErr != nil {
				d.logger.Warn("⚠️  Failed to update dependent clones after merge: %v (merge succeeded, continuing)", updateErr)
				// Don't fail the merge - it already succeeded. Clone updates can be retried later.
			}
		} else {
			d.logger.Warn("⚠️  Failed to get config for clone updates: %v", cfgErr)
		}

		// Extract PR ID from URL for database storage
		var prIDPtr *string
		if prURLStr != "" {
			prID := extractPRIDFromURL(prURLStr)
			if prID != "" {
				prIDPtr = &prID
			}
		}

		// Prepare completion summary
		completionSummary := fmt.Sprintf("Story completed via merge. PR: %s, Commit: %s", prURLStr, mergeResult.CommitSHA)

		// Handle work acceptance (queue completion, database persistence, state transition signal)
		d.handleWorkAccepted(ctx, storyIDStr, "merge", prIDPtr, &mergeResult.CommitSHA, &completionSummary)
	}

	// Set typed merge response payload
	resultMsg.SetTypedPayload(proto.NewMergeResponsePayload(mergeResponsePayload))

	return resultMsg, nil
}

// queryStoryMetrics retrieves metrics for a story from the internal metrics recorder.
func (d *Driver) queryStoryMetrics(_ context.Context, storyID string) *metrics.StoryMetrics {
	cfg, err := config.GetConfig()
	if err != nil {
		d.logger.Warn("📊 Failed to get config for metrics query: %v", err)
		return nil
	}

	if cfg.Agents == nil || !cfg.Agents.Metrics.Enabled {
		d.logger.Warn("📊 Metrics not enabled - skipping metrics query")
		return nil
	}

	d.logger.Info("📊 Querying internal metrics for completed story %s", storyID)

	// Get the internal metrics recorder (singleton)
	recorder := metrics.NewInternalRecorder()
	storyMetrics := recorder.GetStoryMetrics(storyID)

	if storyMetrics != nil {
		d.logger.Info("📊 Story %s metrics: prompt tokens: %d, completion tokens: %d, total tokens: %d, total cost: $%.6f",
			storyID, storyMetrics.PromptTokens, storyMetrics.CompletionTokens, storyMetrics.TotalTokens, storyMetrics.TotalCost)
	} else {
		d.logger.Warn("📊 No metrics found for story %s", storyID)
	}

	return storyMetrics
}

// extractPRIDFromURL extracts the PR number from a GitHub PR URL.
func extractPRIDFromURL(prURL string) string {
	// Extract PR number from URLs like:
	// https://github.com/owner/repo/pull/123
	// https://api.github.com/repos/owner/repo/pulls/123
	parts := strings.Split(prURL, "/")
	if len(parts) > 0 {
		// Get the last part which should be the PR number
		lastPart := parts[len(parts)-1]
		// Validate it's numeric
		if _, err := strconv.Atoi(lastPart); err == nil {
			return lastPart
		}
	}
	return ""
}

// MergeAttemptResult represents the result of a merge attempt.
//
//nolint:govet // Simple result struct, logical grouping preferred
type MergeAttemptResult struct {
	HasConflicts bool
	ConflictInfo string
	CommitSHA    string
}

// generateConflictGuidance creates detailed guidance for resolving merge conflicts.
// Provides specific instructions for knowledge.dot conflicts.
func (d *Driver) generateConflictGuidance(conflictInfo string) string {
	hasKnowledgeConflict := strings.Contains(conflictInfo, ".maestro/knowledge.dot") || strings.Contains(conflictInfo, "knowledge.dot")

	if hasKnowledgeConflict {
		return `Merge conflicts detected, including in the knowledge graph.

**KNOWLEDGE GRAPH CONFLICT RESOLUTION**

The knowledge graph (.maestro/knowledge.dot) has conflicts. Please resolve carefully:

1. **Pull the latest main branch**:
   ` + "`" + `git pull origin main` + "`" + `

2. **Open .maestro/knowledge.dot and resolve conflicts**:
   - **Keep all unique nodes from both branches** (no data loss)
   - **For duplicate node IDs with different content**:
     * Prefer status='current' over 'deprecated' or 'legacy'
     * Merge complementary descriptions if both add value
     * Choose the more specific/detailed example
     * Use the higher priority value (critical > high > medium > low)
   - **Preserve all unique edges** (relationships)
   - **Remove conflict markers** (<<<<<<, =======, >>>>>>>)
   - **Ensure valid DOT syntax** after resolution

3. **Validate the merged file**:
   - Check that all nodes have required fields (type, level, status, description)
   - Verify all enum values are correct (see schema in DOC_GRAPH.md)
   - Ensure edge references point to existing nodes
   - Confirm DOT syntax is valid (no trailing commas, balanced braces)

4. **Commit and push**:
   ` + "`" + `git add .maestro/knowledge.dot` + "`" + `
   ` + "`" + `git commit -m "Resolved knowledge graph conflicts"` + "`" + `
   ` + "`" + `git push` + "`" + `

5. **Resubmit the PR** for review

The knowledge graph is critical for architectural consistency. Take time to merge thoughtfully.

**OTHER CONFLICTS**:
` + conflictInfo
	}

	// Standard conflict message for non-knowledge files
	return fmt.Sprintf("Merge conflicts detected. Resolve conflicts in the following files and resubmit:\n\n%s", conflictInfo)
}

// attemptPRMerge attempts to merge a PR using the centralized GitHub client.
func (d *Driver) attemptPRMerge(ctx context.Context, prURL, branchName, storyID string) (*MergeAttemptResult, error) {
	// Get GitHub client from config (pure API calls, no workdir needed)
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
	ghClient = ghClient.WithTimeout(2 * time.Minute)

	// Determine the PR reference (URL or branch)
	var prRef string
	if prURL != "" && prURL != " " {
		prRef = prURL
		d.logger.Info("🔀 Attempting to merge PR: %s", prURL)
	} else if branchName != "" {
		// Check if PR exists for branch, create if needed
		d.logger.Info("🔀 Looking for existing PR for branch: %s", branchName)
		prs, listErr := ghClient.ListPRsForBranch(ctx, branchName)
		if listErr != nil || len(prs) == 0 {
			// No PR found, create one
			d.logger.Info("🔀 No existing PR found, creating new PR for branch: %s", branchName)
			targetBranch := cfg.Git.TargetBranch
			if targetBranch == "" {
				targetBranch = github.DefaultBranch
			}
			pr, createErr := ghClient.CreatePR(ctx, github.PRCreateOptions{
				Title: fmt.Sprintf("Story merge: %s", storyID),
				Body:  fmt.Sprintf("Automated merge for story %s", storyID),
				Head:  branchName,
				Base:  targetBranch,
			})
			if createErr != nil {
				return nil, fmt.Errorf("failed to create PR for branch %s: %w", branchName, createErr)
			}
			prRef = fmt.Sprintf("%d", pr.Number)
			d.logger.Info("🔀 PR #%d created successfully", pr.Number)
		} else {
			prRef = fmt.Sprintf("%d", prs[0].Number)
			d.logger.Info("🔀 Found existing PR #%d for branch: %s", prs[0].Number, branchName)
		}
	} else {
		return nil, fmt.Errorf("no PR URL or branch name provided for merge")
	}

	// Attempt merge with squash and delete branch
	mergeResult, err := ghClient.MergePRWithResult(ctx, prRef, github.PRMergeOptions{
		Method:       "squash",
		DeleteBranch: true,
	})
	if err != nil {
		return nil, fmt.Errorf("merge failed: %w", err)
	}

	// Convert to local result type
	result := &MergeAttemptResult{
		HasConflicts: mergeResult.HasConflicts,
		ConflictInfo: mergeResult.ConflictInfo,
		CommitSHA:    mergeResult.SHA,
	}

	if result.HasConflicts {
		d.logger.Warn("🔀 Merge conflicts detected")
	} else {
		d.logger.Info("🔀 Merge completed successfully")
	}

	return result, nil
}

// categorizeMergeError categorizes a merge error into appropriate status and feedback.
func (d *Driver) categorizeMergeError(err error) (proto.ApprovalStatus, string) {
	errorStr := strings.ToLower(err.Error())

	// Recoverable errors (NEEDS_CHANGES) - coder can potentially fix these
	if strings.Contains(errorStr, "conflict") || strings.Contains(errorStr, "merge conflict") {
		return proto.ApprovalStatusNeedsChanges, "Merge conflicts detected. Resolve conflicts and resubmit."
	}
	if strings.Contains(errorStr, "no pull request found") || strings.Contains(errorStr, "could not resolve to a pull request") {
		return proto.ApprovalStatusNeedsChanges, "Pull request not found. Ensure the PR is created and accessible."
	}
	if strings.Contains(errorStr, "permission denied") || strings.Contains(errorStr, "forbidden") {
		return proto.ApprovalStatusNeedsChanges, "Permission denied for merge. Check repository access and branch protection rules."
	}
	if strings.Contains(errorStr, "branch") && (strings.Contains(errorStr, "not found") || strings.Contains(errorStr, "does not exist")) {
		return proto.ApprovalStatusNeedsChanges, "Branch not found. Ensure the branch exists and is pushed to remote."
	}
	if strings.Contains(errorStr, "network") || strings.Contains(errorStr, "timeout") || strings.Contains(errorStr, "connection") {
		return proto.ApprovalStatusNeedsChanges, "Network error during merge. Please retry."
	}
	if strings.Contains(errorStr, "not mergeable") || strings.Contains(errorStr, "cannot be merged") {
		return proto.ApprovalStatusNeedsChanges, "Pull request is not mergeable. Check for conflicts or required status checks."
	}
	if strings.Contains(errorStr, "required status check") || strings.Contains(errorStr, "check") {
		return proto.ApprovalStatusNeedsChanges, "Required status checks not passing. Ensure all checks pass before merge."
	}

	// Unrecoverable errors (REJECTED) - fundamental issues
	if strings.Contains(errorStr, "gh") && strings.Contains(errorStr, "not found") {
		return proto.ApprovalStatusRejected, "GitHub CLI (gh) not available. Cannot perform merge operations."
	}
	if strings.Contains(errorStr, "not a git repository") || strings.Contains(errorStr, "repository") && strings.Contains(errorStr, "not found") {
		return proto.ApprovalStatusRejected, "Git repository not properly configured. Cannot perform merge operations."
	}
	if strings.Contains(errorStr, "authentication failed") && strings.Contains(errorStr, "token") {
		return proto.ApprovalStatusRejected, "GitHub authentication not configured. Cannot access repository."
	}

	// Default to NEEDS_CHANGES for unknown errors (safer to allow retry)
	return proto.ApprovalStatusNeedsChanges, fmt.Sprintf("Merge failed with error: %s. Please investigate and retry.", err.Error())
}
