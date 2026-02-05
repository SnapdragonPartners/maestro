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

	// Build merge request payload
	payload := &proto.MergeRequestPayload{
		StoryID:    e.StoryID,
		BranchName: e.BranchName,
		PRURL:      e.PRUrl,
	}

	// Set typed payload
	mergeMsg.SetTypedPayload(proto.NewMergeRequestPayload(payload))

	// Store story_id in message metadata for tracking
	if e.StoryID != "" {
		mergeMsg.SetMetadata("story_id", e.StoryID)
	}

	runtime.Info("📤 Sending merge request for story %s to %s", e.StoryID, e.TargetAgent)

	// Send the merge request
	if err := runtime.SendMessage(mergeMsg); err != nil {
		return nil, fmt.Errorf("failed to send merge request: %w", err)
	}

	// Create timeout context
	timeoutCtx, cancel := context.WithTimeout(ctx, e.Timeout)
	defer cancel()

	// Block waiting for RESPONSE message with correlation check
	runtime.Info("⏳ Waiting for merge response (timeout: %v)", e.Timeout)

	// Loop to drain stale responses and wait for the correct one
	var responseMsg *proto.AgentMsg
	for {
		var err error
		responseMsg, err = runtime.ReceiveMessage(timeoutCtx, proto.MsgTypeRESPONSE)
		if err != nil {
			return nil, fmt.Errorf("failed to receive merge response: %w", err)
		}

		// Verify response correlation - ParentMsgID should match our request ID
		if responseMsg.ParentMsgID != mergeMsg.ID {
			runtime.Info("⚠️ Discarding stale response (ParentMsgID=%s, expected=%s) - waiting for correct response",
				responseMsg.ParentMsgID, mergeMsg.ID)
			continue // Keep waiting for the correct response
		}
		break // Found the correct response
	}

	// Extract merge result from response payload
	typedPayload := responseMsg.GetTypedPayload()
	if typedPayload == nil {
		return nil, fmt.Errorf("merge response missing typed payload")
	}

	mergeResponse, err := typedPayload.ExtractMergeResponse()
	if err != nil {
		return nil, fmt.Errorf("failed to extract merge response: %w", err)
	}

	result := &git.MergeResult{
		Status:       mergeResponse.Status,
		ConflictInfo: mergeResponse.ConflictDetails,
		MergeCommit:  mergeResponse.MergeCommit,
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
