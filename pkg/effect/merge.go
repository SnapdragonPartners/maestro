package effect

import (
	"context"
	"fmt"
	"time"

	"orchestrator/pkg/git"
	"orchestrator/pkg/proto"
)

// MergeEffect represents a merge request effect that blocks until architect responds with merge result.
type MergeEffect struct {
	StoryID     string        // The story ID for the merge request
	PRUrl       string        // The pull request URL to merge
	BranchName  string        // The branch name to merge
	TargetAgent string        // Target agent (typically "architect")
	Timeout     time.Duration // Timeout for waiting for response
}

// Execute sends a merge request and blocks waiting for the architect's response.
func (e *MergeEffect) Execute(ctx context.Context, runtime Runtime) (any, error) {
	agentID := runtime.GetAgentID()

	// Create REQUEST message with merge payload
	mergeMsg := proto.NewAgentMsg(proto.MsgTypeREQUEST, agentID, e.TargetAgent)
	mergeMsg.SetPayload(proto.KeyKind, string(proto.RequestKindMerge))
	mergeMsg.SetPayload("story_id", e.StoryID)
	mergeMsg.SetPayload("pr_url", e.PRUrl)
	mergeMsg.SetPayload("branch_name", e.BranchName)

	runtime.Info("📤 Sending merge request for story %s to %s", e.StoryID, e.TargetAgent)

	// Send the merge request
	if err := runtime.SendMessage(mergeMsg); err != nil {
		return nil, fmt.Errorf("failed to send merge request: %w", err)
	}

	// Create timeout context
	timeoutCtx, cancel := context.WithTimeout(ctx, e.Timeout)
	defer cancel()

	// Block waiting for RESPONSE message
	runtime.Info("⏳ Waiting for merge response (timeout: %v)", e.Timeout)

	responseMsg, err := runtime.ReceiveMessage(timeoutCtx, proto.MsgTypeRESPONSE)
	if err != nil {
		return nil, fmt.Errorf("failed to receive merge response: %w", err)
	}

	// Extract merge result from response payload
	statusRaw, statusExists := responseMsg.GetPayload("status")
	conflictInfoRaw, _ := responseMsg.GetPayload("conflict_details")
	mergeCommitRaw, _ := responseMsg.GetPayload("merge_commit")

	if !statusExists {
		return nil, fmt.Errorf("merge response missing status field")
	}

	status, ok := statusRaw.(string)
	if !ok {
		return nil, fmt.Errorf("merge status is not a string: %T", statusRaw)
	}

	conflictInfo, _ := conflictInfoRaw.(string)
	mergeCommit, _ := mergeCommitRaw.(string)

	result := &git.MergeResult{
		Status:       status,
		ConflictInfo: conflictInfo,
		MergeCommit:  mergeCommit,
	}

	runtime.Info("📥 Received merge response: %s", result.Status)
	return result, nil
}

// Type returns the effect type identifier.
func (e *MergeEffect) Type() string {
	return "merge"
}

// NewMergeEffect creates a merge effect with default timeout.
func NewMergeEffect(storyID, prURL, branchName string) *MergeEffect {
	return &MergeEffect{
		StoryID:     storyID,
		PRUrl:       prURL,
		BranchName:  branchName,
		TargetAgent: "architect",
		Timeout:     5 * time.Minute, // Default 5 minute timeout
	}
}

// NewMergeEffectWithTimeout creates a merge effect with custom timeout.
func NewMergeEffectWithTimeout(storyID, prURL, branchName string, timeout time.Duration) *MergeEffect {
	return &MergeEffect{
		StoryID:     storyID,
		PRUrl:       prURL,
		BranchName:  branchName,
		TargetAgent: "architect",
		Timeout:     timeout,
	}
}
