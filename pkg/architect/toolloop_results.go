package architect

import (
	"fmt"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/tools"
)

// SubmitReplyResult contains the response text from a submit_reply tool call.
type SubmitReplyResult struct {
	Response string
}

// ExtractSubmitReply extracts the response from a submit_reply tool call.
// Returns error if submit_reply was not called or response is empty.
func ExtractSubmitReply(calls []agent.ToolCall, _ []any) (SubmitReplyResult, error) {
	for i := range calls {
		if calls[i].Name == tools.ToolSubmitReply {
			response, ok := calls[i].Parameters["response"].(string)
			if !ok || response == "" {
				return SubmitReplyResult{}, fmt.Errorf("submit_reply called without valid response parameter")
			}
			return SubmitReplyResult{Response: response}, nil
		}
	}
	return SubmitReplyResult{}, fmt.Errorf("submit_reply tool was not called")
}

// SpecReviewResult contains the outcome of a spec review (feedback or approval).
//
//nolint:govet // Bool field logically precedes string for semantic grouping
type SpecReviewResult struct {
	Approved bool
	Feedback string
}

// ExtractSpecReview extracts the result from spec review tools (spec_feedback or submit_stories).
// Returns error if neither tool was called successfully.
func ExtractSpecReview(calls []agent.ToolCall, results []any) (SpecReviewResult, error) {
	for i := range calls {
		toolCall := &calls[i]

		// Check for spec_feedback (rejection/changes requested)
		if toolCall.Name == tools.ToolSpecFeedback {
			// Check if tool executed successfully from results
			resultMap, ok := results[i].(map[string]any)
			if !ok {
				continue
			}

			success, _ := resultMap["success"].(bool)
			if !success {
				continue
			}

			feedback, ok := resultMap["feedback"].(string)
			if !ok || feedback == "" {
				return SpecReviewResult{}, fmt.Errorf("spec_feedback result missing feedback field")
			}

			return SpecReviewResult{
				Approved: false,
				Feedback: feedback,
			}, nil
		}

		// Check for submit_stories (approval)
		if toolCall.Name == tools.ToolSubmitStories {
			// Check if tool executed successfully from results
			resultMap, ok := results[i].(map[string]any)
			if !ok {
				continue
			}

			success, _ := resultMap["success"].(bool)
			if !success {
				continue
			}

			return SpecReviewResult{
				Approved: true,
				Feedback: "Spec approved and stories submitted",
			}, nil
		}
	}

	return SpecReviewResult{}, fmt.Errorf("neither spec_feedback nor submit_stories tool was called successfully")
}

// ReviewCompleteResult contains the outcome of a review_complete tool call.
type ReviewCompleteResult struct {
	Status   string
	Feedback string
}

// ExtractReviewComplete extracts the result from a review_complete tool call.
// Returns error if review_complete was not called successfully.
func ExtractReviewComplete(calls []agent.ToolCall, results []any) (ReviewCompleteResult, error) {
	for i := range calls {
		if calls[i].Name != tools.ToolReviewComplete {
			continue
		}

		// Check if tool executed successfully from results
		resultMap, ok := results[i].(map[string]any)
		if !ok {
			continue
		}

		success, _ := resultMap["success"].(bool)
		if !success {
			continue
		}

		status, _ := resultMap["status"].(string)
		feedback, _ := resultMap["feedback"].(string)

		return ReviewCompleteResult{
			Status:   status,
			Feedback: feedback,
		}, nil
	}

	return ReviewCompleteResult{}, fmt.Errorf("review_complete tool was not called successfully")
}
