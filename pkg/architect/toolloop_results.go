package architect

import (
	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/toolloop"
	"orchestrator/pkg/tools"
)

// SubmitReplyResult contains the response text from a submit_reply tool call.
type SubmitReplyResult struct {
	Response string
}

// ExtractSubmitReply extracts the response from a submit_reply tool call.
// Returns ErrNoTerminalTool if submit_reply was not called.
// Returns ErrInvalidResult if response parameter is missing or empty.
func ExtractSubmitReply(calls []agent.ToolCall, _ []any) (SubmitReplyResult, error) {
	for i := range calls {
		if calls[i].Name == tools.ToolSubmitReply {
			response, ok := calls[i].Parameters["response"].(string)
			if !ok || response == "" {
				return SubmitReplyResult{}, toolloop.ErrInvalidResult
			}
			return SubmitReplyResult{Response: response}, nil
		}
	}
	return SubmitReplyResult{}, toolloop.ErrNoTerminalTool
}

// SpecReviewResult contains the outcome of a spec review (feedback or approval).
//
//nolint:govet // Bool field logically precedes string for semantic grouping
type SpecReviewResult struct {
	Approved bool
	Feedback string
}

// ExtractSpecReview extracts the result from spec review tools (spec_feedback or submit_stories).
// Returns ErrNoTerminalTool if neither tool was called successfully.
// Returns ErrInvalidResult if tool payload is malformed.
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
				return SpecReviewResult{}, toolloop.ErrInvalidResult
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

	return SpecReviewResult{}, toolloop.ErrNoTerminalTool
}

// ReviewCompleteResult contains the outcome of a review_complete tool call.
type ReviewCompleteResult struct {
	Status   string
	Feedback string
}

// ExtractReviewComplete extracts the result from a review_complete tool call.
// Returns ErrNoTerminalTool if review_complete was not called successfully.
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

	return ReviewCompleteResult{}, toolloop.ErrNoTerminalTool
}
