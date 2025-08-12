package coder

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/effect"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/utils"
)

// handleCodeReview processes the CODE_REVIEW state using Effects pattern.
func (c *Coder) handleCodeReview(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	// Get files created and story info for context
	filesCreatedRaw, _ := sm.GetStateValue(KeyFilesCreated)
	originalStory := utils.GetStateValueOr[string](sm, string(stateDataKeyTaskContent), "")
	plan := utils.GetStateValueOr[string](sm, KeyPlan, "")
	storyID := utils.GetStateValueOr[string](sm, KeyStoryID, "")

	var approvalEff *effect.ApprovalEffect

	// Check if files were actually created during implementation
	filesCreated := []string{}
	if files, ok := filesCreatedRaw.([]string); ok {
		filesCreated = files
	}

	if len(filesCreated) == 0 {
		// No files created - request completion approval
		c.logger.Info("🧑‍💻 No files created, requesting story completion approval")

		codeContent := "Story completed during implementation phase: tests passed, no new files needed"
		approvalEff = effect.NewApprovalEffect(codeContent, "Story requirements already satisfied, requesting completion approval", proto.ApprovalTypeCompletion)
		approvalEff.StoryID = storyID
	} else {
		// Files were created - request code approval with file summary
		c.logger.Info("🧑‍💻 Files created during implementation, requesting code review approval")

		filesSummary := strings.Join(filesCreated, ", ")
		codeContent := fmt.Sprintf("Code implementation and testing completed: %d files created (%s), tests passed\n\nOriginal Story:\n%s\n\nImplementation Plan:\n%s", len(filesCreated), filesSummary, originalStory, plan)
		approvalEff = effect.NewApprovalEffect(codeContent, "Code requires architect approval before completion", proto.ApprovalTypeCode)
		approvalEff.StoryID = storyID
	}

	// Execute approval effect - blocks until architect responds
	result, err := c.ExecuteEffect(ctx, approvalEff)
	if err != nil {
		c.logger.Error("🧑‍💻 Approval effect failed: %v", err)
		return proto.StateError, false, logx.Wrap(err, "approval effect failed")
	}

	// Process the approval result
	if approvalResult, ok := result.(*effect.ApprovalResult); ok {
		return c.processApprovalResult(ctx, sm, approvalResult)
	}

	return proto.StateError, false, logx.Errorf("invalid approval result type: %T", result)
}

// processApprovalResult processes the architect's approval response and determines next state.
func (c *Coder) processApprovalResult(_ context.Context, sm *agent.BaseStateMachine, result *effect.ApprovalResult) (proto.State, bool, error) {
	// Store completion timestamp
	sm.SetStateData(KeyCodeReviewCompletedAt, time.Now().UTC())

	// Handle approval based on status
	switch result.Status {
	case proto.ApprovalStatusApproved:
		c.logger.Info("🧑‍💻 Code approved by architect")

		// For completion approvals, go directly to DONE
		// For code approvals, proceed to AWAIT_MERGE
		if strings.Contains(result.Feedback, "completion") || strings.Contains(result.Feedback, "no changes") {
			c.logger.Info("🧑‍💻 Completion approved - story finished successfully")
			return proto.StateDone, false, nil
		}

		c.logger.Info("🧑‍💻 Code approved - proceeding to merge")
		return StateAwaitMerge, false, nil

	case proto.ApprovalStatusNeedsChanges:
		c.logger.Info("🧑‍💻 Code needs changes: %s", result.Feedback)

		// Store feedback for CODING state to address
		sm.SetStateData(KeyCodeReviewRejectionFeedback, result.Feedback)
		return StateCoding, false, nil

	case proto.ApprovalStatusRejected:
		c.logger.Error("🧑‍💻 Code rejected by architect: %s", result.Feedback)
		return proto.StateError, false, logx.Errorf("code rejected by architect: %s", result.Feedback)

	default:
		return proto.StateError, false, logx.Errorf("unknown approval status: %s", result.Status)
	}
}

// convertApprovalData converts various approval data formats to ApprovalResult.
func convertApprovalData(data any) (*proto.ApprovalResult, error) {
	// If data is nil or empty, return error indicating no approval data.
	if data == nil {
		return nil, logx.Errorf("no approval data available")
	}

	// If it's already the correct type, return it.
	if result, ok := data.(*proto.ApprovalResult); ok {
		return result, nil
	}

	// If it's a map (from JSON deserialization), convert it.
	if dataMap, ok := data.(map[string]any); ok {
		// Convert map to JSON and then to struct.
		jsonData, err := json.Marshal(dataMap)
		if err != nil {
			return nil, logx.Wrap(err, "failed to marshal approval data")
		}

		var result proto.ApprovalResult
		if err := json.Unmarshal(jsonData, &result); err != nil {
			return nil, logx.Wrap(err, "failed to unmarshal approval data")
		}

		return &result, nil
	}

	// If it's a string (from cleanup or serialization), handle appropriately.
	if str, ok := data.(string); ok {
		// Empty string means no approval result (from cleanup).
		if str == "" {
			return nil, logx.Errorf("no approval data available")
		}
		// Non-empty string might be JSON-serialized approval result.
		var result proto.ApprovalResult
		if err := json.Unmarshal([]byte(str), &result); err != nil {
			return nil, logx.Wrap(err, "failed to unmarshal approval data from string")
		}
		return &result, nil
	}

	return nil, logx.Errorf("unsupported approval data type: %T", data)
}
