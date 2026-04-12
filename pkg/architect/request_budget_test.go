package architect

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orchestrator/pkg/proto"
)

// TestBuildApprovalResponse_BudgetReview_PreservesEmptyFeedback verifies that budget reviews
// with empty feedback keep it empty (so the coder-side no-op check works).
func TestBuildApprovalResponse_BudgetReview_PreservesEmptyFeedback(t *testing.T) {
	driver := newTestDriver()

	// Create a request message with budget review payload
	requestMsg := proto.NewAgentMsg(proto.MsgTypeREQUEST, "coder-001", "architect-001")
	proto.SetStoryID(requestMsg, "story-123")
	approvalPayload := &proto.ApprovalRequestPayload{
		ApprovalType: proto.ApprovalTypeBudgetReview,
		Content:      "Budget review request",
	}
	requestMsg.SetTypedPayload(proto.NewApprovalRequestPayload(approvalPayload))

	response, err := driver.buildApprovalResponseFromReviewComplete(
		context.Background(), requestMsg, approvalPayload, reviewStatusApproved, "")
	require.NoError(t, err)

	// Extract the approval result from the response
	typedPayload := response.GetTypedPayload()
	require.NotNil(t, typedPayload)
	approvalResponse, err := typedPayload.ExtractApprovalResponse()
	require.NoError(t, err)

	// Empty feedback should be preserved for budget reviews
	assert.Empty(t, approvalResponse.Feedback, "budget review empty feedback should be preserved as empty")
}

// TestBuildApprovalResponse_CodeReview_FillsPlaceholder verifies that non-budget reviews
// with empty feedback get the generic placeholder.
func TestBuildApprovalResponse_CodeReview_FillsPlaceholder(t *testing.T) {
	driver := newTestDriver()

	requestMsg := proto.NewAgentMsg(proto.MsgTypeREQUEST, "coder-001", "architect-001")
	proto.SetStoryID(requestMsg, "story-123")
	approvalPayload := &proto.ApprovalRequestPayload{
		ApprovalType: proto.ApprovalTypeCode,
		Content:      "Code review request",
	}
	requestMsg.SetTypedPayload(proto.NewApprovalRequestPayload(approvalPayload))

	response, err := driver.buildApprovalResponseFromReviewComplete(
		context.Background(), requestMsg, approvalPayload, reviewStatusApproved, "")
	require.NoError(t, err)

	typedPayload := response.GetTypedPayload()
	require.NotNil(t, typedPayload)
	approvalResponse, err := typedPayload.ExtractApprovalResponse()
	require.NoError(t, err)

	assert.Equal(t, "Review completed via single-turn review", approvalResponse.Feedback,
		"non-budget review empty feedback should get placeholder")
}

// TestBuildApprovalResponse_BudgetReview_NonEmptyFeedbackPreserved verifies that budget reviews
// with non-empty feedback pass it through unchanged.
func TestBuildApprovalResponse_BudgetReview_NonEmptyFeedbackPreserved(t *testing.T) {
	driver := newTestDriver()

	requestMsg := proto.NewAgentMsg(proto.MsgTypeREQUEST, "coder-001", "architect-001")
	proto.SetStoryID(requestMsg, "story-123")
	approvalPayload := &proto.ApprovalRequestPayload{
		ApprovalType: proto.ApprovalTypeBudgetReview,
		Content:      "Budget review request",
	}
	requestMsg.SetTypedPayload(proto.NewApprovalRequestPayload(approvalPayload))

	feedback := "The work is complete. Call the done tool now."
	response, err := driver.buildApprovalResponseFromReviewComplete(
		context.Background(), requestMsg, approvalPayload, reviewStatusApproved, feedback)
	require.NoError(t, err)

	typedPayload := response.GetTypedPayload()
	require.NotNil(t, typedPayload)
	approvalResponse, err := typedPayload.ExtractApprovalResponse()
	require.NoError(t, err)

	assert.Equal(t, feedback, approvalResponse.Feedback,
		"budget review non-empty feedback should be passed through")
}

// TestBuildApprovalResponse_CompletionReview_FillsPlaceholder verifies that completion reviews
// also get the placeholder on empty feedback.
func TestBuildApprovalResponse_CompletionReview_FillsPlaceholder(t *testing.T) {
	driver := newTestDriver()

	requestMsg := proto.NewAgentMsg(proto.MsgTypeREQUEST, "coder-001", "architect-001")
	proto.SetStoryID(requestMsg, "story-123")
	approvalPayload := &proto.ApprovalRequestPayload{
		ApprovalType: proto.ApprovalTypeCompletion,
		Content:      "Completion request",
	}
	requestMsg.SetTypedPayload(proto.NewApprovalRequestPayload(approvalPayload))

	response, err := driver.buildApprovalResponseFromReviewComplete(
		context.Background(), requestMsg, approvalPayload, reviewStatusApproved, "")
	require.NoError(t, err)

	typedPayload := response.GetTypedPayload()
	require.NotNil(t, typedPayload)
	approvalResponse, err := typedPayload.ExtractApprovalResponse()
	require.NoError(t, err)

	assert.Equal(t, "Review completed via single-turn review", approvalResponse.Feedback,
		"completion review empty feedback should get placeholder")
}
