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

// SubmitStoriesResult contains the outcome of a submit_stories tool call.
type SubmitStoriesResult struct {
	Success bool
}

// ExtractSubmitStories extracts the result from a submit_stories tool call.
// Returns ErrNoTerminalTool if submit_stories was not called successfully.
func ExtractSubmitStories(calls []agent.ToolCall, results []any) (SubmitStoriesResult, error) {
	for i := range calls {
		if calls[i].Name != tools.ToolSubmitStories {
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

		return SubmitStoriesResult{Success: true}, nil
	}

	return SubmitStoriesResult{}, toolloop.ErrNoTerminalTool
}
